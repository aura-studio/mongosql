package tests

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestSelect_BasicCoverage exercises a wide variety of SQL syntax against MongoDB
// to ensure the translator + driver chain returns the expected row sets.
func TestSelect_BasicCoverage(t *testing.T) {
	d := newDriver(t)
	seed(t, d, map[string][]bson.M{
		"users":  userFixture(),
		"orders": orderFixture(),
	})

	cases := []struct {
		name string
		sql  string
		want []string // expected sorted "name" values
	}{
		{
			name: "select_star",
			sql:  "SELECT * FROM users",
			want: []string{"alice", "bob", "carol", "dave", "erin", "frank", "gary"},
		},
		{
			name: "select_columns",
			sql:  "SELECT name FROM users",
			want: []string{"alice", "bob", "carol", "dave", "erin", "frank", "gary"},
		},
		{
			name: "where_eq_string",
			sql:  "SELECT name FROM users WHERE city = 'BJ'",
			want: []string{"alice", "carol", "frank"},
		},
		{
			name: "where_eq_int",
			sql:  "SELECT name FROM users WHERE age = 30",
			want: []string{"alice"},
		},
		{
			name: "where_neq",
			sql:  "SELECT name FROM users WHERE city != 'BJ'",
			want: []string{"bob", "dave", "erin", "gary"},
		},
		{
			name: "where_lt",
			sql:  "SELECT name FROM users WHERE age < 30",
			want: []string{"bob", "erin", "frank"},
		},
		{
			name: "where_lte",
			sql:  "SELECT name FROM users WHERE age <= 30",
			want: []string{"alice", "bob", "erin", "frank"},
		},
		{
			name: "where_gt",
			sql:  "SELECT name FROM users WHERE age > 30",
			want: []string{"carol", "dave"},
		},
		{
			name: "where_gte",
			sql:  "SELECT name FROM users WHERE age >= 30",
			want: []string{"alice", "carol", "dave"},
		},
		{
			name: "where_and",
			sql:  "SELECT name FROM users WHERE city = 'BJ' AND age > 25",
			want: []string{"alice", "carol"},
		},
		{
			name: "where_or",
			sql:  "SELECT name FROM users WHERE city = 'GZ' OR age = 22",
			want: []string{"dave", "frank"},
		},
		{
			name: "where_in",
			sql:  "SELECT name FROM users WHERE city IN ('BJ', 'GZ')",
			want: []string{"alice", "carol", "dave", "frank"},
		},
		{
			name: "where_not_in",
			sql:  "SELECT name FROM users WHERE city NOT IN ('BJ', 'GZ')",
			want: []string{"bob", "erin", "gary"},
		},
		{
			name: "where_like_prefix",
			sql:  "SELECT name FROM users WHERE name LIKE 'a%'",
			want: []string{"alice"},
		},
		{
			name: "where_like_suffix",
			sql:  "SELECT name FROM users WHERE name LIKE '%y'",
			want: []string{"gary"},
		},
		{
			name: "where_like_underscore",
			sql:  "SELECT name FROM users WHERE name LIKE 'b_b'",
			want: []string{"bob"},
		},
		{
			name: "where_not_like",
			sql:  "SELECT name FROM users WHERE name NOT LIKE '%a%' AND name NOT LIKE '%r%'",
			want: []string{"bob"},
		},
		{
			name: "where_between",
			sql:  "SELECT name FROM users WHERE age BETWEEN 25 AND 30",
			want: []string{"alice", "bob", "erin"},
		},
		{
			name: "where_not_between",
			sql:  "SELECT name FROM users WHERE age NOT BETWEEN 25 AND 30",
			want: []string{"carol", "dave", "frank"},
		},
		{
			name: "where_is_null",
			sql:  "SELECT name FROM users WHERE age IS NULL",
			want: []string{"gary"},
		},
		{
			name: "where_is_not_null",
			sql:  "SELECT name FROM users WHERE age IS NOT NULL",
			want: []string{"alice", "bob", "carol", "dave", "erin", "frank"},
		},
		{
			name: "where_bool_true",
			sql:  "SELECT name FROM users WHERE vip = true",
			want: []string{"alice", "carol", "erin"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := d.Exec(testCtx, tc.sql)
			if err != nil {
				t.Fatalf("exec: %v\nSQL: %s", err, tc.sql)
			}
			equalStrings(t, sortedNames(res.Rows), tc.want)
		})
	}
}
