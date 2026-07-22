package repo

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// maxLimit is the upper bound on the limit query parameter accepted from HTTP
// requests. Requests exceeding this value are silently capped to prevent trivial
// single-request DoS via oversized result sets.
const maxLimit = 1000

// validColumn matches safe SQL identifier names: starts with a letter or underscore,
// followed by letters, digits, or underscores. Rejects any input containing SQL
// metacharacters, spaces, or injection sequences.
var validColumn = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// SortKey describes a single ORDER BY expression.
type SortKey struct {
	Column string
	Desc   bool
}

// orderClause returns the SQL ORDER BY fragment, e.g. "created_at DESC".
func (s SortKey) orderClause() string {
	if s.Desc {
		return s.Column + " DESC"
	}
	return s.Column + " ASC"
}

// Asc returns a SortKey for ascending order on col.
func Asc(col string) SortKey { return SortKey{Column: col} }

// Descending returns a SortKey for descending order on col.
// Named Descending (not Desc) to avoid shadowing the SortKey.Desc field at call sites.
func Descending(col string) SortKey { return SortKey{Column: col, Desc: true} }

// CursorParams carries the pagination state for a single List call.
type CursorParams struct {
	// Cursor is the opaque token from the previous page. Empty means first page.
	Cursor string
	// Limit is the maximum number of results to return. Must be > 0.
	Limit int
	// OrderBy specifies one or more sort expressions. Order matters — the first key
	// is the primary sort, subsequent keys break ties.
	OrderBy []SortKey
}

// CursorPage is the pagination metadata returned alongside a list result.
type CursorPage struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasNext    bool   `json:"has_next"`
	Limit      int    `json:"limit"`
}

// cursorPayload stores per-column raw JSON values — preserves original int64/string/float types.
type cursorPayload struct {
	Values map[string]json.RawMessage `json:"v"`
}

// encodeCursor encodes all ORDER BY column values into an opaque base64-JSON cursor.
// Encoding all sort key values (not just PK) enables correct keyset WHERE conditions
// for any ORDER BY, including multi-column DESC/ASC mixes.
func encodeCursor(values map[string]any) (string, error) {
	raw := make(map[string]json.RawMessage, len(values))
	for k, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("repo: encode cursor %s: %w", k, err)
		}
		raw[k] = b
	}
	b, err := json.Marshal(cursorPayload{Values: raw})
	if err != nil {
		return "", fmt.Errorf("repo: encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeCursor decodes an opaque cursor back to its per-column raw JSON values.
func decodeCursor(cursor string) (map[string]json.RawMessage, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("repo: decode cursor: %w", err)
	}
	var p cursorPayload
	if err = json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("repo: decode cursor: %w", err)
	}
	return p.Values, nil
}

// unmarshalCursorValue decodes a raw JSON cursor value into a Go value safe to pass
// as a GORM WHERE argument. Uses json.Number to avoid float64 truncation for large
// int64 timestamps or numeric primary keys (JSON numbers default to float64 otherwise).
func unmarshalCursorValue(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var val any
	if err := dec.Decode(&val); err != nil {
		return nil, err
	}
	if n, ok := val.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i, nil
		}
		return n.Float64()
	}
	return val, nil
}

// NewCursorParams builds a CursorParams directly from already-parsed values.
// Use this when the inputs come from a non-http source (huma input struct,
// gRPC, CLI). limit <= 0 falls back to a safe default of 20 so callers never
// hit the "limit must be > 0" guard inside the repo.
func NewCursorParams(cursor string, limit int, sorts ...SortKey) CursorParams {
	if limit <= 0 {
		limit = 20
	}
	return CursorParams{
		Cursor:  cursor,
		Limit:   limit,
		OrderBy: sorts,
	}
}

// CursorParamsFromRequest reads cursor, limit, and order_by from r.URL.Query().
// defaultLimit is used when the "limit" param is absent or non-positive.
// defaultOrderBy provides fallback sort order when the "order_by" param is absent.
//
// order_by query param format: "created_at:desc,id:asc"
func CursorParamsFromRequest(r *http.Request, defaultLimit int, defaultOrderBy ...SortKey) CursorParams {
	q := r.URL.Query()
	p := CursorParams{
		Cursor:  q.Get("cursor"),
		Limit:   defaultLimit,
		OrderBy: defaultOrderBy,
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
		if l > maxLimit {
			l = maxLimit
		}
		p.Limit = l
	}
	if ob := q.Get("order_by"); ob != "" {
		if parsed := parseSortKeys(ob); len(parsed) > 0 {
			p.OrderBy = parsed
		}
	}
	return p
}

// parseSortKeys parses "created_at:desc,id:asc" into a []SortKey slice.
// Columns without a direction suffix default to ascending.
func parseSortKeys(s string) []SortKey {
	parts := strings.Split(s, ",")
	out := make([]SortKey, 0, len(parts))
	for _, part := range parts {
		col, dir, _ := strings.Cut(strings.TrimSpace(part), ":")
		col = strings.TrimSpace(col)
		if col == "" {
			continue
		}
		if !validColumn.MatchString(col) {
			continue
		}
		out = append(out, SortKey{
			Column: col,
			Desc:   strings.EqualFold(strings.TrimSpace(dir), "desc"),
		})
	}
	return out
}
