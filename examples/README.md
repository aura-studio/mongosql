# mongosql examples

Two ways to construct a `driver.Driver`, differing only in **who owns the
MongoDB connection**.

| | `driver.New(client, dbName)` — [examples/inject](inject) | `driver.Connect(ctx, uri, dbName)` — [examples/connect](connect) |
|---|---|---|
| Connection | **injected** — you already have a `*mongo.Client` | **dialed** by mongosql from the URI |
| Lifecycle | you own it; `Driver.Close` is a **no-op** | mongosql owns it; `Driver.Close` disconnects |
| Use it for | embedding in a service that shares one pool (e.g. tango) | standalone programs / CLIs / scripts |

Both return the same `*driver.Driver`; call `Exec(ctx, sql)` the same way.

## Run

```bash
# inject: you dial the client, mongosql shares it (won't close it)
go run ./examples/inject  "mongodb://localhost:27017" mydb "SELECT * FROM users LIMIT 5"

# connect: mongosql dials + owns the client (Close disconnects it)
go run ./examples/connect "mongodb://localhost:27017" mydb "INSERT INTO users (name) VALUES ('alice')"
```

Args are `[uri] [database] [sql]` (all optional; default to
`mongodb://localhost:27017` / `test` / a sample `SELECT`).

## Injected mode (the gist)

```go
client, _ := mongo.Connect(options.Client().ApplyURI(uri)) // you own this
defer client.Disconnect(ctx)

d, _ := driver.New(client, "mydb") // share it; no second dial
defer d.Close(ctx)                 // no-op — your pool is untouched

res, _ := d.Exec(ctx, "SELECT * FROM users LIMIT 5")
```

## URI mode (the gist)

```go
d, _ := driver.Connect(ctx, "mongodb://localhost:27017", "mydb") // mongosql dials + owns
defer d.Close(ctx)                                               // disconnects it

res, _ := d.Exec(ctx, "SELECT * FROM users LIMIT 5")
```
