package tests

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestSelect_OrderLimitOffset checks ORDER BY / LIMIT / OFFSET and projection.
func TestSelect_OrderLimitOffset(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	t.Run("order_asc", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT name FROM users WHERE age IS NOT NULL ORDER BY age ASC")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		got := names(res.Rows)
		want := []string{"frank", "bob", "erin", "alice", "dave", "carol"}
		equalStrings(t, got, want)
	})

	t.Run("order_desc", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT name FROM users WHERE age IS NOT NULL ORDER BY age DESC")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		got := names(res.Rows)
		want := []string{"carol", "dave", "alice", "erin", "bob", "frank"}
		equalStrings(t, got, want)
	})

	t.Run("limit", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT name FROM users WHERE age IS NOT NULL ORDER BY age ASC LIMIT 2")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		got := names(res.Rows)
		want := []string{"frank", "bob"}
		equalStrings(t, got, want)
	})

	t.Run("limit_offset", func(t *testing.T) {
		res, err := d.Exec(testCtx, "SELECT name FROM users WHERE age IS NOT NULL ORDER BY age ASC LIMIT 2 OFFSET 2")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		got := names(res.Rows)
		want := []string{"erin", "alice"}
		equalStrings(t, got, want)
	})
}

// TestSelect_Distinct verifies SELECT DISTINCT col FROM ...
func TestSelect_Distinct(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": userFixture()})

	res, err := d.Exec(testCtx, "SELECT DISTINCT city FROM users")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	cities := map[string]bool{}
	for _, r := range res.Rows {
		if c, ok := r["city"].(string); ok {
			cities[c] = true
		}
	}
	want := []string{"BJ", "SH", "GZ", "SZ"}
	if len(cities) != len(want) {
		t.Fatalf("expected %d distinct cities, got %d (%v)", len(want), len(cities), cities)
	}
	for _, w := range want {
		if !cities[w] {
			t.Fatalf("missing city %s in %v", w, cities)
		}
	}
}
