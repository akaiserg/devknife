// Package desktop runs devknife as a native desktop application: the
// embedded UI inside the platform WebView (WebKit on macOS, WebView2 on
// Windows, WebKitGTK on Linux) with a real window, menu bar and Dock icon.
package desktop

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"devknife/internal/clip"
)

// InAppBundle reports whether the running executable lives inside a macOS
// .app bundle (i.e. it was launched from Finder, Launchpad or the Dock).
func InAppBundle() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

// bridge implements the native helpers exposed to the UI. Every call carries
// a per-launch token only the top-level page knows, so content rendered in a
// sandboxed preview frame cannot use them.
type bridge struct {
	token  string // guards the native functions below
	apiKey string // the web server's access key, so the page needn't keep it in the URL
}

func newBridge() *bridge {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &bridge{token: hex.EncodeToString(b)}
}

var errDenied = errors.New("denied")

func (b *bridge) check(tok string) error {
	if tok != b.token {
		return errDenied
	}
	return nil
}

func (b *bridge) clipRead(tok string) (string, error) {
	if err := b.check(tok); err != nil {
		return "", err
	}
	return clip.Read()
}

func (b *bridge) clipWrite(tok, s string) error {
	if err := b.check(tok); err != nil {
		return err
	}
	return clip.Write(s)
}

func (b *bridge) save(tok, name, b64 string) (string, error) {
	if err := b.check(tok); err != nil {
		return "", err
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	return clip.SaveToDownloads(name, data)
}

func (b *bridge) reveal(tok, path string) error {
	if err := b.check(tok); err != nil {
		return err
	}
	return clip.Reveal(path)
}

func (b *bridge) openURL(tok, raw string) error {
	if err := b.check(tok); err != nil {
		return err
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("only http(s) links can be opened")
	}
	return clip.Open(u.String())
}

// windowTitle cleans a title requested by the page: one line, at most 120
// characters, never empty.
func windowTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	if s == "" {
		return "devknife"
	}
	return s
}

// initScript exposes the tokens and a flag to the top-level page only.
func (b *bridge) initScript() string {
	return `window.__devknife={native:true,token:"` + b.token + `",key:"` + b.apiKey + `",os:"` + runtime.GOOS + `"};`
}
