package main

import (
	"context"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// handleMeta intercepts MySQL administrative / introspection queries that UI
// clients send during connection setup or when refreshing schema browsers.
// These queries cannot be translated to MongoDB; instead we return canned
// (or computed) responses so clients can complete their handshake and
// discover collections.
//
// Returns (result, true, nil) if the query was handled here, or (nil, false, nil)
// to let the caller forward it to the SQL translator. On error returns (nil, true, err).
func (h *handler) handleMeta(q string) (*mysql.Result, bool, error) {
	upper := strings.ToUpper(strings.TrimSpace(q))

	// ───── empty / whitespace ─────
	if upper == "" {
		return emptyOK(), true, nil
	}

	// ───── connection setup / no-ops ─────
	switch {
	case strings.HasPrefix(upper, "SET "),
		upper == "SET",
		strings.HasPrefix(upper, "BEGIN"),
		strings.HasPrefix(upper, "START TRANSACTION"),
		strings.HasPrefix(upper, "COMMIT"),
		strings.HasPrefix(upper, "ROLLBACK"),
		strings.HasPrefix(upper, "SAVEPOINT"),
		strings.HasPrefix(upper, "RELEASE SAVEPOINT"),
		strings.HasPrefix(upper, "FLUSH"),
		strings.HasPrefix(upper, "RESET"),
		strings.HasPrefix(upper, "LOCK TABLES"),
		strings.HasPrefix(upper, "UNLOCK TABLES"),
		strings.HasPrefix(upper, "KILL "),
		strings.HasPrefix(upper, "GRANT"),
		strings.HasPrefix(upper, "REVOKE"),
		strings.HasPrefix(upper, "CREATE USER"),
		strings.HasPrefix(upper, "DROP USER"),
		strings.HasPrefix(upper, "ALTER USER"),
		strings.HasPrefix(upper, "HANDLER"),
		strings.HasPrefix(upper, "PREPARE"),
		strings.HasPrefix(upper, "EXECUTE"),
		strings.HasPrefix(upper, "DEALLOCATE"),
		strings.HasPrefix(upper, "XA "):
		return emptyOK(), true, nil
	}

	// ───── DDL routed to real MongoDB operations ─────
	switch {
	case strings.HasPrefix(upper, "CREATE TABLE"):
		r, err := h.handleCreateTable(q, upper)
		return r, true, err
	case strings.HasPrefix(upper, "DROP TABLE"):
		r, err := h.handleDropTable(q, upper)
		return r, true, err
	case strings.HasPrefix(upper, "TRUNCATE"):
		r, err := h.handleTruncate(q, upper)
		return r, true, err
	case strings.HasPrefix(upper, "CREATE INDEX"),
		strings.HasPrefix(upper, "CREATE UNIQUE INDEX"):
		r, err := h.handleCreateIndex(q)
		return r, true, err
	case strings.HasPrefix(upper, "DROP INDEX"):
		r, err := h.handleDropIndex(q)
		return r, true, err
	case strings.HasPrefix(upper, "ALTER TABLE"):
		r, err := h.handleAlterTable(q)
		return r, true, err
	case strings.HasPrefix(upper, "RENAME TABLE"):
		r, err := h.handleRenameTable(q)
		return r, true, err
	case strings.HasPrefix(upper, "CREATE DATABASE"),
		strings.HasPrefix(upper, "CREATE SCHEMA"):
		r, err := h.handleCreateDatabase(q)
		return r, true, err
	case strings.HasPrefix(upper, "CHECK TABLE"),
		strings.HasPrefix(upper, "ANALYZE TABLE"),
		strings.HasPrefix(upper, "OPTIMIZE TABLE"),
		strings.HasPrefix(upper, "REPAIR TABLE"),
		strings.HasPrefix(upper, "CHECKSUM TABLE"):
		r, err := h.handleTableAdmin(q, upper)
		return r, true, err
	case strings.HasPrefix(upper, "DROP DATABASE"),
		strings.HasPrefix(upper, "DROP SCHEMA"):
		r, err := h.handleDropDatabase(q, upper)
		return r, true, err
	}

	// ───── USE <db> ─────
	if strings.HasPrefix(upper, "USE ") {
		dbName := strings.TrimSpace(q[4:])
		dbName = strings.Trim(dbName, "`\"' ")
		h.currentDB = dbName
		h.d.UseDB(dbName)
		return emptyOK(), true, nil
	}

	// ───── SELECT queries: handle system / FROM-less / meta-schema ─────
	if strings.HasPrefix(upper, "SELECT") {
		selectBody := upper[6:]
		// Queries against information_schema, mysql, performance_schema
		if containsMetaSchema(upper) {
			return emptyResult([]string{"(empty)"}), true, nil
		}
		if containsSystemExpr(selectBody) {
			return systemSelect(q, h.currentDB), true, nil
		}
		// SELECT without a FROM clause: literal-only / expression queries
		// like `SELECT 'keep alive'`, `SELECT 1+1`, etc. Route to the
		// expression-row builder rather than to the SQL→Mongo translator.
		if !hasTopLevelFrom(selectBody) {
			return systemSelect(q, h.currentDB), true, nil
		}
	}

	// ───── SHOW commands ─────
	if strings.HasPrefix(upper, "SHOW") {
		return h.handleShow(q, upper), true, nil
	}

	// ───── DESC / DESCRIBE / EXPLAIN ─────
	if strings.HasPrefix(upper, "DESC ") ||
		strings.HasPrefix(upper, "DESCRIBE ") ||
		strings.HasPrefix(upper, "EXPLAIN ") {
		return emptyResult([]string{"Field", "Type", "Null", "Key", "Default", "Extra"}), true, nil
	}

	return nil, false, nil
}

// containsSystemExpr returns true if the SELECT body references system
// variables (@@), functions like VERSION(), DATABASE() etc.
func containsSystemExpr(body string) bool {
	if strings.Contains(body, "@@") {
		return true
	}
	for _, fn := range []string{
		"VERSION()", "DATABASE()", "SCHEMA()", "USER()", "CURRENT_USER",
		"CONNECTION_ID()", "NOW()", "CURRENT_TIMESTAMP", "LAST_INSERT_ID()",
		"ROW_COUNT()", "FOUND_ROWS()", "CHARSET(", "COLLATION(",
	} {
		if strings.Contains(body, fn) {
			return true
		}
	}
	// "SELECT 1" / "SELECT 1 AS ..." pattern
	trimmed := strings.TrimSpace(body)
	if len(trimmed) > 0 && (trimmed[0] >= '0' && trimmed[0] <= '9') {
		return true
	}
	return false
}

// containsMetaSchema checks if query touches information_schema / mysql / performance_schema.
func containsMetaSchema(upper string) bool {
	return strings.Contains(upper, "INFORMATION_SCHEMA") ||
		strings.Contains(upper, "PERFORMANCE_SCHEMA") ||
		strings.Contains(upper, "MYSQL.") ||
		strings.Contains(upper, "`MYSQL`.") ||
		strings.Contains(upper, "SYS.")
}

// hasTopLevelFrom returns true if `body` (the part after SELECT) contains a
// top-level FROM keyword (i.e. one not inside a string, parens, or
// identifier). Body is expected to already be upper-cased.
func hasTopLevelFrom(body string) bool {
	depth := 0
	i := 0
	for i < len(body) {
		c := body[i]
		switch c {
		case '\'', '"', '`':
			// skip quoted segment
			quote := c
			i++
			for i < len(body) {
				if body[i] == '\\' && i+1 < len(body) {
					i += 2
					continue
				}
				if body[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		if depth == 0 && c == 'F' && i+4 <= len(body) {
			// Check for whitespace-bounded "FROM"
			if body[i:i+4] == "FROM" {
				prevOK := i == 0 || isSQLBoundary(body[i-1])
				nextOK := i+4 == len(body) || isSQLBoundary(body[i+4])
				if prevOK && nextOK {
					return true
				}
			}
		}
		i++
	}
	return false
}

func isSQLBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '(', ')', ',', ';':
		return true
	}
	return false
}

// handleShow dispatches all SHOW commands.
func (h *handler) handleShow(q, upper string) *mysql.Result {
	// Strip "SHOW " prefix for matching.
	rest := strings.TrimSpace(upper[4:])

	switch {
	case strings.HasPrefix(rest, "DATABASES"),
		strings.HasPrefix(rest, "SCHEMAS"):
		return h.showDatabasesReal()

	case strings.HasPrefix(rest, "FULL TABLES"),
		strings.HasPrefix(rest, "TABLES"):
		r, _ := h.showTables(strings.HasPrefix(rest, "FULL"))
		return r

	case strings.HasPrefix(rest, "FULL COLUMNS"),
		strings.HasPrefix(rest, "COLUMNS"),
		strings.HasPrefix(rest, "FIELDS"),
		strings.HasPrefix(rest, "FULL FIELDS"):
		return emptyResult([]string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"})

	case strings.HasPrefix(rest, "CREATE DATABASE"),
		strings.HasPrefix(rest, "CREATE SCHEMA"):
		return rowsResult(
			[]string{"Database", "Create Database"},
			[][]any{{h.currentDB, "CREATE DATABASE `" + h.currentDB + "`"}},
		)

	case strings.HasPrefix(rest, "CREATE TABLE"):
		name := extractTableName(q, "SHOW CREATE TABLE")
		return rowsResult(
			[]string{"Table", "Create Table"},
			[][]any{{name, "CREATE TABLE `" + name + "` (\n  `_id` varchar(255) NOT NULL,\n  PRIMARY KEY (`_id`)\n) ENGINE=MongoDB"}},
		)

	case strings.HasPrefix(rest, "INDEX"),
		strings.HasPrefix(rest, "INDEXES"),
		strings.HasPrefix(rest, "KEYS"):
		return emptyResult([]string{
			"Table", "Non_unique", "Key_name", "Seq_in_index", "Column_name",
			"Collation", "Cardinality", "Sub_part", "Packed", "Null", "Index_type",
			"Comment", "Index_comment",
		})

	case strings.HasPrefix(rest, "TABLE STATUS"):
		return emptyResult([]string{
			"Name", "Engine", "Version", "Row_format", "Rows", "Avg_row_length",
			"Data_length", "Max_data_length", "Index_length", "Data_free",
			"Auto_increment", "Create_time", "Update_time", "Check_time",
			"Collation", "Checksum", "Create_options", "Comment",
		})

	case strings.HasPrefix(rest, "VARIABLES"),
		strings.HasPrefix(rest, "SESSION VARIABLES"),
		strings.HasPrefix(rest, "GLOBAL VARIABLES"):
		return showVariables()

	case strings.HasPrefix(rest, "STATUS"),
		strings.HasPrefix(rest, "SESSION STATUS"),
		strings.HasPrefix(rest, "GLOBAL STATUS"):
		return emptyResult([]string{"Variable_name", "Value"})

	case strings.HasPrefix(rest, "WARNINGS"):
		return emptyResult([]string{"Level", "Code", "Message"})

	case strings.HasPrefix(rest, "ERRORS"):
		return emptyResult([]string{"Level", "Code", "Message"})

	case strings.HasPrefix(rest, "ENGINES"):
		return rowsResult(
			[]string{"Engine", "Support", "Comment", "Transactions", "XA", "Savepoints"},
			[][]any{{"MongoDB", "DEFAULT", "MongoDB backed storage", "NO", "NO", "NO"}},
		)

	case strings.HasPrefix(rest, "CHARACTER SET"),
		strings.HasPrefix(rest, "CHARSET"):
		return rowsResult(
			[]string{"Charset", "Description", "Default collation", "Maxlen"},
			[][]any{{"utf8mb4", "UTF-8 Unicode", "utf8mb4_general_ci", int64(4)}},
		)

	case strings.HasPrefix(rest, "COLLATION"):
		return rowsResult(
			[]string{"Collation", "Charset", "Id", "Default", "Compiled", "Sortlen"},
			[][]any{{"utf8mb4_general_ci", "utf8mb4", int64(45), "Yes", "Yes", int64(1)}},
		)

	case strings.HasPrefix(rest, "GRANTS"):
		return rowsResult(
			[]string{"Grants for root@localhost"},
			[][]any{{"GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost'"}},
		)

	case strings.HasPrefix(rest, "PROCESSLIST"),
		strings.HasPrefix(rest, "FULL PROCESSLIST"):
		return emptyResult([]string{"Id", "User", "Host", "db", "Command", "Time", "State", "Info"})

	case strings.HasPrefix(rest, "PLUGINS"):
		return emptyResult([]string{"Name", "Status", "Type", "Library", "License"})

	case strings.HasPrefix(rest, "PRIVILEGES"):
		return emptyResult([]string{"Privilege", "Context", "Comment"})

	case strings.HasPrefix(rest, "TRIGGERS"):
		return emptyResult([]string{"Trigger", "Event", "Table", "Statement", "Timing", "Created", "sql_mode", "Definer", "character_set_client", "collation_connection", "Database Collation"})

	case strings.HasPrefix(rest, "EVENTS"):
		return emptyResult([]string{"Db", "Name", "Definer", "Time zone", "Type", "Execute at", "Interval value", "Interval field", "Starts", "Ends", "Status", "Originator", "character_set_client", "collation_connection", "Database Collation"})

	case strings.HasPrefix(rest, "PROCEDURE STATUS"),
		strings.HasPrefix(rest, "FUNCTION STATUS"):
		return emptyResult([]string{"Db", "Name", "Type", "Definer", "Modified", "Created", "Security_type", "Comment", "character_set_client", "collation_connection", "Database Collation"})

	case strings.HasPrefix(rest, "CREATE PROCEDURE"),
		strings.HasPrefix(rest, "CREATE FUNCTION"),
		strings.HasPrefix(rest, "CREATE TRIGGER"),
		strings.HasPrefix(rest, "CREATE EVENT"),
		strings.HasPrefix(rest, "CREATE VIEW"):
		return emptyOK()

	case strings.HasPrefix(rest, "OPEN TABLES"):
		return emptyResult([]string{"Database", "Table", "In_use", "Name_locked"})

	case strings.HasPrefix(rest, "MASTER STATUS"),
		strings.HasPrefix(rest, "SLAVE STATUS"),
		strings.HasPrefix(rest, "REPLICA STATUS"),
		strings.HasPrefix(rest, "BINARY LOGS"),
		strings.HasPrefix(rest, "BINLOG EVENTS"):
		return emptyResult([]string{"(empty)"})

	default:
		// Unknown SHOW — return empty result rather than an error.
		return emptyResult([]string{"(empty)"})
	}
}

func extractTableName(q, prefix string) string {
	upper := strings.ToUpper(q)
	idx := strings.Index(upper, strings.ToUpper(prefix))
	if idx < 0 {
		return "unknown"
	}
	name := strings.TrimSpace(q[idx+len(prefix):])
	name = strings.Trim(name, "`\"' ;")
	// Remove db prefix like `db`.`table`
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
		name = strings.Trim(name, "`\"' ")
	}
	if name == "" {
		return "unknown"
	}
	return name
}

// systemSelect handles `SELECT @@version`, `SELECT VERSION()`, etc. We don't
// parse the column list — we just return one row built from the literal
// expressions, naming each column after the original expression text.
func systemSelect(q, db string) *mysql.Result {
	// Strip `SELECT ` prefix and split on commas (very rough, sufficient for
	// the synthetic queries that GUIs emit).
	rest := q
	if i := strings.Index(strings.ToUpper(rest), "SELECT "); i >= 0 {
		rest = rest[i+len("SELECT "):]
	}
	exprs := splitTopLevel(rest, ',')
	names := make([]string, 0, len(exprs))
	values := make([]any, 0, len(exprs))
	for _, e := range exprs {
		e = strings.TrimSpace(e)
		// Honour `expr AS alias`.
		alias := e
		lower := strings.ToLower(e)
		if idx := strings.Index(lower, " as "); idx >= 0 {
			alias = strings.TrimSpace(e[idx+4:])
			e = strings.TrimSpace(e[:idx])
			alias = strings.Trim(alias, "`\"'")
		}
		names = append(names, alias)
		values = append(values, systemValueFor(e, db))
	}
	return rowsResult(names, [][]any{values})
}

func systemValueFor(expr, db string) any {
	upper := strings.ToUpper(strings.TrimSpace(expr))
	// Strip leading @@ and optional SESSION./GLOBAL. prefix
	key := upper
	if strings.HasPrefix(key, "@@") {
		key = key[2:]
	}
	if strings.HasPrefix(key, "SESSION.") {
		key = key[8:]
	}
	if strings.HasPrefix(key, "GLOBAL.") {
		key = key[7:]
	}

	switch key {
	case "VERSION", "VERSION_COMMENT":
		if key == "VERSION" && !strings.HasPrefix(upper, "@@") && upper == "VERSION()" {
			return "8.0.0-mongodb-sql-driver"
		}
		if key == "VERSION" {
			return "8.0.0-mongodb-sql-driver"
		}
		return "mongodb-sql-driver"
	case "HOSTNAME", "SERVERNAME":
		return "localhost"
	case "PORT":
		return int64(3307)
	case "CHARACTER_SET_CLIENT", "CHARACTER_SET_RESULTS",
		"CHARACTER_SET_CONNECTION", "CHARACTER_SET_SERVER",
		"CHARACTER_SET_DATABASE", "CHARACTER_SET_FILESYSTEM",
		"CHARACTER_SET_SYSTEM", "COLLATION_DATABASE":
		if strings.Contains(key, "COLLATION") {
			return "utf8mb4_general_ci"
		}
		return "utf8mb4"
	case "COLLATION_SERVER", "COLLATION_CONNECTION":
		return "utf8mb4_general_ci"
	case "SQL_MODE":
		return ""
	case "LICENSE":
		return "Apache-2.0"
	case "AUTOCOMMIT", "AUTO_INCREMENT_INCREMENT", "AUTO_INCREMENT_OFFSET":
		return int64(1)
	case "TX_ISOLATION", "TRANSACTION_ISOLATION":
		return "READ-COMMITTED"
	case "TX_READ_ONLY", "TRANSACTION_READ_ONLY":
		return int64(0)
	case "LOWER_CASE_TABLE_NAMES":
		return int64(0)
	case "MAX_ALLOWED_PACKET":
		return int64(67108864)
	case "NET_WRITE_TIMEOUT", "NET_READ_TIMEOUT":
		return int64(30)
	case "WAIT_TIMEOUT", "INTERACTIVE_TIMEOUT":
		return int64(28800)
	case "TIME_ZONE", "SYSTEM_TIME_ZONE":
		return "UTC"
	case "HAVE_SSL", "HAVE_OPENSSL":
		return "DISABLED"
	case "SSL_CA", "SSL_CERT", "SSL_KEY":
		return ""
	case "INIT_CONNECT":
		return ""
	case "SQL_SAFE_UPDATES":
		return int64(0)
	case "SQL_SELECT_LIMIT":
		return "18446744073709551615"
	case "FOREIGN_KEY_CHECKS":
		return int64(1)
	case "UNIQUE_CHECKS":
		return int64(1)
	case "PROFILING":
		return int64(0)
	case "HAVE_PROFILING":
		return "YES"
	case "LOG_BIN":
		return int64(0)
	case "INNODB_VERSION":
		return "8.0.0"
	case "VERSION_COMPILE_OS":
		return "linux"
	case "VERSION_COMPILE_MACHINE":
		return "x86_64"
	case "PERFORMANCE_SCHEMA":
		return int64(0)
	case "EVENT_SCHEDULER":
		return "OFF"
	case "GROUP_CONCAT_MAX_LEN":
		return int64(1024)
	case "MAX_CONNECTIONS":
		return int64(151)
	case "THREAD_STACK":
		return int64(262144)
	}

	// Handle function calls
	switch upper {
	case "VERSION()":
		return "8.0.0-mongodb-sql-driver"
	case "DATABASE()", "SCHEMA()":
		return db
	case "CURRENT_USER", "CURRENT_USER()", "USER()", "SESSION_USER()":
		return "root@localhost"
	case "CONNECTION_ID()":
		return int64(1)
	case "NOW()", "CURRENT_TIMESTAMP", "CURRENT_TIMESTAMP()",
		"SYSDATE()", "LOCALTIME", "LOCALTIME()", "LOCALTIMESTAMP", "LOCALTIMESTAMP()":
		return time.Now().UTC().Format("2006-01-02 15:04:05")
	case "CURDATE()", "CURRENT_DATE", "CURRENT_DATE()":
		return time.Now().UTC().Format("2006-01-02")
	case "CURTIME()", "CURRENT_TIME", "CURRENT_TIME()":
		return time.Now().UTC().Format("15:04:05")
	case "LAST_INSERT_ID()":
		return int64(0)
	case "ROW_COUNT()":
		return int64(0)
	case "FOUND_ROWS()":
		return int64(0)
	case "1":
		return int64(1)
	case "TRUE":
		return int64(1)
	case "FALSE":
		return int64(0)
	case "NULL":
		return nil
	}

	// Numeric literal
	if len(upper) > 0 && upper[0] >= '0' && upper[0] <= '9' {
		return expr
	}

	// Quoted string literal
	if (strings.HasPrefix(expr, "'") && strings.HasSuffix(expr, "'")) ||
		(strings.HasPrefix(expr, "\"") && strings.HasSuffix(expr, "\"")) {
		return expr[1 : len(expr)-1]
	}

	// Unknown — return empty string rather than error.
	return ""
}

func (h *handler) showDatabasesReal() *mysql.Result {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dbs, err := h.d.Client().ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return rowsResult(
			[]string{"Database"},
			[][]any{{"information_schema"}, {"mysql"}, {h.currentDB}},
		)
	}
	rows := [][]any{{"information_schema"}, {"mysql"}}
	for _, db := range dbs {
		rows = append(rows, []any{db})
	}
	return rowsResult([]string{"Database"}, rows)
}

func (h *handler) showTables(full bool) (*mysql.Result, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	colls, err := h.d.DB().ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return rowsResult([]string{"Tables_in_" + h.currentDB}, nil), true
	}
	col := "Tables_in_" + h.currentDB
	if full {
		rows := make([][]any, 0, len(colls))
		for _, c := range colls {
			rows = append(rows, []any{c, "BASE TABLE"})
		}
		return rowsResult([]string{col, "Table_type"}, rows), true
	}
	rows := make([][]any, 0, len(colls))
	for _, c := range colls {
		rows = append(rows, []any{c})
	}
	return rowsResult([]string{col}, rows), true
}

func showVariables() *mysql.Result {
	return rowsResult(
		[]string{"Variable_name", "Value"},
		[][]any{
			{"version", "8.0.0-mongodb-sql-driver"},
			{"version_comment", "mongodb-sql-driver"},
			{"character_set_server", "utf8mb4"},
			{"collation_server", "utf8mb4_general_ci"},
			{"max_allowed_packet", "67108864"},
			{"sql_mode", ""},
			{"autocommit", "ON"},
			{"time_zone", "UTC"},
			{"transaction_isolation", "READ-COMMITTED"},
			{"lower_case_table_names", "0"},
		},
	)
}

// splitTopLevel splits s by sep, ignoring sep characters that appear inside
// matching parentheses or quotes. Good enough for synthetic GUI queries.
func splitTopLevel(s string, sep byte) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote && (i == 0 || s[i-1] != '\\') {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
