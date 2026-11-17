// Package clip reads and writes the system clipboard and opens files/URLs
// using each platform's own tools (no cgo).
package clip

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Clipboard access via the platform's own tools; no cgo, no dependencies.

func clipboardCmds(write bool) [][]string {
	switch runtime.GOOS {
	case "darwin":
		if write {
			return [][]string{{"pbcopy"}}
		}
		return [][]string{{"pbpaste"}}
	case "windows":
		if write {
			return [][]string{{"clip.exe"}}
		}
		return [][]string{{"powershell.exe", "-NoProfile", "-Command", "Get-Clipboard -Raw"}}
	default:
		if write {
			return [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
		}
		return [][]string{{"wl-paste", "--no-newline"}, {"xclip", "-selection", "clipboard", "-o"}, {"xsel", "--clipboard", "--output"}}
	}
}

// Read returns the clipboard text.
func Read() (string, error) {
	for _, c := range clipboardCmds(false) {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		out, err := exec.Command(c[0], c[1:]...).Output()
		if err != nil {
			return "", fmt.Errorf("reading clipboard: %v", err)
		}
		s := string(out)
		if runtime.GOOS == "windows" {
			s = strings.TrimSuffix(s, "\r\n")
		}
		return s, nil
	}
	return "", fmt.Errorf("no clipboard tool found (install wl-clipboard, xclip or xsel)")
}

// Write replaces the clipboard text.
func Write(s string) error {
	for _, c := range clipboardCmds(true) {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = bytes.NewReader([]byte(s))
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("no clipboard tool found (install wl-clipboard, xclip or xsel)")
}

// Open opens a URL or file with the OS default handler.
func Open(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

// SaveToDownloads writes data to the user's Downloads folder (falling back to
// the home directory) under a unique name and returns the full path.
func SaveToDownloads(name string, data []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Downloads")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir = home
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "devknife-output.txt"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for i := 2; ; i++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return "", err
		}
		return path, f.Close()
	}
}

// Reveal shows a file in the platform file manager.
func Reveal(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	case "windows":
		return exec.Command("explorer", "/select,", path).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}
