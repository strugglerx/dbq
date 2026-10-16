# Agent-DB-CLI 架构设计方案 (Multi-Database Universal Edition)

> 面向 AI Agent（Claude, Codex, Antigravity, AutoGPT 等）深度优化的通用数据库命令行交互工具。
> 统一支持 **达梦（DM8）**、**MySQL**、**PostgreSQL**、**SQLite** 等主流关系型数据库，纯 Go 零 CGO 绿色分发。

---

## 1. 痛点深度剖析与演进

### 1.1 从单一 `backend/hack/dmq` 到通用多数据库 CLI
原有的 `backend/hack/dmq/main.go` 针对特定工程解决达梦直查，但存在三个重大瓶颈：
1. **数据库绑定单一**：仅支持达梦，无法在同一个工具体系内无缝切换 MySQL、PostgreSQL 或本地 SQLite；
2. **项目强耦合**：绑定 `base-v2/internal/pkg/dmisolated`、GoFrame 配置和特定机器绝对路径；
3. **缺少 Agent 原生能力**：Agent 无法通过统一的命令（如 `tables`, `desc`, `sample`, `schema-prompt`）探索不同数据库的表结构与注释。不同数据库的数据字典差异极大（MySQL 是 `information_schema`，PG 是 `pg_catalog`/`information_schema`，DM 是 `ALL_TABLES`/`USER_TABLES`，SQLite 是 `sqlite_master`/`PRAGMA`）。让 Agent 自己手写字典 SQL 会浪费海量上下文与推理时间。

### 1.2 Agent-DB-CLI 的定位与目标
- **统一抽象（One CLI for All DBs）**：无论底层是达梦、MySQL、PG 还是 SQLite，Agent 只需要使用相同的语义命令：
  - `agent-db tables` -> 自动适配底层数据字典，输出表名、表类型、注释与行数。
  - `agent-db desc <table>` -> 自动统一输出字段名、类型、主键、非空、默认值、列注释与索引。
  - `agent-db sample <table>` -> 自动适配方言（`ROWNUM` vs `LIMIT`），零 SQL 智能抽样。
  - `agent-db schema-prompt` -> 提取核心表结构，生成极度紧凑的 Markdown 上下文，专为注入 LLM Prompt 打造。
- **纯 Go 零 CGO（Zero-Dependency Static Binary）**：
  - 达梦：`gitee.com/chunanyong/dm`
  - MySQL：`github.com/go-sql-driver/mysql`
  - PostgreSQL：`github.com/lib/pq`
  - SQLite：`modernc.org/sqlite`（纯 Go 实现，无须系统安装 gcc 或 sqlite3 库）
  单个二进制跨平台（macOS arm64/amd64, Linux, Windows）随处运行。

---

## 2. 核心架构设计：Dialect（方言引擎）模式

```
                 +---------------------------+
                 |    CLI (Cobra & Flags)    |
                 +-------------+-------------+
                               |
                 +-------------v-------------+
                 |    Config & DSN Resolver  |
                 | (Auto-detect DB & Config) |
                 +-------------+-------------+
                               |
               +---------------v---------------+
               |       Dialect Registry        |
               +---------------+---------------+
                               |
         +-------------+-------+-------+-------------+
         |             |               |             |
   +-----v-----+ +-----v-----+   +-----v-----+ +-----v-----+
   | DM8       | | MySQL     |   | Postgres  | | SQLite    |
   | Dialect   | | Dialect   |   | Dialect   | | Dialect   |
   +-----+-----+ +-----+-----+   +-----+-----+ +-----+-----+
         |             |               |             |
         +-------------+-------+-------+-------------+
                               |
                +--------------v--------------+
                |    database/sql ConnPool    |
                +--------------+--------------+
                               |
      +------------------------+------------------------+
      |                        |                        |
+-----v------+           +-----v------+           +-----v------+
| Inspector  |           |  Executor  |           | Formatter  |
| Tables     |           | ReadOnly   |           | Table/MD   |
| Desc/Info  |           | DangerGuard|           | JSON       |
| DDL/Sample |           | Timeout    |           | CompactJSON|
| PromptGen  |           | Truncation |           | TSV/JSONL  |
+------------+           +------------+           +------------+
```

### 2.1 数据库类型自动推断规则 (Auto-Detection)
CLI 会根据输入自动识别数据库类型，无需人工频繁指定 `--type`：
1. **DSN Scheme 识别**：
   - `dm://...` -> DM8
   - `mysql://...` 或包含 `@tcp(` 且无 `dm:` -> MySQL
   - `postgres://...` 或 `postgresql://...` -> PostgreSQL
   - `sqlite://...`、以 `.db`/`.sqlite`/`.sqlite3` 结尾的文件路径，或 `:memory:` -> SQLite
2. **GoFrame 项目 link 语法识别**：
   - `dm:user:pass@tcp(...)/...` -> DM8
   - `mysql:user:pass@tcp(...)/...` -> MySQL
   - `pgsql:user:pass@tcp(...)/...` -> PostgreSQL
   - `sqlite:...` -> SQLite
3. **默认端口识别**：
   - 5236 -> DM8
   - 3306 -> MySQL
   - 5432 -> PostgreSQL
4. **显式指定**：
   - `--type dm|mysql|postgres|sqlite` 或环境变量 `DB_TYPE`

---

## 3. 功能特性与命令体系

### 3.1 统一命令列表
| 命令 | 别名 | 功能描述 | Agent 核心用途 |
| :--- | :--- | :--- | :--- |
| `tables` | `ls` | 列出当前库/Schema 的所有表与视图（附带注释与行数） | 初始探索“库里有什么” |
| `desc` | `info` | 查看表结构（字段、类型、主键、是否可空、列注释、索引） | 编写 SQL 或代码前理解字段定义 |
| `sample` | `head` | 智能采样表前 N 条记录（默认 5 条） | 查看真实数据样例、时间格式、枚举值等 |
| `ddl` | `show-create` | 导出指定表的完整建表语句 | 迁移或完整结构分析 |
| `schema-prompt` | `prompt` | 提取一个或多个表的精简 Schema，生成最省 Token 的 Markdown 上下文 | **直接塞入 LLM System Prompt** |
| `query` | `q` | 执行任意 SQL 查询或写操作（支持 `-w` 与格式化输出） | 执行查询或变更数据 |
| `explain` | - | 执行计划分析 | 诊断慢查询 |
| `status` | `ping` | 连通性测试，输出数据库版本、当前库/Schema、编码与特性 | 确保连接可用 |

### 3.2 Token 经济学：Agent 专属优化
1. **Compact JSON 模式（`--format compact-json`）**：
   - 标准 JSON 会在每行重复列名（如 100 行 * 10 个字段 = 1000 次字段名重复）。
   - Compact 模式输出格式：
     ```json
     {
       "columns": ["id", "username", "status"],
       "rows": [
         [1, "admin", 1],
         [2, "operator", 1]
       ]
     }
     ```
     相比标准 JSON **节省 60% 以上的 Token**！
2. **Markdown 表格模式（`--format table` / `--format md`）**：
   - 采用标准 Markdown 对齐语法，直接作为 LLM 上下文阅读体验极佳。
3. **单元格超长熔断（`--max-cell 2000`）**：
   - 避免长文本、JSON 或大二进制字段瞬间撑爆上下文。
4. **行数硬限制（默认 `--limit 100`）**：
   - 防止 Agent 执行 `SELECT * FROM log_table` 导致卡死或爆 Token。

### 3.3 安全守护体系（Agent Guardrails）
- **默认严格只读**：拦截任何 `INSERT`, `UPDATE`, `DELETE`, `DROP`, `ALTER`, `TRUNCATE`, `RENAME` 语句。
- **写操作显式授权**：必须带 `-w` 或 `--write` 才能执行写操作。
- **高危全表变更二次保护**：
  - 拦截无 `WHERE` 条件的 `UPDATE` 或 `DELETE`；
  - 拦截 `DROP TABLE` 与 `TRUNCATE TABLE`；
  - 必须同时传入 `--dangerously-allow-bulk-mutation`，防止 Agent 自主执行引发重大事故。
- **超时熔断**：默认 30s 超时强制中断。

---

## 4. 多环境多项目配置管理

支持 5 层级配置探测：
1. **CLI 参数**（最高优先级）：`--dsn`, `--host`, `--port`, `-u`, `-p`, `-d`/`-s`, `--type`
2. **环境变量**：`DB_DSN`, `DB_TYPE`, `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`（以及 DM 专属变量兼容）
3. **本地目录配置**：`.agent-db.yaml`, `.dmcli.yaml`
4. **项目工程配置自动适配**：
   - 向上递归探测 GoFrame `manifest/config/config.yaml`（自动解析 `database.default.link`，支持 dm/mysql/pgsql）
5. **全局用户配置**：
   - `~/.config/agent-db/config.yaml`（支持 `--profile prod/dev/test/local`）
