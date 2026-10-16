package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	_ "gitee.com/chunanyong/dm"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const (
	defaultMaxRows    = 100 // 针对 Agent 上下文优化的默认行数限制
	defaultTimeoutSec = 30
	cellMaxRunes      = 2000 // 单元格长文本截断限制，防止大字段刷屏
)

func main() {
	opts := options{}
	flag.StringVar(&opts.conn, "c", "", "使用的命名连接（在 .dbq.yaml 或 ~/.config/dbq/profiles.yaml 中定义）")
	flag.StringVar(&opts.dsn, "dsn", "", "数据库连接串（支持 dm://, mysql://, postgres://, sqlite: 等）")
	flag.StringVar(&opts.driver, "driver", "", "显式指定驱动（dm / mysql / pgx / sqlite）")
	flag.BoolVar(&opts.write, "w", false, "允许写操作（INSERT/UPDATE/DELETE/DDL 等）")
	flag.BoolVar(&opts.danger, "danger", false, "允许全表无 WHERE 条件的 UPDATE/DELETE 或 DROP/TRUNCATE 危险操作")
	flag.BoolVar(&opts.tsv, "tsv", false, "制表符分隔输出（TSV）")
	flag.BoolVar(&opts.json, "json", false, "JSON 数组输出")
	flag.BoolVar(&opts.compact, "compact", false, "Token 紧凑 JSON 输出（{columns: [...], rows: [...]}，为 Agent 深度省 Token）")
	flag.IntVar(&opts.maxRows, "n", defaultMaxRows, "最大返回行数，0 为不限")
	flag.IntVar(&opts.timeoutSec, "timeout", defaultTimeoutSec, "语句超时秒数")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "dbq — 通用多数据库终端直查与 AI Agent 工具（支持 达梦DM8 / MySQL / PostgreSQL / SQLite）\n\n"+
			"用法:\n"+
			"  dbq [flags] \"SQL\"                  # 执行单条或多条 SQL 查询/变更\n"+
			"  dbq [flags] tables [pattern]         # 列出所有表、视图与注释（别名 ls）\n"+
			"  dbq [flags] desc <table_name>        # 查看表结构定义与索引（别名 columns, info）\n"+
			"  dbq [flags] sample <table_name>      # 快速抽样前 N 条数据（默认 5 条）\n"+
			"  dbq [flags] prompt [table_names...]  # 提取表结构生成专用于 LLM 的高密度 Markdown 上下文\n"+
			"  dbq [flags] conns                    # 列出所有已配置的命名连接与环境变量连接\n"+
			"  dbq [flags] ping                     # 测试连接连通性与数据库状态\n"+
			"  dbq [flags] < file.sql               # 从标准输入读取 SQL\n\n"+
			"Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()

	// 1. 处理无需连库或特殊子命令
	if len(args) > 0 && args[0] == "conns" {
		runConns()
		return
	}

	// 2. 解析连接
	rConn, err := resolveConn(opts)
	if err != nil {
		fail(1, opts, "%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(opts.timeoutSec)*time.Second)
	defer cancel()

	db, err := sql.Open(rConn.Driver, rConn.DSN)
	if err != nil {
		fail(1, opts, "打开数据库失败 (%s): %v", rConn.Driver, err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		fail(1, opts, "连接数据库失败 (%s): %v", rConn.Driver, err)
	}

	d, ok := dialects[rConn.Driver]
	if !ok {
		d = dialects["dm"] // fallback
	}

	// 3. 处理元数据子命令
	if len(args) > 0 {
		cmd := strings.ToLower(args[0])
		subArgs := args[1:]
		switch cmd {
		case "tables", "ls":
			runTables(ctx, db, d, subArgs, opts)
			return
		case "desc", "columns", "info", "describe":
			if len(subArgs) == 0 {
				fail(1, opts, "desc 命令缺少表名参数，例如: dbq desc sys_user")
			}
			runDesc(ctx, db, d, subArgs[0], opts)
			return
		case "sample", "head":
			if len(subArgs) == 0 {
				fail(1, opts, "sample 命令缺少表名参数，例如: dbq sample sys_user")
			}
			sampleLimit := opts.maxRows
			if sampleLimit <= 0 || sampleLimit > 100 {
				sampleLimit = 5
			}
			query := d.SampleSQL(subArgs[0], sampleLimit)
			runStatements(ctx, db, rConn, []string{query}, opts)
			return
		case "prompt", "schema", "context":
			runPrompt(ctx, db, d, subArgs, opts)
			return
		case "ping", "status":
			runPing(ctx, db, rConn)
			return
		}
	}

	// 4. 处理 SQL 执行
	sqlText := strings.TrimSpace(strings.Join(args, " "))
	if sqlText == "" {
		stdin, err := io.ReadAll(os.Stdin)
		if err != nil {
			fail(1, opts, "读取 stdin 失败: %v", err)
		}
		sqlText = strings.TrimSpace(string(stdin))
	}
	if sqlText == "" {
		flag.Usage()
		os.Exit(1)
	}

	stmts := splitStatements(sqlText)
	if len(stmts) == 0 {
		fail(1, opts, "未检测到有效 SQL 语句")
	}

	runStatements(ctx, db, rConn, stmts, opts)
}

func runStatements(ctx context.Context, db *sql.DB, rConn *ResolvedConn, stmts []string, opts options) {
	for i, stmt := range stmts {
		kw := firstKeyword(stmt)
		readonly := isReadOnlyKW(kw)

		if !readonly && (!opts.write || rConn.Readonly) {
			if rConn.Readonly {
				fail(2, opts, "语句 #%d 写操作被拦截: 连接 %s 被配置为只读 (readonly=true)", i+1, rConn.Source)
			}
			fail(2, opts, "语句 #%d 写操作被拦截: 包含修改语句 (%s)，需要显式指定 -w 才能执行", i+1, kw)
		}

		// 检查高危操作拦截
		if !readonly {
			if kw == "DROP" || kw == "TRUNCATE" {
				if !opts.danger {
					fail(2, opts, "高危破坏性操作被拦截: 检测到 %s 语句，需显式添加 -danger 参数才允许执行", kw)
				}
			} else if (kw == "DELETE" || kw == "UPDATE") && !hasTopLevelWhere(stmt) {
				if !opts.danger {
					fail(2, opts, "全表修改风险被拦截: 检测到无 WHERE 条件的 %s 语句，需显式添加 -danger 参数才允许执行", kw)
				}
			}
		}

		start := time.Now()
		if readonly {
			runQuery(ctx, db, stmt, opts, start)
		} else {
			runExec(ctx, db, stmt, opts, start)
		}
	}
}

func isReadOnlyKW(kw string) bool {
	switch strings.ToUpper(kw) {
	case "SELECT", "WITH", "EXPLAIN", "DESC", "DESCRIBE", "SHOW", "PRAGMA":
		return true
	}
	return false
}

func runQuery(ctx context.Context, db *sql.DB, sqlText string, opts options, start time.Time) {
	rows, err := db.QueryContext(ctx, sqlText)
	if err != nil {
		fail(1, opts, "查询执行失败: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		fail(1, opts, "读取列信息失败: %v", err)
	}

	asJSON := opts.json
	asCompact := opts.compact
	asTSV := opts.tsv

	var out io.Writer = os.Stdout
	if !asTSV && !asJSON && !asCompact {
		out = tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		io.WriteString(out, strings.Join(cols, "\t")+"\n")
	} else if asTSV {
		io.WriteString(out, strings.Join(cols, "\t")+"\n")
	}

	var jsonRows []map[string]any
	var compactRows [][]any

	truncatedCells := 0
	shown := 0
	total := 0

	for rows.Next() {
		total++
		if opts.maxRows > 0 && total > opts.maxRows {
			continue // 继续读完统计总数
		}

		vals := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			fail(1, opts, "读取第 %d 行失败: %v", total, err)
		}

		cells := make([]string, len(cols))
		jsonRow := make(map[string]any, len(cols))
		compactRow := make([]any, len(cols))

		for i, v := range vals {
			s, isNull := formatCellValue(v)
			if !isNull {
				t, cut := truncateCellRunes(s, cellMaxRunes)
				if cut {
					truncatedCells++
				}
				s = t
				jsonRow[cols[i]] = s
				compactRow[i] = s
			} else {
				jsonRow[cols[i]] = nil
				compactRow[i] = nil
			}
			cells[i] = s
		}

		shown++
		if asCompact {
			compactRows = append(compactRows, compactRow)
		} else if asJSON {
			jsonRows = append(jsonRows, jsonRow)
		} else {
			io.WriteString(out, strings.Join(cells, "\t")+"\n")
		}
	}

	if err := rows.Err(); err != nil {
		fail(1, opts, "遍历结果失败: %v", err)
	}

	if asCompact {
		res := map[string]any{
			"columns": cols,
			"rows":    compactRows,
			"meta": map[string]any{
				"shown_rows":   shown,
				"total_rows":   total,
				"elapsed_sec":  time.Since(start).Seconds(),
				"truncated":    opts.maxRows > 0 && total > opts.maxRows,
				"cell_cut_cnt": truncatedCells,
			},
		}
		enc := json.NewEncoder(os.Stdout)
		if err := enc.Encode(res); err != nil {
			fail(1, opts, "JSON 输出编码失败: %v", err)
		}
	} else if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(jsonRows); err != nil {
			fail(1, opts, "JSON 输出编码失败: %v", err)
		}
	} else if f, ok := out.(*tabwriter.Writer); ok {
		f.Flush()
	}

	meta := fmt.Sprintf("rows=%d elapsed=%.2fs", shown, time.Since(start).Seconds())
	if opts.maxRows > 0 && total > opts.maxRows {
		meta += fmt.Sprintf(" (共 %d 行，已截断显示前 %d 行，使用 -n 参数调整)", total, opts.maxRows)
	}
	if truncatedCells > 0 {
		meta += fmt.Sprintf(" (%d 个超长字段已截断至 %d 字符)", truncatedCells, cellMaxRunes)
	}
	fmt.Fprintln(os.Stderr, meta)
}

func runExec(ctx context.Context, db *sql.DB, sqlText string, opts options, start time.Time) {
	res, err := db.ExecContext(ctx, sqlText)
	if err != nil {
		fail(1, opts, "执行修改失败: %v", err)
	}
	n, _ := res.RowsAffected()
	if opts.json || opts.compact {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"ok":            true,
			"affected_rows": n,
			"elapsed_sec":   time.Since(start).Seconds(),
		})
		return
	}
	fmt.Fprintf(os.Stderr, "affected=%d elapsed=%.2fs\n", n, time.Since(start).Seconds())
}

func runTables(ctx context.Context, db *sql.DB, d dialect, args []string, opts options) {
	sqlStr := d.TablesSQL()
	pattern := ""
	if len(args) > 0 {
		pattern = strings.ToUpper(args[0])
	}

	rows, err := db.QueryContext(ctx, sqlStr)
	if err != nil {
		fail(1, opts, "获取表清单失败: %v", err)
	}
	defer rows.Close()

	type tableItem struct {
		Name    string `json:"table_name"`
		Comment string `json:"comment"`
	}
	var tables []tableItem

	for rows.Next() {
		var name string
		var comment sql.NullString
		if err := rows.Scan(&name, &comment); err != nil {
			continue
		}
		if pattern != "" && !strings.Contains(strings.ToUpper(name), pattern) {
			continue
		}
		c := ""
		if comment.Valid {
			c = comment.String
		}
		tables = append(tables, tableItem{Name: name, Comment: c})
	}

	if opts.json || opts.compact {
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(tables)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TABLE_NAME\tCOMMENT")
	for _, t := range tables {
		fmt.Fprintf(w, "%s\t%s\n", t.Name, t.Comment)
	}
	w.Flush()
	fmt.Fprintf(os.Stderr, "tables=%d\n", len(tables))
}

func runDesc(ctx context.Context, db *sql.DB, d dialect, tableName string, opts options) {
	colSQL := d.ColumnsSQL()
	rows, err := db.QueryContext(ctx, colSQL, tableName)
	if err != nil {
		// 达梦方言若小写表名查不到，尝试转大写重试
		if upper := strings.ToUpper(tableName); upper != tableName {
			rows, err = db.QueryContext(ctx, colSQL, upper)
		}
		if err != nil {
			fail(1, opts, "获取表字段信息失败 (%s): %v", tableName, err)
		}
	}
	defer rows.Close()

	type colItem struct {
		Name     string `json:"column_name"`
		Type     string `json:"type"`
		Nullable string `json:"nullable"`
		Default  string `json:"default"`
		Comment  string `json:"comment"`
	}
	var cols []colItem

	for rows.Next() {
		var name, colType, nullable string
		var dflt, comment sql.NullString
		if err := rows.Scan(&name, &colType, &nullable, &dflt, &comment); err != nil {
			continue
		}
		defVal := ""
		if dflt.Valid {
			defVal = strings.TrimSpace(dflt.String)
		}
		comm := ""
		if comment.Valid {
			comm = comment.String
		}
		cols = append(cols, colItem{
			Name:     name,
			Type:     colType,
			Nullable: nullable,
			Default:  defVal,
			Comment:  comm,
		})
	}

	if opts.json || opts.compact {
		_ = json.NewEncoder(os.Stdout).Encode(cols)
		return
	}

	fmt.Printf("Table: %s\n", tableName)
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "COLUMN_NAME\tTYPE\tNULLABLE\tDEFAULT\tCOMMENT")
	for _, c := range cols {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.Name, c.Type, c.Nullable, c.Default, c.Comment)
	}
	w.Flush()

	// 打印索引信息
	if idxSQL := d.IndexesSQL(); idxSQL != "" {
		iRows, err := db.QueryContext(ctx, idxSQL, tableName)
		if err == nil {
			defer iRows.Close()
			hasIndex := false
			for iRows.Next() {
				if !hasIndex {
					fmt.Println("\nIndexes:")
					hasIndex = true
				}
				var idxName, u, columns string
				if err := iRows.Scan(&idxName, &u, &columns); err == nil {
					fmt.Printf("  - %s (unique=%s): (%s)\n", idxName, u, columns)
				}
			}
		}
	}
}

func runPrompt(ctx context.Context, db *sql.DB, d dialect, tableNames []string, opts options) {
	if len(tableNames) == 0 {
		// 获取前 20 张表
		tRows, err := db.QueryContext(ctx, d.TablesSQL())
		if err == nil {
			defer tRows.Close()
			for tRows.Next() {
				var name string
				var comment sql.NullString
				_ = tRows.Scan(&name, &comment)
				tableNames = append(tableNames, name)
				if len(tableNames) >= 20 {
					break
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("## Database Schema Context\n\n")

	for _, tbl := range tableNames {
		colSQL := d.ColumnsSQL()
		rows, err := db.QueryContext(ctx, colSQL, tbl)
		if err != nil {
			if upper := strings.ToUpper(tbl); upper != tbl {
				rows, err = db.QueryContext(ctx, colSQL, upper)
			}
			if err != nil {
				continue
			}
		}

		sb.WriteString(fmt.Sprintf("### Table: `%s`\n", tbl))
		sb.WriteString("| Column | Type | Nullable | Default | Comment |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")

		for rows.Next() {
			var name, colType, nullable string
			var dflt, comment sql.NullString
			if err := rows.Scan(&name, &colType, &nullable, &dflt, &comment); err == nil {
				comm := ""
				if comment.Valid {
					comm = comment.String
				}
				defVal := ""
				if dflt.Valid {
					defVal = strings.TrimSpace(dflt.String)
				}
				sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s |\n", name, colType, nullable, defVal, comm))
			}
		}
		rows.Close()
		sb.WriteString("\n")
	}

	fmt.Println(sb.String())
}

func runConns() {
	var projCfg, userCfg *Config
	projPath := findProjectConfig()
	if projPath != "" {
		projCfg, _ = loadConfig(projPath)
	}
	userPath := userConfigPath()
	if userPath != "" {
		userCfg, _ = loadConfig(userPath)
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDRIVER\tSOURCE\tDSN")

	count := 0
	if projCfg != nil {
		for name, c := range projCfg.Connections {
			defMark := ""
			if projCfg.Default == name {
				defMark = " (default)"
			}
			dsn := c.DSN
			if c.DSNEnv != "" {
				dsn = "$" + c.DSNEnv
			} else if c.Command != "" {
				dsn = "`" + c.Command + "`"
			}
			fmt.Fprintf(w, "%s%s\t%s\tproject:%s\t%s\n", name, defMark, c.Driver, projPath, maskDSN(dsn))
			count++
		}
	}
	if userCfg != nil {
		for name, c := range userCfg.Connections {
			defMark := ""
			if userCfg.Default == name {
				defMark = " (default)"
			}
			dsn := c.DSN
			if c.DSNEnv != "" {
				dsn = "$" + c.DSNEnv
			} else if c.Command != "" {
				dsn = "`" + c.Command + "`"
			}
			fmt.Fprintf(w, "%s%s\t%s\tuser:%s\t%s\n", name, defMark, c.Driver, userPath, maskDSN(dsn))
			count++
		}
	}
	if env := os.Getenv("DBQ_DSN"); env != "" {
		fmt.Fprintf(w, "DBQ_DSN\t(auto)\tenv:DBQ_DSN\t%s\n", maskDSN(env))
		count++
	}
	if env := os.Getenv("DM_DSN"); env != "" {
		fmt.Fprintf(w, "DM_DSN\tdm\tenv:DM_DSN\t%s\n", maskDSN(env))
		count++
	}

	w.Flush()
	if count == 0 {
		fmt.Println("未找到任何已配置的连接。可通过 -dsn 直接传入，或创建 .dbq.yaml 配置命名连接。")
	}
}

func runPing(ctx context.Context, db *sql.DB, rConn *ResolvedConn) {
	start := time.Now()
	if err := db.PingContext(ctx); err != nil {
		fail(1, options{}, "Ping 失败: %v", err)
	}
	latency := time.Since(start)

	var version string
	switch rConn.Driver {
	case "sqlite":
		_ = db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version)
	case "mysql":
		_ = db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	case "pgx":
		_ = db.QueryRowContext(ctx, "SELECT version()").Scan(&version)
	case "dm":
		_ = db.QueryRowContext(ctx, "SELECT BANNER FROM V$VERSION WHERE ROWNUM=1").Scan(&version)
	}

	fmt.Printf("Connection: %s\n", rConn.Source)
	fmt.Printf("Driver:     %s\n", rConn.Driver)
	fmt.Printf("DSN:        %s\n", maskDSN(rConn.DSN))
	if version != "" {
		fmt.Printf("Version:    %s\n", strings.TrimSpace(version))
	}
	fmt.Printf("Latency:    %.2fms\n", float64(latency.Microseconds())/1000.0)
	fmt.Println("Status:     OK")
}

func formatCellValue(v any) (string, bool) {
	switch val := v.(type) {
	case nil:
		return "NULL", true
	case []byte:
		return string(val), false
	case time.Time:
		return val.Format("2006-01-02 15:04:05"), false
	default:
		return fmt.Sprint(val), false
	}
}

func truncateCellRunes(s string, max int) (string, bool) {
	runes := []rune(s)
	if len(runes) <= max {
		return s, false
	}
	return string(runes[:max]) + "…", true
}

func fail(code int, opts options, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if opts.json || opts.compact {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"ok":    false,
			"error": msg,
		})
	} else {
		fmt.Fprintf(os.Stderr, "dbq: %s\n", msg)
	}
	os.Exit(code)
}
