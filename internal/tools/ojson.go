package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"devknife/internal/core"
)

// OMap is a JSON object that remembers key order. Most converters care about
// order (YAML, CSV columns, generated structs), and Go maps lose it.
type OMap struct {
	Keys []string
	Vals map[string]any
}

func newOMap() *OMap { return &OMap{Vals: map[string]any{}} }

// Set inserts or replaces a key, keeping first-insertion order.
func (m *OMap) Set(k string, v any) {
	if _, ok := m.Vals[k]; !ok {
		m.Keys = append(m.Keys, k)
	}
	m.Vals[k] = v
}

// Get returns a value.
func (m *OMap) Get(k string) (any, bool) { v, ok := m.Vals[k]; return v, ok }

// maxJSONDepth bounds nesting in every structured value devknife builds
// (JSON, YAML). Each level is a recursive call in the decoder, the encoders
// and the converters; without a bound, a few MB of "[[[[…" overflows the
// goroutine stack, which Go reports as a fatal error that recover() cannot
// catch, so the whole app exits. Pretty-printed output also grows with
// depth², so the bound keeps a 2 KB input from turning into 8 MB of indent.
// Python's json module stops near the same depth; real documents rarely pass 100.
const maxJSONDepth = 1000

var errTooDeep = fmt.Errorf("nesting is deeper than %d levels", maxJSONDepth)

// decodeOrdered parses JSON into OMap / []any / string / json.Number / bool / nil.
func decodeOrdered(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after top-level value (offset %d)", dec.InputOffset())
	}
	return v, nil
}

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if depth >= maxJSONDepth {
			return nil, errTooDeep
		}
		switch t {
		case '{':
			m := newOMap()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("object key must be a string")
				}
				v, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				m.Set(k, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return m, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %q", t)
	default:
		return t, nil
	}
}

// encodeOrdered writes ordered values back to JSON. indent "" means compact.
func encodeOrdered(v any, indent string, sortKeys bool) string {
	var b strings.Builder
	writeOrdered(&b, v, indent, "", sortKeys)
	return b.String()
}

func writeOrdered(b *strings.Builder, v any, indent, prefix string, sortKeys bool) {
	nl := func(p string) {
		if indent != "" {
			core.CheckOutput(b.Len())
			b.WriteByte('\n')
			b.WriteString(p)
		}
	}
	switch t := v.(type) {
	case *OMap:
		if len(t.Keys) == 0 {
			b.WriteString("{}")
			return
		}
		keys := t.Keys
		if sortKeys {
			keys = append([]string(nil), keys...)
			sort.Strings(keys)
		}
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(prefix + indent)
			b.WriteString(jsonString(k))
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			writeOrdered(b, t.Vals[k], indent, prefix+indent, sortKeys)
		}
		nl(prefix)
		b.WriteByte('}')
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(prefix + indent)
			writeOrdered(b, e, indent, prefix+indent, sortKeys)
		}
		nl(prefix)
		b.WriteByte(']')
	case string:
		b.WriteString(jsonString(t))
	case json.Number:
		b.WriteString(t.String())
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	case map[string]any:
		m := newOMap()
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			m.Set(k, t[k])
		}
		writeOrdered(b, m, indent, prefix, sortKeys)
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			b.WriteString(jsonString(fmt.Sprint(t)))
			return
		}
		b.Write(raw)
	}
}

// jsonString quotes s as JSON without escaping <, > and & (unlike json.Marshal).
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(b.String(), "\n")
}

// toPlain converts ordered values into plain Go values (map[string]any etc).
func toPlain(v any) any {
	switch t := v.(type) {
	case *OMap:
		m := make(map[string]any, len(t.Keys))
		for _, k := range t.Keys {
			m[k] = toPlain(t.Vals[k])
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = toPlain(e)
		}
		return out
	}
	return v
}

// ---------------------------------------------------------------------------
// Lenient JSON: accept what people actually paste (JS object literals, config
// files with comments) and normalise it to strict JSON. A position map lets us
// report errors against the user's original text.

type relaxed struct {
	out []byte
	pos []int // pos[i] = offset in source of out[i]
}

func (r *relaxed) emit(b byte, at int) { r.out = append(r.out, b); r.pos = append(r.pos, at) }

// relaxJSON converts JSON5-ish input to strict JSON: strips // and /* */
// comments, trailing commas, converts single-quoted strings, quotes bare keys,
// and maps undefined/NaN/Infinity to null.
func relaxJSON(src string) (*relaxed, error) {
	r := &relaxed{}
	i := 0
	n := len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated /* comment")
			}
			i += end + 4
		case c == '#' && lineStartsWith(src, i):
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'':
			quote := c
			start := i
			r.emit('"', i)
			i++
			for i < n && src[i] != quote {
				if src[i] == '\\' && i+1 < n {
					if src[i+1] == '\'' { // \' is not valid JSON
						r.emit('\'', i)
						i += 2
						continue
					}
					r.emit(src[i], i)
					r.emit(src[i+1], i+1)
					i += 2
					continue
				}
				if src[i] == '"' { // a double quote inside a single-quoted string
					r.emit('\\', i)
				}
				if src[i] == '\n' {
					r.emit('\\', i)
					r.emit('n', i)
					i++
					continue
				}
				r.emit(src[i], i)
				i++
			}
			if i >= n {
				return nil, fmt.Errorf("unterminated string starting at %s", lineCol(src, start))
			}
			r.emit('"', i)
			i++
		case c == ',':
			// drop trailing commas
			j := skipSpaceAndComments(src, i+1)
			if j < n && (src[j] == '}' || src[j] == ']') {
				i++
				continue
			}
			r.emit(c, i)
			i++
		case isIdentStart(c):
			j := i
			for j < n && isIdentPart(src[j]) {
				j++
			}
			word := src[i:j]
			k := skipSpaceAndComments(src, j)
			switch {
			case k < n && src[k] == ':' && !isJSONLiteral(word):
				r.emit('"', i)
				for x := i; x < j; x++ {
					r.emit(src[x], x)
				}
				r.emit('"', j-1)
			case word == "undefined" || word == "NaN" || word == "Infinity" || word == "None":
				for x, ch := range []byte("null") {
					_ = x
					r.emit(ch, i)
				}
			case word == "True" || word == "False":
				for _, ch := range []byte(strings.ToLower(word)) {
					r.emit(ch, i)
				}
			default:
				for x := i; x < j; x++ {
					r.emit(src[x], x)
				}
			}
			i = j
		default:
			r.emit(c, i)
			i++
		}
	}
	return r, nil
}

func lineStartsWith(src string, i int) bool {
	for j := i - 1; j >= 0; j-- {
		switch src[j] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		default:
			return false
		}
	}
	return true
}

func isJSONLiteral(w string) bool { return w == "true" || w == "false" || w == "null" }

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-' }

func skipSpaceAndComments(src string, i int) int {
	for i < len(src) {
		switch {
		case src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r':
			i++
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return len(src)
			}
			i += end + 4
		default:
			return i
		}
	}
	return i
}

// lineCol renders a byte offset as "line L, column C".
func lineCol(src string, off int) string {
	if off > len(src) {
		off = len(src)
	}
	if off < 0 {
		off = 0
	}
	line := 1 + strings.Count(src[:off], "\n")
	lastNL := strings.LastIndexByte(src[:off], '\n')
	col := utf8.RuneCountInString(src[lastNL+1:off]) + 1
	return fmt.Sprintf("line %d, column %d", line, col)
}

// errorContext shows the offending line with a caret under the error column.
func errorContext(src string, off int) string {
	if off > len(src) {
		off = len(src)
	}
	if off < 0 {
		off = 0
	}
	start := strings.LastIndexByte(src[:off], '\n') + 1
	end := strings.IndexByte(src[off:], '\n')
	if end < 0 {
		end = len(src)
	} else {
		end += off
	}
	line := src[start:end]
	col := utf8.RuneCountInString(src[start:off])
	// keep very long (minified) lines readable
	runes := []rune(line)
	if len(runes) > 80 {
		from := col - 40
		if from < 0 {
			from = 0
		}
		to := from + 80
		if to > len(runes) {
			to = len(runes)
		}
		line = string(runes[from:to])
		col -= from
	}
	line = strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return '·'
		}
		return r
	}, line)
	return line + "\n" + strings.Repeat(" ", col) + "^"
}

// jsonError turns a decoding error into a human message with position + caret.
func jsonError(src string, err error, posMap []int) error {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	off := -1
	switch {
	case errors.As(err, &se):
		off = int(se.Offset) - 1
	case errors.As(err, &te):
		off = int(te.Offset) - 1
	}
	if off < 0 {
		return err
	}
	if posMap != nil {
		if off >= len(posMap) {
			off = len(src)
		} else {
			off = posMap[off]
		}
	}
	msg := strings.TrimPrefix(err.Error(), "json: ")
	if errors.As(err, &se) && strings.Contains(msg, "unexpected end of JSON input") {
		msg = "unexpected end of input (missing closing bracket or quote?)"
		off = len(strings.TrimRight(src, " \n\r\t"))
	}
	return fmt.Errorf("invalid JSON at %s: %s\n\n%s", lineCol(src, off), msg, errorContext(src, off))
}

// parseJSONInput parses (optionally lenient) JSON into ordered values,
// returning precise errors. It also unwraps stringified JSON ("{\"a\":1}").
func parseJSONInput(in string, lenient bool) (v any, notes []string, err error) {
	src := strings.TrimPrefix(strings.TrimSpace(in), "\uFEFF")
	if src == "" {
		return nil, nil, fmt.Errorf("input is empty")
	}
	data := []byte(src)
	var posMap []int
	if !json.Valid(data) && lenient {
		if r, rerr := relaxJSON(src); rerr == nil && json.Valid(r.out) {
			data, posMap = r.out, r.pos
			notes = append(notes, "Input was not strict JSON; normalised comments, quotes, bare keys or trailing commas.")
		} else if rerr == nil {
			data, posMap = r.out, r.pos
		}
	}
	v, err = decodeOrdered(data)
	if errors.Is(err, errTooDeep) {
		return nil, nil, fmt.Errorf("JSON %w", errTooDeep)
	}
	if err != nil {
		var plain any
		if uerr := json.Unmarshal(data, &plain); uerr != nil {
			return nil, nil, jsonError(src, uerr, posMap)
		}
		return nil, nil, err
	}
	if s, ok := v.(string); ok {
		inner := strings.TrimSpace(s)
		if (strings.HasPrefix(inner, "{") || strings.HasPrefix(inner, "[")) && json.Valid([]byte(inner)) {
			v2, err2 := decodeOrdered([]byte(inner))
			if err2 == nil {
				notes = append(notes, "Unwrapped stringified JSON.")
				return v2, notes, nil
			}
		}
	}
	return v, notes, nil
}
