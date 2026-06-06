// Command connect demonstrates the self-dialing URI mode: driver.Connect(uri, db).
//
// Use this for standalone programs / CLIs / quick scripts where mongosql should
// own the connection. Connect dials the client and pings it; the returned Driver
// owns it, so Close disconnects it.
//
//	go run ./examples/connect "mongodb://localhost:27017" mydb "SELECT * FROM users LIMIT 5"
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aura-studio/mongosql/driver"
)

func main() {
	uri, dbName, sql := args()
	ctx := context.Background()

	// mongosql dials and owns the connection.
	d, err := driver.Connect(ctx, uri, dbName)
	if err != nil {
		log.Fatalf("driver.Connect: %v", err)
	}
	defer func() { _ = d.Close(ctx) }() // disconnects the client mongosql dialed

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
