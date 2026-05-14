package tests

import (
	"reflect"
	"sort"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// userFixture returns a small set of users with deterministic _id values.
func userFixture() []bson.M {
	return []bson.M{
		{"_id": 1, "name": "alice", "age": int32(30), "city": "BJ", "vip": true},
		{"_id": 2, "name": "bob", "age": int32(25), "city": "SH", "vip": false},
		{"_id": 3, "name": "carol", "age": int32(40), "city": "BJ", "vip": true},
		{"_id": 4, "name": "dave", "age": int32(35), "city": "GZ", "vip": false},
		{"_id": 5, "name": "erin", "age": int32(28), "city": "SH", "vip": true},
		{"_id": 6, "name": "frank", "age": int32(22), "city": "BJ", "vip": false},
		{"_id": 7, "name": "gary", "age": nil, "city": "SZ", "vip": false},
	}
}

func orderFixture() []bson.M {
	return []bson.M{
		{"_id": 100, "user_id": 1, "amount": int32(50)},
		{"_id": 101, "user_id": 1, "amount": int32(150)},
		{"_id": 102, "user_id": 2, "amount": int32(75)},
		{"_id": 103, "user_id": 3, "amount": int32(200)},
		{"_id": 104, "user_id": 5, "amount": int32(20)},
	}
}

// names extracts the "name" column from a result row set.
func names(rows []map[string]interface{}) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if v, ok := r["name"]; ok {
			out = append(out, asString(v))
		}
	}
	return out
}

func asString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	}
	return ""
}

func sortedNames(rows []map[string]interface{}) []string {
	n := names(rows)
	sort.Strings(n)
	return n
}

func equalStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
