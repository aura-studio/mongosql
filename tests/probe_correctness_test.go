package tests

import (
	"sort"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// This file contains targeted correctness probes derived from a code review.
// Each probe asserts the MySQL-correct behaviour; a failure indicates a
// divergence/bug in the SQL->MongoDB translation. Probes use t.Errorf (not
// Fatalf) and log actual output so every finding is recorded in one run.

func collectStr(rows []map[string]interface{}, key string) []string {
	out := []string{}
	for _, r := range rows {
		if v, ok := r[key]; ok {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	case float32:
		return float64(n)
	}
	return 0
}

// P1: COUNT(col) must count rows where col is non-NULL, including col = 0.
func TestProbe_CountColZero(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"cnt_test": {
		{"_id": 1, "v": int32(0)},
		{"_id": 2, "v": int32(5)},
		{"_id": 3, "v": nil},
		{"_id": 4}, // missing v
	}})

	res, err := d.Exec(testCtx, "SELECT COUNT(v) AS c, COUNT(*) AS total FROM cnt_test")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	c := toInt64(res.Rows[0]["c"])
	total := toInt64(res.Rows[0]["total"])
	t.Logf("COUNT(v)=%d COUNT(*)=%d (rows=%v)", c, total, res.Rows)
	if total != 4 {
		t.Errorf("COUNT(*) got %d want 4", total)
	}
	// MySQL: COUNT(v) counts non-NULL => v=0 and v=5 => 2.
	if c != 2 {
		t.Errorf("LIKELY BUG: COUNT(v) got %d want 2 (col value 0 is non-NULL and must be counted)", c)
	}
}

// P2: HAVING that restates the aggregate function (HAVING COUNT(*) > 1).
func TestProbe_HavingAggregateFunc(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	res, err := d.Exec(testCtx,
		"SELECT city, COUNT(*) AS n FROM users GROUP BY city HAVING COUNT(*) > 1")
	if err != nil {
		t.Errorf("LIKELY BUG: HAVING COUNT(*) > 1 returned error: %v", err)
		return
	}
	got := collectStr(res.Rows, "city")
	want := []string{"BJ", "SH"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("HAVING COUNT(*)>1 got %v want %v", got, want)
	}
}

// P3: negative FLOOR/CEIL via constant INSERT VALUES (static eval path).
func TestProbe_NegativeFloorCeilStatic(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"floors": {}})

	if _, err := d.Exec(testCtx,
		"INSERT INTO floors (id, f, c) VALUES (1, FLOOR(-2.5), CEIL(-2.5))"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	res, err := d.Exec(testCtx, "SELECT f, c FROM floors WHERE id = 1")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	f := toFloat(res.Rows[0]["f"])
	c := toFloat(res.Rows[0]["c"])
	t.Logf("FLOOR(-2.5)=%v CEIL(-2.5)=%v", f, c)
	if f != -3 {
		t.Errorf("LIKELY BUG: FLOOR(-2.5) got %v want -3", f)
	}
	if c != -2 {
		t.Errorf("LIKELY BUG: CEIL(-2.5) got %v want -2", c)
	}
}

// P4: POW with non-integer / negative exponent + MOD on floats (static path).
func TestProbe_PowModStatic(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"nums": {}})

	if _, err := d.Exec(testCtx,
		"INSERT INTO nums (id, p_half, p_neg, m) VALUES (1, POW(2,0.5), POW(2,-1), MOD(5.5,2))"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	res, err := d.Exec(testCtx, "SELECT p_half, p_neg, m FROM nums WHERE id = 1")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	ph := toFloat(res.Rows[0]["p_half"])
	pn := toFloat(res.Rows[0]["p_neg"])
	m := toFloat(res.Rows[0]["m"])
	t.Logf("POW(2,0.5)=%v POW(2,-1)=%v MOD(5.5,2)=%v", ph, pn, m)
	if ph < 1.41 || ph > 1.42 {
		t.Errorf("LIKELY BUG: POW(2,0.5) got %v want ~1.414", ph)
	}
	if pn != 0.5 {
		t.Errorf("LIKELY BUG: POW(2,-1) got %v want 0.5", pn)
	}
	if m != 1.5 {
		t.Errorf("LIKELY BUG: MOD(5.5,2) got %v want 1.5", m)
	}
}

// P5: ROUND half-away-from-zero (MySQL) vs banker's rounding (Mongo $round).
func TestProbe_RoundHalf(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"round_test": {
		{"_id": 1, "v": 2.5},
		{"_id": 2, "v": 3.5},
		{"_id": 3, "v": 0.5},
	}})

	res, err := d.Exec(testCtx, "SELECT _id, ROUND(v, 0) AS r FROM round_test")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	got := map[int64]float64{}
	for _, r := range res.Rows {
		got[toInt64(r["_id"])] = toFloat(r["r"])
	}
	t.Logf("ROUND results: 2.5->%v 3.5->%v 0.5->%v", got[1], got[2], got[3])
	// MySQL ROUND: round half away from zero.
	if got[1] != 3 {
		t.Errorf("DIVERGENCE: ROUND(2.5) got %v want 3 (MySQL); Mongo $round uses banker's rounding", got[1])
	}
	if got[3] != 1 {
		t.Errorf("DIVERGENCE: ROUND(0.5) got %v want 1 (MySQL)", got[3])
	}
}

// P6: NOT / != three-valued logic with NULL field (gary has age=NULL).
func TestProbe_NullThreeValuedLogic(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	t.Run("not_eq", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT name FROM users WHERE NOT (age = 25)")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		got := collectStr(res.Rows, "name")
		// MySQL: NOT(NULL=25) = NULL => gary excluded; bob (25) excluded.
		want := []string{"alice", "carol", "dave", "erin", "frank"}
		t.Logf("WHERE NOT(age=25) -> %v", got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("DIVERGENCE: NOT(age=25) got %v want %v (NULL age must be excluded)", got, want)
		}
	})

	t.Run("ne", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT name FROM users WHERE age != 25")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		got := collectStr(res.Rows, "name")
		want := []string{"alice", "carol", "dave", "erin", "frank"}
		t.Logf("WHERE age!=25 -> %v", got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("DIVERGENCE: age!=25 got %v want %v (NULL age must be excluded)", got, want)
		}
	})
}

// P7: LIKE default case-insensitivity (MySQL *_ci collation).
func TestProbe_LikeCaseInsensitive(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	res, err := d.Exec(testCtx, "SELECT name FROM users WHERE name LIKE 'A%'")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	got := collectStr(res.Rows, "name")
	t.Logf("WHERE name LIKE 'A%%' -> %v", got)
	// MySQL default collation is case-insensitive: 'A%' matches 'alice'.
	if len(got) != 1 || got[0] != "alice" {
		t.Errorf("DIVERGENCE: LIKE 'A%%' got %v want [alice] (MySQL LIKE is case-insensitive by default)", got)
	}
}

// P8: JOIN projection of two columns sharing the same leaf name (a.x, b.x).
func TestProbe_JoinSameLeafName(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"a_tbl": {{"_id": 1, "x": "AX"}},
		"b_tbl": {{"_id": 10, "x": "BX", "aid": 1}},
	})

	res, err := d.Exec(testCtx,
		"SELECT a.x AS ax, b.x AS bx FROM a_tbl a JOIN b_tbl b ON a._id = b.aid")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 joined row, got %d (%v)", len(res.Rows), res.Rows)
	}
	row := res.Rows[0]
	t.Logf("joined row = %v", row)
	ax, _ := row["ax"].(string)
	bx, _ := row["bx"].(string)
	if ax != "AX" {
		t.Errorf("LIKELY BUG: a.x AS ax got %q want \"AX\" (leaf-name collision may drop it)", ax)
	}
	if bx != "BX" {
		t.Errorf("LIKELY BUG: b.x AS bx got %q want \"BX\"", bx)
	}
}

// P8b: JOIN projection a.x, b.x WITHOUT aliases (raw collision path).
func TestProbe_JoinSameLeafNameNoAlias(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"a_tbl": {{"_id": 1, "x": "AX"}},
		"b_tbl": {{"_id": 10, "x": "BX", "aid": 1}},
	})

	res, err := d.Exec(testCtx,
		"SELECT a.x, b.x FROM a_tbl a JOIN b_tbl b ON a._id = b.aid")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	row := res.Rows[0]
	t.Logf("joined row (no alias) = %v", row)
	// Both values must survive in some distinct form.
	foundAX, foundBX := false, false
	for _, v := range row {
		if s, ok := v.(string); ok {
			if s == "AX" {
				foundAX = true
			}
			if s == "BX" {
				foundBX = true
			}
		}
	}
	if !foundAX || !foundBX {
		t.Errorf("LIKELY BUG: a.x/b.x leaf-name collision dropped a value: row=%v (AX=%v BX=%v)", row, foundAX, foundBX)
	}
}

// P9: LIMIT 0 must return zero rows.
func TestProbe_LimitZero(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	res, err := d.Exec(testCtx, "SELECT name FROM users LIMIT 0")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	t.Logf("LIMIT 0 -> %d rows", len(res.Rows))
	if len(res.Rows) != 0 {
		t.Errorf("LIKELY BUG: LIMIT 0 got %d rows want 0", len(res.Rows))
	}
}

// P10: aggregate over empty set — MySQL returns one row (NULL); Mongo returns none.
func TestProbe_AggregateEmptySet(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	res, err := d.Exec(testCtx, "SELECT SUM(age) AS s, COUNT(*) AS n FROM users WHERE city = 'NOPE'")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	t.Logf("aggregate over empty set -> %d rows: %v", len(res.Rows), res.Rows)
	// MySQL: 1 row with SUM=NULL, COUNT=0.
	if len(res.Rows) != 1 {
		t.Errorf("DIVERGENCE: aggregate over empty set got %d rows want 1 (MySQL returns one row with NULL/0)", len(res.Rows))
	}
}

// P12: COUNT(DISTINCT col) — the DISTINCT modifier must be honoured.
func TestProbe_CountDistinct(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	// 7 users across 4 distinct cities: BJ, SH, GZ, SZ.
	res, err := d.Exec(testCtx, "SELECT COUNT(DISTINCT city) AS c FROM users")
	if err != nil {
		t.Logf("COUNT(DISTINCT city) error: %v", err)
		t.Errorf("LIKELY BUG: COUNT(DISTINCT city) returned error instead of 4: %v", err)
		return
	}
	c := toInt64(res.Rows[0]["c"])
	t.Logf("COUNT(DISTINCT city) = %d", c)
	if c != 4 {
		t.Errorf("LIKELY BUG: COUNT(DISTINCT city) got %d want 4 (DISTINCT modifier ignored?)", c)
	}
}

// P13: RIGHT JOIN — characterize actual behaviour (expected: error or correct
// right-outer semantics; suspected: silently treated as INNER JOIN).
func TestProbe_RightJoinBehaviour(t *testing.T) {
	d := newDriver(t)
	// order 105 has user_id 999 with no matching user; user 6 (frank) has no order.
	seed(t, d, map[string][]bson.M{
		"users": userFixture(),
		"orders": {
			{"_id": 100, "user_id": 1, "amount": int32(50)},
			{"_id": 105, "user_id": 999, "amount": int32(10)}, // no matching user
		},
	})

	res, err := d.Exec(testCtx,
		"SELECT orders.amount AS amt FROM users RIGHT JOIN orders ON users._id = orders.user_id")
	if err != nil {
		t.Logf("RIGHT JOIN cleanly rejected with error: %v", err)
		return
	}
	t.Logf("RIGHT JOIN accepted, %d rows: %v", len(res.Rows), res.Rows)
	// MySQL RIGHT JOIN keeps every row of the right table (orders) => 2 rows
	// (amount 50 and the unmatched amount 10). INNER JOIN would drop the 10.
	if len(res.Rows) != 2 {
		t.Errorf("LIKELY BUG: RIGHT JOIN got %d rows want 2 (every right-table row preserved); appears treated as INNER JOIN", len(res.Rows))
	}
}

// P11: robustness — unsupported syntax must return an error, never panic.
func TestProbe_UnsupportedNoPanic(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	cases := []string{
		"SELECT * FROM users WHERE city IN (SELECT city FROM users)",
		"SELECT name FROM users UNION SELECT name FROM users",
		"SELECT * FROM (SELECT * FROM users) t",
		"SELECT name FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u._id)",
		"SELECT ROW_NUMBER() OVER (ORDER BY age) AS rn FROM users",
		"SELECT * FROM users RIGHT JOIN orders ON users._id = orders.user_id",
	}
	for _, sql := range cases {
		func(sql string) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("PANIC on unsupported SQL %q: %v", sql, r)
				}
			}()
			_, err := d.Exec(testCtx, sql)
			if err == nil {
				t.Logf("NOTE: unsupported SQL accepted without error: %q", sql)
			} else {
				t.Logf("ok (clean error) %q -> %v", sql, err)
			}
		}(sql)
	}
}
