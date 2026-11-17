//go:build cgo && !nodesktop

package desktop

import (
	"errors"
	"os"

	webview "github.com/webview/webview_go"

	"devknife/internal/core"
	"devknife/internal/web"
)

// Available reports whether this build has a native window.
const Available = true

// Run opens the devknife window and blocks until it is closed.
func Run(reg *core.Registry, version string) error {
	addr, key, shutdown, err := web.Start(reg, "127.0.0.1:0", version)
	if err != nil {
		return err
	}
	w := webview.New(os.Getenv("DEVKNIFE_DEBUG") != "")
	if w == nil {
		return errors.New("could not create the app window (is a WebView runtime installed?)")
	}
	defer w.Destroy()
	setupMenu()
	w.SetTitle("devknife")
	w.SetSize(1320, 860, webview.HintNone)
	w.SetSize(760, 480, webview.HintMin)
	b := newBridge()
	b.apiKey = key
	w.Init(b.initScript())
	// The page keeps the window title in sync with the open tool.
	setTitle := func(tok, title string) error {
		if err := b.check(tok); err != nil {
			return err
		}
		title = windowTitle(title)
		w.Dispatch(func() { w.SetTitle(title) })
		return nil
	}
	for name, fn := range map[string]any{
		"dkClipRead":  b.clipRead,
		"dkClipWrite": b.clipWrite,
		"dkSave":      b.save,
		"dkReveal":    b.reveal,
		"dkOpenURL":   b.openURL,
		"dkSetTitle":  setTitle,
	} {
		if err := w.Bind(name, fn); err != nil {
			return err
		}
	}
	w.Navigate(addr)
	w.Run()
	// The window is gone; let a settings save that's still in flight finish.
	shutdown()
	return nil
}
