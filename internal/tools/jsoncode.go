package tools

import (
	"encoding/json"
	"fmt"
	"go/format"
	"regexp"
	"strings"
	"unicode"

	"devknife/internal/core"
)

// A small quicktype: infer a schema from JSON samples (merging every element
// of arrays so optional and nullable fields are detected) and emit types.

type jtype struct {
	kind     string // null, bool, int, float, string, object, array, any
	fields   []*jfield
	elem     *jtype
	name     string
	nullable bool
	samples  int // how many objects were merged (for optional detection)
}

type jfield struct {
	key      string
	t        *jtype
	seen     int
	optional bool
}

func (t *jtype) field(k string) *jfield {
	for _, f := range t.fields {
		if f.key == k {
			return f
		}
	}
	return nil
}

func inferType(v any) *jtype {
	switch x := v.(type) {
	case nil:
		return &jtype{kind: "null"}
	case bool:
		return &jtype{kind: "bool"}
	case json.Number:
		if strings.ContainsAny(x.String(), ".eE") {
			return &jtype{kind: "float"}
		}
		return &jtype{kind: "int"}
	case string:
		return &jtype{kind: "string"}
	case []any:
		var elem *jtype
		for _, e := range x {
			elem = mergeTypes(elem, inferType(e))
		}
		if elem == nil {
			elem = &jtype{kind: "any"}
		}
		return &jtype{kind: "array", elem: elem}
	case *OMap:
		t := &jtype{kind: "object", samples: 1}
		for _, k := range x.Keys {
			t.fields = append(t.fields, &jfield{key: k, t: inferType(x.Vals[k]), seen: 1})
		}
		return t
	}
	return &jtype{kind: "any"}
}

func mergeTypes(a, b *jtype) *jtype {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.kind == "null" && b.kind == "null" {
		return a
	}
	if a.kind == "null" {
		c := *b
		c.nullable = true
		return &c
	}
	if b.kind == "null" {
		c := *a
		c.nullable = true
		return &c
	}
	nullable := a.nullable || b.nullable
	switch {
	case a.kind == b.kind && a.kind == "object":
		out := &jtype{kind: "object", nullable: nullable, samples: a.samples + b.samples}
		for _, f := range a.fields {
			out.fields = append(out.fields, &jfield{key: f.key, t: f.t, seen: f.seen})
		}
		for _, f := range b.fields {
			if ex := out.field(f.key); ex != nil {
				ex.t = mergeTypes(ex.t, f.t)
				ex.seen += f.seen
			} else {
				out.fields = append(out.fields, &jfield{key: f.key, t: f.t, seen: f.seen})
			}
		}
		for _, f := range out.fields {
			f.optional = f.seen < out.samples
		}
		return out
	case a.kind == b.kind && a.kind == "array":
		return &jtype{kind: "array", elem: mergeTypes(a.elem, b.elem), nullable: nullable}
	case a.kind == b.kind:
		return &jtype{kind: a.kind, nullable: nullable}
	case (a.kind == "int" && b.kind == "float") || (a.kind == "float" && b.kind == "int"):
		return &jtype{kind: "float", nullable: nullable}
	}
	return &jtype{kind: "any", nullable: nullable}
}

// ---------------------------------------------------------------------------
// naming

var goInitialisms = setOf("id", "url", "uri", "http", "https", "api", "json", "xml", "html", "sql", "ip", "uuid", "uid", "cpu", "css", "dns", "tcp", "udp", "tls", "ssl", "ttl", "utc", "ui", "os", "io", "acl", "ascii", "eof", "guid", "hmac", "jwt", "rpc", "sla", "smtp", "ssh", "vm", "xsrf", "xss", "csrf")

func identWords(key string) []string {
	ws := splitWords(key)
	var out []string
	for _, w := range ws {
		w = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, w)
		if w != "" {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		out = []string{"field"}
	}
	return out
}

func pascal(key string, initialisms bool) string {
	var b strings.Builder
	for _, w := range identWords(key) {
		if initialisms && goInitialisms[strings.ToLower(w)] {
			b.WriteString(strings.ToUpper(w))
		} else {
			b.WriteString(capWord(w))
		}
	}
	s := b.String()
	if s != "" && unicode.IsDigit(rune(s[0])) {
		s = "N" + s
	}
	return s
}

func camel(key string) string {
	p := pascal(key, false)
	rs := []rune(p)
	rs[0] = unicode.ToLower(rs[0])
	return string(rs)
}

func snake(key string) string {
	ws := identWords(key)
	for i := range ws {
		ws[i] = strings.ToLower(ws[i])
	}
	s := strings.Join(ws, "_")
	if s != "" && unicode.IsDigit(rune(s[0])) {
		s = "n" + s
	}
	return s
}

func singular(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.HasSuffix(l, "ies") && len(s) > 4:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(l, "sses"), strings.HasSuffix(l, "xes"), strings.HasSuffix(l, "ches"), strings.HasSuffix(l, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(l, "ss"), strings.HasSuffix(l, "us"), strings.HasSuffix(l, "is"):
		return s
	case strings.HasSuffix(l, "s") && len(s) > 3:
		return s[:len(s)-1]
	}
	return s
}

// assignNames gives every object type a unique PascalCase name and returns
// them in emission order (root first, then depth-first).
func assignNames(root *jtype, rootName string) []*jtype {
	var order []*jtype
	used := map[string]bool{}
	var walk func(t *jtype, name string)
	walk = func(t *jtype, name string) {
		switch t.kind {
		case "object":
			base := pascal(name, true)
			if base == "" {
				base = "Item"
			}
			n := base
			for i := 2; used[n]; i++ {
				n = fmt.Sprintf("%s%d", base, i)
			}
			used[n] = true
			t.name = n
			order = append(order, t)
			for _, f := range t.fields {
				walk(f.t, f.key)
			}
		case "array":
			walk(t.elem, singular(name))
		}
	}
	walk(root, rootName)
	return order
}

var reIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ---------------------------------------------------------------------------
// emitters

func goType(t *jtype) string {
	var s string
	switch t.kind {
	case "bool":
		s = "bool"
	case "int":
		s = "int64"
	case "float":
		s = "float64"
	case "string":
		s = "string"
	case "object":
		s = t.name
	case "array":
		return "[]" + goType(t.elem)
	default:
		return "any"
	}
	if t.nullable {
		return "*" + s
	}
	return s
}

func emitGo(types []*jtype) string {
	var b strings.Builder
	b.WriteString("package main\n\n")
	for _, t := range types {
		fmt.Fprintf(&b, "type %s struct {\n", t.name)
		for _, f := range t.fields {
			typ := goType(f.t)
			tag := f.key
			if f.optional {
				tag += ",omitempty"
				if !strings.HasPrefix(typ, "*") && !strings.HasPrefix(typ, "[]") && typ != "any" {
					typ = "*" + typ
				}
			}
			fmt.Fprintf(&b, "\t%s %s `json:%s`\n", pascal(f.key, true), typ, dq(tag))
		}
		b.WriteString("}\n\n")
	}
	src := b.String()
	if out, err := format.Source([]byte(src)); err == nil {
		return string(out)
	}
	return src
}

func tsType(t *jtype) string {
	var s string
	switch t.kind {
	case "bool":
		s = "boolean"
	case "int", "float":
		s = "number"
	case "string":
		s = "string"
	case "object":
		s = t.name
	case "array":
		inner := tsType(t.elem)
		if strings.Contains(inner, "|") {
			inner = "(" + inner + ")"
		}
		s = inner + "[]"
	case "null":
		return "null"
	default:
		return "unknown"
	}
	if t.nullable {
		return s + " | null"
	}
	return s
}

func emitTS(types []*jtype) string {
	var b strings.Builder
	for i, t := range types {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "export interface %s {\n", t.name)
		for _, f := range t.fields {
			k := f.key
			if !reIdent.MatchString(k) {
				k = sq(k)
			}
			opt := ""
			if f.optional {
				opt = "?"
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", k, opt, tsType(f.t))
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func rustType(t *jtype) string {
	var s string
	switch t.kind {
	case "bool":
		s = "bool"
	case "int":
		s = "i64"
	case "float":
		s = "f64"
	case "string":
		s = "String"
	case "object":
		s = t.name
	case "array":
		s = "Vec<" + rustType(t.elem) + ">"
	default:
		return "serde_json::Value"
	}
	if t.nullable {
		return "Option<" + s + ">"
	}
	return s
}

var rustKeywords = setOf("type", "match", "ref", "self", "struct", "enum", "fn", "impl", "trait", "mod", "use", "pub", "crate", "move", "loop", "where", "async", "await", "dyn", "box", "in", "for", "if", "else", "let", "mut", "const", "static", "return", "true", "false", "super", "as", "break", "continue", "extern", "unsafe", "while")

func emitRust(types []*jtype) string {
	var b strings.Builder
	b.WriteString("use serde::{Deserialize, Serialize};\n")
	for _, t := range types {
		fmt.Fprintf(&b, "\n#[derive(Debug, Clone, Serialize, Deserialize)]\npub struct %s {\n", t.name)
		for _, f := range t.fields {
			name := snake(f.key)
			if rustKeywords[name] {
				name = "r#" + name
			}
			typ := rustType(f.t)
			if f.optional && !strings.HasPrefix(typ, "Option<") {
				typ = "Option<" + typ + ">"
			}
			if strings.TrimPrefix(name, "r#") != f.key {
				fmt.Fprintf(&b, "    #[serde(rename = %s)]\n", dq(f.key))
			}
			if f.optional {
				b.WriteString("    #[serde(default, skip_serializing_if = \"Option::is_none\")]\n")
			}
			fmt.Fprintf(&b, "    pub %s: %s,\n", name, typ)
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func pyType(t *jtype) string {
	var s string
	switch t.kind {
	case "bool":
		s = "bool"
	case "int":
		s = "int"
	case "float":
		s = "float"
	case "string":
		s = "str"
	case "object":
		s = t.name
	case "array":
		s = "List[" + pyType(t.elem) + "]"
	default:
		return "Any"
	}
	if t.nullable {
		return "Optional[" + s + "]"
	}
	return s
}

var pyKeywords = setOf("class", "def", "from", "import", "global", "pass", "return", "lambda", "yield", "with", "while", "try", "except", "finally", "raise", "if", "elif", "else", "for", "in", "is", "not", "and", "or", "as", "assert", "break", "continue", "del", "nonlocal", "None", "True", "False", "async", "await", "type")

func emitPython(types []*jtype, pydantic bool) string {
	var b strings.Builder
	b.WriteString("from __future__ import annotations\n\n")
	if pydantic {
		b.WriteString("from typing import Any, List, Optional\n\nfrom pydantic import BaseModel, Field\n")
	} else {
		b.WriteString("from dataclasses import dataclass, field\nfrom typing import Any, List, Optional\n")
	}
	// Python needs dependencies defined first: emit in reverse discovery order
	for i := len(types) - 1; i >= 0; i-- {
		t := types[i]
		if pydantic {
			fmt.Fprintf(&b, "\n\nclass %s(BaseModel):\n", t.name)
		} else {
			fmt.Fprintf(&b, "\n\n@dataclass\nclass %s:\n", t.name)
		}
		if len(t.fields) == 0 {
			b.WriteString("    pass\n")
		}
		// dataclasses need fields with defaults last
		ordered := append([]*jfield{}, t.fields...)
		if !pydantic {
			var req, opt []*jfield
			for _, f := range ordered {
				if f.optional {
					opt = append(opt, f)
				} else {
					req = append(req, f)
				}
			}
			ordered = append(req, opt...)
		}
		for _, f := range ordered {
			name := snake(f.key)
			if pyKeywords[name] {
				name += "_"
			}
			typ := pyType(f.t)
			if f.optional && !strings.HasPrefix(typ, "Optional[") {
				typ = "Optional[" + typ + "]"
			}
			switch {
			case pydantic && name != f.key && f.optional:
				fmt.Fprintf(&b, "    %s: %s = Field(None, alias=%s)\n", name, typ, dq(f.key))
			case pydantic && name != f.key:
				fmt.Fprintf(&b, "    %s: %s = Field(alias=%s)\n", name, typ, dq(f.key))
			case f.optional:
				fmt.Fprintf(&b, "    %s: %s = None\n", name, typ)
			default:
				comment := ""
				if name != f.key {
					comment = "  # json: " + dq(f.key)
				}
				fmt.Fprintf(&b, "    %s: %s%s\n", name, typ, comment)
			}
		}
	}
	return b.String()
}

func swiftType(t *jtype) string {
	var s string
	switch t.kind {
	case "bool":
		s = "Bool"
	case "int":
		s = "Int"
	case "float":
		s = "Double"
	case "string":
		s = "String"
	case "object":
		s = t.name
	case "array":
		s = "[" + swiftType(t.elem) + "]"
	default:
		s = "JSONAny"
	}
	if t.nullable {
		return s + "?"
	}
	return s
}

func emitSwift(types []*jtype) string {
	var b strings.Builder
	b.WriteString("import Foundation\n")
	usesAny := false
	for _, t := range types {
		fmt.Fprintf(&b, "\n// MARK: - %s\nstruct %s: Codable {\n", t.name, t.name)
		needKeys := false
		for _, f := range t.fields {
			name := camel(f.key)
			typ := swiftType(f.t)
			if strings.Contains(typ, "JSONAny") {
				usesAny = true
			}
			if f.optional && !strings.HasSuffix(typ, "?") {
				typ += "?"
			}
			if name != f.key {
				needKeys = true
			}
			fmt.Fprintf(&b, "    let %s: %s\n", name, typ)
		}
		if needKeys {
			b.WriteString("\n    enum CodingKeys: String, CodingKey {\n")
			for _, f := range t.fields {
				name := camel(f.key)
				if name != f.key {
					fmt.Fprintf(&b, "        case %s = %s\n", name, dq(f.key))
				} else {
					fmt.Fprintf(&b, "        case %s\n", name)
				}
			}
			b.WriteString("    }\n")
		}
		b.WriteString("}\n")
	}
	if usesAny {
		b.WriteString("\n// JSONAny: values of mixed type — replace with a concrete type or an AnyCodable package.\ntypealias JSONAny = String\n")
	}
	return b.String()
}

func kotlinType(t *jtype) string {
	var s string
	switch t.kind {
	case "bool":
		s = "Boolean"
	case "int":
		s = "Long"
	case "float":
		s = "Double"
	case "string":
		s = "String"
	case "object":
		s = t.name
	case "array":
		s = "List<" + kotlinType(t.elem) + ">"
	default:
		s = "JsonElement"
	}
	if t.nullable {
		return s + "?"
	}
	return s
}

func emitKotlin(types []*jtype) string {
	var b strings.Builder
	b.WriteString("import kotlinx.serialization.SerialName\nimport kotlinx.serialization.Serializable\nimport kotlinx.serialization.json.JsonElement\n")
	for _, t := range types {
		fmt.Fprintf(&b, "\n@Serializable\ndata class %s(\n", t.name)
		for i, f := range t.fields {
			name := camel(f.key)
			typ := kotlinType(f.t)
			def := ""
			if f.optional {
				if !strings.HasSuffix(typ, "?") {
					typ += "?"
				}
				def = " = null"
			}
			ann := ""
			if name != f.key {
				ann = "@SerialName(" + dq(f.key) + ") "
			}
			sep := ","
			if i == len(t.fields)-1 {
				sep = ""
			}
			fmt.Fprintf(&b, "    %sval %s: %s%s%s\n", ann, name, typ, def, sep)
		}
		b.WriteString(")\n")
	}
	return b.String()
}

func javaType(t *jtype, boxed bool) string {
	switch t.kind {
	case "bool":
		if boxed || t.nullable {
			return "Boolean"
		}
		return "boolean"
	case "int":
		if boxed || t.nullable {
			return "Long"
		}
		return "long"
	case "float":
		if boxed || t.nullable {
			return "Double"
		}
		return "double"
	case "string":
		return "String"
	case "object":
		return t.name
	case "array":
		return "List<" + javaType(t.elem, true) + ">"
	}
	return "Object"
}

func emitJava(types []*jtype) string {
	var b strings.Builder
	b.WriteString("import com.fasterxml.jackson.annotation.JsonProperty;\nimport java.util.List;\n")
	for _, t := range types {
		var params []string
		for _, f := range t.fields {
			name := camel(f.key)
			p := javaType(f.t, f.optional) + " " + name
			if name != f.key {
				p = "@JsonProperty(" + dq(f.key) + ") " + p
			}
			params = append(params, p)
		}
		fmt.Fprintf(&b, "\npublic record %s(\n    %s\n) {}\n", t.name, strings.Join(params, ",\n    "))
	}
	return b.String()
}

func csType(t *jtype) string {
	var s string
	switch t.kind {
	case "bool":
		s = "bool"
	case "int":
		s = "long"
	case "float":
		s = "double"
	case "string":
		return "string" + map[bool]string{true: "?", false: ""}[t.nullable]
	case "object":
		s = t.name
	case "array":
		return "List<" + csType(t.elem) + ">"
	default:
		return "JsonElement"
	}
	if t.nullable {
		return s + "?"
	}
	return s
}

func emitCSharp(types []*jtype) string {
	var b strings.Builder
	b.WriteString("using System.Collections.Generic;\nusing System.Text.Json;\nusing System.Text.Json.Serialization;\n")
	for _, t := range types {
		fmt.Fprintf(&b, "\npublic class %s\n{\n", t.name)
		for _, f := range t.fields {
			typ := csType(f.t)
			if f.optional && !strings.HasSuffix(typ, "?") {
				typ += "?"
			}
			fmt.Fprintf(&b, "    [JsonPropertyName(%s)]\n    public %s %s { get; set; }\n\n", dq(f.key), typ, pascal(f.key, false))
		}
		out := strings.TrimSuffix(b.String(), "\n")
		b.Reset()
		b.WriteString(out)
		b.WriteString("}\n")
	}
	return b.String()
}

func schemaOf(t *jtype) *OMap {
	m := newOMap()
	typ := map[string]string{"bool": "boolean", "int": "integer", "float": "number", "string": "string", "object": "object", "array": "array", "null": "null"}[t.kind]
	if typ != "" {
		if t.nullable {
			m.Set("type", []any{typ, "null"})
		} else {
			m.Set("type", typ)
		}
	}
	switch t.kind {
	case "object":
		props := newOMap()
		var req []any
		for _, f := range t.fields {
			props.Set(f.key, schemaOf(f.t))
			if !f.optional {
				req = append(req, f.key)
			}
		}
		m.Set("properties", props)
		if len(req) > 0 {
			m.Set("required", req)
		}
	case "array":
		m.Set("items", schemaOf(t.elem))
	}
	return m
}

func emitSchema(root *jtype, name string) string {
	s := schemaOf(root)
	out := newOMap()
	out.Set("$schema", "https://json-schema.org/draft/2020-12/schema")
	out.Set("title", name)
	for _, k := range s.Keys {
		out.Set(k, s.Vals[k])
	}
	return encodeOrdered(out, "  ", false)
}

var codeLangs = []struct{ id, lang string }{
	{"go", "go"}, {"typescript", "ts"}, {"rust", "rust"}, {"python", "python"}, {"pydantic", "python"},
	{"swift", "swift"}, {"kotlin", "kotlin"}, {"java", "java"}, {"csharp", "csharp"}, {"json-schema", "json"},
}

func jsonToCodeTool() *core.Tool {
	var choices []string
	for _, l := range codeLangs {
		choices = append(choices, l.id)
	}
	return &core.Tool{
		ID:          "json2code",
		Name:        "JSON to Code",
		Category:    core.CatConvert,
		Description: "Generate typed models from JSON: Go structs, TypeScript interfaces, Rust serde, Python dataclasses/Pydantic, Swift Codable, Kotlin, Java records, C# classes or JSON Schema. Arrays are merged to detect optional and nullable fields.",
		Aliases:     []string{"json-to-code", "quicktype", "json2go", "json2ts"},
		Keywords:    []string{"struct", "interface", "types", "model", "schema", "codegen", "typescript", "golang"},
		Args:        []string{"?lang", "input"},
		Options: []core.Option{
			{Key: "lang", Label: "Language", Type: core.OptSelect, Default: "go", Choices: choices, Short: "l"},
			{Key: "name", Label: "Root type", Type: core.OptString, Default: "Root", Short: "n"},
		},
		Sample: `{"name":"How To Live Forever","artist":{"name":"Michael Forrest","founded":1982,"members":["Michael Forrest"]},"tracks":[{"name":"Get Connected","duration":208,"explicit":false},{"name":"Spirit","duration":185.5,"remix_of":null,"featured-artists":["Ada"]}],"release_date":"2024-03-01","id":42}`,
		Run: func(r core.Request) (*core.Result, error) {
			v, notes, err := parseJSONInput(r.Input, true)
			if err != nil {
				return nil, err
			}
			root := inferType(v)
			name := r.Str("name")
			if name == "" {
				name = "Root"
			}
			wrapped := root
			if root.kind != "object" {
				if root.kind == "array" && root.elem.kind == "object" {
					notes = append(notes, "Top-level array: generated the element type.")
					wrapped = root.elem
				} else {
					return nil, fmt.Errorf("top-level JSON must be an object or an array of objects")
				}
			}
			types := assignNames(wrapped, name)
			lang := r.Str("lang")
			var out string
			switch lang {
			case "typescript":
				out = emitTS(types)
			case "rust":
				out = emitRust(types)
			case "python":
				out = emitPython(types, false)
			case "pydantic":
				out = emitPython(types, true)
			case "swift":
				out = emitSwift(types)
			case "kotlin":
				out = emitKotlin(types)
			case "java":
				out = emitJava(types)
			case "csharp":
				out = emitCSharp(types)
			case "json-schema":
				out = emitSchema(root, name)
			default:
				out = emitGo(types)
			}
			hl := lang
			for _, l := range codeLangs {
				if l.id == lang {
					hl = l.lang
				}
			}
			return &core.Result{Output: out, Lang: hl, Warnings: notes, Fields: fields("Types", fmt.Sprint(len(types)))}, nil
		},
	}
}
