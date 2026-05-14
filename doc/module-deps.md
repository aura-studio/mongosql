# 模块依赖树

## 依赖关系图

```
driver
├── translator
│   ├── internal/sel          ← SELECT 语句翻译
│   │   ├── internal/expr     ← SQL 表达式 → BSON 转换
│   │   ├── plan              ← 语义模型（SelectPlan 等）
│   │   └── stmt              ← 输出类型（FindStmt / AggregateStmt）
│   ├── internal/write        ← INSERT / UPDATE / DELETE 翻译
│   │   ├── internal/expr
│   │   ├── internal/sel      ← 复用 WHERE 子句翻译
│   │   └── stmt              ← 输出类型（InsertStmt / UpdateStmt / DeleteStmt）
│   └── stmt
└── stmt
```

> 所有内部路径前缀为 `translator/`，上图中省略以保持简洁。

## 分层结构

```
┌─────────────────────────────────────────────┐
│  driver                                     │  ← 执行层：连接 MongoDB，执行 Statement
├─────────────────────────────────────────────┤
│  translator                                 │  ← 入口层：解析 SQL，分派到 sel / write
├──────────────────────┬──────────────────────┤
│  internal/sel        │  internal/write      │  ← 翻译层：SELECT / DML 各自的翻译逻辑
├──────────────────────┴──────────────────────┤
│  internal/expr                              │  ← 基础层：值转换、WHERE → BSON filter
├─────────────────────────────────────────────┤
│  plan              │  stmt                  │  ← 模型层：语义计划 / 输出语句（无内部依赖）
└─────────────────────────────────────────────┘
```

## 外部依赖

```
vitess.io/vitess                ← SQL 解析器（AST）
go.mongodb.org/mongo-driver/v2  ← MongoDB 官方 Go 驱动（bson / mongo）
```

## 包职责一览

| 包 | 职责 | 依赖 |
|---|---|---|
| `driver` | 连接 MongoDB，执行各类 Statement | `translator`, `stmt` |
| `translator` | 解析 SQL 文本，分派到对应翻译器 | `sel`, `write`, `stmt` |
| `internal/sel` | 将 SELECT AST 翻译为 FindStmt / AggregateStmt | `expr`, `plan`, `stmt` |
| `internal/write` | 将 INSERT/UPDATE/DELETE AST 翻译为对应 Stmt | `expr`, `sel`, `stmt` |
| `internal/expr` | SQL 字面值 → BSON 值；WHERE 子句 → bson.D filter | *(无内部依赖)* |
| `plan` | 语义模型：SelectPlan, SourceRef, FieldRef, AggSpec 等 | *(无内部依赖)* |
| `stmt` | 输出接口与结构体：Statement, FindStmt, AggregateStmt 等 | *(无内部依赖)* |
