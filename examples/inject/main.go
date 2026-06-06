// Command inject demonstrates the injected-connection mode: driver.New(client.Database(dbName)).
//
// Use this when your program already owns a *mongo.Client (a shared connection
// pool) and wants to run SQL through mongosql over it — e.g. embedding mongosql
// in another service. mongosql does NOT dial and Close is a no-op, so your pool
// is left intact; YOU own the client's lifecycle.
//
//	go run ./examples/inject "mongodb://localhost:27017" mydb "SELECT * FROM users LIMIT 5"
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/aura-studio/mongosql/driver"
)

func main() {
	uri, dbName, sql := args()
	ctx := context.Background()

	// You dial and own the client (typically shared across your app).
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(ctx) }() // your client → you close it

	// Inject the existing connection's database. mongosql will not dial or close
	// it.
	d, err := driver.New(client.Database(dbName))
	if err != nil {
		log.Fatalf("driver.New: %v", err)
	}
	defer func() { _ = d.Close(ctx) }() // no-op for injected connections

	res, err := d.Exec(ctx, sql)
	if err != nil {
		log.Fatalf("exec: %v", err)
	}
	fmt.Printf("kind=%s rows=%d insertedIds=%v matched=%d modified=%d deleted=%d\n",
		res.Kind, len(res.Rows), res.InsertedIDs, res.MatchedCount, res.ModifiedCount, res.DeletedCount)
}

func args() (uri, db, sql string) {
	uri, db, sql = "mongodb://localhost:27017", "test", "SELECT * FROM users LIMIT 5"
	if len(os.Args) > 1 {
		uri = os.Args[1]
	}
	if len(os.Args) > 2 {
		db = os.Args[2]
	}
	if len(os.Args) > 3 {
		sql = os.Args[3]
	}
	return uri, db, sql
}
