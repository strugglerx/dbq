package main

import "strings"

// SQL 词法状态，供语句切分和关键字判断共用。

type tokenKind int

const (
	tkWord    tokenKind = iota // 标识符/关键字
	tkStr                      // 字符串字面量
	tkComment                  // 注释
	tkOther                    // 其他符号
)

type token struct {
	kind  tokenKind
	text  string // 原文（含引号/注释符）
	word  string // 大写化后的词（仅 tkWord）
	depth int    // 词法时的括号深度（进入该词之前）
}

// scanSQL 逐词扫描 SQL，回调每个 token。
func scanSQL(sql string, fn func(t token) bool) {
	depth := 0
	i := 0
	n := len(sql)
	for i < n {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '-' && i+1 < n && sql[i+1] == '-':
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = n - i
			}
			if !fn(token{kind: tkComment, text: sql[i : i+j], depth: depth}) {
				return
			}
			i += j
		case c == '/' && i+1 < n && sql[i+1] == '*':
			j := strings.Index(sql[i+2:], "*/")
			end := n
			if j >= 0 {
				end = i + 2 + j + 2
			}
			if !fn(token{kind: tkComment, text: sql[i:end], depth: depth}) {
				return
			}
			i = end
		case c == '\'':
			j := i + 1
			for j < n {
				if sql[j] == '\'' {
					if j+1 < n && sql[j+1] == '\'' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			if !fn(token{kind: tkStr, text: sql[i:j], depth: depth}) {
				return
			}
			i = j
		case c == '"' || c == '`':
			q := c
			j := i + 1
			for j < n {
				if sql[j] == q {
					if q == '"' && j+1 < n && sql[j+1] == '"' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			if !fn(token{kind: tkStr, text: sql[i:j], depth: depth}) {
				return
			}
			i = j
		case c == '(':
			depth++
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			i++
		case isWordChar(c):
			j := i
			for j < n && isWordChar(sql[j]) {
				j++
			}
			if !fn(token{kind: tkWord, text: sql[i:j], word: strings.ToUpper(sql[i:j]), depth: depth}) {
				return
			}
			i = j
		default:
			if !fn(token{kind: tkOther, text: string(c), depth: depth}) {
				return
			}
			i++
		}
	}
}

func isWordChar(c byte) bool {
	return c == '_' || c == '$' || c == '#' || c == '@' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// firstKeyword 返回第一个有效关键字（跳过注释和括号内内容）。
func firstKeyword(sql string) string {
	var kw string
	scanSQL(sql, func(t token) bool {
		if t.kind == tkWord && t.depth == 0 && kw == "" {
			// 跳过前导括号场景：scanSQL 已保证 depth==0
			kw = t.word
			return false
		}
		return true
	})
	return kw
}

// hasTopLevelWhere 判断是否存在顶层 WHERE（括号外）。
func hasTopLevelWhere(sql string) bool {
	found := false
	scanSQL(sql, func(t token) bool {
		if t.kind == tkWord && t.depth == 0 && t.word == "WHERE" {
			found = true
			return false
		}
		return true
	})
	return found
}

// splitStatements 把多语句 SQL 按顶层分号切分。
func splitStatements(sql string) []string {
	var stmts []string
	var cur strings.Builder
	depth := 0
	i, n := 0, len(sql)

	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			stmts = append(stmts, s)
		}
		cur.Reset()
	}

	for i < n {
		c := sql[i]
		switch {
		case c == '-' && i+1 < n && sql[i+1] == '-':
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = n - i
			}
			cur.WriteString(sql[i : i+j])
			i += j
		case c == '/' && i+1 < n && sql[i+1] == '*':
			j := strings.Index(sql[i+2:], "*/")
			end := n
			if j >= 0 {
				end = i + 2 + j + 2
			}
			cur.WriteString(sql[i:end])
			i = end
		case c == '\'':
			j := i + 1
			for j < n {
				if sql[j] == '\'' {
					if j+1 < n && sql[j+1] == '\'' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			cur.WriteString(sql[i:j])
			i = j
		case c == '"' || c == '`':
			q := c
			j := i + 1
			for j < n {
				if sql[j] == q {
					if q == '"' && j+1 < n && sql[j+1] == '"' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			cur.WriteString(sql[i:j])
			i = j
		case c == '(':
			depth++
			cur.WriteByte(c)
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
			i++
		case c == ';' && depth == 0:
			flush()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return stmts
}
