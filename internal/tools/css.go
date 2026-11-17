package tools

import (
	"bytes"
	"strings"

	"devknife/internal/core"
)

// CSS-family formatter shared by CSS, LESS and SCSS. It is token-based rather
// than a full parser, so it never rejects input: unknown syntax passes through
// with normalised whitespace.

type cssTok struct {
	kind string // text, open, close, semi, comment, lcomment
	val  string
}

func tokenizeCSS(src, dialect string) []cssTok {
	var toks []cssTok
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			toks = append(toks, cssTok{"text", buf.String()})
			buf.Reset()
		}
	}
	parens := 0
	n := len(src)
	for i := 0; i < n; i++ {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			flush()
			if end < 0 {
				toks = append(toks, cssTok{"comment", src[i:]})
				i = n
			} else {
				toks = append(toks, cssTok{"comment", src[i : i+end+4]})
				i += end + 3
			}
		case c == '/' && i+1 < n && src[i+1] == '/' && dialect != "css" && parens == 0 && (i == 0 || src[i-1] != ':'):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = n - i
			}
			flush()
			toks = append(toks, cssTok{"lcomment", strings.TrimRight(src[i:i+end], " \t\r")})
			i += end - 1
		case c == '"' || c == '\'':
			j := i + 1
			for j < n && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= n {
				j = n - 1
			}
			buf.WriteString(src[i : j+1])
			i = j
		case c == '(':
			parens++
			buf.WriteByte(c)
		case c == ')':
			if parens > 0 {
				parens--
			}
			buf.WriteByte(c)
		case c == '{' && i > 0 && (src[i-1] == '#' || src[i-1] == '@'):
			// interpolation #{...} (SCSS) or @{...} (LESS)
			end := strings.IndexByte(src[i:], '}')
			if end < 0 {
				end = n - i - 1
			}
			buf.WriteString(src[i : i+end+1])
			i += end
		case c == '{' && parens == 0:
			flush()
			toks = append(toks, cssTok{"open", "{"})
		case c == '}' && parens == 0:
			flush()
			toks = append(toks, cssTok{"close", "}"})
		case c == ';' && parens == 0:
			flush()
			toks = append(toks, cssTok{"semi", ";"})
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return toks
}

// splitTop splits s on sep, ignoring separators inside (), [] and quotes.
func splitTop(s string, sep byte) []string {
	var parts []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == sep && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func collapseWS(s string) string {
	return strings.TrimSpace(reWS.ReplaceAllString(s, " "))
}

func formatSelector(s string) []string {
	s = collapseWS(s)
	if strings.HasPrefix(s, "@") {
		return []string{s}
	}
	parts := splitTop(s, ',')
	for i, p := range parts {
		p = strings.TrimSpace(p)
		// normalise child combinator spacing outside parens
		p = spaceTopLevel(p, '>')
		parts[i] = p
	}
	return parts
}

func spaceTopLevel(s string, op byte) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		}
		if c == op && depth == 0 {
			out := strings.TrimRight(b.String(), " ")
			b.Reset()
			b.WriteString(out)
			if out != "" {
				b.WriteByte(' ')
			}
			b.WriteByte(op)
			b.WriteByte(' ')
			for i+1 < len(s) && s[i+1] == ' ' {
				i++
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func formatDeclaration(s string) string {
	s = collapseWS(s)
	if s == "" {
		return s
	}
	// leave mixin calls, @include, @extend etc. alone apart from whitespace
	if strings.HasPrefix(s, "@include") || strings.HasPrefix(s, "@extend") || strings.HasPrefix(s, "@import") || strings.HasPrefix(s, "@use") || strings.HasPrefix(s, "@forward") || strings.HasPrefix(s, "@charset") {
		return s
	}
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ':' && depth == 0:
			if i+1 < len(s) && s[i+1] == ':' { // ::pseudo — not a declaration
				return s
			}
			prop := strings.TrimSpace(s[:i])
			val := strings.TrimSpace(s[i+1:])
			if strings.ContainsAny(prop, " >+~") && !strings.HasPrefix(prop, "$") && !strings.HasPrefix(prop, "@") && !strings.HasPrefix(prop, "--") {
				return s // probably a nested selector like "a :hover" in SCSS
			}
			return prop + ": " + val
		}
	}
	return s
}

func beautifyCSS(src, indent, dialect string) string {
	toks := tokenizeCSS(src, dialect)
	var b bytes.Buffer // Truncate makes closing an empty rule O(1)
	depth := 0
	pad := func() string { core.CheckOutput(b.Len()); return strings.Repeat(indent, depth) }
	lastWasClose := false
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case "comment", "lcomment":
			if lastWasClose && depth == 0 {
				b.WriteString("\n")
			}
			body := t.val
			if t.kind == "comment" && strings.Contains(body, "\n") {
				body = dedentComment(body, pad())
			}
			b.WriteString(pad() + body + "\n")
			lastWasClose = false
		case "text":
			txt := strings.TrimSpace(t.val)
			if txt == "" {
				continue
			}
			next := ""
			if i+1 < len(toks) {
				next = toks[i+1].kind
			}
			if lastWasClose && (depth == 0 || next == "open") {
				b.WriteString("\n")
			}
			lastWasClose = false
			switch next {
			case "open":
				sels := formatSelector(txt)
				for k, s := range sels {
					b.WriteString(pad() + s)
					if k < len(sels)-1 {
						b.WriteString(",\n")
					}
				}
				b.WriteString(" {\n")
				depth++
				i++
			case "semi":
				b.WriteString(pad() + formatDeclaration(txt) + ";\n")
				i++
			default:
				// last declaration without a semicolon, or stray text
				if depth > 0 && next == "close" {
					b.WriteString(pad() + formatDeclaration(txt) + ";\n")
				} else {
					b.WriteString(pad() + collapseWS(txt) + "\n")
				}
			}
		case "open":
			b.WriteString(pad() + "{\n")
			depth++
		case "close":
			if depth > 0 {
				depth--
			}
			if bytes.HasSuffix(b.Bytes(), []byte("{\n")) { // empty rule → {}
				b.Truncate(b.Len() - 1)
				b.WriteString("}\n")
			} else {
				b.WriteString(pad() + "}\n")
			}
			lastWasClose = true
		case "semi":
			// stray semicolon
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func dedentComment(c, pad string) string {
	lines := strings.Split(c, "\n")
	for i := 1; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "*") {
			lines[i] = pad + " " + l
		} else {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

// minifyCSSLike is a conservative minifier that understands SCSS/LESS syntax
// (nesting, // comments, interpolation) which strict CSS minifiers reject.
func minifyCSSLike(src, dialect string) string {
	toks := tokenizeCSS(src, dialect)
	var b strings.Builder
	for i, t := range toks {
		switch t.kind {
		case "comment":
			if strings.HasPrefix(t.val, "/*!") {
				b.WriteString(t.val)
			}
		case "lcomment":
		case "text":
			s := collapseWS(t.val)
			if s == "" {
				continue
			}
			next := ""
			if i+1 < len(toks) {
				next = toks[i+1].kind
			}
			if next == "open" {
				s = tightenSelector(s)
			} else {
				s = tightenDecl(s)
			}
			b.WriteString(s)
		case "semi":
			// drop the semicolon right before a closing brace
			j := i + 1
			for j < len(toks) && (toks[j].kind == "lcomment" || (toks[j].kind == "comment" && !strings.HasPrefix(toks[j].val, "/*!")) || (toks[j].kind == "text" && strings.TrimSpace(toks[j].val) == "")) {
				j++
			}
			if j < len(toks) && toks[j].kind == "close" {
				continue
			}
			b.WriteByte(';')
		default:
			b.WriteString(t.val)
		}
	}
	return b.String()
}

func tightenSelector(s string) string {
	parts := splitTop(s, ',')
	for i, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.ReplaceAll(p, " > ", ">")
		p = strings.ReplaceAll(p, " + ", "+")
		p = strings.ReplaceAll(p, " ~ ", "~")
		parts[i] = p
	}
	return strings.Join(parts, ",")
}

func tightenDecl(s string) string {
	d := formatDeclaration(s)
	if idx := strings.Index(d, ": "); idx > 0 && !strings.HasPrefix(d, "@include") {
		d = d[:idx] + ":" + d[idx+2:]
	}
	// spaces after commas inside values are safe to drop outside strings
	var b strings.Builder
	var quote byte
	for i := 0; i < len(d); i++ {
		c := d[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
		}
		if c == ' ' && i > 0 && d[i-1] == ',' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
