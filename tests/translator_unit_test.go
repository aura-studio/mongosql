package tests

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/example/mongodb-sql-driver/translator"
	"github.com/example/mongodb-sql-driver/translator/stmt"
)

func newTranslator(t *testing.T) *translator.Translator {
	t.Helper()
	tr, err := translator.New()
	if err != nil {
		t.Fatalf("new translator: %v", err)
	}
	return tr
}

func TestTranslateSelectFind(t *testing.T) {
	tr := newTranslator(t)

	st, err := tr.Translate("SELECT name FROM users WHERE city = 'BJ' ORDER BY age DESC LIMIT 2 OFFSET 1")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}

	find, ok := st.(*stmt.FindStmt)
	if !ok {
		t.Fatalf("expected FindStmt, got %T", st)
	}
	if find.Collection != "users" {
		t.Fatalf("collection: got %q want %q", find.Collection, "users")
	}
	if !reflect.DeepEqual(find.Filter, bson.M{"city": bson.M{"$eq": "BJ"}}) {
		t.Fatalf("filter: got %#v", find.Filter)
	}
	if !reflect.DeepEqual(find.Projection, bson.M{"name": 1, "_id": 0}) {
		t.Fatalf("projection: got %#v", find.Projection)
	}
	if !reflect.DeepEqual(find.Sort, bson.D{{Key: "age", Value: -1}}) {
		t.Fatalf("sort: got %#v", find.Sort)
	}
	if find.Limit != 2 || find.Skip != 1 {
		t.Fatalf("limit/skip: got (%d, %d)", find.Limit, find.Skip)
	}
}

func TestTranslateSelectDistinct(t *testing.T) {
	tr := newTranslator(t)

	st, err := tr.Translate("SELECT DISTINCT city FROM users")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}

	find, ok := st.(*stmt.FindStmt)
	if !ok {
		t.Fatalf("expected FindStmt, got %T", st)
	}
	if find.Distinct != "city" {
		t.Fatalf("distinct: got %q want %q", find.Distinct, "city")
	}
}

func TestTranslateSelectJoinAggregate(t *testing.T) {
	tr := newTranslator(t)

	st, err := tr.Translate(
		"SELECT users.name AS user_name, orders.amount AS amt " +
			"FROM users JOIN orders ON users._id = orders.user_id " +
			"ORDER BY orders.amount ASC",
	)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}

	agg, ok := st.(*stmt.AggregateStmt)
	if !ok {
		t.Fatalf("expected AggregateStmt, got %T", st)
	}
	if agg.Collection != "users" {
		t.Fatalf("collection: got %q want %q", agg.Collection, "users")
	}

	want := []bson.M{
		{"$lookup": bson.M{
			"from":         "orders",
			"localField":   "_id",
			"foreignField": "user_id",
			"as":           "orders",
		}},
		{"$unwind": bson.M{
			"path":                       "$orders",
			"preserveNullAndEmptyArrays": false,
		}},
		{"$sort": bson.M{"orders.amount": 1}},
		{"$project": bson.M{
			"_id":       0,
			"user_name": "$name",
			"amt":       "$orders.amount",
		}},
	}
	if !reflect.DeepEqual(agg.Pipeline, want) {
		t.Fatalf("pipeline:\n got: %#v\nwant: %#v", agg.Pipeline, want)
	}
}

func TestTranslateSelectSourceForms(t *testing.T) {
	tr := newTranslator(t)

	cases := []struct {
		name           string
		sql            string
		wantCollection string
	}{
		{name: "plain table", sql: "SELECT name FROM users", wantCollection: "users"},
		{name: "aliased table", sql: "SELECT name FROM users u", wantCollection: "users"},
		{name: "qualified table", sql: "SELECT name FROM app.users", wantCollection: "users"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := tr.Translate(tc.sql)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			find, ok := st.(*stmt.FindStmt)
			if !ok {
				t.Fatalf("expected FindStmt, got %T", st)
			}
			if find.Collection != tc.wantCollection {
				t.Fatalf("collection: got %q want %q", find.Collection, tc.wantCollection)
			}
		})
	}
}
