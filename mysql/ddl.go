package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongoopt "go.mongodb.org/mongo-driver/v2/mongo/options"
	"vitess.io/vitess/go/vt/sqlparser"

	"github.com/example/mongodb-sql-driver/driver"
)

// handleCreateTable parses CREATE TABLE with vitess to extract column
// metadata (defaults, auto_increment, ON UPDATE) and index definitions,
// then creates the MongoDB collection, stores the schema, and creates
// MongoDB indexes.
func (h *handler) handleCreateTable(q, upper string) (*mysql.Result, error) {
	ifNotExists := strings.Contains(upper, "IF NOT EXISTS")

	// Parse with vitess to get the full TableSpec.
	parser, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "CREATE TABLE: parser init: "+err.Error())
	}
	parsed, err := parser.Parse(q)
	if err != nil {
		// Fallback: extract table name the old way if vitess can't parse.
		return h.handleCreateTableFallback(q, upper, ifNotExists)
	}
	ct, ok := parsed.(*sqlparser.CreateTable)
	if !ok {
		return h.handleCreateTableFallback(q, upper, ifNotExists)
	}

	tableName := ct.Table.Name.String()
	if tableName == "" {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "CREATE TABLE: missing table name")
	}

	db := h.d.DB()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check existence.
	if !ifNotExists {
		colls, err := db.ListCollectionNames(ctx, bson.M{"name": tableName})
		if err == nil && len(colls) > 0 {
			return nil, mysql.NewError(mysql.ER_TABLE_EXISTS_ERROR,
				"Table '"+tableName+"' already exists")
		}
	}

	// Create collection.
	if err := db.CreateCollection(ctx, tableName); err != nil {
		if ifNotExists && isNamespaceExists(err) {
			return emptyOK(), nil
		}
		return nil, mysql.NewError(mysql.ER_CANT_CREATE_TABLE, "CREATE TABLE: "+err.Error())
	}
	log.Printf("created collection %s.%s", db.Name(), tableName)

	// Extract and store schema.
	schema := extractSchema(tableName, ct.TableSpec)
	if schema != nil && len(schema.Columns) > 0 {
		if err := h.d.Schemas.Save(ctx, db, schema); err != nil {
			log.Printf("warning: save schema for %s: %v", tableName, err)
		} else {
			log.Printf("saved schema for %s (%d columns, %d indexes)",
				tableName, len(schema.Columns), len(schema.Indexes))
		}
	}

	// Create MongoDB indexes.
	if schema != nil {
		createIndexes(ctx, db, tableName, schema.Indexes)
	}

	return emptyOK(), nil
}

// handleCreateTableFallback is the old string-based CREATE TABLE handler
// for when vitess can't parse the DDL.
func (h *handler) handleCreateTableFallback(q, upper string, ifNotExists bool) (*mysql.Result, error) {
	rest := q[len("CREATE TABLE"):]
	restU := upper[len("CREATE TABLE"):]
	if idx := strings.Index(restU, "IF NOT EXISTS"); idx >= 0 && strings.TrimSpace(restU[:idx]) == "" {
		rest = rest[idx+len("IF NOT EXISTS"):]
	}
	_, name := splitDBTable(firstIdent(rest))
	if name == "" {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "CREATE TABLE: missing table name")
	}
	db := h.d.DB()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !ifNotExists {
		colls, err := db.ListCollectionNames(ctx, bson.M{"name": name})
		if err == nil && len(colls) > 0 {
			return nil, mysql.NewError(mysql.ER_TABLE_EXISTS_ERROR,
				"Table '"+name+"' already exists")
		}
	}
	if err := db.CreateCollection(ctx, name); err != nil {
		if ifNotExists && isNamespaceExists(err) {
			return emptyOK(), nil
		}
		return nil, mysql.NewError(mysql.ER_CANT_CREATE_TABLE, "CREATE TABLE: "+err.Error())
	}
	log.Printf("created collection %s.%s (fallback)", db.Name(), name)
	return emptyOK(), nil
}

// extractSchema converts a vitess TableSpec into our driver.TableSchema.
func extractSchema(tableName string, spec *sqlparser.TableSpec) *driver.TableSchema {
	if spec == nil {
		return nil
	}
	schema := &driver.TableSchema{TableName: tableName}

	for _, col := range spec.Columns {
		schema.Columns = append(schema.Columns, columnDefFromVitess(col))
	}

	for _, idx := range spec.Indexes {
		schema.Indexes = append(schema.Indexes, indexDefFromVitess(idx))
	}

	return schema
}

// columnDefFromVitess converts a vitess ColumnDefinition into a driver.ColumnDef.
func columnDefFromVitess(col *sqlparser.ColumnDefinition) driver.ColumnDef {
	cd := driver.ColumnDef{
		Name: col.Name.String(),
		Type: strings.ToUpper(col.Type.Type),
	}
	if col.Type.Options != nil {
		if col.Type.Options.Null != nil && !*col.Type.Options.Null {
			cd.NotNull = true
		}
		cd.AutoIncrement = col.Type.Options.Autoincrement
		if col.Type.Options.Default != nil {
			cd.HasDefault = true
			defStr := sqlparser.String(col.Type.Options.Default)
			defStr = strings.TrimSpace(defStr)
			upper := strings.ToUpper(defStr)
			if strings.Contains(upper, "CURRENT_TIMESTAMP") ||
				strings.Contains(upper, "NOW()") ||
				strings.Contains(upper, "CURDATE()") ||
				strings.Contains(upper, "CURTIME()") ||
				strings.Contains(upper, "UUID()") {
				cd.DefaultIsFunc = true
				if strings.Contains(upper, "CURRENT_TIMESTAMP") || strings.Contains(upper, "NOW") {
					cd.DefaultValue = "CURRENT_TIMESTAMP"
				} else if strings.Contains(upper, "CURDATE") {
					cd.DefaultValue = "CURDATE"
				} else if strings.Contains(upper, "CURTIME") {
					cd.DefaultValue = "CURTIME"
				} else if strings.Contains(upper, "UUID") {
					cd.DefaultValue = "UUID"
				}
			} else {
				cd.DefaultValue = strings.Trim(defStr, "'\"")
			}
		}
		if col.Type.Options.OnUpdate != nil {
			onUpStr := strings.ToUpper(sqlparser.String(col.Type.Options.OnUpdate))
			if strings.Contains(onUpStr, "CURRENT_TIMESTAMP") || strings.Contains(onUpStr, "NOW") {
				cd.OnUpdate = "CURRENT_TIMESTAMP"
			}
		}
	}
	return cd
}

// indexDefFromVitess converts a vitess IndexDefinition into a driver.IndexDef.
func indexDefFromVitess(idx *sqlparser.IndexDefinition) driver.IndexDef {
	id := driver.IndexDef{
		Name:    idx.Info.Name.String(),
		Primary: idx.Info.Type == sqlparser.IndexTypePrimary,
		Unique:  idx.Info.Type == sqlparser.IndexTypeUnique,
	}
	for _, col := range idx.Columns {
		id.Columns = append(id.Columns, col.Column.String())
	}
	return id
}

// createIndexes creates MongoDB indexes based on the parsed index definitions.
func createIndexes(ctx context.Context, db *mongo.Database, tableName string, indexes []driver.IndexDef) {
	coll := db.Collection(tableName)
	for _, idx := range indexes {
		if idx.Primary {
			// PRIMARY KEY on _id is automatic in MongoDB; for user-defined
			// primary keys we create a unique index.
			if len(idx.Columns) == 1 && strings.ToLower(idx.Columns[0]) == "_id" {
				continue
			}
		}
		if len(idx.Columns) == 0 {
			continue
		}

		keys := bson.D{}
		for _, c := range idx.Columns {
			keys = append(keys, bson.E{Key: c, Value: 1})
		}

		idxName := idx.Name
		if idxName == "" || strings.ToUpper(idxName) == "PRIMARY" {
			idxName = "idx_" + strings.Join(idx.Columns, "_")
		}

		opts := mongoopt.Index().SetName(idxName)
		if idx.Unique || idx.Primary {
			opts.SetUnique(true)
		}

		model := mongo.IndexModel{Keys: keys, Options: opts}
		name, err := coll.Indexes().CreateOne(ctx, model)
		if err != nil {
			log.Printf("warning: create index %s on %s: %v", idxName, tableName, err)
		} else {
			log.Printf("created index %s on %s.%s", name, db.Name(), tableName)
		}
	}
}

// handleCreateIndex handles CREATE [UNIQUE] INDEX idx ON table (cols...).
// Vitess parses this as *sqlparser.AlterTable with AddIndexDefinition.
func (h *handler) handleCreateIndex(q string) (*mysql.Result, error) {
	parser, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "CREATE INDEX: parser init: "+err.Error())
	}
	parsed, err := parser.Parse(q)
	if err != nil {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "CREATE INDEX: "+err.Error())
	}
	alter, ok := parsed.(*sqlparser.AlterTable)
	if !ok || len(alter.AlterOptions) == 0 {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "CREATE INDEX: unexpected AST")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := h.d.DB()
	tableName := alter.Table.Name.String()

	for _, opt := range alter.AlterOptions {
		addIdx, ok := opt.(*sqlparser.AddIndexDefinition)
		if !ok {
			continue
		}
		info := addIdx.IndexDefinition.Info
		var cols []string
		for _, col := range addIdx.IndexDefinition.Columns {
			cols = append(cols, col.Column.String())
		}
		if len(cols) == 0 {
			continue
		}

		idx := driver.IndexDef{
			Name:    info.Name.String(),
			Columns: cols,
			Primary: info.Type == sqlparser.IndexTypePrimary,
			Unique:  info.Type == sqlparser.IndexTypeUnique,
		}
		createIndexes(ctx, db, tableName, []driver.IndexDef{idx})

		// Also update stored schema if it exists.
		if schema := h.d.Schemas.Get(ctx, db, tableName); schema != nil {
			schema.Indexes = append(schema.Indexes, idx)
			_ = h.d.Schemas.Save(ctx, db, schema)
		}
	}

	return emptyOK(), nil
}

// handleDropIndex handles DROP INDEX idx ON table.
// Vitess parses this as *sqlparser.AlterTable with DropKey.
func (h *handler) handleDropIndex(q string) (*mysql.Result, error) {
	parser, err := sqlparser.New(sqlparser.Options{})
	if err != nil {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "DROP INDEX: parser init: "+err.Error())
	}
	parsed, err := parser.Parse(q)
	if err != nil {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "DROP INDEX: "+err.Error())
	}
	alter, ok := parsed.(*sqlparser.AlterTable)
	if !ok || len(alter.AlterOptions) == 0 {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "DROP INDEX: unexpected AST")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db := h.d.DB()
	tableName := alter.Table.Name.String()
	coll := db.Collection(tableName)

	for _, opt := range alter.AlterOptions {
		dk, ok := opt.(*sqlparser.DropKey)
		if !ok {
			continue
		}
		idxName := dk.Name.String()
		if err := coll.Indexes().DropOne(ctx, idxName); err != nil {
			log.Printf("warning: drop index %s on %s: %v", idxName, tableName, err)
		} else {
			log.Printf("dropped index %s on %s.%s", idxName, db.Name(), tableName)
		}

		// Also update stored schema if it exists.
		if schema := h.d.Schemas.Get(ctx, db, tableName); schema != nil {
			for i, si := range schema.Indexes {
				if si.Name == idxName {
					schema.Indexes = append(schema.Indexes[:i], schema.Indexes[i+1:]...)
					break
				}
			}
			_ = h.d.Schemas.Save(ctx, db, schema)
		}
	}

	return emptyOK(), nil
}

// handleDropTable maps `DROP TABLE [IF EXISTS] name [, name ...]` to
// `db.collection.drop()` for each name.
func (h *handler) handleDropTable(q, upper string) (*mysql.Result, error) {
	rest := q[len("DROP TABLE"):]
	restU := upper[len("DROP TABLE"):]
	ifExists := false
	if idx := strings.Index(restU, "IF EXISTS"); idx >= 0 && strings.TrimSpace(restU[:idx]) == "" {
		rest = rest[idx+len("IF EXISTS"):]
		ifExists = true
	}
	names := splitTopLevel(rest, ',')
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, n := range names {
		dbName, name := splitDBTable(firstIdent(n))
		if name == "" {
			continue
		}
		db := h.d.DB()
		if dbName != "" {
			db = h.d.Client().Database(dbName)
		}
		if err := db.Collection(name).Drop(ctx); err != nil {
			if ifExists {
				continue
			}
			return nil, mysql.NewError(mysql.ER_UNKNOWN_ERROR, "DROP TABLE: "+err.Error())
		}
		// Clean up schema and auto_increment counter.
		h.d.Schemas.Delete(ctx, db, name)
		_, _ = db.Collection("_auto_increment").DeleteOne(ctx, bson.M{"_id": name})
		log.Printf("dropped collection %s.%s", db.Name(), name)
	}
	return emptyOK(), nil
}

// handleTruncate maps `TRUNCATE [TABLE] name` to dropping all documents.
func (h *handler) handleTruncate(q, upper string) (*mysql.Result, error) {
	rest := q[len("TRUNCATE"):]
	restU := upper[len("TRUNCATE"):]
	if strings.HasPrefix(strings.TrimSpace(restU), "TABLE") {
		idx := strings.Index(restU, "TABLE")
		rest = rest[idx+len("TABLE"):]
	}
	dbName, name := splitDBTable(firstIdent(rest))
	if name == "" {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "TRUNCATE: missing table name")
	}
	db := h.d.DB()
	if dbName != "" {
		db = h.d.Client().Database(dbName)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Collection(name).Drop(ctx); err != nil {
		return nil, mysql.NewError(mysql.ER_UNKNOWN_ERROR, "TRUNCATE: "+err.Error())
	}
	if err := db.CreateCollection(ctx, name); err != nil && !isNamespaceExists(err) {
		return nil, mysql.NewError(mysql.ER_UNKNOWN_ERROR, "TRUNCATE: "+err.Error())
	}
	return emptyOK(), nil
}

// handleDropDatabase maps `DROP DATABASE [IF EXISTS] name` to
// `db.dropDatabase()`.
func (h *handler) handleDropDatabase(q, upper string) (*mysql.Result, error) {
	prefix := "DROP DATABASE"
	if strings.HasPrefix(upper, "DROP SCHEMA") {
		prefix = "DROP SCHEMA"
	}
	rest := q[len(prefix):]
	restU := upper[len(prefix):]
	ifExists := false
	if idx := strings.Index(restU, "IF EXISTS"); idx >= 0 && strings.TrimSpace(restU[:idx]) == "" {
		rest = rest[idx+len("IF EXISTS"):]
		ifExists = true
	}
	name := strings.Trim(strings.TrimSpace(firstIdent(rest)), "`\"' ;")
	if name == "" {
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, "DROP DATABASE: missing name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.d.Client().Database(name).Drop(ctx); err != nil {
		if ifExists {
			return emptyOK(), nil
		}
		return nil, mysql.NewError(mysql.ER_UNKNOWN_ERROR, "DROP DATABASE: "+err.Error())
	}
	log.Printf("dropped database %s", name)
	return emptyOK(), nil
}

// firstIdent returns the first whitespace/'('-delimited token from s,
// stripped of surrounding quoting.
func firstIdent(s string) string {
	s = strings.TrimSpace(s)
	end := len(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '(' || c == ';' || c == ',' {
			end = i
			break
		}
	}
	return strings.Trim(s[:end], "`\"' ;")
}

// splitDBTable splits an identifier like `db.table` into ("db","table").
func splitDBTable(ident string) (string, string) {
	ident = strings.Trim(ident, "`\"' ")
	if dot := strings.LastIndex(ident, "."); dot >= 0 {
		return strings.Trim(ident[:dot], "`\"' "), strings.Trim(ident[dot+1:], "`\"' ")
	}
	return "", ident
}

// isNamespaceExists detects MongoDB's "NamespaceExists" (code 48) error.
func isNamespaceExists(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "NamespaceExists") || strings.Contains(msg, "(NamespaceExists)") || strings.Contains(msg, "code: 48")
}
