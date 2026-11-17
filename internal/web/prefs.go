package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// prefs persists UI state (favorites, recent tools, per-tool input/options,
// history, theme) in the user config dir, so it survives restarts even though
// the desktop window uses a fresh localhost port on every launch.
//
// Clients change it key by key (PATCH), so two windows editing different
// keys don't overwrite each other, and a client that failed to load can't
// wipe what it never saw.
type prefs struct {
	mu       sync.Mutex
	path     string // "" = memory only
	data     map[string]json.RawMessage
	backedUp bool // the file as found at startup was kept as .bak
}

const maxPrefs = 16 << 20

// DefaultPrefsPath is <user config dir>/devknife/ui-state.json.
func DefaultPrefsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "devknife", "ui-state.json")
}

func newPrefs(path string) *prefs {
	p := &prefs{path: path, data: map[string]json.RawMessage{}}
	if path == "" {
		return p
	}
	// Fall back to the backup when the main file is missing or unreadable,
	// e.g. after a crash between the two renames in write.
	for i, f := range []string{path, path + ".bak"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(b, &m) == nil && m != nil {
			p.data = m
			if i > 0 {
				fmt.Fprintf(os.Stderr, "devknife: restored UI state from %s\n", f)
			}
			return p
		}
		if i == 0 {
			// Keep the damaged file for inspection instead of overwriting it.
			if os.Rename(f, f+".corrupt") == nil {
				fmt.Fprintf(os.Stderr, "devknife: %s was unreadable; moved it to %s.corrupt\n", f, f)
			}
		}
	}
	return p
}

func (p *prefs) get(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	b, err := json.Marshal(p.data)
	p.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

type prefsPatch struct {
	Set map[string]json.RawMessage `json:"set"`
	Del []string                   `json:"del"`
}

func (p *prefs) patch(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPrefs))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "the change is larger than 16 MB"})
		return
	}
	var pt prefsPatch
	if err := json.Unmarshal(body, &pt); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `expected {"set": {...}, "del": [...]}`})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next := make(map[string]json.RawMessage, len(p.data)+len(pt.Set))
	for k, v := range p.data {
		next[k] = v
	}
	for k, v := range pt.Set {
		next[k] = v
	}
	for _, k := range pt.Del {
		delete(next, k)
	}
	b, err := json.Marshal(next)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(b) > maxPrefs {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "saved state would pass 16 MB; clear the history or large saved inputs"})
		return
	}
	if err := p.write(b); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	p.data = next
	w.WriteHeader(http.StatusNoContent)
}

// write replaces the file atomically: write a temp file, fsync it, rename it
// over the old one. The first write of a session first moves the file as it
// was at startup to .bak, so one bad session can't destroy everything.
func (p *prefs) write(b []byte) error {
	if p.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o700); err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if !p.backedUp {
		if err := os.Rename(p.path, p.path+".bak"); err == nil || errors.Is(err, fs.ErrNotExist) {
			p.backedUp = true
		}
	}
	return os.Rename(tmp, p.path)
}
