# dbq (Database Query)

<p align="center">
  <strong>专为 AI Agent（Claude, Codex, Cursor, Antigravity 等）与终端开发者设计的通用数据库直查工具</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/CGO-Zero%20CGO-brightgreen?style=flat" alt="Zero CGO" />
  <img src="https://img.shields.io/badge/Databases-DM8%20|%20MySQL%20|%20PG%20|%20SQLite-blue?style=flat" alt="Databases" />
  <img src="https://img.shields.io/badge/License-MIT-orange?style=flat" alt="License" />
</p>

---

## 💡 为什么需要 dbq？

传统的数据库客户端（如 `mysql`, `psql`, `disql`, DBeaver）主要是为人类视觉交互设计的：
1. **多库割裂**：不同数据库命令不同、参数不同、方言字典差异极大。
2. **环境依赖沉重**：部分国产数据库（如达梦 DM8）需安装特定的动态链接库（CGO）或 Java 运行环境，在容器或 CI/Agent 沙箱中极难一键跑通。
3. **缺少 Agent 原生能力**：AI Agent 接到写 SQL 或排查任务时，第一步是“探索表结构”。不同数据库字典各异（`ALL_TABLES` vs `information_schema` vs `sqlite_master`），Agent 极易写错字典 SQL。
4. **Token 浪费严重**：常规 JSON 数组在成百上千行数据中反复输出重复列名，迅速撑爆 LLM 上下文。
5. **误操作风险高**：Agent 自主执行脚本时，容易因幻觉生成不带 `WHERE` 的 `UPDATE` 或直接 `DROP TABLE`。

**`dbq`** 应运而生：一个**单文件绿色二进制、纯 Go 零 CGO**、自带多数据库 Schema 探索、Token 紧凑模式与双重安全拦截的通用 CLI。

---

## 🌟 核心特性

- 🔌 **多数据库纯 Go 驱动（Zero CGO）**：
  - 🇨🇳 **达梦 DM8**（`dm://` 或 GoFrame `dm:user:pass@tcp(...)`）
  - 🐬 **MySQL / MariaDB**（`mysql://` 或 `user:pass@tcp(...)`）
  - 🐘 **PostgreSQL**（`postgres://` 或 `pgsql:user:pass@tcp(...)`）
  - 🪶 **SQLite**（`sqlite:file.db` 或本地文件路径）
  单个二进制文件在 macOS (Apple Silicon/Intel)、Linux (amd64/arm64) 和 Windows 均可秒级运行。
- 🤖 **Agent-First Schema 探索**：
  - `tables`：统一列出所有表、视图与中文注释。
  - `desc <table>`：一键查看字段定义、类型、可空、默认值、列注释与索引。
  - `sample <table>`：免拼 SQL 智能抽样前 N 条样例数据（自动适配 `ROWNUM` / `LIMIT`）。
  - `prompt [tables...]`：**一键生成高信息密度的 Markdown Schema**，可直接注入 LLM System Prompt。
- 📉 **深度 Token 优化**：
  - 提供 `-compact` 模式（`{columns: [...], rows: [...]}` 矩阵结构），相比标准 JSON **节省 60%+ 上下文 Token**。
  - 默认输出限流保护（`-n 100` 行上限与 2000 字符长文本截断），杜绝长报文污染。
- 🛡️ **双重安全拦截卫士**：
  - **默认严格只读**：非只读语句（`INSERT/UPDATE/DELETE/DDL`）必须显式加 `-w` 参数。
  - **高危全表与破坏操作拦截**：无 `WHERE` 条件的 `UPDATE/DELETE` 或 `DROP/TRUNCATE`，必须加 `-danger` 参数才允许执行。
- ⚡ **现有工程零配置即用**：
  - 向上递归自动探测 GoFrame 项目配置（`manifest/config/config.yaml` 或 `backend/manifest/...`），在老项目中直接运行即可连库。
  - 支持项目级 `.dbq.yaml`、用户级 `~/.config/dbq/profiles.yaml` 及动态 `command` 获取密码（如结合 Vault / 密钥管理器）。

---

## 🚀 安装与编译

### 1. 源码编译（推荐）
```bash
git clone https://github.com/strugglerx/dbq.git
cd dbq
make build
# 生成的可执行文件为 ./dbq
```

### 2. 全局安装到 PATH
```bash
make install
# 或者
go install .
```

---

## 📖 命令行使用指南

```bash
dbq [flags] "SQL"                  # 执行单条或多条 SQL 查询/变更
dbq [flags] tables [pattern]         # 列出所有表、视图与注释（别名 ls）
dbq [flags] desc <table_name>        # 查看表结构定义与索引（别名 columns, info）
dbq [flags] sample <table_name>      # 快速抽样前 N 条数据（默认 5 条）
dbq [flags] prompt [table_names...]  # 提取表结构生成专用于 LLM 的高密度 Markdown 上下文
dbq [flags] conns                    # 列出所有已配置的命名连接与环境变量连接
dbq [flags] ping                     # 测试连接连通性与数据库状态
dbq [flags] < file.sql               # 从标准输入读取 SQL
```

### 常用 Flags
| Flag | 说明 | 默认值 |
| :--- | :--- | :--- |
| `-c string` | 指定使用的命名连接（在 `.dbq.yaml` 或 `profiles.yaml` 中配置） | 项目 default |
| `-dsn string` | 显式传入数据库连接串（支持 URL 或各库标准 DSN） | - |
| `-driver string` | 显式指定驱动（`dm` / `mysql` / `pgx` / `sqlite`） | 自动识别 |
| `-w` | 允许写操作（`INSERT/UPDATE/DELETE/DDL`） | `false`（默认只读） |
| `-danger` | 强制允许全表无 WHERE 的 `UPDATE/DELETE` 或 `DROP/TRUNCATE` | `false` |
| `-compact` | 输出 Token 紧凑型 JSON（Agent 推荐） | `false` |
| `-json` | 输出标准 JSON 数组格式 | `false` |
| `-tsv` | 制表符分隔输出（管道友好） | `false` |
| `-n int` | 最大返回行数限制（0 为不限） | `100` |
| `-timeout int` | 语句超时秒数 | `30` |

---

## 💡 典型使用场景

### 1. Agent 探索数据库表结构
```bash
# 查库里有哪些表
$ dbq tables
TABLE_NAME  COMMENT
sys_user    系统用户表
sys_role    角色表
sys_dept    部门表
tables=3

# 查看表的字段、注释及索引
$ dbq desc sys_user
Table: sys_user
COLUMN_NAME  TYPE          NULLABLE  DEFAULT  COMMENT
id           BIGINT        NO                 主键ID
username     VARCHAR(64)   NO                 登录账号
status       INT           YES       1        状态: 1启用 0禁用
created_at   DATETIME      YES                创建时间

# 智能抽样 3 条真实数据查看形态
$ dbq sample sys_user -n 3
```

### 2. 生成专供 LLM Prompt 的精炼上下文
执行 `dbq prompt sys_user sys_role` 会直接输出高信息密度的 Markdown，Agent 可直接塞入自己的 Prompt 中：
````markdown
## Database Schema Context

### Table: `sys_user` (系统用户表)
| Column | Type | Nullable | Default | Comment |
| :--- | :--- | :--- | :--- | :--- |
| `id` | BIGINT | NO |  | 主键ID |
| `username` | VARCHAR(64) | NO |  | 登录账号 |
| `status` | INT | YES | 1 | 状态: 1启用 0禁用 |
| `created_at` | DATETIME | YES |  | 创建时间 |

### Table: `sys_role` (角色表)
...
````

### 3. Agent 紧凑查询（节约 60%+ Token）
```bash
$ dbq -compact "SELECT id, username, status FROM sys_user WHERE status = 1"
{"columns":["id","username","status"],"meta":{"cell_cut_cnt":0,"elapsed_sec":0.001,"shown_rows":2,"total_rows":2,"truncated":false},"rows":[["1","admin","1"],["2","operator","1"]]}
```

### 4. 直连不同数据库
```bash
# 达梦 DM8
dbq -dsn "dm://SYSDBA:SYSDBA@127.0.0.1:5236?schema=XIAOSAN" "SELECT * FROM sys_user"

# MySQL
dbq -dsn "mysql://root:123456@127.0.0.1:3306/demo" "SELECT * FROM users"

# PostgreSQL
dbq -dsn "postgres://postgres:123456@127.0.0.1:5432/demo?sslmode=disable" "SELECT * FROM users"

# SQLite
dbq -dsn "test.db" "SELECT * FROM users"
```

### 5. 安全熔断机制验证
```bash
# 未带 -w 执行写操作 -> 自动拦截
$ dbq "UPDATE sys_user SET status = 0 WHERE id = 1"
dbq: 语句 #1 写操作被拦截: 包含修改语句 (UPDATE)，需要显式指定 -w 才能执行

# 带 -w 但忘记写 WHERE 条件 -> 自动拦截全表风险
$ dbq -w "DELETE FROM sys_user"
dbq: 全表修改风险被拦截: 检测到无 WHERE 条件的 DELETE 语句，需显式添加 -danger 参数才允许执行

# 显式确认后方可执行
$ dbq -w -danger "DELETE FROM sys_user"
affected=5 elapsed=0.01s
```

---

## ⚙️ 多环境与连接配置

### 1. 项目级配置（`.dbq.yaml`）
在工程根目录下放置 `.dbq.yaml`：
```yaml
default: dev

connections:
  dev:
    dsn: "dm://SYSDBA:SYSDBA@127.0.0.1:5236?schema=XIAOSAN"
  
  test:
    driver: mysql
    dsn_env: TEST_MYSQL_DSN # 从环境变量获取 DSN
  
  prod:
    driver: pgx
    readonly: true         # 强制生产库为只读连接
    command: "vault read -field=dsn secret/db/prod" # 通过外部命令动态获取
```

通过 `-c` 快速切换环境：
```bash
dbq -c test "SELECT count(*) FROM orders"
```

### 2. 用户全局配置
放在 `~/.config/dbq/profiles.yaml`，跨所有工程共享通用连接。

### 3. 查看当前可用连接
```bash
$ dbq conns
NAME           DRIVER  SOURCE               DSN
dev (default)  dm      project:.dbq.yaml    dm://SYSDBA:***@127.0.0.1:5236?schema=XIAOSAN
test           mysql   project:.dbq.yaml    $TEST_MYSQL_DSN
prod           pgx     project:.dbq.yaml    `vault read -field=dsn secret/db/prod`
```

---

## 🛠️ 项目目录说明

```
.
├── main.go         # CLI 入口、命令路由、多格式输出与执行引擎
├── conn.go         # 多环境连接加载器（.dbq.yaml、GoFrame 自动适配、动态 command）
├── dialect.go      # DM8、MySQL、Postgres、SQLite 四大多库方言字典映射
├── split.go        # 纯手写 SQL 词法扫描器（分号安全拆分与顶层 WHERE 检测）
├── compat.go       # 跨平台标准库辅助适配
├── main_test.go    # 单元测试（多语句切分、安全拦截、SQLite 端到端）
├── Makefile        # 构建与安装脚本
├── DESIGN.md       # 深度架构设计规格文档
└── README.md       # 本文档
```

---

## 📄 License

本项目采用 [MIT License](LICENSE) 开源协议。
