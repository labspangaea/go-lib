package repo

import (
	"net/http"
	"strconv"
)

// OffsetParams carries the offset/limit pagination state for a single List call.
//
// Prefer CursorParams for large datasets — keyset pagination scales without the
// OFFSET-N performance cliff. Use OffsetParams when the client genuinely needs
// random-access positioning ("jump to row 100") or when total-count semantics
// (HasNext, Total) are required by the API contract.
type OffsetParams struct {
	// Offset is the number of rows to skip. Must be >= 0; negative inputs are clamped to 0.
	Offset int
	// Limit is the maximum number of results to return. Must be > 0; non-positive
	// inputs fall back to defaultLimit.
	Limit int
}

// NewOffsetParams builds an OffsetParams directly from already-parsed values.
// Use this when the inputs come from a non-http source (huma input struct,
// gRPC, CLI). Negative offsets clamp to 0; limit <= 0 falls back to 20.
func NewOffsetParams(offset, limit int) OffsetParams {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	return OffsetParams{Offset: offset, Limit: limit}
}

// OffsetParamsFromRequest reads offset and limit from r.URL.Query().
// defaultLimit is used when the "limit" param is absent or non-positive.
// Negative offsets are clamped to 0; non-numeric inputs fall back to defaults.
func OffsetParamsFromRequest(r *http.Request, defaultLimit int) OffsetParams {
	q := r.URL.Query()
	p := OffsetParams{Limit: defaultLimit}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 {
		if l > maxLimit {
			l = maxLimit
		}
		p.Limit = l
	}
	if o, err := strconv.Atoi(q.Get("offset")); err == nil && o > 0 {
		p.Offset = o
	}
	return p
}
