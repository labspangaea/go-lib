package repo

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// — cursor encode / decode —

func TestEncodeDecode_RoundTrip(t *testing.T) {
	vals := map[string]any{
		"created_at": int64(1_700_000_000_000),
		"id":         "abc-123",
	}
	encoded, err := encodeCursor(vals)
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	if encoded == "" {
		t.Fatal("expected non-empty cursor")
	}

	raw, err := decodeCursor(encoded)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}

	gotID, err := unmarshalCursorValue(raw["id"])
	if err != nil {
		t.Fatalf("unmarshal id: %v", err)
	}
	if gotID != "abc-123" {
		t.Errorf("id: want %q got %v", "abc-123", gotID)
	}

	gotTS, err := unmarshalCursorValue(raw["created_at"])
	if err != nil {
		t.Fatalf("unmarshal created_at: %v", err)
	}
	// Must preserve int64 precision — must NOT be truncated to float64.
	if gotTS != int64(1_700_000_000_000) {
		t.Errorf("created_at: want %d got %v (%T)", int64(1_700_000_000_000), gotTS, gotTS)
	}
}

func TestDecodeCursor_InvalidBase64(t *testing.T) {
	_, err := decodeCursor("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDecodeCursor_InvalidJSON(t *testing.T) {
	// base64("hello") — valid base64, not a valid cursorPayload
	_, err := decodeCursor("aGVsbG8")
	if err == nil {
		t.Fatal("expected error for invalid JSON payload")
	}
}

func TestUnmarshalCursorValue_String(t *testing.T) {
	raw, _ := json.Marshal("hello")
	got, err := unmarshalCursorValue(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Errorf("want %q got %v", "hello", got)
	}
}

func TestUnmarshalCursorValue_Int64Precision(t *testing.T) {
	const large = int64(9_007_199_254_740_993) // > 2^53 — would lose precision as float64
	raw, _ := json.Marshal(large)
	got, err := unmarshalCursorValue(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != large {
		t.Errorf("precision loss: want %d got %v (%T)", large, got, got)
	}
}

func TestUnmarshalCursorValue_Float(t *testing.T) {
	raw, _ := json.Marshal(3.14)
	got, err := unmarshalCursorValue(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := got.(float64); !ok {
		t.Errorf("expected float64, got %T", got)
	}
}

// — Filter constructors —

func TestFilter_NotNil(t *testing.T) {
	tests := []struct {
		name string
		f    Filter
	}{
		{"Eq", Eq("col", "val")},
		{"Like", Like("col", "val")},
		{"FullText", FullText("col", "val")},
		{"In", In("col", "a", "b")},
		{"IsNull", IsNull("col")},
		{"NotNull", NotNull("col")},
		{"Raw", Raw("col > ?", 1)},
	}
	for _, tt := range tests {
		if tt.f == nil {
			t.Errorf("%s: filter must not be nil", tt.name)
		}
	}
}

// — FilterSet —

func TestFilterSet_AddIf_SkipsFalse(t *testing.T) {
	var fs FilterSet
	fs.AddIf(false, Eq("status", "active"))
	fs.AddIf(true, Like("name", "john"))
	if got := len(fs.Build()); got != 1 {
		t.Errorf("expected 1 filter, got %d", got)
	}
}

func TestFilterSet_Add_Chaining(t *testing.T) {
	var fs FilterSet
	fs.Add(Eq("a", 1)).Add(Eq("b", 2)).Add(Eq("c", 3))
	if got := len(fs.Build()); got != 3 {
		t.Errorf("expected 3 filters, got %d", got)
	}
}

func TestFilterSet_Empty(t *testing.T) {
	var fs FilterSet
	if got := fs.Build(); len(got) != 0 {
		t.Errorf("empty set should build to empty slice, got %d", len(got))
	}
}

// — SortKey / orderClauses —

func TestSortKey_OrderClause(t *testing.T) {
	tests := []struct {
		sk   SortKey
		want string
	}{
		{Asc("id"), "id ASC"},
		{Descending("created_at"), "created_at DESC"},
		{SortKey{Column: "name", Desc: false}, "name ASC"},
	}
	for _, tt := range tests {
		if got := tt.sk.orderClause(); got != tt.want {
			t.Errorf("orderClause: want %q got %q", tt.want, got)
		}
	}
}

func TestOrderClauses_Single(t *testing.T) {
	got := orderClauses([]SortKey{Descending("created_at")})
	if got != "created_at DESC" {
		t.Errorf("want %q got %q", "created_at DESC", got)
	}
}

func TestOrderClauses_Multi(t *testing.T) {
	got := orderClauses([]SortKey{Descending("created_at"), Asc("id")})
	want := "created_at DESC, id ASC"
	if got != want {
		t.Errorf("want %q got %q", want, got)
	}
}

func TestOrderClauses_Empty(t *testing.T) {
	got := orderClauses(nil)
	if got != "" {
		t.Errorf("empty keys should produce empty string, got %q", got)
	}
}

// — filterCursorValues —

func TestFilterCursorValues_TrimsUnused(t *testing.T) {
	all := map[string]any{
		"id":         "x",
		"created_at": int64(1000),
		"updated_at": int64(2000), // not in orderBy
	}
	orderBy := []SortKey{Descending("created_at"), Asc("id")}
	got := filterCursorValues(all, orderBy)

	if len(got) != 2 {
		t.Fatalf("expected 2 values, got %d: %v", len(got), got)
	}
	if _, ok := got["updated_at"]; ok {
		t.Error("updated_at should have been filtered out")
	}
	if _, ok := got["id"]; !ok {
		t.Error("id should be present")
	}
	if _, ok := got["created_at"]; !ok {
		t.Error("created_at should be present")
	}
}

func TestFilterCursorValues_MissingColumn(t *testing.T) {
	// If a sort column has no value in all, it should simply be absent (not panic).
	all := map[string]any{"id": "x"}
	orderBy := []SortKey{Descending("created_at"), Asc("id")}
	got := filterCursorValues(all, orderBy)
	if len(got) != 1 {
		t.Errorf("expected 1 value (id only), got %d", len(got))
	}
}

// — CursorParamsFromRequest —

func TestCursorParamsFromRequest_Defaults(t *testing.T) {
	r := &http.Request{URL: &url.URL{RawQuery: ""}}
	p := CursorParamsFromRequest(r, 20, Descending("created_at"), Asc("id"))
	if p.Limit != 20 {
		t.Errorf("limit: want 20 got %d", p.Limit)
	}
	if len(p.OrderBy) != 2 {
		t.Errorf("orderBy: want 2 got %d", len(p.OrderBy))
	}
	if p.Cursor != "" {
		t.Errorf("cursor should be empty by default")
	}
}

func TestCursorParamsFromRequest_Override(t *testing.T) {
	r := &http.Request{URL: &url.URL{RawQuery: "limit=5&order_by=name:asc,id:desc&cursor=abc"}}
	p := CursorParamsFromRequest(r, 20)
	if p.Limit != 5 {
		t.Errorf("limit: want 5 got %d", p.Limit)
	}
	if p.Cursor != "abc" {
		t.Errorf("cursor: want abc got %q", p.Cursor)
	}
	if len(p.OrderBy) != 2 {
		t.Fatalf("orderBy: want 2 got %d", len(p.OrderBy))
	}
	if p.OrderBy[0].Column != "name" || p.OrderBy[0].Desc {
		t.Errorf("key[0]: want name:asc got %+v", p.OrderBy[0])
	}
	if p.OrderBy[1].Column != "id" || !p.OrderBy[1].Desc {
		t.Errorf("key[1]: want id:desc got %+v", p.OrderBy[1])
	}
}

func TestCursorParamsFromRequest_InvalidLimitFallback(t *testing.T) {
	for _, q := range []string{"limit=0", "limit=-1", "limit=abc"} {
		r := &http.Request{URL: &url.URL{RawQuery: q}}
		p := CursorParamsFromRequest(r, 15)
		if p.Limit != 15 {
			t.Errorf("%s: invalid limit should fall back to default, got %d", q, p.Limit)
		}
	}
}

func TestCursorParamsFromRequest_LimitCappedAtMaxLimit(t *testing.T) {
	r := &http.Request{URL: &url.URL{RawQuery: "limit=10000000"}}
	p := CursorParamsFromRequest(r, 20)
	if p.Limit != maxLimit {
		t.Errorf("limit: want %d (maxLimit) got %d", maxLimit, p.Limit)
	}
}

func TestCursorParamsFromRequest_LimitAtMaxLimitBoundary(t *testing.T) {
	r := &http.Request{URL: &url.URL{RawQuery: "limit=1000"}}
	p := CursorParamsFromRequest(r, 20)
	if p.Limit != 1000 {
		t.Errorf("limit: want 1000 (exact boundary, must not be capped) got %d", p.Limit)
	}
}

func TestCursorParamsFromRequest_LimitBelowMaxLimit_Unchanged(t *testing.T) {
	r := &http.Request{URL: &url.URL{RawQuery: "limit=50"}}
	p := CursorParamsFromRequest(r, 20)
	if p.Limit != 50 {
		t.Errorf("limit: want 50 (below maxLimit, must not be altered) got %d", p.Limit)
	}
}

// — parseSortKeys —

func TestParseSortKeys(t *testing.T) {
	tests := []struct {
		input string
		want  []SortKey
	}{
		{"created_at:desc,id:asc", []SortKey{Descending("created_at"), Asc("id")}},
		{"name", []SortKey{Asc("name")}},
		{"", nil},
		{"  col1:DESC , col2:ASC  ", []SortKey{Descending("col1"), Asc("col2")}},
		{",", nil},
	}
	for _, tt := range tests {
		got := parseSortKeys(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("input %q: want %d keys got %d (%v)", tt.input, len(tt.want), len(got), got)
			continue
		}
		for i := range got {
			if got[i].Column != tt.want[i].Column || got[i].Desc != tt.want[i].Desc {
				t.Errorf("input %q key[%d]: want %+v got %+v", tt.input, i, tt.want[i], got[i])
			}
		}
	}
}

// — parseSortKeys: injection rejection —

// TestParseSortKeys_RejectsMaliciousColumns asserts that column names containing
// SQL metacharacters or injection payloads are silently dropped so they never
// reach db.Order() / db.Where() as raw SQL fragments.
func TestParseSortKeys_RejectsMaliciousColumns(t *testing.T) {
	malicious := []string{
		"id; DROP TABLE users--",
		"(SELECT 1)",
		"id UNION SELECT password FROM users",
		"1=1",
		"col name",    // space
		"col.name",    // dot
		"col-name",    // hyphen
		"col`name",    // backtick
		"col\"name",   // double quote
		"col'name",    // single quote
		"col/**/name", // comment sequence
	}
	for _, col := range malicious {
		input := col + ":asc"
		got := parseSortKeys(input)
		if len(got) != 0 {
			t.Errorf("parseSortKeys(%q): expected 0 keys (malicious column dropped), got %d: %+v", input, len(got), got)
		}
	}
}

// TestParseSortKeys_AcceptsValidColumns asserts that legitimate snake_case and
// mixed-case column names continue to work after the validation was added.
func TestParseSortKeys_AcceptsValidColumns(t *testing.T) {
	valid := []struct {
		col  string
		desc bool
	}{
		{"id", false},
		{"created_at", true},
		{"_internal", false},
		{"Col1", false},
		{"user_id_fk", true},
	}
	for _, tc := range valid {
		dir := "asc"
		if tc.desc {
			dir = "desc"
		}
		input := tc.col + ":" + dir
		got := parseSortKeys(input)
		if len(got) != 1 {
			t.Errorf("parseSortKeys(%q): expected 1 key, got %d", input, len(got))
			continue
		}
		if got[0].Column != tc.col {
			t.Errorf("parseSortKeys(%q): column want %q got %q", input, tc.col, got[0].Column)
		}
		if got[0].Desc != tc.desc {
			t.Errorf("parseSortKeys(%q): desc want %v got %v", input, tc.desc, got[0].Desc)
		}
	}
}

// TestParseSortKeys_MixedValidAndInvalid asserts that valid columns are kept and
// invalid columns are dropped from a comma-separated list — order preserved.
func TestParseSortKeys_MixedValidAndInvalid(t *testing.T) {
	// "id" is valid, "bad col" contains a space (invalid), "created_at" is valid
	got := parseSortKeys("id:asc,bad col:desc,created_at:desc")
	if len(got) != 2 {
		t.Fatalf("expected 2 keys (bad col dropped), got %d: %+v", len(got), got)
	}
	if got[0].Column != "id" {
		t.Errorf("key[0]: want id got %q", got[0].Column)
	}
	if got[1].Column != "created_at" {
		t.Errorf("key[1]: want created_at got %q", got[1].Column)
	}
}

// — buildPage —

type pageTestModel struct {
	ID        string
	CreatedAt int64
}

func (m pageTestModel) TableName() string  { return "tests" }
func (m pageTestModel) PrimaryKey() string { return "id" }
func (m pageTestModel) GetPK() string      { return m.ID }
func (m pageTestModel) CursorValues() map[string]any {
	return map[string]any{"id": m.ID, "created_at": m.CreatedAt}
}

func TestBuildPage_NoNext(t *testing.T) {
	rows := []pageTestModel{{ID: "1"}, {ID: "2"}}
	page, out := buildPage[pageTestModel, string](rows, 5, []SortKey{Asc("id")})
	if page.HasNext {
		t.Error("HasNext should be false when rows <= limit")
	}
	if page.NextCursor != "" {
		t.Error("NextCursor should be empty when HasNext=false")
	}
	if len(out) != 2 {
		t.Errorf("want 2 rows got %d", len(out))
	}
	if page.Limit != 5 {
		t.Errorf("Limit: want 5 got %d", page.Limit)
	}
}

func TestBuildPage_HasNext(t *testing.T) {
	// limit=2 but 3 rows fetched — triggers HasNext
	rows := []pageTestModel{{ID: "1"}, {ID: "2"}, {ID: "3"}}
	page, out := buildPage[pageTestModel, string](rows, 2, []SortKey{Asc("id")})
	if !page.HasNext {
		t.Error("HasNext should be true when rows > limit")
	}
	if len(out) != 2 {
		t.Errorf("rows should be trimmed to limit: want 2 got %d", len(out))
	}
	if page.NextCursor == "" {
		t.Error("NextCursor should be set when HasNext=true")
	}
}

func TestBuildPage_CursorEncodeLastKeptRow(t *testing.T) {
	rows := []pageTestModel{
		{ID: "a", CreatedAt: 999},
		{ID: "b", CreatedAt: 888}, // last *kept* row
		{ID: "c", CreatedAt: 777}, // extra row that triggers HasNext
	}
	orderBy := []SortKey{Descending("created_at"), Asc("id")}
	page, _ := buildPage[pageTestModel, string](rows, 2, orderBy)
	if page.NextCursor == "" {
		t.Fatal("expected non-empty cursor")
	}

	raw, err := decodeCursor(page.NextCursor)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}

	ts, err := unmarshalCursorValue(raw["created_at"])
	if err != nil {
		t.Fatalf("unmarshal created_at: %v", err)
	}
	if ts != int64(888) {
		t.Errorf("cursor encodes wrong row: want created_at=888 got %v", ts)
	}

	id, err := unmarshalCursorValue(raw["id"])
	if err != nil {
		t.Fatalf("unmarshal id: %v", err)
	}
	if id != "b" {
		t.Errorf("cursor encodes wrong row: want id=b got %v", id)
	}
}

func TestBuildPage_EmptyRows(t *testing.T) {
	page, out := buildPage[pageTestModel, string](nil, 10, nil)
	if page.HasNext {
		t.Error("empty rows should not have next page")
	}
	if len(out) != 0 {
		t.Error("empty rows should return empty slice")
	}
}

// — applyKeysetCondition error paths —

func TestApplyKeysetCondition_EmptyKeysReturnsNil(t *testing.T) {
	// Passing nil keys should return (db, nil) unchanged.
	// We can't easily test the SQL output without a real DB driver,
	// but we can verify the function does not return an error for edge cases.
	rawVals := map[string]json.RawMessage{}
	db, err := applyKeysetCondition(nil, nil, rawVals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db != nil {
		t.Error("nil input db with empty keys should return nil")
	}
}

func TestApplyKeysetCondition_BadCursorValue(t *testing.T) {
	// Malformed JSON in rawVals should return an error.
	rawVals := map[string]json.RawMessage{
		"id": json.RawMessage(`{invalid`),
	}
	keys := []SortKey{Asc("id")}
	_, err := applyKeysetCondition(nil, keys, rawVals)
	if err == nil {
		t.Fatal("expected error for malformed cursor value JSON")
	}
}
