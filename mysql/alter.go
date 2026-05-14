package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongoopt "go.mongodb.org/mongo-driver/v2/mongo/options"
	"vitess.io/vitess/go/vt/sqlparser"

	"github.com/example/mongodb-sql-driver/driver"
)

// handleAlterTable handles ALTER TABLE with various ADD/DROP/MODIFY/RENAME
// options. It mutates the stored schema, applies physical operations to
// MongoDB (rename collection, $unset for dropped columns, index changes),
// and tries to keep schemaless tables working too.
func (h *handler) handleAlterTable(q string) (*mysql.Result, error) {
	parser, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "ALTER TABLE: parser init: "+err.Error())
	}
	parsed, err := parser.Parse(q)
	if err != nil {
		// Best-effort no-op so clients don't fail on unsupported syntax.
		log.Printf("ALTER TABLE parse failed, treating as no-op: %v", err)
		return emptyOK(), nil
	}
	alter, ok := parsed.(*sqlparser.AlterTable)
	if !ok {
		return emptyOK(), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := h.d.DB()
	tableName := alter.Table.Name.String()
	coll := db.Collection(tableName)

	// Load existing schema (may be nil for schemaless tables).
	schema := h.d.Schemas.Get(ctx, db, tableName)
	schemaChanged := false

	for _, opt := range alter.AlterOptions {
		switch o := opt.(type) {
		case *sqlparser.AddColumns:
			for _, col := range o.Columns {
				cd := columnDefFromVitess(col)
				if schema != nil {
					schema.Columns = append(schema.Columns, cd)
					schemaChanged = true
				}
				// Backfill the new column with its default value across existing docs.
				if cd.HasDefault {
					val := evalDefaultLiteral(cd)
					if val != nil {
						_, err := coll.UpdateMany(ctx,
							bson.M{cd.Name: bson.M{"$exists": false}},
							bson.M{"$set": bson.M{cd.Name: val}})
						if err != nil {
							log.Printf("warning: backfill column %s on %s: %v", cd.Name, tableName, err)
						}
					}
				}
				log.Printf("alter %s: added column %s", tableName, cd.Name)
			}

		case *sqlparser.DropColumn:
			colName := o.Name.Name.String()
			// $unset across all docs.
			if _, err := coll.UpdateMany(ctx, bson.M{}, bson.M{"$unset": bson.M{colName: ""}}); err != nil {
				log.Printf("warning: drop column %s on %s: %v", colName, tableName, err)
			}
			if schema != nil {
				schema.Columns = removeColumn(schema.Columns, colName)
				schemaChanged = true
			}
			log.Printf("alter %s: dropped column %s", tableName, colName)

		case *sqlparser.ModifyColumn:
			cd := columnDefFromVitess(o.NewColDefinition)
			if schema != nil {
				schema.Columns = replaceColumn(schema.Columns, cd.Name, cd)
				schemaChanged = true
			}
			log.Printf("alter %s: modified column %s", tableName, cd.Name)

		case *sqlparser.ChangeColumn:
			oldName := o.OldColumn.Name.String()
			cd := columnDefFromVitess(o.NewColDefinition)
			if oldName != cd.Name {
				// Drop indexes covering the old column name first so $rename
				// doesn't trip a duplicate-key error (since renamed-away values
				// become missing/null in the index).
				var droppedIdx []driver.IndexDef
				if schema != nil {
					for _, si := range schema.Indexes {
						for _, c := range si.Columns {
							if strings.EqualFold(c, oldName) {
								if si.Name != "" {
									_ = coll.Indexes().DropOne(ctx, si.Name)
								}
								droppedIdx = append(droppedIdx, si)
								break
							}
						}
					}
				}

				if _, err := coll.UpdateMany(ctx,
					bson.M{oldName: bson.M{"$exists": true}},
					bson.M{"$rename": bson.M{oldName: cd.Name}}); err != nil {
					log.Printf("warning: rename column %s->%s on %s: %v", oldName, cd.Name, tableName, err)
				}

				// Recreate dropped indexes with the new column name.
				for _, si := range droppedIdx {
					for i, c := range si.Columns {
						if strings.EqualFold(c, oldName) {
							si.Columns[i] = cd.Name
						}
					}
					createIndexes(ctx, db, tableName, []driver.IndexDef{si})
					if schema != nil {
						schema.Indexes = replaceIndex(schema.Indexes, si)
					}
				}
			}
			if schema != nil {
				schema.Columns = replaceColumn(schema.Columns, oldName, cd)
				schemaChanged = true
			}
			log.Printf("alter %s: changed column %s -> %s", tableName, oldName, cd.Name)

		case *sqlparser.AddIndexDefinition:
			idx := indexDefFromVitess(o.IndexDefinition)
			createIndexes(ctx, db, tableName, []driver.IndexDef{idx})
			if schema != nil {
				schema.Indexes = append(schema.Indexes, idx)
				schemaChanged = true
			}

		case *sqlparser.DropKey:
			idxName := o.Name.String()
			if o.Type == sqlparser.PrimaryKeyType {
				idxName = "PRIMARY"
			}
			if idxName != "" && idxName != "PRIMARY" {
				if err := coll.Indexes().DropOne(ctx, idxName); err != nil {
					log.Printf("warning: drop index %s on %s: %v", idxName, tableName, err)
				}
			}
			if schema != nil {
				schema.Indexes = removeIndex(schema.Indexes, idxName)
				schemaChanged = true
			}

		case *sqlparser.RenameTableName:
			newName := o.Table.Name.String()
			if err := renameCollection(ctx, h, db.Name(), tableName, newName); err != nil {
				return nil, mysql.NewError(mysql.ER_UNKNOWN_ERROR, "ALTER TABLE RENAME: "+err.Error())
			}
			tableName = newName
			coll = db.Collection(newName)
			if schema != nil {
				schema.TableName = newName
				schemaChanged = true
			}

		default:
			// Unsupported option (CHARSET, ENGINE, COMMENT, etc.) — ignore silently.
			log.Printf("alter %s: ignoring unsupported option %T", tableName, o)
		}
	}

	if schema != nil && schemaChanged {
		if err := h.d.Schemas.Save(ctx, db, schema); err != nil {
			log.Printf("warning: save schema for %s: %v", tableName, err)
		}
	}
	return emptyOK(), nil
}

// handleRenameTable handles RENAME TABLE a TO b, c TO d ...
func (h *handler) handleRenameTable(q string) (*mysql.Result, error) {
	parser, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "RENAME TABLE: parser init: "+err.Error())
	}
	parsed, err := parser.Parse(q)
	if err != nil {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "RENAME TABLE: "+err.Error())
	}
	rt, ok := parsed.(*sqlparser.RenameTable)
	if !ok {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "RENAME TABLE: unexpected AST")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := h.d.DB()

	for _, pair := range rt.TablePairs {
		from := pair.FromTable.Name.String()
		to := pair.ToTable.Name.String()
		if err := renameCollection(ctx, h, db.Name(), from, to); err != nil {
			return nil, mysql.NewError(mysql.ER_UNKNOWN_ERROR, "RENAME TABLE: "+err.Error())
		}
	}
	return emptyOK(), nil
}

// handleCreateDatabase handles CREATE DATABASE [IF NOT EXISTS] name.
// MongoDB creates databases lazily on first write, so we just register the
// name with the driver to make subsequent USE work.
func (h *handler) handleCreateDatabase(q string) (*mysql.Result, error) {
	parser, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		return emptyOK(), nil
	}
	parsed, err := parser.Parse(q)
	if err != nil {
		return emptyOK(), nil
	}
	cd, ok := parsed.(*sqlparser.CreateDatabase)
	if !ok {
		return emptyOK(), nil
	}
	log.Printf("CREATE DATABASE %s (lazy: created on first write)", cd.DBName.String())
	return emptyOK(), nil
}

// handleTableAdmin handles CHECK / ANALYZE / OPTIMIZE / REPAIR / CHECKSUM TABLE.
// These are no-ops for MongoDB but we return MySQL-compatible status rows.
func (h *handler) handleTableAdmin(q, upper string) (*mysql.Result, error) {
	op := "check"
	switch {
	case strings.HasPrefix(upper, "ANALYZE"):
		op = "analyze"
	case strings.HasPrefix(upper, "OPTIMIZE"):
		op = "optimize"
	case strings.HasPrefix(upper, "REPAIR"):
		op = "repair"
	case strings.HasPrefix(upper, "CHECKSUM"):
		op = "checksum"
	}
	// Strip leading verb (and possible TABLE keyword).
	rest := q
	for _, prefix := range []string{"ANALYZE TABLE", "OPTIMIZE TABLE", "REPAIR TABLE",
		"CHECKSUM TABLE", "CHECK TABLE"} {
		if strings.HasPrefix(strings.ToUpper(rest), prefix) {
			rest = rest[len(prefix):]
			break
		}
	}
	names := splitTopLevel(rest, ',')

	dbName := h.currentDB
	if dbName == "" {
		dbName = h.d.DB().Name()
	}

	if op == "checksum" {
		cols := []string{"Table", "Checksum"}
		var values [][]any
		for _, n := range names {
			values = append(values, []any{dbName + "." + cleanIdent(n), uint64(0)})
		}
		return rowsResult(cols, values), nil
	}

	cols := []string{"Table", "Op", "Msg_type", "Msg_text"}
	var values [][]any
	for _, n := range names {
		values = append(values, []any{dbName + "." + cleanIdent(n), op, "status", "OK"})
	}
	return rowsResult(cols, values), nil
}

// renameCollection performs a MongoDB rename and migrates any stored schema +
// auto-increment counter to the new table name.
func renameCollection(ctx context.Context, h *handler, dbName, from, to string) error {
	client := h.d.Client()
	if client == nil {
		return nil
	}
	admin := client.Database("admin")
	cmd := bson.D{
		{Key: "renameCollection", Value: dbName + "." + from},
		{Key: "to", Value: dbName + "." + to},
		{Key: "dropTarget", Value: false},
	}
	if err := admin.RunCommand(ctx, cmd).Err(); err != nil {
		return err
	}
	log.Printf("renamed %s.%s -> %s.%s", dbName, from, dbName, to)

	// Migrate stored schema.
	db := h.d.DB()
	if schema := h.d.Schemas.Get(ctx, db, from); schema != nil {
		schema.TableName = to
		_ = h.d.Schemas.Save(ctx, db, schema)
		h.d.Schemas.Delete(ctx, db, from)
	}
	// Migrate auto-increment counter (cannot $set on _id; copy + delete).
	autoColl := db.Collection("_auto_increment")
	var counter bson.M
	if err := autoColl.FindOne(ctx, bson.M{"_id": from}).Decode(&counter); err == nil {
		seq := counter["seq"]
		_, _ = autoColl.UpdateOne(ctx,
			bson.M{"_id": to},
			bson.M{"$set": bson.M{"seq": seq}},
			mongoUpsert(),
		)
		_, _ = autoColl.DeleteOne(ctx, bson.M{"_id": from})
	}
	return nil
}

// removeColumn returns cols with the given name removed (case-insensitive).
func removeColumn(cols []driver.ColumnDef, name string) []driver.ColumnDef {
	out := cols[:0]
	for _, c := range cols {
		if !strings.EqualFold(c.Name, name) {
			out = append(out, c)
		}
	}
	return out
}

// replaceColumn replaces the column with the given old name (or appends if absent).
func replaceColumn(cols []driver.ColumnDef, oldName string, newCol driver.ColumnDef) []driver.ColumnDef {
	for i, c := range cols {
		if strings.EqualFold(c.Name, oldName) {
			cols[i] = newCol
			return cols
		}
	}
	return append(cols, newCol)
}

// removeIndex returns indexes with the given name removed (case-insensitive).
func removeIndex(indexes []driver.IndexDef, name string) []driver.IndexDef {
	out := indexes[:0]
	for _, i := range indexes {
		if !strings.EqualFold(i.Name, name) {
			out = append(out, i)
		}
	}
	return out
}

// replaceIndex replaces the index with the same name (or appends if absent).
func replaceIndex(indexes []driver.IndexDef, idx driver.IndexDef) []driver.IndexDef {
	for i, ix := range indexes {
		if strings.EqualFold(ix.Name, idx.Name) {
			indexes[i] = idx
			return indexes
		}
	}
	return append(indexes, idx)
}

// evalDefaultLiteral returns the literal default value (non-function) for a
// column, or nil if the column has no usable default.
func evalDefaultLiteral(cd driver.ColumnDef) interface{} {
	if !cd.HasDefault {
		return nil
	}
	if cd.DefaultIsFunc {
		switch cd.DefaultValue {
		case "CURRENT_TIMESTAMP":
			return time.Now()
		default:
			return nil
		}
	}
	return cd.DefaultValue
}

// cleanIdent strips backticks, quotes and whitespace from a parsed identifier.
func cleanIdent(s string) string {
	return strings.Trim(strings.TrimSpace(s), "`\"' ")
}

// mongoUpsert returns an UpdateOptions with upsert=true.
func mongoUpsert() *mongoopt.UpdateOneOptionsBuilder {
	return mongoopt.UpdateOne().SetUpsert(true)
}
