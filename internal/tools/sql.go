package tools

import (
	"strings"

	"devknife/internal/core"
)

type stok struct {
	kind string // word, str, ident, num, op, open, close, comma, semi, dot, comment, param
	val  string
	nl   bool // newline preceded (for comments)
}

var sqlKeywords = setOf(
	"select", "from", "where", "and", "or", "not", "in", "is", "null", "as", "on", "join", "left", "right", "inner", "outer", "full", "cross", "natural",
	"group", "by", "order", "having", "limit", "offset", "union", "all", "distinct", "insert", "into", "values", "update", "set", "delete", "create",
	"table", "drop", "alter", "add", "column", "index", "primary", "key", "foreign", "references", "default", "unique", "check", "constraint",
	"case", "when", "then", "else", "end", "asc", "desc", "between", "like", "ilike", "exists", "with", "recursive", "returning", "intersect",
	"except", "true", "false", "if", "view", "database", "schema", "grant", "revoke", "begin", "commit", "rollback", "transaction", "using",
	"over", "partition", "window", "rows", "range", "unbounded", "preceding", "following", "current", "row", "fetch", "next", "only", "first",
	"last", "nulls", "lateral", "conflict", "do", "nothing", "replace", "ignore", "temporary", "temp", "truncate", "cascade", "restrict",
	"integer", "int", "varchar", "text", "boolean", "timestamp", "date", "serial", "bigint", "smallint", "decimal", "numeric", "float", "real",
	"char", "json", "jsonb", "uuid", "auto_increment", "engine", "collate", "charset", "any", "some", "similar", "escape", "cast", "interval",
	"materialized", "trigger", "function", "procedure", "returns", "language", "declare", "for", "each", "execute", "explain", "analyze", "filter", "within",
)

// clause keywords that start a new line, longest first
var sqlClauses = []string{
	"insert into", "delete from", "group by", "order by", "partition by", "union all", "on conflict", "left outer join", "right outer join",
	"full outer join", "left join", "right join", "inner join", "cross join", "full join", "natural join", "join", "select distinct",
	"select", "from", "where", "having", "limit", "offset", "union", "intersect", "except", "values", "update", "set", "returning", "with",
	"window", "fetch", "create table", "alter table", "drop table", "create index", "create view", "truncate",
}

var sqlJoinish = setOf("join", "left join", "right join", "inner join", "cross join", "full join", "natural join", "left outer join", "right outer join", "full outer join")

func tokenizeSQL(src string) []stok {
	var toks []stok
	n := len(src)
	nl := false
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\n':
			nl = true
			i++
		case isSpace(c):
			i++
		case strings.HasPrefix(src[i:], "--") || (c == '#' && (i == 0 || src[i-1] == '\n')):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = n - i
			}
			toks = append(toks, stok{kind: "comment", val: strings.TrimSpace(src[i : i+end]), nl: nl})
			i += end
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				end = n - i - 4
			}
			toks = append(toks, stok{kind: "comment", val: src[i : i+end+4], nl: nl})
			i += end + 4
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < n {
				if src[j] == '\\' && c == '\'' {
					j += 2
					continue
				}
				if src[j] == c {
					if j+1 < n && src[j+1] == c { // doubled quote escape
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= n {
				j = n - 1
			}
			kind := "str"
			if c != '\'' {
				kind = "ident"
			}
			toks = append(toks, stok{kind: kind, val: src[i : j+1]})
			i = j + 1
		case c == '[' && i+1 < n && isIdentStart(src[i+1]):
			end := strings.IndexByte(src[i:], ']')
			if end < 0 {
				end = n - i - 1
			}
			toks = append(toks, stok{kind: "ident", val: src[i : i+end+1]})
			i += end + 1
		case c == '$' && i+1 < n && (src[i+1] == '$' || isIdentStart(src[i+1])) && strings.Contains(src[i+1:], "$"):
			// PostgreSQL dollar quoting: $$...$$ or $tag$...$tag$
			end := strings.IndexByte(src[i+1:], '$')
			tag := src[i : i+end+2]
			if strings.Trim(tag, "$") == "" || isAlnumStr(strings.Trim(tag, "$")) {
				close := strings.Index(src[i+len(tag):], tag)
				if close >= 0 {
					toks = append(toks, stok{kind: "str", val: src[i : i+len(tag)+close+len(tag)]})
					i += len(tag) + close + len(tag)
					break
				}
			}
			toks = append(toks, stok{kind: "param", val: "$"})
			i++
		case (c >= '0' && c <= '9') || (c == '.' && i+1 < n && src[i+1] >= '0' && src[i+1] <= '9'):
			j := i
			for j < n && (isAlnum(src[j]) || src[j] == '.') {
				j++
			}
			toks = append(toks, stok{kind: "num", val: src[i:j]})
			i = j
		case isIdentStart(c) || c >= 0x80:
			j := i
			for j < n && (isAlnum(src[j]) || src[j] == '_' || src[j] == '$' || src[j] >= 0x80) {
				j++
			}
			toks = append(toks, stok{kind: "word", val: src[i:j]})
			i = j
		case c == '(':
			toks = append(toks, stok{kind: "open", val: "("})
			i++
		case c == ')':
			toks = append(toks, stok{kind: "close", val: ")"})
			i++
		case c == ',':
			toks = append(toks, stok{kind: "comma", val: ","})
			i++
		case c == ';':
			toks = append(toks, stok{kind: "semi", val: ";"})
			i++
		case c == '.':
			toks = append(toks, stok{kind: "dot", val: "."})
			i++
		case (c == '?' || c == ':' || c == '@') && i+1 < n && (isIdentStart(src[i+1]) || (src[i+1] >= '0' && src[i+1] <= '9')) && !(c == ':' && i > 0 && src[i-1] == ':'):
			j := i + 1
			for j < n && (isAlnum(src[j]) || src[j] == '_') {
				j++
			}
			toks = append(toks, stok{kind: "param", val: src[i:j]})
			i = j
		default:
			ops := []string{"->>", "#>>", "::", "<=", ">=", "<>", "!=", "||", "->", ":=", "#>", "@>", "<@", "&&"}
			m := string(c)
			for _, o := range ops {
				if strings.HasPrefix(src[i:], o) {
					m = o
					break
				}
			}
			kind := "op"
			if m == "?" {
				kind = "param"
			}
			toks = append(toks, stok{kind: kind, val: m})
			i += len(m)
		}
		if len(toks) > 0 && toks[len(toks)-1].kind != "comment" {
			nl = false
		}
	}
	return toks
}

func isAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func isAlnumStr(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isAlnum(s[i]) {
			return false
		}
	}
	return true
}

type sqlFrame struct {
	base, indent int
	sub          bool // subquery paren (indents) vs function/list paren (inline)
}

func formatSQL(src, indent, kwCase string) string {
	toks := tokenizeSQL(src)
	var b strings.Builder
	base, ind := 0, 0
	var stack []sqlFrame
	parenDepth := 0 // inline parens since the current clause began
	var parenStack []int
	lineStart := true
	betweenPending := false
	caseDepth := []int{}
	prevKind, prevVal := "", ""

	kw := func(s string) string {
		switch kwCase {
		case "lower":
			return strings.ToLower(s)
		case "preserve":
			return s
		}
		return strings.ToUpper(s)
	}
	pendingLevel := 0
	// indentation is written lazily so consecutive newline() calls collapse
	newline := func(level int) {
		if !lineStart {
			b.WriteString("\n")
		}
		lineStart = true
		pendingLevel = max(level, 0)
	}
	write := func(s string, space bool) {
		if lineStart {
			if b.Len() > 0 {
				core.CheckOutput(b.Len())
				b.WriteString(strings.Repeat(indent, pendingLevel))
			}
		} else if space {
			b.WriteByte(' ')
		}
		b.WriteString(s)
		lineStart = false
	}
	spaceBefore := func(kind, val string) bool {
		switch {
		case lineStart:
			return false
		case kind == "comma" || kind == "close" || kind == "dot" || kind == "semi":
			return false
		case prevKind == "open" || prevKind == "dot":
			return false
		case val == "::" || prevVal == "::":
			return false
		}
		return true
	}

	for i := 0; i < len(toks); i++ {
		t := toks[i]
		lv := strings.ToLower(t.val)

		if t.kind == "comment" {
			if t.nl || i == 0 {
				newline(ind)
			}
			write(t.val, true)
			if strings.HasPrefix(t.val, "--") || strings.HasPrefix(t.val, "#") {
				newline(ind)
			}
			continue
		}

		if t.kind == "word" {
			// multi-word clause keywords
			matched := ""
			for _, cl := range sqlClauses {
				parts := strings.Fields(cl)
				if i+len(parts) > len(toks) {
					continue
				}
				ok := true
				for k, p := range parts {
					if toks[i+k].kind != "word" || strings.ToLower(toks[i+k].val) != p {
						ok = false
						break
					}
				}
				if ok {
					matched = cl
					break
				}
			}
			if matched != "" && parenDepth == 0 {
				words := strings.Fields(matched)
				orig := make([]string, len(words))
				for k := range words {
					orig[k] = kw(toks[i+k].val)
				}
				i += len(words) - 1
				if sqlJoinish[matched] {
					newline(base + 1)
					write(strings.Join(orig, " "), false)
					ind = base + 1
				} else if matched == "set" && prevVal != "" && strings.ToLower(prevVal) != "update" && !isUpdateTarget(toks, i) {
					write(kw("SET"), true) // e.g. "ON CONFLICT ... DO UPDATE SET", charset
				} else {
					if b.Len() > 0 {
						newline(base)
					}
					write(strings.Join(orig, " "), false)
					ind = base + 1
					// short clauses stay on one line: LIMIT 10, OFFSET 5
					if matched == "limit" || matched == "offset" || matched == "fetch" {
						prevKind, prevVal = "word", matched
						continue
					}
					newline(ind)
				}
				prevKind, prevVal = "word", matched
				continue
			}
			switch {
			case (lv == "and" || lv == "or") && parenDepth == 0 && !betweenPending:
				newline(ind)
				write(kw(t.val), false)
				prevKind, prevVal = "word", lv
				continue
			case lv == "and" && betweenPending:
				betweenPending = false
			case lv == "between":
				betweenPending = true
			case lv == "on" && parenDepth == 0:
				// keep "JOIN x ON cond" on one line
			case lv == "case":
				write(kw(t.val), spaceBefore(t.kind, t.val))
				caseDepth = append(caseDepth, ind)
				ind++
				prevKind, prevVal = "word", lv
				continue
			case (lv == "when" || lv == "else") && len(caseDepth) > 0:
				newline(ind)
				write(kw(t.val), false)
				prevKind, prevVal = "word", lv
				continue
			case lv == "end" && len(caseDepth) > 0:
				ind = caseDepth[len(caseDepth)-1]
				caseDepth = caseDepth[:len(caseDepth)-1]
				newline(ind)
				write(kw(t.val), false)
				prevKind, prevVal = "word", lv
				continue
			}
			val := t.val
			if sqlKeywords[lv] && prevKind != "dot" && !(i+1 < len(toks) && toks[i+1].kind == "dot") {
				val = kw(t.val)
			}
			write(val, spaceBefore(t.kind, t.val))
			prevKind, prevVal = t.kind, t.val
			continue
		}

		switch t.kind {
		case "open":
			isSub := i+1 < len(toks) && toks[i+1].kind == "word" && (strings.EqualFold(toks[i+1].val, "select") || strings.EqualFold(toks[i+1].val, "with"))
			space := spaceBefore(t.kind, t.val)
			if prevKind == "word" && !sqlKeywords[strings.ToLower(prevVal)] {
				space = false // function call: count(*)
			}
			write("(", space)
			if isSub {
				stack = append(stack, sqlFrame{base: base, indent: ind, sub: true})
				parenStack = append(parenStack, parenDepth)
				parenDepth = 0
				base = ind + 1
				ind = base
				newline(base)
			} else {
				stack = append(stack, sqlFrame{base: base, indent: ind})
				parenDepth++
			}
		case "close":
			if len(stack) > 0 {
				fr := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if fr.sub {
					base, ind = fr.base, fr.indent
					if len(parenStack) > 0 {
						parenDepth = parenStack[len(parenStack)-1]
						parenStack = parenStack[:len(parenStack)-1]
					}
					newline(ind)
					write(")", false)
					break
				}
				parenDepth--
			}
			write(")", false)
		case "comma":
			write(",", false)
			if parenDepth == 0 {
				newline(ind)
			}
		case "semi":
			write(";", false)
			base, ind, parenDepth = 0, 0, 0
			stack = nil
			if i+1 < len(toks) {
				b.WriteString("\n")
				lineStart = false
				newline(0)
			}
		default:
			write(t.val, spaceBefore(t.kind, t.val))
		}
		prevKind, prevVal = t.kind, t.val
	}
	out := b.String()
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n")) + "\n"
}

func isUpdateTarget(toks []stok, i int) bool {
	// "UPDATE t SET": SET follows the table name after UPDATE
	for j := i - 1; j >= 0 && j >= i-4; j-- {
		if toks[j].kind == "word" && strings.EqualFold(toks[j].val, "update") {
			return true
		}
	}
	return false
}

func minifySQL(src string) string {
	toks := tokenizeSQL(src)
	var b strings.Builder
	prev := stok{}
	for _, t := range toks {
		if t.kind == "comment" {
			if strings.HasPrefix(t.val, "/*!") { // MySQL executable comments
				b.WriteString(t.val)
			}
			continue
		}
		needSpace := b.Len() > 0 && isWordLike(prev) && isWordLike(t)
		if needSpace {
			b.WriteByte(' ')
		} else if b.Len() > 0 && t.kind == "op" && prev.kind == "op" {
			b.WriteByte(' ')
		}
		b.WriteString(t.val)
		prev = t
	}
	return b.String()
}

func isWordLike(t stok) bool {
	return t.kind == "word" || t.kind == "num" || t.kind == "str" || t.kind == "ident" || t.kind == "param"
}
