package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devknife/internal/tools"
)

func newTestEnv(stdin string, tty bool) (*env, *bytes.Buffer, *bytes.Buffer, *string) {
	var out, errb bytes.Buffer
	clip := "clipboard text"
	e := &env{
		stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb,
		stdinPiped: !tty, stdoutTTY: false, reg: tools.Registry(),
		readClip:    func() (string, error) { return clip, nil },
		writeClip:   func(s string) error { clip = s; return nil },
		openBrowser: func(string) error { return nil },
	}
	return e, &out, &errb, &clip
}

func TestCLI(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "in.json")
	_ = os.WriteFile(file, []byte(`{"z":1,"a":2}`), 0o644)
	cases := []struct {
		name  string
		args  []string
		stdin string
		tty   bool
		want  string
	}{
		{"positional mode + input", []string{"b64", "decode", "SGVsbG8="}, "", true, "Hello\n"},
		{"mode prefix", []string{"base64", "dec", "SGVsbG8="}, "", true, "Hello\n"},
		{"stdin pipe", []string{"json", "--mode", "minify"}, "{ \"a\" : 1 }\n", false, "{\"a\":1}\n"},
		{"file flag + bool", []string{"json", "-f", file, "--sort", "--mode=minify"}, "", true, "{\"a\":2,\"z\":1}\n"},
		{"no- prefix bool", []string{"json", "--no-lenient", "-m", "minify", `{"a":1}`}, "", true, "{\"a\":1}\n"},
		{"optional select arg", []string{"hash", "md5", "hello"}, "", true, "5d41402abc4b2a76b9719d911017c592\n"},
		{"input that is not a choice", []string{"hash", "hello"}, "", true, "SHA256           2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824\n"},
		{"multi-word input joins", []string{"case", "snake", "hello", "big", "world"}, "", true, "hello_big_world\n"},
		{"attached short value", []string{"uuid", "v5", "--namespace", "dns", "--name", "example.com"}, "", true, "cfbff0d1-9375-5685-968c-48ce8b15ae17\n"},
		{"diff two files", []string{"diff", file, file}, "", true, "No differences.\n"},
		{"auto detect stdin", nil, "1700000000\n", false, "2023-11-14T22:13:20Z\n"},
		{"paste flag", []string{"inspect", "-p", "--show", "chars"}, "", true, "t: 2\n"},
		{"command by ID with a value", []string{"cmds", "kill-port", "--set", "port=8080"}, "", true, "lsof -ti tcp:8080 -sTCP:LISTEN | xargs -r kill -9\n"},
		{"multi-word option argument joins", []string{"cmds", "--os", "macOS", "kill", "port"}, "", true, "# Kill the process using a port (careful)\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, out, errb, _ := newTestEnv(c.stdin, c.tty)
			if err := e.run(c.args); err != nil {
				t.Fatalf("error: %v (stderr %s)", err, errb.String())
			}
			if c.name == "paste flag" || c.name == "input that is not a choice" || c.name == "multi-word option argument joins" {
				if !strings.Contains(out.String(), c.want) {
					t.Fatalf("got %q", out.String())
				}
				return
			}
			if out.String() != c.want {
				t.Fatalf("got %q want %q", out.String(), c.want)
			}
		})
	}
}

func TestCLIErrorsAndJSON(t *testing.T) {
	e, _, _, _ := newTestEnv("", true)
	if err := e.run([]string{"json", "--bogus"}); err == nil || !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("expected flag hint, got %v", err)
	}
	if err := e.run([]string{"jsn"}); err == nil || !strings.Contains(err.Error(), "did you mean: json") {
		t.Fatalf("expected suggestion, got %v", err)
	}
	if err := e.run([]string{"json"}); err == nil || !strings.Contains(err.Error(), "no input") {
		t.Fatalf("expected no input error, got %v", err)
	}
	if err := e.run([]string{"json", "--mode", "nope", "{}"}); err == nil {
		t.Fatal("expected invalid choice error")
	}

	e, out, _, _ := newTestEnv("", true)
	if err := e.run([]string{"color", "--json", "#ff0000"}); err != nil {
		t.Fatal(err)
	}
	var res struct {
		Tool   string `json:"tool"`
		Fields []struct{ Label, Value string }
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Tool != "color" || len(res.Fields) == 0 {
		t.Fatalf("bad --json output: %v %s", err, out.String())
	}

	e, _, _, clip := newTestEnv("", true)
	if err := e.run([]string{"b64", "hi", "--copy"}); err != nil || *clip != "aGk=" {
		t.Fatalf("copy: %v %q", err, *clip)
	}

	dir := t.TempDir()
	png := filepath.Join(dir, "qr.png")
	e, _, _, _ = newTestEnv("", true)
	if err := e.run([]string{"qr", "hello", "-o", png}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(png); !bytes.HasPrefix(b, []byte("\x89PNG")) {
		t.Fatal("qr -o did not write a PNG")
	}
}

func TestListHelpCompletion(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"list", "json"}, {"help"}, {"json", "--help"}, {"completion", "zsh"}, {"completion", "bash"}, {"completion", "fish"}, {"version"}} {
		e, out, _, _ := newTestEnv("", true)
		if err := e.run(args); err != nil || out.Len() == 0 {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestStdinHandling(t *testing.T) {
	// Generators leave piped stdin alone, so `while read x; do devknife uuid; done` works.
	e, out, _, _ := newTestEnv("b\nc\n", false)
	if err := e.run([]string{"uuid"}); err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(e.stdin); string(rest) != "b\nc\n" {
		t.Fatalf("uuid swallowed stdin; left %q", rest)
	}
	if got := strings.TrimSpace(out.String()); len(got) != 36 {
		t.Fatalf("uuid output %q", got)
	}
	// decoding still reads stdin
	e, out, _, _ = newTestEnv("cfbff0d1-9375-5685-968c-48ce8b15ae17\n", false)
	if err := e.run([]string{"uuid", "decode"}); err != nil || !strings.Contains(out.String(), "cfbff0d1") {
		t.Fatalf("uuid decode from stdin: %v %q", err, out.String())
	}
	// "-" asks for stdin explicitly
	e, out, _, _ = newTestEnv("hi\n", false)
	if err := e.run([]string{"base64", "-"}); err != nil || out.String() != "aGk=\n" {
		t.Fatalf("base64 -: %v %q", err, out.String())
	}
	// `echo x |` drops the newline; `< file` keeps it, matching -f file
	e, out, _, _ = newTestEnv("hello world\n", false)
	if err := e.run([]string{"base64"}); err != nil || out.String() != "aGVsbG8gd29ybGQ=\n" {
		t.Fatalf("piped: %v %q", err, out.String())
	}
	e, out, _, _ = newTestEnv("hello world\n", false)
	e.stdinRegular = true
	if err := e.run([]string{"base64"}); err != nil || out.String() != "aGVsbG8gd29ybGQK\n" {
		t.Fatalf("redirected file: %v %q", err, out.String())
	}
}

func TestHashFileArgument(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f.txt")
	_ = os.WriteFile(f, []byte("hello world\n"), 0o644)
	e, out, errb, _ := newTestEnv("", true)
	if err := e.run([]string{"hash", "sha256", f}); err != nil {
		t.Fatal(err)
	}
	// sha256 of the file's bytes, not of its path
	if out.String() != "a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447\n" {
		t.Fatalf("hash of a file argument: %q", out.String())
	}
	if !strings.Contains(errb.String(), "contents of the file") {
		t.Fatalf("expected a note on stderr, got %q", errb.String())
	}
}

// A tool option named like a global flag is unreachable from the CLI
// (svg2css --output used to write a file instead of picking a format).
func TestToolOptionsDontShadowGlobalFlags(t *testing.T) {
	globals := map[string]bool{"file": true, "output": true, "paste": true, "copy": true, "json": true, "quiet": true, "open": true, "help": true}
	shorts := map[string]bool{"f": true, "o": true, "p": true, "c": true, "j": true, "h": true}
	for _, tool := range tools.Registry().All() {
		for _, o := range tool.Options {
			if globals[strings.ReplaceAll(o.Key, "_", "-")] {
				t.Errorf("%s: option --%s collides with a global flag", tool.ID, o.Key)
			}
			if shorts[o.Short] {
				t.Errorf("%s: option -%s collides with a global flag", tool.ID, o.Short)
			}
		}
	}
	svg := filepath.Join(t.TempDir(), "i.svg")
	_ = os.WriteFile(svg, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o644)
	e, out, _, _ := newTestEnv("", true)
	if err := e.run([]string{"svg2css", "--format", "mask", "-f", svg}); err != nil || !strings.Contains(out.String(), "mask") {
		t.Fatalf("svg2css --format mask: %v %q", err, out.String())
	}
}

func TestOpenIsSandboxed(t *testing.T) {
	var opened string
	e, _, _, _ := newTestEnv("", true)
	e.openBrowser = func(p string) error { opened = p; return nil }
	if err := e.run([]string{"markdown", "--open", "# hi <script>alert(1)</script> ![](https://tracker.example/p.gif)"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if !strings.HasPrefix(strings.ToLower(html), "<!doctype html><meta http-equiv=\"content-security-policy\"") {
		t.Fatalf("CSP must follow the doctype: %.120s", html)
	}
	if !strings.Contains(html, "script-src 'none'") || !strings.Contains(html, "default-src 'none'") {
		t.Fatalf("preview file policy: %.300s", html)
	}
}

func TestUIRefusesNonLoopback(t *testing.T) {
	e, _, _, _ := newTestEnv("", true)
	if err := e.run([]string{"ui", "--addr", "0.0.0.0:7777", "--no-open"}); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("want a loopback error, got %v", err)
	}
}
