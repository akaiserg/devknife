package tools

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sergi/go-diff/diffmatchpatch"

	"devknife/internal/core"
)

// ---------------------------------------------------------------------------
// Text diff

type diffOp struct {
	Op   int    `json:"op"` // -1 delete, 0 equal, 1 insert
	Text string `json:"text"`
}

// tokensToRunes maps each distinct token to a rune so diffmatchpatch can
// diff at word or line granularity.
func tokensToRunes(a, b []string) ([]rune, []rune, []string) {
	idx := map[string]rune{}
	var table []string
	enc := func(toks []string) []rune {
		out := make([]rune, len(toks))
		for i, t := range toks {
			r, ok := idx[t]
			if !ok {
				r = rune(len(table) + 0xE000) // private use area → valid runes
				if r >= 0xF8FF {
					r = rune(len(table)) + 0xF0000
				}
				idx[t] = r
				table = append(table, t)
			}
			out[i] = r
		}
		return out
	}
	return enc(a), enc(b), table
}

func decodeRunes(r []rune, table []string) string {
	var b strings.Builder
	for _, x := range r {
		i := int(x - 0xE000)
		if x >= 0xF0000 {
			i = int(x - 0xF0000)
		}
		b.WriteString(table[i])
	}
	return b.String()
}

var reWordTok = regexp.MustCompile(`\w+|\s+|[^\w\s]`)

func splitLinesKeep(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

func computeDiff(a, b, mode string) []diffOp {
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = 5 * time.Second
	var diffs []diffmatchpatch.Diff
	switch mode {
	case "chars":
		diffs = dmp.DiffMain(a, b, false)
		diffs = dmp.DiffCleanupSemantic(diffs)
	default:
		var ta, tb []string
		if mode == "words" {
			ta, tb = reWordTok.FindAllString(a, -1), reWordTok.FindAllString(b, -1)
		} else {
			ta, tb = splitLinesKeep(a), splitLinesKeep(b)
		}
		ra, rb, table := tokensToRunes(ta, tb)
		for _, d := range dmp.DiffMainRunes(ra, rb, false) {
			diffs = append(diffs, diffmatchpatch.Diff{Type: d.Type, Text: decodeRunes([]rune(d.Text), table)})
		}
		if mode == "words" {
			diffs = dmp.DiffCleanupSemantic(diffs)
		}
	}
	ops := make([]diffOp, 0, len(diffs))
	for _, d := range diffs {
		op := 0
		switch d.Type {
		case diffmatchpatch.DiffDelete:
			op = -1
		case diffmatchpatch.DiffInsert:
			op = 1
		}
		ops = append(ops, diffOp{Op: op, Text: d.Text})
	}
	return ops
}

type udLine struct {
	op   byte // ' ', '-', '+'
	text string
	a, b int // 1-based line numbers
}

func unifiedDiff(ops []diffOp, ctx int, nameA, nameB string) string {
	var lines []udLine
	la, lb := 1, 1
	for _, o := range ops {
		for _, l := range splitLinesKeep(o.Text) {
			switch o.Op {
			case 0:
				lines = append(lines, udLine{' ', l, la, lb})
				la++
				lb++
			case -1:
				lines = append(lines, udLine{'-', l, la, lb})
				la++
			case 1:
				lines = append(lines, udLine{'+', l, la, lb})
				lb++
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", nameA, nameB)
	i := 0
	for i < len(lines) {
		if lines[i].op == ' ' {
			i++
			continue
		}
		start := max(0, i-ctx)
		end := i
		for end < len(lines) {
			if lines[end].op != ' ' {
				end++
				continue
			}
			// run of context: does another change follow within 2*ctx?
			j := end
			for j < len(lines) && lines[j].op == ' ' && j-end < 2*ctx {
				j++
			}
			if j < len(lines) && lines[j].op != ' ' {
				end = j
				continue
			}
			end = min(len(lines), end+ctx)
			break
		}
		h := lines[start:end]
		ca, cb := 0, 0
		for _, l := range h {
			if l.op != '+' {
				ca++
			}
			if l.op != '-' {
				cb++
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h[0].a, ca, h[0].b, cb)
		for _, l := range h {
			b.WriteByte(l.op)
			b.WriteString(l.text)
			if !strings.HasSuffix(l.text, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
		i = end
	}
	return b.String()
}

func diffTool() *core.Tool {
	return &core.Tool{
		ID:          "diff",
		Name:        "Text Diff Checker",
		Category:    core.CatInspect,
		Description: "Compare two texts by character, word or line. Shows inline highlights, side-by-side view, a unified diff (git-style) and change statistics.",
		Aliases:     []string{"compare", "textdiff"},
		Keywords:    []string{"difference", "compare", "patch", "unified", "changes"},
		Args:        []string{"@input", "@other"},
		Placeholder: "Original text",
		Options: []core.Option{
			{Key: "other", Label: "Changed text", Type: core.OptText, Default: "", Placeholder: "Changed text"},
			{Key: "mode", Label: "Diff by", Type: core.OptSelect, Default: "lines", Choices: []string{"lines", "words", "chars"}, Short: "m"},
			{Key: "ignore_ws", Label: "Ignore whitespace", Type: core.OptBool, Default: "false"},
			{Key: "ignore_case", Label: "Ignore case", Type: core.OptBool, Default: "false"},
			{Key: "context", Label: "Context lines", Type: core.OptNumber, Default: "3"},
		},
		Sample: "if (param == ISCSI_PARAM_LOCAL_PORT)\n    rc = kernel_getsockname(tcp_sw_conn->sock,\n                (struct sockaddr *)&addr);\nelse\n    rc = kernel_getpeername(tcp_sw_conn->sock,\n                (struct sockaddr *)&addr);\nspin_unlock_bh(&conn->session->frwd_lock);\nif (rc < 0)\n    return rc;\n",
		Templates: []core.Template{
			{Name: "Sample comparison", Input: "if (param == ISCSI_PARAM_LOCAL_PORT)\n    rc = kernel_getsockname(tcp_sw_conn->sock,\n                (struct sockaddr *)&addr);\nelse\n    rc = kernel_getpeername(tcp_sw_conn->sock,\n                (struct sockaddr *)&addr);\nspin_unlock_bh(&conn->session->frwd_lock);\nif (rc < 0)\n    return rc;\n",
				Opts: map[string]string{"other": "sock = tcp_sw_conn->sock;\nsock_hold(sock->sk);\nspin_unlock_bh(&conn->session->frwd_lock);\n\nif (param == ISCSI_PARAM_LOCAL_PORT)\n    rc = kernel_getsockname(sock,\n                (struct sockaddr *)&addr);\nelse\n    rc2 = kernel_getpeername(sock,\n                (struct sockaddr *)&addr);\nsock_put(sock->sk);\nif (rc < 0)\n    return rc;\n"}},
		},
		Run: func(r core.Request) (*core.Result, error) {
			a, b := r.Input, r.Str("other")
			norm := func(s string) string {
				if r.Bool("ignore_case") {
					s = strings.ToLower(s)
				}
				if r.Bool("ignore_ws") {
					lines := strings.Split(s, "\n")
					for i, l := range lines {
						lines[i] = strings.Join(strings.Fields(l), " ")
					}
					s = strings.Join(lines, "\n")
				}
				return strings.ReplaceAll(s, "\r\n", "\n")
			}
			na, nb := norm(a), norm(b)
			mode := r.Str("mode")
			ops := computeDiff(na, nb, mode)
			lineOps := ops
			if mode != "lines" {
				lineOps = computeDiff(na, nb, "lines")
			}
			adds, dels := 0, 0
			unit := map[string]string{"lines": "line", "words": "word", "chars": "char"}[mode]
			countUnits := func(s string) int {
				switch mode {
				case "lines":
					return len(splitLinesKeep(s))
				case "words":
					return len(strings.Fields(s))
				}
				return utf8.RuneCountInString(s)
			}
			same := 0
			for _, o := range ops {
				switch o.Op {
				case 1:
					adds += countUnits(o.Text)
				case -1:
					dels += countUnits(o.Text)
				default:
					same += utf8.RuneCountInString(o.Text)
				}
			}
			total := max(utf8.RuneCountInString(na), utf8.RuneCountInString(nb))
			sim := 100.0
			if total > 0 {
				sim = 100 * float64(same) / float64(total)
			}
			res := &core.Result{View: "diff", Data: map[string]any{"ops": ops, "mode": mode}}
			if na == nb {
				res.Output = "No differences."
			} else if mode == "lines" {
				res.Output, res.Lang = unifiedDiff(lineOps, max(0, r.Int("context", 3)), "a", "b"), "diff"
			} else {
				var sb strings.Builder
				for _, o := range ops {
					switch o.Op {
					case -1:
						sb.WriteString("[-" + o.Text + "-]")
					case 1:
						sb.WriteString("{+" + o.Text + "+}")
					default:
						sb.WriteString(o.Text)
					}
				}
				res.Output = sb.String()
			}
			changes := 0
			for _, o := range ops {
				if o.Op != 0 {
					changes++
				}
			}
			res.Fields = fields("Additions", plural(adds, unit), "Deletions", plural(dels, unit), "Changes", strconv.Itoa(changes), "Similarity", fmt.Sprintf("%.1f%%", sim))
			return res, nil
		},
	}
}

// ---------------------------------------------------------------------------
// String inspector

var invisibleNames = map[rune]string{
	0x00A0: "NO-BREAK SPACE", 0x00AD: "SOFT HYPHEN", 0x034F: "COMBINING GRAPHEME JOINER", 0x061C: "ARABIC LETTER MARK", 0x115F: "HANGUL CHOSEONG FILLER",
	0x180E: "MONGOLIAN VOWEL SEPARATOR", 0x2000: "EN QUAD", 0x2001: "EM QUAD", 0x2002: "EN SPACE", 0x2003: "EM SPACE", 0x2004: "THREE-PER-EM SPACE",
	0x2005: "FOUR-PER-EM SPACE", 0x2006: "SIX-PER-EM SPACE", 0x2007: "FIGURE SPACE", 0x2008: "PUNCTUATION SPACE", 0x2009: "THIN SPACE", 0x200A: "HAIR SPACE",
	0x200B: "ZERO WIDTH SPACE", 0x200C: "ZERO WIDTH NON-JOINER", 0x200D: "ZERO WIDTH JOINER", 0x200E: "LEFT-TO-RIGHT MARK", 0x200F: "RIGHT-TO-LEFT MARK",
	0x2028: "LINE SEPARATOR", 0x2029: "PARAGRAPH SEPARATOR", 0x202A: "LEFT-TO-RIGHT EMBEDDING", 0x202B: "RIGHT-TO-LEFT EMBEDDING", 0x202C: "POP DIRECTIONAL FORMATTING",
	0x202D: "LEFT-TO-RIGHT OVERRIDE", 0x202E: "RIGHT-TO-LEFT OVERRIDE", 0x202F: "NARROW NO-BREAK SPACE", 0x205F: "MEDIUM MATHEMATICAL SPACE", 0x2060: "WORD JOINER",
	0x2066: "LEFT-TO-RIGHT ISOLATE", 0x2067: "RIGHT-TO-LEFT ISOLATE", 0x2068: "FIRST STRONG ISOLATE", 0x2069: "POP DIRECTIONAL ISOLATE", 0x3000: "IDEOGRAPHIC SPACE",
	0xFE0E: "VARIATION SELECTOR-15", 0xFE0F: "VARIATION SELECTOR-16", 0xFEFF: "ZERO WIDTH NO-BREAK SPACE (BOM)", 0xFFFC: "OBJECT REPLACEMENT CHARACTER", 0xFFFD: "REPLACEMENT CHARACTER",
	'\t': "TAB", '\r': "CARRIAGE RETURN", 0x0B: "VERTICAL TAB", 0x0C: "FORM FEED", 0x00: "NULL", 0x1B: "ESCAPE",
}

// graphemeCount approximates user-perceived characters: combining marks,
// ZWJ sequences, variation selectors, skin tones and flag pairs collapse.
func graphemeCount(s string) int {
	n := 0
	prevZWJ := false
	riPending := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0xFE0F || r == 0xFE0E || (r >= 0x1F3FB && r <= 0x1F3FF) || (r >= 0xE0020 && r <= 0xE007F):
			// extends previous
		case r == 0x200D:
			prevZWJ = true
			continue
		case prevZWJ:
		case r >= 0x1F1E6 && r <= 0x1F1FF: // regional indicators pair up into flags
			if riPending {
				riPending = false
			} else {
				riPending = true
				n++
			}
		case r == '\n' && n > 0 && false:
		default:
			n++
			riPending = false
		}
		prevZWJ = false
	}
	return n
}

func categoryOf(r rune) string {
	for _, c := range []struct {
		name string
		t    *unicode.RangeTable
	}{{"Lu", unicode.Lu}, {"Ll", unicode.Ll}, {"Lt", unicode.Lt}, {"Lm", unicode.Lm}, {"Lo", unicode.Lo}, {"Mn", unicode.Mn}, {"Mc", unicode.Mc}, {"Me", unicode.Me},
		{"Nd", unicode.Nd}, {"Nl", unicode.Nl}, {"No", unicode.No}, {"Pc", unicode.Pc}, {"Pd", unicode.Pd}, {"Ps", unicode.Ps}, {"Pe", unicode.Pe}, {"Pi", unicode.Pi},
		{"Pf", unicode.Pf}, {"Po", unicode.Po}, {"Sm", unicode.Sm}, {"Sc", unicode.Sc}, {"Sk", unicode.Sk}, {"So", unicode.So}, {"Zs", unicode.Zs}, {"Zl", unicode.Zl},
		{"Zp", unicode.Zp}, {"Cc", unicode.Cc}, {"Cf", unicode.Cf}, {"Co", unicode.Co}} {
		if unicode.Is(c.t, r) {
			return c.name
		}
	}
	return "Cn"
}

func scriptOf(r rune) string {
	for _, name := range []string{"Latin", "Greek", "Cyrillic", "Armenian", "Hebrew", "Arabic", "Devanagari", "Thai", "Hangul", "Hiragana", "Katakana", "Han", "Georgian", "Ethiopic", "Cherokee"} {
		if unicode.Is(unicode.Scripts[name], r) {
			return name
		}
	}
	return ""
}

func stringInspectorTool() *core.Tool {
	return &core.Tool{
		ID:          "inspect",
		Name:        "String Inspector",
		Category:    core.CatInspect,
		Description: "Count characters (graphemes, code points, UTF-16 units, bytes), words, lines and sentences; find invisible or look-alike characters; see word frequency or a per-character Unicode breakdown.",
		Aliases:     []string{"string-inspector", "count", "wc", "chars"},
		Keywords:    []string{"length", "unicode", "count", "invisible", "zero width", "homoglyph", "bytes", "emoji"},
		Options: []core.Option{
			{Key: "show", Label: "Show", Type: core.OptSelect, Default: "words", Choices: []string{"words", "chars", "unicode"}},
			{Key: "case_sensitive", Label: "Case sensitive", Type: core.OptBool, Default: "false"},
		},
		Sample: "This is a special emoji 👨‍👩‍👧.\nIt is made of several code points joined by ZWJ.\nWatch out for invisible​characters and the non breaking space — they break code!",
		Detect: func(in string) *core.Detection {
			for _, r := range in {
				if _, ok := invisibleNames[r]; ok && r != '\t' && r != '\r' {
					return det(65, "Contains invisible characters")
				}
			}
			return det(10, "Text")
		},
		Run: func(r core.Request) (*core.Result, error) {
			s := r.Input
			words := strings.FieldsFunc(s, func(c rune) bool { return !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '\'' && c != '_' })
			lines := 0
			if s != "" {
				lines = strings.Count(s, "\n") + 1
			}
			sentences := len(reSentence.FindAllString(s, -1))
			paragraphs := 0
			for _, p := range reParaBreak.Split(strings.TrimSpace(s), -1) {
				if strings.TrimSpace(p) != "" {
					paragraphs++
				}
			}
			ws, nonASCII, upper, lower, digits, punct := 0, 0, 0, 0, 0, 0
			type inv struct {
				r         rune
				line, col int
			}
			var invisibles []inv
			line, col := 1, 0
			scripts := map[string]bool{}
			for _, c := range s {
				col++
				if c == '\n' {
					line, col = line+1, 0
				}
				switch {
				case unicode.IsSpace(c):
					ws++
				case unicode.IsUpper(c):
					upper++
				case unicode.IsLower(c):
					lower++
				case unicode.IsDigit(c):
					digits++
				case unicode.IsPunct(c):
					punct++
				}
				if c > 127 {
					nonASCII++
				}
				if name, ok := invisibleNames[c]; ok && c != '\t' && c != '\r' {
					_ = name
					invisibles = append(invisibles, inv{c, line, col})
				}
				if unicode.IsLetter(c) {
					if sc := scriptOf(c); sc != "" {
						scripts[sc] = true
					}
				}
			}
			res := &core.Result{}
			res.Fields = fields(
				"Characters", strconv.Itoa(graphemeCount(s)),
				"Code points", strconv.Itoa(utf8.RuneCountInString(s)),
				"UTF-16 units (JS length)", strconv.Itoa(utf16Len(s)),
				"Bytes (UTF-8)", strconv.Itoa(len(s)),
				"Words", strconv.Itoa(len(words)),
				"Lines", strconv.Itoa(lines),
				"Sentences", strconv.Itoa(sentences),
				"Paragraphs", strconv.Itoa(paragraphs),
				"Whitespace", strconv.Itoa(ws),
				"Upper / lower / digits", fmt.Sprintf("%d / %d / %d", upper, lower, digits),
				"Punctuation", strconv.Itoa(punct),
				"Non-ASCII", strconv.Itoa(nonASCII),
			)
			if len(scripts) > 1 {
				var sc []string
				for k := range scripts {
					sc = append(sc, k)
				}
				sort.Strings(sc)
				res.Fields = append(res.Fields, core.Field{Label: "Scripts", Value: strings.Join(sc, ", ")})
				if scripts["Latin"] && (scripts["Cyrillic"] || scripts["Greek"]) {
					res.Warnings = append(res.Warnings, "Mixed Latin and Cyrillic/Greek letters — possible look-alike (homoglyph) characters.")
				}
			}
			if len(invisibles) > 0 {
				var desc []string
				for i, iv := range invisibles {
					if i == 10 {
						desc = append(desc, fmt.Sprintf("… and %d more", len(invisibles)-10))
						break
					}
					desc = append(desc, fmt.Sprintf("U+%04X %s (line %d, col %d)", iv.r, invisibleNames[iv.r], iv.line, iv.col))
				}
				res.Fields = append(res.Fields, core.Field{Label: "Invisible characters", Value: strings.Join(desc, "; ")})
				res.Warnings = append(res.Warnings, fmt.Sprintf("Found %s.", plural(len(invisibles), "invisible character")))
			}
			cs := r.Bool("case_sensitive")
			switch r.Str("show") {
			case "chars":
				counts := map[string]int{}
				for _, c := range s {
					k := string(c)
					if !cs {
						k = strings.ToLower(k)
					}
					counts[k]++
				}
				res.Output = freqTable(counts, func(k string) string {
					c, _ := utf8.DecodeRuneInString(k)
					if n, ok := invisibleNames[c]; ok {
						return fmt.Sprintf("U+%04X %s", c, n)
					}
					if c == '\n' {
						return "⏎ (newline)"
					}
					if c == ' ' {
						return "␠ (space)"
					}
					return k
				})
			case "unicode":
				var b strings.Builder
				i := 0
				for off, c := range s {
					if i == 2000 {
						b.WriteString("… (truncated)\n")
						break
					}
					i++
					shown := string(c)
					if n, ok := invisibleNames[c]; ok {
						shown = "<" + n + ">"
					} else if c == '\n' {
						shown = "⏎"
					} else if c == ' ' {
						shown = "␠"
					}
					enc := make([]byte, utf8.RuneLen(c))
					utf8.EncodeRune(enc, c)
					hexb := make([]string, len(enc))
					for j, x := range enc {
						hexb[j] = fmt.Sprintf("%02X", x)
					}
					fmt.Fprintf(&b, "%-6d %-10s U+%04X  %s  UTF-8: %s\n", off, shown, c, categoryOf(c), strings.Join(hexb, " "))
				}
				res.Output = b.String()
			default:
				counts := map[string]int{}
				for _, w := range words {
					if !cs {
						w = strings.ToLower(w)
					}
					counts[w]++
				}
				res.Output = freqTable(counts, func(k string) string { return k })
				res.Fields = append(res.Fields, core.Field{Label: "Unique words", Value: strconv.Itoa(len(counts))})
			}
			return res, nil
		},
	}
}

func freqTable(counts map[string]int, label func(string) string) string {
	type kv struct {
		k string
		n int
	}
	var list []kv
	for k, n := range counts {
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].k < list[j].k
	})
	var b strings.Builder
	for _, e := range list {
		fmt.Fprintf(&b, "%s: %d\n", label(e.k), e.n)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Cron

type cronField struct {
	raw    string
	values []int
	all    bool // "*" or "?": every value
	// star: the field starts with "*" ("*", "*/2"). Vixie cron and cronie
	// combine day-of-month and day-of-week with OR only when neither starts
	// with "*"; otherwise both must match.
	star bool
}

var monthNames = []string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
var dowNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func parseCronField(raw string, lo, hi int, names []string) (cronField, error) {
	f := cronField{raw: raw}
	if raw == "*" || raw == "?" {
		f.all = true
	}
	f.star = strings.HasPrefix(raw, "*") || raw == "?"
	set := map[int]bool{}
	lookup := func(s string) (int, error) {
		if n, err := strconv.Atoi(s); err == nil {
			return n, nil
		}
		for i, nm := range names {
			if nm != "" && strings.EqualFold(s, nm[:3]) {
				return i, nil
			}
		}
		return 0, fmt.Errorf("invalid value %q", s)
	}
	for _, part := range strings.Split(raw, ",") {
		step := 1
		if a, b, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(b)
			if err != nil || n <= 0 {
				return f, fmt.Errorf("invalid step in %q", part)
			}
			step, part = n, a
		}
		start, end := lo, hi
		switch {
		case part == "*" || part == "?":
		case strings.ContainsAny(part, "LW#"):
			return f, fmt.Errorf("%q uses Quartz-only syntax (L, W, #), not supported by standard cron", part)
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			var err error
			if start, err = lookup(a); err != nil {
				return f, err
			}
			if end, err = lookup(b); err != nil {
				return f, err
			}
		default:
			n, err := lookup(part)
			if err != nil {
				return f, err
			}
			start = n
			end = n
			if step > 1 { // "5/15" means from 5 to max every 15
				end = hi
			}
		}
		if start < lo || end > hi || start > end {
			return f, fmt.Errorf("value out of range in %q (allowed %d-%d)", part, lo, hi)
		}
		for v := start; v <= end; v += step {
			set[v] = true
		}
	}
	for v := range set {
		f.values = append(f.values, v)
	}
	sort.Ints(f.values)
	return f, nil
}

type cronSpec struct {
	sec, min, hour, dom, mon, dow cronField
	hasSec                        bool
}

var cronMacros = map[string]string{"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *", "@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *"}

func parseCron(expr string) (*cronSpec, error) {
	e := strings.TrimSpace(expr)
	if e == "@reboot" {
		return nil, fmt.Errorf("@reboot runs once at startup; it has no schedule")
	}
	if m, ok := cronMacros[strings.ToLower(e)]; ok {
		e = m
	}
	if strings.HasPrefix(e, "@every ") {
		return nil, fmt.Errorf("@every is an interval, not a cron schedule (try: */5 * * * *)")
	}
	parts := strings.Fields(e)
	spec := &cronSpec{}
	switch len(parts) {
	case 5:
		parts = append([]string{"0"}, parts...)
	case 6:
		spec.hasSec = true
	case 7:
		spec.hasSec = true
		parts = parts[:6] // Quartz year field ignored
	default:
		return nil, fmt.Errorf("expected 5 fields (minute hour day-of-month month day-of-week), got %d", len(parts))
	}
	var err error
	if spec.sec, err = parseCronField(parts[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("seconds: %v", err)
	}
	if spec.min, err = parseCronField(parts[1], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %v", err)
	}
	if spec.hour, err = parseCronField(parts[2], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %v", err)
	}
	if spec.dom, err = parseCronField(parts[3], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day of month: %v", err)
	}
	if spec.mon, err = parseCronField(parts[4], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %v", err)
	}
	if spec.dow, err = parseCronField(parts[5], 0, 7, dowNames); err != nil {
		return nil, fmt.Errorf("day of week: %v", err)
	}
	// 7 == Sunday; "0,7" must not list Sunday twice
	seen := map[int]bool{}
	dows := spec.dow.values[:0]
	for _, v := range spec.dow.values {
		if v == 7 {
			v = 0
		}
		if !seen[v] {
			seen[v] = true
			dows = append(dows, v)
		}
	}
	sort.Ints(dows)
	spec.dow.values = dows
	return spec, nil
}

func contains(xs []int, v int) bool {
	i := sort.SearchInts(xs, v)
	return i < len(xs) && xs[i] == v
}

// dayMatches applies cron's day rule: when both day fields are restricted
// (neither starts with "*"), a day matching either one fires; otherwise both
// must match. So "0 0 */2 * 1" is odd-numbered days that are Mondays.
func (c *cronSpec) dayMatches(t time.Time) bool {
	domOK := contains(c.dom.values, t.Day())
	dowOK := contains(c.dow.values, int(t.Weekday()))
	if c.dom.star || c.dow.star {
		return domOK && dowOK
	}
	return domOK || dowOK
}

func (c *cronSpec) next(from time.Time, n int) []time.Time {
	var out []time.Time
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	for d := 0; d < 366*5 && len(out) < n; d++ {
		t := day.AddDate(0, 0, d)
		if !contains(c.mon.values, int(t.Month())) {
			continue
		}
		if !c.dayMatches(t) {
			continue
		}
		for _, h := range c.hour.values {
			for _, m := range c.min.values {
				for _, s := range c.sec.values {
					cand := time.Date(t.Year(), t.Month(), t.Day(), h, m, s, 0, t.Location())
					if cand.Hour() != h { // skipped by DST
						continue
					}
					if cand.After(from) {
						out = append(out, cand)
						if len(out) == n {
							return out
						}
					}
				}
			}
		}
	}
	return out
}

func ordinal(n int) string {
	suf := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suf = "st"
		case 2:
			suf = "nd"
		case 3:
			suf = "rd"
		}
	}
	return strconv.Itoa(n) + suf
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

func describeList(f cronField, unit string, name func(int) string) string {
	raw := f.raw
	if a, b, ok := strings.Cut(raw, "/"); ok && !strings.Contains(raw, ",") {
		s := fmt.Sprintf("every %d %ss", mustAtoi(b), unit)
		if unit == "day of the week" {
			s = "every " + ordinal(mustAtoi(b)) + " " + unit
		}
		if mustAtoi(b) == 1 {
			s = "every " + unit
		}
		if a != "*" && a != "?" {
			if x, y, isRange := strings.Cut(a, "-"); isRange {
				s += " from " + name(lookupName(x, f)) + " through " + name(lookupName(y, f))
			} else {
				s += " starting at " + name(lookupName(a, f))
			}
		}
		return s
	}
	if x, y, ok := strings.Cut(raw, "-"); ok && !strings.Contains(raw, ",") {
		return name(lookupName(x, f)) + " through " + name(lookupName(y, f))
	}
	var names []string
	for _, v := range f.values {
		names = append(names, name(v))
	}
	return joinAnd(names)
}

func lookupName(s string, f cronField) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	for i, nm := range monthNames {
		if nm != "" && strings.EqualFold(s, nm[:3]) {
			if f.values != nil && f.values[len(f.values)-1] <= 12 && len(f.values) > 0 && f.values[0] >= 1 {
				return i
			}
		}
	}
	for i, nm := range dowNames[:7] {
		if strings.EqualFold(s, nm[:3]) {
			return i
		}
	}
	return 0
}

func mustAtoi(s string) int { n, _ := strconv.Atoi(s); return n }

func describeCron(c *cronSpec) string {
	var parts []string
	single := func(f cronField) bool { return len(f.values) == 1 && !strings.ContainsAny(f.raw, "/-*") }
	pad2 := func(v int) string { return fmt.Sprintf("%02d", v) }
	secOK := !c.hasSec || single(c.sec)
	switch {
	case secOK && single(c.min) && !strings.ContainsAny(c.hour.raw, "*/") && len(c.hour.values) <= 6:
		var times []string
		for _, h := range c.hour.values {
			t := pad2(h) + ":" + pad2(c.min.values[0])
			if c.hasSec && c.sec.values[0] != 0 {
				t += ":" + pad2(c.sec.values[0])
			}
			times = append(times, t)
		}
		if strings.Contains(c.hour.raw, "-") {
			parts = append(parts, fmt.Sprintf("At minute %d past every hour from %s through %s", c.min.values[0], pad2(c.hour.values[0])+":00", pad2(c.hour.values[len(c.hour.values)-1])+":00"))
		} else {
			parts = append(parts, "At "+joinAnd(times))
		}
	default:
		if c.hasSec && !c.sec.all {
			parts = append(parts, "At "+describeList(c.sec, "second", func(v int) string { return "second " + strconv.Itoa(v) }))
		} else if c.hasSec {
			parts = append(parts, "Every second")
		}
		switch {
		case c.min.all && !c.hasSec:
			parts = append(parts, "Every minute")
		case c.min.all:
		case strings.Contains(c.min.raw, "/"):
			parts = append(parts, capFirst(describeList(c.min, "minute", strconv.Itoa)))
		case single(c.min):
			parts = append(parts, fmt.Sprintf("At minute %d", c.min.values[0]))
		default:
			parts = append(parts, "At minutes "+describeList(c.min, "minute", strconv.Itoa))
		}
		switch {
		case c.hour.all:
			if single(c.min) {
				parts[len(parts)-1] += " past every hour"
			}
		case strings.Contains(c.hour.raw, "/"):
			parts = append(parts, "past "+describeList(c.hour, "hour", func(v int) string { return pad2(v) + ":00" }))
		case strings.Contains(c.hour.raw, "-") && !strings.Contains(c.hour.raw, ","):
			parts = append(parts, fmt.Sprintf("between %s and %s", pad2(c.hour.values[0])+":00", pad2(c.hour.values[len(c.hour.values)-1])+":59"))
		default:
			var hs []string
			for _, h := range c.hour.values {
				hs = append(hs, pad2(h)+":00")
			}
			parts = append(parts, "during the hours "+joinAnd(hs))
		}
	}
	if !c.dom.all {
		switch {
		case strings.Contains(c.dom.raw, "/"):
			parts = append(parts, describeList(c.dom, "day", ordinal))
		case strings.Contains(c.dom.raw, "-") && !strings.Contains(c.dom.raw, ","):
			parts = append(parts, fmt.Sprintf("between day %d and %d of the month", c.dom.values[0], c.dom.values[len(c.dom.values)-1]))
		default:
			parts = append(parts, "on day "+describeList(c.dom, "day", strconv.Itoa)+" of the month")
		}
	}
	if !c.dow.all {
		prefix := "only on "
		if !c.dom.star && !c.dow.star {
			prefix = "or on " // cron's OR rule: either day field matches
		}
		if strings.Contains(c.dow.raw, "-") && !strings.Contains(c.dow.raw, ",") && !strings.Contains(c.dow.raw, "/") {
			parts = append(parts, describeList(c.dow, "day", func(v int) string { return dowNames[v] }))
		} else {
			parts = append(parts, prefix+describeList(c.dow, "day of the week", func(v int) string { return dowNames[v] }))
		}
	}
	if !c.mon.all {
		if strings.Contains(c.mon.raw, "/") || (strings.Contains(c.mon.raw, "-") && !strings.Contains(c.mon.raw, ",")) {
			parts = append(parts, describeList(c.mon, "month", func(v int) string { return monthNames[v] }))
		} else {
			parts = append(parts, "only in "+describeList(c.mon, "month", func(v int) string { return monthNames[v] }))
		}
	}
	return strings.Join(parts, ", ")
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

var reCronChars = regexp.MustCompile(`^[\d*/,\-?A-Za-z]+$`)

func cronTool() *core.Tool {
	return &core.Tool{
		ID:          "cron",
		Name:        "Cron Job Parser",
		Category:    core.CatInspect,
		Description: "Explain a cron expression in plain English, expand every field, and list the next run times in any time zone. Supports 5/6-field syntax, names (MON, JAN) and macros (@daily).",
		Aliases:     []string{"crontab", "cron-parser", "schedule"},
		Keywords:    []string{"crontab", "schedule", "job", "next run"},
		Options: []core.Option{
			{Key: "count", Label: "Next runs", Type: core.OptNumber, Default: "5", Short: "n"},
			{Key: "tz", Label: "Time zone", Type: core.OptString, Default: "local", Placeholder: "local, UTC, Europe/Paris…", Short: "z"},
		},
		Sample: "*/15 9-17 * * MON-FRI",
		Templates: []core.Template{
			{Name: "Every minute", Input: "* * * * *"},
			{Name: "Every 5 minutes", Input: "*/5 * * * *"},
			{Name: "Every hour", Input: "0 * * * *"},
			{Name: "Every day at midnight", Input: "0 0 * * *"},
			{Name: "Every weekday at 09:00", Input: "0 9 * * 1-5"},
			{Name: "Every 15 min during business hours", Input: "*/15 9-17 * * MON-FRI"},
			{Name: "Twice a day (00:00 and 12:00)", Input: "0 0,12 * * *"},
			{Name: "Every Sunday at 02:30", Input: "30 2 * * 0"},
			{Name: "First day of every month", Input: "0 0 1 * *"},
			{Name: "Every quarter", Input: "0 0 1 */3 *"},
			{Name: "Every 30 seconds (6 fields)", Input: "*/30 * * * * *"},
		},
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if !isSingleLine(t) {
				return nil
			}
			if _, ok := cronMacros[strings.ToLower(t)]; ok {
				return det(90, "Cron macro")
			}
			fs := strings.Fields(t)
			if (len(fs) == 5 || len(fs) == 6) && reCronChars.MatchString(strings.Join(fs, "")) && strings.ContainsAny(t, "*/") {
				if _, err := parseCron(t); err == nil {
					return det(92, "Cron expression")
				}
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			c, err := parseCron(r.Input)
			if err != nil {
				return nil, err
			}
			loc, err := loadTZ(r.Str("tz"))
			if err != nil {
				return nil, err
			}
			desc := describeCron(c)
			show := func(f cronField, fmtv func(int) string) string {
				if f.all {
					return "(all)"
				}
				var xs []string
				for _, v := range f.values {
					xs = append(xs, fmtv(v))
				}
				return strings.Join(xs, ", ")
			}
			res := &core.Result{}
			res.Fields = fields("Description", desc)
			if c.hasSec {
				res.Fields = append(res.Fields, fields("Seconds", show(c.sec, func(v int) string { return fmt.Sprintf(":%02d", v) }))...)
			}
			res.Fields = append(res.Fields, fields(
				"Minutes", show(c.min, func(v int) string { return fmt.Sprintf(":%02d", v) }),
				"Hours", show(c.hour, strconv.Itoa),
				"Day of month", show(c.dom, strconv.Itoa),
				"Months", show(c.mon, func(v int) string { return monthNames[v][:3] }),
				"Day of week", show(c.dow, func(v int) string { return dowNames[v][:3] }),
			)...)
			runs := c.next(now().In(loc), max(1, min(r.Int("count", 5), 100)))
			var lines []string
			for i, t := range runs {
				s := t.Format("Mon, 02 Jan 2006 15:04:05 MST")
				lines = append(lines, s)
				res.Fields = append(res.Fields, core.Field{Label: fmt.Sprintf("Next run %d", i+1), Value: s + " (" + relative(t, now()) + ")"})
			}
			res.Output = desc + "\n\nNext runs (" + loc.String() + "):\n" + strings.Join(lines, "\n")
			if len(runs) == 0 {
				res.Warnings = []string{"This schedule never fires in the next 5 years (e.g. Feb 30)."}
			}
			return res, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Color

type rgba struct{ r, g, b, a float64 } // r,g,b 0-255, a 0-1

var reNum = `\s*(-?[\d.]+)(%|deg|rad|turn|grad)?\s*`

var reNumFull = regexp.MustCompile(`^` + reNum + `$`)

func parseNumUnit(s string) (float64, string, error) {
	m := reNumFull.FindStringSubmatch(s)
	if m == nil {
		return 0, "", fmt.Errorf("invalid number %q", s)
	}
	v, err := strconv.ParseFloat(m[1], 64)
	return v, m[2], err
}

func splitColorArgs(s string) []string {
	s = strings.ReplaceAll(s, "/", " / ")
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ",", " ")
	}
	return strings.Fields(s)
}

func parseColor(in string) (rgba, error) {
	s := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(in), ";")))
	if hex, ok := cssNamedColors[s]; ok {
		s = hex
	}
	if s == "transparent" {
		return rgba{0, 0, 0, 0}, nil
	}
	if strings.HasPrefix(s, "0x") && len(s) == 8 {
		s = "#" + s[2:]
	}
	if h := strings.TrimPrefix(s, "#"); isHex(h) && (len(h) == 3 || len(h) == 4 || len(h) == 6 || len(h) == 8) {
		if len(h) <= 4 {
			var b strings.Builder
			for _, c := range h {
				b.WriteRune(c)
				b.WriteRune(c)
			}
			h = b.String()
		}
		v, _ := strconv.ParseUint(h, 16, 64)
		if len(h) == 6 {
			return rgba{float64(v >> 16 & 255), float64(v >> 8 & 255), float64(v & 255), 1}, nil
		}
		return rgba{float64(v >> 24 & 255), float64(v >> 16 & 255), float64(v >> 8 & 255), float64(v&255) / 255}, nil
	}
	m := reColorFunc.FindStringSubmatch(s)
	if m == nil {
		// bare "92, 204, 127"
		if parts := splitColorArgs(s); len(parts) == 3 || len(parts) == 4 {
			m = []string{"", "rgb", s}
		} else {
			return rgba{}, fmt.Errorf("unrecognized color %q", in)
		}
	}
	fn := m[1]
	args := splitColorArgs(m[2])
	alpha := 1.0
	for i, a := range args {
		if a == "/" && i+1 < len(args) {
			v, u, err := parseNumUnit(args[i+1])
			if err != nil {
				return rgba{}, err
			}
			alpha = v
			if u == "%" {
				alpha = v / 100
			}
			args = args[:i]
			break
		}
	}
	if len(args) == 4 && fn != "cmyk" {
		v, u, err := parseNumUnit(args[3])
		if err != nil {
			return rgba{}, err
		}
		alpha = v
		if u == "%" {
			alpha = v / 100
		}
		args = args[:3]
	}
	nums := make([]float64, len(args))
	units := make([]string, len(args))
	for i, a := range args {
		v, u, err := parseNumUnit(a)
		if err != nil {
			return rgba{}, err
		}
		nums[i], units[i] = v, u
	}
	hue := func(v float64, u string) float64 {
		switch u {
		case "rad":
			v = v * 180 / math.Pi
		case "turn":
			v *= 360
		case "grad":
			v *= 0.9
		}
		return math.Mod(math.Mod(v, 360)+360, 360)
	}
	pct := func(v float64, u string) float64 {
		if u == "%" || v > 1 {
			return v / 100
		}
		return v
	}
	switch fn {
	case "rgb", "rgba":
		if len(nums) != 3 {
			return rgba{}, fmt.Errorf("rgb() needs 3 values")
		}
		c := rgba{a: alpha}
		ch := []*float64{&c.r, &c.g, &c.b}
		for i := range nums {
			v := nums[i]
			if units[i] == "%" {
				v = v * 255 / 100
			}
			*ch[i] = math.Max(0, math.Min(255, v))
		}
		return c, nil
	case "hsl", "hsla":
		if len(nums) != 3 {
			return rgba{}, fmt.Errorf("hsl() needs 3 values")
		}
		r, g, b := hslToRGB(hue(nums[0], units[0]), pct(nums[1], units[1]), pct(nums[2], units[2]))
		return rgba{r, g, b, alpha}, nil
	case "hsv", "hsb":
		if len(nums) != 3 {
			return rgba{}, fmt.Errorf("hsv() needs 3 values")
		}
		r, g, b := hsvToRGB(hue(nums[0], units[0]), pct(nums[1], units[1]), pct(nums[2], units[2]))
		return rgba{r, g, b, alpha}, nil
	case "hwb":
		if len(nums) != 3 {
			return rgba{}, fmt.Errorf("hwb() needs 3 values")
		}
		w, bl := pct(nums[1], units[1]), pct(nums[2], units[2])
		if w+bl >= 1 {
			g := w / (w + bl) * 255
			return rgba{g, g, g, alpha}, nil
		}
		r, g, b := hsvToRGB(hue(nums[0], units[0]), 1-w/(1-bl), 1-bl)
		return rgba{r, g, b, alpha}, nil
	case "cmyk", "device-cmyk":
		if len(nums) != 4 {
			return rgba{}, fmt.Errorf("cmyk() needs 4 values")
		}
		c, mm, y, k := pct(nums[0], units[0]), pct(nums[1], units[1]), pct(nums[2], units[2]), pct(nums[3], units[3])
		return rgba{255 * (1 - c) * (1 - k), 255 * (1 - mm) * (1 - k), 255 * (1 - y) * (1 - k), alpha}, nil
	}
	return rgba{}, fmt.Errorf("unsupported color function %s()", fn)
}

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	c := (1 - math.Abs(2*l-1)) * s
	return chroma(h, c, l-c/2)
}

func hsvToRGB(h, s, v float64) (float64, float64, float64) {
	c := v * s
	return chroma(h, c, v-c)
}

func chroma(h, c, m float64) (float64, float64, float64) {
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return (r + m) * 255, (g + m) * 255, (b + m) * 255
}

func (c rgba) hsl() (h, s, l float64) {
	r, g, b := c.r/255, c.g/255, c.b/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	d := mx - mn
	if d == 0 {
		return 0, 0, l
	}
	s = d / (1 - math.Abs(2*l-1))
	return hueOf(r, g, b, mx, d), s, l
}

func (c rgba) hsv() (h, s, v float64) {
	r, g, b := c.r/255, c.g/255, c.b/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	d := mx - mn
	if mx > 0 {
		s = d / mx
	}
	if d == 0 {
		return 0, s, mx
	}
	return hueOf(r, g, b, mx, d), s, mx
}

func hueOf(r, g, b, mx, d float64) float64 {
	var h float64
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h
}

func (c rgba) luminance() float64 {
	lin := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

func contrast(a, b rgba) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func wcag(ratio float64) string {
	switch {
	case ratio >= 7:
		return "AAA"
	case ratio >= 4.5:
		return "AA"
	case ratio >= 3:
		return "AA large text"
	}
	return "fail"
}

func r8(v float64) int { return int(math.Round(v)) }

func trimF(v float64, prec int) string {
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

func colorTool() *core.Tool {
	return &core.Tool{
		ID:          "color",
		Name:        "Color Converter",
		Category:    core.CatInspect,
		Description: "Convert between HEX, RGB(A), HSL(A), HSB/HSV, HWB, CMYK and CSS named colors. Shows WCAG contrast against white/black and ready-to-paste code for CSS, Swift, Android, Flutter, Compose, .NET and more.",
		Aliases:     []string{"colour", "hex", "rgb", "hsl"},
		Keywords:    []string{"hex", "rgb", "hsl", "hsv", "cmyk", "palette", "contrast", "wcag"},
		Sample:      "#5CCC7F",
		Detect: func(in string) *core.Detection {
			t := strings.ToLower(strings.TrimSpace(in))
			if !isSingleLine(t) || len(t) > 60 {
				return nil
			}
			if reHexColorFull.MatchString(t) {
				return det(95, "Hex color")
			}
			if reColorFnPrefix.MatchString(t) {
				if _, err := parseColor(t); err == nil {
					return det(97, "CSS color")
				}
			}
			if _, ok := cssNamedColors[t]; ok && len(t) > 3 {
				return det(45, "Named color")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			c, err := parseColor(r.Input)
			if err != nil {
				return nil, err
			}
			R, G, B := r8(c.r), r8(c.g), r8(c.b)
			A := c.a
			A255 := r8(A * 255)
			hex6 := fmt.Sprintf("#%02X%02X%02X", R, G, B)
			hex8 := fmt.Sprintf("%s%02X", hex6, A255)
			h, s, l := c.hsl()
			hv, sv, vv := c.hsv()
			_, _, _ = hv, sv, vv
			wht := (1 - math.Max(c.r, math.Max(c.g, c.b))/255)
			whiteness := math.Min(c.r, math.Min(c.g, c.b)) / 255
			k := wht
			cy, mg, ye := 0.0, 0.0, 0.0
			if k < 1 {
				cy = (1 - c.r/255 - k) / (1 - k)
				mg = (1 - c.g/255 - k) / (1 - k)
				ye = (1 - c.b/255 - k) / (1 - k)
			}
			pct := func(v float64) string { return trimF(v*100, 0) + "%" }
			alphaS := trimF(A, 3)
			white, black := rgba{255, 255, 255, 1}, rgba{0, 0, 0, 1}
			cw, cb := contrast(c, white), contrast(c, black)
			nearest, dist := "", math.MaxFloat64
			for name, hx := range cssNamedColors {
				nc, _ := parseColor(hx)
				d := math.Pow(nc.r-c.r, 2) + math.Pow(nc.g-c.g, 2) + math.Pow(nc.b-c.b, 2)
				if d < dist || (d == dist && name < nearest) {
					nearest, dist = name, d
				}
			}
			if dist == 0 {
				nearest += " (exact)"
			}
			res := &core.Result{View: "color", Data: map[string]any{"hex": hex8, "css": fmt.Sprintf("rgba(%d, %d, %d, %s)", R, G, B, alphaS)}}
			sw := fmt.Sprintf("rgba(%d,%d,%d,%s)", R, G, B, alphaS)
			res.Fields = []core.Field{
				{Label: "Hex", Value: hex6, Color: sw},
				{Label: "Hex with alpha", Value: hex8, Color: sw},
				{Label: "RGB", Value: fmt.Sprintf("rgb(%d, %d, %d)", R, G, B), Color: sw},
				{Label: "RGBA", Value: fmt.Sprintf("rgba(%d, %d, %d, %s)", R, G, B, alphaS), Color: sw},
				{Label: "HSL", Value: fmt.Sprintf("hsl(%s, %s, %s)", trimF(h, 0), pct(s), pct(l)), Color: sw},
				{Label: "HSLA", Value: fmt.Sprintf("hsla(%s, %s, %s, %s)", trimF(h, 0), pct(s), pct(l), alphaS), Color: sw},
				{Label: "HSB (HSV)", Value: fmt.Sprintf("hsb(%s, %s, %s)", trimF(hv, 0), pct(sv), pct(vv)), Color: sw},
				{Label: "HWB", Value: fmt.Sprintf("hwb(%s %s %s)", trimF(h, 0), pct(whiteness), pct(wht)), Color: sw},
				{Label: "CMYK", Value: fmt.Sprintf("cmyk(%s, %s, %s, %s)", pct(cy), pct(mg), pct(ye), pct(k)), Color: sw},
				{Label: "Nearest named color", Value: nearest},
				{Label: "Contrast on white", Value: fmt.Sprintf("%.2f:1 (%s)", cw, wcag(cw))},
				{Label: "Contrast on black", Value: fmt.Sprintf("%.2f:1 (%s)", cb, wcag(cb))},
			}
			rf, gf, bf := trimF(c.r/255, 4), trimF(c.g/255, 4), trimF(c.b/255, 4)
			argb := fmt.Sprintf("0x%02X%02X%02X%02X", A255, R, G, B)
			res.Output = strings.Join([]string{
				"/* CSS Color Level 4 */",
				fmt.Sprintf("rgb(%d %d %d / %s)", R, G, B, pct(A)),
				fmt.Sprintf("hsl(%sdeg %s %s / %s)", trimF(h, 0), pct(s), pct(l), pct(A)),
				"",
				"// Swift (UIKit / AppKit / SwiftUI)",
				fmt.Sprintf("UIColor(red: %s, green: %s, blue: %s, alpha: %s)", rf, gf, bf, alphaS),
				fmt.Sprintf("NSColor(calibratedRed: %s, green: %s, blue: %s, alpha: %s)", rf, gf, bf, alphaS),
				fmt.Sprintf("Color(red: %s, green: %s, blue: %s, opacity: %s)", rf, gf, bf, alphaS),
				"",
				"// Android (Java/Kotlin) & XML",
				fmt.Sprintf("Color.argb(%d, %d, %d, %d)", A255, R, G, B),
				fmt.Sprintf("<color name=\"brand\">#%02X%02X%02X%02X</color>", A255, R, G, B),
				"",
				"// Jetpack Compose / Flutter",
				fmt.Sprintf("Color(%s)", argb),
				"",
				"// .NET / Java AWT / Unity",
				fmt.Sprintf("Color.FromArgb(%d, %d, %d, %d)", A255, R, G, B),
				fmt.Sprintf("new Color(%d, %d, %d, %d)", R, G, B, A255),
				fmt.Sprintf("new Color(%sf, %sf, %sf, %sf)", rf, gf, bf, alphaS),
				"",
				"/* Tailwind arbitrary value */",
				fmt.Sprintf("bg-[%s]", strings.ToLower(hex6)),
			}, "\n")
			return res, nil
		},
	}
}

var cssNamedColors = map[string]string{
	"aliceblue": "#f0f8ff", "antiquewhite": "#faebd7", "aqua": "#00ffff", "aquamarine": "#7fffd4", "azure": "#f0ffff", "beige": "#f5f5dc", "bisque": "#ffe4c4",
	"black": "#000000", "blanchedalmond": "#ffebcd", "blue": "#0000ff", "blueviolet": "#8a2be2", "brown": "#a52a2a", "burlywood": "#deb887", "cadetblue": "#5f9ea0",
	"chartreuse": "#7fff00", "chocolate": "#d2691e", "coral": "#ff7f50", "cornflowerblue": "#6495ed", "cornsilk": "#fff8dc", "crimson": "#dc143c", "cyan": "#00ffff",
	"darkblue": "#00008b", "darkcyan": "#008b8b", "darkgoldenrod": "#b8860b", "darkgray": "#a9a9a9", "darkgreen": "#006400", "darkgrey": "#a9a9a9", "darkkhaki": "#bdb76b",
	"darkmagenta": "#8b008b", "darkolivegreen": "#556b2f", "darkorange": "#ff8c00", "darkorchid": "#9932cc", "darkred": "#8b0000", "darksalmon": "#e9967a",
	"darkseagreen": "#8fbc8f", "darkslateblue": "#483d8b", "darkslategray": "#2f4f4f", "darkslategrey": "#2f4f4f", "darkturquoise": "#00ced1", "darkviolet": "#9400d3",
	"deeppink": "#ff1493", "deepskyblue": "#00bfff", "dimgray": "#696969", "dimgrey": "#696969", "dodgerblue": "#1e90ff", "firebrick": "#b22222", "floralwhite": "#fffaf0",
	"forestgreen": "#228b22", "fuchsia": "#ff00ff", "gainsboro": "#dcdcdc", "ghostwhite": "#f8f8ff", "gold": "#ffd700", "goldenrod": "#daa520", "gray": "#808080",
	"green": "#008000", "greenyellow": "#adff2f", "grey": "#808080", "honeydew": "#f0fff0", "hotpink": "#ff69b4", "indianred": "#cd5c5c", "indigo": "#4b0082",
	"ivory": "#fffff0", "khaki": "#f0e68c", "lavender": "#e6e6fa", "lavenderblush": "#fff0f5", "lawngreen": "#7cfc00", "lemonchiffon": "#fffacd", "lightblue": "#add8e6",
	"lightcoral": "#f08080", "lightcyan": "#e0ffff", "lightgoldenrodyellow": "#fafad2", "lightgray": "#d3d3d3", "lightgreen": "#90ee90", "lightgrey": "#d3d3d3",
	"lightpink": "#ffb6c1", "lightsalmon": "#ffa07a", "lightseagreen": "#20b2aa", "lightskyblue": "#87cefa", "lightslategray": "#778899", "lightslategrey": "#778899",
	"lightsteelblue": "#b0c4de", "lightyellow": "#ffffe0", "lime": "#00ff00", "limegreen": "#32cd32", "linen": "#faf0e6", "magenta": "#ff00ff", "maroon": "#800000",
	"mediumaquamarine": "#66cdaa", "mediumblue": "#0000cd", "mediumorchid": "#ba55d3", "mediumpurple": "#9370db", "mediumseagreen": "#3cb371", "mediumslateblue": "#7b68ee",
	"mediumspringgreen": "#00fa9a", "mediumturquoise": "#48d1cc", "mediumvioletred": "#c71585", "midnightblue": "#191970", "mintcream": "#f5fffa", "mistyrose": "#ffe4e1",
	"moccasin": "#ffe4b5", "navajowhite": "#ffdead", "navy": "#000080", "oldlace": "#fdf5e6", "olive": "#808000", "olivedrab": "#6b8e23", "orange": "#ffa500",
	"orangered": "#ff4500", "orchid": "#da70d6", "palegoldenrod": "#eee8aa", "palegreen": "#98fb98", "paleturquoise": "#afeeee", "palevioletred": "#db7093",
	"papayawhip": "#ffefd5", "peachpuff": "#ffdab9", "peru": "#cd853f", "pink": "#ffc0cb", "plum": "#dda0dd", "powderblue": "#b0e0e6", "purple": "#800080",
	"rebeccapurple": "#663399", "red": "#ff0000", "rosybrown": "#bc8f8f", "royalblue": "#4169e1", "saddlebrown": "#8b4513", "salmon": "#fa8072", "sandybrown": "#f4a460",
	"seagreen": "#2e8b57", "seashell": "#fff5ee", "sienna": "#a0522d", "silver": "#c0c0c0", "skyblue": "#87ceeb", "slateblue": "#6a5acd", "slategray": "#708090",
	"slategrey": "#708090", "snow": "#fffafa", "springgreen": "#00ff7f", "steelblue": "#4682b4", "tan": "#d2b48c", "teal": "#008080", "thistle": "#d8bfd8",
	"tomato": "#ff6347", "turquoise": "#40e0d0", "violet": "#ee82ee", "wheat": "#f5deb3", "white": "#ffffff", "whitesmoke": "#f5f5f5", "yellow": "#ffff00",
	"yellowgreen": "#9acd32",
}

// Compiled once: detectors run on every paste, tools on every keystroke.
var (
	reSentence      = regexp.MustCompile(`[^.!?\n]+[.!?]+`)
	reParaBreak     = regexp.MustCompile(`\n\s*\n`)
	reColorFunc     = regexp.MustCompile(`^([a-z]+)\((.*)\)$`)
	reHexColorFull  = regexp.MustCompile(`^#([0-9a-f]{3}|[0-9a-f]{4}|[0-9a-f]{6}|[0-9a-f]{8})$`)
	reColorFnPrefix = regexp.MustCompile(`^(rgba?|hsla?|hsv|hsb|hwb|cmyk)\(`)
)
