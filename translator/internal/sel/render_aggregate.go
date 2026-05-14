package sel

import (
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"vitess.io/vitess/go/vt/sqlparser"

	"github.com/example/mongodb-sql-driver/translator/internal/expr"
	"github.com/example/mongodb-sql-driver/translator/plan"
	"github.com/example/mongodb-sql-driver/translator/stmt"
)

// buildAggregate constructs a MongoDB aggregation pipeline for a SelectPlan
// that involves JOIN, GROUP BY, HAVING, aggregate functions, or expressions.
func buildAggregate(p *plan.SelectPlan) (stmt.Statement, error) {
	mainAlias := p.MainSource.Alias
	mainColl := p.MainSource.Collection
	s := p.Raw

	pipeline := make([]bson.M, 0, 8)

	// JOIN -> $lookup + $unwind.
	for _, j := range p.Joins {
		lookupStage, err := buildLookupStage(j, mainAlias)
		if err != nil {
			return nil, err
		}
		pipeline = append(pipeline, lookupStage)
		pipeline = append(pipeline, bson.M{"$unwind": bson.M{
			"path":                       "$" + j.Right.Alias,
			"preserveNullAndEmptyArrays": j.Outer,
		}})
	}

	// WHERE -> $match.
	if s.Where != nil {
		filter, err := translateWhereWithMain(s.Where.Expr, mainAlias)
		if err != nil {
			return nil, err
		}
		pipeline = append(pipeline, bson.M{"$match": filter})
	}

	// GROUP BY / aggregate functions -> $group.
	groupExprs := []sqlparser.Expr{}
	if s.GroupBy != nil {
		groupExprs = s.GroupBy.Exprs
	}

	if p.HasAgg || len(groupExprs) > 0 {
		group, err := buildGroup(groupExprs, p.Items, mainAlias)
		if err != nil {
			return nil, err
		}
		pipeline = append(pipeline, bson.M{"$group": group})

		project := bson.M{"_id": 0}
		if len(groupExprs) > 0 {
			for _, e := range groupExprs {
				cn, ok := e.(*sqlparser.ColName)
				if ok {
					name := cn.Name.String()
					project[name] = "$_id." + name
				}
			}
		}
		for _, item := range p.Items {
			if item.Kind == plan.SelectItemAggregate {
				project[aggResultName(item, mainAlias)] = 1
			}
		}
		pipeline = append(pipeline, bson.M{"$project": project})
	}

	// HAVING -> $match (after group).
	if s.Having != nil {
		filter, err := expr.TranslateWhere(s.Having.Expr)
		if err != nil {
			return nil, err
		}
		pipeline = append(pipeline, bson.M{"$match": filter})
	}

	// ORDER BY.
	if len(s.OrderBy) > 0 {
		sortDoc := bson.M{}
		for _, o := range s.OrderBy {
			dir := 1
			if o.Direction == sqlparser.DescOrder {
				dir = -1
			}
			cn, ok := o.Expr.(*sqlparser.ColName)
			if ok {
				sortDoc[renderColName(cn, mainAlias)] = dir
			} else {
				// ORDER BY expression — use the alias if available, or generate
				// a temporary projected name. For now, try to find a matching
				// SELECT item alias.
				alias := findExprAlias(o.Expr, p.Items)
				if alias != "" {
					sortDoc[alias] = dir
				} else {
					return nil, fmt.Errorf("ORDER BY expression must have an alias in SELECT list")
				}
			}
		}
		pipeline = append(pipeline, bson.M{"$sort": sortDoc})
	}

	// LIMIT / OFFSET.
	if p.Offset > 0 {
		pipeline = append(pipeline, bson.M{"$skip": p.Offset})
	}
	if p.Limit > 0 {
		pipeline = append(pipeline, bson.M{"$limit": p.Limit})
	}

	// Final projection — handles plain fields, expression items, and SELECT *.
	if !p.HasAgg && len(groupExprs) == 0 && !p.HasStar && len(p.Items) > 0 {
		project := bson.M{"_id": 0}
		askedForID := false
		for _, item := range p.Items {
			switch item.Kind {
			case plan.SelectItemField:
				if item.Field == nil {
					return nil, fmt.Errorf("nil field in SelectItemField")
				}
				path := renderFieldRef(*item.Field, mainAlias)
				outName := path
				if item.Alias != "" {
					outName = item.Alias
				}
				// If outName contains a dot (joined field like "t.level"),
				// flatten it to the leaf name (e.g. "level": "$t.level").
				if outName == path && strings.Contains(outName, ".") {
					parts := strings.Split(outName, ".")
					outName = parts[len(parts)-1]
				}
				if outName == path {
					project[outName] = 1
				} else {
					project[outName] = "$" + path
				}
				if outName == "_id" {
					askedForID = true
				}
			case plan.SelectItemExpr:
				aggExpr, err := expr.ToAggExprWithMain(item.RawExpr, mainAlias)
				if err != nil {
					return nil, fmt.Errorf("expression projection: %w", err)
				}
				outName := item.Alias
				if outName == "" {
					outName = sqlparser.String(item.RawExpr)
				}
				project[outName] = aggExpr
			default:
				return nil, fmt.Errorf("unsupported select expression in aggregate: kind=%s", item.Kind)
			}
		}
		if askedForID {
			delete(project, "_id")
		}
		pipeline = append(pipeline, bson.M{"$project": project})
	}

	return &stmt.AggregateStmt{Collection: mainColl, Pipeline: pipeline}, nil
}

// buildLookupStage creates the $lookup stage for a join.
// For equi-joins it uses the simple localField/foreignField form.
// For non-equi joins it uses the pipeline form with $expr.
func buildLookupStage(j plan.JoinPlan, mainAlias string) (bson.M, error) {
	if j.OnExpr == nil {
		// Equi-join (classic form).
		return bson.M{"$lookup": bson.M{
			"from":         j.Right.Collection,
			"localField":   j.LeftField.UnqualifiedPath(),
			"foreignField": j.RightField.UnqualifiedPath(),
			"as":           j.Right.Alias,
		}}, nil
	}

	// Non-equi join: use pipeline form.
	// Collect all fields from the left side that need to be passed as "let"
	// variables, then build a $match inside the pipeline.
	letVars := bson.M{}
	rightAlias := j.Right.Alias

	// Walk the ON expression to find left-side column references and convert
	// them to let variables. Left-side columns are those not qualified with
	// the right alias (or qualified with the left/main alias).
	var collectLeftCols func(sqlparser.Expr)
	collectLeftCols = func(e sqlparser.Expr) {
		if e == nil {
			return
		}
		switch v := e.(type) {
		case *sqlparser.ColName:
			q := ""
			if !v.Qualifier.IsEmpty() {
				q = v.Qualifier.Name.String()
			}
			if q != rightAlias {
				varName := "left_" + v.Name.String()
				fieldPath := v.Name.String()
				if q != "" && q != mainAlias {
					fieldPath = q + "." + v.Name.String()
				}
				letVars[varName] = "$" + fieldPath
			}
		case *sqlparser.ComparisonExpr:
			collectLeftCols(v.Left)
			collectLeftCols(v.Right)
		case *sqlparser.AndExpr:
			collectLeftCols(v.Left)
			collectLeftCols(v.Right)
		case *sqlparser.OrExpr:
			collectLeftCols(v.Left)
			collectLeftCols(v.Right)
		case *sqlparser.BinaryExpr:
			collectLeftCols(v.Left)
			collectLeftCols(v.Right)
		}
	}
	collectLeftCols(j.OnExpr)

	// Build the $expr inside the lookup pipeline's $match.
	// Left-side columns become $$left_<name>, right-side columns become $<name>.
	matchExpr, err := translateJoinOnExpr(j.OnExpr, mainAlias, rightAlias)
	if err != nil {
		return nil, fmt.Errorf("non-equi JOIN ON: %w", err)
	}

	lookupPipeline := []bson.M{
		{"$match": bson.M{"$expr": matchExpr}},
	}

	return bson.M{"$lookup": bson.M{
		"from":     j.Right.Collection,
		"let":      letVars,
		"pipeline": lookupPipeline,
		"as":       rightAlias,
	}}, nil
}

// translateJoinOnExpr converts an ON expression for a non-equi join into
// a MongoDB aggregation expression suitable for use inside $lookup pipeline's
// $match.$expr. Left-side columns use $$left_<name> (let variables), and
// right-side columns use $<name> (current document in the "from" collection).
func translateJoinOnExpr(e sqlparser.Expr, mainAlias, rightAlias string) (interface{}, error) {
	if e == nil {
		return nil, nil
	}
	switch v := e.(type) {
	case *sqlparser.ColName:
		q := ""
		if !v.Qualifier.IsEmpty() {
			q = v.Qualifier.Name.String()
		}
		if q == rightAlias {
			return "$" + v.Name.String(), nil
		}
		// Left side → let variable.
		return "$$left_" + v.Name.String(), nil
	case *sqlparser.ComparisonExpr:
		left, err := translateJoinOnExpr(v.Left, mainAlias, rightAlias)
		if err != nil {
			return nil, err
		}
		right, err := translateJoinOnExpr(v.Right, mainAlias, rightAlias)
		if err != nil {
			return nil, err
		}
		op := ""
		switch v.Operator {
		case sqlparser.EqualOp:
			op = "$eq"
		case sqlparser.NotEqualOp:
			op = "$ne"
		case sqlparser.LessThanOp:
			op = "$lt"
		case sqlparser.LessEqualOp:
			op = "$lte"
		case sqlparser.GreaterThanOp:
			op = "$gt"
		case sqlparser.GreaterEqualOp:
			op = "$gte"
		default:
			return nil, fmt.Errorf("unsupported ON operator: %s", v.Operator.ToString())
		}
		return map[string]interface{}{op: []interface{}{left, right}}, nil
	case *sqlparser.AndExpr:
		l, err := translateJoinOnExpr(v.Left, mainAlias, rightAlias)
		if err != nil {
			return nil, err
		}
		r, err := translateJoinOnExpr(v.Right, mainAlias, rightAlias)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"$and": []interface{}{l, r}}, nil
	case *sqlparser.OrExpr:
		l, err := translateJoinOnExpr(v.Left, mainAlias, rightAlias)
		if err != nil {
			return nil, err
		}
		r, err := translateJoinOnExpr(v.Right, mainAlias, rightAlias)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"$or": []interface{}{l, r}}, nil
	case *sqlparser.Literal:
		return expr.LiteralValue(v)
	case *sqlparser.BinaryExpr:
		left, err := translateJoinOnExpr(v.Left, mainAlias, rightAlias)
		if err != nil {
			return nil, err
		}
		right, err := translateJoinOnExpr(v.Right, mainAlias, rightAlias)
		if err != nil {
			return nil, err
		}
		switch v.Operator {
		case sqlparser.PlusOp:
			return map[string]interface{}{"$add": []interface{}{left, right}}, nil
		case sqlparser.MinusOp:
			return map[string]interface{}{"$subtract": []interface{}{left, right}}, nil
		case sqlparser.MultOp:
			return map[string]interface{}{"$multiply": []interface{}{left, right}}, nil
		case sqlparser.DivOp:
			return map[string]interface{}{"$divide": []interface{}{left, right}}, nil
		}
		return nil, fmt.Errorf("unsupported binary op in ON: %s", v.Operator.ToString())
	}
	return nil, fmt.Errorf("unsupported expression in JOIN ON: %T", e)
}

// findExprAlias finds the alias for an ORDER BY expression among SELECT items.
func findExprAlias(e sqlparser.Expr, items []plan.SelectItem) string {
	target := sqlparser.String(e)
	for _, item := range items {
		if item.Alias != "" && sqlparser.String(item.RawExpr) == target {
			return item.Alias
		}
	}
	return ""
}

// renderColName turns a sqlparser column reference into the MongoDB field path.
func renderColName(cn *sqlparser.ColName, mainAlias string) string {
	name := cn.Name.String()
	if cn.Qualifier.IsEmpty() {
		return name
	}
	q := cn.Qualifier.Name.String()
	if q == mainAlias {
		return name
	}
	return q + "." + name
}

// renderFieldRef is the FieldRef equivalent of renderColName.
func renderFieldRef(f plan.FieldRef, mainAlias string) string {
	if f.SourceAlias == "" || f.SourceAlias == mainAlias {
		return f.UnqualifiedPath()
	}
	return f.SourceAlias + "." + f.UnqualifiedPath()
}

func translateWhereWithMain(e sqlparser.Expr, mainAlias string) (bson.M, error) {
	if mainAlias != "" {
		stripMainQualifier(e, mainAlias)
	}
	return expr.TranslateWhere(e)
}

func stripMainQualifier(e sqlparser.Expr, mainAlias string) {
	pre := func(c *sqlparser.Cursor) bool {
		cn, ok := c.Node().(*sqlparser.ColName)
		if !ok {
			return true
		}
		if !cn.Qualifier.IsEmpty() && cn.Qualifier.Name.String() == mainAlias {
			cn.Qualifier = sqlparser.TableName{}
		}
		return true
	}
	sqlparser.Rewrite(e, pre, nil)
}

// buildGroup constructs the $group stage document.
func buildGroup(groupExprs []sqlparser.Expr, items []plan.SelectItem, mainAlias string) (bson.M, error) {
	group := bson.M{}

	switch len(groupExprs) {
	case 0:
		group["_id"] = nil
	case 1:
		cn, ok := groupExprs[0].(*sqlparser.ColName)
		if !ok {
			return nil, fmt.Errorf("GROUP BY supports column references only")
		}
		group["_id"] = bson.M{cn.Name.String(): "$" + renderColName(cn, mainAlias)}
	default:
		idDoc := bson.M{}
		for _, e := range groupExprs {
			cn, ok := e.(*sqlparser.ColName)
			if !ok {
				return nil, fmt.Errorf("GROUP BY supports column references only")
			}
			idDoc[cn.Name.String()] = "$" + renderColName(cn, mainAlias)
		}
		group["_id"] = idDoc
	}

	for _, item := range items {
		if item.Kind != plan.SelectItemAggregate || item.Agg == nil {
			continue
		}
		op := string(item.Agg.Func)
		name := aggResultName(item, mainAlias)

		// Determine the argument: either a simple field path or an expression.
		var argVal interface{}
		if item.Agg.Star {
			argVal = nil // for COUNT(*)
		} else if item.Agg.ArgExpr != nil {
			// Expression argument (e.g. SUM(price * qty)).
			ae, err := expr.ToAggExprWithMain(item.Agg.ArgExpr, mainAlias)
			if err != nil {
				return nil, fmt.Errorf("aggregate %s argument: %w", op, err)
			}
			argVal = ae
		} else if item.Agg.Arg != nil {
			argVal = "$" + renderFieldRef(*item.Agg.Arg, mainAlias)
		}

		switch op {
		case "COUNT":
			if item.Agg.Star {
				group[name] = bson.M{"$sum": 1}
			} else {
				group[name] = bson.M{"$sum": bson.M{
					"$cond": []interface{}{
						bson.M{"$ifNull": []interface{}{argVal, false}},
						1, 0,
					},
				}}
			}
		case "SUM":
			group[name] = bson.M{"$sum": argVal}
		case "AVG":
			group[name] = bson.M{"$avg": argVal}
		case "MIN":
			group[name] = bson.M{"$min": argVal}
		case "MAX":
			group[name] = bson.M{"$max": argVal}
		default:
			return nil, fmt.Errorf("unsupported aggregate: %s", op)
		}
	}
	return group, nil
}

func aggArg(a *plan.AggSpec, mainAlias string) string {
	if a.Star || a.Arg == nil {
		return "*"
	}
	return renderFieldRef(*a.Arg, mainAlias)
}

func aggResultName(item plan.SelectItem, mainAlias string) string {
	if item.Alias != "" {
		return item.Alias
	}
	if item.Agg == nil {
		return "value"
	}
	op := strings.ToLower(string(item.Agg.Func))
	if item.Agg.Star {
		return op + "_star"
	}
	if item.Agg.ArgExpr != nil {
		return op + "_expr"
	}
	arg := aggArg(item.Agg, mainAlias)
	if arg == "*" {
		return op + "_star"
	}
	return op + "_" + arg
}
