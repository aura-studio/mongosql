// Unit tests for the "medium difficulty" features:
//
//	#3  SUM(price*qty) — aggregate functions with expression arguments
//	#6  SELECT a+b, UPPER(name) — expression projection
//	#7  CASE WHEN
//	#8  WHERE UPPER(name)='X' — expression on left side of comparison
//	#9  Non-equi JOIN (ON a.x > b.y)
//	#11 INSERT INTO ... SELECT ...
package tests

import (
	"testing"

	"github.com/aura-studio/mongosql/translator/stmt"
)

// ─── #6  Expression projection ───────────────────────────────────────────────

func TestExprProjection_Arithmetic(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT price + qty AS total FROM products")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	agg, ok := st.(*stmt.AggregateStmt)
	if !ok {
		t.Fatalf("expected AggregateStmt for expression projection, got %T", st)
	}
	if agg.Collection != "products" {
		t.Fatalf("collection: got %q", agg.Collection)
	}
	// Should have a $project stage with total expression.
	if len(agg.Pipeline) == 0 {
		t.Fatal("empty pipeline")
	}
}

func TestExprProjection_Function(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT UPPER(name) AS upper_name FROM users")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	_, ok := st.(*stmt.AggregateStmt)
	if !ok {
		t.Fatalf("expected AggregateStmt, got %T", st)
	}
}

func TestExprProjection_CaseWhen(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT CASE WHEN price > 10 THEN 'expensive' ELSE 'cheap' END AS tier FROM products")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	_, ok := st.(*stmt.AggregateStmt)
	if !ok {
		t.Fatalf("expected AggregateStmt, got %T", st)
	}
}

func TestExprProjection_MixedWithField(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT name, price * qty AS total FROM products")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	_, ok := st.(*stmt.AggregateStmt)
	if !ok {
		t.Fatalf("expected AggregateStmt, got %T", st)
	}
}

// ─── #7  CASE WHEN ──────────────────────────────────────────────────────────

func TestCaseWhen_Simple(t *testing.T) {
	tr := newTranslator(t)
	_, err := tr.Translate("SELECT CASE status WHEN 1 THEN 'active' WHEN 2 THEN 'inactive' ELSE 'unknown' END AS label FROM users")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
}

func TestCaseWhen_Searched(t *testing.T) {
	tr := newTranslator(t)
	_, err := tr.Translate("SELECT CASE WHEN age < 18 THEN 'minor' WHEN age < 65 THEN 'adult' ELSE 'senior' END AS bracket FROM users")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
}

// ─── #8  WHERE left-side expression ─────────────────────────────────────────

func TestWhereExprLeftSide(t *testing.T) {
	tr := newTranslator(t)
	// WHERE with expression on left side should use $expr.
	st, err := tr.Translate("SELECT * FROM users WHERE price + qty > 100")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	// Because of $expr, this should still result in a FindStmt (the WHERE
	// triggers $expr but doesn't force aggregation).
	find, ok := st.(*stmt.FindStmt)
	if !ok {
		t.Fatalf("expected FindStmt, got %T", st)
	}
	if find.Filter == nil {
		t.Fatal("expected non-nil filter")
	}
	// Check $expr is in the filter.
	if _, hasExpr := find.Filter["$expr"]; !hasExpr {
		t.Fatalf("expected $expr in filter, got %v", find.Filter)
	}
}

// ─── #3  Aggregate functions with expression arguments ──────────────────────

func TestAggExprArg_SumMultiply(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT SUM(price * qty) AS revenue FROM orders")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	_, ok := st.(*stmt.AggregateStmt)
	if !ok {
		t.Fatalf("expected AggregateStmt, got %T", st)
	}
}

func TestAggExprArg_AvgExpr(t *testing.T) {
	tr := newTranslator(t)
	_, err := tr.Translate("SELECT AVG(price * qty) AS avg_rev FROM orders GROUP BY category")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
}

func TestAggExprArg_MinMaxExpr(t *testing.T) {
	tr := newTranslator(t)
	_, err := tr.Translate("SELECT MIN(price - discount) AS min_net, MAX(price - discount) AS max_net FROM products")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
}

// ─── #9  Non-equi JOIN ──────────────────────────────────────────────────────

func TestNonEquiJoin_GreaterThan(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT a.name, b.name FROM t1 a JOIN t2 b ON a.score > b.threshold")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	agg, ok := st.(*stmt.AggregateStmt)
	if !ok {
		t.Fatalf("expected AggregateStmt, got %T", st)
	}
	// Should use pipeline-form $lookup.
	if len(agg.Pipeline) < 2 {
		t.Fatalf("pipeline too short: %d stages", len(agg.Pipeline))
	}
}

func TestNonEquiJoin_Complex(t *testing.T) {
	tr := newTranslator(t)
	_, err := tr.Translate("SELECT a.id, b.id FROM items a LEFT JOIN discounts b ON a.category = b.category AND a.price > b.min_price")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
}

// ─── #11  INSERT INTO ... SELECT ────────────────────────────────────────────

func TestInsertSelect_Basic(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("INSERT INTO archive (id, name) SELECT id, name FROM users WHERE age > 60")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	is, ok := st.(*stmt.InsertSelectStmt)
	if !ok {
		t.Fatalf("expected InsertSelectStmt, got %T", st)
	}
	if is.TargetCollection != "archive" {
		t.Fatalf("target: got %q", is.TargetCollection)
	}
	if is.SourceCollection != "users" {
		t.Fatalf("source: got %q", is.SourceCollection)
	}
	if len(is.Columns) != 2 || is.Columns[0] != "id" || is.Columns[1] != "name" {
		t.Fatalf("columns: got %v", is.Columns)
	}
}

func TestInsertSelect_WithExpr(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("INSERT INTO summary (name, total) SELECT name, price * qty AS total FROM orders")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	_, ok := st.(*stmt.InsertSelectStmt)
	if !ok {
		t.Fatalf("expected InsertSelectStmt, got %T", st)
	}
}
