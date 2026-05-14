# 项目结构说明

本文不再描述重构方案，而是说明当前仓库的目录结构、各层职责，以及新增功能时应修改的位置。

## 一、项目结构树

```text
mongodb-sql-driver/
├── go.mod
├── go.sum
├── doc/
│   ├── design.md
│   └── sql-subset.md
├── driver/
│   └── driver.go
├── tests/
│   ├── aggregate_test.go
│   ├── dml_test.go
│   ├── fixtures_test.go
│   ├── helpers_test.go
│   ├── translator_unit_test.go
│   ├── select_basic_test.go
│   └── select_order_limit_test.go
└── translator/
    ├── translator.go
    ├── plan/
    │   └── plan.go
    ├── stmt/
    │   └── stmt.go
    └── internal/
        ├── expr/
        │   └── expr.go
        ├── sel/
        │   ├── items.go
        │   ├── limits.go
        │   ├── plan.go
        │   ├── render_aggregate.go
        │   ├── render_find.go
        │   └── source.go
        └── write/
            └── write.go
```

## 二、顶层目录职责

### `driver/`

这一层负责把“翻译结果”接到 MongoDB 官方 Go Driver 上。

- `driver.go`
  - 持有 `mongo.Client` 和默认数据库句柄。
  - 调用 `translator.Translator` 把 SQL 解析成可执行语句。
  - 根据语句类型分派到 `Find`、`Aggregate`、`Insert`、`Update`、`Delete` 的执行路径。

可以把这一层理解为“执行器”，它不负责理解 SQL 语义，只负责执行已经翻译好的结果。

### `translator/`

这一层负责把 SQL 转成项目内部可执行的语句对象，是整个仓库的核心。

当前已经拆成多个 package，分成三类：

- 对外入口：`translator`
- 对外数据模型：`translator/stmt`、`translator/plan`
- 内部实现：`translator/internal/*`

### `tests/`

这一层统一放所有测试，包括纯翻译单元测试和连接 MongoDB 的集成测试。

- `select_basic_test.go`
  - 验证基础 SELECT 行为。
- `select_order_limit_test.go`
  - 验证排序、分页、限制条数等行为。
- `aggregate_test.go`
  - 验证聚合、分组、JOIN 等需要 aggregation pipeline 的查询。
- `dml_test.go`
  - 验证 INSERT、UPDATE、DELETE。
- `fixtures_test.go`
  - 负责准备和清理测试数据。
- `helpers_test.go`
  - 放测试共用的辅助函数。
- `translator_unit_test.go`
  - 验证 translator 的纯翻译输出，不依赖 MongoDB。

这一层的目标是把测试入口统一收口到一个目录里，再按测试内容区分“纯翻译校验”和“真实执行链路校验”。

### `doc/`

- `design.md`
  - 当前文档，用来解释项目结构树和职责边界。
- `sql-subset.md`
  - 列出当前支持的 SQL 语法范围和每个语法元素对应的 MongoDB 执行方式。

## 三、`translator` 内部结构

### 1. `translator/translator.go`

这是对外入口 package 的唯一主文件，职责刻意保持很薄：

- 创建 Vitess parser
- 解析原始 SQL
- 根据语句类型分派到 SELECT 或写操作翻译器

这里不再放 SELECT 规划细节、WHERE 渲染逻辑、聚合构造逻辑。它只负责“接收请求并把请求路由到正确子模块”。

### 2. `translator/stmt/`

这个 package 定义“可执行语句”模型，也就是 translator 对 driver 输出的最终结果。

- `stmt.go`
  - `Statement`
  - `FindStmt`
  - `AggregateStmt`
  - `InsertStmt`
  - `UpdateStmt`
  - `DeleteStmt`

这一层表达的是“最终怎么执行”，因此最接近 driver。

### 3. `translator/plan/`

这个 package 定义 SELECT 的语义规划模型，位于 AST 与执行语句之间。

- `plan.go`
  - `SourceRef`
  - `FieldRef`
  - `SelectItem`
  - `AggSpec`
  - `JoinPlan`
  - `SelectPlan`

这一层表达的是“这条 SQL 的语义是什么”，而不是“最后生成什么 BSON”。

例如：

- 主表是谁
- JOIN 的右表是谁
- SELECT 列是字段还是聚合
- WHERE / SORT / LIMIT 的归一化结果是什么
- 这条 SELECT 最后应该走 `find` 还是 `aggregate`

把这些信息独立出来后，渲染层就不需要反复解释 AST。

## 四、`translator/internal` 的职责划分

`internal` 目录表示这些 package 只给 translator 自己使用，不承诺对外稳定。

### 1. `translator/internal/expr/`

这是表达式工具层，属于叶子模块。

- `expr.go`
  - 字面量转 Go 值
  - SQL 值表达式转 Go 值
  - 列名转 Mongo 字段路径
  - WHERE / HAVING 转 Mongo filter
  - LIKE 转正则

它的定位是“表达式和 BSON 的基础转换”，尽量不感知更高层的 SELECT 规划细节。

### 2. `translator/internal/sel/`

这是 SELECT 翻译器，是当前最复杂的一层。

#### `plan.go`

SELECT 的主入口。

- `Translate`
  - 一次性完成“规划 + 渲染”。
- `Plan`
  - 把 `*sqlparser.Select` 转成 `*plan.SelectPlan`。
- `Render`
  - 根据 `SelectPlan` 决定走 `FindStmt` 还是 `AggregateStmt`。

#### `items.go`

负责解释 SELECT 列表。

- 判断 `SELECT *`
- 判断字段列还是聚合列
- 把聚合函数整理成 `AggSpec`

#### `source.go`

负责解释 FROM 和 JOIN。

- 解析主表和别名
- 解析 `db.collection`
- 解析 JOIN 右表
- 解析 ON 两侧字段
- 导出 `SourceRefFromAliasedTable` 给写操作翻译器复用

这一层的核心目标是把“表名、库名、别名、字段归属”从 AST 中提炼出来。

#### `limits.go`

放排序、限制、偏移等小型辅助逻辑。

- `ORDER BY` -> `bson.D`
- `LIMIT/OFFSET` -> `int64`

这部分逻辑不复杂，但单独拆开后可以避免堆在主流程文件中。

#### `render_find.go`

负责把 `SelectPlan` 渲染成 `stmt.FindStmt`。

主要处理：

- projection 生成
- `SELECT DISTINCT` 的 find 路径
- `_id` 是否保留

#### `render_aggregate.go`

负责把 `SelectPlan` 渲染成 `stmt.AggregateStmt`。

主要处理：

- JOIN -> `$lookup` / `$unwind`
- WHERE -> `$match`
- GROUP BY / 聚合函数 -> `$group`
- HAVING -> group 后的 `$match`
- ORDER BY / LIMIT / OFFSET
- 最终 `$project`

这层专门处理 MongoDB aggregation pipeline 相关细节，因此单独拆开是合理的。

### 3. `translator/internal/write/`

这个 package 负责非 SELECT 语句。

- `write.go`
  - `Insert`
  - `Update`
  - `Delete`

它和 `sel` 的区别是：

- `sel` 需要先做语义规划，再选择 find 或 aggregate
- `write` 直接把 AST 翻译成写操作语句即可

但它仍然复用了 `sel.SourceRefFromAliasedTable`，保证表名、库名、别名的解释规则一致。

## 五、当前调用链

项目当前的主链路可以概括为：

```text
SQL 字符串
  -> translator.Translator.Parse
  -> 按语句类型分派
     -> SELECT: internal/sel.Plan -> internal/sel.Render
     -> INSERT/UPDATE/DELETE: internal/write
  -> 生成 stmt.Statement
  -> driver.Driver.Exec
  -> MongoDB Driver
```

如果按职责看，可以再压缩成三层：

```text
解析层 -> 语义层 -> 执行层
```

- 解析层：Vitess AST
- 语义层：`plan.SelectPlan`
- 执行层：`stmt.*Stmt` + `driver`

## 六、测试结构说明

项目现在仍然有两类测试，但它们都集中在 `tests/` 目录下。

### 1. Translator 单元测试

位置：`tests/translator_unit_test.go`

特点：

- 不依赖 MongoDB 实例或只依赖最少的外部环境
- 主要验证翻译结果的结构是否正确
- 适合在重构 translator 内部时快速回归

### 2. Driver / MongoDB 集成测试

位置：`tests/`

特点：

- 连接真实 MongoDB
- 验证 SQL 从输入到执行结果的完整链路
- 适合确认行为兼容性

## 七、以后改功能时应该改哪里

为了减少搜索成本，可以按需求类型找修改入口。

### 如果要加新的 SELECT 语义

优先看：

- `translator/internal/sel/items.go`
- `translator/internal/sel/source.go`
- `translator/plan/plan.go`

典型场景：

- 新的 SELECT 列类型
- 新的聚合函数
- 新的字段归属规则
- 新的 JOIN 规划信息

### 如果要改 WHERE / 比较运算支持

优先看：

- `translator/internal/expr/expr.go`

典型场景：

- 新的比较运算符
- 新的字面量解析
- LIKE / REGEXP 行为调整

### 如果要改 find 路径输出

优先看：

- `translator/internal/sel/render_find.go`

典型场景：

- projection 规则
- DISTINCT 的输出形式
- `_id` 默认隐藏策略

### 如果要改 aggregate 路径输出

优先看：

- `translator/internal/sel/render_aggregate.go`

典型场景：

- `$lookup` / `$unwind` 结构
- `$group` 生成规则
- HAVING / ORDER BY / 最终 `$project`

### 如果要改写操作

优先看：

- `translator/internal/write/write.go`

典型场景：

- INSERT VALUES 扩展
- UPDATE SET 扩展
- DELETE 限制条件调整

### 如果要改执行方式

优先看：

- `driver/driver.go`
- `translator/stmt/stmt.go`

典型场景：

- 支持新的执行选项
- `db.collection` 的执行目标切换
- 返回结果结构调整

## 八、总结

当前项目结构已经明确分成了几层：

- `translator` 负责对外入口和分派
- `plan` 负责表达 SELECT 的语义模型
- `stmt` 负责表达最终执行语句
- `internal/expr` 负责表达式与 BSON 基础转换
- `internal/sel` 负责 SELECT 的规划与渲染
- `internal/write` 负责写操作翻译
- `driver` 负责真正执行 MongoDB 调用
- `tests` 负责端到端行为验证

这个结构的核心价值不是“文件更多”，而是把“解析、语义、渲染、执行”四类职责分开。以后继续扩展 translator 时，应优先保持这种边界，而不是把新逻辑重新塞回入口文件。
