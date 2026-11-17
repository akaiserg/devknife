# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

devknife is an offline developer toolbox in the spirit of DevUtils: 46 tools (formatters, converters, encoders, generators, inspectors, and a shell command library) compiled into one Go binary. That binary is three things at once:
- **A native desktop app.** This is the primary deliverable: a compiled app with its own window, not a browser page.
- **A pipe-friendly CLI.**
- **An MCP server.**

Product constraints set by the user:
- **No PHP tools.** Leave out PHP↔JSON, PHP (un)serialize and PHP code-gen targets.
- **It must remain a compiled desktop app.** Shipping only a browser page doesn't count.

## Commands

The toolchain is Go 1.25.5. Always build with `GOTOOLCHAIN=local`; the Makefile exports it. Dependencies are pinned to versions that still support Go 1.25, because newer x/net, x/crypto and minify releases require Go 1.26. Don't `go get -u`.

```sh
make build        # bin/devknife (cgo, includes the native window)
make app          # dist/devknife.app (macOS bundle, icon, ad-hoc signed); ARCHS="arm64 amd64" for universal
make run          # build + open the desktop window
make test         # go test ./...
make vet
make vuln         # govulncheck (needs network; not part of vet)
make cli-all      # CGO_ENABLED=0 cross-compiled CLI-only binaries
```

Other commands:

- **Run one test:** `go test ./internal/tools -run 'TestTools/json_path_filter'`. Spaces in subtest names become `_`.
- **Print tool outputs while testing:** `SHOW=json,sql go test ./internal/tools -run TestSamplesRun -v`. Use `SHOW=all` for every tool.
- **Untruncated output:** a shell hook rewrites commands through `rtk`, which condenses `go test` output. To see full `-v` logs, prefix with `rtk proxy` (`rtk proxy go test -v ...`).
- **cgo warnings:** the vendored webview header emits clang `-Wdeprecated-literal-operator` warnings. The Makefile silences them with `CGO_CXXFLAGS`; set it yourself when calling `go build` directly.
- **Builds without the native window:** `-tags nodesktop` or `CGO_ENABLED=0` uses `internal/desktop/fallback.go`, which opens the UI in a browser. Check that these still compile after touching `internal/desktop`.
- **Browser mode for UI work:** `go run ./cmd/devknife ui --no-open --port 7791`.
  - URL state seeds a tool: `#/<tool>?<opt>=<val>&in=<base64url input>`.
  - Headless Chrome `--screenshot` works for visual checks. Its viewport is never narrower than 500px; smaller `--window-size` values only crop the image.

## Architecture

### One tool model, many frontends

`internal/core/tool.go` defines `Tool`, `Option`, `Request`, `Result`, `Detection`, `Template` and `Registry`.

**Tools.** `internal/tools/registry.go` builds the registry. Every tool is a pure `Run(Request) (*Result, error)`.
- A tool must never touch the network or the filesystem. The web UI and MCP server expose tools to untrusted input.
- File bytes arrive via `Request.Data`.

**Frontends.** The CLI (`internal/cli`), web API (`internal/web`), desktop window (`internal/desktop`) and MCP server (`internal/mcp`) all render from the same registry. A new tool shows up in all of them automatically, along with `--help`, shell completion, the MCP JSON schema and the UI controls.

**`Tool.Execute`** wraps `Run`:
- It fills option defaults and rejects unknown keys.
- It resolves select values case-insensitively, including unambiguous prefixes (`matchChoice`).
- It recovers panics.
- It refuses results larger than `core.MaxOutput`: 256 MB for the CLI. The `ui`, `app` and `mcp` entry points lower it at startup to their own result limit (`web.MaxResult` 64 MB, `mcp.MaxResult` 32 MB), because a builder at the limit holds several times that in memory.
- It returns the error passed to `core.Abort(err)`, which ends a run from deep inside a tool without threading an error through every helper.

Always call `Execute`, not `Run`.

**Hostile input.** Stack overflows and out-of-memory are fatal in Go and can't be recovered, so:
- Recursive parsers and renderers need explicit depth limits: `maxJSONDepth` (1000, `ojson.go`; also used by the YAML converter, which budgets alias expansion too, and by the `time` expression parser) and `maxMarkupDepth` (1000, `markup.go`).
- Anything that writes indentation calls `core.CheckOutput(len)` as it goes (JSON, CSS/LESS/SCSS, JS, SQL, markup; `budgetBuffer` for yaml.v3). Indented output grows with depth × lines, so input that stays under the depth limits can still multiply ~1000×.
- Anything else that multiplies its input (regex replace/template, image decoding) checks sizes before allocating.
- `DEVKNIFE_HOSTILE=1 go test ./internal/tools -run TestEveryToolSurvivesHostileInput` feeds every tool deep nesting, a 10 MB line and amplifying shapes (about two minutes; add `/<tool>` for one tool). Run it after adding a tool or a parser.

**`core.Runner`** bounds tool runs for the web API and MCP: a per-run deadline (`TimeoutError`) and a concurrency limit (`ErrBusy`). Go can't stop a goroutine, so a timed-out run keeps its slot until it finishes; the CLI has no deadline.

**`Registry.Add`** panics on duplicate IDs or aliases. Check alias collisions when adding a tool.

**`Detect`** returns a 0–100 score plus options to preselect, such as `mode=decode`.
- The UI uses it for smart paste and the "Looks like X" banner, and to rank "Send to…" targets.
- The CLI uses it for `devknife` with piped stdin and for `detect`.
- MCP exposes it as the `detect` tool.
- `TestDetection` pins the expected top hits.

**`Result`** fields map to UI rendering:
- `Output` plus `Lang` (syntax highlighting).
- `Fields` (copyable rows; `Color` adds a swatch).
- `Image` (data URL).
- `HTML` (sandboxed preview).
- `View` + `Data` for special renderers: `jwt`, `regex`, `diff`, `color`, `image`, `preview`, `commands`. These match the `VIEW` table in `internal/web/static/app.js`. A new `View` needs a renderer there.

**`Option.Type`** decides the control:
- `select` with ≤5 short choices renders as a segmented control; otherwise a dropdown.
- `text` renders as a secondary editor in the input pane. For example, the diff tool's `other` option is its right-hand side.
- `Short` is the one-letter CLI flag.
- `Hidden` keeps an option out of the UI's option bar (the tool's view sets it itself); it stays a CLI flag and an MCP argument.

**`Tool.Args`** maps CLI positional arguments; see the comment on the field.
- `?key` means an optional select that is only consumed when the argument is a valid choice. That's how `devknife hash sha256 <text>` works.
- For `InputImage` tools (hash, base64-image, qr…), a lone positional argument naming an existing regular file is read as that file, with a note on stderr, so `devknife hash sha256 file` hashes the contents like `sha256sum`.
- `-` as the input argument (or `-f -`) reads stdin explicitly.
- `@key` reads a file path.
- The last `input` swallows the remaining arguments. So does a plain string option when it is the last target (`devknife cmds kill port` → `search="kill port"`).

**Stdin.** Piped stdin is read only when the tool wants input (`wantsStdin`): not for `InputNone` tools, and not for `InputOptional` tools in a generate mode, so `while read x; do devknife uuid; done` doesn't swallow the loop's input. The trailing newline is trimmed only for pipes (`echo x |`), not for `< file`, which matches `-f file`.

**Option names** must not collide with the global flags (`file output paste copy json quiet open help`, `-f -o -p -c -j -h`): the global flag wins and the option becomes unreachable. `TestToolOptionsDontShadowGlobalFlags` enforces it.

**Secrets.** Mark key/password options `Sensitive: true`, and tools whose input is a secret `SensitiveInput: true`. The UI then never writes them to prefs, history, URLs or share links.

**`Templates`** are named presets of input and options (QR payloads, regexes, cron examples). `TestSamplesRun` executes every tool's `Sample` and every template, so a broken preset fails the tests.

### Shared tool internals (`internal/tools`)

- **JSON:** `ojson.go` holds the order-preserving JSON model (`OMap`, `decodeOrdered`, `encodeOrdered`).
  - It preserves key order and big numbers across YAML, CSV and code generation.
  - `relaxJSON` is a lenient JSON5-ish normalizer that keeps a position map, so errors report line/column against the original input.
- **Formatters** are hand-written tokenizers:
  - `markup.go`: HTML, XML, ERB and JSX; case-preserving and SVG-safe.
  - `css.go`: CSS, LESS and SCSS.
  - `js.go`, `sql.go`.
  - Minification uses `tdewolff/minify`, with own fallbacks.
- **Regex offsets** are converted to UTF-16 (`utf16Offsets`) because the UI highlights matches with JS string indexes.
- **Helpers** in `helpers.go`: `det()`, `text()`, `fields()`, `optMode()`, `optIndent()`.
- **Shell commands** (`commands.go`, category Reference): a curated `shellCmds` list. Each entry has `Any` (same everywhere) or `Mac`/`Linux` variants, and `Danger` for anything that kills or deletes.
  - Placeholders are `{{name:default}}` (lowercase names, so Docker's `{{.Names}}` is left alone). Values come from the hidden `set` option in `port=8080 msg="two words"` form. `fillCmd`/`parseCmdValues` in Go and `fillCommand`/`parseValues` in `app.js` must agree.
  - The tool only returns text; nothing is ever executed. `TestCommandsParseAsShell` runs `bash -n` on every filled-in variant, and every placeholder needs a default.
  - The UI's `commands` view fills values client-side, and keeps the user's own commands in prefs under `snippets`.

### Web server and UI (`internal/web`)

**Server.**
- It binds to loopback only.
- It binds to loopback only; `ui --addr` refuses non-loopback addresses (`IsLoopbackAddr`).
- `guard` rejects foreign `Host` (DNS rebinding) and foreign `Origin` headers, and requires `application/json` on POST/PUT/PATCH (CSRF).
- Every `/api/*` request needs the access key in the `X-Devknife-Key` header (401 otherwise), so other local users and apps can't read prefs or history. The key is persistent (`<UserConfigDir>/devknife/access-key`, 0600). The desktop app injects it as `window.__devknife.key`; `devknife ui` prints a link with `?k=KEY`, which `app.js` moves into `localStorage` and strips from the address bar. It's a header, not a cookie, because cookies are shared across all localhost ports.
- Previews don't need the key: their random IDs are the credential, and the sandboxed frame couldn't send a header anyway.
- Runs go through a `core.Runner` (15 s deadline) and results are capped at 64 MB; the CLI hint in the error points to the terminal for bigger jobs.
- Static files are embedded from `static/` (vanilla JS, no CDN) under a strict CSP.

**HTML previews.** They're served from `/preview/{id}` with a CSP `sandbox`, which gives them an opaque origin. The policy is `default-src 'none'` plus data:/blob: images, media and fonts, so a preview can't fetch anything (no tracking pixels). Scripts are off unless the tool's `scripts` option is set; network loads need its `remote` option. The cache is bounded by count and bytes. `devknife <tool> --open` writes the same policy as a `<meta>` tag (`web.PreviewMetaCSP`) into a temp file under `devknife-previews`, swept after 10 minutes.

**UI state.** Favorites, recent tools, history, per-tool input and options, theme, and the user's own shell commands (`snippets`) live in `<UserConfigDir>/devknife/ui-state.json`, through `GET /api/prefs` and `PATCH /api/prefs` (`{"set":{…},"del":[…]}`). They are not in `localStorage`.
- The desktop app gets a random port on every launch, so a per-origin `localStorage` would reset each time.
- In `app.js`, `store.get/set/del` go through the in-memory `PREFS` object and mark keys dirty; only dirty keys are sent, so two windows don't overwrite each other. Failed saves toast once and are retried with the next change; pending changes are flushed on `pagehide` and when the page is hidden. If the initial load failed, nothing is saved.
- The server writes atomically (temp file + fsync + rename). The first write of each session keeps the previous file as `ui-state.json.bak`; a damaged file is moved to `.corrupt` and the `.bak` is loaded.
- Saved inputs are capped (100K characters per tool, about 2M in total) and are kept only while "Remember inputs and history" is on. `snippets` are things the user saved on purpose, so neither that switch nor "Clear all" history touches them.

### Desktop shell (`internal/desktop`)

- `webview.go` (`cgo && !nodesktop`) starts the web server on `127.0.0.1:0` and opens a system WebView using `github.com/webview/webview_go`.
- `menu_darwin.go` installs a Cocoa menu bar through Objective-C. Without an Edit menu, macOS never routes ⌘C/⌘V/⌘X/⌘A/⌘Z to the WebView.
- **Native bridge.** Functions bound into the page (`dkClipRead`, `dkClipWrite`, `dkSave`, `dkReveal`, `dkOpenURL`, `dkSetTitle`) cover what WKWebView can't do: clipboard reads, blob downloads (saves go to `~/Downloads`), and keeping the window title in sync with the open tool.
  - The desktop app also stores `lastTool` in prefs and reopens that tool on launch.
  - Every call requires a per-launch token injected into the top frame only.
  - WKWebView exposes the message handler to all frames, so without the token a sandboxed preview could call these functions.
- In the UI, `NATIVE` / `TOKEN` (`window.__devknife`) switch to these bridges.

**Launch logic** (`internal/cli/cli.go`, the no-args case):
1. Piped stdin always runs auto-detect.
2. Otherwise, `AppMode=1` (an ldflag set for GUI builds) or running inside a `.app` bundle (`desktop.InAppBundle`) opens the window.
3. Otherwise, the CLI prints help.

A Finder launch has stdin on `/dev/null`, which counts as a char device and therefore as a TTY.

### Packaging

- `scripts/package-macos.sh` builds the bundle: Info.plist, an `.icns` from `scripts/mkicon`, and an ad-hoc `codesign`.
- `scripts/mkicon` is a pure-Go icon renderer. Draw each overlapping primitive in its own rasterizer pass; sub-paths with opposite winding cancel out.
- Windows and Linux targets exist in the Makefile but must be built on those platforms (they need cgo and the WebView libraries).

## Gotchas

- The Write/Edit tools can turn a `\ufeff` or `\uFEFF` escape in source into a literal BOM byte, which causes a compile error ("illegal byte order mark"). In code, prefer `rune(0xFEFF)` or `string(rune(0xFEFF))`. After writing, check: `python3 -c "import pathlib;print([str(p) for p in pathlib.Path('.').rglob('*.go') if b'\xef\xbb\xbf' in p.read_bytes()])"`.
- Keep CLI output pipe-clean. Only `Output` goes to stdout when stdout isn't a TTY. The fields table, warnings and status lines go to the terminal or stderr.
