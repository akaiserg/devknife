package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"devknife/internal/core"
)

var (
	reHexOnly = regexp.MustCompile(`^(0x)?[0-9a-fA-F]+$`)
	reDigits  = regexp.MustCompile(`^-?\d+$`)
)

// ---------------------------------------------------------------------------
// URL parser

func urlParserTool() *core.Tool {
	return &core.Tool{
		ID:          "url-parse",
		Name:        "URL Parser",
		Category:    core.CatConvert,
		Description: "Break a URL into protocol, credentials, host, port, path, query and fragment. Query parameters are decoded into JSON (repeated keys become arrays).",
		Aliases:     []string{"urlparse", "query-string", "qs"},
		Keywords:    []string{"query", "parameters", "querystring", "host", "path"},
		Sample:      "https://user:pa55@api.example.com:8443/v1/search/results.json?q=dev+tools&tags[]=go&tags[]=cli&page=2&utm_source=newsletter#section-3",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if !isSingleLine(t) || strings.ContainsAny(t, " \t") {
				return nil
			}
			u, err := url.Parse(t)
			if err != nil || u.Scheme == "" || (u.Host == "" && u.Opaque == "") {
				if strings.HasPrefix(t, "?") || (strings.Contains(t, "=") && strings.Contains(t, "&") && !strings.Contains(t, "/")) {
					return det(55, "Query string")
				}
				return nil
			}
			score := 75
			if u.RawQuery != "" {
				score = 88
			}
			return det(score, "URL")
		},
		Run: func(r core.Request) (*core.Result, error) {
			t := strings.TrimSpace(r.Input)
			if t == "" {
				return nil, fmt.Errorf("input is empty")
			}
			if !strings.Contains(t, "://") && (strings.HasPrefix(t, "?") || (strings.Contains(t, "=") && !strings.Contains(t, "/"))) {
				t = "http://query.local/?" + strings.TrimPrefix(t, "?")
			}
			u, err := url.Parse(t)
			if err != nil {
				return nil, fmt.Errorf("invalid URL: %v", err)
			}
			pass, _ := u.User.Password()
			file := ""
			if i := strings.LastIndex(u.Path, "/"); i >= 0 && i < len(u.Path)-1 {
				file = u.Path[i+1:]
			}
			port := u.Port()
			if port == "" {
				port = map[string]string{"http": "80 (default)", "https": "443 (default)", "ws": "80 (default)", "wss": "443 (default)", "ftp": "21 (default)"}[u.Scheme]
			}
			res := &core.Result{Lang: "json"}
			res.Fields = fields(
				"Protocol", u.Scheme,
				"Username", u.User.Username(),
				"Password", pass,
				"Host", u.Host,
				"Hostname", u.Hostname(),
				"Port", port,
				"Origin", u.Scheme+"://"+u.Host,
				"Path", u.Path,
				"File name", file,
				"Query", u.RawQuery,
				"Fragment", u.Fragment,
			)
			// drop empty rows for a cleaner table
			var keep []core.Field
			for _, f := range res.Fields {
				if f.Value != "" {
					keep = append(keep, f)
				}
			}
			res.Fields = keep
			qm := newOMap()
			for _, pair := range strings.Split(u.RawQuery, "&") {
				if pair == "" {
					continue
				}
				k, v, _ := strings.Cut(pair, "=")
				dk, err1 := url.QueryUnescape(k)
				dv, err2 := url.QueryUnescape(v)
				if err1 != nil {
					dk = k
				}
				if err2 != nil {
					dv = v
				}
				if prev, ok := qm.Get(dk); ok {
					if arr, isArr := prev.([]any); isArr {
						qm.Set(dk, append(arr, dv))
					} else {
						qm.Set(dk, []any{prev, dv})
					}
				} else if strings.HasSuffix(dk, "[]") {
					qm.Set(dk, []any{dv})
				} else {
					qm.Set(dk, dv)
				}
			}
			res.Output = encodeOrdered(qm, "  ", false)
			return res, nil
		},
	}
}

// ---------------------------------------------------------------------------
// YAML <-> JSON

// maxAliasExpansion caps how many values YAML aliases may expand to. Each
// alias is copied in full, so a few nested anchors ("billion laughs") turn a
// 300-byte document into gigabytes.
const maxAliasExpansion = 1_000_000

// yamlConv converts YAML nodes to ordered values with a depth limit and an
// alias-expansion budget shared by every document of one input.
type yamlConv struct {
	depth    int
	inAlias  int
	expanded int
}

func (c *yamlConv) node(n *yaml.Node) (any, error) {
	c.depth++
	defer func() { c.depth-- }()
	if c.depth > maxJSONDepth+1 { // +1: the document node wraps the root
		return nil, fmt.Errorf("YAML %w", errTooDeep)
	}
	if c.inAlias > 0 {
		if c.expanded++; c.expanded > maxAliasExpansion {
			return nil, fmt.Errorf("YAML aliases expand to more than %d values; refusing to expand them (is this a \"billion laughs\" document?)", maxAliasExpansion)
		}
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return c.node(n.Content[0])
	case yaml.AliasNode:
		c.inAlias++
		defer func() { c.inAlias-- }()
		return c.node(n.Alias)
	case yaml.MappingNode:
		m := newOMap()
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Tag == "!!merge" || k.Value == "<<" && k.Tag != "!!str" {
				merged, err := c.node(v)
				if err != nil {
					return nil, err
				}
				mergeInto := func(x any) {
					if mm, ok := x.(*OMap); ok {
						for _, kk := range mm.Keys {
							if _, exists := m.Get(kk); !exists {
								m.Set(kk, mm.Vals[kk])
							}
						}
					}
				}
				if arr, ok := merged.([]any); ok {
					for _, x := range arr {
						mergeInto(x)
					}
				} else {
					mergeInto(merged)
				}
				continue
			}
			val, err := c.node(v)
			if err != nil {
				return nil, err
			}
			m.Set(k.Value, val)
		}
		return m, nil
	case yaml.SequenceNode:
		arr := make([]any, 0, len(n.Content))
		for _, item := range n.Content {
			v, err := c.node(item)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		return arr, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int":
			var i big.Int
			v := strings.ReplaceAll(n.Value, "_", "")
			if _, ok := i.SetString(v, 0); ok {
				return json.Number(i.String()), nil
			}
			return n.Value, nil
		case "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return nil, err
			}
			if f != f || f > 1e308 || f < -1e308 { // NaN / Inf have no JSON form
				return n.Value, nil
			}
			if _, err := strconv.ParseFloat(n.Value, 64); err == nil && !strings.ContainsAny(n.Value, "_") {
				return json.Number(strings.TrimPrefix(n.Value, "+")), nil
			}
			return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
		case "!!timestamp":
			return n.Value, nil
		case "!!binary":
			return n.Value, nil
		}
		return n.Value, nil
	}
	return nil, fmt.Errorf("unsupported YAML node")
}

func yamlToJSONTool() *core.Tool {
	return &core.Tool{
		ID:          "yaml2json",
		Name:        "YAML to JSON",
		Category:    core.CatConvert,
		Description: "Convert YAML to JSON preserving key order. Supports anchors, aliases, merge keys (<<) and multi-document streams.",
		Aliases:     []string{"yaml-to-json", "y2j"},
		Keywords:    []string{"yml", "kubernetes", "config"},
		Options:     []core.Option{optIndent()},
		Sample:      "# service config\nservice:\n  name: devknife\n  port: 7777\n  enabled: true\n  tags: [cli, web]\ndefaults: &defaults\n  retries: 3\n  timeout: 2.5s\nprod:\n  <<: *defaults\n  replicas: 2\n  owner: ~\n",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if t == "" || strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "<") || !strings.Contains(t, ":") {
				return nil
			}
			var n yaml.Node
			if err := yaml.Unmarshal([]byte(t), &n); err != nil || len(n.Content) == 0 || n.Content[0].Kind != yaml.MappingNode {
				return nil
			}
			lines := strings.Count(t, "\n") + 1
			if lines < 2 {
				return det(25, "Single-line YAML mapping")
			}
			if reYAMLBlock.MatchString(t) {
				return det(72, "YAML")
			}
			return det(55, "YAML")
		},
		Run: func(r core.Request) (*core.Result, error) {
			dec := yaml.NewDecoder(strings.NewReader(r.Input))
			conv := &yamlConv{}
			var docs []any
			for {
				var n yaml.Node
				err := dec.Decode(&n)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return nil, fmt.Errorf("invalid YAML: %s", strings.TrimPrefix(err.Error(), "yaml: "))
				}
				v, err := conv.node(&n)
				if err != nil {
					return nil, err
				}
				docs = append(docs, v)
			}
			if len(docs) == 0 {
				return nil, fmt.Errorf("input is empty")
			}
			var v any = docs[0]
			res := &core.Result{Lang: "json"}
			if len(docs) > 1 {
				v = docs
				res.Warnings = []string{fmt.Sprintf("%d YAML documents found; converted to a JSON array.", len(docs))}
			}
			res.Output = encodeOrdered(v, indentStr(r), false)
			return res, nil
		},
	}
}

func orderedToYAMLNode(v any) *yaml.Node {
	switch t := v.(type) {
	case *OMap:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range t.Keys {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, orderedToYAMLNode(t.Vals[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range t {
			n.Content = append(n.Content, orderedToYAMLNode(e))
		}
		return n
	case string:
		n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}
		if strings.Contains(t, "\n") {
			n.Style = yaml.LiteralStyle
		}
		return n
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(t.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: t.String()}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(t)}
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
}

func jsonToYAMLTool() *core.Tool {
	return &core.Tool{
		ID:          "json2yaml",
		Name:        "JSON to YAML",
		Category:    core.CatConvert,
		Description: "Convert JSON to YAML preserving key order. Strings that would change meaning (\"yes\", \"1.0\", \"null\") are quoted; multi-line strings use block style.",
		Aliases:     []string{"json-to-yaml", "j2y"},
		Keywords:    []string{"yml", "kubernetes", "config"},
		Options: []core.Option{
			{Key: "indent", Label: "Indent", Type: core.OptSelect, Default: "2", Choices: []string{"2", "4"}, Short: "i"},
		},
		Sample: `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","labels":{"app":"web"}},"spec":{"replicas":3,"template":{"spec":{"containers":[{"name":"web","image":"nginx:1.27","ports":[{"containerPort":80}],"env":[{"name":"DEBUG","value":"false"}]}]}}}}`,
		Run: func(r core.Request) (*core.Result, error) {
			v, notes, err := parseJSONInput(r.Input, true)
			if err != nil {
				return nil, err
			}
			var b budgetBuffer
			enc := yaml.NewEncoder(&b)
			enc.SetIndent(r.Int("indent", 2))
			if err := enc.Encode(orderedToYAMLNode(v)); err != nil {
				return nil, err
			}
			_ = enc.Close()
			return &core.Result{Output: b.String(), Lang: "yaml", Warnings: notes}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Number base

func numberBaseTool() *core.Tool {
	return &core.Tool{
		ID:          "base",
		Name:        "Number Base Converter",
		Category:    core.CatConvert,
		Description: "Convert integers of any size between binary, octal, decimal, hex and any base 2–36. Prefixes 0x/0b/0o are detected; underscores and spaces are ignored.",
		Aliases:     []string{"number-base", "radix", "hex2dec", "dec2hex", "bin"},
		Keywords:    []string{"binary", "octal", "hexadecimal", "decimal", "convert"},
		Options: []core.Option{
			{Key: "from", Label: "Input base", Type: core.OptSelect, Default: "auto", Choices: []string{"auto", "2", "8", "10", "16", "32", "36"}},
			{Key: "to", Label: "Custom base", Type: core.OptNumber, Default: "36", Help: "Extra output base (2–36)"},
		},
		Sample: "0x2512c02c",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			lt := strings.ToLower(t)
			switch {
			case reHexLit.MatchString(lt):
				return det(70, "Hex number")
			case reBinLit.MatchString(lt):
				return det(85, "Binary number")
			case reOctLit.MatchString(lt):
				return det(85, "Octal number")
			case reDigits.MatchString(t) && len(t) < 40:
				return det(35, "Integer")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			t := strings.ToLower(strings.TrimSpace(r.Input))
			t = strings.NewReplacer("_", "", " ", "", ",", "").Replace(t)
			if t == "" {
				return nil, fmt.Errorf("enter a number")
			}
			neg := strings.HasPrefix(t, "-")
			t = strings.TrimPrefix(t, "-")
			base := 10
			switch r.Str("from") {
			case "auto":
				switch {
				case strings.HasPrefix(t, "0x"):
					base, t = 16, t[2:]
				case strings.HasPrefix(t, "0b"):
					base, t = 2, t[2:]
				case strings.HasPrefix(t, "0o"):
					base, t = 8, t[2:]
				case strings.HasSuffix(t, "h") && isHex(t[:len(t)-1]):
					base, t = 16, t[:len(t)-1]
				case reHexLetter.MatchString(t) && isHex(t):
					base = 16
				}
			default:
				base, _ = strconv.Atoi(r.Str("from"))
				for _, p := range []string{"0x", "0b", "0o"} {
					t = strings.TrimPrefix(t, p)
				}
			}
			n, ok := new(big.Int).SetString(t, base)
			if !ok {
				return nil, fmt.Errorf("%q is not a valid base-%d number", r.Input, base)
			}
			if neg {
				n.Neg(n)
			}
			to := r.Int("to", 36)
			if to < 2 || to > 36 {
				return nil, fmt.Errorf("custom base must be between 2 and 36")
			}
			bin := n.Text(2)
			res := &core.Result{Output: n.String()}
			res.Fields = fields(
				"Binary (2)", bin,
				"Octal (8)", n.Text(8),
				"Decimal (10)", n.String(),
				"Hex (16)", n.Text(16),
				fmt.Sprintf("Base %d", to), n.Text(to),
				"Binary (grouped)", groupDigits(strings.TrimPrefix(bin, "-"), 4),
				"Bit length", strconv.Itoa(new(big.Int).Abs(n).BitLen()),
			)
			if base != 10 {
				res.Fields = append(res.Fields, core.Field{Label: "Parsed as", Value: fmt.Sprintf("base %d", base)})
			}
			return res, nil
		},
	}
}

func groupDigits(s string, n int) string {
	for len(s)%n != 0 {
		s = "0" + s
	}
	var parts []string
	for i := 0; i < len(s); i += n {
		parts = append(parts, s[i:i+n])
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// JSON <-> CSV

var delimiters = map[string]rune{"comma": ',', "semicolon": ';', "tab": '\t', "pipe": '|'}

func flatten(prefix string, v any, out *OMap) {
	switch t := v.(type) {
	case *OMap:
		if len(t.Keys) == 0 && prefix != "" {
			out.Set(prefix, "{}")
		}
		for _, k := range t.Keys {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, t.Vals[k], out)
		}
	case []any:
		out.Set(prefix, encodeOrdered(t, "", false))
	default:
		out.Set(prefix, v)
	}
}

func csvCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	return encodeOrdered(v, "", false)
}

func jsonToCSVTool() *core.Tool {
	return &core.Tool{
		ID:          "json2csv",
		Name:        "JSON to CSV",
		Category:    core.CatConvert,
		Description: "Convert a JSON array of objects to CSV. Columns are the union of all keys (in first-seen order); nested objects flatten to dot.paths.",
		Aliases:     []string{"json-to-csv"},
		Keywords:    []string{"spreadsheet", "excel", "table", "tsv"},
		Options: []core.Option{
			{Key: "delimiter", Label: "Delimiter", Type: core.OptSelect, Default: "comma", Choices: []string{"comma", "semicolon", "tab", "pipe"}, Short: "d"},
			{Key: "flatten", Label: "Flatten nested", Type: core.OptBool, Default: "true"},
			{Key: "header", Label: "Header row", Type: core.OptBool, Default: "true"},
		},
		Sample: `[{"id":1,"name":"Ada Lovelace","email":"ada@example.com","address":{"city":"London","zip":"N1"},"tags":["math","code"]},{"id":2,"name":"Grace Hopper","email":"grace@example.com","address":{"city":"New York"},"active":true}]`,
		Run: func(r core.Request) (*core.Result, error) {
			v, notes, err := parseJSONInput(r.Input, true)
			if err != nil {
				return nil, err
			}
			var rows []any
			switch t := v.(type) {
			case []any:
				rows = t
			case *OMap:
				// {"data": [...]} — use the first array-of-objects value
				for _, k := range t.Keys {
					if arr, ok := t.Vals[k].([]any); ok && len(arr) > 0 {
						if _, isObj := arr[0].(*OMap); isObj {
							rows = arr
							notes = append(notes, fmt.Sprintf("Using the array under %q.", k))
							break
						}
					}
				}
				if rows == nil {
					rows = []any{t}
				}
			default:
				return nil, fmt.Errorf("expected a JSON array of objects")
			}
			var buf bytes.Buffer
			w := csv.NewWriter(&buf)
			w.Comma = delimiters[r.Str("delimiter")]
			cols := newOMap()
			flat := make([]*OMap, 0, len(rows))
			allArrays := true
			for _, row := range rows {
				if _, ok := row.([]any); !ok {
					allArrays = false
				}
			}
			if allArrays && len(rows) > 0 {
				for _, row := range rows {
					arr := row.([]any)
					rec := make([]string, len(arr))
					for i, c := range arr {
						rec[i] = csvCell(c)
					}
					_ = w.Write(rec)
				}
				w.Flush()
				return &core.Result{Output: buf.String(), Lang: "csv", Warnings: notes, Fields: fields("Rows", strconv.Itoa(len(rows)))}, nil
			}
			for _, row := range rows {
				m := newOMap()
				switch t := row.(type) {
				case *OMap:
					if r.Bool("flatten") {
						flatten("", t, m)
					} else {
						for _, k := range t.Keys {
							m.Set(k, t.Vals[k])
						}
					}
				default:
					m.Set("value", t)
				}
				for _, k := range m.Keys {
					cols.Set(k, true)
				}
				flat = append(flat, m)
			}
			if r.Bool("header") {
				_ = w.Write(cols.Keys)
			}
			for _, m := range flat {
				rec := make([]string, len(cols.Keys))
				for i, k := range cols.Keys {
					if x, ok := m.Get(k); ok {
						rec[i] = csvCell(x)
					}
				}
				_ = w.Write(rec)
			}
			w.Flush()
			return &core.Result{Output: buf.String(), Lang: "csv", Warnings: notes, Fields: fields("Rows", strconv.Itoa(len(flat)), "Columns", strconv.Itoa(len(cols.Keys)))}, nil
		},
	}
}

func sniffDelimiter(s string) rune {
	first := strings.SplitN(s, "\n", 2)[0]
	best, bestN := ',', 0
	for _, d := range []rune{',', '\t', ';', '|'} {
		if n := strings.Count(first, string(d)); n > bestN {
			best, bestN = d, n
		}
	}
	return best
}

func inferCell(s string) any {
	switch s {
	case "":
		return nil
	case "true", "TRUE", "True":
		return true
	case "false", "FALSE", "False":
		return false
	case "null", "NULL":
		return nil
	}
	if reJSONNumLit.MatchString(s) && len(s) < 18 {
		return json.Number(s)
	}
	return s
}

func csvToJSONTool() *core.Tool {
	return &core.Tool{
		ID:          "csv2json",
		Name:        "CSV to JSON",
		Category:    core.CatConvert,
		Description: "Convert CSV/TSV to JSON. Delimiter is auto-detected; numbers, booleans and empty cells are typed; dot.path headers can rebuild nested objects.",
		Aliases:     []string{"csv-to-json", "tsv2json"},
		Keywords:    []string{"spreadsheet", "excel", "table", "tsv"},
		Options: []core.Option{
			{Key: "delimiter", Label: "Delimiter", Type: core.OptSelect, Default: "auto", Choices: []string{"auto", "comma", "semicolon", "tab", "pipe"}, Short: "d"},
			{Key: "header", Label: "First row is header", Type: core.OptBool, Default: "true"},
			{Key: "types", Label: "Infer types", Type: core.OptBool, Default: "true"},
			{Key: "nest", Label: "Nest dot.paths", Type: core.OptBool, Default: "false"},
			optIndent(),
		},
		Sample: "id,name,email,active,score\n1,Ada Lovelace,ada@example.com,true,98.5\n2,\"Hopper, Grace\",grace@example.com,false,\n3,Linus,linus@example.com,true,77",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			lines := strings.Split(t, "\n")
			if len(lines) < 2 || strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "<") {
				return nil
			}
			d := sniffDelimiter(t)
			n := strings.Count(lines[0], string(d))
			if n == 0 {
				return nil
			}
			consistent := 0
			for _, l := range lines[1:min(len(lines), 20)] {
				if strings.Count(l, string(d)) == n {
					consistent++
				}
			}
			if consistent >= (min(len(lines), 20)-1)*8/10 {
				return det(68, fmt.Sprintf("CSV (%d columns)", n+1))
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			rd := csv.NewReader(strings.NewReader(strings.TrimPrefix(r.Input, "\uFEFF")))
			if d := r.Str("delimiter"); d == "auto" {
				rd.Comma = sniffDelimiter(r.Input)
			} else {
				rd.Comma = delimiters[d]
			}
			rd.FieldsPerRecord = -1
			rd.LazyQuotes = true
			recs, err := rd.ReadAll()
			if err != nil {
				return nil, fmt.Errorf("invalid CSV: %v", err)
			}
			if len(recs) == 0 {
				return nil, fmt.Errorf("input is empty")
			}
			cell := func(s string) any {
				if r.Bool("types") {
					return inferCell(s)
				}
				return s
			}
			var out []any
			if !r.Bool("header") {
				for _, rec := range recs {
					row := make([]any, len(rec))
					for i, c := range rec {
						row[i] = cell(c)
					}
					out = append(out, row)
				}
			} else {
				hdr := recs[0]
				for _, rec := range recs[1:] {
					m := newOMap()
					for i, h := range hdr {
						var v any
						if i < len(rec) {
							v = cell(rec[i])
						}
						if r.Bool("nest") && strings.Contains(h, ".") {
							setPath(m, strings.Split(h, "."), v)
						} else {
							m.Set(h, v)
						}
					}
					out = append(out, m)
				}
			}
			return &core.Result{Output: encodeOrdered(out, indentStr(r), false), Lang: "json", Fields: fields("Rows", strconv.Itoa(len(out)), "Columns", strconv.Itoa(len(recs[0])))}, nil
		},
	}
}

func setPath(m *OMap, path []string, v any) {
	for _, p := range path[:len(path)-1] {
		next, ok := m.Get(p)
		nm, isMap := next.(*OMap)
		if !ok || !isMap {
			nm = newOMap()
			m.Set(p, nm)
		}
		m = nm
	}
	m.Set(path[len(path)-1], v)
}

// ---------------------------------------------------------------------------
// HTML -> JSX

var jsxAttrNames = map[string]string{
	"class": "className", "for": "htmlFor", "tabindex": "tabIndex", "readonly": "readOnly", "maxlength": "maxLength", "minlength": "minLength",
	"colspan": "colSpan", "rowspan": "rowSpan", "contenteditable": "contentEditable", "crossorigin": "crossOrigin", "autocomplete": "autoComplete",
	"autofocus": "autoFocus", "autoplay": "autoPlay", "enctype": "encType", "novalidate": "noValidate", "srcset": "srcSet", "srcdoc": "srcDoc", "usemap": "useMap",
	"frameborder": "frameBorder", "allowfullscreen": "allowFullScreen", "accept-charset": "acceptCharset", "http-equiv": "httpEquiv",
	"cellpadding": "cellPadding", "cellspacing": "cellSpacing", "datetime": "dateTime", "spellcheck": "spellCheck", "accesskey": "accessKey",
	"formaction": "formAction", "formmethod": "formMethod", "formnovalidate": "formNoValidate", "formtarget": "formTarget", "hreflang": "hrefLang",
	"inputmode": "inputMode", "itemprop": "itemProp", "itemscope": "itemScope", "itemtype": "itemType", "marginheight": "marginHeight",
	"marginwidth": "marginWidth", "playsinline": "playsInline", "referrerpolicy": "referrerPolicy", "charset": "charSet", "classid": "classID",
	"xlink:href": "xlinkHref", "xml:lang": "xmlLang", "xml:space": "xmlSpace", "xmlns:xlink": "xmlnsXlink", "viewbox": "viewBox",
}

var jsxEvents = []string{"click", "dblclick", "change", "input", "submit", "keydown", "keyup", "keypress", "focus", "blur", "load", "error", "scroll",
	"mouseover", "mouseout", "mouseenter", "mouseleave", "mousedown", "mouseup", "mousemove", "touchstart", "touchend", "touchmove", "contextmenu",
	"drag", "dragstart", "dragend", "dragover", "dragenter", "dragleave", "drop", "wheel", "copy", "cut", "paste", "select", "reset", "resize",
	"animationend", "transitionend", "play", "pause", "ended", "canplay", "timeupdate", "volumechange", "pointerdown", "pointerup", "pointermove"}

func camelDashes(s string) string {
	parts := strings.Split(s, "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func styleToJSX(style string) string {
	var props []string
	for _, decl := range splitTop(style, ';') {
		k, v, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		key := k
		if strings.HasPrefix(k, "--") {
			key = "'" + k + "'"
		} else {
			if strings.HasPrefix(k, "-ms-") {
				k = k[1:]
			}
			key = camelDashes(strings.TrimPrefix(k, "-"))
			if strings.HasPrefix(k, "-") {
				key = strings.ToUpper(key[:1]) + key[1:]
			}
		}
		if reDecimalLit.MatchString(v) {
			props = append(props, fmt.Sprintf("%s: %s", key, v))
		} else {
			props = append(props, fmt.Sprintf("%s: '%s'", key, strings.ReplaceAll(v, "'", `\'`)))
		}
	}
	return "{ " + strings.Join(props, ", ") + " }"
}

func htmlToJSX(src string) string {
	mode := markupMode{html: true}
	toks := tokenizeMarkup(src, mode)
	eventMap := map[string]string{}
	for _, e := range jsxEvents {
		eventMap["on"+e] = "on" + strings.ToUpper(e[:1]) + e[1:]
	}
	for k, v := range map[string]string{"ondblclick": "onDoubleClick", "onmouseover": "onMouseOver", "onmouseout": "onMouseOut", "onmouseenter": "onMouseEnter", "onmouseleave": "onMouseLeave", "onmousedown": "onMouseDown", "onmouseup": "onMouseUp", "onmousemove": "onMouseMove", "onkeydown": "onKeyDown", "onkeyup": "onKeyUp", "onkeypress": "onKeyPress", "ontouchstart": "onTouchStart", "ontouchend": "onTouchEnd", "ontouchmove": "onTouchMove", "oncontextmenu": "onContextMenu", "ondragstart": "onDragStart", "ondragend": "onDragEnd", "ondragover": "onDragOver", "ondragenter": "onDragEnter", "ondragleave": "onDragLeave", "onanimationend": "onAnimationEnd", "ontransitionend": "onTransitionEnd", "oncanplay": "onCanPlay", "ontimeupdate": "onTimeUpdate", "onvolumechange": "onVolumeChange", "onpointerdown": "onPointerDown", "onpointerup": "onPointerUp", "onpointermove": "onPointerMove"} {
		eventMap[k] = v
	}
	for i := range toks {
		t := &toks[i]
		switch t.kind {
		case mStart:
			if htmlVoid[t.lname] {
				t.self = true
			}
			for j := range t.attrs {
				a := &t.attrs[j]
				ln := strings.ToLower(a.name)
				switch {
				case ln == "style" && a.hasValue:
					t.attrs[j] = mattr{name: "style", value: "{" + styleToJSX(a.value) + "}", hasValue: true}
				case strings.HasPrefix(ln, "on") && eventMap[ln] != "":
					a.name = eventMap[ln]
					a.value = "{() => { " + strings.TrimSuffix(strings.TrimSpace(a.value), ";") + " }}"
					a.quote = 0
				case ln == "value" && (t.lname == "input" || t.lname == "textarea" || t.lname == "select"):
					a.name = "defaultValue"
				case ln == "checked" && t.lname == "input":
					a.name = "defaultChecked"
				case jsxAttrNames[ln] != "":
					a.name = jsxAttrNames[ln]
				case strings.HasPrefix(ln, "data-") || strings.HasPrefix(ln, "aria-"):
				case strings.Contains(a.name, "-") || strings.Contains(a.name, ":"):
					a.name = camelDashes(strings.ReplaceAll(a.name, ":", "-"))
				}
			}
		case mComment:
			body := strings.TrimSuffix(strings.TrimPrefix(t.raw, "<!--"), "-->")
			t.kind, t.raw = mText, "{/*"+body+"*/}"
		case mText:
			t.raw = strings.NewReplacer("{", "{'{'}", "}", "{'}'}").Replace(t.raw)
		case mDoctype:
			t.kind, t.raw = mText, ""
		}
	}
	tree := buildMarkupTree(toks, mode)
	// JSX needs a single root
	roots := 0
	for _, c := range tree.children {
		if c.tok.kind == mStart || (c.tok.kind == mText && strings.TrimSpace(c.tok.raw) != "") {
			roots++
		}
	}
	rr := &mrenderer{indent: "  ", mode: mode, wrap: 100}
	if roots > 1 { // JSX needs a single root: wrap in a fragment
		rr.line(0, "<>")
		rr.renderChildren(tree, 1)
		rr.line(0, "</>")
	} else {
		rr.renderChildren(tree, 0)
	}
	return strings.TrimRight(rr.b.String(), "\n")
}

func htmlToJSXTool() *core.Tool {
	return &core.Tool{
		ID:          "html2jsx",
		Name:        "HTML to JSX",
		Category:    core.CatConvert,
		Description: "Convert HTML to JSX: class→className, for→htmlFor, inline styles to objects, event handlers to camelCase, SVG attributes, self-closing void tags, comments, and an optional component wrapper.",
		Aliases:     []string{"jsx", "html-to-jsx", "react"},
		Keywords:    []string{"react", "component", "className", "tsx"},
		Options: []core.Option{
			{Key: "component", Label: "Wrap in component", Type: core.OptBool, Default: "false"},
			{Key: "name", Label: "Component name", Type: core.OptString, Default: "MyComponent"},
		},
		Sample: `<!-- Login form -->
<form class="login" onsubmit="handleSubmit(event)">
  <label for="email" style="font-weight: bold; margin-bottom: 4px">Email</label>
  <input id="email" type="email" tabindex="1" value="ada@example.com" required>
  <svg viewBox="0 0 24 24" stroke-width="2" fill-rule="evenodd"><path d="M5 12h14"/></svg>
  <button class="btn btn-primary" onclick="track('login')" disabled>Sign in</button>
</form>`,
		Run: func(r core.Request) (*core.Result, error) {
			if strings.TrimSpace(r.Input) == "" {
				return nil, fmt.Errorf("input is empty")
			}
			out := htmlToJSX(r.Input)
			if r.Bool("component") {
				name := r.Str("name")
				if name == "" {
					name = "MyComponent"
				}
				ind := "    " + strings.ReplaceAll(out, "\n", "\n    ")
				out = fmt.Sprintf("export default function %s() {\n  return (\n%s\n  );\n}\n", name, ind)
			}
			return &core.Result{Output: out, Lang: "jsx"}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// String case

// splitWords splits identifiers and phrases into words: camelCase, PascalCase,
// snake_case, kebab-case, acronyms (URLDecoder → URL Decoder) and spaces.
func splitWords(s string) []string {
	var words []string
	var cur []rune
	rs := []rune(s)
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	for i, c := range rs {
		switch {
		case c == '_' || c == '-' || c == '.' || c == '/' || c == '\\' || unicode.IsSpace(c) || (unicode.IsPunct(c) && c != '\''):
			flush()
		case unicode.IsUpper(c):
			if len(cur) > 0 {
				prev := cur[len(cur)-1]
				nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					flush()
				}
			}
			cur = append(cur, c)
		case c == '\'':
			// drop apostrophes: don't → dont
		default:
			cur = append(cur, c)
		}
	}
	flush()
	return words
}

func capWord(w string) string {
	rs := []rune(strings.ToLower(w))
	if len(rs) == 0 {
		return w
	}
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

var caseChoices = []string{"camelCase", "PascalCase", "snake_case", "CONSTANT_CASE", "kebab-case", "COBOL-CASE", "Train-Case", "dot.case", "path/case", "Title Case", "Sentence case", "lower case", "UPPER CASE", "flatcase"}

func convertCase(s, to string) string {
	ws := splitWords(s)
	if len(ws) == 0 {
		return s
	}
	lower := make([]string, len(ws))
	caps := make([]string, len(ws))
	for i, w := range ws {
		lower[i] = strings.ToLower(w)
		caps[i] = capWord(w)
	}
	switch to {
	case "camelCase":
		return lower[0] + strings.Join(caps[1:], "")
	case "PascalCase":
		return strings.Join(caps, "")
	case "snake_case":
		return strings.Join(lower, "_")
	case "CONSTANT_CASE":
		return strings.ToUpper(strings.Join(lower, "_"))
	case "kebab-case":
		return strings.Join(lower, "-")
	case "COBOL-CASE":
		return strings.ToUpper(strings.Join(lower, "-"))
	case "Train-Case":
		return strings.Join(caps, "-")
	case "dot.case":
		return strings.Join(lower, ".")
	case "path/case":
		return strings.Join(lower, "/")
	case "Title Case":
		return strings.Join(caps, " ")
	case "Sentence case":
		return capWord(lower[0]) + " " + strings.Join(lower[1:], " ")
	case "lower case":
		return strings.Join(lower, " ")
	case "UPPER CASE":
		return strings.ToUpper(strings.Join(lower, " "))
	case "flatcase":
		return strings.Join(lower, "")
	}
	return s
}

func caseTool() *core.Tool {
	return &core.Tool{
		ID:          "case",
		Name:        "String Case Converter",
		Category:    core.CatConvert,
		Description: "Convert each line between camelCase, PascalCase, snake_case, CONSTANT_CASE, kebab-case, Title Case and more. Acronyms (requestURLDecoderID) split correctly.",
		Aliases:     []string{"string-case", "camel", "snake", "kebab"},
		Keywords:    []string{"camelcase", "pascalcase", "snakecase", "kebabcase", "uppercase", "lowercase", "title"},
		Args:        []string{"?to", "input"},
		Options: []core.Option{
			{Key: "to", Label: "Convert to", Type: core.OptSelect, Default: "camelCase", Choices: caseChoices, Short: "t"},
		},
		Sample: "requestURLDecoderID\nHTTP_CLIENT_FACTORY\ngeneric_activity_highlights\nRankedOrganicTweet\nsuggest-activity-feed\nThis is a normal sentence",
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if isSingleLine(t) && len(t) < 80 && reIdentCase.MatchString(t) {
				return det(60, "Identifier")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			lines := strings.Split(r.Input, "\n")
			for i, l := range lines {
				lines[i] = convertCase(strings.TrimRight(l, "\r"), r.Str("to"))
			}
			res := &core.Result{Output: strings.Join(lines, "\n")}
			// show every variant for single-line input — handy for picking one
			if len(lines) == 1 && strings.TrimSpace(lines[0]) != "" {
				for _, c := range caseChoices {
					res.Fields = append(res.Fields, core.Field{Label: c, Value: convertCase(r.Input, c)})
				}
			}
			return res, nil
		},
	}
}

// ---------------------------------------------------------------------------
// SVG -> CSS

func svgDataURI(svg string, b64 bool) string {
	if b64 {
		return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
	}
	// "mini SVG data URI": swap double quotes for single, collapse whitespace
	// and only escape what must be escaped — far shorter than base64.
	s := reWS.ReplaceAllString(strings.TrimSpace(svg), " ")
	s = strings.ReplaceAll(s, "> <", "><")
	s = strings.ReplaceAll(s, `"`, "'")
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '%' || c == '#' || c == '<' || c == '>' || c == '{' || c == '}' || c == '|' || c == '\\' || c == '^' || c == '`' || c == '"' || c < 0x20:
			fmt.Fprintf(&b, "%%%02X", c)
		case c > 127:
			b.WriteString(url.PathEscape(string(c)))
		default:
			b.WriteRune(c)
		}
	}
	return "data:image/svg+xml," + b.String()
}

func svgToCSSTool() *core.Tool {
	return &core.Tool{
		ID:          "svg2css",
		Name:        "SVG to CSS",
		Category:    core.CatConvert,
		Description: "Turn SVG markup into a CSS background-image data URI (compact URL-encoding or Base64), with live preview and optional minification.",
		Aliases:     []string{"svg-to-css", "svg-data-uri"},
		Keywords:    []string{"data uri", "background-image", "icon", "inline svg"},
		Options: []core.Option{
			{Key: "encoding", Label: "Encoding", Type: core.OptSelect, Default: "url", Choices: []string{"url", "base64"}},
			{Key: "minify", Label: "Minify SVG", Type: core.OptBool, Default: "true"},
			// not "output": that is the CLI's global -o/--output flag
			{Key: "format", Label: "Output", Type: core.OptSelect, Default: "background", Choices: []string{"background", "mask", "data-uri", "img"}},
		},
		Sample: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="48" height="48">
  <!-- heart -->
  <path fill="#d40000" d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z"/>
</svg>`,
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if (strings.HasPrefix(t, "<svg") || strings.HasPrefix(t, "<?xml") && strings.Contains(t, "<svg")) && strings.HasSuffix(t, "</svg>") {
				return det(88, "SVG markup")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			svg := strings.TrimSpace(r.Input)
			if !strings.Contains(svg, "<svg") {
				return nil, fmt.Errorf("input does not contain an <svg> element")
			}
			if i := strings.Index(svg, "<svg"); i > 0 {
				svg = svg[i:] // drop XML prolog / doctype
			}
			orig := len(svg)
			if r.Bool("minify") {
				if m, err := minifier.String("image/svg+xml", svg); err == nil {
					svg = m
				}
			}
			if !strings.Contains(svg, "xmlns") {
				svg = strings.Replace(svg, "<svg", `<svg xmlns="http://www.w3.org/2000/svg"`, 1)
			}
			uri := svgDataURI(svg, r.Str("encoding") == "base64")
			var out string
			lang := "css"
			switch r.Str("format") {
			case "mask":
				out = fmt.Sprintf("-webkit-mask-image: url(\"%s\");\nmask-image: url(\"%s\");\nmask-repeat: no-repeat;\nbackground-color: currentColor;", uri, uri)
			case "data-uri":
				out, lang = uri, ""
			case "img":
				out, lang = fmt.Sprintf(`<img src="%s" alt="" />`, strings.ReplaceAll(uri, `"`, "%22")), "html"
			default:
				out = fmt.Sprintf("background-image: url(\"%s\");\nbackground-repeat: no-repeat;", uri)
			}
			return &core.Result{Output: out, Lang: lang, Image: svgDataURI(svg, true), View: "image", Fields: fields("SVG size", sizeDelta(orig, len(svg)), "Data URI length", strconv.Itoa(len(uri)))}, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Hex <-> ASCII

func hexToASCIITool() *core.Tool {
	return &core.Tool{
		ID:          "hex2ascii",
		Name:        "Hex to ASCII",
		Category:    core.CatConvert,
		Description: "Decode hex bytes to text. Accepts spaces, colons, 0x and \\x prefixes; non-printable bytes are shown as escapes.",
		Aliases:     []string{"hex-to-ascii", "unhex", "hex-decode"},
		Keywords:    []string{"hexadecimal", "bytes", "decode", "text"},
		Sample:      "48 65 6c 6c 6f 2c 20 64 65 76 6b 6e 69 66 65 21 20 f0 9f 9a 80",
		Detect: func(in string) *core.Detection {
			c := cleanHex(in)
			if len(c) >= 6 && len(c)%2 == 0 && isHex(c) && !reDigits.MatchString(c) {
				b, _ := hex.DecodeString(c)
				if printableRatio(string(b)) > 0.9 {
					return det(70, "Hex → "+truncate(string(b), 30))
				}
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			c := cleanHex(r.Input)
			if len(c)%2 == 1 {
				c = "0" + c
			}
			b, err := hex.DecodeString(c)
			if err != nil {
				return nil, fmt.Errorf("invalid hex: %v", err)
			}
			res := &core.Result{Fields: fields("Bytes", strconv.Itoa(len(b)))}
			if strings.ToValidUTF8(string(b), "") == string(b) {
				res.Output = string(b)
			} else {
				var sb strings.Builder
				for _, x := range b {
					if x >= 0x20 && x < 0x7f || x == '\n' || x == '\t' {
						sb.WriteByte(x)
					} else {
						fmt.Fprintf(&sb, `\x%02x`, x)
					}
				}
				res.Output = sb.String()
				res.Warnings = []string{"Not valid UTF-8; non-printable bytes shown as \\xHH."}
			}
			return res, nil
		},
	}
}

func cleanHex(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("0x", "", `\x`, "", " ", "", ":", "", "-", "", "\n", "", "\r", "", "\t", "", ",", "").Replace(s)
	return s
}

func asciiToHexTool() *core.Tool {
	return &core.Tool{
		ID:          "ascii2hex",
		Name:        "ASCII to Hex",
		Category:    core.CatConvert,
		Description: "Encode text (UTF-8) as hex bytes with your choice of separator and prefix.",
		Aliases:     []string{"ascii-to-hex", "hex-encode", "tohex"},
		Keywords:    []string{"hexadecimal", "bytes", "encode"},
		Options: []core.Option{
			{Key: "separator", Label: "Format", Type: core.OptSelect, Default: "space", Choices: []string{"none", "space", "colon", "0x", `\x`, "comma"}},
			{Key: "upper", Label: "Uppercase", Type: core.OptBool, Default: "false"},
		},
		Sample: "Hello, devknife! 🚀",
		Run: func(r core.Request) (*core.Result, error) {
			data := []byte(r.Input)
			if len(r.Data) > 0 {
				data = r.Data
			}
			parts := make([]string, len(data))
			for i, x := range data {
				h := fmt.Sprintf("%02x", x)
				if r.Bool("upper") {
					h = strings.ToUpper(h)
				}
				parts[i] = h
			}
			var out string
			switch r.Str("separator") {
			case "none":
				out = strings.Join(parts, "")
			case "colon":
				out = strings.Join(parts, ":")
			case "0x":
				out = "0x" + strings.Join(parts, " 0x")
			case `\x`:
				out = `\x` + strings.Join(parts, `\x`)
			case "comma":
				out = "0x" + strings.Join(parts, ", 0x")
			default:
				out = strings.Join(parts, " ")
			}
			if len(data) == 0 {
				out = ""
			}
			return &core.Result{Output: out, Fields: fields("Bytes", strconv.Itoa(len(data)))}, nil
		},
	}
}

func convertTools() []*core.Tool {
	return []*core.Tool{urlParserTool(), yamlToJSONTool(), jsonToYAMLTool(), numberBaseTool(), jsonToCSVTool(), csvToJSONTool(), htmlToJSXTool(), caseTool(), svgToCSSTool(), curlTool(), jsonToCodeTool(), hexToASCIITool(), asciiToHexTool()}
}

// Compiled once: detectors run on every paste, tools on every keystroke.
var (
	reYAMLBlock  = regexp.MustCompile(`(?m)^\s*[\w.-]+:\s*$|^\s*-\s+\S|^---`)
	reHexLit     = regexp.MustCompile(`^0x[0-9a-f_]+$`)
	reBinLit     = regexp.MustCompile(`^0b[01_]+$`)
	reOctLit     = regexp.MustCompile(`^0o[0-7_]+$`)
	reHexLetter  = regexp.MustCompile(`[a-f]`)
	reJSONNumLit = regexp.MustCompile(`^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$`)
	reDecimalLit = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	reIdentCase  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*([_-][A-Za-z0-9]+)+$|^[a-z]+([A-Z][a-z0-9]+)+$|^([A-Z][a-z0-9]+){2,}$`)
)
