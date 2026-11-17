package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"devknife/internal/core"
)

func jsonFormatTool() *core.Tool {
	return &core.Tool{
		ID:          "json",
		Name:        "JSON Format/Validate",
		Category:    core.CatFormat,
		Description: "Pretty-print, minify or validate JSON with exact error locations. Lenient mode accepts comments, trailing commas, single quotes and bare keys; stringified JSON is unwrapped automatically.",
		Aliases:     []string{"json-format", "jf"},
		Keywords:    []string{"pretty", "prettify", "beautify", "lint", "validate", "minify", "json5"},
		Options: []core.Option{
			optMode("format", "format", "minify", "validate"),
			optIndent(),
			{Key: "sort", Label: "Sort keys", Type: core.OptBool, Default: "false", Short: "s"},
			{Key: "lenient", Label: "Lenient", Type: core.OptBool, Default: "true", Help: "Accept JSON5-style comments, trailing commas, single quotes and bare keys"},
			{Key: "path", Label: "JSON path", Type: core.OptString, Default: "", Placeholder: ".users[*].name", Help: "Only show what matches: .key, [0], [-1], [*], ..key, [1:3], length, keys()", Short: "q"},
		},
		Sample: `{"name":"devknife","version":"1.0.0","tags":["cli","web"],"config":{"port":7777,"debug":false,"ratio":0.75},"owner":null}`,
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if !(strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) {
				if strings.HasPrefix(t, `"{`) || strings.HasPrefix(t, `"[`) {
					if v, _, err := parseJSONInput(t, false); err == nil {
						if _, isStr := v.(string); !isStr {
							return det(85, "Stringified JSON")
						}
					}
				}
				return nil
			}
			if json.Valid([]byte(t)) {
				if strings.Contains(t, "\n") {
					return det(80, "JSON")
				}
				return det(90, "Minified JSON")
			}
			if _, _, err := parseJSONInput(t, true); err == nil {
				return det(70, "JSON-like (lenient)")
			}
			return det(30, "Looks like broken JSON")
		},
		Run: func(r core.Request) (*core.Result, error) {
			v, notes, err := parseJSONInput(r.Input, r.Bool("lenient"))
			if err != nil {
				return nil, err
			}
			res := &core.Result{Lang: "json", Warnings: notes}
			if p := strings.TrimSpace(r.Str("path")); p != "" && p != "." && p != "$" {
				out, multi, err := queryJSON(v, p)
				if err != nil {
					return nil, fmt.Errorf("JSON path: %v", err)
				}
				if multi {
					v = out
				} else if len(out) == 1 {
					v = out[0]
				} else {
					v = nil
					res.Warnings = append(res.Warnings, "JSON path matched nothing")
				}
			}
			switch r.Str("mode") {
			case "minify":
				res.Output = encodeOrdered(v, "", r.Bool("sort"))
			case "validate":
				res.Output = "✓ Valid JSON"
				res.Lang = ""
			default:
				res.Output = encodeOrdered(v, indentStr(r), r.Bool("sort"))
			}
			keys, depth, nodes := jsonStats(v, 1)
			res.Fields = fields(
				"Type", jsonType(v),
				"Size", sizeDelta(len(r.Input), len(res.Output)),
				"Depth", strconv.Itoa(depth),
				"Keys", strconv.Itoa(keys),
				"Values", strconv.Itoa(nodes),
			)
			if r.Str("mode") == "validate" {
				res.Fields[1] = core.Field{Label: "Size", Value: humanBytes(len(r.Input))}
			}
			return res, nil
		},
	}
}

func jsonType(v any) string {
	switch t := v.(type) {
	case *OMap:
		return fmt.Sprintf("object (%d keys)", len(t.Keys))
	case []any:
		return fmt.Sprintf("array (%d items)", len(t))
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return "unknown"
}

func jsonStats(v any, d int) (keys, depth, nodes int) {
	depth = d
	nodes = 1
	switch t := v.(type) {
	case *OMap:
		keys += len(t.Keys)
		for _, k := range t.Keys {
			k2, d2, n2 := jsonStats(t.Vals[k], d+1)
			keys += k2
			nodes += n2
			depth = max(depth, d2)
		}
	case []any:
		for _, e := range t {
			k2, d2, n2 := jsonStats(e, d+1)
			keys += k2
			nodes += n2
			depth = max(depth, d2)
		}
	}
	return
}

// ---------------------------------------------------------------------------
// JSON Query: a forgiving JSONPath subset. Not in DevUtils, but it is the
// thing you reach for right after formatting a big API response.

func jsonQueryTool() *core.Tool {
	return &core.Tool{
		ID:          "json-query",
		Name:        "JSON Query",
		Category:    core.CatInspect,
		Description: "Extract values from JSON with a path: .users[0].name, users[*].email, $..id, items[-1], items[1:3], items.length.",
		Aliases:     []string{"jq", "jsonpath"},
		Keywords:    []string{"jsonpath", "path", "select", "extract", "filter"},
		Extra:       true,
		Args:        []string{"path", "input"},
		Options: []core.Option{
			{Key: "path", Label: "Path", Type: core.OptString, Default: "", Placeholder: ".users[*].name", Short: "q"},
			{Key: "raw", Label: "Raw strings", Type: core.OptBool, Default: "false", Help: "Print string results without quotes, one per line", Short: "r"},
		},
		Sample: `{"users":[{"id":1,"name":"Ada","email":"ada@example.com","roles":["admin"]},{"id":2,"name":"Linus","email":"linus@example.com","roles":["dev","ops"]}],"total":2}`,
		Run: func(r core.Request) (*core.Result, error) {
			v, notes, err := parseJSONInput(r.Input, true)
			if err != nil {
				return nil, err
			}
			path := strings.TrimSpace(r.Str("path"))
			results, multi, err := queryJSON(v, path)
			if err != nil {
				return nil, err
			}
			res := &core.Result{Lang: "json", Warnings: notes}
			if r.Bool("raw") {
				var lines []string
				for _, x := range results {
					if s, ok := x.(string); ok {
						lines = append(lines, s)
					} else {
						lines = append(lines, encodeOrdered(x, "", false))
					}
				}
				res.Output, res.Lang = strings.Join(lines, "\n"), ""
			} else if multi {
				res.Output = encodeOrdered(results, "  ", false)
			} else if len(results) == 1 {
				res.Output = encodeOrdered(results[0], "  ", false)
			} else {
				res.Output = "null"
			}
			res.Fields = fields("Matches", strconv.Itoa(len(results)))
			return res, nil
		},
	}
}

type pathStep struct {
	kind  string // key, index, wildcard, recurse, slice, length, keys
	key   string
	index int
	from  *int
	to    *int
}

func parseJSONPath(p string) ([]pathStep, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "$")
	var steps []pathStep
	i := 0
	for i < len(p) {
		switch c := p[i]; {
		case c == '.' && i+1 < len(p) && p[i+1] == '.':
			i += 2
			j := i
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			steps = append(steps, pathStep{kind: "recurse", key: p[i:j]})
			i = j
		case c == '.':
			i++
		case c == '[':
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed [ in path at position %d", i)
			}
			inner := strings.TrimSpace(p[i+1 : i+end])
			i += end + 1
			switch {
			case inner == "*":
				steps = append(steps, pathStep{kind: "wildcard"})
			case len(inner) >= 2 && (inner[0] == '"' || inner[0] == '\''):
				steps = append(steps, pathStep{kind: "key", key: inner[1 : len(inner)-1]})
			case strings.Contains(inner, ":"):
				parts := strings.SplitN(inner, ":", 2)
				st := pathStep{kind: "slice"}
				if s := strings.TrimSpace(parts[0]); s != "" {
					n, err := strconv.Atoi(s)
					if err != nil {
						return nil, fmt.Errorf("bad slice start %q", s)
					}
					st.from = &n
				}
				if s := strings.TrimSpace(parts[1]); s != "" {
					n, err := strconv.Atoi(s)
					if err != nil {
						return nil, fmt.Errorf("bad slice end %q", s)
					}
					st.to = &n
				}
				steps = append(steps, st)
			default:
				n, err := strconv.Atoi(inner)
				if err != nil {
					steps = append(steps, pathStep{kind: "key", key: inner})
				} else {
					steps = append(steps, pathStep{kind: "index", index: n})
				}
			}
		default:
			j := i
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			key := p[i:j]
			switch key {
			case "*":
				steps = append(steps, pathStep{kind: "wildcard"})
			case "length", "len()", "length()":
				steps = append(steps, pathStep{kind: "length", key: key})
			case "keys()":
				steps = append(steps, pathStep{kind: "keys"})
			default:
				steps = append(steps, pathStep{kind: "key", key: key})
			}
			i = j
		}
	}
	return steps, nil
}

// queryJSON evaluates the path; multi reports whether the path can yield
// several values (so results are shown as an array).
func queryJSON(root any, path string) (out []any, multi bool, err error) {
	steps, err := parseJSONPath(path)
	if err != nil {
		return nil, false, err
	}
	cur := []any{root}
	for _, st := range steps {
		var next []any
		for _, v := range cur {
			switch st.kind {
			case "key":
				if m, ok := v.(*OMap); ok {
					if x, ok := m.Get(st.key); ok {
						next = append(next, x)
					}
				} else if arr, ok := v.([]any); ok && !multi {
					// convenience: users.name on an array maps over items
					for _, e := range arr {
						if m, ok := e.(*OMap); ok {
							if x, ok := m.Get(st.key); ok {
								next = append(next, x)
							}
						}
					}
					multi = true
				}
			case "index":
				if arr, ok := v.([]any); ok {
					idx := st.index
					if idx < 0 {
						idx += len(arr)
					}
					if idx >= 0 && idx < len(arr) {
						next = append(next, arr[idx])
					}
				}
			case "wildcard":
				multi = true
				switch t := v.(type) {
				case []any:
					next = append(next, t...)
				case *OMap:
					for _, k := range t.Keys {
						next = append(next, t.Vals[k])
					}
				}
			case "slice":
				multi = true
				if arr, ok := v.([]any); ok {
					from, to := 0, len(arr)
					if st.from != nil {
						from = *st.from
						if from < 0 {
							from += len(arr)
						}
					}
					if st.to != nil {
						to = *st.to
						if to < 0 {
							to += len(arr)
						}
					}
					from = max(0, min(from, len(arr)))
					to = max(from, min(to, len(arr)))
					next = append(next, arr[from:to]...)
				}
			case "recurse":
				multi = true
				walkJSON(v, func(x any) {
					if st.key == "" || st.key == "*" {
						next = append(next, x)
						return
					}
					if m, ok := x.(*OMap); ok {
						if y, ok := m.Get(st.key); ok {
							next = append(next, y)
						}
					}
				})
			case "length":
				switch t := v.(type) {
				case []any:
					next = append(next, json.Number(strconv.Itoa(len(t))))
				case *OMap:
					if x, ok := t.Get(st.key); ok { // a real "length" key wins
						next = append(next, x)
					} else {
						next = append(next, json.Number(strconv.Itoa(len(t.Keys))))
					}
				case string:
					next = append(next, json.Number(strconv.Itoa(len([]rune(t)))))
				}
			case "keys":
				if m, ok := v.(*OMap); ok {
					ks := make([]any, len(m.Keys))
					for i, k := range m.Keys {
						ks[i] = k
					}
					next = append(next, ks)
				}
			}
		}
		cur = next
	}
	return cur, multi, nil
}

func walkJSON(v any, fn func(any)) {
	fn(v)
	switch t := v.(type) {
	case *OMap:
		for _, k := range t.Keys {
			walkJSON(t.Vals[k], fn)
		}
	case []any:
		for _, e := range t {
			walkJSON(e, fn)
		}
	}
}
