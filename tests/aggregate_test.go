package tests

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestAggregate_GroupBy exercises GROUP BY + COUNT/SUM/AVG/MIN/MAX and HAVING.
func TestAggregate_GroupBy(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	t.Run("count_star_group_by", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT city, COUNT(*) AS n FROM users GROUP BY city")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		got := map[string]int64{}
		for _, r := range res.Rows {
			city, _ := r["city"].(string)
			got[city] = toInt64(r["n"])
		}
		want := map[string]int64{"BJ": 3, "SH": 2, "GZ": 1, "SZ": 1}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("city %s: got %d, want %d (full=%v)", k, got[k], v, got)
			}
		}
	})

	t.Run("sum_avg_min_max", func(t *testing.T) {
		res, err := d.Exec(testCtx,
			"SELECT city, SUM(age) AS s, AVG(age) AS a, MIN(age) AS mn, MAX(age) AS mx "+
				"FROM users WHERE age IS NOT NULL GROUP BY city")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		bj := findRow(res.Rows, "city", "BJ")
		if bj == nil {
			t.Fatalf("missing BJ row in %v", res.Rows)
		}
		if got := toInt64(bj["s"]); got != 92 { // 30+40+22
			t.Fatalf("BJ SUM(age) got %d want 92", got)
		}
		if got := toInt64(bj["mn"]); got != 22 {
			t.Fatalf("BJ MIN(age) got %d want 22", got)
		}
		if got := toInt64(bj["mx"]); got != 40 {
			t.Fatalf("BJ MAX(age) got %d want 40", got)
		}
	})

	t.Run("having", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT city, COUNT(*) AS n FROM users GROUP BY city HAVING n > 1")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		got := map[string]int64{}
		for _, r := range res.Rows {
			city, _ := r["city"].(string)
			got[city] = toInt64(r["n"])
		}
		want := map[string]int64{"BJ": 3, "SH": 2}
		if len(got) != len(want) {
			t.Fatalf("got=%v want=%v", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("city %s: got %d want %d", k, got[k], v)
			}
		}
	})

	t.Run("count_star_no_group", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT COUNT(*) AS n FROM users")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		if len(res.Rows) != 1 || toInt64(res.Rows[0]["n"]) != 7 {
			t.Fatalf("unexpected COUNT(*) result: %v", res.Rows)
		}
	})
}

// TestAggregate_Join verifies a single $lookup / equi-JOIN translation.
func TestAggregate_Join(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"users":  userFixture(),
		"orders": orderFixture(),
	})

	res, err := d.Exec(testCtx,
		"SELECT users.name AS user_name, orders.amount AS amt "+
			"FROM users JOIN orders ON users._id = orders.user_id "+
			"ORDER BY orders.amount ASC")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if len(res.Rows) != 5 {
		t.Fatalf("expected 5 joined rows, got %d (%v)", len(res.Rows), res.Rows)
	}
	first := res.Rows[0]
	if name, _ := first["user_name"].(string); name != "erin" {
		t.Fatalf("expected first joined user erin, got %v", first)
	}
	if amt := toInt64(first["amt"]); amt != 20 {
		t.Fatalf("expected first joined amount 20, got %v (%v)", amt, first)
	}
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func findRow(rows []map[string]interface{}, key string, val interface{}) map[string]interface{} {
	for _, r := range rows {
		if r[key] == val {
			return r
		}
	}
	return nil
}
