package tests

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestInsertUpdateDelete(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{"users": nil})

	t.Run("insert_single", func(t *testing.T) {
		res, err := d.Exec(testCtx,
			"INSERT INTO users (_id, name, age, city, vip) VALUES (1, 'alice', 30, 'BJ', true)")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		if len(res.InsertedIDs) != 1 {
			t.Fatalf("expected 1 inserted, got %v", res.InsertedIDs)
		}
	})

	t.Run("insert_multi", func(t *testing.T) {
		res, err := d.Exec(testCtx,
			"INSERT INTO users (_id, name, age, city, vip) VALUES (2, 'bob', 25, 'SH', false), (3, 'carol', 40, 'BJ', true)")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		if len(res.InsertedIDs) != 2 {
			t.Fatalf("expected 2 inserted, got %v", res.InsertedIDs)
		}
	})

	t.Run("update", func(t *testing.T) {
		res, err := d.Exec(testCtx, "UPDATE users SET city = 'TJ' WHERE name = 'bob'")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		if res.MatchedCount != 1 || res.ModifiedCount != 1 {
			t.Fatalf("expected matched=1 modified=1, got matched=%d modified=%d",
				res.MatchedCount, res.ModifiedCount)
		}

		check, err := d.Exec(testCtx, "SELECT city FROM users WHERE name = 'bob'")
		if err != nil {
			t.Fatalf("exec select: %v", err)
		}
		if len(check.Rows) != 1 || check.Rows[0]["city"] != "TJ" {
			t.Fatalf("expected city=TJ, got %v", check.Rows)
		}
	})

	t.Run("delete", func(t *testing.T) {
		res, err := d.Exec(testCtx, "DELETE FROM users WHERE city = 'BJ'")
		if err != nil {
			t.Fatalf("exec: %v", err)
		}
		if res.DeletedCount != 2 {
			t.Fatalf("expected 2 deleted, got %d", res.DeletedCount)
		}

		check, err := d.Exec(testCtx, "SELECT name FROM users")
		if err != nil {
			t.Fatalf("exec select: %v", err)
		}
		if len(check.Rows) != 1 {
			t.Fatalf("expected 1 remaining, got %v", check.Rows)
		}
	})
}
