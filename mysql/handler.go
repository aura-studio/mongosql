package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/server"

	sqldriver "github.com/aura-studio/mongosql/driver"
)

// handler implements server.Handler. One handler instance is created per
// connection; it tracks the currently selected database name (for SHOW
// DATABASES / USE) and delegates real query execution to the SQL→Mongo
// driver.
type handler struct {
	server.EmptyHandler
	d         *sqldriver.Driver
	defaultDB string
	currentDB string
}

func newHandler(d *sqldriver.Driver, defaultDB string) *handler {
	return &handler{d: d, defaultDB: defaultDB, currentDB: defaultDB}
}

// UseDB is called for COM_INIT_DB / `USE <db>`. Switch the underlying
// driver to the requested database.
func (h *handler) UseDB(dbName string) error {
	if dbName != "" {
		h.currentDB = dbName
		h.d.UseDB(dbName)
	}
	return nil
}

// HandleQuery is the main entry point for COM_QUERY.
func (h *handler) HandleQuery(query string) (*mysql.Result, error) {
	q := stripSQLComments(query)
	q = strings.TrimSpace(q)
	// Strip trailing semicolons and whitespace (clients may send multiple).
	for strings.HasSuffix(q, ";") {
		q = strings.TrimSuffix(q, ";")
		q = strings.TrimSpace(q)
	}
	if q == "" {
		return emptyOK(), nil
	}
	if r, ok, err := h.handleMeta(q); ok {
		return r, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := h.d.Exec(ctx, q)
	if err != nil {
		log.Printf("query error: %v\nsql: %s", err, q)
		return nil, mysql.NewError(mysql.ER_PARSE_ERROR, err.Error())
	}
	return resultToMySQL(res)
}

// HandleStmtPrepare advertises a prepared statement with no parameters and
// no columns. A second-pass HandleStmtExecute will run the literal query.
func (h *handler) HandleStmtPrepare(query string) (params int, columns int, ctx any, err error) {
	return 0, 0, query, nil
}

// HandleStmtExecute is COM_STMT_EXECUTE. We ignore args (no params) and run
// the original query text.
func (h *handler) HandleStmtExecute(_ any, query string, _ []any) (*mysql.Result, error) {
	return h.HandleQuery(query)
}

// HandleStmtClose is COM_STMT_CLOSE. Nothing to release.
func (h *handler) HandleStmtClose(_ any) error { return nil }

// HandleFieldList — COM_FIELD_LIST is deprecated; return empty.
func (h *handler) HandleFieldList(_ string, _ string) ([]*mysql.Field, error) {
	return nil, nil
}

// HandleOtherCommand swallows COM_PING / COM_DEBUG / etc.
func (h *handler) HandleOtherCommand(cmd byte, _ []byte) error {
	if cmd == mysql.COM_PING {
		return nil
	}
	return mysql.NewError(mysql.ER_UNKNOWN_ERROR, fmt.Sprintf("command %d not supported", cmd))
}

func emptyOK() *mysql.Result {
	return &mysql.Result{Status: mysql.SERVER_STATUS_AUTOCOMMIT}
}

// stripSQLComments removes /* ... */ and -- ... and # ... comments so that
// prefix-based dispatch in handleMeta works for queries that clients prepend
// with version markers (e.g. mysql-connector-j hint comments).
func stripSQLComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		// Block comment /* ... */
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += 2 + end + 2
			b.WriteByte(' ')
			continue
		}
		// Line comment -- ...
		if i+1 < len(s) && s[i] == '-' && s[i+1] == '-' {
			nl := strings.IndexByte(s[i:], '\n')
			if nl < 0 {
				return b.String()
			}
			i += nl
			continue
		}
		// Line comment # ...
		if s[i] == '#' {
			nl := strings.IndexByte(s[i:], '\n')
			if nl < 0 {
				return b.String()
			}
			i += nl
			continue
		}
		// String literal — copy through verbatim (avoid stripping comment-like
		// content inside strings).
		if s[i] == '\'' || s[i] == '"' || s[i] == '`' {
			quote := s[i]
			b.WriteByte(s[i])
			i++
			for i < len(s) {
				if s[i] == '\\' && i+1 < len(s) {
					b.WriteByte(s[i])
					b.WriteByte(s[i+1])
					i += 2
					continue
				}
				b.WriteByte(s[i])
				if s[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
