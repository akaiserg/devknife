package tools

import (
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"devknife/internal/core"
)

func runCmds(t *testing.T, opts map[string]string) *core.Result {
	t.Helper()
	tool, ok := Registry().Get("commands")
	if !ok {
		t.Fatal("no commands tool")
	}
	res, err := tool.Execute(core.Request{Opts: opts})
	if err != nil {
		t.Fatalf("%v: %v", opts, err)
	}
	return res
}

func cmdItems(res *core.Result) []cmdItem {
	return res.Data.(map[string]any)["items"].([]cmdItem)
}

var reKebab = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestCommandLibraryIsWellFormed(t *testing.T) {
	ids := map[string]bool{}
	for _, c := range shellCmds {
		if !reKebab.MatchString(c.ID) || ids[c.ID] {
			t.Errorf("%q: IDs must be unique kebab-case", c.ID)
		}
		ids[c.ID] = true
		if c.Title == "" || c.Desc == "" || !slices.Contains(cmdCategories, c.Cat) {
			t.Errorf("%s: needs a title, a description and a known category (got %q)", c.ID, c.Cat)
		}
		if (c.Any != "") == (c.Mac != "" || c.Linux != "") {
			t.Errorf("%s: set either Any or the Mac/Linux variants", c.ID)
		}
		for _, cmd := range []string{c.Any, c.Mac, c.Linux} {
			if strings.TrimSpace(cmd) != cmd {
				t.Errorf("%s: %q has surrounding space", c.ID, cmd)
			}
			// every placeholder needs a default, so a copied command is never half-filled
			for name, def := range paramDefs(cmd) {
				if def == "" {
					t.Errorf("%s: {{%s}} has no default", c.ID, name)
				}
			}
			if strings.Contains(reCmdParam.ReplaceAllString(cmd, ""), "{{") && !strings.Contains(cmd, "{{.") {
				t.Errorf("%s: malformed placeholder in %q", c.ID, cmd)
			}
		}
	}
	// the category select and the constants stay in sync
	tool, _ := Registry().Get("commands")
	for _, o := range tool.Options {
		if o.Key == "category" && !slices.Equal(o.Choices[1:], cmdCategories) {
			t.Errorf("category choices %v != %v", o.Choices, cmdCategories)
		}
	}
}

func paramDefs(cmd string) map[string]string {
	d := map[string]string{}
	for _, m := range reCmdParam.FindAllStringSubmatch(cmd, -1) {
		if d[m[1]] == "" {
			d[m[1]] = m[2]
		}
	}
	return d
}

// Every filled-in command must at least parse as shell, on each OS.
func TestCommandsParseAsShell(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	for _, c := range shellCmds {
		for _, cmd := range []string{c.Any, c.Mac, c.Linux} {
			if cmd == "" {
				continue
			}
			filled := fillCmd(cmd, nil)
			// sudo !! is history expansion, which only exists interactively
			filled = strings.ReplaceAll(filled, "sudo !!", "sudo true")
			if out, err := exec.Command(bash, "-n", "-c", filled).CombinedOutput(); err != nil {
				t.Errorf("%s: %q doesn't parse: %s", c.ID, filled, out)
			}
		}
	}
}

func TestCommandSearch(t *testing.T) {
	for _, c := range []struct{ query, first string }{
		{"kill port", "kill-port"},
		{"open ports", "open-ports"},
		{"memory", "memory-usage"},
		{"disk", "disk-free"},
		{"BIG FILES", "big-files"},
		{"eaddrinuse", "kill-port"}, // a tag
		{"undo commit", "git-undo-commit"},
	} {
		items := cmdItems(runCmds(t, map[string]string{"search": c.query, "os": "all"}))
		if len(items) == 0 || items[0].ID != c.first {
			ids := []string{}
			for _, it := range items {
				ids = append(ids, it.ID)
			}
			t.Errorf("search %q: want %s first, got %v", c.query, c.first, ids)
		}
	}
	res := runCmds(t, map[string]string{"search": "no such thing anywhere"})
	if len(cmdItems(res)) != 0 || res.Output != "" || len(res.Warnings) != 1 {
		t.Errorf("a search without hits should warn, got %+v", res)
	}
	tool, _ := Registry().Get("commands")
	if _, err := tool.Execute(core.Request{Opts: map[string]string{"search": strings.Repeat("word ", 1e6)}}); err == nil {
		t.Error("a huge search should be refused")
	}
	all := cmdItems(runCmds(t, map[string]string{"os": "all"}))
	if len(all) != len(shellCmds) {
		t.Errorf("an empty search lists everything: %d of %d", len(all), len(shellCmds))
	}
	docker := cmdItems(runCmds(t, map[string]string{"category": "docker"}))
	for _, it := range docker {
		if it.Category != cmdDocker {
			t.Errorf("category filter let %s through", it.ID)
		}
	}
	if len(docker) == 0 {
		t.Error("no Docker commands")
	}
}

func TestCommandOSVariants(t *testing.T) {
	mac := runCmds(t, map[string]string{"search": "open-ports", "os": "macOS"})
	if mac.Output != "lsof -nP -iTCP -sTCP:LISTEN\n" {
		t.Errorf("macOS: %q", mac.Output)
	}
	linux := runCmds(t, map[string]string{"search": "open-ports", "os": "linux"})
	if linux.Output != "sudo ss -tulpn\n" {
		t.Errorf("Linux: %q", linux.Output)
	}
	both := runCmds(t, map[string]string{"search": "open-ports", "os": "all"})
	if !strings.Contains(both.Output, "# macOS:\nlsof") || !strings.Contains(both.Output, "# Linux:\nsudo ss") {
		t.Errorf("all: %q", both.Output)
	}
	// macOS-only commands disappear on Linux; an exact ID says why
	for _, it := range cmdItems(runCmds(t, map[string]string{"os": "linux"})) {
		if it.ID == "delete-ds-store" {
			t.Error("a macOS-only command was listed for Linux")
		}
	}
	tool, _ := Registry().Get("commands")
	if _, err := tool.Execute(core.Request{Opts: map[string]string{"search": "delete-ds-store", "os": "linux"}}); err == nil || !strings.Contains(err.Error(), "no Linux version") {
		t.Errorf("want a 'no Linux version' error, got %v", err)
	}
}

func TestCommandValues(t *testing.T) {
	res := runCmds(t, map[string]string{"search": "kill-port", "set": "port=8080"})
	if res.Output != "lsof -ti tcp:8080 -sTCP:LISTEN | xargs -r kill -9\n" {
		t.Errorf("got %q", res.Output)
	}
	// defaults fill what isn't set; a name used twice takes the first default
	res = runCmds(t, map[string]string{"search": "replace-text", "os": "linux", "set": `to="new value"`})
	want := `grep -rl --exclude-dir=.git --exclude-dir=node_modules 'old' . | xargs -r sed -i 's/old/new value/g'` + "\n"
	if res.Output != want {
		t.Errorf("got  %q\nwant %q", res.Output, want)
	}
	// Docker's own {{.Names}} templates are not placeholders
	if got := fillCmd(`docker ps --format '{{.Names}}' {{x:1}}`, nil); got != `docker ps --format '{{.Names}}' 1` {
		t.Errorf("got %q", got)
	}

	for in, want := range map[string]map[string]string{
		"":                            {},
		"port=8080":                   {"port": "8080"},
		" port=1  host=a.b ":          {"port": "1", "host": "a.b"},
		`msg="two words" q='it\s'`:    {"msg": "two words", "q": `it\s`},
		`msg="say \"hi\" \\ ok"`:      {"msg": `say "hi" \ ok`},
		"url=https://x.test/?a=b&c=d": {"url": "https://x.test/?a=b&c=d"},
		"empty=":                      {"empty": ""},
	} {
		got, err := parseCmdValues(in)
		if err != nil || !mapsEqual(got, want) {
			t.Errorf("parseCmdValues(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"8080", "Port=1", `msg="open`, "=x"} {
		if _, err := parseCmdValues(bad); err == nil {
			t.Errorf("parseCmdValues(%q) should fail", bad)
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
