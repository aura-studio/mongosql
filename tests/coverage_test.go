package tests

// coverage_test.go — comprehensive tests derived from doc/sql-subset.md.
// Each test function maps to a section in the spec. Tests that overlap with
// existing test files are intentionally included for completeness; they
// exercise the full translator → driver → MongoDB round-trip.

import (
	"sort"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/aura-studio/mongosql/driver"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func coverageUsers() []bson.M {
	return []bson.M{
		{"_id": 1, "name": "alice", "age": int32(30), "city": "BJ", "vip": true, "score": 85.5, "addr": bson.M{"zip": "100000"}},
		{"_id": 2, "name": "bob", "age": int32(25), "city": "SH", "vip": false, "score": 92.0, "addr": bson.M{"zip": "200000"}},
		{"_id": 3, "name": "carol", "age": int32(40), "city": "BJ", "vip": true, "score": 78.0, "addr": bson.M{"zip": "100001"}},
		{"_id": 4, "name": "dave", "age": int32(35), "city": "GZ", "vip": false, "score": 60.0, "addr": bson.M{"zip": "510000"}},
		{"_id": 5, "name": "erin", "age": int32(28), "city": "SH", "vip": true, "score": 95.5, "addr": bson.M{"zip": "200001"}},
		{"_id": 6, "name": "frank", "age": int32(22), "city": "BJ", "vip": false, "score": 45.0, "addr": bson.M{"zip": "100002"}},
		{"_id": 7, "name": "gary", "age": nil, "city": "SZ", "vip": false, "score": nil, "addr": bson.M{"zip": "518000"}},
	}
}

func coverageOrders() []bson.M {
	return []bson.M{
		{"_id": 100, "user_id": 1, "amount": int32(50), "product": "A"},
		{"_id": 101, "user_id": 1, "amount": int32(150), "product": "B"},
		{"_id": 102, "user_id": 2, "amount": int32(75), "product": "A"},
		{"_id": 103, "user_id": 3, "amount": int32(200), "product": "C"},
		{"_id": 104, "user_id": 5, "amount": int32(20), "product": "A"},
	}
}

func coverageProfiles() []bson.M {
	return []bson.M{
		{"_id": 1, "user_id": 1, "bio": "hello"},
		{"_id": 2, "user_id": 2, "bio": "world"},
		{"_id": 3, "user_id": 3, "bio": "test"},
	}
}

func seedCoverage(t *testing.T) *driver_wrap {
	t.Helper()
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"users":    coverageUsers(),
		"orders":   coverageOrders(),
		"profiles": coverageProfiles(),
	})
	return &driver_wrap{d}
}

type driver_wrap struct {
	d *driver.Driver
}

// exec is a shortcut that calls Exec and fails on error.
func (w *driver_wrap) exec(t *testing.T, sql string) *driver.Result {
	t.Helper()
	res, err := w.d.Exec(testCtx, sql)
	if err != nil {
		t.Fatalf("exec: %v\nSQL: %s", err, sql)
	}
	return res
}

// col extracts a single column's values from result rows, in order.
func col(rows []map[string]interface{}, key string) []interface{} {
	out := make([]interface{}, 0, len(rows))
	for _, r := range rows {
		out = append(out, r[key])
	}
	return out
}

func colStrings(rows []map[string]interface{}, key string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if v, ok := r[key].(string); ok {
			out = append(out, v)
		}
	}
	return out
}

func sortedColStrings(rows []map[string]interface{}, key string) []string {
	s := colStrings(rows, key)
	sort.Strings(s)
	return s
}

// ---------------------------------------------------------------------------
// SELECT
// ---------------------------------------------------------------------------

func TestCoverage_SelectStar(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT * FROM users")
	if len(res.Rows) != 7 {
		t.Fatalf("expected 7 rows, got %d", len(res.Rows))
	}
	// SELECT * hides MongoDB's internal _id by default; user-defined fields
	// (name, age, ...) should be present.
	if _, ok := res.Rows[0]["_id"]; ok {
		t.Fatalf("SELECT * should hide _id by default")
	}
	if _, ok := res.Rows[0]["name"]; !ok {
		t.Fatalf("SELECT * should include user-defined columns")
	}
}

func TestCoverage_SelectColumns(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name, age FROM users WHERE _id = 1")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	if row["name"] != "alice" {
		t.Fatalf("name: got %v", row["name"])
	}
	if _, hasID := row["_id"]; hasID {
		t.Fatalf("_id should be hidden when not explicitly selected")
	}
}

func TestCoverage_SelectColumnAlias(t *testing.T) {
	w := seedCoverage(t)
	// Column alias via aggregate path (JOIN forces aggregate)
	res := w.exec(t, "SELECT users.name AS user_name FROM users JOIN orders ON users._id = orders.user_id ORDER BY orders.amount ASC LIMIT 1")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if _, ok := res.Rows[0]["user_name"]; !ok {
		t.Fatalf("expected alias 'user_name' in result, got keys: %v", res.Rows[0])
	}
}

func TestCoverage_SelectNestedField(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT addr.zip FROM users WHERE _id = 1")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	// MongoDB returns nested projections as subdocuments under the parent key.
	// e.g. SELECT addr.zip produces {"addr": {"zip": "100000"}}
	zip := extractNestedField(row, "addr", "zip")
	if zip != "100000" {
		t.Fatalf("addr.zip: got %v (row=%v)", zip, row)
	}
}

func extractNestedField(row map[string]interface{}, parent, child string) interface{} {
	v, ok := row[parent]
	if !ok {
		return nil
	}
	switch a := v.(type) {
	case bson.M:
		return a[child]
	case bson.D:
		for _, e := range a {
			if e.Key == child {
				return e.Value
			}
		}
	case map[string]interface{}:
		return a[child]
	}
	return nil
}

func TestCoverage_SelectDistinctSingle(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT DISTINCT city FROM users")
	got := make(map[string]bool)
	for _, r := range res.Rows {
		if c, ok := r["city"].(string); ok {
			got[c] = true
		}
	}
	want := []string{"BJ", "SH", "GZ", "SZ"}
	if len(got) != len(want) {
		t.Fatalf("expected %d distinct cities, got %v", len(want), got)
	}
}

func TestCoverage_SelectDistinctMulti(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT DISTINCT city, vip FROM users")
	// BJ/true, BJ/false, SH/false, SH/true, GZ/false, SZ/false = 6 combos
	if len(res.Rows) < 6 {
		t.Fatalf("expected at least 6 distinct (city,vip) combos, got %d: %v", len(res.Rows), res.Rows)
	}
}

// ---------------------------------------------------------------------------
// WHERE — comparison operators
// ---------------------------------------------------------------------------

func TestCoverage_Where_Eq(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age = 30")
	equalStrings(t, sortedNames(res.Rows), []string{"alice"})
}

func TestCoverage_Where_Neq(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE city != 'BJ'")
	equalStrings(t, sortedNames(res.Rows), []string{"bob", "dave", "erin", "gary"})
}

func TestCoverage_Where_NeqAngleBracket(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE city <> 'BJ'")
	equalStrings(t, sortedNames(res.Rows), []string{"bob", "dave", "erin", "gary"})
}

func TestCoverage_Where_Lt(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age < 25")
	equalStrings(t, sortedNames(res.Rows), []string{"frank"})
}

func TestCoverage_Where_Lte(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age <= 25")
	equalStrings(t, sortedNames(res.Rows), []string{"bob", "frank"})
}

func TestCoverage_Where_Gt(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age > 35")
	equalStrings(t, sortedNames(res.Rows), []string{"carol"})
}

func TestCoverage_Where_Gte(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age >= 35")
	equalStrings(t, sortedNames(res.Rows), []string{"carol", "dave"})
}

// ---------------------------------------------------------------------------
// WHERE — logical operators
// ---------------------------------------------------------------------------

func TestCoverage_Where_And(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE city = 'BJ' AND age > 25")
	equalStrings(t, sortedNames(res.Rows), []string{"alice", "carol"})
}

func TestCoverage_Where_Or(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE city = 'GZ' OR city = 'SZ'")
	equalStrings(t, sortedNames(res.Rows), []string{"dave", "gary"})
}

func TestCoverage_Where_Not(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE NOT (city = 'BJ')")
	equalStrings(t, sortedNames(res.Rows), []string{"bob", "dave", "erin", "gary"})
}

// ---------------------------------------------------------------------------
// WHERE — IN / NOT IN
// ---------------------------------------------------------------------------

func TestCoverage_Where_In(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE city IN ('BJ', 'GZ')")
	equalStrings(t, sortedNames(res.Rows), []string{"alice", "carol", "dave", "frank"})
}

func TestCoverage_Where_NotIn(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE city NOT IN ('BJ', 'SH')")
	equalStrings(t, sortedNames(res.Rows), []string{"dave", "gary"})
}

// ---------------------------------------------------------------------------
// WHERE — LIKE / NOT LIKE
// ---------------------------------------------------------------------------

func TestCoverage_Where_LikePercent(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE name LIKE 'a%'")
	equalStrings(t, sortedNames(res.Rows), []string{"alice"})
}

func TestCoverage_Where_LikeSuffix(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE name LIKE '%k'")
	equalStrings(t, sortedNames(res.Rows), []string{"frank"})
}

func TestCoverage_Where_LikeContains(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE name LIKE '%ri%'")
	equalStrings(t, sortedNames(res.Rows), []string{"erin"})
}

func TestCoverage_Where_LikeUnderscore(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE name LIKE 'b_b'")
	equalStrings(t, sortedNames(res.Rows), []string{"bob"})
}

func TestCoverage_Where_NotLike(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE name NOT LIKE '%a%'")
	equalStrings(t, sortedNames(res.Rows), []string{"bob", "erin"})
}

// ---------------------------------------------------------------------------
// WHERE — REGEXP / NOT REGEXP
// ---------------------------------------------------------------------------

func TestCoverage_Where_Regexp(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE name REGEXP '^[a-c]'")
	equalStrings(t, sortedNames(res.Rows), []string{"alice", "bob", "carol"})
}

func TestCoverage_Where_NotRegexp(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE name NOT REGEXP '^[a-c]'")
	equalStrings(t, sortedNames(res.Rows), []string{"dave", "erin", "frank", "gary"})
}

// ---------------------------------------------------------------------------
// WHERE — BETWEEN / NOT BETWEEN
// ---------------------------------------------------------------------------

func TestCoverage_Where_Between(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age BETWEEN 25 AND 30")
	equalStrings(t, sortedNames(res.Rows), []string{"alice", "bob", "erin"})
}

func TestCoverage_Where_NotBetween(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age NOT BETWEEN 25 AND 35")
	equalStrings(t, sortedNames(res.Rows), []string{"carol", "frank"})
}

// ---------------------------------------------------------------------------
// WHERE — IS NULL / IS NOT NULL
// ---------------------------------------------------------------------------

func TestCoverage_Where_IsNull(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age IS NULL")
	equalStrings(t, sortedNames(res.Rows), []string{"gary"})
}

func TestCoverage_Where_IsNotNull(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age IS NOT NULL")
	equalStrings(t, sortedNames(res.Rows), []string{"alice", "bob", "carol", "dave", "erin", "frank"})
}

// ---------------------------------------------------------------------------
// WHERE — IS TRUE / IS FALSE / IS NOT TRUE / IS NOT FALSE
// ---------------------------------------------------------------------------

func TestCoverage_Where_IsTrue(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE vip IS TRUE")
	equalStrings(t, sortedNames(res.Rows), []string{"alice", "carol", "erin"})
}

func TestCoverage_Where_IsFalse(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE vip IS FALSE")
	equalStrings(t, sortedNames(res.Rows), []string{"bob", "dave", "frank", "gary"})
}

func TestCoverage_Where_IsNotTrue(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE vip IS NOT TRUE")
	equalStrings(t, sortedNames(res.Rows), []string{"bob", "dave", "frank", "gary"})
}

func TestCoverage_Where_IsNotFalse(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE vip IS NOT FALSE")
	equalStrings(t, sortedNames(res.Rows), []string{"alice", "carol", "erin"})
}

// ---------------------------------------------------------------------------
// WHERE — value types (string, int, float, negative, bool, NULL)
// ---------------------------------------------------------------------------

func TestCoverage_Where_StringValue(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE city = 'SZ'")
	equalStrings(t, sortedNames(res.Rows), []string{"gary"})
}

func TestCoverage_Where_IntValue(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age = 30")
	equalStrings(t, sortedNames(res.Rows), []string{"alice"})
}

func TestCoverage_Where_FloatValue(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE score > 90.0")
	equalStrings(t, sortedNames(res.Rows), []string{"bob", "erin"})
}

func TestCoverage_Where_NegativeValue(t *testing.T) {
	// Seed a row with negative value
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"temps": {
			{"_id": 1, "city": "A", "temp": int32(-5)},
			{"_id": 2, "city": "B", "temp": int32(10)},
			{"_id": 3, "city": "C", "temp": int32(-15)},
		},
	})
	res, err := d.Exec(testCtx, "SELECT city FROM temps WHERE temp > -10")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	got := sortedColStrings(res.Rows, "city")
	equalStrings(t, got, []string{"A", "B"})
}

func TestCoverage_Where_BoolValue(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE vip = true")
	equalStrings(t, sortedNames(res.Rows), []string{"alice", "carol", "erin"})

	res2 := w.exec(t, "SELECT name FROM users WHERE vip = false")
	equalStrings(t, sortedNames(res2.Rows), []string{"bob", "dave", "frank", "gary"})
}

func TestCoverage_Where_NullValue(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age IS NULL")
	equalStrings(t, sortedNames(res.Rows), []string{"gary"})
}

// ---------------------------------------------------------------------------
// ORDER BY / LIMIT / OFFSET
// ---------------------------------------------------------------------------

func TestCoverage_OrderBy_Asc(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age IS NOT NULL ORDER BY age ASC")
	got := names(res.Rows)
	want := []string{"frank", "bob", "erin", "alice", "dave", "carol"}
	equalStrings(t, got, want)
}

func TestCoverage_OrderBy_Desc(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age IS NOT NULL ORDER BY age DESC")
	got := names(res.Rows)
	want := []string{"carol", "dave", "alice", "erin", "bob", "frank"}
	equalStrings(t, got, want)
}

func TestCoverage_Limit(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age IS NOT NULL ORDER BY age ASC LIMIT 3")
	got := names(res.Rows)
	want := []string{"frank", "bob", "erin"}
	equalStrings(t, got, want)
}

func TestCoverage_LimitOffset(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT name FROM users WHERE age IS NOT NULL ORDER BY age ASC LIMIT 2 OFFSET 2")
	got := names(res.Rows)
	want := []string{"erin", "alice"}
	equalStrings(t, got, want)
}

// ---------------------------------------------------------------------------
// Aggregate — COUNT / SUM / AVG / MIN / MAX
// ---------------------------------------------------------------------------

func TestCoverage_Agg_CountStar(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT COUNT(*) AS n FROM users")
	if len(res.Rows) != 1 || toInt64(res.Rows[0]["n"]) != 7 {
		t.Fatalf("COUNT(*) got %v", res.Rows)
	}
}

func TestCoverage_Agg_CountCol(t *testing.T) {
	w := seedCoverage(t)
	// age has a NULL for gary, so COUNT(age) should be 6
	res := w.exec(t, "SELECT COUNT(age) AS n FROM users")
	if len(res.Rows) != 1 || toInt64(res.Rows[0]["n"]) != 6 {
		t.Fatalf("COUNT(age) expected 6, got %v", res.Rows)
	}
}

func TestCoverage_Agg_Sum(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT SUM(age) AS s FROM users")
	// 30+25+40+35+28+22 = 180 (gary's NULL excluded by $sum)
	if len(res.Rows) != 1 || toInt64(res.Rows[0]["s"]) != 180 {
		t.Fatalf("SUM(age) expected 180, got %v", res.Rows)
	}
}

func TestCoverage_Agg_Avg(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT AVG(age) AS a FROM users")
	// 180/6 = 30.0
	if len(res.Rows) != 1 {
		t.Fatalf("AVG(age) expected 1 row, got %v", res.Rows)
	}
	avg := toFloat64(res.Rows[0]["a"])
	if avg != 30.0 {
		t.Fatalf("AVG(age) expected 30.0, got %v", avg)
	}
}

func TestCoverage_Agg_Min(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT MIN(age) AS mn FROM users")
	if len(res.Rows) != 1 || toInt64(res.Rows[0]["mn"]) != 22 {
		t.Fatalf("MIN(age) expected 22, got %v", res.Rows)
	}
}

func TestCoverage_Agg_Max(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT MAX(age) AS mx FROM users")
	if len(res.Rows) != 1 || toInt64(res.Rows[0]["mx"]) != 40 {
		t.Fatalf("MAX(age) expected 40, got %v", res.Rows)
	}
}

// ---------------------------------------------------------------------------
// Aggregate — GROUP BY
// ---------------------------------------------------------------------------

func TestCoverage_GroupBy(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT city, COUNT(*) AS n FROM users GROUP BY city")
	got := make(map[string]int64)
	for _, r := range res.Rows {
		city, _ := r["city"].(string)
		got[city] = toInt64(r["n"])
	}
	want := map[string]int64{"BJ": 3, "SH": 2, "GZ": 1, "SZ": 1}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("city %s: got %d want %d (full=%v)", k, got[k], v, got)
		}
	}
}

func TestCoverage_GroupBy_MultiAgg(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT city, SUM(age) AS s, MIN(age) AS mn, MAX(age) AS mx FROM users WHERE age IS NOT NULL GROUP BY city")
	bj := findRow(res.Rows, "city", "BJ")
	if bj == nil {
		t.Fatalf("missing BJ row")
	}
	if toInt64(bj["s"]) != 92 { // 30+40+22
		t.Fatalf("BJ SUM got %d", toInt64(bj["s"]))
	}
	if toInt64(bj["mn"]) != 22 {
		t.Fatalf("BJ MIN got %d", toInt64(bj["mn"]))
	}
	if toInt64(bj["mx"]) != 40 {
		t.Fatalf("BJ MAX got %d", toInt64(bj["mx"]))
	}
}

// ---------------------------------------------------------------------------
// Aggregate — HAVING
// ---------------------------------------------------------------------------

func TestCoverage_Having(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT city, COUNT(*) AS n FROM users GROUP BY city HAVING n > 1")
	got := make(map[string]int64)
	for _, r := range res.Rows {
		city, _ := r["city"].(string)
		got[city] = toInt64(r["n"])
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 cities with count>1, got %v", got)
	}
	if got["BJ"] != 3 || got["SH"] != 2 {
		t.Fatalf("unexpected: %v", got)
	}
}

// ---------------------------------------------------------------------------
// JOIN
// ---------------------------------------------------------------------------

func TestCoverage_Join(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT users.name AS user_name, orders.amount AS amt FROM users JOIN orders ON users._id = orders.user_id ORDER BY orders.amount ASC")
	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(res.Rows))
	}
	// First row: erin with amount 20
	if res.Rows[0]["user_name"] != "erin" || toInt64(res.Rows[0]["amt"]) != 20 {
		t.Fatalf("first row: %v", res.Rows[0])
	}
}

func TestCoverage_LeftJoin(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT users.name AS user_name, orders.amount AS amt FROM users LEFT JOIN orders ON users._id = orders.user_id ORDER BY users.name ASC")
	// All 7 users should appear; those without orders have null amount
	if len(res.Rows) < 7 {
		t.Fatalf("LEFT JOIN expected at least 7 rows, got %d", len(res.Rows))
	}
	// alice has 2 orders, bob 1, carol 1, dave 0, erin 1, frank 0, gary 0
	// total = 2+1+1+1+1+1+1 = 8 rows (alice appears twice)
	// Actually: dave, frank, gary have no orders → each appears once with null
	// alice has 2 orders → appears twice
	// total = 2+1+1+1+1+1+1 = 8
	if len(res.Rows) != 8 {
		t.Fatalf("LEFT JOIN expected 8 rows, got %d: %v", len(res.Rows), res.Rows)
	}
}

func TestCoverage_JoinWithAlias(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t, "SELECT u.name AS user_name, o.amount AS amt FROM users u JOIN orders o ON u._id = o.user_id ORDER BY o.amount DESC LIMIT 1")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0]["user_name"] != "carol" || toInt64(res.Rows[0]["amt"]) != 200 {
		t.Fatalf("expected carol/200, got %v", res.Rows[0])
	}
}

func TestCoverage_ChainedJoin(t *testing.T) {
	w := seedCoverage(t)
	res := w.exec(t,
		"SELECT users.name AS uname, orders.amount AS amt, profiles.bio AS bio "+
			"FROM users "+
			"JOIN orders ON users._id = orders.user_id "+
			"JOIN profiles ON users._id = profiles.user_id "+
			"ORDER BY orders.amount ASC")
	// Only users 1,2,3 have profiles; user 1 has 2 orders, user 2 has 1, user 3 has 1 → 4 rows
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d: %v", len(res.Rows), res.Rows)
	}
}

// ---------------------------------------------------------------------------
// INSERT
// ---------------------------------------------------------------------------

func TestCoverage_InsertSingle(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"items": nil})

	res, err := d.Exec(testCtx, "INSERT INTO items (name, qty, price) VALUES ('widget', 10, 2.5)")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(res.InsertedIDs) != 1 {
		t.Fatalf("expected 1 insert, got %v", res.InsertedIDs)
	}

	// Verify
	check, err := d.Exec(testCtx, "SELECT name, qty, price FROM items")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(check.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(check.Rows))
	}
	if check.Rows[0]["name"] != "widget" {
		t.Fatalf("name: got %v", check.Rows[0]["name"])
	}
}

func TestCoverage_InsertMulti(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"items": nil})

	res, err := d.Exec(testCtx, "INSERT INTO items (name, qty) VALUES ('a', 1), ('b', 2), ('c', 3)")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(res.InsertedIDs) != 3 {
		t.Fatalf("expected 3 inserts, got %v", res.InsertedIDs)
	}
}

func TestCoverage_InsertNegativeAndNull(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"items": nil})

	_, err := d.Exec(testCtx, "INSERT INTO items (name, qty, note) VALUES ('x', -5, NULL)")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	check, err := d.Exec(testCtx, "SELECT name, qty, note FROM items")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(check.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(check.Rows))
	}
	if toInt64(check.Rows[0]["qty"]) != -5 {
		t.Fatalf("qty: got %v", check.Rows[0]["qty"])
	}
	if check.Rows[0]["note"] != nil {
		t.Fatalf("note: expected nil, got %v", check.Rows[0]["note"])
	}
}

// ---------------------------------------------------------------------------
// UPDATE
// ---------------------------------------------------------------------------

func TestCoverage_UpdateWithWhere(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"items": {
			{"_id": 1, "name": "a", "qty": int32(10)},
			{"_id": 2, "name": "b", "qty": int32(20)},
		},
	})

	res, err := d.Exec(testCtx, "UPDATE items SET qty = 99 WHERE name = 'a'")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.MatchedCount != 1 || res.ModifiedCount != 1 {
		t.Fatalf("matched=%d modified=%d", res.MatchedCount, res.ModifiedCount)
	}

	check, err := d.Exec(testCtx, "SELECT qty FROM items WHERE name = 'a'")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if toInt64(check.Rows[0]["qty"]) != 99 {
		t.Fatalf("qty: got %v", check.Rows[0]["qty"])
	}
}

func TestCoverage_UpdateWithoutWhere(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"items": {
			{"_id": 1, "name": "a", "qty": int32(10)},
			{"_id": 2, "name": "b", "qty": int32(20)},
		},
	})

	res, err := d.Exec(testCtx, "UPDATE items SET qty = 0")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.MatchedCount != 2 || res.ModifiedCount != 2 {
		t.Fatalf("matched=%d modified=%d", res.MatchedCount, res.ModifiedCount)
	}
}

// ---------------------------------------------------------------------------
// DELETE
// ---------------------------------------------------------------------------

func TestCoverage_DeleteWithWhere(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"items": {
			{"_id": 1, "name": "a"},
			{"_id": 2, "name": "b"},
			{"_id": 3, "name": "c"},
		},
	})

	res, err := d.Exec(testCtx, "DELETE FROM items WHERE name = 'b'")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.DeletedCount != 1 {
		t.Fatalf("deleted: got %d", res.DeletedCount)
	}

	check, err := d.Exec(testCtx, "SELECT name FROM items")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(check.Rows) != 2 {
		t.Fatalf("remaining: got %d", len(check.Rows))
	}
}

func TestCoverage_DeleteWithoutWhere(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"items": {
			{"_id": 1, "name": "a"},
			{"_id": 2, "name": "b"},
		},
	})

	res, err := d.Exec(testCtx, "DELETE FROM items")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.DeletedCount != 2 {
		t.Fatalf("deleted: got %d", res.DeletedCount)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func toFloat64(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}
