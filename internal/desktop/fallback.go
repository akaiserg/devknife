//go:build !cgo || nodesktop

package desktop

import (
	"fmt"
	"os"

	"devknife/internal/clip"
	"devknife/internal/core"
	"devknife/internal/web"
)

// Available reports whether this build has a native window.
const Available = false

// Run falls back to the browser UI when built without the native window
// (CGO_ENABLED=0 or -tags nodesktop).
func Run(reg *core.Registry, version string) error {
	fmt.Fprintln(os.Stderr, "this build has no native window; opening the UI in your browser")
	return web.ListenAndServe(reg, "127.0.0.1:7777", version, func(u string) {
		fmt.Fprintf(os.Stderr, "devknife UI running at %s  (Ctrl+C to stop)\n", u)
		_ = clip.Open(u)
	})
}
