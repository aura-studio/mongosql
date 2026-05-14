# mysql-simulator

将 SQL → MongoDB 翻译层包装成一个 **MySQL 线协议服务器**，从而可以使用任何
MySQL 客户端（命令行 `mysql`、MySQL Workbench、DBeaver、TablePlus、Navicat
等）直接连上来交互式调试。

## 工作原理

```
┌──────────────────┐     MySQL wire     ┌──────────────────┐    BSON / mongo-driver    ┌──────────┐
│  MySQL UI / CLI  │ ─────────────────▶ │  mysql-simulator │ ────────────────────────▶ │ MongoDB  │
│  (Workbench…)    │                    │  (本目录)        │                          │ :27017   │
└──────────────────┘                    └──────────────────┘                          └──────────┘
                                                │
                                                ▼
                                       translator + driver
                                       (本仓库其余部分)
```

- 客户端发来的常规 `SELECT / INSERT / UPDATE / DELETE` 经由 `translator`
  翻译为 MongoDB 查询，再用官方 `mongo-driver` 执行。
- 客户端在握手 / 刷新 schema 时发出的 `SHOW DATABASES`、`SHOW TABLES`、
  `SELECT @@version` 等元数据查询，由本目录的 [`meta.go`](meta.go) 提供桩响
  应，保证 GUI 能正常打开。

## 启动

```bash
# 从仓库根目录
go run ./mysql-simulator \
    --listen   127.0.0.1:3307 \
    --mongo-uri mongodb://localhost:27017 \
    --db        sqlmongo \
    --user      root \
    --password  ""
```

启动成功后会打印：

```
mysql-simulator listening on 127.0.0.1:3307 (mongo=mongodb://localhost:27017 db=sqlmongo user=root)
connect with: mysql -h 127.0.0.1 -P 3307 -u root  sqlmongo
```

## 用 mysql CLI 连接

```bash
mysql -h 127.0.0.1 -P 3307 -u root sqlmongo
```

```sql
SHOW TABLES;
SELECT * FROM users LIMIT 10;
SELECT name, COUNT(*) FROM orders GROUP BY name;
INSERT INTO users (name, age) VALUES ('alice', 30);
```

## 用 GUI 连接（MySQL Workbench / DBeaver / TablePlus / Navicat）

新建一个 MySQL 连接，参数如下：

| 字段 | 值 |
|---|---|
| Host | 127.0.0.1 |
| Port | 3307 |
| User | root |
| Password | （空，或启动时指定的） |
| Database | sqlmongo |

> 部分客户端（如 MySQL Workbench）默认会发送 `SET NAMES`、`SHOW WARNINGS`、
> 一些 `information_schema` 相关查询。本模拟器对常见的元数据语句做了桩响
> 应；遇到完全不识别的语句会返回解析错误，可以在客户端的 SQL 编辑器中直
> 接执行可翻译的查询。

## 限制

- 不支持事务（`BEGIN/COMMIT` 仅作 no-op 应答）。
- 不支持预处理参数绑定（参数会被忽略，按字面 SQL 执行）。
- 列顺序由 BSON map 决定，固定 `_id` 在最前，其余按字母序。
- 仅支持 [`doc/sql-subset.md`](../doc/sql-subset.md) 列出的 SQL 子集；
  不支持 DDL（`CREATE TABLE`、`ALTER TABLE` 等）。
