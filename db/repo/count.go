package repo

import (
	"fmt"

	"gorm.io/gorm"
)

// CountStrategy performs a COUNT query on top of a pre-filtered *gorm.DB scope.
// Opt-in only — cursor pagination never needs a count (uses limit+1 trick instead).
type CountStrategy interface {
	Count(db *gorm.DB) (int64, error)
}

type exactCount struct{}

// ExactCount issues SELECT COUNT(1) — correct on both MySQL and PostgreSQL.
// Use only when the API contract requires a total count (e.g. admin dashboards).
func ExactCount() CountStrategy { return exactCount{} }

func (exactCount) Count(db *gorm.DB) (int64, error) {
	var n int64
	if err := db.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("repo: exact count: %w", err)
	}
	return n, nil
}

type approxCount struct{ tableName string }

// ApproxCount reads the row estimate from pg_class — O(1), no table scan.
// PostgreSQL only. The value is stale until the next ANALYZE; suitable for
// display totals only, never for billing or compliance.
func ApproxCount(tableName string) CountStrategy { return approxCount{tableName: tableName} }

func (a approxCount) Count(db *gorm.DB) (int64, error) {
	var n int64
	err := db.Raw(
		"SELECT reltuples::bigint FROM pg_class WHERE relname = ?",
		a.tableName,
	).Scan(&n).Error
	if err != nil {
		return 0, fmt.Errorf("repo: approx count %s: %w", a.tableName, err)
	}
	return n, nil
}
