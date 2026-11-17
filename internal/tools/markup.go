package tools

import (
	"fmt"
	"regexp"
	"strings"

	"devknife/internal/core"
)

// A small, forgiving markup tokenizer + tree builder shared by the HTML, ERB,
// XML and HTML→JSX tools. Unlike x/net/html it preserves attribute case and
// original quoting (SVG viewBox, Vue @click, Angular [prop] all survive), and
// it understands ERB tags.

type mkind int

const (
	mText mkind = iota
	mStart
	mEnd
	mComment
	mDoctype
	mCDATA
	mPI
	mERB
	mRaw // raw text content of script/style/textarea/pre
)

type mattr struct {
	name     string
	value    string
	hasValue bool
	quote    byte // 0 = unquoted
}

type mtok struct {
	kind  mkind
	name  string // original case
	lname string
	attrs []mattr
	self  bool // self-closing />
	raw   string
}

type markupMode struct {
	html bool // HTML rules: void elements, raw text elements, case-insensitive
	erb  bool
}

var htmlVoid = setOf("area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr", "keygen", "command")
var htmlRawText = setOf("script", "style", "textarea", "pre", "title", "xmp", "plaintext")
var htmlInline = setOf("a", "abbr", "acronym", "b", "bdi", "bdo", "big", "br", "button", "cite", "code", "data", "del", "dfn", "em", "i", "img", "input", "ins", "kbd", "label", "mark", "meter", "output", "progress", "q", "s", "samp", "select", "small", "span", "strike", "strong", "sub", "sup", "time", "tt", "u", "var", "wbr", "svg", "math", "iframe", "canvas", "audio", "video", "picture", "object", "embed", "font")

func setOf(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func tokenizeMarkup(src string, mode markupMode) []mtok {
	var toks []mtok
	n := len(src)
	i := 0
	textStart := 0
	flushText := func(end int) {
		if end > textStart {
			toks = append(toks, mtok{kind: mText, raw: src[textStart:end]})
		}
	}
	for i < n {
		if src[i] != '<' {
			i++
			continue
		}
		rest := src[i:]
		switch {
		case mode.erb && strings.HasPrefix(rest, "<%"):
			end := strings.Index(rest[2:], "%>")
			if end < 0 {
				i++
				continue
			}
			flushText(i)
			toks = append(toks, mtok{kind: mERB, raw: rest[:end+4]})
			i += end + 4
			textStart = i
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest[4:], "-->")
			flushText(i)
			if end < 0 {
				toks = append(toks, mtok{kind: mComment, raw: rest})
				i = n
			} else {
				toks = append(toks, mtok{kind: mComment, raw: rest[:end+7]})
				i += end + 7
			}
			textStart = i
		case strings.HasPrefix(rest, "<![CDATA["):
			end := strings.Index(rest, "]]>")
			flushText(i)
			if end < 0 {
				end = len(rest) - 3
			}
			toks = append(toks, mtok{kind: mCDATA, raw: rest[:end+3]})
			i += end + 3
			textStart = i
		case strings.HasPrefix(rest, "<!") || strings.HasPrefix(rest, "<?"):
			closer := ">"
			if rest[1] == '?' {
				closer = "?>"
			}
			end := strings.Index(rest, closer)
			flushText(i)
			if end < 0 {
				end = len(rest) - len(closer)
			}
			k := mDoctype
			if rest[1] == '?' {
				k = mPI
			}
			toks = append(toks, mtok{kind: k, raw: rest[:end+len(closer)]})
			i += end + len(closer)
			textStart = i
		case len(rest) > 2 && rest[1] == '/' && isTagStart(rest[2]):
			j := 2
			for j < len(rest) && !isSpace(rest[j]) && rest[j] != '>' {
				j++
			}
			name := rest[2:j]
			end := strings.IndexByte(rest[j:], '>')
			if end < 0 {
				i++
				continue
			}
			flushText(i)
			toks = append(toks, mtok{kind: mEnd, name: name, lname: strings.ToLower(name)})
			i += j + end + 1
			textStart = i
		case len(rest) > 1 && isTagStart(rest[1]):
			tok, consumed, ok := parseStartTag(rest, mode)
			if !ok {
				i++
				continue
			}
			flushText(i)
			toks = append(toks, tok)
			i += consumed
			textStart = i
			if mode.html && htmlRawText[tok.lname] && !tok.self {
				closeIdx := indexFold(src[i:], "</"+tok.lname)
				if closeIdx < 0 {
					closeIdx = n - i
				}
				if closeIdx > 0 {
					toks = append(toks, mtok{kind: mRaw, raw: src[i : i+closeIdx], lname: tok.lname})
				}
				i += closeIdx
				textStart = i
			}
		default:
			i++
		}
	}
	flushText(n)
	return toks
}

func isTagStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == ':'
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

func indexFold(s, sub string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(sub))
}

func parseStartTag(s string, mode markupMode) (mtok, int, bool) {
	j := 1
	for j < len(s) && !isSpace(s[j]) && s[j] != '>' && !(s[j] == '/' && j+1 < len(s) && s[j+1] == '>') {
		j++
	}
	tok := mtok{kind: mStart, name: s[1:j]}
	tok.lname = strings.ToLower(tok.name)
	for j < len(s) {
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		if j >= len(s) {
			return tok, 0, false
		}
		if s[j] == '>' {
			return tok, j + 1, true
		}
		if strings.HasPrefix(s[j:], "/>") {
			tok.self = true
			return tok, j + 2, true
		}
		if s[j] == '/' {
			j++
			continue
		}
		if mode.erb && strings.HasPrefix(s[j:], "<%") {
			end := strings.Index(s[j:], "%>")
			if end < 0 {
				return tok, 0, false
			}
			tok.attrs = append(tok.attrs, mattr{name: s[j : j+end+2]})
			j += end + 2
			continue
		}
		k := j
		for k < len(s) && !isSpace(s[k]) && s[k] != '=' && s[k] != '>' && !(s[k] == '/' && k+1 < len(s) && s[k+1] == '>') {
			if mode.erb && strings.HasPrefix(s[k:], "<%") {
				if e := strings.Index(s[k:], "%>"); e >= 0 {
					k += e + 2
					continue
				}
			}
			k++
		}
		a := mattr{name: s[j:k]}
		j = k
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		if j < len(s) && s[j] == '=' {
			j++
			for j < len(s) && isSpace(s[j]) {
				j++
			}
			a.hasValue = true
			if j < len(s) && (s[j] == '"' || s[j] == '\'') {
				q := s[j]
				end := strings.IndexByte(s[j+1:], q)
				if end < 0 {
					return tok, 0, false
				}
				a.quote = q
				a.value = s[j+1 : j+1+end]
				j += end + 2
			} else {
				k := j
				for k < len(s) && !isSpace(s[k]) && s[k] != '>' {
					k++
				}
				a.value = s[j:k]
				j = k
			}
		}
		if a.name != "" {
			tok.attrs = append(tok.attrs, a)
		}
	}
	return tok, 0, false
}

func (a mattr) String() string {
	if !a.hasValue {
		return a.name
	}
	// unquoted {expression} values (JSX, Svelte) are kept as expressions
	if a.quote == 0 && strings.HasPrefix(a.value, "{") && strings.HasSuffix(a.value, "}") {
		return a.name + "=" + a.value
	}
	q := a.quote
	if q == 0 {
		q = '"'
		if strings.Contains(a.value, `"`) {
			q = '\''
		}
	}
	return a.name + "=" + string(q) + a.value + string(q)
}

func (t mtok) openTag() string {
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(t.name)
	for _, a := range t.attrs {
		b.WriteByte(' ')
		b.WriteString(a.String())
	}
	if t.self {
		b.WriteString(" /")
	}
	b.WriteByte('>')
	return b.String()
}

// ---------------------------------------------------------------------------
// Tree

type mnode struct {
	tok      mtok
	children []*mnode
	parent   *mnode
	elem     bool // element with children
	erbBlock bool // virtual node for an ERB block (<% if %> ... <% end %>)
	erbMid   bool // <% else %> style marker rendered at parent depth
	endTag   string
	depth    int
}

// maxMarkupDepth bounds element nesting. The renderers recurse once per
// level, and a stack overflow is fatal in Go (recover can't catch it), so
// millions of nested tags would kill the app. Browsers stop nesting at 512.
const maxMarkupDepth = 1000

var (
	erbKwRe  = regexp.MustCompile(`^<%-?\s*(if|unless|case|while|until|for|begin)\b`)
	erbDoRe  = regexp.MustCompile(`\bdo\b(\s*\|[^|]*\|)?\s*-?%>$`)
	erbEndRe = regexp.MustCompile(`^<%-?\s*end\b`)
	erbMidRe = regexp.MustCompile(`^<%-?\s*(else|elsif|when|in|rescue|ensure)\b`)
	pCloser  = setOf("address", "article", "aside", "blockquote", "div", "dl", "fieldset", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "main", "nav", "ol", "p", "pre", "section", "table", "ul")
)

func buildMarkupTree(toks []mtok, mode markupMode) *mnode {
	root := &mnode{elem: true}
	cur := root
	add := func(n *mnode) {
		n.parent, n.depth = cur, cur.depth+1
		if n.depth > maxMarkupDepth {
			core.Abort(fmt.Errorf("tags are nested deeper than %d levels", maxMarkupDepth))
		}
		cur.children = append(cur.children, n)
	}
	for _, t := range toks {
		switch t.kind {
		case mStart:
			if mode.html {
				// implied end tags for the common sloppy cases
				for {
					cl := cur.tok.lname
					if cur == root || cur.erbBlock {
						break
					}
					if (t.lname == "li" && cl == "li") ||
						(t.lname == "option" && cl == "option") ||
						((t.lname == "td" || t.lname == "th") && (cl == "td" || cl == "th")) ||
						(t.lname == "tr" && (cl == "td" || cl == "th" || cl == "tr")) ||
						((t.lname == "dt" || t.lname == "dd") && (cl == "dt" || cl == "dd")) ||
						(cl == "p" && pCloser[t.lname]) {
						cur = cur.parent
						continue
					}
					break
				}
			}
			n := &mnode{tok: t}
			add(n)
			if !t.self && !(mode.html && htmlVoid[t.lname]) {
				n.elem = true
				cur = n
			}
		case mEnd:
			found := false
			for p := cur; p != nil && p != root; p = p.parent {
				if p.erbBlock {
					break
				}
				if (mode.html && p.tok.lname == t.lname) || (!mode.html && p.tok.name == t.name) {
					for c := cur; c != p; c = c.parent {
						c.endTag = "" // implicitly closed
					}
					p.endTag = "</" + t.name + ">"
					cur = p.parent
					found = true
					break
				}
			}
			if !found && !(mode.html && htmlVoid[t.lname]) {
				add(&mnode{tok: mtok{kind: mText, raw: "</" + t.name + ">"}})
			}
		case mERB:
			switch {
			case erbEndRe.MatchString(t.raw):
				for p := cur; p != root; p = p.parent {
					if p.erbBlock {
						p.endTag = t.raw
						cur = p.parent
						break
					}
				}
			case erbMidRe.MatchString(t.raw) && cur.erbBlock:
				add(&mnode{tok: t, erbMid: true})
			case !strings.HasPrefix(t.raw, "<%#") && (erbDoRe.MatchString(t.raw) || (!strings.HasPrefix(t.raw, "<%=") && erbKwRe.MatchString(t.raw))):
				n := &mnode{tok: t, erbBlock: true, elem: true}
				add(n)
				cur = n
			default:
				add(&mnode{tok: t})
			}
		default:
			add(&mnode{tok: t})
		}
	}
	return root
}

// ---------------------------------------------------------------------------
// Beautify

type mrenderer struct {
	b      strings.Builder
	indent string
	mode   markupMode
	wrap   int
}

func (r *mrenderer) line(depth int, s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	core.CheckOutput(r.b.Len() + len(s))
	r.b.WriteString(strings.Repeat(r.indent, depth))
	r.b.WriteString(s)
	r.b.WriteByte('\n')
}

func (r *mrenderer) isInline(n *mnode) bool {
	if n.erbBlock || n.erbMid {
		return false
	}
	switch n.tok.kind {
	case mText:
		return true
	case mERB:
		return strings.HasPrefix(n.tok.raw, "<%=")
	case mCDATA:
		return !r.mode.html
	case mStart:
		if !r.mode.html || !htmlInline[n.tok.lname] {
			return false // in XML every element gets its own line
		}
		for _, c := range n.children {
			if !r.isInline(c) {
				return false
			}
		}
		return true
	case mRaw:
		return false
	}
	return false
}

func hasElementChild(n *mnode) bool {
	for _, c := range n.children {
		if c.tok.kind == mStart || c.tok.kind == mComment || c.tok.kind == mCDATA {
			return true
		}
		if c.tok.kind == mText && strings.TrimSpace(c.tok.raw) != "" && len(n.children) > 1 {
			return true // mixed content: keep structure
		}
	}
	return false
}

func (r *mrenderer) inlineString(n *mnode) string {
	switch n.tok.kind {
	case mText:
		if r.mode.html {
			return reWS.ReplaceAllString(n.tok.raw, " ")
		}
		return n.tok.raw
	case mERB:
		return n.tok.raw
	case mStart:
		var b strings.Builder
		b.WriteString(n.tok.openTag())
		for _, c := range n.children {
			b.WriteString(r.inlineString(c))
		}
		b.WriteString(r.closeTag(n))
		return b.String()
	}
	return n.tok.raw
}

func (r *mrenderer) closeTag(n *mnode) string {
	if !n.elem {
		return ""
	}
	if n.endTag != "" || n.erbBlock {
		return n.endTag
	}
	// implicitly closed in the source; make it explicit
	if n.tok.kind == mStart {
		return "</" + n.tok.name + ">"
	}
	return ""
}

func (r *mrenderer) openTag(n *mnode, depth int) string {
	s := n.tok.openTag()
	if len(s)+depth*len(r.indent) <= r.wrap || len(n.tok.attrs) < 2 {
		return s
	}
	// wrap long attribute lists, one per line
	pad := "\n" + strings.Repeat(r.indent, depth+1)
	var b strings.Builder
	b.WriteString("<" + n.tok.name)
	for _, a := range n.tok.attrs {
		core.CheckOutput(r.b.Len() + b.Len())
		b.WriteString(pad + a.String())
	}
	if n.tok.self {
		b.WriteString(" /")
	}
	b.WriteString(">")
	return b.String()
}

func (r *mrenderer) renderChildren(n *mnode, depth int) {
	var run strings.Builder
	flush := func() {
		s := strings.TrimSpace(run.String())
		if s != "" {
			if r.mode.html {
				r.line(depth, s)
			} else {
				for _, l := range strings.Split(s, "\n") {
					r.line(depth, strings.TrimSpace(l))
				}
			}
		}
		run.Reset()
	}
	for _, c := range n.children {
		// whitespace with a line break between inline siblings: keep the
		// author's line structure instead of joining everything into one line
		if c.tok.kind == mText && strings.TrimSpace(c.tok.raw) == "" && strings.Contains(c.tok.raw, "\n") {
			flush()
			continue
		}
		if r.isInline(c) {
			run.WriteString(r.inlineString(c))
			continue
		}
		flush()
		r.renderNode(c, depth)
	}
	flush()
}

func (r *mrenderer) renderNode(n *mnode, depth int) {
	switch n.tok.kind {
	case mComment, mDoctype, mPI, mCDATA:
		r.line(depth, strings.TrimSpace(n.tok.raw))
		return
	case mERB:
		if n.erbMid {
			r.line(max(depth-1, 0), n.tok.raw)
			return
		}
		if !n.erbBlock {
			r.line(depth, n.tok.raw)
			return
		}
		r.line(depth, n.tok.raw)
		r.renderChildren(n, depth+1)
		r.line(depth, n.endTag)
		return
	case mRaw:
		r.renderRaw(n, depth)
		return
	case mText:
		r.line(depth, strings.TrimSpace(n.tok.raw))
		return
	}
	open := r.openTag(n, depth)
	closeT := r.closeTag(n)
	if len(n.children) == 0 {
		r.line(depth, open+closeT)
		return
	}
	// raw-text elements: keep their content verbatim (pre/textarea) or
	// format it with the matching beautifier (script/style)
	if len(n.children) == 1 && n.children[0].tok.kind == mRaw {
		raw := n.children[0]
		lname := n.tok.lname
		if lname == "pre" || lname == "textarea" || lname == "title" || lname == "xmp" {
			r.line(depth, open+raw.tok.raw+closeT)
			return
		}
		r.line(depth, open)
		r.renderRaw(raw, depth+1, n)
		r.line(depth, closeT)
		return
	}
	allInline := true
	for _, c := range n.children {
		if !r.isInline(c) {
			allInline = false
			break
		}
	}
	if allInline && !strings.Contains(open, "\n") {
		var b strings.Builder
		for _, c := range n.children {
			b.WriteString(r.inlineString(c))
		}
		content := strings.TrimSpace(b.String())
		if r.mode.html || !strings.Contains(content, "\n") {
			s := open + content + closeT
			if len(s)+depth*len(r.indent) <= r.wrap || !r.mode.html {
				r.line(depth, s)
				return
			}
		}
	}
	r.line(depth, open)
	r.renderChildren(n, depth+1)
	r.line(depth, closeT)
}

func (r *mrenderer) renderRaw(n *mnode, depth int, parent ...*mnode) {
	content := n.tok.raw
	if strings.TrimSpace(content) == "" {
		return
	}
	var formatted string
	var p *mnode
	if len(parent) > 0 {
		p = parent[0]
	}
	if p != nil {
		typ := ""
		for _, a := range p.tok.attrs {
			if strings.EqualFold(a.name, "type") {
				typ = strings.ToLower(a.value)
			}
		}
		switch {
		case p.tok.lname == "style":
			formatted = beautifyCSS(content, r.indent, "css")
		case p.tok.lname == "script" && strings.Contains(typ, "json"):
			if v, _, err := parseJSONInput(content, true); err == nil {
				formatted = encodeOrdered(v, r.indent, false)
			}
		case p.tok.lname == "script" && (typ == "" || strings.Contains(typ, "javascript") || typ == "module"):
			formatted = beautifyJS(content, r.indent)
		}
	}
	if formatted == "" {
		formatted = dedent(content)
	}
	for _, l := range strings.Split(strings.TrimRight(formatted, "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			r.b.WriteByte('\n')
			continue
		}
		r.line(depth, l)
	}
}

// dedent removes common leading whitespace and surrounding blank lines.
func dedent(s string) string {
	lines := strings.Split(strings.Trim(s, "\n\r"), "\n")
	minIndent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent < 0 || n < minIndent {
			minIndent = n
		}
	}
	for i, l := range lines {
		if len(l) >= minIndent && minIndent > 0 {
			lines[i] = l[minIndent:]
		}
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	return strings.Join(lines, "\n")
}

func beautifyMarkup(src, indent string, mode markupMode) string {
	toks := tokenizeMarkup(src, mode)
	tree := buildMarkupTree(toks, mode)
	r := &mrenderer{indent: indent, mode: mode, wrap: 120}
	r.renderChildren(tree, 0)
	out := r.b.String()
	// collapse runs of blank lines
	out = reBlankLines.ReplaceAllString(out, "\n\n")
	return strings.TrimRight(out, "\n") + "\n"
}

// ---------------------------------------------------------------------------
// Minify (used for ERB and XML, and as an HTML fallback)

var htmlBlockish = setOf("html", "head", "body", "div", "p", "ul", "ol", "li", "table", "thead", "tbody", "tfoot", "tr", "td", "th", "section", "article", "header", "footer", "nav", "main", "aside", "h1", "h2", "h3", "h4", "h5", "h6", "form", "fieldset", "meta", "link", "script", "style", "title", "br", "hr", "dl", "dt", "dd", "figure", "figcaption", "blockquote", "option", "select", "noscript", "template")

func minifyMarkup(src string, mode markupMode, keepComments bool) string {
	toks := tokenizeMarkup(src, mode)
	var b strings.Builder
	isBlockTok := func(t mtok) bool {
		switch t.kind {
		case mStart, mEnd:
			return !mode.html || htmlBlockish[t.lname]
		case mComment, mDoctype, mPI, mCDATA:
			return true
		case mERB:
			return !strings.HasPrefix(t.raw, "<%=")
		}
		return false
	}
	for i, t := range toks {
		switch t.kind {
		case mText:
			s := t.raw
			if mode.html {
				s = reWS.ReplaceAllString(s, " ")
			}
			if strings.TrimSpace(s) == "" {
				prevBlock := i == 0 || isBlockTok(toks[i-1])
				nextBlock := i == len(toks)-1 || isBlockTok(toks[i+1])
				if prevBlock || nextBlock || !mode.html {
					continue
				}
				s = " "
			} else if mode.html {
				if i == 0 || isBlockTok(toks[i-1]) {
					s = strings.TrimLeft(s, " ")
				}
				if i == len(toks)-1 || isBlockTok(toks[i+1]) {
					s = strings.TrimRight(s, " ")
				}
			}
			b.WriteString(s)
		case mStart:
			b.WriteString(t.openTag())
		case mEnd:
			b.WriteString("</" + t.name + ">")
		case mComment:
			if keepComments || strings.HasPrefix(t.raw, "<!--[if") || strings.HasPrefix(t.raw, "<!--!") {
				b.WriteString(t.raw)
			}
		case mRaw:
			if t.lname == "pre" || t.lname == "textarea" {
				b.WriteString(t.raw)
			} else {
				b.WriteString(strings.TrimSpace(t.raw))
			}
		default:
			b.WriteString(t.raw)
		}
	}
	return b.String()
}

// Compiled once: detectors run on every paste, tools on every keystroke.
var (
	reBlankLines = regexp.MustCompile(`\n{3,}`)
)
