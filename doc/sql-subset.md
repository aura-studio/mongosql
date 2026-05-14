# 支持的 SQL 子集

本文档列出当前项目支持的 SQL 语法范围，以及每个语法元素对应的 MongoDB 执行方式。

## SELECT

| 语法 | 示例 | MongoDB 映射 | 备注 |
|---|---|---|---|
| `SELECT *` | `SELECT * FROM users` | `Find` 不设 projection | |
| 指定列 | `SELECT name, age FROM users` | `Find` + projection `{name:1, age:1, _id:0}` | 未显式选 `_id` 时自动隐藏 |
| 列别名 | `SELECT name AS n FROM users` | projection 或 `$project` 中用别名作 key | |
| 表达式投影 | `SELECT price * qty AS revenue FROM t` | aggregation `$project` + `$multiply` | 自动升级为 aggregation pipeline |
| 函数投影 | `SELECT UPPER(name) AS uname FROM t` | `$project` + `$toUpper` | 支持 30+ 标量函数 |
| `CASE WHEN` | `SELECT CASE WHEN price>10 THEN 'hi' ELSE 'lo' END AS tier FROM t` | `$project` + `$switch` / `$cond` | 支持简单/搜索两种形式 |
| `DISTINCT`（单列） | `SELECT DISTINCT city FROM users` | `collection.Distinct()` | |
| `DISTINCT`（多列） | `SELECT DISTINCT a, b FROM t` | aggregation pipeline | 走 `$group` |
| 嵌套字段 | `SELECT a.b FROM users` | projection `{"a.b": 1}` | 点号被当作嵌套文档路径 |
| `db.collection` | `SELECT * FROM mydb.users` | `SourceRef.Database` 被填充 | 用于 INSERT...SELECT 的跨库目标 |

### 表达式投影支持的函数

以下函数可用于 SELECT 列表中（当参数包含列引用时自动翻译为 MongoDB 聚合表达式）：

| 类别 | 函数 |
|---|---|
| 字符串 | `UPPER`, `LOWER`, `CONCAT`, `CONCAT_WS`, `SUBSTRING`, `LENGTH`, `TRIM`, `LTRIM`, `RTRIM`, `REPLACE`, `LEFT`, `RIGHT`, `REVERSE` |
| 数值 | `ABS`, `CEIL`, `FLOOR`, `ROUND`, `MOD`, `POW`, `SQRT`, `LOG`, `LOG10`, `LOG2`, `EXP` |
| 条件 | `IF`, `IFNULL`, `COALESCE`, `NULLIF`, `CASE WHEN` |
| 日期 | `YEAR`, `MONTH`, `DAY`, `HOUR`, `MINUTE`, `SECOND` |
| 类型 | `CAST`, `CONVERT` |

## WHERE

| 语法 | 示例 | MongoDB 映射 |
|---|---|---|
| `=` | `WHERE age = 18` | `{age: {$eq: 18}}` |
| `!=` / `<>` | `WHERE age != 18` | `{age: {$ne: 18}}` |
| `<` / `<=` / `>` / `>=` | `WHERE age > 18` | `{age: {$gt: 18}}` |
| `AND` | `WHERE a = 1 AND b = 2` | `{$and: [...]}` |
| `OR` | `WHERE a = 1 OR b = 2` | `{$or: [...]}` |
| `NOT` | `WHERE NOT (a = 1)` | `{$nor: [...]}` |
| `IN` | `WHERE id IN (1,2,3)` | `{id: {$in: [1,2,3]}}` |
| `NOT IN` | `WHERE id NOT IN (1,2)` | `{id: {$nin: [1,2]}}` |
| `LIKE` | `WHERE name LIKE '%test%'` | `{name: {$regex: ".*test.*"}}` |
| `NOT LIKE` | `WHERE name NOT LIKE '%x'` | `{name: {$not: {$regex: ...}}}` |
| `REGEXP` | `WHERE name REGEXP '^A'` | `{name: {$regex: "^A"}}` |
| `NOT REGEXP` | `WHERE name NOT REGEXP '^A'` | `{name: {$not: {$regex: ...}}}` |
| `BETWEEN` | `WHERE age BETWEEN 18 AND 30` | `{age: {$gte: 18, $lte: 30}}` |
| `IS NULL` | `WHERE name IS NULL` | `{name: {$eq: null}}` |
| `IS NOT NULL` | `WHERE name IS NOT NULL` | `{name: {$ne: null}}` |
| `IS TRUE` / `IS FALSE` | `WHERE active IS TRUE` | `{active: {$eq: true}}` |
| `IS NOT TRUE` / `IS NOT FALSE` | `WHERE active IS NOT TRUE` | `{active: {$ne: true}}` |
| 列与列比较 | `WHERE a = b` | `{$expr: {$eq: ["$a","$b"]}}` | |
| 表达式比较 | `WHERE price * qty > 100` | `{$expr: {$gt: [{$multiply:["$price","$qty"]}, 100]}}` | 左右侧均可为表达式 |

### WHERE 右侧值类型

| 类型 | 示例 | 备注 |
|---|---|---|
| 字符串 | `'hello'` | |
| 整数 | `42` | 解析为 `int64` |
| 浮点数 / 小数 | `3.14` | 解析为 `float64` |
| 负数 | `-1` | 通过一元负号处理 |
| 布尔 | `TRUE` / `FALSE` | |
| NULL | `NULL` | |
| 值列表 | `(1, 2, 3)` | 用于 `IN` / `NOT IN` |
| 列引用 | `WHERE a = b` | 通过 `$expr` 实现 |
| 算术表达式 | `WHERE a = b + 1` | 通过 `$expr` 实现 |

**不支持**：右侧为子查询。

## ORDER BY / LIMIT / OFFSET

| 语法 | 示例 | MongoDB 映射 |
|---|---|---|
| `ORDER BY` | `ORDER BY age DESC` | `Find` 的 `SetSort` 或 pipeline `$sort` |
| `LIMIT` | `LIMIT 10` | `Find` 的 `SetLimit` 或 pipeline `$limit` |
| `OFFSET` | `LIMIT 10 OFFSET 5` | `Find` 的 `SetSkip` 或 pipeline `$skip` |

**注意**：`ORDER BY` 在 Find 路径下仅支持列引用；在 aggregation 路径下支持通过 SELECT 别名引用表达式排序。

## 聚合

| 语法 | 示例 | MongoDB 映射 |
|---|---|---|
| `COUNT(*)` | `SELECT COUNT(*) FROM t` | `$group` + `{$sum: 1}` |
| `COUNT(col)` | `SELECT COUNT(name) FROM t` | `$group` + `{$sum: {$cond: ...}}` |
| `SUM(col)` | `SELECT SUM(amount) FROM t` | `$group` + `{$sum: "$amount"}` |
| `SUM(expr)` | `SELECT SUM(price * qty) FROM t` | `$group` + `{$sum: {$multiply: ["$price","$qty"]}}` |
| `AVG(col)` | `SELECT AVG(score) FROM t` | `$group` + `{$avg: "$score"}` |
| `AVG(expr)` | `SELECT AVG(price * qty) FROM t` | `$group` + `{$avg: {$multiply: ...}}` |
| `MIN(col/expr)` | `SELECT MIN(price - discount) FROM t` | `$group` + `{$min: {$subtract: ...}}` |
| `MAX(col/expr)` | `SELECT MAX(price - discount) FROM t` | `$group` + `{$max: {$subtract: ...}}` |
| `GROUP BY` | `GROUP BY city` | `$group` 的 `_id` 字段 |
| `HAVING` | `HAVING COUNT(*) > 1` | group 后追加 `$match` |

聚合函数参数支持列引用、`*` 和任意算术/函数表达式。

## JOIN

| 语法 | 示例 | MongoDB 映射 |
|---|---|---|
| 等值 `JOIN ... ON` | `JOIN orders ON users._id = orders.uid` | `$lookup` (localField/foreignField) + `$unwind` |
| 等值 `LEFT JOIN ... ON` | `LEFT JOIN orders ON ...` | `$lookup` + `$unwind`（`preserveNullAndEmptyArrays: true`） |
| 非等值 `JOIN ... ON` | `JOIN t ON a.score > b.min_score` | `$lookup` (let/pipeline) + `$unwind` |
| 非等值 `LEFT JOIN` | `LEFT JOIN t ON a.x >= b.y` | `$lookup` (let/pipeline) + `$unwind`（preserve） |
| 多表链式 JOIN | `A JOIN B ON ... JOIN C ON ...` | 多次 `$lookup` + `$unwind` |
| 带表别名 | `FROM users u JOIN orders o ON u._id = o.uid` | 别名用于字段归属判断 |

非等值 JOIN 支持 `=`, `!=`, `<`, `<=`, `>`, `>=` 以及 `AND`/`OR` 组合条件。

**限制**：
- 右表必须是单表，不支持子查询
- 非等值 JOIN 无法利用索引（由 MongoDB `$lookup` pipeline 限制决定），性能低于等值 JOIN

## INSERT

| 语法 | 示例 | MongoDB 映射 |
|---|---|---|
| 单行插入 | `INSERT INTO t (a, b) VALUES (1, 'x')` | `InsertMany` 单文档 |
| 多行插入 | `INSERT INTO t (a, b) VALUES (1, 'x'), (2, 'y')` | `InsertMany` 多文档 |
| 常量算术 | `INSERT INTO t (a) VALUES (1+2)` | 编译期求值后 `InsertMany` |
| `INSERT ... SELECT` | `INSERT INTO t (a, b) SELECT x, y FROM s WHERE ...` | 先运行 SELECT aggregation，再 `InsertMany` 结果 |

**限制**：
- 必须显式列出列名
- `VALUES` 中不支持引用列（无源数据）；支持常量表达式（算术、函数）
- `INSERT ... SELECT` 不支持 `ON DUPLICATE KEY UPDATE`

## UPDATE

| 语法 | 示例 | MongoDB 映射 |
|---|---|---|
| `UPDATE ... SET ... WHERE` | `UPDATE t SET name = 'x' WHERE id = 1` | `UpdateMany` + `{$set: {name: "x"}}` |
| SET 引用列 | `UPDATE t SET cnt = cnt + 1` | pipeline-style update `[{$set: {cnt: {$add: ["$cnt", 1]}}}]` |
| SET 多列表达式 | `UPDATE t SET price = price * 2, qty = qty + 10` | 同上，支持多列同时引用列 |
| 无 WHERE | `UPDATE t SET name = 'x'` | `UpdateMany` 空 filter |

**限制**：
- 仅支持单表
- SET 右侧支持常量、列引用和算术表达式；不支持子查询
- 引用列的 SET 需要 MongoDB 4.2+（pipeline-style update）

## DELETE

| 语法 | 示例 | MongoDB 映射 |
|---|---|---|
| `DELETE FROM ... WHERE` | `DELETE FROM t WHERE id = 1` | `DeleteMany` |
| 无 WHERE | `DELETE FROM t` | `DeleteMany` 空 filter |

**限制**：
- 仅支持单表

## DDL（通过 mysql-simulator）

以下 DDL 在 mysql-simulator 层直接转为 MongoDB API 调用，不经过 SQL 翻译器：

| 语法 | 实现方式 |
|---|---|
| `CREATE TABLE` | 创建集合 + 保存 schema 元信息 + 创建索引 |
| `DROP TABLE` | `collection.Drop()` |
| `TRUNCATE TABLE` | `collection.Drop()` 后重建 |
| `CREATE INDEX` / `CREATE UNIQUE INDEX` | `collection.Indexes().CreateOne()` |
| `DROP INDEX` | `collection.Indexes().DropOne()` |
| `ALTER TABLE ADD/DROP/MODIFY COLUMN` | 更新 schema 元信息 |
| `ALTER TABLE ADD/DROP INDEX` | 对应 MongoDB 索引操作 |
| `RENAME TABLE` | `collection.Rename()` |
| `CREATE DATABASE` | MongoDB 延迟创建，记录即生效 |
| `DROP DATABASE` | `db.Drop()` |

## CASE WHEN 表达式

| 语法 | 示例 | MongoDB 映射 |
|---|---|---|
| 搜索型（单分支） | `CASE WHEN a>0 THEN 'pos' ELSE 'neg' END` | `{$cond: [{$gt:["$a",0]}, "pos", "neg"]}` |
| 搜索型（多分支） | `CASE WHEN a>10 THEN 'hi' WHEN a>0 THEN 'mid' ELSE 'lo' END` | `{$switch: {branches: [...], default: "lo"}}` |
| 简单型 | `CASE status WHEN 1 THEN 'active' WHEN 2 THEN 'off' END` | `{$switch: {branches: [{case: {$eq: ["$status",1]}, then: "active"}, ...]}}` |

CASE WHEN 可用于 SELECT 列表、UPDATE SET、WHERE（通过 `$expr`）。

## 不支持的语法

以下语法当前不在支持范围内，输入后会返回错误：

| 类别 | 示例 |
|---|---|
| 子查询（FROM） | `SELECT * FROM (SELECT ...)` |
| 子查询（WHERE） | `WHERE id IN (SELECT ...)` |
| 子查询（EXISTS） | `WHERE EXISTS (SELECT ...)` |
| UNION / INTERSECT / EXCEPT | `SELECT ... UNION SELECT ...` |
| 窗口函数 | `ROW_NUMBER() OVER (...)` |
| 事务 | `BEGIN` / `COMMIT` / `ROLLBACK`（连接层面返回 OK 但无实际事务语义） |
| GROUP BY 表达式 | `GROUP BY YEAR(created_at)` |
| ORDER BY 表达式（Find 路径） | `ORDER BY price * qty`（aggregation 路径可通过别名排序） |
| RIGHT JOIN / FULL OUTER JOIN | 仅支持 INNER JOIN 和 LEFT JOIN |
| UPDATE/DELETE 多表 | `UPDATE a JOIN b ON ... SET ...` |
| SELECT FOR UPDATE | `SELECT ... FOR UPDATE` |
