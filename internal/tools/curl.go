package tools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"devknife/internal/core"
)

type curlReq struct {
	Method   string
	URL      string
	Headers  [][2]string
	Body     string
	JSONBody any // parsed body when it is JSON
	Form     [][2]string
	User     string
	Pass     string
	HasAuth  bool
	Insecure bool
	Warnings []string
}

func (c *curlReq) header(name string) (string, bool) {
	for _, h := range c.Headers {
		if strings.EqualFold(h[0], name) {
			return h[1], true
		}
	}
	return "", false
}

func (c *curlReq) setHeader(name, value string) {
	for i, h := range c.Headers {
		if strings.EqualFold(h[0], name) {
			c.Headers[i][1] = value
			return
		}
	}
	c.Headers = append(c.Headers, [2]string{name, value})
}

// shellSplit tokenizes a shell command line (bash quoting rules, plus the
// cmd.exe ^ continuations Chrome emits for "Copy as cURL (cmd)").
func shellSplit(s string) ([]string, error) {
	s = strings.ReplaceAll(s, "\\\r\n", " ")
	s = strings.ReplaceAll(s, "\\\n", " ")
	if strings.Contains(s, "^\n") || strings.Contains(s, "^\r\n") || strings.Contains(s, `^"`) {
		s = strings.NewReplacer("^\r\n", " ", "^\n", " ", `^"`, `"`, "^^", "^", "^%", "%", "^&", "&", "^<", "<", "^>", ">", "^|", "|", "^\\", "\\").Replace(s)
	}
	var args []string
	var cur strings.Builder
	inArg := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		case c == '\'':
			inArg = true
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("unterminated single quote")
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case c == '$' && i+1 < len(s) && s[i+1] == '\'':
			inArg = true
			j := i + 2
			var raw strings.Builder
			for j < len(s) && s[j] != '\'' {
				if s[j] == '\\' && j+1 < len(s) {
					raw.WriteByte(s[j])
					j++
				}
				raw.WriteByte(s[j])
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated $'...' string")
			}
			un, err := unescapeBackslash(raw.String())
			if err != nil {
				un = raw.String()
			}
			cur.WriteString(un)
			i = j
		case c == '"':
			inArg = true
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) && strings.IndexByte("\"\\$`\n", s[j+1]) >= 0 {
					j++
				}
				cur.WriteByte(s[j])
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated double quote")
			}
			i = j
		case c == '\\' && i+1 < len(s):
			inArg = true
			i++
			cur.WriteByte(s[i])
		default:
			inArg = true
			cur.WriteByte(c)
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

func parseCurl(cmd string) (*curlReq, error) {
	args, err := shellSplit(strings.TrimSpace(cmd))
	if err != nil {
		return nil, err
	}
	if len(args) == 0 || !strings.HasSuffix(strings.ToLower(args[0]), "curl") && !strings.HasSuffix(strings.ToLower(args[0]), "curl.exe") {
		return nil, fmt.Errorf("input must start with curl")
	}
	c := &curlReq{}
	var data []string
	getMode := false
	explicitMethod := false
	needsVal := map[string]bool{"-X": true, "--request": true, "-H": true, "--header": true, "-d": true, "--data": true, "--data-raw": true, "--data-ascii": true,
		"--data-binary": true, "--data-urlencode": true, "--json": true, "-F": true, "--form": true, "--form-string": true, "-u": true, "--user": true, "-b": true, "--cookie": true,
		"-A": true, "--user-agent": true, "-e": true, "--referer": true, "--url": true, "-o": true, "--output": true, "-m": true, "--max-time": true,
		"--connect-timeout": true, "-x": true, "--proxy": true, "--retry": true, "-w": true, "--write-out": true, "--cacert": true, "--cert": true, "--key": true, "-c": true, "--cookie-jar": true, "-T": true, "--upload-file": true, "-r": true, "--range": true, "--oauth2-bearer": true}
	for i := 1; i < len(args); i++ {
		a := args[i]
		flag, val := a, ""
		hasInline := false
		if strings.HasPrefix(a, "--") && strings.Contains(a, "=") {
			flag, val, _ = strings.Cut(a, "=")
			hasInline = needsVal[flag]
			if !hasInline {
				flag = a
			}
		} else if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			// -XPOST, -H'..', or combined boolean flags like -sSL
			short := a[:2]
			if needsVal[short] {
				flag, val, hasInline = short, a[2:], true
			} else {
				for _, ch := range a[1:] {
					switch ch {
					case 'k':
						c.Insecure = true
					case 'I':
						c.Method = "HEAD"
						explicitMethod = true
					case 'G':
						getMode = true
					}
				}
				continue
			}
		}
		if needsVal[flag] && !hasInline {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a value", flag)
			}
			i++
			val = args[i]
		}
		switch flag {
		case "-X", "--request":
			c.Method = strings.ToUpper(val)
			explicitMethod = true
		case "-H", "--header":
			k, v, ok := strings.Cut(val, ":")
			if !ok {
				if strings.HasSuffix(val, ";") {
					c.Headers = append(c.Headers, [2]string{strings.TrimSuffix(val, ";"), ""})
				}
				continue
			}
			c.Headers = append(c.Headers, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
		case "-d", "--data", "--data-ascii", "--data-raw", "--data-binary":
			if strings.HasPrefix(val, "@") && flag != "--data-raw" {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s %s reads a file; replace the placeholder body with its contents", flag, val))
			}
			data = append(data, val)
		case "--data-urlencode":
			if k, v, ok := strings.Cut(val, "="); ok {
				data = append(data, k+"="+url.QueryEscape(v))
			} else {
				data = append(data, url.QueryEscape(val))
			}
		case "--json":
			data = append(data, val)
			if _, ok := c.header("Content-Type"); !ok {
				c.setHeader("Content-Type", "application/json")
			}
			if _, ok := c.header("Accept"); !ok {
				c.setHeader("Accept", "application/json")
			}
		case "-F", "--form", "--form-string":
			k, v, _ := strings.Cut(val, "=")
			c.Form = append(c.Form, [2]string{k, v})
		case "-u", "--user":
			c.User, c.Pass, _ = strings.Cut(val, ":")
			c.HasAuth = true
		case "--oauth2-bearer":
			c.setHeader("Authorization", "Bearer "+val)
		case "-b", "--cookie":
			c.setHeader("Cookie", val)
		case "-A", "--user-agent":
			c.setHeader("User-Agent", val)
		case "-e", "--referer":
			c.setHeader("Referer", val)
		case "--url":
			c.URL = val
		case "-k", "--insecure":
			c.Insecure = true
		case "-I", "--head":
			c.Method, explicitMethod = "HEAD", true
		case "-G", "--get":
			getMode = true
		case "--compressed":
			c.Warnings = append(c.Warnings, "--compressed: most HTTP clients negotiate compression automatically")
		case "-L", "--location", "-s", "--silent", "-S", "--show-error", "-v", "--verbose", "-i", "--include", "-f", "--fail", "--http1.1", "--http2", "-#", "--progress-bar", "--no-buffer", "-N":
			// output/transport flags with no code equivalent
		case "-o", "--output", "-m", "--max-time", "--connect-timeout", "-x", "--proxy", "--retry", "-w", "--write-out", "--cacert", "--cert", "--key", "-c", "--cookie-jar", "-T", "--upload-file", "-r", "--range":
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s is not translated", flag))
		default:
			if strings.HasPrefix(a, "-") {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s is not a supported option", a))
				continue
			}
			if c.URL == "" {
				c.URL = a
			}
		}
	}
	if c.URL == "" {
		return nil, fmt.Errorf("no URL found in the curl command")
	}
	if !strings.Contains(c.URL, "://") {
		c.URL = "http://" + c.URL
	}
	body := strings.Join(data, "&")
	if getMode && body != "" {
		sep := "?"
		if strings.Contains(c.URL, "?") {
			sep = "&"
		}
		c.URL += sep + body
		body = ""
	}
	c.Body = body
	if c.Method == "" {
		switch {
		case body != "" || len(c.Form) > 0:
			c.Method = "POST"
		default:
			c.Method = "GET"
		}
	}
	_ = explicitMethod
	if body != "" {
		if _, ok := c.header("Content-Type"); !ok && len(c.Form) == 0 {
			c.setHeader("Content-Type", "application/x-www-form-urlencoded")
		}
		if json.Valid([]byte(body)) {
			if v, err := decodeOrdered([]byte(body)); err == nil {
				if _, isObj := v.(*OMap); isObj {
					c.JSONBody = v
				} else if _, isArr := v.([]any); isArr {
					c.JSONBody = v
				}
			}
		}
	}
	return c, nil
}

// ---------------------------------------------------------------------------
// string literal helpers

func dq(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func sq(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", `\r`, "\t", `\t`)
	return `'` + r.Replace(s) + `'`
}

func rubyDQ(s string) string { return strings.ReplaceAll(dq(s), "#{", `\#{`) }

func isJSONContent(c *curlReq) bool {
	ct, _ := c.header("Content-Type")
	return c.JSONBody != nil && (ct == "" || strings.Contains(ct, "json"))
}

// pyLiteral renders ordered JSON as a Python literal.
func pyLiteral(v any, indent string) string {
	switch t := v.(type) {
	case *OMap:
		if len(t.Keys) == 0 {
			return "{}"
		}
		var b strings.Builder
		b.WriteString("{\n")
		for i, k := range t.Keys {
			b.WriteString(indent + "    " + sq(k) + ": " + pyLiteral(t.Vals[k], indent+"    "))
			if i < len(t.Keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		return b.String() + indent + "}"
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = indent + "    " + pyLiteral(e, indent+"    ")
		}
		return "[\n" + strings.Join(parts, ",\n") + "\n" + indent + "]"
	case string:
		return sq(t)
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	}
	return "None"
}

func indentLines(s, pad string) string {
	return strings.ReplaceAll(s, "\n", "\n"+pad)
}

// ---------------------------------------------------------------------------
// generators

func genGo(c *curlReq) string {
	var b strings.Builder
	imports := []string{`"fmt"`, `"io"`, `"log"`, `"net/http"`}
	bodyExpr := "nil"
	if len(c.Form) > 0 {
		imports = append(imports, `"bytes"`, `"mime/multipart"`)
	} else if c.Body != "" {
		imports = append(imports, `"strings"`)
		bodyExpr = "strings.NewReader(" + goRawOrQuoted(c.Body) + ")"
	}
	if c.Insecure {
		imports = append(imports, `"crypto/tls"`)
	}
	sort.Strings(imports)
	b.WriteString("package main\n\nimport (\n")
	for _, im := range imports {
		b.WriteString("\t" + im + "\n")
	}
	b.WriteString(")\n\nfunc main() {\n")
	if c.Insecure {
		b.WriteString("\tclient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}\n")
	} else {
		b.WriteString("\tclient := &http.Client{}\n")
	}
	if len(c.Form) > 0 {
		b.WriteString("\tvar body bytes.Buffer\n\tw := multipart.NewWriter(&body)\n")
		for _, f := range c.Form {
			if strings.HasPrefix(f[1], "@") {
				fmt.Fprintf(&b, "\t// TODO: attach file %s\n\tfw, _ := w.CreateFormFile(%s, %s)\n\tfw.Write([]byte(\"file contents\"))\n", f[1], dq(f[0]), dq(strings.TrimPrefix(f[1], "@")))
			} else {
				fmt.Fprintf(&b, "\tw.WriteField(%s, %s)\n", dq(f[0]), dq(f[1]))
			}
		}
		b.WriteString("\tw.Close()\n")
		bodyExpr = "&body"
	}
	fmt.Fprintf(&b, "\treq, err := http.NewRequest(%s, %s, %s)\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n", dq(c.Method), dq(c.URL), bodyExpr)
	for _, h := range c.Headers {
		fmt.Fprintf(&b, "\treq.Header.Set(%s, %s)\n", dq(h[0]), dq(h[1]))
	}
	if len(c.Form) > 0 {
		b.WriteString("\treq.Header.Set(\"Content-Type\", w.FormDataContentType())\n")
	}
	if c.HasAuth {
		fmt.Fprintf(&b, "\treq.SetBasicAuth(%s, %s)\n", dq(c.User), dq(c.Pass))
	}
	b.WriteString("\tresp, err := client.Do(req)\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tdefer resp.Body.Close()\n\tbodyText, err := io.ReadAll(resp.Body)\n\tif err != nil {\n\t\tlog.Fatal(err)\n\t}\n\tfmt.Printf(\"%s\\n\", bodyText)\n}\n")
	return b.String()
}

func goRawOrQuoted(s string) string {
	if !strings.Contains(s, "`") && (strings.Contains(s, `"`) || strings.Contains(s, "\n")) {
		return "`" + s + "`"
	}
	return dq(s)
}

func genPython(c *curlReq) string {
	var b strings.Builder
	b.WriteString("import requests\n\n")
	var hdrs [][2]string
	for _, h := range c.Headers {
		if isJSONContent(c) && strings.EqualFold(h[0], "Content-Type") {
			continue // requests sets it for json=
		}
		hdrs = append(hdrs, h)
	}
	if len(hdrs) > 0 {
		b.WriteString("headers = {\n")
		for _, h := range hdrs {
			fmt.Fprintf(&b, "    %s: %s,\n", sq(h[0]), sq(h[1]))
		}
		b.WriteString("}\n\n")
	}
	args := []string{sq(c.URL)}
	if len(hdrs) > 0 {
		args = append(args, "headers=headers")
	}
	switch {
	case len(c.Form) > 0:
		var data, files []string
		for _, f := range c.Form {
			if strings.HasPrefix(f[1], "@") {
				files = append(files, fmt.Sprintf("    %s: open(%s, 'rb'),", sq(f[0]), sq(strings.TrimPrefix(f[1], "@"))))
			} else {
				data = append(data, fmt.Sprintf("    %s: %s,", sq(f[0]), sq(f[1])))
			}
		}
		if len(data) > 0 {
			b.WriteString("data = {\n" + strings.Join(data, "\n") + "\n}\n\n")
			args = append(args, "data=data")
		}
		if len(files) > 0 {
			b.WriteString("files = {\n" + strings.Join(files, "\n") + "\n}\n\n")
			args = append(args, "files=files")
		}
	case isJSONContent(c):
		b.WriteString("json_data = " + pyLiteral(c.JSONBody, "") + "\n\n")
		args = append(args, "json=json_data")
	case c.Body != "":
		b.WriteString("data = " + sq(c.Body) + "\n\n")
		args = append(args, "data=data")
	}
	if c.HasAuth {
		args = append(args, fmt.Sprintf("auth=(%s, %s)", sq(c.User), sq(c.Pass)))
	}
	if c.Insecure {
		args = append(args, "verify=False")
	}
	method := strings.ToLower(c.Method)
	switch method {
	case "get", "post", "put", "patch", "delete", "head", "options":
		fmt.Fprintf(&b, "response = requests.%s(\n    %s,\n)\n", method, strings.Join(args, ",\n    "))
	default:
		fmt.Fprintf(&b, "response = requests.request(\n    %s,\n    %s,\n)\n", sq(c.Method), strings.Join(args, ",\n    "))
	}
	b.WriteString("print(response.status_code)\nprint(response.text)\n")
	return b.String()
}

func jsHeaders(c *curlReq, extra map[string]string) []string {
	var hs []string
	for _, h := range c.Headers {
		hs = append(hs, fmt.Sprintf("%s: %s", sq(h[0]), sq(h[1])))
	}
	for k, v := range extra {
		hs = append(hs, fmt.Sprintf("%s: %s", sq(k), v))
	}
	return hs
}

func genFetch(c *curlReq) string {
	var b strings.Builder
	var pre strings.Builder
	extra := map[string]string{}
	if c.HasAuth {
		extra["Authorization"] = "'Basic ' + btoa(" + sq(c.User+":"+c.Pass) + ")"
	}
	opts := []string{"method: " + sq(c.Method)}
	if hs := jsHeaders(c, extra); len(hs) > 0 {
		opts = append(opts, "headers: {\n    "+strings.Join(hs, ",\n    ")+"\n  }")
	}
	switch {
	case len(c.Form) > 0:
		pre.WriteString("const form = new FormData();\n")
		for _, f := range c.Form {
			if strings.HasPrefix(f[1], "@") {
				fmt.Fprintf(&pre, "form.append(%s, fileInput.files[0]); // %s\n", sq(f[0]), f[1])
			} else {
				fmt.Fprintf(&pre, "form.append(%s, %s);\n", sq(f[0]), sq(f[1]))
			}
		}
		pre.WriteString("\n")
		opts = append(opts, "body: form")
	case isJSONContent(c):
		opts = append(opts, "body: JSON.stringify("+indentLines(encodeOrdered(c.JSONBody, "  ", false), "  ")+")")
	case c.Body != "":
		opts = append(opts, "body: "+sq(c.Body))
	}
	b.WriteString(pre.String())
	fmt.Fprintf(&b, "const response = await fetch(%s, {\n  %s\n});\n\nconst data = await response.text();\nconsole.log(data);\n", sq(c.URL), strings.Join(opts, ",\n  "))
	return b.String()
}

func genAxios(c *curlReq) string {
	var b strings.Builder
	b.WriteString("import axios from 'axios';\n\n")
	opts := []string{"method: " + sq(strings.ToLower(c.Method)), "url: " + sq(c.URL)}
	if hs := jsHeaders(c, nil); len(hs) > 0 {
		opts = append(opts, "headers: {\n    "+strings.Join(hs, ",\n    ")+"\n  }")
	}
	if c.HasAuth {
		opts = append(opts, fmt.Sprintf("auth: {\n    username: %s,\n    password: %s\n  }", sq(c.User), sq(c.Pass)))
	}
	switch {
	case len(c.Form) > 0:
		b.WriteString("const form = new FormData();\n")
		for _, f := range c.Form {
			fmt.Fprintf(&b, "form.append(%s, %s);\n", sq(f[0]), sq(f[1]))
		}
		b.WriteString("\n")
		opts = append(opts, "data: form")
	case isJSONContent(c):
		opts = append(opts, "data: "+indentLines(encodeOrdered(c.JSONBody, "  ", false), "  "))
	case c.Body != "":
		opts = append(opts, "data: "+sq(c.Body))
	}
	if c.Insecure {
		b.WriteString("import https from 'https';\n\n")
		opts = append(opts, "httpsAgent: new https.Agent({ rejectUnauthorized: false })")
	}
	fmt.Fprintf(&b, "const response = await axios({\n  %s\n});\n\nconsole.log(response.data);\n", strings.Join(opts, ",\n  "))
	return b.String()
}

func genRust(c *curlReq) string {
	var b strings.Builder
	b.WriteString("// Cargo.toml: reqwest = { version = \"0.12\", features = [\"blocking\", \"multipart\"] }\n")
	b.WriteString("use reqwest::header;\n\nfn main() -> Result<(), Box<dyn std::error::Error>> {\n")
	b.WriteString("    let mut headers = header::HeaderMap::new();\n")
	for _, h := range c.Headers {
		fmt.Fprintf(&b, "    headers.insert(%s, %s.parse().unwrap());\n", dq(strings.ToLower(h[0])), dq(h[1]))
	}
	b.WriteString("\n    let client = reqwest::blocking::Client::builder()\n")
	if c.Insecure {
		b.WriteString("        .danger_accept_invalid_certs(true)\n")
	}
	b.WriteString("        .build()?;\n")
	if len(c.Form) > 0 {
		b.WriteString("    let form = reqwest::blocking::multipart::Form::new()")
		for _, f := range c.Form {
			if strings.HasPrefix(f[1], "@") {
				fmt.Fprintf(&b, "\n        .file(%s, %s)?", dq(f[0]), dq(strings.TrimPrefix(f[1], "@")))
			} else {
				fmt.Fprintf(&b, "\n        .text(%s, %s)", dq(f[0]), dq(f[1]))
			}
		}
		b.WriteString(";\n")
	}
	fmt.Fprintf(&b, "    let res = client\n        .request(reqwest::Method::from_bytes(b%s)?, %s)\n        .headers(headers)\n", dq(c.Method), dq(c.URL))
	if c.HasAuth {
		fmt.Fprintf(&b, "        .basic_auth(%s, Some(%s))\n", dq(c.User), dq(c.Pass))
	}
	switch {
	case len(c.Form) > 0:
		b.WriteString("        .multipart(form)\n")
	case c.Body != "":
		fmt.Fprintf(&b, "        .body(%s)\n", rustRawString(c.Body))
	}
	b.WriteString("        .send()?\n        .text()?;\n    println!(\"{}\", res);\n\n    Ok(())\n}\n")
	return b.String()
}

// rustRawString quotes s as a Rust raw string r#"…"#, with one more '#'
// than the longest run of '#' after a '"' in s, so the body can't end it early.
func rustRawString(s string) string {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '"' {
			continue
		}
		run := 0
		for j := i + 1; j < len(s) && s[j] == '#'; j++ {
			run++
		}
		n = max(n, run)
	}
	h := strings.Repeat("#", n+1)
	return "r" + h + `"` + s + `"` + h
}

func genJava(c *curlReq) string {
	var b strings.Builder
	b.WriteString("import java.net.URI;\nimport java.net.http.HttpClient;\nimport java.net.http.HttpRequest;\nimport java.net.http.HttpResponse;\n")
	if c.HasAuth {
		b.WriteString("import java.util.Base64;\n")
	}
	b.WriteString("\npublic class Main {\n    public static void main(String[] args) throws Exception {\n")
	b.WriteString("        HttpClient client = HttpClient.newHttpClient();\n\n")
	b.WriteString("        HttpRequest request = HttpRequest.newBuilder()\n")
	fmt.Fprintf(&b, "            .uri(URI.create(%s))\n", dq(c.URL))
	body := "HttpRequest.BodyPublishers.noBody()"
	if c.Body != "" {
		body = "HttpRequest.BodyPublishers.ofString(" + dq(c.Body) + ")"
	}
	fmt.Fprintf(&b, "            .method(%s, %s)\n", dq(c.Method), body)
	for _, h := range c.Headers {
		fmt.Fprintf(&b, "            .header(%s, %s)\n", dq(h[0]), dq(h[1]))
	}
	if c.HasAuth {
		fmt.Fprintf(&b, "            .header(\"Authorization\", \"Basic \" + Base64.getEncoder().encodeToString(%s.getBytes()))\n", dq(c.User+":"+c.Pass))
	}
	b.WriteString("            .build();\n\n")
	b.WriteString("        HttpResponse<String> response = client.send(request, HttpResponse.BodyHandlers.ofString());\n")
	b.WriteString("        System.out.println(response.statusCode());\n        System.out.println(response.body());\n    }\n}\n")
	if len(c.Form) > 0 {
		b.WriteString("// note: multipart form fields are not translated for java.net.http\n")
	}
	return b.String()
}

func genCSharp(c *curlReq) string {
	var b strings.Builder
	b.WriteString("using System.Net.Http;\nusing System.Net.Http.Headers;\nusing System.Text;\n\n")
	if c.Insecure {
		b.WriteString("var handler = new HttpClientHandler { ServerCertificateCustomValidationCallback = HttpClientHandler.DangerousAcceptAnyServerCertificateValidator };\nusing var client = new HttpClient(handler);\n\n")
	} else {
		b.WriteString("using var client = new HttpClient();\n\n")
	}
	fmt.Fprintf(&b, "var request = new HttpRequestMessage(new HttpMethod(%s), %s);\n", dq(c.Method), dq(c.URL))
	ct := ""
	for _, h := range c.Headers {
		if strings.EqualFold(h[0], "Content-Type") {
			ct = h[1]
			continue
		}
		fmt.Fprintf(&b, "request.Headers.TryAddWithoutValidation(%s, %s);\n", dq(h[0]), dq(h[1]))
	}
	if c.HasAuth {
		fmt.Fprintf(&b, "request.Headers.Authorization = new AuthenticationHeaderValue(\"Basic\", Convert.ToBase64String(Encoding.UTF8.GetBytes(%s)));\n", dq(c.User+":"+c.Pass))
	}
	switch {
	case len(c.Form) > 0:
		b.WriteString("var form = new MultipartFormDataContent();\n")
		for _, f := range c.Form {
			fmt.Fprintf(&b, "form.Add(new StringContent(%s), %s);\n", dq(f[1]), dq(f[0]))
		}
		b.WriteString("request.Content = form;\n")
	case c.Body != "":
		if ct == "" {
			ct = "text/plain"
		}
		mt, _, _ := strings.Cut(ct, ";")
		fmt.Fprintf(&b, "request.Content = new StringContent(%s, Encoding.UTF8, %s);\n", dq(c.Body), dq(strings.TrimSpace(mt)))
	}
	b.WriteString("\nvar response = await client.SendAsync(request);\nresponse.EnsureSuccessStatusCode();\nConsole.WriteLine(await response.Content.ReadAsStringAsync());\n")
	return b.String()
}

func genRuby(c *curlReq) string {
	var b strings.Builder
	b.WriteString("require 'net/http'\nrequire 'uri'\n\n")
	fmt.Fprintf(&b, "uri = URI(%s)\n", rubyDQ(c.URL))
	cls := map[string]string{"GET": "Get", "POST": "Post", "PUT": "Put", "PATCH": "Patch", "DELETE": "Delete", "HEAD": "Head", "OPTIONS": "Options"}[c.Method]
	if cls == "" {
		fmt.Fprintf(&b, "req = Net::HTTPGenericRequest.new(%s, true, true, uri)\n", rubyDQ(c.Method))
	} else {
		fmt.Fprintf(&b, "req = Net::HTTP::%s.new(uri)\n", cls)
	}
	for _, h := range c.Headers {
		fmt.Fprintf(&b, "req[%s] = %s\n", rubyDQ(h[0]), rubyDQ(h[1]))
	}
	if c.HasAuth {
		fmt.Fprintf(&b, "req.basic_auth(%s, %s)\n", rubyDQ(c.User), rubyDQ(c.Pass))
	}
	if len(c.Form) > 0 {
		var parts []string
		for _, f := range c.Form {
			parts = append(parts, fmt.Sprintf("[%s, %s]", rubyDQ(f[0]), rubyDQ(f[1])))
		}
		fmt.Fprintf(&b, "req.set_form([%s], 'multipart/form-data')\n", strings.Join(parts, ", "))
	} else if c.Body != "" {
		fmt.Fprintf(&b, "req.body = %s\n", rubyDQ(c.Body))
	}
	opts := "use_ssl: uri.scheme == 'https'"
	if c.Insecure {
		opts += ", verify_mode: OpenSSL::SSL::VERIFY_NONE"
	}
	fmt.Fprintf(&b, "\nres = Net::HTTP.start(uri.hostname, uri.port, %s) do |http|\n  http.request(req)\nend\n\nputs res.code\nputs res.body\n", opts)
	return b.String()
}

func genSwift(c *curlReq) string {
	var b strings.Builder
	b.WriteString("import Foundation\n\n")
	fmt.Fprintf(&b, "let url = URL(string: %s)!\nvar request = URLRequest(url: url)\nrequest.httpMethod = %s\n", dq(c.URL), dq(c.Method))
	for _, h := range c.Headers {
		fmt.Fprintf(&b, "request.setValue(%s, forHTTPHeaderField: %s)\n", dq(h[1]), dq(h[0]))
	}
	if c.HasAuth {
		fmt.Fprintf(&b, "request.setValue(\"Basic \" + Data(%s.utf8).base64EncodedString(), forHTTPHeaderField: \"Authorization\")\n", dq(c.User+":"+c.Pass))
	}
	if c.Body != "" {
		fmt.Fprintf(&b, "request.httpBody = %s.data(using: .utf8)\n", dq(c.Body))
	}
	b.WriteString("\nlet (data, response) = try await URLSession.shared.data(for: request)\nprint((response as? HTTPURLResponse)?.statusCode ?? 0)\nprint(String(decoding: data, as: UTF8.self))\n")
	return b.String()
}

func genHTTPie(c *curlReq) string {
	parts := []string{"http"}
	if c.Insecure {
		parts = append(parts, "--verify=no")
	}
	if len(c.Form) > 0 {
		parts = append(parts, "--multipart")
	} else if c.Body != "" && !isJSONContent(c) {
		parts = append(parts, "--raw="+shellQuote(c.Body))
	}
	if c.HasAuth {
		parts = append(parts, "-a", shellQuote(c.User+":"+c.Pass))
	}
	parts = append(parts, c.Method, shellQuote(c.URL))
	for _, h := range c.Headers {
		if isJSONContent(c) && strings.EqualFold(h[0], "Content-Type") {
			continue
		}
		parts = append(parts, shellQuote(h[0]+":"+h[1]))
	}
	for _, f := range c.Form {
		parts = append(parts, shellQuote(f[0]+"="+f[1]))
	}
	if isJSONContent(c) {
		if m, ok := c.JSONBody.(*OMap); ok {
			for _, k := range m.Keys {
				v := m.Vals[k]
				if s, isStr := v.(string); isStr {
					parts = append(parts, shellQuote(k+"="+s))
				} else {
					parts = append(parts, shellQuote(k+":="+encodeOrdered(v, "", false)))
				}
			}
		} else {
			parts = append(parts, "--raw="+shellQuote(c.Body))
		}
	}
	return strings.Join(parts, " \\\n  ") + "\n"
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func genRequestJSON(c *curlReq) string {
	m := newOMap()
	m.Set("method", c.Method)
	m.Set("url", c.URL)
	if u, err := url.Parse(c.URL); err == nil && u.RawQuery != "" {
		q := newOMap()
		for k, vs := range u.Query() {
			if len(vs) == 1 {
				q.Set(k, vs[0])
			} else {
				arr := make([]any, len(vs))
				for i, v := range vs {
					arr[i] = v
				}
				q.Set(k, arr)
			}
		}
		sort.Strings(q.Keys)
		m.Set("query", q)
	}
	h := newOMap()
	for _, x := range c.Headers {
		h.Set(x[0], x[1])
	}
	if c.HasAuth {
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.User+":"+c.Pass)))
	}
	m.Set("headers", h)
	if c.JSONBody != nil {
		m.Set("body", c.JSONBody)
	} else if c.Body != "" {
		m.Set("body", c.Body)
	}
	if len(c.Form) > 0 {
		f := newOMap()
		for _, x := range c.Form {
			f.Set(x[0], x[1])
		}
		m.Set("form", f)
	}
	return encodeOrdered(m, "  ", false)
}

var curlTargets = []struct {
	id, lang string
	gen      func(*curlReq) string
}{
	{"go", "go", genGo},
	{"python", "python", genPython},
	{"javascript", "js", genFetch},
	{"node-axios", "js", genAxios},
	{"rust", "rust", genRust},
	{"java", "java", genJava},
	{"csharp", "csharp", genCSharp},
	{"ruby", "ruby", genRuby},
	{"swift", "swift", genSwift},
	{"httpie", "shell", genHTTPie},
	{"json", "json", genRequestJSON},
}

func curlTool() *core.Tool {
	var choices []string
	for _, t := range curlTargets {
		choices = append(choices, t.id)
	}
	return &core.Tool{
		ID:          "curl",
		Name:        "cURL to Code",
		Category:    core.CatConvert,
		Description: "Turn a curl command (e.g. Chrome “Copy as cURL”) into Go, Python, JavaScript fetch, axios, Rust, Java, C#, Ruby, Swift or HTTPie code.",
		Aliases:     []string{"curl2code", "curl-to-code"},
		Keywords:    []string{"http", "request", "fetch", "requests", "axios", "convert"},
		Args:        []string{"?lang", "input"},
		Options: []core.Option{
			{Key: "lang", Label: "Language", Type: core.OptSelect, Default: "go", Choices: choices, Short: "l"},
		},
		Sample: `curl 'https://api.example.com/v1/users?limit=10' \
  -X POST \
  -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.e30.abc' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json' \
  --data-raw '{"name":"Ada Lovelace","email":"ada@example.com","roles":["admin"],"active":true}' \
  --compressed`,
		Detect: func(in string) *core.Detection {
			t := strings.TrimSpace(in)
			if strings.HasPrefix(strings.ToLower(t), "curl ") || strings.HasPrefix(strings.ToLower(t), "curl.exe ") {
				return det(98, "curl command")
			}
			return nil
		},
		Run: func(r core.Request) (*core.Result, error) {
			c, err := parseCurl(r.Input)
			if err != nil {
				return nil, err
			}
			for _, t := range curlTargets {
				if t.id == r.Str("lang") {
					return &core.Result{Output: t.gen(c), Lang: t.lang, Warnings: c.Warnings, Fields: fields("Method", c.Method, "URL", c.URL, "Headers", fmt.Sprint(len(c.Headers)))}, nil
				}
			}
			return nil, fmt.Errorf("unknown language")
		},
	}
}
