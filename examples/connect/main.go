// Command connect demonstrates the self-dialing URI mode: driver.Connect(uri).
//
// Use this for standalone programs / CLIs / quick scripts where mongosql should
// own the connection. The database is taken from the URI path (the segment after
// the host). Connect dials the client and pings it; the returned Driver owns it,
// so Close disconnects it.
//
//	go run ./examples/connect "mongodb://localhost:27017/mydb" "SELECT * FROM users LIMIT 5"
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aura-studio/mongosql/driver"
)

func main() {
	uri, sql := args()
	ctx := context.Background()

	// mongosql dials and owns the connection; the db comes from the URI path.
	d, err := driver.Connect(ctx, uri)
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

func args() (uri, sql string) {
	uri, sql = "mongodb://localhost:27017/test", "SELECT * FROM users LIMIT 5"
	if len(os.Args) > 1 {
		uri = os.Args[1]
	}
	if len(os.Args) > 2 {
		sql = os.Args[2]
	}
	return uri, sql
}
