package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"go.mongodb.org/mongo-driver/v2/bson"

	sqldriver "github.com/example/mongodb-sql-driver/driver"
)

// resultToMySQL converts a driver.Result into a *mysql.Result that the MySQL
// wire protocol can serialise back to the client.
func resultToMySQL(r *sqldriver.Result) (*mysql.Result, error) {
	switch r.Kind {
	case "select":
		return rowsToResult(r.Rows)
	case "insert":
		return &mysql.Result{
			Status:       mysql.SERVER_STATUS_AUTOCOMMIT,
			AffectedRows: uint64(len(r.InsertedIDs)),
		}, nil
	case "update":
		return &mysql.Result{
			Status:       mysql.SERVER_STATUS_AUTOCOMMIT,
			AffectedRows: uint64(r.ModifiedCount),
		}, nil
	case "delete":
		return &mysql.Result{
			Status:       mysql.SERVER_STATUS_AUTOCOMMIT,
			AffectedRows: uint64(r.DeletedCount),
		}, nil
	}
	return emptyOK(), nil
}

// rowsToResult collects all column names appearing in the rows, normalises
// each cell to a MySQL-friendly value and builds a text-protocol resultset.
func rowsToResult(rows []map[string]interface{}) (*mysql.Result, error) {
	cols := collectColumns(rows)
	values := make([][]any, len(rows))
	for i, row := range rows {
		rec := make([]any, len(cols))
		for j, c := range cols {
			rec[j] = mysqlValue(row[c])
		}
		values[i] = rec
	}
	return rowsResult(cols, values), nil
}

// rowsResult wraps the given column names and pre-converted rows in a text
// resultset.
func rowsResult(cols []string, values [][]any) *mysql.Result {
	rs, err := mysql.BuildSimpleResultset(cols, values, false)
	if err != nil {
		return emptyOK()
	}
	r := mysql.NewResult(rs)
	r.Status = mysql.SERVER_STATUS_AUTOCOMMIT
	return r
}

// emptyResult is a zero-row resultset with the given column names — used for
// SHOW commands where the schema matters but we have no data.
func emptyResult(cols []string) *mysql.Result {
	return rowsResult(cols, nil)
}

// collectColumns returns the union of keys across rows. `_id` is placed
// first (when present); the rest are alphabetically sorted for determinism
// since map iteration order is randomised.
func collectColumns(rows []map[string]interface{}) []string {
	seen := make(map[string]struct{})
	for _, r := range rows {
		for k := range r {
			seen[k] = struct{}{}
		}
	}
	cols := make([]string, 0, len(seen))
	hasID := false
	for k := range seen {
		if k == "_id" {
			hasID = true
			continue
		}
		cols = append(cols, k)
	}
	sort.Strings(cols)
	if hasID {
		cols = append([]string{"_id"}, cols...)
	}
	if len(cols) == 0 {
		cols = []string{"(empty)"}
	}
	return cols
}

// mysqlValue normalises a BSON / Mongo decoded value into a type that
// go-mysql's text-resultset writer understands. Nested documents and arrays
// are stringified as their canonical JSON-ish form so they at least show up
// in the UI rather than crashing the encoder.
func mysqlValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, bool, []byte:
		return x
	case bson.ObjectID:
		return x.Hex()
	case time.Time:
		return x.UTC().Format("2006-01-02 15:04:05")
	case bson.D, bson.M, bson.A, []any, map[string]any:
		b, err := bson.MarshalExtJSON(struct {
			V any `bson:"v"`
		}{V: x}, false, false)
		if err != nil {
			return fmt.Sprintf("%v", x)
		}
		// MarshalExtJSON emits {"v": ...}; trim wrapper.
		s := string(b)
		if len(s) > 7 {
			return s[6 : len(s)-1]
		}
		return s
	default:
		return fmt.Sprintf("%v", x)
	}
}
