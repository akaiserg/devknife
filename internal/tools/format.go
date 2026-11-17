package tools

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/tdewolff/minify/v2"
	mcss "github.com/tdewolff/minify/v2/css"
	mhtml "github.com/tdewolff/minify/v2/html"
	mjs "github.com/tdewolff/minify/v2/js"
	msvg "github.com/tdewolff/minify/v2/svg"
	mxml "github.com/tdewolff/minify/v2/xml"

	"devknife/internal/core"
)

var minifier = func() *minify.M {
	m := minify.New()
	m.AddFunc("text/css", mcss.Minify)
	m.Add("text/html", &mhtml.Minifier{KeepDocumentTags: true, KeepEndTags: true, KeepQuotes: true, KeepDefaultAttrVals: true, KeepSpecialComments: true})
	m.AddFunc("image/svg+xml", msvg.Minify)
	m.AddFuncRegexp(regexp.MustCompile(`^(application|text)/(x-)?(java|ecma)script$`), mjs.Minify)
	m.AddFunc("module", mjs.Minify)
	m.AddFuncRegexp(regexp.MustCompile(`[/+]xml$`), mxml.Minify)
	return m
}()

func minifyResult(lang, in, out string) *core.Result {
	return &core.Result{Output: out, Lang: lang, Fields: fields("Size", sizeDelta(len(in), len(out)))}
}

var (
	reHTMLDoc  = regexp.MustCompile(`(?i)^\s*(<!doctype\s+html|<html[\s>])`)
	reHTMLTags = regexp.MustCompile(`(?i)<(div|span|p|a|ul|ol|li|table|tr|td|body|head|section|article|nav|header|footer|img|br|h[1-6]|form|input|button|label|select|script|style|link|meta|main|strong|em)\b`)
	reERB      = regexp.MustCompile(`<%[=#-]?[\s\S]*?%>`)
	reXMLDecl  = regexp.MustCompile(`^\s*<\?xml\b`)
	reSCSS     = regexp.MustCompile(`(?m)(^\s*\$[\w-]+\s*:|@mixin\b|@include\b|@extend\b|@use\b|@forward\b|#\{|&[:.\-_\[]|@each\b|@if\b)`)
	reLESS     = regexp.MustCompile(`(?m)(^\s*@[\w-]+\s*:|\.[\w-]+\(\s*\)\s*;|~["']|@\{[\w-]+\}|\bwhen\s*\(|&:extend)`)
	reCSSRule  = regexp.MustCompile(`[^{}]+\{\s*[\w-]+\s*:[^{}]*\}`)
	reJS       = regexp.MustCompile(`(\bfunction\b\s*\w*\s*\(|=>|\b(const|let|var)\s+[\w${\[]|\bconsole\.\w+\(|\brequire\(|\bimport\s+[\w{*].*\bfrom\b|\bexport\s+(default|const|function|class)\b|\bdocument\.\w+|\bwindow\.\w+)`)
	reSQL      = regexp.MustCompile(`(?is)^\s*(\(?\s*select\b.+\bfrom\b|insert\s+into\b|update\s+\S+\s+set\b|delete\s+from\b|create\s+(table|index|view|unique)\b|alter\s+table\b|drop\s+table\b|with\s+\w+\s+as\s*\()`)
)

func markupTool(id, name, lang string, aliases []string, mode markupMode, sample string, detect func(string) *core.Detection) *core.Tool {
	return &core.Tool{
		ID:          id,
		Name:        name,
		Category:    core.CatFormat,
		Description: fmt.Sprintf("Beautify or minify %s. Never rejects input: unknown or broken markup is passed through with normalised whitespace.", strings.Split(name, " ")[0]),
		Aliases:     aliases,
		Keywords:    []string{"pretty", "prettify", "beautify", "format", "minify", "compress", "indent"},
		Options:     []core.Option{optMode("beautify", "beautify", "minify"), optIndent()},
		Sample:      sample,
		Detect:      detect,
		Run: func(r core.Request) (*core.Result, error) {
			if strings.TrimSpace(r.Input) == "" {
				return nil, fmt.Errorf("input is empty")
			}
			if r.Str("mode") == "minify" {
				var out string
				var err error
				switch id {
				case "html":
					out, err = minifier.String("text/html", r.Input)
					if err != nil {
						out, err = minifyMarkup(r.Input, mode, false), nil
					}
				case "xml":
					out, err = minifier.String("text/xml", r.Input)
					if err != nil {
						out, err = minifyMarkup(r.Input, mode, false), nil
					}
				default:
					out = minifyMarkup(r.Input, mode, false)
				}
				return minifyResult(lang, r.Input, strings.TrimSpace(out)), err
			}
			return text(beautifyMarkup(r.Input, indentStr(r), mode), lang), nil
		},
	}
}

func cssTool(id, name, dialect string, aliases []string, sample string, detect func(string) *core.Detection) *core.Tool {
	return &core.Tool{
		ID:          id,
		Name:        name,
		Category:    core.CatFormat,
		Description: fmt.Sprintf("Beautify or minify %s. Handles nesting, interpolation and // comments where the language allows them.", strings.Split(name, " ")[0]),
		Aliases:     aliases,
		Keywords:    []string{"pretty", "prettify", "beautify", "format", "minify", "compress", "stylesheet"},
		Options:     []core.Option{optMode("beautify", "beautify", "minify"), optIndent()},
		Sample:      sample,
		Detect:      detect,
		Run: func(r core.Request) (*core.Result, error) {
			if strings.TrimSpace(r.Input) == "" {
				return nil, fmt.Errorf("input is empty")
			}
			if r.Str("mode") == "minify" {
				if dialect == "css" {
					if out, err := minifier.String("text/css", r.Input); err == nil {
						return minifyResult("css", r.Input, out), nil
					}
				}
				return minifyResult(dialect, r.Input, minifyCSSLike(r.Input, dialect)), nil
			}
			return text(beautifyCSS(r.Input, indentStr(r), dialect), dialect), nil
		},
	}
}

func formatTools() []*core.Tool {
	htmlDetect := func(in string) *core.Detection {
		if reERB.MatchString(in) {
			return nil
		}
		if reHTMLDoc.MatchString(in) {
			return det(85, "HTML document")
		}
		if strings.Contains(strings.TrimSpace(in), "<svg") && looksLikeMarkup(in) {
			return det(40, "SVG markup")
		}
		if looksLikeMarkup(in) && reHTMLTags.MatchString(in) {
			return det(70, "HTML fragment")
		}
		return nil
	}
	xmlDetect := func(in string) *core.Detection {
		if reXMLDecl.MatchString(in) {
			return det(90, "XML document")
		}
		t := strings.TrimSpace(in)
		if looksLikeMarkup(t) && !reHTMLTags.MatchString(t) && !reERB.MatchString(t) {
			if strings.HasPrefix(t, "<svg") {
				return det(55, "SVG (XML)")
			}
			return det(65, "XML-like markup")
		}
		return nil
	}
	erbDetect := func(in string) *core.Detection {
		if reERB.MatchString(in) && strings.Contains(in, "<") {
			return det(90, "ERB template")
		}
		return nil
	}
	cssDetect := func(in string) *core.Detection {
		if reSCSS.MatchString(in) || reLESS.MatchString(in) || looksLikeMarkup(in) {
			return nil
		}
		if reCSSRule.MatchString(in) && !reJS.MatchString(in) {
			return det(75, "CSS")
		}
		return nil
	}
	scssDetect := func(in string) *core.Detection {
		if strings.Contains(in, "{") && reSCSS.MatchString(in) && !looksLikeMarkup(in) {
			return det(78, "SCSS")
		}
		return nil
	}
	lessDetect := func(in string) *core.Detection {
		if strings.Contains(in, "{") && reLESS.MatchString(in) && !looksLikeMarkup(in) && !reSCSS.MatchString(in) {
			return det(76, "LESS")
		}
		return nil
	}
	jsDetect := func(in string) *core.Detection {
		t := strings.TrimSpace(in)
		if looksLikeMarkup(t) || strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") && !strings.Contains(t, ";") && !strings.Contains(t, "=>") {
			return nil
		}
		if strings.HasPrefix(strings.ToLower(t), "curl ") {
			return nil
		}
		if m := reJS.FindAllStringIndex(t, 5); len(m) > 0 {
			return det(50+min(len(m), 4)*5, "JavaScript")
		}
		return nil
	}
	sqlDetect := func(in string) *core.Detection {
		if reSQL.MatchString(in) {
			return det(85, "SQL")
		}
		return nil
	}

	return []*core.Tool{
		jsonFormatTool(),
		markupTool("html", "HTML Beautify/Minify", "html", []string{"html-format"}, markupMode{html: true},
			`<!DOCTYPE html><html><head><title>Demo</title><style>body{margin:0;font-family:sans-serif}.card{padding:16px}</style></head><body><div class="card"><h1>Hello <em>world</em></h1><ul><li>One</li><li>Two</li></ul><img src="logo.png" alt="Logo"><script>document.querySelector('.card').addEventListener('click',()=>{console.log('clicked')})</script></div></body></html>`,
			htmlDetect),
		cssTool("css", "CSS Beautify/Minify", "css", []string{"css-format"},
			`body{margin:0;padding:0;font:14px/1.5 -apple-system,sans-serif}.btn,.btn-primary>span{color:#fff;background:url(//cdn.example.com/bg.png)}@media (max-width:600px){.btn{display:block}}`,
			cssDetect),
		{
			ID:          "js",
			Name:        "JS Beautify/Minify",
			Category:    core.CatFormat,
			Description: "Beautify or minify JavaScript. Handles template literals, regex literals, arrow functions, and code without semicolons.",
			Aliases:     []string{"javascript", "js-format"},
			Keywords:    []string{"pretty", "prettify", "beautify", "format", "minify", "uglify", "compress", "ecmascript", "typescript"},
			Options:     []core.Option{optMode("beautify", "beautify", "minify"), optIndent()},
			Sample:      `const sum=(a,b)=>a+b;function greet(name){if(!name){throw new Error("name required")}const msg=` + "`Hello, ${name}!`" + `;return {msg,len:msg.length}}export default {sum,greet};`,
			Detect:      jsDetect,
			Run: func(r core.Request) (*core.Result, error) {
				if strings.TrimSpace(r.Input) == "" {
					return nil, fmt.Errorf("input is empty")
				}
				if r.Str("mode") == "minify" {
					out, err := minifier.String("application/javascript", r.Input)
					if err != nil {
						return nil, fmt.Errorf("cannot minify: %v", err)
					}
					return minifyResult("js", r.Input, out), nil
				}
				return text(beautifyJS(r.Input, indentStr(r)), "js"), nil
			},
		},
		markupTool("erb", "ERB Beautify/Minify", "erb", nil, markupMode{html: true, erb: true},
			"<ul class=\"users\"><% @users.each do |user| %><li><%= link_to user.name, user %><% if user.admin? %><span class=\"badge\">admin</span><% else %><span>member</span><% end %></li><% end %></ul>",
			erbDetect),
		cssTool("less", "LESS Beautify/Minify", "less", nil,
			`@primary:#428bca;.bordered(@w:2px){border:@w solid black}#header{color:darken(@primary,10%);.navigation{font-size:12px;&:hover{color:red}}.bordered(4px);}`,
			lessDetect),
		cssTool("scss", "SCSS Beautify/Minify", "scss", []string{"sass"},
			`$primary:#333;@mixin flex($dir:row){display:flex;flex-direction:$dir}.nav{@include flex(column);ul{margin:0;li{color:$primary;&:hover{color:red}}}// comment
.item-#{$i}{width:10px}}`,
			scssDetect),
		markupTool("xml", "XML Beautify/Minify", "xml", []string{"xml-format"}, markupMode{},
			`<?xml version="1.0" encoding="UTF-8"?><catalog xmlns:dc="http://purl.org/dc/elements/1.1/"><book id="bk101"><dc:author>Gambardella, Matthew</dc:author><title>XML Developer's Guide</title><price currency="USD">44.95</price><description><![CDATA[An in-depth look at <XML>.]]></description></book><!-- more books --><book id="bk102" available="false"/></catalog>`,
			xmlDetect),
		{
			ID:          "sql",
			Name:        "SQL Formatter",
			Category:    core.CatFormat,
			Description: "Format or compact SQL queries. Understands subqueries, CASE, CTEs, joins, comments, and Postgres/MySQL/SQL Server quoting.",
			Aliases:     []string{"sql-format"},
			Keywords:    []string{"query", "pretty", "beautify", "postgres", "mysql", "sqlite"},
			Options: []core.Option{
				optMode("format", "format", "minify"),
				optIndent(),
				{Key: "case", Label: "Keywords", Type: core.OptSelect, Default: "upper", Choices: []string{"upper", "lower", "preserve"}},
			},
			Sample: `select u.id, u.name, count(o.id) as orders, case when sum(o.total) > 1000 then 'vip' else 'regular' end as tier from users u left join orders o on o.user_id = u.id and o.status = 'paid' where u.created_at between '2024-01-01' and '2024-12-31' and u.id in (select user_id from subscriptions where active = true) group by u.id, u.name having count(o.id) > 2 order by orders desc limit 10;`,
			Detect: sqlDetect,
			Run: func(r core.Request) (*core.Result, error) {
				if strings.TrimSpace(r.Input) == "" {
					return nil, fmt.Errorf("input is empty")
				}
				if r.Str("mode") == "minify" {
					return minifyResult("sql", r.Input, minifySQL(r.Input)), nil
				}
				return text(formatSQL(r.Input, indentStr(r), r.Str("case")), "sql"), nil
			},
		},
		linesTool(),
	}
}

// ---------------------------------------------------------------------------

func linesTool() *core.Tool {
	return &core.Tool{
		ID:          "lines",
		Name:        "Line Sort/Dedupe",
		Category:    core.CatFormat,
		Description: "Sort, deduplicate, reverse, shuffle and clean up lines of text. Natural sort orders file2 before file10.",
		Aliases:     []string{"sort", "dedupe", "uniq"},
		Keywords:    []string{"unique", "duplicate", "lines", "order", "shuffle", "reverse", "natural"},
		Options: []core.Option{
			{Key: "sort", Label: "Sort", Type: core.OptSelect, Default: "asc", Choices: []string{"none", "asc", "desc", "natural", "length", "reverse", "shuffle"}, Short: "s"},
			{Key: "dedupe", Label: "Remove duplicates", Type: core.OptBool, Default: "true", Short: "u"},
			{Key: "ignore_case", Label: "Ignore case", Type: core.OptBool, Default: "false"},
			{Key: "trim", Label: "Trim lines", Type: core.OptBool, Default: "true"},
			{Key: "remove_empty", Label: "Remove empty", Type: core.OptBool, Default: "true"},
		},
		Sample: "banana\napple\nfile10.txt\nCherry\napple\nfile2.txt\n\nbanana\n  date  ",
		Detect: func(in string) *core.Detection {
			lines := strings.Split(strings.TrimSpace(in), "\n")
			if len(lines) >= 3 {
				seen := map[string]bool{}
				dups := 0
				for _, l := range lines {
					if seen[l] {
						dups++
					}
					seen[l] = true
				}
				if dups > 0 {
					return det(40, fmt.Sprintf("%s with duplicates", plural(len(lines), "line")))
				}
				return det(20, plural(len(lines), "line"))
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			in := strings.ReplaceAll(r.Input, "\r\n", "\n")
			lines := strings.Split(strings.TrimSuffix(in, "\n"), "\n")
			total := len(lines)
			ic := r.Bool("ignore_case")
			key := func(s string) string {
				if ic {
					return strings.ToLower(s)
				}
				return s
			}
			var out []string
			seen := map[string]bool{}
			empty, dups := 0, 0
			for _, l := range lines {
				if r.Bool("trim") {
					l = strings.TrimSpace(l)
				}
				if r.Bool("remove_empty") && strings.TrimSpace(l) == "" {
					empty++
					continue
				}
				if r.Bool("dedupe") {
					if seen[key(l)] {
						dups++
						continue
					}
					seen[key(l)] = true
				}
				out = append(out, l)
			}
			switch r.Str("sort") {
			case "asc":
				sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
			case "desc":
				sort.SliceStable(out, func(i, j int) bool { return key(out[i]) > key(out[j]) })
			case "natural":
				sort.SliceStable(out, func(i, j int) bool { return naturalLess(key(out[i]), key(out[j])) })
			case "length":
				sort.SliceStable(out, func(i, j int) bool { return len([]rune(out[i])) < len([]rune(out[j])) })
			case "reverse":
				for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
					out[i], out[j] = out[j], out[i]
				}
			case "shuffle":
				rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
			}
			return &core.Result{
				Output: strings.Join(out, "\n"),
				Fields: fields("Lines in", strconv.Itoa(total), "Lines out", strconv.Itoa(len(out)), "Duplicates removed", strconv.Itoa(dups), "Empty removed", strconv.Itoa(empty)),
			}, nil
		},
	}
}

// naturalLess compares strings treating digit runs as numbers.
func naturalLess(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if unicode.IsDigit(ra[i]) && unicode.IsDigit(rb[j]) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ra[i] != rb[j] {
			return ra[i] < rb[j]
		}
		i++
		j++
	}
	return len(ra)-i < len(rb)-j
}
