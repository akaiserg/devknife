// Package core defines the tool model shared by every frontend (CLI, web UI,
// MCP server). A tool is a pure function from (input, options) to a Result;
// tools never touch the network or the filesystem, which keeps every frontend
// safe to expose and trivially testable.
package core

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Categories mirror the DevUtils grouping so users coming from it feel at home.
const (
	CatFormat   = "Format & Minify"
	CatConvert  = "Convert"
	CatInspect  = "Inspect & Debug"
	CatGenerate = "Generate"
	CatEncode   = "Encode & Decode"
	// CatReference holds look-up tools such as the shell command library.
	CatReference = "Reference"
)

// CategoryOrder is the display order for categories.
var CategoryOrder = []string{CatFormat, CatConvert, CatInspect, CatGenerate, CatEncode, CatReference}

// OptionType controls how an option is rendered and parsed.
type OptionType string

const (
	OptBool   OptionType = "bool"
	OptSelect OptionType = "select"
	OptString OptionType = "string"
	OptNumber OptionType = "number"
	OptText   OptionType = "text" // multi-line secondary input (e.g. diff right side)
)

// Option is a tool parameter. Key doubles as the CLI flag name (--key).
type Option struct {
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	Type        OptionType `json:"type"`
	Default     string     `json:"default"`
	Choices     []string   `json:"choices,omitempty"`
	Help        string     `json:"help,omitempty"`
	Placeholder string     `json:"placeholder,omitempty"`
	Short       string     `json:"short,omitempty"` // optional one-letter CLI flag
	// Sensitive marks secrets (signing keys, HMAC keys). The UI never saves
	// them to disk, in history or in shareable links.
	Sensitive bool `json:"sensitive,omitempty"`
	// Hidden options are left out of the UI's option bar because the tool's
	// view sets them with its own controls. They stay CLI flags and MCP
	// arguments.
	Hidden bool `json:"hidden,omitempty"`
}

// InputKind describes what a tool consumes.
type InputKind string

const (
	InputText     InputKind = "text"     // main text input required
	InputNone     InputKind = "none"     // generator: input ignored
	InputOptional InputKind = "optional" // generator that can use input (e.g. hash of text, decode a UUID)
	InputImage    InputKind = "image"    // accepts binary (image) data or base64 text
)

// Request is what a tool receives.
type Request struct {
	Input string
	Data  []byte // optional binary payload (e.g. an uploaded image)
	Opts  map[string]string
}

// Str returns an option value.
func (r Request) Str(k string) string { return r.Opts[k] }

// Bool returns an option as a bool.
func (r Request) Bool(k string) bool {
	switch strings.ToLower(strings.TrimSpace(r.Opts[k])) {
	case "1", "true", "yes", "on", "y":
		return true
	}
	return false
}

// Int returns an option as an int, falling back to def when unset or invalid.
func (r Request) Int(k string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(r.Opts[k]))
	if err != nil {
		return def
	}
	return v
}

// Field is a labelled value displayed in an info table (with copy buttons in
// the UI, aligned columns in the CLI).
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Color string `json:"color,omitempty"` // CSS color swatch to render next to the value
}

// Result is what a tool returns.
type Result struct {
	Output   string   `json:"output"`
	Lang     string   `json:"lang,omitempty"` // syntax highlighting hint
	Fields   []Field  `json:"fields,omitempty"`
	HTML     string   `json:"html,omitempty"`  // rendered preview, shown in a sandboxed iframe
	Image    string   `json:"image,omitempty"` // data: URL
	View     string   `json:"view,omitempty"`  // special renderer: diff, regex, color
	Data     any      `json:"data,omitempty"`  // payload for the special renderer
	Warnings []string `json:"warnings,omitempty"`
}

// Detection is a smart-detection hit: how confident a tool is that it is the
// right one for an input, and which options it would pre-select.
type Detection struct {
	Score int               `json:"score"` // 0-100
	Opts  map[string]string `json:"opts,omitempty"`
	Note  string            `json:"note,omitempty"`
}

// Tool is a single utility.
type Tool struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Aliases     []string  `json:"aliases,omitempty"`
	Keywords    []string  `json:"keywords,omitempty"`
	Options     []Option  `json:"options,omitempty"`
	Input       InputKind `json:"input"`
	Placeholder string    `json:"placeholder,omitempty"`
	Sample      string    `json:"sample,omitempty"`
	Extra       bool      `json:"extra,omitempty"` // not part of the DevUtils feature set
	// SensitiveInput marks tools whose input is a secret (passwords): the UI
	// never saves it to disk or history.
	SensitiveInput bool `json:"sensitiveInput,omitempty"`
	// Templates are named presets (input and/or options) offered in a picker,
	// e.g. QR vCard/WiFi payloads, cron examples, common regexes.
	Templates []Template `json:"templates,omitempty"`
	// Args maps CLI positional arguments to targets: "input" or an option key.
	// A leading "@" means the argument is a file path to read; a leading "?"
	// marks an optional select option that is only consumed when the argument
	// is a valid choice (e.g. "devknife hash sha256 file"). The last "input"
	// target swallows all remaining arguments. Nil means ["?mode", "input"].
	Args []string `json:"-"`

	Detect func(in string) *Detection     `json:"-"`
	Run    func(Request) (*Result, error) `json:"-"`
}

// Template is a named preset.
type Template struct {
	Name  string            `json:"name"`
	Input string            `json:"input,omitempty"`
	Opts  map[string]string `json:"opts,omitempty"`
}

// Option returns the option with the given key.
func (t *Tool) Option(key string) (Option, bool) {
	for _, o := range t.Options {
		if o.Key == key {
			return o, true
		}
	}
	return Option{}, false
}

// MaxOutput caps the text one run may return (Output plus HTML). Tools whose
// output can outgrow their input many times over (regex replace, templates)
// check it while building, so they fail before allocating, not after.
//
// The CLI keeps the default. Long-lived processes (UI, desktop app, MCP)
// lower it at startup, before any run, to the most they return anyway: a
// builder at the limit can hold several times that in memory while growing.
var MaxOutput = 256 << 20

// ErrTooLarge is wrapped by every result-size error.
var ErrTooLarge = errors.New("the result is too large")

// OutputTooLarge is the error for a result over a size limit.
func OutputTooLarge(n, limit int) error {
	return fmt.Errorf("%w (%s; the limit is %s)", ErrTooLarge, fmtMB(n), fmtMB(limit))
}

// Abort ends the current run with err from deep inside a tool, where
// returning an error would mean threading it through every helper. Execute
// returns err; it must only be called during Execute.
func Abort(err error) { panic(abort{err}) }

type abort struct{ err error }

// CheckOutput stops a run whose output has grown past MaxOutput. Formatters
// call it as they write indentation: indented output grows with nesting depth
// times line count, so a few hundred KB of nested input could otherwise build
// gigabytes before the final size check in Execute.
func CheckOutput(n int) {
	if n > MaxOutput {
		Abort(fmt.Errorf("%w (over the %s limit)", ErrTooLarge, fmtMB(MaxOutput)))
	}
}

func fmtMB(n int) string {
	if n < 1<<20 {
		return fmt.Sprintf("%d KB", (n+1023)/1024)
	}
	return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
}

// Execute validates and defaults options, then runs the tool. Panics inside a
// tool are converted to errors so one bad input can never take a server down.
// (Stack overflows and out-of-memory are fatal in Go and can't be recovered,
// so tools must bound recursion depth and allocation themselves.)
func (t *Tool) Execute(req Request) (res *Result, err error) {
	opts := make(map[string]string, len(t.Options))
	for _, o := range t.Options {
		opts[o.Key] = o.Default
	}
	for k, v := range req.Opts {
		o, ok := t.Option(k)
		if !ok {
			return nil, fmt.Errorf("unknown option %q for %s (valid: %s)", k, t.ID, strings.Join(t.optionKeys(), ", "))
		}
		if o.Type == OptSelect && v != "" {
			v = matchChoice(o.Choices, v)
			if v == "" {
				return nil, fmt.Errorf("invalid value %q for --%s (choose one of: %s)", req.Opts[k], k, strings.Join(o.Choices, ", "))
			}
		}
		opts[k] = v
	}
	req.Opts = opts
	defer func() {
		if r := recover(); r != nil {
			if a, ok := r.(abort); ok {
				res, err = nil, a.err
				return
			}
			res, err = nil, fmt.Errorf("internal error in %s: %v", t.ID, r)
		}
	}()
	res, err = t.Run(req)
	if err == nil && res == nil {
		res = &Result{}
	}
	if err == nil {
		if n := len(res.Output) + len(res.HTML); n > MaxOutput {
			return nil, OutputTooLarge(n, MaxOutput)
		}
	}
	return res, err
}

func (t *Tool) optionKeys() []string {
	keys := make([]string, 0, len(t.Options))
	for _, o := range t.Options {
		keys = append(keys, o.Key)
	}
	return keys
}

// matchChoice resolves a select value case-insensitively, also accepting an
// unambiguous prefix ("d" -> "decode").
func matchChoice(choices []string, v string) string {
	lv := strings.ToLower(v)
	for _, c := range choices {
		if strings.ToLower(c) == lv {
			return c
		}
	}
	var hit string
	for _, c := range choices {
		if strings.HasPrefix(strings.ToLower(c), lv) {
			if hit != "" {
				return ""
			}
			hit = c
		}
	}
	return hit
}

// MatchChoice is the exported form of matchChoice for frontends.
func MatchChoice(choices []string, v string) string { return matchChoice(choices, v) }

// Registry holds all tools.
type Registry struct {
	tools []*Tool
	byKey map[string]*Tool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{byKey: map[string]*Tool{}} }

// Add registers tools; duplicate IDs or aliases panic at startup.
func (r *Registry) Add(ts ...*Tool) {
	for _, t := range ts {
		if t.Input == "" {
			t.Input = InputText
		}
		for _, k := range append([]string{t.ID}, t.Aliases...) {
			if prev, dup := r.byKey[k]; dup {
				panic(fmt.Sprintf("duplicate tool key %q (%s vs %s)", k, prev.ID, t.ID))
			}
			r.byKey[k] = t
		}
		r.tools = append(r.tools, t)
	}
}

// Get finds a tool by ID or alias.
func (r *Registry) Get(key string) (*Tool, bool) {
	t, ok := r.byKey[strings.ToLower(key)]
	return t, ok
}

// All returns tools in category display order, preserving registration order
// within a category.
func (r *Registry) All() []*Tool {
	rank := map[string]int{}
	for i, c := range CategoryOrder {
		rank[c] = i
	}
	out := append([]*Tool(nil), r.tools...)
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Category] < rank[out[j].Category] })
	return out
}

// Suggestion is a ranked detection result.
type Suggestion struct {
	Tool *Tool  `json:"-"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Detection
}

// Detect runs every tool's detector against the input and returns hits sorted
// by confidence (highest first).
func (r *Registry) Detect(in string) []Suggestion {
	if strings.TrimSpace(in) == "" {
		return nil
	}
	var out []Suggestion
	for _, t := range r.tools {
		if t.Detect == nil {
			continue
		}
		d := safeDetect(t, in)
		if d == nil || d.Score <= 0 {
			continue
		}
		out = append(out, Suggestion{Tool: t, ID: t.ID, Name: t.Name, Detection: *d})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func safeDetect(t *Tool, in string) (d *Detection) {
	defer func() {
		if recover() != nil {
			d = nil
		}
	}()
	return t.Detect(in)
}

// Search returns tools matching a fuzzy query, best first.
func (r *Registry) Search(q string) []*Tool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return r.All()
	}
	type hit struct {
		t *Tool
		s int
	}
	var hits []hit
	for _, t := range r.All() {
		best := 0
		for _, cand := range append([]string{t.Name, t.ID, t.Description}, append(t.Aliases, t.Keywords...)...) {
			if s := FuzzyScore(q, strings.ToLower(cand)); s > best {
				best = s
			}
		}
		if best > 0 {
			hits = append(hits, hit{t, best})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].s > hits[j].s })
	out := make([]*Tool, len(hits))
	for i, h := range hits {
		out[i] = h.t
	}
	return out
}

// FuzzyScore scores how well query matches s as a subsequence. Exact and
// prefix matches score highest; 0 means no match.
func FuzzyScore(q, s string) int {
	switch {
	case q == s:
		return 1000
	case strings.HasPrefix(s, q):
		return 800 - len(s)
	case strings.Contains(s, q):
		return 600 - len(s)
	}
	score, si, run := 0, 0, 0
	for _, qc := range q {
		found := false
		for si < len(s) {
			c := rune(s[si])
			si++
			if c == qc {
				run++
				score += 10 + run*5
				if si == 1 || s[si-2] == ' ' || s[si-2] == '-' || s[si-2] == '/' {
					score += 15 // word-boundary bonus
				}
				found = true
				break
			}
			run = 0
		}
		if !found {
			return 0
		}
	}
	return score
}
