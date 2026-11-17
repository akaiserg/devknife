package tools

// Regression tests for the hardening in CODE-REVIEW.md: hostile input must
// produce an error quickly, never crash the process, and security verdicts
// must not claim more than they checked.

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"hash/crc32"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"devknife/internal/core"
)

func run(t *testing.T, id, input string, opts map[string]string) (*core.Result, error) {
	t.Helper()
	tool, ok := Registry().Get(id)
	if !ok {
		t.Fatalf("no tool %q", id)
	}
	if opts == nil {
		opts = map[string]string{}
	}
	return tool.Execute(core.Request{Input: input, Opts: opts})
}

func nested(open, close string, n int) string {
	return strings.Repeat(open, n) + strings.Repeat(close, n)
}

// H1: millions of nesting levels used to overflow the goroutine stack, a
// fatal error that recover() can't catch.
func TestDeepJSONIsAnErrorNotACrash(t *testing.T) {
	deepArr := nested("[", "]", 3_000_000)
	deepObj := strings.Repeat(`{"a":`, 1_000_000) + "1" + strings.Repeat("}", 1_000_000)
	for _, c := range []struct {
		tool string
		opts map[string]string
	}{
		{"json", nil}, {"json", map[string]string{"mode": "minify"}}, {"json", map[string]string{"lenient": "false"}},
		{"json2yaml", nil}, {"json2csv", nil}, {"json2code", nil}, {"json-query", map[string]string{"path": "..a"}},
		{"jwt", map[string]string{"mode": "encode", "secret": "k"}},
	} {
		for name, in := range map[string]string{"arrays": deepArr, "objects": deepObj} {
			start := time.Now()
			_, err := run(t, c.tool, in, c.opts)
			if err == nil || !strings.Contains(err.Error(), "nesting is deeper than") {
				t.Errorf("%s %v (%s): want a nesting error, got %v", c.tool, c.opts, name, err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("%s (%s) took %s", c.tool, name, d)
			}
		}
	}
	// the limit itself: 1000 levels are fine, 1001 are not
	if _, err := run(t, "json", nested("[", "]", maxJSONDepth), nil); err != nil {
		t.Fatalf("%d levels should parse: %v", maxJSONDepth, err)
	}
	if _, err := run(t, "json", nested("[", "]", maxJSONDepth+1), nil); err == nil {
		t.Fatalf("%d levels should be refused", maxJSONDepth+1)
	}
}

// A few nested YAML anchors expand exponentially ("billion laughs").
func TestYAMLAliasBombAndDepth(t *testing.T) {
	laughs := `a: &a ["lol","lol","lol","lol","lol","lol","lol","lol","lol"]
b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]
c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]
d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]
e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]
f: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e]
g: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f]
h: &h [*g,*g,*g,*g,*g,*g,*g,*g,*g]
`
	start := time.Now()
	if _, err := run(t, "yaml2json", laughs, nil); err == nil || !strings.Contains(err.Error(), "aliases expand") {
		t.Fatalf("want alias-expansion error, got %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("alias bomb took %s", d)
	}
	// ordinary anchors and merge keys still work
	if res, err := run(t, "yaml2json", "base: &b {x: 1}\nuse:\n  <<: *b\n  y: 2\n", nil); err != nil || !strings.Contains(res.Output, `"x": 1`) {
		t.Fatalf("merge keys: %v %v", err, res)
	}
	if _, err := run(t, "yaml2json", nested("[", "]", 2000), nil); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Fatalf("deep YAML: %v", err)
	}
}

// pngHeader is a PNG that declares w×h pixels but carries no image data:
// enough for image.DecodeConfig, which is all the size check may read.
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(typ string, data []byte) {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(typ)
		b.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(typ))
		crc.Write(data)
		_ = binary.Write(&b, binary.BigEndian, crc.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // 8-bit grayscale
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	return b.Bytes()
}

// H2: a 140 KB PNG of 12000×12000 used to allocate hundreds of MB.
func TestQRReaderRefusesHugeImages(t *testing.T) {
	tool, _ := Registry().Get("qr")
	_, err := tool.Execute(core.Request{Data: pngHeader(12000, 12000), Opts: map[string]string{"mode": "read"}})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("want a size error, got %v", err)
	}
	// a normal QR code still reads back
	gen, err := tool.Execute(core.Request{Input: "https://example.com/round-trip", Opts: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(gen.Image, "data:image/png;base64,"))
	res, err := tool.Execute(core.Request{Data: png, Opts: map[string]string{"mode": "read"}})
	if err != nil || res.Output != "https://example.com/round-trip" {
		t.Fatalf("round trip: %v %v", err, res)
	}
}

func b64url(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func jwtStatus(t *testing.T, token, secret string) (status, msg string) {
	t.Helper()
	res, err := run(t, "jwt", token, map[string]string{"secret": secret})
	if err != nil {
		t.Fatal(err)
	}
	d := res.Data.(map[string]any)
	return d["status"].(string), d["message"].(string)
}

func pemBlock(typ string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}))
}

// H4 and L1: the debugger must never call an unverified token "verified".
func TestJWTVerdicts(t *testing.T) {
	// alg: none (any capitalization) is "unsigned", even with a key entered
	for _, alg := range []string{"none", "None", "NONE"} {
		tok := b64url(`{"alg":"`+alg+`"}`) + "." + b64url(`{"sub":"admin"}`) + "."
		for _, secret := range []string{"", "anything"} {
			if st, _ := jwtStatus(t, tok, secret); st != "unsigned" {
				t.Errorf("alg %s, secret %q: status %q, want unsigned", alg, secret, st)
			}
		}
	}

	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	pubDER, _ := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	pubPEM := pemBlock("PUBLIC KEY", pubDER)
	privDER, _ := x509.MarshalPKCS8PrivateKey(rsaKey)
	privPEM := pemBlock("PRIVATE KEY", privDER)

	// RS256 → HS256 algorithm confusion: HMAC "signed" with the public key text
	forged, err := run(t, "jwt", `{"sub":"admin"}`, map[string]string{"mode": "encode", "alg": "HS256", "secret": pubPEM})
	if err != nil {
		t.Fatal(err)
	}
	if len(forged.Warnings) == 0 {
		t.Error("signing HS256 with a PEM key should warn")
	}
	if st, msg := jwtStatus(t, forged.Output, pubPEM); st != "invalid" || !strings.Contains(msg, "algorithm-confusion") {
		t.Fatalf("forged HS256: %s %q", st, msg)
	}
	// a genuine RS256 token with the same key verifies
	rs, _ := run(t, "jwt", `{"sub":"admin"}`, map[string]string{"mode": "encode", "alg": "RS256", "secret": privPEM})
	if st, msg := jwtStatus(t, rs.Output, pubPEM); st != "verified" {
		t.Fatalf("RS256: %s %q", st, msg)
	}

	// PS256: salt length equals the hash length (RFC 7518 §3.5)
	ps, err := run(t, "jwt", `{"sub":"admin"}`, map[string]string{"mode": "encode", "alg": "PS256", "secret": privPEM})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(ps.Output, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPSS(&rsaKey.PublicKey, crypto.SHA256, digest[:], sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}); err != nil {
		t.Fatalf("PS256 salt is not the hash length: %v", err)
	}
	if st, _ := jwtStatus(t, ps.Output, pubPEM); st != "verified" {
		t.Fatalf("PS256 should verify: %s", st)
	}
	// a max-salt PSS signature (Go's default) is flagged, not silently accepted
	maxSalt, _ := rsa.SignPSS(rand.Reader, rsaKey, crypto.SHA256, digest[:], nil)
	tok := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(maxSalt)
	if st, msg := jwtStatus(t, tok, pubPEM); st != "invalid" || !strings.Contains(msg, "salt length") {
		t.Fatalf("max-salt PSS: %s %q", st, msg)
	}

	// ES256: signature length and curve must match the algorithm
	ec256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ec256DER, _ := x509.MarshalPKCS8PrivateKey(ec256)
	es, err := run(t, "jwt", `{"sub":"admin"}`, map[string]string{"mode": "encode", "alg": "ES256", "secret": pemBlock("PRIVATE KEY", ec256DER)})
	if err != nil {
		t.Fatal(err)
	}
	pub256, _ := x509.MarshalPKIXPublicKey(&ec256.PublicKey)
	if st, _ := jwtStatus(t, es.Output, pemBlock("PUBLIC KEY", pub256)); st != "verified" {
		t.Fatalf("ES256 should verify: %s", st)
	}
	p := strings.Split(es.Output, ".")
	s, _ := base64.RawURLEncoding.DecodeString(p[2])
	short := p[0] + "." + p[1] + "." + base64.RawURLEncoding.EncodeToString(s[1:])
	if st, msg := jwtStatus(t, short, pemBlock("PUBLIC KEY", pub256)); st != "invalid" || !strings.Contains(msg, "64 bytes") {
		t.Fatalf("short ES256 signature: %s %q", st, msg)
	}
	// ES256 header, but signed (validly) with a P-384 key
	ec384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	hdr := b64url(`{"alg":"ES256"}`) + "." + b64url(`{"sub":"admin"}`)
	d := sha256.Sum256([]byte(hdr))
	r, ss, _ := ecdsa.Sign(rand.Reader, ec384, d[:])
	raw := make([]byte, 96)
	r.FillBytes(raw[:48])
	ss.FillBytes(raw[48:])
	pub384, _ := x509.MarshalPKIXPublicKey(&ec384.PublicKey)
	if st, msg := jwtStatus(t, hdr+"."+base64.RawURLEncoding.EncodeToString(raw), pemBlock("PUBLIC KEY", pub384)); st != "invalid" || !strings.Contains(msg, "P-256") {
		t.Fatalf("ES256 with a P-384 key: %s %q", st, msg)
	}
	// signing refuses the mismatch too
	ec384DER, _ := x509.MarshalPKCS8PrivateKey(ec384)
	if _, err := run(t, "jwt", `{}`, map[string]string{"mode": "encode", "alg": "ES256", "secret": pemBlock("PRIVATE KEY", ec384DER)}); err == nil {
		t.Fatal("ES256 signing with a P-384 key should fail")
	}
}

// regexReplace must give exactly what ReplaceAllString gives.
func TestRegexReplaceMatchesStdlib(t *testing.T) {
	patterns := []string{`x*`, `a|`, `\b`, `(?m)^`, `(?m)$`, `(a)(b)?`, `a*?`, `(?P<w>\w+)`, `é*`, ``, `.`, `\s+`}
	inputs := []string{"", "abc", "aaa bbb", "héllo wörld", "a\nb\nc", "xaxbx", "ab ab a"}
	templates := []string{"", "[$0]", "${1}-", "$$", "<${w}>", "$w!", "x"}
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		for _, in := range inputs {
			for _, tmpl := range templates {
				got, err := regexReplace(re, in, tmpl)
				if want := re.ReplaceAllString(in, tmpl); err != nil || got != want {
					t.Errorf("replace /%s/ in %q with %q: got %q (%v), want %q", p, in, tmpl, got, err, want)
				}
			}
		}
	}
}

// H3: outputs that dwarf the input are refused before they are built.
func TestRegexOutputAmplification(t *testing.T) {
	start := time.Now()
	_, err := run(t, "regex", strings.Repeat("a", 200_000), map[string]string{"pattern": "x*", "mode": "replace", "replacement": strings.Repeat("b", 2048)})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("replace: want a size error, got %v", err)
	}
	_, err = run(t, "regex", strings.Repeat("a", 1_000_000), map[string]string{"pattern": "(a+)", "mode": "template", "replacement": strings.Repeat("$1", 100_000)})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("template: want a size error, got %v", err)
	}
	_, err = run(t, "regex", strings.Repeat("a", 1_100_000), map[string]string{"pattern": "x*", "mode": "split"})
	if err == nil || !strings.Contains(err.Error(), "matches more than") {
		t.Fatalf("split: want a match-count error, got %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("amplification checks took %s", d)
	}
}

// H3: verifying against a hash with an absurd cost would take days.
func TestBcryptVerifyCostCap(t *testing.T) {
	start := time.Now()
	_, err := run(t, "bcrypt", "password", map[string]string{"mode": "verify", "hash": "$2a$31$" + strings.Repeat("a", 53)})
	if err == nil || !strings.Contains(err.Error(), "cost 31") {
		t.Fatalf("want a cost error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("the cost check should not run bcrypt")
	}
}

// L5: Vixie cron ANDs the day fields when either starts with "*".
func TestCronDayFieldRules(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c, err := parseCron("0 0 */2 * 1")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range c.next(from, 8) {
		if r.Weekday() != time.Monday || r.Day()%2 != 1 {
			t.Fatalf("*/2 with Monday fired on %s", r.Format("Mon Jan 2"))
		}
	}
	// both restricted: OR, so Fridays and 13ths both fire
	c, _ = parseCron("0 0 13 * 5")
	var friday, thirteenth bool
	for _, r := range c.next(from, 10) {
		friday = friday || (r.Weekday() == time.Friday && r.Day() != 13)
		thirteenth = thirteenth || (r.Day() == 13 && r.Weekday() != time.Friday)
	}
	if !friday || !thirteenth {
		t.Fatal("13 + Friday should fire on either")
	}
	c, _ = parseCron("0 0 * * 0,7")
	if d := describeCron(c); strings.Count(d, "Sunday") != 1 {
		t.Fatalf("Sunday listed twice: %q", d)
	}
}

// L6: the Rust raw string must outlast any "### in the body.
func TestCurlRustRawString(t *testing.T) {
	res, err := run(t, "curl", "curl https://e.com --data-raw 'a`b\"#c\"##d'", map[string]string{"lang": "rust"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "r###\"a`b\"#c\"##d\"###") {
		t.Fatalf("raw string delimiter:\n%s", res.Output)
	}
}

// v1/v6 UUIDs must not carry the machine's MAC address.
func TestUUIDNodeIsRandom(t *testing.T) {
	for _, typ := range []string{"v1", "v6"} {
		gen, err := run(t, "uuid", "", map[string]string{"type": typ})
		if err != nil {
			t.Fatal(err)
		}
		dec, _ := decodeID(gen.Output)
		found := false
		for _, f := range dec.Fields {
			found = found || f.Label == "Node (random)"
		}
		if !found {
			t.Fatalf("%s node is not random: %v", typ, dec.Fields)
		}
	}
}

// Recursive-descent parsers need a depth limit too.
func TestDeepTimeExpressionIsAnError(t *testing.T) {
	for _, in := range []string{nested("(", ")", 2_000_000), strings.Repeat("-", 2_000_000) + "1"} {
		if _, err := run(t, "time", "now + "+in, nil); err == nil || !strings.Contains(err.Error(), "nested deeper than") {
			t.Errorf("want a nesting error, got %v", err)
		}
	}
	if res, err := run(t, "time", "1700000000 + (((1h)))", nil); err != nil || res.Output == "" {
		t.Errorf("normal parentheses: %v", err)
	}
}

// Indentation grows with depth × lines, so many nests that each stay under
// the depth limit still multiply the output ~1000×. The run must stop at
// core.MaxOutput while writing, not build gigabytes first.
func TestIndentAmplificationIsBounded(t *testing.T) {
	for _, c := range []struct{ tool, in string }{
		{"json", "[" + strings.Repeat(nested("[", "]", 999)+",", 2500) + "1]"},
		{"css", strings.Repeat(nested("a{", "}", 999), 2500)},
	} {
		start := time.Now()
		_, err := run(t, c.tool, c.in, nil)
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Errorf("%s: want a size error, got %v", c.tool, err)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s took %s", c.tool, d)
		}
	}
}

// The markup renderers recurse per level; deep nesting must be an error,
// not a fatal stack overflow.
func TestDeepMarkupIsAnError(t *testing.T) {
	for _, tool := range []string{"html", "xml", "erb", "html2jsx"} {
		_, err := run(t, tool, nested("<a>", "</a>", 200_000), nil)
		if err == nil || !strings.Contains(err.Error(), "nested deeper than") {
			t.Errorf("%s: want a nesting error, got %v", tool, err)
		}
	}
	if _, err := run(t, "html", nested("<div>", "</div>", maxMarkupDepth-1), nil); err != nil {
		t.Errorf("%d levels should format: %v", maxMarkupDepth-1, err)
	}
}

// Every tool, fed hostile shapes, must return (result or error) in bounded
// time. A stack overflow or out-of-memory kills the test binary, which fails
// the run. Opt-in (DEVKNIFE_HOSTILE=1) because it takes about two minutes;
// one tool: -run 'TestEveryToolSurvivesHostileInput/markdown'.
func TestEveryToolSurvivesHostileInput(t *testing.T) {
	if os.Getenv("DEVKNIFE_HOSTILE") == "" {
		t.Skip("set DEVKNIFE_HOSTILE=1 to run")
	}
	inputs := map[string]string{
		"deep brackets":     nested("[", "]", 3_000_000),
		"deep braces":       strings.Repeat(`{"a":`, 1_000_000) + "1" + strings.Repeat("}", 1_000_000),
		"deep parens":       nested("(", ")", 2_000_000),
		"deep tags":         nested("<a>", "</a>", 500_000),
		"10 MB single line": strings.Repeat("ab=c;", 2_000_000),
		// each block stays under the depth limits, but indentation multiplies
		// the output ~1000×: amplification has to be stopped while writing
		"many deep JSON nests": "[" + strings.Repeat(nested("[", "]", 999)+",", 2500) + "1]",
		"many deep CSS blocks": strings.Repeat(nested("a{", "}", 999), 2500),
		"many deep tag blocks": strings.Repeat(nested("<a>", "</a>", 999), 1500),
	}
	// Generous: this checks that every run ends, not speed. Markdown is the
	// slowest, ~25 s on 3M nested brackets (linear, ~7.5 µs per level).
	const budget = 60 * time.Second
	runner := core.NewRunner(budget, 0)
	for _, tool := range Registry().All() {
		t.Run(tool.ID, func(t *testing.T) {
			for name, in := range inputs {
				start := time.Now()
				_, err := runner.Run(t.Context(), tool, core.Request{Input: in, Opts: map[string]string{}})
				var te *core.TimeoutError
				if errors.As(err, &te) {
					t.Errorf("%s: no answer within %s", name, budget)
				}
				if d := time.Since(start); d > 5*time.Second {
					t.Logf("%s: slow (%s)", name, d.Round(time.Millisecond))
				}
			}
		})
	}
}
