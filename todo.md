# mongosql 正确性深度检查 — 测试计划

> 目标：用本地 Docker 起 MongoDB，对 SQL→MongoDB 翻译器做端到端正确性验证，
> 重点排查“翻译能跑通但语义与 MySQL 不一致”的隐蔽 bug。
> 进度约定：每完成一条就把 `- [ ]` 改成 `- [x]`。
>
> 代码版本：`d87c99c`（origin/master，已确认最新）
> 计划创建日期：2026-06-06　|　执行日期：次日

---

## 阶段 0：环境准备

- [x] 启动 MongoDB 容器（`mongo:7`，实际 7.0.34）`docker run -d --name mongosql-test -p 27017:27017 mongo:7`
- [x] 确认连接：ping ok（db.version=7.0.34）
- [x] 评估副本集：单机足够覆盖本轮用例，先用单机
- [x] 环境变量 `MONGO_URI` 用默认值 `mongodb://localhost:27017`

## 阶段 1：构建与静态检查

- [x] `go build ./...` 通过（go 1.26.3 工具链可用）
- [x] `go vet ./...` 无告警
- [x] 依赖拉取完成、测试二进制可编译

## 阶段 2：跑现有测试套件

- [x] 纯翻译单元测试：`translator_unit_test.go` / `medium_features_unit_test.go` / `new_features_unit_test.go` 全 PASS
- [x] 集成测试（连 Mongo）：`select_basic` / `select_order_limit` / `aggregate` / `dml` / `coverage` 全 PASS（`ok 5.0s`）
- [x] 无 skip：MONGO_URI 已连上 7.0.34，确认未被静默跳过
- 结论：**仓库自带测试 100% 通过**（happy-path 覆盖良好，但未覆盖下列边界）

## 阶段 3：翻译层 BSON 断言（由现有单元测试 + 端到端 probe 共同覆盖）

- [x] WHERE 各运算符 / `$expr`（列vs列、表达式比较）：现有单元测试覆盖且通过
- [x] SELECT 投影 / 别名 / `_id` 隐藏 / 嵌套字段：现有单元测试覆盖且通过
- [x] 表达式投影 / 标量函数 / CASE → `$project`：现有单元测试覆盖且通过
- [x] 聚合 + GROUP BY + HAVING / JOIN（等值+非等值）/ 写操作结构：现有单元测试覆盖且通过
- [x] 不支持语法返回错误而非 panic：见阶段 4.11（已实测）

## 阶段 4：端到端语义正确性（需 Mongo）— 实测结果

> 实测文件：`tests/probe_correctness_test.go`。运行：`go test ./tests/ -run TestProbe -v`。
> 结论标记：**BUG=确认错误**，**偏差=与 MySQL 语义不一致（可争议为设计取舍）**。

### 4.1 NULL 与三值逻辑　❌ 偏差确认
- [x] `WHERE NOT (age=25)`：实测含 `gary`(age=NULL)，MySQL 应排除 → **偏差**（`$nor`）
- [x] `WHERE age != 25`：实测同样含 `gary` → **偏差**（`$ne` 匹配 null/缺失）
- [x] `IS NULL/IS NOT NULL`：现有 coverage 测试通过（显式 null 情形 OK）

### 4.2 COUNT 语义　❌ BUG 确认
- [x] `COUNT(v)` 数据含 `v=0`：实测得 **1**，应为 **2** → **BUG**（`$cond` 把 0 当 falsy）
- [x] `COUNT(*)`=4 正确（对照组）

### 4.3 HAVING 引用方式　❌ BUG 确认
- [x] `HAVING COUNT(*) > 1`：实测报错 `unsupported expression in aggregation context: *sqlparser.CountStar` → **BUG**（只能写 `HAVING <别名>`）
- [x] `HAVING n > 1`（别名）：现有 aggregate 测试通过（对照确认）

### 4.4 ROUND / 数值函数取整　❌ BUG + 偏差确认
- [x] `ROUND(2.5,0)`=2、`ROUND(0.5,0)`=0：MySQL 应为 3/1 → **偏差**（`$round` 银行家舍入）
- [x] `FLOOR(-2.5)`=-2（应 -3）、`CEIL(-2.5)`=-1（应 -2）：静态路径 → **BUG**
- [x] `POW(2,0.5)`=1（应 √2）、`POW(2,-1)`=1（应 0.5）、`MOD(5.5,2)`=1（应 1.5）：静态路径 → **BUG**
- [x] 对照：列引用/聚合路径 `$round`/`$pow`/`$mod` 由 Mongo 计算，数值正确（仅 ROUND 舍入策略不同）

### 4.5 LIKE 大小写　❌ 偏差确认
- [x] `name LIKE 'A%'`：实测返回 `[]`，MySQL 默认 ci 应匹配 `alice` → **偏差**（`$regex` 缺 `i`）

### 4.6 投影命名冲突 / SELECT *　❌ BUG 确认
- [x] `SELECT a.x, b.x`（无别名）JOIN：实测只剩 `{x:BX}`，`a.x` 被覆盖丢失 → **BUG**
- [x] `SELECT a.x AS ax, b.x AS bx`（带别名）：正确返回两列（**有别名可规避**）

### 4.7 LIMIT 边界　❌ BUG 确认
- [x] `LIMIT 0`：实测返回 **7 行**，应为 0 → **BUG**（`Limit>0` 判定把 0 当无限制）
- [x] `LIMIT n OFFSET m`、`ORDER BY`：现有 select_order_limit 测试通过

### 4.8 聚合细节　❌ BUG + 偏差确认
- [x] 空结果集聚合 `WHERE city='NOPE'`：实测返回 **0 行**，MySQL 应返回 1 行(NULL/0) → **偏差**
- [x] `COUNT(DISTINCT city)`：实测得 **7**，应为 **4** → **BUG**（DISTINCT 修饰被忽略，items.go 未读 `Count.Distinct`）
- [x] `GROUP BY` 多列、SUM/MIN/MAX：现有 aggregate 测试通过

### 4.9 JOIN 正确性
- [x] 等值 INNER JOIN / LEFT JOIN / 非等值 JOIN：现有 aggregate + 单元测试通过
- [x] **RIGHT JOIN**：实测被**静默当作 INNER JOIN**（返回 1 行，应为 2，未报错）→ **BUG（静默错误结果）**，source.go:50 只判 LeftJoinType

### 4.10 写操作端到端
- [x] INSERT 单/多行/常量算术、`INSERT...SELECT`、UPDATE（普通+pipeline）、DELETE：现有 dml 测试通过

### 4.11 健壮性 / 不支持语法　✅ 基本良好
- [x] 子查询(FROM/WHERE IN/EXISTS)、UNION、窗口函数：均返回**清晰错误，无 panic** ✅
- [x] **RIGHT JOIN 例外**：未报错而是静默错误（见 4.9）⚠️
- [x] 空结果集 / 空集合查询不报错 ✅

## 阶段 5：汇总与产出

- [x] 整理三类清单（见下表）
- [x] 每个 bug 标注文件与行号
- [x] 修复优先级建议（见下）
- [x] 清理 Docker：完成测试后执行 `docker rm -f mongosql-test`

---

## ⭐ 最终结论：确认问题清单（全部已实测复现 → 已全部修复 ✅）

> 仓库**自带测试全绿**，但仅覆盖 happy path。以下 12 项为新增 probe 实测暴露的问题，
> **均已修复并通过实测**（修复后 probe 全 PASS、原有测试 0 回归，`go test ./tests/` 全绿）。

| # | 类别 | 现象（最小复现） | 修复前 / 应为 | 涉及文件 | 状态 |
|---|------|------------------|------------|------|--------|
| 1 | BUG | `COUNT(col)` 漏计 `col=0` 的行 | 1 / 2 | `render_aggregate.go` accumulatorFor | ✅ 已修复 |
| 2 | BUG | `HAVING COUNT(*)>1` 直接写聚合 → 报错 | error / BJ,SH | `render_aggregate.go` buildHaving | ✅ 已修复 |
| 3 | BUG | `COUNT(DISTINCT col)` 忽略 DISTINCT | 7 / 4 | `items.go` + `plan.go` + `render_aggregate.go` | ✅ 已修复 |
| 4 | BUG | `RIGHT JOIN` 静默当 INNER | 1 行 / 2 行 | `source.go`（交换左右表→LEFT JOIN） | ✅ 已修复 |
| 5 | BUG | `LIMIT 0` 被当作无限制 | 7 / 0 | `limits.go`+`plan`+`stmt`+`render_*`+`driver.go`（HasLimit/Empty） | ✅ 已修复 |
| 6 | BUG | JOIN 投影 `a.x,b.x`（无别名）叶子名冲突覆盖 | 丢 a.x / 两列 | `render_aggregate.go`（冲突时改 dotless 限定名） | ✅ 已修复 |
| 7 | BUG | 静态 `FLOOR/CEIL` 负数截断错误 | -2,-1 / -3,-2 | `expr.go` evalFunc（`math.Floor/Ceil`） | ✅ 已修复 |
| 8 | BUG | 静态 `POW` 整数幂循环 / `MOD` 浮点截断 | 1,1,1 / √2,0.5,1.5 | `expr.go` evalFunc（`math.Pow/Mod`） | ✅ 已修复 |
| 9 | 偏差 | `NOT(col=v)`/`col!=v` 对 NULL 行的三值逻辑 | 含 gary / 排除 | `expr.go`（`!=` 加非空约束 + NOT 下推取反） | ✅ 已修复 |
| 10 | 偏差 | `LIKE` 默认区分大小写（MySQL 默认 ci） | [] / [alice] | `expr.go`+`agg_expr.go`（`bson.Regex Options:"i"`） | ✅ 已修复 |
| 11 | 偏差 | `ROUND(.5)` 银行家舍入 vs MySQL 远离 0 | 2,0 / 3,1 | `agg_expr.go`（sign·floor(\|x\|·10^d+0.5)/10^d） | ✅ 已修复 |
| 12 | 偏差 | 空集合聚合返回 0 行（MySQL 返回 1 行 NULL） | 0 行 / 1 行 | `render_aggregate.go`（grouping-less 用 `$facet` 保 1 行） | ✅ 已修复 |

## 阶段 6：Bug 修复（每修一项划掉一行，全部完成）

- [x] #1 COUNT(col)=0 漏计 — `accumulatorFor` 改判 null/missing 而非值真假
- [x] #2 HAVING 直接写聚合函数 — 新增 `buildHaving`，把聚合映射到 `$group` 输出字段再 `$match`
- [x] #3 COUNT/SUM/AVG(DISTINCT) — `AggSpec.Distinct` 贯通；`$addToSet`+`$filter(非null)`+`$size/$sum/$avg`
- [x] #4 RIGHT JOIN — `source.go` 两表 RIGHT JOIN 交换为 LEFT JOIN（多表 RIGHT JOIN 明确报错）
- [x] #5 LIMIT 0 — 引入 `HasLimit`/`Empty`，LIMIT 0 短路返回 0 行（兼顾 find 与 aggregate）
- [x] #6 JOIN 同名列覆盖 — 投影名冲突时降级为 dotless 限定名（`b_x`），两列都保留
- [x] #7 静态 FLOOR/CEIL 负数 — 改用 `math.Floor` / `math.Ceil`
- [x] #8 静态 POW/MOD — 改用 `math.Pow` / 浮点 `math.Mod`（整数操作数仍返回整数）
- [x] #9 NULL 三值逻辑 — `!=` 加 `$ne:null` 约束；`NOT(...)` 改为下推取反（AND/OR/比较/IS/BETWEEN）
- [x] #10 LIKE 大小写 — WHERE 与聚合两条路径都加 `i` 选项（`bson.Regex` / `$regexMatch options`）
- [x] #11 ROUND 舍入 — `$round` 替换为 MySQL 远离 0 舍入公式
- [x] #12 空集合聚合 — grouping-less 聚合用 `$facet` 兜底，空输入也返回 1 行（COUNT→0，其余→NULL）

### 验证
- [x] `go build ./...` / `go vet ./...` 通过
- [x] 13 个 probe 全部 PASS（修复前全 FAIL）
- [x] 仓库原有测试 0 回归（`go test ./tests/` 全绿，含 Coverage/Aggregate/DML/Join 等）
- [x] 改动文件：`plan/plan.go`、`stmt/stmt.go`、`internal/sel/{items,limits,plan,source,render_find,render_aggregate}.go`、`internal/expr/{expr,agg_expr}.go`、`driver/driver.go`；测试 `tests/probe_correctness_test.go`

### 残留的设计性差异（已知、非 bug）
- `NOT(子查询/复杂非可取反表达式)` 仍回退 `$nor`（可能含 NULL 行）——常见比较/逻辑/IS/BETWEEN 已正确。
- `NOT IN (...)` 含 NULL 列表项时的 SQL 严格语义未特殊处理（与多数实现一致，留作已知差异）。
- `SELECT *` + JOIN 仍把右表放入子文档（结构性差异，非数据丢失）。
