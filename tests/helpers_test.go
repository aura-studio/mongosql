package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/aura-studio/mongosql/driver"
)

const (
	defaultURI = "mongodb://localhost:27017"
	dbName     = "sqlmongo_test"
)

var testCtx = context.Background()

// newDriver dials the test MongoDB and returns an isolated Driver per test.
func newDriver(t *testing.T) *driver.Driver {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = defaultURI
	}
	ctx, cancel := context.WithTimeout(testCtx, 5*time.Second)
	defer cancel()
	d, err := driver.Connect(ctx, uri, dbName)
	if err != nil {
		t.Skipf("skipping: cannot connect to MongoDB at %s: %v", uri, err)
	}
	t.Cleanup(func() {
		_ = d.Close(testCtx)
	})
	return d
}

// seed wipes the named collections in the test DB and inserts the supplied docs.
func seed(t *testing.T, d *driver.Driver, data map[string][]bson.M) {
	t.Helper()
	for coll := range data {
		if err := d.DB().Collection(coll).Drop(testCtx); err != nil {
			t.Fatalf("drop %s: %v", coll, err)
		}
	}
	for coll, docs := range data {
		if len(docs) == 0 {
			continue
		}
		generic := make([]interface{}, len(docs))
		for i, doc := range docs {
			generic[i] = doc
		}
		if _, err := d.DB().Collection(coll).InsertMany(testCtx, generic); err != nil {
			t.Fatalf("insert into %s: %v", coll, err)
		}
	}
}
