// Package cli implements the devknife command line: every tool is a
// subcommand, input comes from args / files / stdin / clipboard, and output
// is pipe-friendly (only the result goes to stdout when piped).
package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"devknife/internal/clip"
	"devknife/internal/core"
	"devknife/internal/desktop"
	"devknife/internal/mcp"
	"devknife/internal/tools"
	"devknife/internal/web"
)

// Version is set at build time with -ldflags "-X devknife/internal/cli.Version=...".
var Version = "dev"

// AppMode makes a bare launch (no arguments) open the desktop window. The
// GUI builds (macOS .app, Windows devknife-app.exe) set it via
// -ldflags "-X devknife/internal/cli.AppMode=1"; inside a .app bundle it is
// detected automatically.
var AppMode = ""

type env struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// stdinPiped: stdin is a pipe, file or socket, so it may carry input.
	// A terminal or /dev/null (Finder/Explorer launches, cron) does not.
	stdinPiped bool
	// stdinRegular: stdin is redirected from a regular file (< file).
	stdinRegular bool
	stdoutTTY    bool
	color        bool // ANSI colors on stdout
	colorErr     bool // ANSI colors on stderr (decided separately: 2> file must stay clean)
	reg          *core.Registry
	readClip     func() (string, error)
	writeClip    func(string) error
	openBrowser  func(string) error
}

// isTerminal reports whether f is an interactive terminal: a character
// device other than the null device. (/dev/null is a character device too,
// and redirecting to it must not count as a terminal.)
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(st, null) {
		return false
	}
	return true
}

// Main runs the CLI and returns the process exit code.
func Main(args []string) int {
	e := &env{
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		stdoutTTY: isTerminal(os.Stdout),
		reg:       tools.Registry(), readClip: clip.Read, writeClip: clip.Write, openBrowser: clip.Open,
	}
	if st, err := os.Stdin.Stat(); err == nil {
		m := st.Mode()
		e.stdinRegular = m.IsRegular()
		e.stdinPiped = m.IsRegular() || m&(os.ModeNamedPipe|os.ModeSocket) != 0
	}
	colorOK := os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	e.color = e.stdoutTTY && colorOK
	e.colorErr = isTerminal(os.Stderr) && colorOK
	if err := e.run(args); err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintln(e.stderr, e.paintErr("31", "error: ")+err.Error())
			return 2
		}
		fmt.Fprintln(e.stderr, e.paintErr("31", "error: ")+err.Error())
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

// paint colors text for stdout; paintErr for stderr.
func (e *env) paint(code, s string) string { return ansi(e.color, code, s) }

func (e *env) paintErr(code, s string) string { return ansi(e.colorErr, code, s) }

func ansi(on bool, code, s string) string {
	if !on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (e *env) run(args []string) error {
	if len(args) == 0 {
		// piped input always means "detect and run", even from the app binary;
		// a Finder/Explorer launch has stdin on /dev/null, which isn't piped
		if e.stdinPiped {
			return e.auto(nil)
		}
		if AppMode == "1" || desktop.InAppBundle() {
			core.MaxOutput = web.MaxResult
			return desktop.Run(e.reg, Version)
		}
		e.printHelp()
		return nil
	}
	switch args[0] {
	case "app", "desktop", "gui":
		core.MaxOutput = web.MaxResult // see core.MaxOutput
		return desktop.Run(e.reg, Version)
	case "-h", "--help", "help":
		if len(args) > 1 {
			if t, ok := e.reg.Get(args[1]); ok {
				e.toolHelp(t)
				return nil
			}
		}
		e.printHelp()
		return nil
	case "-v", "--version", "version":
		fmt.Fprintf(e.stdout, "devknife %s (%d tools)\n", Version, len(e.reg.All()))
		return nil
	case "list", "ls":
		return e.list(args[1:])
	case "detect":
		return e.detect(args[1:])
	case "auto":
		return e.auto(args[1:])
	case "ui", "serve", "web":
		core.MaxOutput = web.MaxResult
		return e.ui(args[1:])
	case "mcp":
		core.MaxOutput = mcp.MaxResult
		return mcp.Serve(e.reg, e.stdin, e.stdout, Version)
	case "completion":
		return e.completion(args[1:])
	}
	t, ok := e.reg.Get(args[0])
	if !ok {
		msg := fmt.Sprintf("unknown tool or command %q", args[0])
		if hits := e.reg.Search(args[0]); len(hits) > 0 {
			var names []string
			for i, h := range hits {
				if i == 3 {
					break
				}
				names = append(names, h.ID)
			}
			msg += "\n       did you mean: " + strings.Join(names, ", ") + "?"
		}
		msg += "\n       run 'devknife list' to see all tools"
		return usageError{msg}
	}
	return e.runTool(t, args[1:])
}

// ---------------------------------------------------------------------------

type globals struct {
	file   string
	paste  bool
	copy   bool
	output string
	json   bool
	quiet  bool
	open   bool
	help   bool
}

func parseArgs(t *core.Tool, args []string) (map[string]string, []string, globals, error) {
	var g globals
	opts := map[string]string{}
	var pos []string
	shortTool := map[string]core.Option{}
	for _, o := range t.Options {
		if o.Short != "" {
			shortTool[o.Short] = o
		}
	}
	setOpt := func(o core.Option, val string, hasVal bool, next func() (string, bool)) error {
		if o.Type == core.OptBool {
			if !hasVal {
				val = "true"
			}
			opts[o.Key] = val
			return nil
		}
		if !hasVal {
			v, ok := next()
			if !ok {
				return usageError{fmt.Sprintf("--%s needs a value", o.Key)}
			}
			val = v
		}
		if strings.HasPrefix(val, "@") && len(val) > 1 && o.Type == core.OptText {
			b, err := os.ReadFile(val[1:])
			if err != nil {
				return err
			}
			val = string(b)
		}
		opts[o.Key] = val
		return nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") || len(a) == 1 || (len(a) > 1 && a[1] >= '0' && a[1] <= '9') {
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		long := strings.HasPrefix(a, "--")
		switch {
		case name == "f" || name == "file":
			if !hasVal {
				v, ok := next()
				if !ok {
					return nil, nil, g, usageError{"--file needs a path"}
				}
				val = v
			}
			g.file = val
		case name == "o" || name == "output":
			if !hasVal {
				v, ok := next()
				if !ok {
					return nil, nil, g, usageError{"--output needs a path"}
				}
				val = v
			}
			g.output = val
		case name == "p" || name == "paste":
			g.paste = true
		case name == "c" || name == "copy":
			g.copy = true
		case name == "j" || name == "json":
			g.json = true
		case name == "quiet":
			g.quiet = true
		case name == "open":
			g.open = true
		case name == "h" || name == "help":
			g.help = true
		default:
			var o core.Option
			found := false
			if long {
				key := strings.ReplaceAll(name, "-", "_")
				if opt, ok := t.Option(key); ok {
					o, found = opt, true
				} else if strings.HasPrefix(key, "no_") {
					if opt, ok := t.Option(strings.TrimPrefix(key, "no_")); ok && opt.Type == core.OptBool {
						opts[opt.Key] = "false"
						continue
					}
				}
			} else if opt, ok := shortTool[name]; ok {
				o, found = opt, true
			} else if len(name) > 1 && !hasVal {
				// -sk style short flag with attached value: -n5
				if opt, ok := shortTool[name[:1]]; ok && opt.Type != core.OptBool {
					o, found, val, hasVal = opt, true, name[1:], true
				}
			}
			if !found {
				var keys []string
				for _, op := range t.Options {
					keys = append(keys, "--"+strings.ReplaceAll(op.Key, "_", "-"))
				}
				hint := ""
				if len(keys) > 0 {
					hint = " (options: " + strings.Join(keys, " ") + ")"
				}
				return nil, nil, g, usageError{fmt.Sprintf("unknown flag %s for %s%s", a, t.ID, hint)}
			}
			if err := setOpt(o, val, hasVal, next); err != nil {
				return nil, nil, g, err
			}
		}
	}
	return opts, pos, g, nil
}

// applyPositional maps positional args to targets (see core.Tool.Args).
// For tools that accept files (core.InputImage, e.g. hash), a single input
// argument naming an existing regular file is read as that file, the way
// sha256sum works; fromFile then names it so the caller can say so.
func applyPositional(t *core.Tool, pos []string, opts map[string]string) (input string, inputSet bool, fileData []byte, fromFile string, err error) {
	targets := t.Args
	if targets == nil {
		targets = []string{"?mode", "input"}
	}
	for ti, target := range targets {
		if len(pos) == 0 {
			break
		}
		optional := strings.HasPrefix(target, "?")
		file := strings.HasPrefix(target, "@")
		key := strings.TrimLeft(target, "?@")
		if key == "input" {
			if file {
				b, err := readPath(pos[0])
				if err != nil {
					return "", false, nil, "", err
				}
				input, inputSet, fileData = string(b), true, b
				pos = pos[1:]
				continue
			}
			if t.Input == core.InputImage && len(pos) == 1 && pos[0] != "-" {
				if st, serr := os.Stat(pos[0]); serr == nil && st.Mode().IsRegular() {
					b, err := os.ReadFile(pos[0])
					if err != nil {
						return "", false, nil, "", err
					}
					input, inputSet, fileData, fromFile = string(b), true, b, pos[0]
					pos = nil
					break
				}
			}
			if ti == len(targets)-1 {
				input, inputSet = strings.Join(pos, " "), true
				pos = nil
				break
			}
			input, inputSet = pos[0], true
			pos = pos[1:]
			continue
		}
		o, ok := t.Option(key)
		if !ok {
			continue
		}
		if optional {
			if _, already := opts[key]; already {
				continue
			}
			if o.Type != core.OptSelect {
				continue
			}
			v := matchPositionalChoice(o.Choices, pos[0])
			if v == "" {
				continue
			}
			opts[key] = v
			pos = pos[1:]
			continue
		}
		val := pos[0]
		if file {
			b, err := readPath(val)
			if err != nil {
				return "", false, nil, "", err
			}
			val = string(b)
		} else if ti == len(targets)-1 && o.Type == core.OptString {
			// a trailing text option takes the rest, like input does:
			// devknife cmds kill port → search "kill port"
			opts[key] = strings.Join(pos, " ")
			pos = nil
			break
		}
		opts[key] = val
		pos = pos[1:]
	}
	if len(pos) > 0 {
		return "", false, nil, "", usageError{fmt.Sprintf("unexpected argument %q", pos[0])}
	}
	return input, inputSet, fileData, fromFile, nil
}

// matchPositionalChoice accepts exact choices, or unambiguous prefixes of at
// least 3 letters ("dec" → decode, "snake" → snake_case).
func matchPositionalChoice(choices []string, v string) string {
	lv := strings.ToLower(v)
	for _, c := range choices {
		if strings.ToLower(c) == lv {
			return c
		}
	}
	if len(lv) < 3 {
		return ""
	}
	norm := func(s string) string {
		return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "", ".", "", "/", "").Replace(s))
	}
	var hit string
	for _, c := range choices {
		if strings.HasPrefix(norm(c), norm(lv)) {
			if hit != "" {
				return ""
			}
			hit = c
		}
	}
	return hit
}

func readPath(p string) ([]byte, error) {
	if p == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(p)
}

// readPathOrStdin reads a file, or the env's stdin for "-" (raw bytes, never trimmed).
func (e *env) readPathOrStdin(p string) ([]byte, error) {
	if p == "-" {
		return io.ReadAll(e.stdin)
	}
	return os.ReadFile(p)
}

// trimEcho drops the newline that `echo x |` adds to a single line. Input
// redirected from a file (< file) is left as is, so it matches -f file.
func (e *env) trimEcho(s string) string {
	if !e.stdinRegular && strings.Count(s, "\n") == 1 && strings.HasSuffix(s, "\n") {
		return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
	}
	return s
}

// wantsStdin reports whether a tool reads piped stdin when no input was
// given. Generators don't: inside `while read …; do devknife uuid; done`,
// stdin belongs to the loop, and reading it would swallow the loop's lines
// (or hang on a pipe nobody writes to). Pass "-" to feed them stdin anyway.
func wantsStdin(t *core.Tool, opts map[string]string) bool {
	switch t.Input {
	case core.InputNone:
		return false
	case core.InputOptional:
		mode := opts["mode"]
		if o, ok := t.Option("mode"); ok {
			if mode == "" {
				mode = o.Default
			}
			mode = core.MatchChoice(o.Choices, mode)
		}
		return mode != "generate"
	}
	return true
}

func (e *env) runTool(t *core.Tool, args []string) error {
	opts, pos, g, err := parseArgs(t, args)
	if err != nil {
		return err
	}
	if g.help {
		e.toolHelp(t)
		return nil
	}
	input, inputSet, data, fromFile, err := applyPositional(t, pos, opts)
	if err != nil {
		return err
	}
	if fromFile != "" {
		fmt.Fprintln(e.stderr, e.paintErr("2", fmt.Sprintf("(using the contents of the file %s; to use the text itself, pipe it in)", fromFile)))
	}
	switch {
	case g.file != "":
		b, err := e.readPathOrStdin(g.file)
		if err != nil {
			return err
		}
		input, inputSet, data = string(b), true, b
	case g.paste:
		s, err := e.readClip()
		if err != nil {
			return err
		}
		input, inputSet = s, true
	case inputSet && input == "-" && fromFile == "":
		// an explicit "-" argument means stdin, as in most Unix tools
		b, err := io.ReadAll(e.stdin)
		if err != nil {
			return err
		}
		input, data = e.trimEcho(string(b)), b
	case !inputSet && e.stdinPiped && wantsStdin(t, opts):
		b, err := io.ReadAll(e.stdin)
		if err != nil {
			return err
		}
		input, data, inputSet = e.trimEcho(string(b)), b, true
	}
	if !inputSet && t.Input == core.InputText {
		return usageError{fmt.Sprintf("no input for %s — pass it as an argument, pipe it via stdin, or use -f FILE / -p (clipboard)\n       try: devknife %s --help", t.ID, t.ID)}
	}
	req := core.Request{Input: input, Opts: opts}
	if data != nil && (t.Input == core.InputImage || !utf8.Valid(data)) {
		req.Data = data
	}
	res, err := t.Execute(req)
	if err != nil {
		return err
	}
	return e.emit(t, res, g)
}

func (e *env) emit(t *core.Tool, res *core.Result, g globals) error {
	if g.output != "" {
		if err := writeOutput(g.output, res); err != nil {
			return err
		}
		fmt.Fprintf(e.stderr, "%s wrote %s\n", e.paintErr("32", "✓"), g.output)
	}
	if g.copy {
		if err := e.writeClip(res.Output); err != nil {
			return err
		}
		fmt.Fprintln(e.stderr, e.paintErr("32", "✓")+" copied to clipboard")
	}
	if g.open && res.HTML != "" {
		path, err := writePreviewFile(res)
		if err != nil {
			return err
		}
		if err := e.openBrowser(path); err != nil {
			return err
		}
		fmt.Fprintln(e.stderr, e.paintErr("32", "✓")+" opened "+path)
		return nil
	}
	if g.json {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(struct {
			Tool string `json:"tool"`
			*core.Result
		}{t.ID, res})
	}
	if g.output != "" && !e.stdoutTTY {
		return nil
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(e.stderr, e.paintErr("33", "! ")+w)
	}
	out := res.Output
	if out != "" {
		if e.color {
			out = colorize(out, res.Lang)
		}
		fmt.Fprint(e.stdout, out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Fprintln(e.stdout)
		}
	}
	// the info table goes to the terminal only, so pipes stay clean
	if len(res.Fields) > 0 && !g.quiet && (e.stdoutTTY || res.Output == "") {
		if res.Output != "" {
			fmt.Fprintln(e.stdout, e.paint("2", strings.Repeat("─", 40)))
		}
		width := 0
		for _, f := range res.Fields {
			width = max(width, utf8.RuneCountInString(f.Label))
		}
		for _, f := range res.Fields {
			if f.Value == "" {
				continue
			}
			pad := strings.Repeat(" ", width-utf8.RuneCountInString(f.Label))
			fmt.Fprintf(e.stdout, "%s%s  %s\n", e.paint("2", f.Label), pad, f.Value)
		}
	}
	return nil
}

func writeOutput(path string, res *core.Result) error {
	ext := strings.ToLower(filepath.Ext(path))
	if res.Image != "" && ext != ".txt" && ext != ".css" && ext != ".html" {
		if i := strings.Index(res.Image, ";base64,"); i >= 0 {
			b, err := base64.StdEncoding.DecodeString(res.Image[i+8:])
			if err != nil {
				return err
			}
			return os.WriteFile(path, b, 0o644)
		}
	}
	if res.HTML != "" && ext == ".html" {
		return os.WriteFile(path, []byte(res.HTML), 0o644)
	}
	out := res.Output
	if !strings.HasSuffix(out, "\n") && out != "" {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out), 0o644)
}

// writePreviewFile writes HTML for --open with the policy the UI's preview
// frame gets: no network, and no scripts unless the tool allowed them. A
// browser opening a file:// page applies no sandbox of its own. The files
// share one temp folder, swept of old previews on each use.
func writePreviewFile(res *core.Result) (string, error) {
	scripts, remote := false, false
	if m, ok := res.Data.(map[string]any); ok {
		scripts, _ = m["scripts"].(bool)
		remote, _ = m["remote"].(bool)
	}
	dir := filepath.Join(os.TempDir(), "devknife-previews")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, en := range ents {
			if info, err := en.Info(); err == nil && time.Since(info.ModTime()) > 10*time.Minute {
				_ = os.Remove(filepath.Join(dir, en.Name()))
			}
		}
	}
	f, err := os.CreateTemp(dir, "preview-*.html")
	if err != nil {
		return "", err
	}
	defer f.Close()
	meta := `<meta http-equiv="Content-Security-Policy" content="` + web.PreviewMetaCSP(scripts, remote) + `">`
	html := res.HTML
	// After a doctype (anything before it would switch on quirks mode), else first.
	if t := strings.TrimLeft(html, " \t\r\n"); len(t) >= 9 && strings.EqualFold(t[:9], "<!doctype") {
		if i := strings.IndexByte(html, '>'); i >= 0 {
			html = html[:i+1] + meta + html[i+1:]
		}
	} else {
		html = meta + html
	}
	if _, err := f.WriteString(html); err != nil {
		return "", err
	}
	return f.Name(), f.Close()
}

// ---------------------------------------------------------------------------

func (e *env) readAnyInput(args []string) (string, error) {
	if len(args) > 0 {
		if len(args) == 2 && (args[0] == "-f" || args[0] == "--file") {
			b, err := readPath(args[1])
			return string(b), err
		}
		if len(args) == 1 && (args[0] == "-p" || args[0] == "--paste") {
			return e.readClip()
		}
		return strings.Join(args, " "), nil
	}
	if e.stdinPiped {
		b, err := io.ReadAll(e.stdin)
		return strings.TrimSuffix(string(b), "\n"), err
	}
	s, err := e.readClip()
	if err != nil {
		return "", usageError{"no input: pass text, pipe stdin, or copy something to the clipboard"}
	}
	fmt.Fprintln(e.stderr, e.paintErr("2", "(reading from clipboard)"))
	return s, nil
}

func (e *env) detect(args []string) error {
	in, err := e.readAnyInput(args)
	if err != nil {
		return err
	}
	hits := e.reg.Detect(in)
	if len(hits) == 0 {
		fmt.Fprintln(e.stdout, "No specific tool detected. Try: devknife inspect")
		return nil
	}
	for i, h := range hits {
		if i == 8 {
			break
		}
		bar := strings.Repeat("█", h.Score/10) + strings.Repeat("░", 10-h.Score/10)
		mode := ""
		if len(h.Opts) > 0 {
			keys := make([]string, 0, len(h.Opts))
			for k := range h.Opts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				mode += " " + h.Opts[k]
			}
		}
		fmt.Fprintf(e.stdout, "%s %3d%%  %-14s %s\n", e.paint("36", bar), h.Score, h.ID+mode, e.paint("2", h.Note))
	}
	return nil
}

func (e *env) auto(args []string) error {
	in, err := e.readAnyInput(args)
	if err != nil {
		return err
	}
	hits := e.reg.Detect(in)
	if len(hits) == 0 || hits[0].Score < 30 {
		return usageError{"could not detect what this is; run 'devknife detect' or pick a tool"}
	}
	h := hits[0]
	fmt.Fprintf(e.stderr, "%s %s (%s)\n", e.paintErr("36", "→"), h.Name, h.Note)
	opts := map[string]string{}
	for k, v := range h.Opts {
		opts[k] = v
	}
	res, err := h.Tool.Execute(core.Request{Input: in, Opts: opts})
	if err != nil {
		return err
	}
	return e.emit(h.Tool, res, globals{})
}

func (e *env) list(args []string) error {
	asJSON := len(args) > 0 && (args[0] == "--json" || args[0] == "-j")
	all := e.reg.All()
	if asJSON {
		enc := json.NewEncoder(e.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(all)
	}
	if len(args) > 0 {
		all = e.reg.Search(strings.Join(args, " "))
	}
	cat := ""
	for _, t := range all {
		if t.Category != cat {
			cat = t.Category
			fmt.Fprintf(e.stdout, "\n%s\n", e.paint("1", cat))
		}
		al := ""
		if len(t.Aliases) > 0 {
			al = e.paint("2", " ("+strings.Join(t.Aliases, ", ")+")")
		}
		extra := ""
		if t.Extra {
			extra = e.paint("35", " +")
		}
		fmt.Fprintf(e.stdout, "  %-14s %s%s%s\n", e.paint("36", t.ID), t.Name, extra, al)
	}
	fmt.Fprintf(e.stdout, "\n%s\n", e.paint("2", fmt.Sprintf("%d tools · + = beyond DevUtils · devknife <tool> --help for details", len(e.reg.All()))))
	return nil
}

func (e *env) ui(args []string) error {
	addr := "127.0.0.1:7777"
	open := true
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--no-open":
			open = false
		case a == "--port" || a == "-P":
			if i+1 < len(args) {
				i++
				addr = "127.0.0.1:" + args[i]
			}
		case strings.HasPrefix(a, "--port="):
			addr = "127.0.0.1:" + strings.TrimPrefix(a, "--port=")
		case a == "--addr" && i+1 < len(args):
			i++
			addr = args[i]
		default:
			return usageError{"usage: devknife ui [--port 7777] [--no-open]"}
		}
	}
	// The UI exposes saved inputs and history; it is for this machine only.
	if !web.IsLoopbackAddr(addr) {
		return usageError{fmt.Sprintf("refusing to listen on %s: the UI serves your saved inputs and history, so it only listens on loopback (127.0.0.1, ::1 or localhost)", addr)}
	}
	return web.ListenAndServe(e.reg, addr, Version, func(url string) {
		fmt.Fprintf(e.stderr, "%s devknife UI running at %s  (Ctrl+C to stop)\n", e.paintErr("32", "●"), e.paintErr("1", url))
		if open {
			_ = e.openBrowser(url)
		}
	})
}

// ---------------------------------------------------------------------------
// help

func (e *env) printHelp() {
	b := func(s string) string { return e.paint("1", s) }
	c := func(s string) string { return e.paint("36", s) }
	fmt.Fprintf(e.stdout, `%s — %d offline developer tools in one binary

%s
  devknife <tool> [mode] [flags] [input...]
  <command> | devknife <tool> [flags]
  <command> | devknife              %s

%s
  %s       open the desktop app window
  %s        open the same UI in your browser instead
  %s      list tools (%s to search)
  %s    show which tools match the input (args, stdin or clipboard)
  %s      detect and run the best tool
  %s       run as an MCP server so AI agents can use every tool
  %s   shell completion: bash | zsh | fish

%s
  -f, --file PATH   read input from a file ('-' = stdin)
  -p, --paste       read input from the clipboard
  -c, --copy        copy the result to the clipboard
  -o, --output PATH write the result (images are written as binary)
  -j, --json        print the full result as JSON
      --quiet       print only the result, no info table
      --open        open HTML/Markdown previews in the browser
  -h, --help        help for a tool

%s
  echo '{"a":1}' | devknife json
  devknife b64 decode SGVsbG8=
  devknife time now + 2d
  devknife jwt -p                       # decode the JWT in your clipboard
  devknife uuid v7 -n 5
  devknife hash sha256 -f release.tar.gz
  devknife case snake "requestURLDecoderID"
  devknife diff old.txt new.txt
  pbpaste | devknife curl python
  devknife qr "https://example.com"     # renders right in the terminal
`, b("devknife"), len(e.reg.All()), b("Usage"), e.paint("2", "# smart-detects what you piped in"),
		b("Commands"), c("app"), c("ui"), c("list"), c("list <query>"), c("detect"), c("auto"), c("mcp"), c("completion"),
		b("Global flags"), b("Examples"))
}

func (e *env) toolHelp(t *core.Tool) {
	b := func(s string) string { return e.paint("1", s) }
	fmt.Fprintf(e.stdout, "%s  %s\n%s\n\n", b(t.Name), e.paint("2", "("+strings.Join(append([]string{t.ID}, t.Aliases...), ", ")+")"), t.Description)
	targets := t.Args
	if targets == nil {
		targets = []string{"?mode", "input"}
	}
	var usage []string
	for _, tg := range targets {
		key := strings.TrimLeft(tg, "?@")
		if key == "input" {
			if t.Input == core.InputNone {
				continue
			}
			if strings.HasPrefix(tg, "@") {
				usage = append(usage, "[file]")
			} else {
				usage = append(usage, "[input...]")
			}
			continue
		}
		o, ok := t.Option(key)
		if !ok {
			continue
		}
		label := key
		if o.Type == core.OptSelect && len(o.Choices) <= 6 {
			label = strings.Join(o.Choices, "|")
		}
		if strings.HasPrefix(tg, "@") {
			label += "-file"
		}
		usage = append(usage, "["+label+"]")
	}
	fmt.Fprintf(e.stdout, "%s\n  devknife %s %s [flags]\n\n", b("Usage"), t.ID, strings.Join(usage, " "))
	if len(t.Options) > 0 {
		fmt.Fprintln(e.stdout, b("Options"))
		for _, o := range t.Options {
			flag := "    --" + strings.ReplaceAll(o.Key, "_", "-")
			if o.Short != "" {
				flag = "-" + o.Short + ", --" + strings.ReplaceAll(o.Key, "_", "-")
			}
			val := ""
			switch o.Type {
			case core.OptSelect:
				val = " " + strings.Join(o.Choices, "|")
			case core.OptBool:
			case core.OptText:
				val = " TEXT|@file"
			default:
				val = " " + strings.ToUpper(o.Key)
			}
			desc := o.Help
			if desc == "" {
				desc = o.Label
			}
			if o.Default != "" && o.Default != "false" {
				desc += e.paint("2", " (default "+o.Default+")")
			}
			fmt.Fprintf(e.stdout, "  %s%s\n      %s\n", e.paint("36", flag), val, desc)
		}
		fmt.Fprintln(e.stdout)
	}
	if len(t.Templates) > 0 {
		fmt.Fprintln(e.stdout, b("Presets (web UI)"))
		for _, tp := range t.Templates {
			fmt.Fprintf(e.stdout, "  • %s\n", tp.Name)
		}
		fmt.Fprintln(e.stdout)
	}
	if t.Sample != "" && t.Input != core.InputNone && !strings.Contains(t.Sample, "\n") && len(t.Sample) < 120 {
		fmt.Fprintf(e.stdout, "%s\n  devknife %s %s\n", b("Example"), t.ID, shellQuote(t.Sample))
	}
}

func shellQuote(s string) string {
	if reShellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ---------------------------------------------------------------------------
// colors

var (
	reJSONTok = regexp.MustCompile(`("(?:[^"\\]|\\.)*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)`)
)

func colorize(s, lang string) string {
	if len(s) > 1<<20 {
		return s
	}
	switch lang {
	case "json":
		return reJSONTok.ReplaceAllStringFunc(s, func(m string) string {
			sub := reJSONTok.FindStringSubmatch(m)
			switch {
			case sub[2] != "":
				return "\x1b[36m" + sub[1] + "\x1b[0m" + sub[2]
			case sub[1] != "":
				return "\x1b[32m" + m + "\x1b[0m"
			case sub[3] != "":
				return "\x1b[35m" + m + "\x1b[0m"
			default:
				return "\x1b[33m" + m + "\x1b[0m"
			}
		})
	case "diff":
		lines := strings.Split(s, "\n")
		for i, l := range lines {
			switch {
			case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
				lines[i] = "\x1b[1m" + l + "\x1b[0m"
			case strings.HasPrefix(l, "+"):
				lines[i] = "\x1b[32m" + l + "\x1b[0m"
			case strings.HasPrefix(l, "-"):
				lines[i] = "\x1b[31m" + l + "\x1b[0m"
			case strings.HasPrefix(l, "@@"):
				lines[i] = "\x1b[36m" + l + "\x1b[0m"
			}
		}
		return strings.Join(lines, "\n")
	}
	return s
}

// ---------------------------------------------------------------------------
// completion

func (e *env) completion(args []string) error {
	if len(args) != 1 {
		return usageError{"usage: devknife completion bash|zsh|fish"}
	}
	var names []string
	for _, t := range e.reg.All() {
		names = append(names, t.ID)
		names = append(names, t.Aliases...)
	}
	names = append(names, "app", "ui", "list", "detect", "auto", "mcp", "completion", "version", "help")
	words := strings.Join(names, " ")
	switch args[0] {
	case "bash":
		fmt.Fprintf(e.stdout, "_devknife() { local cur=${COMP_WORDS[COMP_CWORD]}; if [ $COMP_CWORD -eq 1 ]; then COMPREPLY=($(compgen -W %s -- \"$cur\")); else COMPREPLY=($(compgen -f -- \"$cur\")); fi; }\ncomplete -F _devknife devknife\n", strconv.Quote(words))
	case "zsh":
		fmt.Fprintf(e.stdout, "#compdef devknife\n_devknife() { if (( CURRENT == 2 )); then compadd -- %s; else _files; fi }\ncompdef _devknife devknife\n", words)
	case "fish":
		for _, t := range e.reg.All() {
			fmt.Fprintf(e.stdout, "complete -c devknife -n __fish_use_subcommand -a %s -d %s\n", t.ID, strconv.Quote(t.Name))
		}
		for _, c := range []string{"app", "ui", "list", "detect", "auto", "mcp", "completion"} {
			fmt.Fprintf(e.stdout, "complete -c devknife -n __fish_use_subcommand -a %s\n", c)
		}
	default:
		return usageError{"supported shells: bash, zsh, fish"}
	}
	return nil
}

var (
	reShellSafe = regexp.MustCompile(`^[\w@%+=:,./-]+$`)
)
