package tools

import (
	"strings"
	"testing"

	"devknife/internal/core"
)

type tc struct {
	tool     string
	input    string
	opts     map[string]string
	want     string   // exact output (if set)
	contains []string // substrings of output or any field value
	field    [2]string
	wantErr  string
}

func runTC(t *testing.T, reg *core.Registry, c tc) {
	t.Helper()
	tool, ok := reg.Get(c.tool)
	if !ok {
		t.Fatalf("no tool %q", c.tool)
	}
	opts := map[string]string{}
	for k, v := range c.opts {
		opts[k] = v
	}
	in := c.input
	if in == "SAMPLE" {
		in = tool.Sample
	}
	res, err := tool.Execute(core.Request{Input: in, Opts: opts})
	if c.wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Fatalf("want error containing %q, got %v", c.wantErr, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.want != "" && res.Output != c.want {
		t.Fatalf("output mismatch\n got: %q\nwant: %q", res.Output, c.want)
	}
	all := res.Output
	for _, f := range res.Fields {
		all += "\n" + f.Label + ": " + f.Value
	}
	for _, s := range c.contains {
		if !strings.Contains(all, s) {
			t.Fatalf("missing %q in:\n%s", s, all)
		}
	}
	if c.field[0] != "" {
		found := false
		for _, f := range res.Fields {
			if f.Label == c.field[0] {
				found = true
				if f.Value != c.field[1] {
					t.Fatalf("field %s = %q, want %q", f.Label, f.Value, c.field[1])
				}
			}
		}
		if !found {
			t.Fatalf("field %q not found", c.field[0])
		}
	}
}

func TestTools(t *testing.T) {
	reg := Registry()
	cases := map[string]tc{
		// format
		"json format":       {tool: "json", input: `{"b":1,"a":[true,null]}`, want: "{\n  \"b\": 1,\n  \"a\": [\n    true,\n    null\n  ]\n}"},
		"json sort tab":     {tool: "json", input: `{"b":1,"a":2}`, opts: map[string]string{"sort": "true", "indent": "tab"}, want: "{\n\t\"a\": 2,\n\t\"b\": 1\n}"},
		"json minify":       {tool: "json", input: "{\n \"a\" : [1, 2] }", opts: map[string]string{"mode": "minify"}, want: `{"a":[1,2]}`},
		"json lenient":      {tool: "json", input: "{a: 'x', // c\n b: [1,2,],}", opts: map[string]string{"mode": "minify"}, want: `{"a":"x","b":[1,2]}`},
		"json big numbers":  {tool: "json", input: `{"id": 12345678901234567890, "f": 1.50}`, opts: map[string]string{"mode": "minify"}, want: `{"id":12345678901234567890,"f":1.50}`},
		"json strict error": {tool: "json", input: "{\n  \"a\": 1,\n}", opts: map[string]string{"lenient": "false"}, wantErr: "line 3"},
		"json path filter":      {tool: "json", input: `{"users":[{"name":"ada"},{"name":"bob"}]}`, opts: map[string]string{"path": ".users[*].name", "mode": "minify"}, want: `["ada","bob"]`},
		"json query":        {tool: "jq", input: `{"users":[{"name":"ada","age":36},{"name":"bob","age":20}]}`, opts: map[string]string{"path": ".users[*].name", "raw": "true"}, want: "ada\nbob"},
		"json query last":   {tool: "jq", input: `{"a":[1,2,3]}`, opts: map[string]string{"path": ".a[-1]"}, want: "3"},
		"css minify":        {tool: "css", input: "a {\n  color: red;\n  margin: 0px;\n}\n", opts: map[string]string{"mode": "minify"}, contains: []string{"a{color:red;margin:0"}},
		"sql upper":         {tool: "sql", input: "select a,b from t where x=1 and y=2", contains: []string{"SELECT\n  a,\n  b\nFROM\n  t\nWHERE\n  x = 1\n  AND y = 2"}},
		"lines sort dedupe": {tool: "lines", input: "b\na\nb\nc", opts: map[string]string{"sort": "asc", "dedupe": "true"}, want: "a\nb\nc"},
		"lines natural":     {tool: "lines", input: "file10\nfile2\nfile1", opts: map[string]string{"sort": "natural"}, want: "file1\nfile2\nfile10"},
		"xml format":        {tool: "xml", input: `<a><b x="1">t</b><c/></a>`, contains: []string{"<a>\n  <b x=\"1\">t</b>\n  <c />\n</a>"}},
		// encode
		"base64 enc":      {tool: "base64", input: "hello world", want: "aGVsbG8gd29ybGQ="},
		"base64 dec":      {tool: "base64", input: "aGVsbG8gd29ybGQ", opts: map[string]string{"mode": "decode"}, want: "hello world"},
		"base64 urlsafe":  {tool: "base64", input: "\xfb\xff", opts: map[string]string{"urlsafe": "true", "nopad": "true"}, want: "-_8"},
		"url encode":      {tool: "url", input: "a b&c=d/é", want: "a%20b%26c%3Dd%2F%C3%A9"},
		"url decode":      {tool: "url", input: "a%20b%26c", opts: map[string]string{"mode": "decode"}, want: "a b&c"},
		"html entity":     {tool: "html-entity", input: `<a href="x">&</a>`, want: "&lt;a href=&quot;x&quot;&gt;&amp;&lt;/a&gt;"},
		"html entity dec": {tool: "html-entity", input: "&lt;&#x41;&eacute;&#169;", opts: map[string]string{"mode": "decode"}, want: "<Aé©"},
		"backslash esc":   {tool: "backslash", input: "a\"b\n\tc\\", want: `a\"b\n\tc\\`},
		"backslash unesc": {tool: "backslash", input: `xé\x41\n😀`, opts: map[string]string{"mode": "unescape"}, want: "xéA\n😀"},
		"cert sample":     {tool: "cert", input: "SAMPLE", contains: []string{"Certificate:", "Signature Algorithm"}},
		// convert
		"yaml2json":     {tool: "yaml2json", input: "b: 1\na:\n  - x\n  - true\n", want: "{\n  \"b\": 1,\n  \"a\": [\n    \"x\",\n    true\n  ]\n}"},
		"json2yaml":     {tool: "json2yaml", input: `{"b":1,"a":["x","1"]}`, want: "b: 1\na:\n  - x\n  - \"1\"\n"},
		"base convert":  {tool: "base", input: "0xff", field: [2]string{"Decimal (10)", "255"}},
		"csv2json":      {tool: "csv2json", input: "id,name,ok\n1,Ada,true\n2,\"Lovelace, A\",false", contains: []string{`"name": "Lovelace, A"`, `"id": 1`, `"ok": true`}},
		"json2csv":      {tool: "json2csv", input: `[{"a":1,"b":{"c":"x,y"}},{"a":2}]`, want: "a,b.c\n1,\"x,y\"\n2,\n"},
		"case snake":    {tool: "case", input: "requestURLDecoderID", opts: map[string]string{"to": "snake_case"}, want: "request_url_decoder_id"},
		"case camel":    {tool: "case", input: "hello-world_foo bar", opts: map[string]string{"to": "camelCase"}, want: "helloWorldFooBar"},
		"hex2ascii":     {tool: "hex2ascii", input: "48 65 6c 6c 6f", want: "Hello"},
		"ascii2hex":     {tool: "ascii2hex", input: "Hi", contains: []string{"48 69"}},
		"url parse":     {tool: "url-parse", input: "https://user:pw@example.com:8080/p/a?x=1&x=2&y=%20#frag", contains: []string{"example.com", "8080", "frag", `"x": [`}},
		"html2jsx":      {tool: "html2jsx", input: `<label for="a" class="b" style="font-size: 12px">x</label>`, contains: []string{`htmlFor="a"`, `className="b"`, `style={{ fontSize: '12px' }}`}},
		"curl to go":    {tool: "curl", input: `curl -u ada:pw https://x.io/api -H 'Accept: application/json'`, opts: map[string]string{"lang": "go"}, contains: []string{`req.SetBasicAuth("ada", "pw")`, `"Accept", "application/json"`}},
		"curl to fetch": {tool: "curl", input: `curl -X PUT https://x.io -d 'a=1'`, opts: map[string]string{"lang": "javascript"}, contains: []string{`method: 'PUT'`, `fetch('https://x.io'`, `body: 'a=1'`}},
		"json2code ts":  {tool: "json2code", input: `{"id":1,"tags":["a"],"owner":{"name":"x"}}`, opts: map[string]string{"lang": "typescript"}, contains: []string{"export interface Root", "tags: string[]", "owner: Owner"}},
		"svg2css":       {tool: "svg2css", input: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><path d="M0 0h1"/></svg>`, contains: []string{"background-image: url(\"data:image/svg+xml,"}},
		// generate
		"hash sha256":   {tool: "hash", input: "hello", opts: map[string]string{"algorithm": "sha256"}, want: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		"hash hmac":     {tool: "hash", input: "The quick brown fox jumps over the lazy dog", opts: map[string]string{"algorithm": "md5", "hmac_key": "key"}, want: "80070713463e7749b90c2dc24911e275"},
		"hash crc32":    {tool: "hash", input: "hello", opts: map[string]string{"algorithm": "crc32"}, want: "3610a686"},
		"uuid decode":   {tool: "uuid", input: "01890a5d-ac96-774b-bcce-b302099a8057", opts: map[string]string{"mode": "decode"}, contains: []string{"7", "2023-06-"}},
		"uuid v5":       {tool: "uuid", input: "", opts: map[string]string{"type": "v5", "namespace": "dns", "name": "example.com"}, want: "cfbff0d1-9375-5685-968c-48ce8b15ae17"},
		"lorem":         {tool: "lorem", input: "", opts: map[string]string{"unit": "words", "count": "5"}, contains: []string{"Lorem ipsum"}},
		"random length": {tool: "random", input: "", opts: map[string]string{"length": "24", "count": "1", "symbols": "false"}, contains: []string{"Entropy"}},
		// inspect
		"time seconds":         {tool: "time", input: "1700000000", want: "2023-11-14T22:13:20Z"},
		"time millis":          {tool: "time", input: "1700000000123", want: "2023-11-14T22:13:20.123Z"},
		"time iso to unix":     {tool: "time", input: "2023-11-14T22:13:20Z", want: "1700000000"},
		"time math":            {tool: "time", input: "1700000000 + 1d", want: "2023-11-15T22:13:20Z"},
		"jwt hs256 verify":     {tool: "jwt", input: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", opts: map[string]string{"secret": "your-256-bit-secret"}, field: [2]string{"Signature", "Signature verified"}},
		"jwt bad secret":       {tool: "jwt", input: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", opts: map[string]string{"secret": "nope"}, contains: []string{"Invalid signature"}},
		"regex replace named":  {tool: "regex", input: "ada@example.com", opts: map[string]string{"pattern": `(?<u>\w+)@(?<d>[\w.]+)`, "mode": "replace", "replacement": "$<d>/$<u>"}, want: "example.com/ada"},
		"regex lookahead hint": {tool: "regex", input: "x", opts: map[string]string{"pattern": `a(?=b)`}, wantErr: "RE2"},
		"cron describe":        {tool: "cron", input: "0 9 * * 1-5", opts: map[string]string{"tz": "UTC"}, field: [2]string{"Description", "At 09:00, Monday through Friday"}},
		"cron every 15":        {tool: "cron", input: "*/15 * * * *", opts: map[string]string{"tz": "UTC"}, field: [2]string{"Description", "Every 15 minutes"}},
		"cron invalid":         {tool: "cron", input: "61 * * * *", wantErr: "minute"},
		"color hex":            {tool: "color", input: "#5CCC7F", field: [2]string{"RGB", "rgb(92, 204, 127)"}},
		"color hsl":            {tool: "color", input: "hsl(0, 100%, 50%)", field: [2]string{"Hex", "#FF0000"}},
		"color named":          {tool: "color", input: "rebeccapurple", field: [2]string{"Hex", "#663399"}},
		"color alpha":          {tool: "color", input: "rgba(0,0,0,.5)", field: [2]string{"Hex with alpha", "#00000080"}},
		"diff identical":       {tool: "diff", input: "a\nb\n", opts: map[string]string{"other": "a\nb\n"}, want: "No differences."},
		"diff unified":         {tool: "diff", input: "a\nb\nc\n", opts: map[string]string{"other": "a\nB\nc\n"}, contains: []string{"@@ -1,3 +1,3 @@", "-b\n+B\n"}},
		"diff words":           {tool: "diff", input: "the quick fox", opts: map[string]string{"other": "the slow fox", "mode": "words"}, want: "the [-quick-]{+slow+} fox"},
		"inspect counts":       {tool: "inspect", input: "héllo 👨‍👩‍👧", contains: []string{"Characters: 7", "Code points: 11", "Bytes (UTF-8): 25"}},
		"inspect invisible":    {tool: "inspect", input: "a​b", contains: []string{"ZERO WIDTH SPACE"}},
		"markdown":             {tool: "markdown", input: "# Hi\n\n- [x] done", contains: []string{`<h1 id="hi">Hi</h1>`, `checked`}},
		"bcrypt verify":        {tool: "bcrypt", input: "secret", opts: map[string]string{"mode": "verify", "hash": "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"}, contains: []string{"does NOT match"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { runTC(t, reg, c) })
	}
}

func TestDetection(t *testing.T) {
	reg := Registry()
	cases := map[string]string{
		`{"a": 1, "b": [1, 2]}`: "json",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxIn0.c2ln": "jwt",
		"1700000000":                               "time",
		"550e8400-e29b-41d4-a716-446655440000":     "uuid",
		"*/5 * * * *":                              "cron",
		"#ff8800":                                  "color",
		"rgb(10, 20, 30)":                          "color",
		"curl -X POST https://x.io -d 'a=1'":       "curl",
		"SELECT id, name FROM users WHERE id = 1":  "sql",
		"https://example.com/path?q=1&x=2":         "url-parse",
		"SGVsbG8sIFdvcmxkISBUaGlzIGlzIGJhc2U2NA==": "base64",
		"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----": "cert",
		"<!DOCTYPE html><html><body><p>x</p></body></html>":            "html",
		"name: x\nitems:\n  - a\n  - b\n":                              "yaml2json",
		"%E2%9C%93%20done%20%26%20more":                                "url",
	}
	for in, want := range cases {
		hits := reg.Detect(in)
		if len(hits) == 0 || hits[0].ID != want {
			got := "none"
			if len(hits) > 0 {
				got = hits[0].ID
			}
			t.Errorf("Detect(%.40q) top = %s, want %s", in, got, want)
		}
	}
}
