package repo_test

import (
	"net/http"
	"testing"

	"github.com/labspangaea/go-lib/db/repo"
)

func TestOffsetParamsFromRequest_Defaults(t *testing.T) {
	r, _ := http.NewRequest("GET", "/items", nil)
	p := repo.OffsetParamsFromRequest(r, 20)
	if p.Limit != 20 {
		t.Errorf("Limit = %d, want 20 (default)", p.Limit)
	}
	if p.Offset != 0 {
		t.Errorf("Offset = %d, want 0 (default)", p.Offset)
	}
}

func TestOffsetParamsFromRequest_Explicit(t *testing.T) {
	r, _ := http.NewRequest("GET", "/items?limit=5&offset=15", nil)
	p := repo.OffsetParamsFromRequest(r, 20)
	if p.Limit != 5 {
		t.Errorf("Limit = %d, want 5", p.Limit)
	}
	if p.Offset != 15 {
		t.Errorf("Offset = %d, want 15", p.Offset)
	}
}

func TestOffsetParamsFromRequest_NegativeOffset_ClampedToZero(t *testing.T) {
	r, _ := http.NewRequest("GET", "/items?offset=-5", nil)
	p := repo.OffsetParamsFromRequest(r, 20)
	if p.Offset != 0 {
		t.Errorf("Offset = %d, want 0 (clamped from negative)", p.Offset)
	}
}

func TestOffsetParamsFromRequest_NonPositiveLimit_FallsBackToDefault(t *testing.T) {
	r, _ := http.NewRequest("GET", "/items?limit=0", nil)
	p := repo.OffsetParamsFromRequest(r, 20)
	if p.Limit != 20 {
		t.Errorf("Limit = %d, want 20 (fallback for non-positive)", p.Limit)
	}
}

func TestOffsetParamsFromRequest_NonNumeric_FallsBackToDefaults(t *testing.T) {
	r, _ := http.NewRequest("GET", "/items?limit=abc&offset=xyz", nil)
	p := repo.OffsetParamsFromRequest(r, 20)
	if p.Limit != 20 {
		t.Errorf("Limit = %d, want 20", p.Limit)
	}
	if p.Offset != 0 {
		t.Errorf("Offset = %d, want 0", p.Offset)
	}
}

func TestOffsetParamsFromRequest_LimitCappedAtMaxLimit(t *testing.T) {
	r, _ := http.NewRequest("GET", "/items?limit=10000000", nil)
	p := repo.OffsetParamsFromRequest(r, 20)
	if p.Limit != 1000 {
		t.Errorf("Limit = %d, want 1000 (capped at maxLimit)", p.Limit)
	}
}

func TestOffsetParamsFromRequest_LimitAtMaxLimitBoundary(t *testing.T) {
	r, _ := http.NewRequest("GET", "/items?limit=1000", nil)
	p := repo.OffsetParamsFromRequest(r, 20)
	if p.Limit != 1000 {
		t.Errorf("Limit = %d, want 1000 (exact boundary, must not be capped)", p.Limit)
	}
}

func TestOffsetParamsFromRequest_LimitBelowMaxLimit_Unchanged(t *testing.T) {
	r, _ := http.NewRequest("GET", "/items?limit=50", nil)
	p := repo.OffsetParamsFromRequest(r, 20)
	if p.Limit != 50 {
		t.Errorf("Limit = %d, want 50 (below maxLimit, must not be altered)", p.Limit)
	}
}
