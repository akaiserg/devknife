package tools

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"devknife/internal/core"
)

// Common option builders keep option names consistent across tools, which
// matters for the CLI (--indent means the same thing everywhere).

func optMode(def string, choices ...string) core.Option {
	return core.Option{Key: "mode", Label: "Mode", Type: core.OptSelect, Default: def, Choices: choices, Short: "m"}
}

func optIndent() core.Option {
	return core.Option{Key: "indent", Label: "Indent", Type: core.OptSelect, Default: "2", Choices: []string{"2", "4", "tab"}, Short: "i"}
}

// optRemote is the opt-in that lets an HTML preview load images, styles and
// fonts from the network. Off by default: previewing a document must not
// reveal to its author that it was opened (tracking pixels).
// budgetBuffer is a bytes.Buffer for encoders that indent (yaml.v3): writes
// stop the run once the output passes core.MaxOutput.
type budgetBuffer struct{ bytes.Buffer }

func (w *budgetBuffer) Write(p []byte) (int, error) {
	core.CheckOutput(w.Len() + len(p))
	return w.Buffer.Write(p)
}

func optRemote() core.Option {
	return core.Option{Key: "remote", Label: "Load remote content", Type: core.OptBool, Default: "false", Help: "Let the preview fetch images, styles and fonts from the internet"}
}

func indentStr(r core.Request) string {
	switch r.Str("indent") {
	case "tab":
		return "\t"
	case "4":
		return "    "
	case "0", "none":
		return ""
	}
	return "  "
}

func det(score int, note string, kv ...string) *core.Detection {
	d := &core.Detection{Score: score, Note: note}
	if len(kv) > 0 {
		d.Opts = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			d.Opts[kv[i]] = kv[i+1]
		}
	}
	return d
}

func text(out, lang string) *core.Result { return &core.Result{Output: out, Lang: lang} }

func fields(kv ...string) []core.Field {
	var fs []core.Field
	for i := 0; i+1 < len(kv); i += 2 {
		fs = append(fs, core.Field{Label: kv[i], Value: kv[i+1]})
	}
	return fs
}

// printableRatio returns the share of runes that are printable/whitespace.
func printableRatio(s string) float64 {
	if s == "" {
		return 0
	}
	if !utf8.ValidString(s) {
		return 0
	}
	total, ok := 0, 0
	for _, r := range s {
		total++
		if unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t' {
			ok++
		}
	}
	return float64(ok) / float64(total)
}

func isSingleLine(s string) bool { return !strings.ContainsAny(strings.TrimSpace(s), "\n\r") }

var reWS = regexp.MustCompile(`\s+`)

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.2f MB", float64(n)/1024/1024)
	}
}

func sizeDelta(before, after int) string {
	if before == 0 {
		return humanBytes(after)
	}
	pct := 100 * float64(after-before) / float64(before)
	return fmt.Sprintf("%s → %s (%+.1f%%)", humanBytes(before), humanBytes(after), pct)
}

// stripTags removes anything that looks like markup; used by detectors.
func looksLikeMarkup(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">")
}
