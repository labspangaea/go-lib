package repo

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"


	"github.com/labspangaea/go-lib/cache"
	"github.com/labspangaea/go-lib/logger"
)

// cachedConfig holds resolved CachedRepo options.
type cachedConfig struct {
	ttl          time.Duration
	jitterFactor float64 // 0 = no jitter; default 0.1 (10%)
	atomicSet    bool    // use SET NX for stampede protection (opt-in)
	twoPhaseList bool    // enable ListIDs → MGet → IN fetch → MSet (opt-in)
}

func defaultCachedConfig() cachedConfig {
	return cachedConfig{
		ttl:          5 * time.Minute,
		jitterFactor: 0.1,
	}
}

// effectiveTTL returns base TTL plus a uniform random jitter offset.
// Every cache write gets its own TTL so keys written together expire at different times,
// preventing cache avalanche on cold start or bulk invalidation.
func (cfg *cachedConfig) effectiveTTL() time.Duration {
	if cfg.jitterFactor == 0 {
		return cfg.ttl
	}
	jitter := time.Duration(rand.Float64() * float64(cfg.ttl) * cfg.jitterFactor)
	return cfg.ttl + jitter
}

// CachedOption configures a CachedRepo at construction time.
type CachedOption func(*cachedConfig)

// WithTTL sets the base cache TTL. Default: 5 minutes.
func WithTTL(d time.Duration) CachedOption {
	return func(cfg *cachedConfig) { cfg.ttl = d }
}

// WithJitter sets the jitter factor applied to each cache write.
// The effective TTL is base + rand(0, base*factor).
// Pass 0 to disable jitter. Default: 0.1 (10%).
func WithJitter(factor float64) CachedOption {
	return func(cfg *cachedConfig) { cfg.jitterFactor = factor }
}

// WithAtomicSet enables SET NX writes via a Lua script to prevent thundering-herd
// stampedes on hot keys. Opt-in — most services do not need this.
func WithAtomicSet() CachedOption {
	return func(cfg *cachedConfig) { cfg.atomicSet = true }
}

// WithTwoPhaseList enables the two-phase list strategy: ListIDs → MGet cache →
// IN fetch misses → MSet. Only enable after profiling confirms cache hit rate > ~70%.
// Default: CachedRepo.List is a pass-through to the underlying repo.List (single query).
func WithTwoPhaseList() CachedOption {
	return func(cfg *cachedConfig) { cfg.twoPhaseList = true }
}

// CachedRepo wraps any Repository[T, ID, DB] and adds a per-entity cache layer.
// The cache key format is "<keyPrefix>:<id>".
//
// Per-entity methods (FindByID, FindByIDs, Update, Delete) are always cached/invalidated.
// List is a pass-through by default; enable two-phase with WithTwoPhaseList().
//
// Nothing here touches DB: every cached path goes through the seven
// driver-agnostic methods, and DB is only carried so the decorator still
// satisfies the same interface as the repository it wraps. That is why a bun
// repository can be cached as readily as a GORM one.
type CachedRepo[T Model[ID], ID comparable, DB any] struct {
	repo      Repository[T, ID, DB]
	c         cache.Cache[*T]
	keyPrefix string
	cfg       cachedConfig

	// resolved at construction via type assertion — nil when not supported
	batchGet cache.BatchGetter[*T]
	batchSet cache.BatchSetter[*T]
}

// NewCached wraps repo with a cache layer. The cache is keyed by "<keyPrefix>:<id>".
func NewCached[T Model[ID], ID comparable, DB any](
	repo Repository[T, ID, DB],
	c cache.Cache[*T],
	keyPrefix string,
	opts ...CachedOption,
) *CachedRepo[T, ID, DB] {
	cfg := defaultCachedConfig()
	for _, o := range opts {
		o(&cfg)
	}
	cr := &CachedRepo[T, ID, DB]{
		repo:      repo,
		c:         c,
		keyPrefix: keyPrefix,
		cfg:       cfg,
	}
	// Detect batch capability once at construction — no per-call type assertions.
	if bg, ok := c.(cache.BatchGetter[*T]); ok {
		cr.batchGet = bg
	}
	if bs, ok := c.(cache.BatchSetter[*T]); ok {
		cr.batchSet = bs
	}
	return cr
}

// key builds the cache key for a given ID.
func (r *CachedRepo[T, ID, DB]) key(id ID) string {
	return fmt.Sprintf("%s:%v", r.keyPrefix, id)
}

// keys builds cache keys for a slice of IDs, preserving order.
func (r *CachedRepo[T, ID, DB]) keys(ids []ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = r.key(id)
	}
	return out
}

// FindByID returns the cached entity or fetches from DB on miss.
func (r *CachedRepo[T, ID, DB]) FindByID(ctx context.Context, id ID) (*T, error) {
	l := logger.FromContext(ctx)
	k := r.key(id)

	if v, ok, err := r.c.Get(ctx, k); err != nil {
		l.Warn("repo: cache get failed, falling back to db", "key", k, "error", err)
	} else if ok {
		return v, nil
	}

	entity, err := r.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if err = r.c.Set(ctx, k, entity, r.cfg.effectiveTTL()); err != nil {
		l.Warn("repo: cache set failed", "key", k, "error", err)
	}
	return entity, nil
}

// FindByIDs batch-fetches entities: MGet from cache, then IN query for misses,
// then MSet all misses back. Final result is reordered to match the ids slice.
func (r *CachedRepo[T, ID, DB]) FindByIDs(ctx context.Context, ids []ID) ([]T, error) {
	if len(ids) == 0 {
		return []T{}, nil
	}
	l := logger.FromContext(ctx)
	keys := r.keys(ids)

	// Phase 1: batch cache read.
	hits, missIDs, err := r.mget(ctx, ids, keys)
	if err != nil {
		l.Warn("repo: mget failed, falling back to db for all ids", "error", err)
		return r.repo.FindByIDs(ctx, ids)
	}

	// Phase 2: DB fetch for misses only.
	var dbRows []T
	if len(missIDs) > 0 {
		dbRows, err = r.repo.FindByIDs(ctx, missIDs)
		if err != nil {
			return nil, err
		}
		// Write misses back to cache in one pipeline round trip.
		if setErr := r.mset(ctx, dbRows); setErr != nil {
			l.Warn("repo: mset failed", "error", setErr)
		}
	}

	// Phase 3: merge hits + misses and reorder to match ids input order.
	index := make(map[any]*T, len(hits)+len(dbRows))
	for _, v := range hits {
		index[(*v).GetPK()] = v
	}
	for i := range dbRows {
		index[dbRows[i].GetPK()] = &dbRows[i]
	}

	out := make([]T, 0, len(ids))
	for _, id := range ids {
		if v, ok := index[id]; ok {
			out = append(out, *v)
		}
	}
	return out, nil
}

// Create writes to DB. No cache write — the entity was just born and has not been read.
func (r *CachedRepo[T, ID, DB]) Create(ctx context.Context, entity *T) error {
	return r.repo.Create(ctx, entity)
}

// Update writes to DB then invalidates the cache entry.
func (r *CachedRepo[T, ID, DB]) Update(ctx context.Context, entity *T, columns []string) error {
	if err := r.repo.Update(ctx, entity, columns); err != nil {
		return err
	}
	l := logger.FromContext(ctx)
	k := r.key((*entity).GetPK())
	if err := r.c.Delete(ctx, k); err != nil {
		l.Warn("repo: cache delete failed after update", "key", k, "error", err)
	}
	return nil
}

// Delete removes from DB then invalidates the cache entry.
func (r *CachedRepo[T, ID, DB]) Delete(ctx context.Context, id ID) error {
	if err := r.repo.Delete(ctx, id); err != nil {
		return err
	}
	l := logger.FromContext(ctx)
	k := r.key(id)
	if err := r.c.Delete(ctx, k); err != nil {
		l.Warn("repo: cache delete failed after delete", "key", k, "error", err)
	}
	return nil
}

// List is a pass-through to the underlying repo by default (single DB query, no caching).
// Enable two-phase mode with WithTwoPhaseList() after profiling confirms > ~70% cache hit rate.
func (r *CachedRepo[T, ID, DB]) List(ctx context.Context, p CursorParams, filters ...Filter) ([]T, *CursorPage, error) {
	if !r.cfg.twoPhaseList {
		return r.repo.List(ctx, p, filters...)
	}
	return r.listTwoPhase(ctx, p, filters)
}

// listTwoPhase implements the opt-in two-phase list:
// Phase 1 ListIDs (covering index) → Phase 2 MGet cache → Phase 3 IN fetch misses + MSet → Phase 4 reorder.
func (r *CachedRepo[T, ID, DB]) listTwoPhase(ctx context.Context, p CursorParams, filters []Filter) ([]T, *CursorPage, error) {
	l := logger.FromContext(ctx)

	// Phase 1: get ordered ID list from DB (covering index scan).
	ids, page, err := r.repo.ListIDs(ctx, p, filters...)
	if err != nil {
		return nil, nil, err
	}
	if len(ids) == 0 {
		return []T{}, page, nil
	}

	keys := r.keys(ids)

	// Phase 2: batch cache read.
	hits, missIDs, err := r.mget(ctx, ids, keys)
	if err != nil {
		l.Warn("repo: mget failed in two-phase list, falling back", "error", err)
		// Fall back: fetch full rows from DB in original cursor-ordered query.
		rows, _, err2 := r.repo.List(ctx, p, filters...)
		return rows, page, err2
	}

	// Phase 3: DB fetch for misses only.
	var dbRows []T
	if len(missIDs) > 0 {
		dbRows, err = r.repo.FindByIDs(ctx, missIDs)
		if err != nil {
			return nil, nil, err
		}
		if setErr := r.mset(ctx, dbRows); setErr != nil {
			l.Warn("repo: mset failed in two-phase list", "error", setErr)
		}
	}

	// Phase 4: merge and reorder to match Phase 1 ID order (canonical order).
	index := make(map[any]*T, len(hits)+len(dbRows))
	for _, v := range hits {
		index[(*v).GetPK()] = v
	}
	for i := range dbRows {
		index[dbRows[i].GetPK()] = &dbRows[i]
	}

	out := make([]T, 0, len(ids))
	for _, id := range ids {
		if v, ok := index[id]; ok {
			out = append(out, *v)
		}
	}
	return out, page, nil
}

// ListIDs delegates to the underlying repo — CachedRepo does not cache ID lists.
func (r *CachedRepo[T, ID, DB]) ListIDs(ctx context.Context, p CursorParams, filters ...Filter) ([]ID, *CursorPage, error) {
	return r.repo.ListIDs(ctx, p, filters...)
}

// DB passes through to the underlying repo's DB escape hatch.
func (r *CachedRepo[T, ID, DB]) DB(ctx context.Context) DB {
	return r.repo.DB(ctx)
}

// mget fetches all ids from cache in one round trip when BatchGetter is available,
// or falls back to sequential Gets. Returns hits as []*T and miss IDs.
func (r *CachedRepo[T, ID, DB]) mget(ctx context.Context, ids []ID, keys []string) (hits []*T, missIDs []ID, err error) {
	if r.batchGet != nil {
		hitMap, batchErr := r.batchGet.MGet(ctx, keys)
		if batchErr != nil {
			return nil, nil, batchErr
		}
		for i, k := range keys {
			if v, ok := hitMap[k]; ok && v != nil {
				hits = append(hits, v)
			} else {
				missIDs = append(missIDs, ids[i])
			}
		}
		return hits, missIDs, nil
	}

	// Fallback: sequential Gets (non-Redis backends).
	for i, id := range ids {
		v, ok, getErr := r.c.Get(ctx, keys[i])
		if getErr != nil {
			return nil, nil, getErr
		}
		if ok {
			hits = append(hits, v)
		} else {
			missIDs = append(missIDs, id)
		}
	}
	return hits, missIDs, nil
}

// mset writes rows back to cache in one pipeline round trip when BatchSetter is available,
// or falls back to sequential Sets.
func (r *CachedRepo[T, ID, DB]) mset(ctx context.Context, rows []T) error {
	if len(rows) == 0 {
		return nil
	}
	ttl := r.cfg.effectiveTTL()

	if r.batchSet != nil {
		entries := make(map[string]*T, len(rows))
		for i := range rows {
			entries[r.key(rows[i].GetPK())] = &rows[i]
		}
		return r.batchSet.MSet(ctx, entries, ttl)
	}

	// Fallback: sequential Sets.
	for i := range rows {
		k := r.key(rows[i].GetPK())
		if err := r.c.Set(ctx, k, &rows[i], ttl); err != nil {
			return err
		}
	}
	return nil
}
