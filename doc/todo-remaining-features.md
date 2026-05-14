# 待实现功能清单

本文档列出尚未实现的 SQL 功能，按实施难度和价值评估排序。
供后续 AI 辅助开发使用。

## 项目上下文

- SQL 解析器：vitess `v0.24.1`
- 翻译架构：SQL AST → `plan.SelectPlan`（语义中间表示）→ `stmt.Statement`（可执行）
- 关键文件：
  - `translator/internal/expr/agg_expr.go` — SQL 表达式 → MongoDB 聚合表达式翻译器
  - `translator/internal/expr/expr.go` — WHERE 过滤器翻译 + 标量值求值
  - `translator/internal/sel/plan.go` — SELECT 语义规划
  - `translator/internal/sel/items.go` — SELECT 列分类（Field / Aggregate / Expr）
  - `translator/internal/sel/render_aggregate.go` — 聚合管道生成
  - `translator/internal/sel/render_find.go` — Find 查询生成
  - `translator/internal/sel/source.go` — FROM / JOIN 解析
  - `translator/internal/write/write.go` — INSERT / UPDATE / DELETE 翻译
  - `translator/translator.go` — 入口分派（按 AST 类型）
  - `driver/driver.go` — MongoDB 执行层
  - `translator/stmt/stmt.go` — 可执行语句模型

---

## P1：高价值、中等难度（建议下一轮实施）

### 1. UNION / UNION ALL

**价值**：高 — BI 和报表场景常用
**难度**：中 (1-2 周)
**方案**：
- vitess 解析 `*sqlparser.Union` 作为顶层 AST 节点
- `translator.go` 新增 `case *sqlparser.Union` 分支
- 左右两侧分别编译为 `AggregateStmt`
- 用 MongoDB 4.4+ 的 `$unionWith` 阶段拼接
- `UNION`（去重）需在 `$unionWith` 后追加 `$group` 去重
- `UNION ALL` 直接拼接无需去重

**关键代码位置**：
- `translator/translator.go:40-50` — 新增 `*sqlparser.Union` case
- 建议新建 `translator/internal/setop/union.go`

**测试建议**：
```sql
SELECT name FROM fruits UNION ALL SELECT name FROM vegetables;
SELECT city FROM users UNION SELECT city FROM orders;
```

---

### 2. 非关联子查询 WHERE IN (SELECT ...)

**价值**：高 — 极常见的查询模式
**难度**：中-高 (1-2 周)
**方案**：
- 在 `expr.go` 的 `translateComparison` 中检测 `c.Right` 为 `*sqlparser.Subquery`
- 方案 A（两阶段）：先执行内层 SELECT 得到值列表，再用 `$in` 构造外层过滤器
- 方案 B（单阶段）：用 `$lookup` + `$match` + `$expr:{$in:...}` 拼接
- 方案 A 实现简单但需要额外的 MongoDB 查询；方案 B 性能更好但复杂度高

**关键代码位置**：
- `translator/internal/expr/expr.go:868-879` — IN/NOT IN 分支
- `driver/driver.go` — 需要支持"先执行子查询再执行外查询"的两阶段模式

**测试建议**：
```sql
SELECT name FROM users WHERE city IN (SELECT city FROM big_cities);
SELECT * FROM orders WHERE user_id NOT IN (SELECT _id FROM blocked_users);
```

---

### 3. GROUP BY 表达式

**价值**：中-高 — 日期分组 `GROUP BY YEAR(created_at)` 很常见
**难度**：低-中 (3-5 天)
**方案**：
- `render_aggregate.go:buildGroup` 中的 `groupExprs` 当前强制要求 `*sqlparser.ColName`
- 放开限制：非 ColName 时用 `ToAggExprWithMain` 翻译为聚合表达式
- `$group._id` 中使用翻译后的表达式
- 后续 `$project` 展开时需为表达式生成合理的输出名

**关键代码位置**：
- `translator/internal/sel/render_aggregate.go:393-416` — `buildGroup` 函数

**测试建议**：
```sql
SELECT YEAR(created_at) AS yr, COUNT(*) AS cnt FROM orders GROUP BY YEAR(created_at);
SELECT price * 10 AS bucket, COUNT(*) FROM products GROUP BY price * 10;
```

---

### 4. ORDER BY 表达式（通用化）

**价值**：中 — 常用于计算字段排序
**难度**：低 (2-3 天)
**方案**：
- Find 路径：遇到非 ColName 的 ORDER BY 时自动升级到 aggregation pipeline
- Aggregation 路径：用 `$addFields` 添加临时排序字段，`$sort` 引用，最终 `$project` 中移除
- 或要求用户通过 SELECT 别名排序（当前已支持）

**关键代码位置**：
- `translator/internal/sel/limits.go:13-29` — Find 路径的 `buildSort`
- `translator/internal/sel/plan.go:41` — `useAggregation` 判定

**测试建议**：
```sql
SELECT name, price * qty AS rev FROM products ORDER BY rev DESC;
SELECT name FROM products ORDER BY price * qty DESC;
```

---

## P2：中等价值、中等难度

### 5. 关联子查询 / EXISTS

**价值**：中 — 高级查询场景
**难度**：高 (2-3 周)
**方案**：
- 检测 `WHERE EXISTS (SELECT ...)` — vitess 解析为 `*sqlparser.ExistsExpr`
- 用 `$lookup` pipeline 形式实现关联：外层列通过 `let` 传入，内层 `$match.$expr` 引用
- 检查结果数组是否为空：`{$match: {$expr: {$gt: [{$size: "$__exists_tmp"}, 0]}}}`
- 需递归调用 `sel.Plan` 编译内层 SELECT

**关键代码位置**：
- `translator/internal/expr/expr.go:741` — `TranslateWhere` 新增 `*sqlparser.ExistsExpr`
- 建议新建 `translator/internal/subquery/subquery.go`

**测试建议**：
```sql
SELECT name FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.user_id = u._id);
```

---

### 6. FROM 子查询（派生表）

**价值**：中 — 报表和嵌套聚合
**难度**：高 (2-3 周)
**方案**：
- `source.go:72` 当前拒绝非 `sqlparser.TableName` 的 FROM 项
- 检测 `*sqlparser.Subquery` → 递归编译内层 SELECT 得到管道
- 如果外层和内层的源集合相同：把内层管道合并到外层管道前段
- 如果不同：用 `$out` / `$merge` 到临时集合再查（需管理清理）

**限制**：MongoDB 没有"内联子集合"概念，跨集合派生表的性能和实现代价较高

**关键代码位置**：
- `translator/internal/sel/source.go:71-73` — FROM 子查询拒绝点

---

### 7. RIGHT JOIN / FULL OUTER JOIN

**价值**：低-中
**难度**：中 (1 周)
**方案**：
- RIGHT JOIN = 交换左右表 + LEFT JOIN，在 `source.go` 重排即可
- FULL OUTER JOIN：MongoDB 无原生支持，需要两次 `$lookup` + `$unionWith` 合并
- FULL OUTER JOIN 复杂度高且性能差

**关键代码位置**：
- `translator/internal/sel/source.go:57` — Join 类型判断

---

### 8. UPDATE / DELETE 多表

**价值**：低-中 — MySQL 特有语法
**难度**：中-高 (1-2 周)
**方案**：
- 先用 aggregation 查出目标文档 `_id` 集合
- 再用 `UpdateMany` / `DeleteMany` 过滤 `{_id: {$in: [...]}}`
- 失去原子性；在副本集上可用 `Session.WithTransaction` 包裹

**关键代码位置**：
- `translator/internal/write/write.go:59` — 当前单表限制

---

## P3：低价值或高难度（按需实施）

### 9. 窗口函数 ROW_NUMBER() / RANK() / LAG() / SUM() OVER (...)

**价值**：中（分析场景有用）
**难度**：高 (3-4 周)
**方案**：
- MongoDB 5.0+ 的 `$setWindowFields` 阶段
- vitess AST 中 `OVER` 子句的 `PARTITION BY` / `ORDER BY` / frame 需要解析
- 新增 plan 层窗口语义模型
- frame 子句（`ROWS BETWEEN ...`）的 MySQL ↔ MongoDB 语义差异需仔细验证

**约束**：仅 MongoDB >= 5.0

**关键代码位置**：
- vitess AST: `sqlparser.OverClause`, `sqlparser.WindowSpecification`
- 建议新建 `translator/internal/window/window.go`

---

### 10. INTERSECT / EXCEPT

**价值**：低
**难度**：高 (2-3 周)
**方案**：
- MongoDB 无原生支持
- INTERSECT：两侧分别查询 → `$lookup` 自连接 + 过滤存在
- EXCEPT：`$lookup` 反连接 + 过滤不存在
- NULL 处理和去重语义需仔细对齐

---

### 11. 事务 BEGIN / COMMIT / ROLLBACK

**价值**：低-中（取决于使用场景）
**难度**：高 (2-3 周)
**方案**：
- MongoDB 副本集 / 分片集群支持多文档事务
- `driver.go` 引入 session 状态，每个连接绑定 `mongo.Session`
- `mysql-simulator/handler.go` 的 `BEGIN` 从空操作改为 `session.StartTransaction()`
- `COMMIT` → `session.CommitTransaction()`
- `ROLLBACK` → `session.AbortTransaction()`

**约束**：
- 单机 MongoDB 不支持事务
- DDL 无法纳入事务
- Savepoint / 嵌套事务不可实现

**关键代码位置**：
- `mysql-simulator/meta.go:32-36` — BEGIN/COMMIT/ROLLBACK 当前返回空 OK

---

### 12. SELECT FOR UPDATE

**价值**：低
**难度**：中
**方案**：在 MongoDB 事务上下文中，文档锁自动生效，只需把 SELECT 包在活跃事务中即可。依赖 #11 事务实现。

---

## 已完成功能速查

以下功能在之前两轮迭代中已实现，不需要再做：

| 功能 | 实现轮次 |
|---|---|
| WHERE 列与列比较 (`WHERE a = b`) | 第 1 轮 |
| UPDATE SET 引用列 (`SET cnt = cnt + 1`) | 第 1 轮 |
| INSERT VALUES 常量算术 (`VALUES (1+2)`) | 第 1 轮 |
| 表达式投影 (`SELECT a+b, UPPER(name)`) | 第 2 轮 |
| CASE WHEN | 第 2 轮 |
| WHERE 左侧表达式 (`WHERE price*qty > 100`) | 第 2 轮 |
| 非等值 JOIN (`ON a.x > b.y`) | 第 2 轮 |
| INSERT INTO ... SELECT | 第 2 轮 |
| 聚合函数表达式参数 (`SUM(price*qty)`) | 第 2 轮 |
| mysql-simulator 认证修复 (caching_sha2_password) | 第 1 轮 |
