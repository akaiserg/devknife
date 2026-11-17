package tools

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"devknife/internal/core"
)

// A token-based JavaScript (and mostly TypeScript) beautifier. It never fails:
// anything it does not understand is passed through with sensible spacing.

type jkind int

const (
	jWord jkind = iota
	jNum
	jStr
	jTmpl
	jRegex
	jPunct
	jLineComment
	jBlockComment
)

type jtok struct {
	kind jkind
	val  string
	nl   int // newlines in the whitespace before this token
}

var jsPuncts = []string{
	">>>=", "...", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=", "??=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "++", "--", "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "**", "<<", ">>",
}

var jsBeforeExpr = setOf("return", "typeof", "case", "do", "else", "in", "of", "new", "delete", "void", "throw", "instanceof", "yield", "await", "extends")

func (t *jtok) is(s string) bool {
	return t != nil && (t.kind == jPunct || t.kind == jWord) && t.val == s
}

func (t *jtok) isAny(ss ...string) bool {
	for _, s := range ss {
		if t.is(s) {
			return true
		}
	}
	return false
}

// exprExpected reports whether, after prev, the next token starts an
// expression (which decides regex vs division and unary vs binary +/-).
func exprExpected(prev *jtok) bool {
	if prev == nil {
		return true
	}
	switch prev.kind {
	case jPunct:
		return !prev.isAny(")", "]", "}", "++", "--")
	case jWord:
		return jsBeforeExpr[prev.val]
	}
	return false
}

func tokenizeJS(src string) []jtok {
	var toks []jtok
	var prev *jtok
	n := len(src)
	nl := 0
	push := func(k jkind, v string) {
		toks = append(toks, jtok{kind: k, val: v, nl: nl})
		nl = 0
		if k != jLineComment && k != jBlockComment {
			prev = &toks[len(toks)-1]
		}
	}
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\n':
			nl++
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = n - i
			}
			push(jLineComment, strings.TrimRight(src[i:i+end], " \t\r"))
			i += end
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				end = n - i - 4
			}
			push(jBlockComment, src[i:i+end+4])
			i += end + 4
		case c == '"' || c == '\'':
			j := scanJSString(src, i)
			push(jStr, src[i:j])
			i = j
		case c == '`':
			j := scanTemplate(src, i)
			push(jTmpl, src[i:j])
			i = j
		case c == '/' && exprExpected(prev):
			j := scanRegex(src, i)
			push(jRegex, src[i:j])
			i = j
		case c >= '0' && c <= '9' || (c == '.' && i+1 < n && src[i+1] >= '0' && src[i+1] <= '9'):
			j := i + 1
			for j < n {
				d := src[j]
				if isIdentPart(d) && d != '-' || d == '.' {
					j++
				} else if (d == '+' || d == '-') && (src[j-1] == 'e' || src[j-1] == 'E') && !strings.HasPrefix(strings.ToLower(src[i:j]), "0x") {
					j++
				} else {
					break
				}
			}
			push(jNum, src[i:j])
			i = j
		case isJSIdentStart(src, i):
			j := i
			for j < n {
				r, size := utf8.DecodeRuneInString(src[j:])
				if r == '$' || r == '_' || r == '#' && j == i || unicode.IsLetter(r) || unicode.IsDigit(r) || r == '‌' || r == '‍' {
					j += size
				} else {
					break
				}
			}
			push(jWord, src[i:j])
			i = j
		default:
			matched := ""
			for _, p := range jsPuncts {
				if strings.HasPrefix(src[i:], p) {
					matched = p
					break
				}
			}
			if matched == "" {
				_, size := utf8.DecodeRuneInString(src[i:])
				matched = src[i : i+size]
			}
			push(jPunct, matched)
			i += len(matched)
		}
	}
	return toks
}

func isJSIdentStart(src string, i int) bool {
	r, _ := utf8.DecodeRuneInString(src[i:])
	if r == '#' {
		return i+1 < len(src) && isIdentStart(src[i+1])
	}
	return r == '$' || r == '_' || unicode.IsLetter(r)
}

func scanJSString(src string, i int) int {
	q := src[i]
	j := i + 1
	for j < len(src) {
		switch src[j] {
		case '\\':
			j += 2
			continue
		case q:
			return j + 1
		case '\n':
			return j
		}
		j++
	}
	return len(src)
}

func scanTemplate(src string, i int) int {
	j := i + 1
	for j < len(src) {
		switch src[j] {
		case '\\':
			j += 2
		case '`':
			return j + 1
		case '$':
			if j+1 < len(src) && src[j+1] == '{' {
				j = scanBraces(src, j+2)
			} else {
				j++
			}
		default:
			j++
		}
	}
	return len(src)
}

func scanBraces(src string, j int) int {
	depth := 1
	for j < len(src) && depth > 0 {
		switch src[j] {
		case '{':
			depth++
			j++
		case '}':
			depth--
			j++
		case '"', '\'':
			j = scanJSString(src, j)
		case '`':
			j = scanTemplate(src, j)
		default:
			j++
		}
	}
	return j
}

func scanRegex(src string, i int) int {
	j := i + 1
	inClass := false
	for j < len(src) {
		c := src[j]
		switch {
		case c == '\\':
			j += 2
			continue
		case c == '\n':
			return j
		case c == '[':
			inClass = true
		case c == ']':
			inClass = false
		case c == '/' && !inClass:
			j++
			for j < len(src) && isIdentPart(src[j]) && src[j] != '-' {
				j++
			}
			return j
		}
		j++
	}
	return len(src)
}

// ---------------------------------------------------------------------------

type jsFrame struct {
	ch          byte
	kw          string // keyword before '(' (if/for/switch...)
	isSwitch    bool
	inCase      bool
	casePending bool
	ternary     int
	inline      bool // short object literal kept on one line
	multiline   bool // array broken over lines
	extra       int  // indentation this frame added
}

type jsFormatter struct {
	b           strings.Builder
	indent      string
	level       int
	stack       []*jsFrame
	root        jsFrame
	lineStart   bool
	pendSpace   bool
	forceNL     bool
	nlExtra     int
	prev        *jtok
	lastParenKw string
	doPending   bool
}

func (f *jsFormatter) top() *jsFrame {
	if len(f.stack) == 0 {
		return &f.root
	}
	return f.stack[len(f.stack)-1]
}

func (f *jsFormatter) newline(blank int) {
	if f.b.Len() == 0 {
		f.lineStart = true
		return
	}
	if !f.lineStart {
		f.b.WriteByte('\n')
	}
	if blank >= 2 && !strings.HasSuffix(f.b.String(), "{\n") && !strings.HasSuffix(f.b.String(), "\n\n") {
		f.b.WriteByte('\n')
	}
	f.lineStart = true
	f.pendSpace = false
}

func (f *jsFormatter) write(s string, space bool) {
	if f.lineStart {
		lvl := f.level + f.nlExtra
		if lvl < 0 {
			lvl = 0
		}
		core.CheckOutput(f.b.Len())
		f.b.WriteString(strings.Repeat(f.indent, lvl))
		f.lineStart = false
		f.nlExtra = 0
	} else if space || f.pendSpace {
		out := f.b.String()
		if len(out) > 0 && out[len(out)-1] != ' ' && out[len(out)-1] != '\n' {
			f.b.WriteByte(' ')
		}
	}
	f.pendSpace = false
	f.b.WriteString(s)
}

func (f *jsFormatter) tight(s string) { f.pendSpace = false; f.write(s, false) }

func endsStatement(t *jtok) bool {
	if t == nil {
		return false
	}
	switch t.kind {
	case jWord:
		return !jsBeforeExpr[t.val] || t.val == "return"
	case jNum, jStr, jTmpl, jRegex:
		return true
	}
	return t.isAny(")", "]", "}", "++", "--")
}

func startsStatement(t *jtok) bool {
	switch t.kind {
	case jWord, jNum, jStr, jTmpl, jRegex:
		return !t.isAny("in", "of", "instanceof")
	}
	return t.isAny("!", "~", "++", "--", "@", "...")
}

func isWordish(t *jtok) bool {
	if t == nil {
		return false
	}
	switch t.kind {
	case jWord, jNum, jStr, jTmpl, jRegex:
		return true
	}
	return t.isAny(")", "]", "}")
}

var jsParenSpaceKw = setOf("if", "for", "while", "switch", "catch", "with", "return", "typeof", "await", "yield", "case", "in", "of", "delete", "void", "throw", "else", "do", "async", "instanceof", "new", "extends")

// words after which a '{' starts an expression (object literal, destructuring)
var jsExprWords = setOf("return", "const", "let", "var", "import", "export", "default", "yield", "await", "typeof", "in", "of", "case", "throw", "new", "void")

var jsBinary = setOf("=", "==", "===", "!=", "!==", "<", ">", "<=", ">=", "&&", "||", "??", "*", "/", "%", "**", "&", "|", "^", "<<", ">>", ">>>",
	"+=", "-=", "*=", "/=", "%=", "**=", "&=", "|=", "^=", "<<=", ">>=", ">>>=", "&&=", "||=", "??=")

// inlineObjectEnd returns the index of the matching '}' if the object starting
// at i is short, flat and was written on one line; otherwise -1.
func inlineObjectEnd(toks []jtok, i int) int {
	depth, length := 0, 0
	for j := i; j < len(toks); j++ {
		t := &toks[j]
		if j > i && t.nl > 0 {
			return -1
		}
		if t.kind == jLineComment || t.kind == jBlockComment || t.isAny(";", "=>") || t.is("function") {
			return -1
		}
		length += len(t.val) + 1
		if length > 70 {
			return -1
		}
		switch {
		case t.isAny("{", "(", "["):
			depth++
			if j > i && t.is("{") {
				return -1
			}
		case t.isAny("}", ")", "]"):
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

func beautifyJS(src, indent string) string {
	toks := tokenizeJS(src)
	f := &jsFormatter{indent: indent, lineStart: true}
	inlineUntil := -1
	for i := 0; i < len(toks); i++ {
		t := &toks[i]
		var next *jtok
		if i+1 < len(toks) {
			next = &toks[i+1]
		}
		top := f.top()

		if t.kind == jLineComment {
			if f.b.Len() > 0 && t.nl == 0 && !f.lineStart {
				f.write(t.val, true)
			} else {
				f.newline(t.nl)
				f.write(t.val, false)
			}
			f.forceNL = true
			continue
		}
		if t.kind == jBlockComment {
			if t.nl > 0 || f.forceNL {
				f.newline(t.nl)
				f.forceNL = false
			}
			f.write(t.val, true)
			if next != nil && next.nl > 0 {
				f.forceNL = true
			} else {
				f.pendSpace = true
			}
			continue
		}

		// line breaks
		switch {
		case f.forceNL:
			f.newline(t.nl)
			f.forceNL = false
		case t.isAny(".", "?.") && t.nl > 0:
			f.newline(0)
			f.nlExtra = 1
		case f.prev != nil && t.nl > 0 && (top.ch == 0 || top.ch == '{') && !top.inline && endsStatement(f.prev) && startsStatement(t):
			f.newline(t.nl)
			if f.prev.is(")") && jsCtl(f.lastParenKw) || f.prev.is("else") {
				f.nlExtra = 1
			}
		}

		switch {
		case t.is("{"):
			blockish := f.prev == nil || f.prev.isAny(")", "=>", ";", "{", "}") ||
				(f.prev.kind == jWord && !jsExprWords[f.prev.val])
			end := -1
			if !blockish && i > inlineUntil {
				end = inlineObjectEnd(toks, i)
			}
			f.write("{", !f.prev.isAny("(", "[", "{", "${") && f.prev != nil)
			if next != nil && next.is("}") {
				f.tight("}")
				i++
				f.prev = &toks[i]
				f.afterClose(toks, i, nil)
				continue
			}
			fr := &jsFrame{ch: '{', isSwitch: f.prev.is(")") && f.lastParenKw == "switch"}
			if f.doPending {
				fr.kw = "do"
				f.doPending = false
			}
			if end > 0 {
				fr.inline = true
				inlineUntil = end
				f.pendSpace = true
			} else {
				fr.extra = 1
				f.level++
				f.forceNL = true
			}
			f.stack = append(f.stack, fr)
		case t.is("}"):
			fr := f.pop('{')
			if fr != nil {
				f.level -= fr.extra
				if fr.inCase {
					f.level--
				}
			}
			if fr != nil && fr.inline {
				f.write("}", true)
			} else {
				f.newline(0)
				f.write("}", false)
			}
			f.afterClose(toks, i, fr)
		case t.is("("):
			kw := ""
			if f.prev != nil && f.prev.kind == jWord {
				kw = f.prev.val
			}
			space := f.prev != nil && f.prev.kind == jWord && jsParenSpaceKw[f.prev.val]
			if f.prev != nil && (f.prev.kind == jWord || f.prev.isAny(")", "]", "?.")) && !space {
				f.tight("(")
			} else {
				f.write("(", space)
			}
			f.stack = append(f.stack, &jsFrame{ch: '(', kw: kw})
		case t.is(")"):
			if fr := f.pop('('); fr != nil {
				f.lastParenKw = fr.kw
				f.level -= fr.extra
				if fr.multiline {
					f.newline(0)
				}
			}
			f.tight(")")
		case t.is("["):
			if f.prev != nil && (f.prev.kind == jWord && !jsBeforeExpr[f.prev.val] || f.prev.isAny(")", "]", "?.") || f.prev.kind == jStr) {
				f.tight("[")
			} else {
				f.write("[", false)
			}
			fr := &jsFrame{ch: '['}
			if next != nil && next.nl > 0 && !next.is("]") {
				fr.multiline = true
				fr.extra = 1
				f.level++
				f.forceNL = true
			}
			f.stack = append(f.stack, fr)
		case t.is("]"):
			if fr := f.pop('['); fr != nil && fr.multiline {
				f.level -= fr.extra
				f.newline(0)
			}
			f.tight("]")
		case t.is(";"):
			f.tight(";")
			if top.ch == '(' {
				f.pendSpace = true
			} else {
				f.forceNL = true
			}
		case t.is(","):
			f.tight(",")
			if (top.ch == '{' && !top.inline) || top.multiline {
				f.forceNL = true
			} else {
				f.pendSpace = true
			}
		case t.isAny(".", "?."):
			f.tight(t.val)
		case t.is(":"):
			switch {
			case top.ternary > 0:
				top.ternary--
				f.write(":", true)
				f.pendSpace = true
			case top.casePending:
				f.tight(":")
				top.casePending = false
				top.inCase = true
				f.level++
				f.forceNL = true
			default:
				f.tight(":")
				f.pendSpace = true
			}
		case t.is("?"):
			top.ternary++
			f.write("?", true)
			f.pendSpace = true
		case t.is("=>"):
			f.write("=>", true)
			f.pendSpace = true
		case t.isAny("!", "~", "...", "@"):
			f.write(t.val, false)
		case t.isAny("++", "--"):
			if isWordish(f.prev) && !f.prev.is("}") {
				f.tight(t.val)
			} else {
				f.write(t.val, false)
			}
		case t.isAny("+", "-"):
			if exprExpected(f.prev) {
				f.write(t.val, f.prev != nil && !f.prev.isAny("(", "[", "!"))
			} else {
				f.write(t.val, true)
				f.pendSpace = true
			}
		case t.kind == jPunct && jsBinary[t.val]:
			f.write(t.val, true)
			f.pendSpace = true
		case t.kind == jPunct:
			f.write(t.val, false)
		default: // words, numbers, strings, templates, regexes
			if t.kind == jWord && (t.val == "case" || t.val == "default") && top.isSwitch && (next == nil || !next.is("(")) {
				if top.inCase {
					f.level--
					top.inCase = false
				}
				f.newline(t.nl)
				top.casePending = true
			}
			if t.is("do") {
				f.doPending = true
			}
			space := isWordish(f.prev) || (f.prev != nil && f.prev.isAny("=>", ",", ":", "?"))
			if f.prev != nil && f.prev.isAny("!", "~", "...", "@", "(", "[", ".", "?.", "++", "--", "#") && !(f.prev.isAny("++", "--") && isWordish(prevOf(toks, i-1))) {
				space = false
			}
			if f.prev != nil && f.prev.isAny("+", "-") && exprExpected(prevOf(toks, i-1)) {
				space = false // unary
			}
			f.write(t.val, space)
		}
		f.prev = t
	}
	out := strings.TrimRight(f.b.String(), " \n")
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n") + "\n"
}

func jsCtl(kw string) bool { return kw == "if" || kw == "for" || kw == "while" || kw == "with" }

// prevOf returns the significant token before index i (skipping comments).
func prevOf(toks []jtok, i int) *jtok {
	for j := i - 1; j >= 0; j-- {
		if toks[j].kind != jLineComment && toks[j].kind != jBlockComment {
			return &toks[j]
		}
	}
	return nil
}

func (f *jsFormatter) pop(ch byte) *jsFrame {
	for len(f.stack) > 0 {
		fr := f.stack[len(f.stack)-1]
		f.stack = f.stack[:len(f.stack)-1]
		if fr.ch == ch {
			return fr
		}
		// unbalanced: unwind indentation of the frame we skip
		f.level -= fr.extra
		if fr.inCase {
			f.level--
		}
	}
	return nil
}

// afterClose decides what follows a closing brace.
func (f *jsFormatter) afterClose(toks []jtok, i int, fr *jsFrame) {
	var next *jtok
	for j := i + 1; j < len(toks); j++ {
		if toks[j].kind != jLineComment && toks[j].kind != jBlockComment {
			next = &toks[j]
			break
		}
		if toks[j].kind == jLineComment && toks[j].nl == 0 {
			return
		}
	}
	if next == nil {
		return
	}
	switch {
	case next.isAny("else", "catch", "finally"):
		f.pendSpace = true
	case next.is("while") && fr != nil && fr.kw == "do":
		f.pendSpace = true
	case next.isAny(")", "]", ",", ";", ".", "?.", "(", ":", "?", "`") || next.kind == jTmpl:
	case f.top().inline || f.top().ch == '(' || f.top().ch == '[':
		// inside an expression (callback arguments, arrays of objects)
		if next.kind == jPunct && jsBinary[next.val] || next.is("=>") {
			f.pendSpace = true
		}
	case next.kind == jPunct && (jsBinary[next.val] || next.isAny("+", "-")):
		f.pendSpace = true
	default:
		f.forceNL = true
	}
}
