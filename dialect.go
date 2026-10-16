package main

import "fmt"

// dialect 保存各数据库方言差异。

type dialect struct {
	Placeholder   string // 占位符风格（当前未使用，预留）
	QuoteIdent    func(string) string
	TablesSQL     func() string
	ColumnsSQL    func() string
	IndexesSQL    func() string // 返回 "" 表示 Go 侧自行处理
	IndexGoHandle bool          // true 表示用 Go 侧 pragma 处理索引
	SampleSQL     func(table string, limit int) string
}

var dialects = map[string]dialect{
	"dm": {
		QuoteIdent: func(s string) string { return `"` + s + `"` },
		TablesSQL: func() string {
			return `SELECT t.TABLE_NAME AS table_name, c.COMMENTS AS comment
FROM USER_TABLES t
LEFT JOIN USER_TAB_COMMENTS c ON c.TABLE_NAME = t.TABLE_NAME
ORDER BY t.TABLE_NAME`
		},
		ColumnsSQL: func() string {
			return `SELECT col.COLUMN_NAME AS column_name, col.DATA_TYPE || CASE WHEN col.DATA_TYPE IN ('VARCHAR','VARCHAR2','CHAR') THEN '(' || col.DATA_LENGTH || ')' ELSE '' END AS type,
CASE col.NULLABLE WHEN 'Y' THEN 'YES' ELSE 'NO' END AS nullable,
col.DATA_DEFAULT AS default_value, cc.COMMENTS AS comment
FROM USER_TAB_COLUMNS col
LEFT JOIN USER_COL_COMMENTS cc ON cc.TABLE_NAME = col.TABLE_NAME AND cc.COLUMN_NAME = col.COLUMN_NAME
WHERE col.TABLE_NAME = :1
ORDER BY col.COLUMN_ID`
		},
		IndexesSQL: func() string {
			return `SELECT i.INDEX_NAME AS index_name,
CASE i.UNIQUENESS WHEN 'UNIQUE' THEN 'YES' ELSE 'NO' END AS unique_flag,
LISTAGG(ic.COLUMN_NAME, ',') WITHIN GROUP (ORDER BY ic.COLUMN_POSITION) AS columns
FROM USER_INDEXES i
JOIN USER_IND_COLUMNS ic ON ic.INDEX_NAME = i.INDEX_NAME
WHERE i.TABLE_NAME = :1
GROUP BY i.INDEX_NAME, i.UNIQUENESS
ORDER BY i.INDEX_NAME`
		},
		SampleSQL: func(table string, limit int) string {
			return fmt.Sprintf(`SELECT * FROM "%s" WHERE ROWNUM <= %d`, table, limit)
		},
	},
	"mysql": {
		QuoteIdent: func(s string) string { return "`" + s + "`" },
		TablesSQL: func() string {
			return `SELECT TABLE_NAME AS table_name, TABLE_COMMENT AS comment
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = COALESCE(DATABASE(), '%')
ORDER BY TABLE_NAME`
		},
		ColumnsSQL: func() string {
			return `SELECT COLUMN_NAME AS column_name, COLUMN_TYPE AS type,
CASE IS_NULLABLE WHEN 'YES' THEN 'YES' ELSE 'NO' END AS nullable,
COLUMN_DEFAULT AS default_value, COLUMN_COMMENT AS comment
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = COALESCE(DATABASE(), '%') AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION`
		},
		IndexesSQL: func() string {
			return `SELECT INDEX_NAME AS index_name,
CASE WHEN NON_UNIQUE = 0 THEN 'YES' ELSE 'NO' END AS unique_flag,
GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX) AS columns
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = COALESCE(DATABASE(), '%') AND TABLE_NAME = ?
GROUP BY INDEX_NAME, NON_UNIQUE
ORDER BY INDEX_NAME`
		},
		SampleSQL: func(table string, limit int) string {
			return fmt.Sprintf("SELECT * FROM `%s` LIMIT %d", table, limit)
		},
	},
	"pgx": {
		QuoteIdent: func(s string) string { return `"` + s + `"` },
		TablesSQL: func() string {
			return `SELECT c.relname AS table_name,
COALESCE(obj_description(c.oid), '') AS comment
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r','p')
AND n.nspname NOT IN ('pg_catalog','information_schema')
ORDER BY n.nspname, c.relname`
		},
		ColumnsSQL: func() string {
			return `SELECT a.attname AS column_name,
format_type(a.atttypid, a.atttypmod) AS type,
CASE a.attnotnull WHEN true THEN 'NO' ELSE 'YES' END AS nullable,
COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '') AS default_value,
COALESCE(col_description(a.attrelid, a.attnum), '') AS comment
FROM pg_attribute a
LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE a.attrelid = $1::regclass AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`
		},
		IndexesSQL: func() string {
			return `SELECT i.relname AS index_name,
CASE ix.indisunique WHEN true THEN 'YES' ELSE 'NO' END AS unique_flag,
string_agg(a.attname, ',' ORDER BY array_position(ix.indkey, a.attnum)) AS columns
FROM pg_index ix
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey)
WHERE t.relname = $1
GROUP BY i.relname, ix.indisunique
ORDER BY i.relname`
		},
		SampleSQL: func(table string, limit int) string {
			return fmt.Sprintf(`SELECT * FROM "%s" LIMIT %d`, table, limit)
		},
	},
	"sqlite": {
		QuoteIdent: func(s string) string { return `"` + s + `"` },
		TablesSQL: func() string {
			return `SELECT name AS table_name, '' AS comment
FROM sqlite_master
WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
ORDER BY name`
		},
		ColumnsSQL: func() string {
			return `SELECT name AS column_name, type AS type,
CASE WHEN "notnull" = 0 THEN 'YES' ELSE 'NO' END AS nullable,
COALESCE(dflt_value, '') AS default_value, '' AS comment
FROM pragma_table_info(?)`
		},
		IndexesSQL:    func() string { return "" },
		IndexGoHandle: true,
		SampleSQL: func(table string, limit int) string {
			return fmt.Sprintf(`SELECT * FROM "%s" LIMIT %d`, table, limit)
		},
	},
}
