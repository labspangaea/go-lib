package bunrepo

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// orderModel is the shape the scaffolder generates: bun struct tags for the
// driver, plus the Model[ID] methods this package needs.
type orderModel struct {
	bun.BaseModel `bun:"table:orders"`

	ID        string `bun:"id,pk"`
	Quantity  int64  `bun:"quantity"`
	CreatedAt int64  `bun:"created_at"`
}

func (orderModel) TableName() string  { return "orders" }
func (orderModel) PrimaryKey() string { return "id" }
func (m orderModel) GetPK() string    { return m.ID }
func (m orderModel) CursorValues() map[string]any {
	return map[string]any{"id": m.ID, "created_at": m.CreatedAt, "quantity": m.Quantity}
}

// testDB returns a *bun.DB that renders SQL but never connects: sql.OpenDB is
// lazy, so as long as a test only calls q.String() no dial is attempted.
func testDB(t *testing.T) *bun.DB {
	t.Helper()
	sqldb := sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN("postgres://u:p@127.0.0.1:5432/testdb?sslmode=disable"),
	))
	t.Cleanup(func() { _ = sqldb.Close() })
	return bun.NewDB(sqldb, pgdialect.New())
}

// —— cursor round trip ——

func TestCursorRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		vals map[string]any
	}{
		{"string and int64", map[string]any{"id": "abc-123", "created_at": int64(1754006400000)}},
		{"int64 beyond float53", map[string]any{"n": int64(9007199254740993)}},
		{"float", map[string]any{"score": 12.5}},
		{"bool", map[string]any{"active": true}},
		{"empty string", map[string]any{"id": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := encodeCursor(tt.vals)
			if err != nil {
				t.Fatalf("encodeCursor: %v", err)
			}
			if strings.ContainsAny(enc, "+/=") {
				t.Errorf("cursor %q is not URL-safe base64", enc)
			}

			raw, err := decodeCursor(enc)
			if err != nil {
				t.Fatalf("decodeCursor: %v", err)
			}
			for col, want := range tt.vals {
				got, err := unmarshalCursorValue(raw[col])
				if err != nil {
					t.Fatalf("unmarshalCursorValue(%s): %v", col, err)
				}
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("column %s: got %v (%T), want %v (%T)", col, got, got, want, want)
				}
			}
		})
	}
}

// A plain json.Unmarshal turns every number into float64, which silently
// truncates millisecond timestamps and large integer keys. This is the
// regression guard for that.
func TestCursorPreservesInt64Type(t *testing.T) {
	const ts = int64(1754006400123)

	enc, err := encodeCursor(map[string]any{"created_at": ts})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	raw, err := decodeCursor(enc)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	got, err := unmarshalCursorValue(raw["created_at"])
	if err != nil {
		t.Fatalf("unmarshalCursorValue: %v", err)
	}

	v, ok := got.(int64)
	if !ok {
		t.Fatalf("got %T, want int64 — a float64 here binds as a numeric literal and breaks bigint comparisons", got)
	}
	if v != ts {
		t.Errorf("got %d, want %d", v, ts)
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	for _, in := range []string{"not-base64!!", "YWJj", "!!!"} {
		if _, err := decodeCursor(in); err == nil {
			t.Errorf("decodeCursor(%q) = nil error, want error", in)
		}
	}
}

// —— keyset predicate ——

func TestApplyKeysetCondition(t *testing.T) {
	db := testDB(t)

	tests := []struct {
		name     string
		keys     []SortKey
		vals     map[string]any
		wantSQL  []string
		absentIn []string
	}{
		{
			name:    "single ascending key",
			keys:    []SortKey{Asc("id")},
			vals:    map[string]any{"id": "a"},
			wantSQL: []string{"(id > 'a')"},
		},
		{
			name:    "single descending key",
			keys:    []SortKey{Descending("created_at")},
			vals:    map[string]any{"created_at": int64(100)},
			wantSQL: []string{"(created_at < 100)"},
		},
		{
			name: "desc then asc tiebreaker",
			keys: []SortKey{Descending("created_at"), Asc("id")},
			vals: map[string]any{"created_at": int64(100), "id": "a"},
			wantSQL: []string{
				"(created_at < 100)",
				"OR",
				"(created_at = 100 AND id > 'a')",
			},
		},
		{
			name:     "cursor missing the first key yields no predicate",
			keys:     []SortKey{Asc("id")},
			vals:     map[string]any{"other": 1},
			absentIn: []string{"id >"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := encodeCursor(tt.vals)
			if err != nil {
				t.Fatalf("encodeCursor: %v", err)
			}
			raw, err := decodeCursor(enc)
			if err != nil {
				t.Fatalf("decodeCursor: %v", err)
			}

			var rows []orderModel
			q := db.NewSelect().Model(&rows)
			q, err = applyKeysetCondition(q, tt.keys, raw)
			if err != nil {
				t.Fatalf("applyKeysetCondition: %v", err)
			}

			got := q.String()
			for _, want := range tt.wantSQL {
				if !strings.Contains(got, want) {
					t.Errorf("SQL missing %q\ngot: %s", want, got)
				}
			}
			for _, absent := range tt.absentIn {
				if strings.Contains(got, absent) {
					t.Errorf("SQL should not contain %q\ngot: %s", absent, got)
				}
			}
		})
	}
}

// bun's SelectQuery.Order parses each argument separately, so orderClauses must
// return one fragment per key — a comma-joined string is quoted as a single
// malformed identifier.
func TestOrderClausesRendersPerKey(t *testing.T) {
	db := testDB(t)

	var rows []orderModel
	got := db.NewSelect().
		Model(&rows).
		Order(orderClauses([]SortKey{Descending("created_at"), Asc("id")})...).
		String()

	if !strings.Contains(got, `"created_at" DESC`) {
		t.Errorf("missing descending primary sort\ngot: %s", got)
	}
	if !strings.Contains(got, `"id" ASC`) {
		t.Errorf("missing ascending tiebreaker\ngot: %s", got)
	}
}

// —— filters ——

func TestFilters(t *testing.T) {
	db := testDB(t)

	tests := []struct {
		name    string
		filter  Filter
		wantSQL string
	}{
		{"eq", Eq("quantity", 5), `"quantity" = 5`},
		{"like wraps in percent", Like("id", "abc"), `"id" LIKE '%abc%'`},
		{"in", In("id", "a", "b"), `"id" IN ('a', 'b')`},
		{"is null", IsNull("quantity"), `"quantity" IS NULL`},
		{"not null", NotNull("quantity"), `"quantity" IS NOT NULL`},
		{"raw", Raw("quantity > ?", 3), `quantity > 3`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rows []orderModel
			got := tt.filter.Apply(db.NewSelect().Model(&rows)).String()
			if !strings.Contains(got, tt.wantSQL) {
				t.Errorf("got: %s\nwant substring: %s", got, tt.wantSQL)
			}
		})
	}
}

func TestFilterSetAddIf(t *testing.T) {
	var fs FilterSet
	fs.Add(Eq("a", 1))
	fs.AddIf(false, Eq("b", 2))
	fs.AddIf(true, Eq("c", 3))

	if got := len(fs.Build()); got != 2 {
		t.Errorf("got %d filters, want 2 (AddIf(false) must not append)", got)
	}
}

func TestApplyFiltersChains(t *testing.T) {
	db := testDB(t)
	var rows []orderModel

	got := applyFilters(db.NewSelect().Model(&rows), []Filter{
		Eq("quantity", 5),
		Like("id", "abc"),
	}).String()

	if !strings.Contains(got, `"quantity" = 5`) || !strings.Contains(got, `"id" LIKE '%abc%'`) {
		t.Errorf("both filters must survive chaining\ngot: %s", got)
	}
}

// —— error mapping ——

// pgFakeError implements the same Field(byte) string shape as pgdriver.Error,
// whose own fields are unexported and unconstructable from a test.
type pgFakeError struct{ sqlstate string }

func (e pgFakeError) Error() string { return "pg error " + e.sqlstate }
func (e pgFakeError) Field(k byte) string { //nolint:unparam // mirrors pgdriver.Error
	if k == sqlstateField {
		return e.sqlstate
	}
	return ""
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"nil", nil, nil},
		{"no rows", sql.ErrNoRows, ErrNotFound},
		{"wrapped no rows", fmt.Errorf("scan: %w", sql.ErrNoRows), ErrNotFound},

		{"pg unique violation", pgFakeError{"23505"}, ErrDuplicateRecord},
		{"pg foreign key violation", pgFakeError{"23503"}, ErrForeignKeyViolation},
		{"pg not null violation", pgFakeError{"23502"}, ErrConstraintViolation},
		{"pg check violation", pgFakeError{"23514"}, ErrConstraintViolation},
		{"pg unrelated sqlstate", pgFakeError{"42P01"}, nil},
		{"pg wrapped", fmt.Errorf("insert: %w", pgFakeError{"23505"}), ErrDuplicateRecord},

		{"mysql duplicate entry", &mysql.MySQLError{Number: 1062}, ErrDuplicateRecord},
		{"mysql row is referenced", &mysql.MySQLError{Number: 1451}, ErrForeignKeyViolation},
		{"mysql no referenced row", &mysql.MySQLError{Number: 1452}, ErrForeignKeyViolation},
		{"mysql bad null", &mysql.MySQLError{Number: 1048}, ErrConstraintViolation},
		{"mysql check constraint", &mysql.MySQLError{Number: 3819}, ErrConstraintViolation},
		{"mysql unrelated", &mysql.MySQLError{Number: 1146}, nil},
		{"mysql wrapped", fmt.Errorf("exec: %w", &mysql.MySQLError{Number: 1062}), ErrDuplicateRecord},

		{"unrecognised", errors.New("boom"), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapError(tt.err); !errors.Is(got, tt.want) {
				t.Errorf("MapError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// —— params ——

func TestNewCursorParamsDefaults(t *testing.T) {
	tests := []struct {
		limit, want int
	}{{0, 20}, {-5, 20}, {50, 50}}

	for _, tt := range tests {
		if got := NewCursorParams("", tt.limit, Asc("id")).Limit; got != tt.want {
			t.Errorf("NewCursorParams(limit=%d).Limit = %d, want %d", tt.limit, got, tt.want)
		}
	}
}

func TestNewOffsetParamsClamps(t *testing.T) {
	p := NewOffsetParams(-1, 0)
	if p.Offset != 0 || p.Limit != 20 {
		t.Errorf("got offset=%d limit=%d, want offset=0 limit=20", p.Offset, p.Limit)
	}
}

func TestParseSortKeysRejectsUnsafeColumns(t *testing.T) {
	got := parseSortKeys("created_at:desc, id, 1=1; DROP TABLE orders")
	if len(got) != 2 {
		t.Fatalf("got %d keys, want 2 (injection attempt must be dropped): %+v", len(got), got)
	}
	if got[0].Column != "created_at" || !got[0].Desc {
		t.Errorf("first key = %+v, want created_at DESC", got[0])
	}
	if got[1].Column != "id" || got[1].Desc {
		t.Errorf("second key = %+v, want id ASC", got[1])
	}
}

// —— page building ——

func TestBuildPage(t *testing.T) {
	keys := []SortKey{Descending("created_at"), Asc("id")}
	rows := []orderModel{
		{ID: "a", CreatedAt: 3},
		{ID: "b", CreatedAt: 2},
		{ID: "c", CreatedAt: 1},
	}

	t.Run("full page sets HasNext and trims the probe row", func(t *testing.T) {
		page, got := buildPage(rows, 2, keys)
		if !page.HasNext {
			t.Error("HasNext = false, want true")
		}
		if len(got) != 2 {
			t.Errorf("got %d rows, want 2 (limit+1 probe row must be trimmed)", len(got))
		}
		if page.NextCursor == "" {
			t.Fatal("NextCursor is empty")
		}

		raw, err := decodeCursor(page.NextCursor)
		if err != nil {
			t.Fatalf("decodeCursor: %v", err)
		}
		if _, ok := raw["quantity"]; ok {
			t.Error("cursor encodes quantity, which is not in OrderBy")
		}
		if len(raw) != 2 {
			t.Errorf("cursor has %d columns, want 2 (id + created_at)", len(raw))
		}
	})

	t.Run("last page has no cursor", func(t *testing.T) {
		page, got := buildPage(rows, 5, keys)
		if page.HasNext {
			t.Error("HasNext = true, want false")
		}
		if page.NextCursor != "" {
			t.Errorf("NextCursor = %q, want empty", page.NextCursor)
		}
		if len(got) != 3 {
			t.Errorf("got %d rows, want 3", len(got))
		}
	})
}

// —— open ——

func TestOpenRejectsEmptyDSN(t *testing.T) {
	if _, err := Open("   "); err == nil {
		t.Error("Open(\"\") = nil error, want error")
	}
}

func TestIsPostgresDSN(t *testing.T) {
	tests := []struct {
		dsn  string
		want bool
	}{
		{"postgres://u:p@localhost:5432/db?sslmode=disable", true},
		{"postgresql://u:p@localhost:5432/db", true},
		{"mysql:mysql@tcp(localhost:3306)/db?parseTime=true", false},
		{"host=localhost port=5432 user=postgres", false}, // GORM key=value form — pgdriver cannot parse it
	}

	for _, tt := range tests {
		if got := isPostgresDSN(tt.dsn); got != tt.want {
			t.Errorf("isPostgresDSN(%q) = %v, want %v", tt.dsn, got, tt.want)
		}
	}
}
