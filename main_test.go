package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSplitStatements(t *testing.T) {
	sql := `SELECT * FROM a; INSERT INTO b VALUES (';', 1); -- comment;
SELECT * FROM c;`
	stmts := splitStatements(sql)
	if len(stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d: %v", len(stmts), stmts)
	}
}

func TestKeywordsAndSafety(t *testing.T) {
	if kw := firstKeyword("  SELECT * FROM a"); kw != "SELECT" {
		t.Errorf("expected SELECT, got %s", kw)
	}
	if !isReadOnlyKW("SELECT") || !isReadOnlyKW("EXPLAIN") {
		t.Errorf("expected read-only for SELECT/EXPLAIN")
	}
	if isReadOnlyKW("DELETE") || isReadOnlyKW("DROP") {
		t.Errorf("expected non-read-only for DELETE/DROP")
	}

	if hasTopLevelWhere("DELETE FROM users") {
		t.Errorf("expected false for no where clause")
	}
	if !hasTopLevelWhere("DELETE FROM users WHERE id = 1") {
		t.Errorf("expected true for where clause")
	}
}

func TestEndToEndSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, "CREATE TABLE test (id INT, name TEXT); INSERT INTO test VALUES (1, 'demo');")
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	d := dialects["sqlite"]
	rows, err := db.QueryContext(ctx, d.TablesSQL())
	if err != nil {
		t.Fatalf("tables query failed: %v", err)
	}
	defer rows.Close()

	var tbl, comm string
	if rows.Next() {
		_ = rows.Scan(&tbl, &comm)
	}
	if tbl != "test" {
		t.Errorf("expected table name 'test', got '%s'", tbl)
	}
}
