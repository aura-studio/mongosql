// Unit tests for the three "easy" features added in this iteration:
//   #1  WHERE col = col  (column-to-column comparison via $expr)
//   #4  UPDATE SET col = col ± expr  (pipeline-style update)
//   #5  INSERT VALUES (constant arithmetic expressions)
//
// These tests only exercise the translator layer — no MongoDB connection needed.
package tests

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/example/mongodb-sql-driver/translator/stmt"
)

// ─── #1  WHERE col = col ─────────────────────────────────────────────────────

func TestWhereColEqualsCol(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT * FROM t WHERE a = b")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	find, ok := st.(*stmt.FindStmt)
	if !ok {
		t.Fatalf("expected FindStmt, got %T", st)
	}
	want := bson.M{"$expr": bson.M{"$eq": []interface{}{"$a", "$b"}}}
	if !reflect.DeepEqual(find.Filter, want) {
		t.Fatalf("filter:\n got  %#v\n want %#v", find.Filter, want)
	}
}

func TestWhereColNotEqualCol(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT * FROM t WHERE x != y")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	find := st.(*stmt.FindStmt)
	want := bson.M{"$expr": bson.M{"$ne": []interface{}{"$x", "$y"}}}
	if !reflect.DeepEqual(find.Filter, want) {
		t.Fatalf("filter: got %#v, want %#v", find.Filter, want)
	}
}

func TestWhereColLtCol(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT * FROM t WHERE score < max_score")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	find := st.(*stmt.FindStmt)
	want := bson.M{"$expr": bson.M{"$lt": []interface{}{"$score", "$max_score"}}}
	if !reflect.DeepEqual(find.Filter, want) {
		t.Fatalf("filter: got %#v, want %#v", find.Filter, want)
	}
}

// Column-to-literal still takes the fast (non-$expr) path.
func TestWhereColEqualsLiteralStillFastPath(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("SELECT * FROM t WHERE age = 18")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	find := st.(*stmt.FindStmt)
	// Fast path: {age: {$eq: 18}}
	want := bson.M{"age": bson.M{"$eq": int64(18)}}
	if !reflect.DeepEqual(find.Filter, want) {
		t.Fatalf("filter: got %#v, want %#v", find.Filter, want)
	}
}

// ─── #4  UPDATE SET col = col ± expr ─────────────────────────────────────────

func TestUpdateSetColRefProducesPipelineMarker(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("UPDATE counters SET cnt = cnt + 1 WHERE id = 1")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	upd, ok := st.(*stmt.UpdateStmt)
	if !ok {
		t.Fatalf("expected UpdateStmt, got %T", st)
	}
	// The translator marks pipeline updates with "$pipeline": true.
	if _, ok := upd.Update["$pipeline"]; !ok {
		t.Fatalf("expected $pipeline marker in update doc, got %#v", upd.Update)
	}
	setDoc, ok := upd.Update["$set"].(bson.M)
	if !ok {
		t.Fatalf("expected $set in update doc, got %#v", upd.Update)
	}
	// cnt should translate to {$add: ["$cnt", 1]}
	wantCnt := map[string]interface{}{"$add": []interface{}{"$cnt", int64(1)}}
	if !reflect.DeepEqual(setDoc["cnt"], wantCnt) {
		t.Fatalf("cnt expr:\n got  %#v\n want %#v", setDoc["cnt"], wantCnt)
	}
}

func TestUpdateSetLiteralNoMarker(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("UPDATE users SET name = 'bob' WHERE id = 1")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	upd := st.(*stmt.UpdateStmt)
	if _, ok := upd.Update["$pipeline"]; ok {
		t.Fatalf("literal SET should NOT produce $pipeline marker")
	}
	setDoc := upd.Update["$set"].(bson.M)
	if setDoc["name"] != "bob" {
		t.Fatalf("expected name=bob, got %v", setDoc["name"])
	}
}

func TestUpdateSetColMultiply(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("UPDATE products SET price = price * 2")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	upd := st.(*stmt.UpdateStmt)
	if _, ok := upd.Update["$pipeline"]; !ok {
		t.Fatalf("expected $pipeline marker")
	}
	setDoc := upd.Update["$set"].(bson.M)
	want := map[string]interface{}{"$multiply": []interface{}{"$price", int64(2)}}
	if !reflect.DeepEqual(setDoc["price"], want) {
		t.Fatalf("price expr:\n got  %#v\n want %#v", setDoc["price"], want)
	}
}

// ─── #5  INSERT VALUES (constant arithmetic) ─────────────────────────────────

func TestInsertConstArith(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("INSERT INTO t (a, b, c) VALUES (1+2, 10*3, 7-1)")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	ins, ok := st.(*stmt.InsertStmt)
	if !ok {
		t.Fatalf("expected InsertStmt, got %T", st)
	}
	if len(ins.Docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(ins.Docs))
	}
	doc := ins.Docs[0]
	if doc["a"] != int64(3) {
		t.Fatalf("a: got %v want 3", doc["a"])
	}
	if doc["b"] != int64(30) {
		t.Fatalf("b: got %v want 30", doc["b"])
	}
	if doc["c"] != int64(6) {
		t.Fatalf("c: got %v want 6", doc["c"])
	}
}

func TestInsertNegativeAndFloat(t *testing.T) {
	tr := newTranslator(t)
	st, err := tr.Translate("INSERT INTO t (x, y) VALUES (-5, 1.5 * 2)")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	ins := st.(*stmt.InsertStmt)
	doc := ins.Docs[0]
	if doc["x"] != int64(-5) {
		t.Fatalf("x: got %v want -5", doc["x"])
	}
	if doc["y"] != float64(3) {
		t.Fatalf("y: got %v want 3.0", doc["y"])
	}
}

func TestInsertColRefRejected(t *testing.T) {
	tr := newTranslator(t)
	_, err := tr.Translate("INSERT INTO t (a) VALUES (other_col)")
	if err == nil {
		t.Fatal("expected error for column reference in INSERT VALUES")
	}
}
