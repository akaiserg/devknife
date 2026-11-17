// Package web serves the devknife UI: a single-page app embedded in the
// binary that talks to a small JSON API. It binds to loopback only, rejects
// foreign Host/Origin headers (DNS-rebinding and CSRF protection), and
// requires an access key on every API call, so other local users and
// sandboxed apps can't read history or saved inputs.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"devknife/internal/core"
)

//go:embed static
var staticFS embed.FS

const (
	maxBody = 48 << 20
	// MaxResult caps what the UI receives. Larger results belong in the CLI
	// (-o FILE), and the WebView could not render them usefully anyway.
	MaxResult = 64 << 20
	// runTimeout bounds one tool run; bcrypt at cost 16 takes a few seconds.
	runTimeout      = 15 * time.Second
	maxPreviews     = 24
	maxPreviewBytes = 96 << 20
	// KeyHeader carries the access key on API requests.
	KeyHeader = "X-Devknife-Key"
)

type server struct {
	reg     *core.Registry
	version string
	key     string
	runner  *core.Runner

	mu           sync.Mutex
	previews     map[string]preview
	order        []string
	previewBytes int
	prefs        *prefs
}

func newServer(reg *core.Registry, version, prefsPath, key string) *server {
	return &server{
		reg: reg, version: version, key: key,
		runner:   core.NewRunner(runTimeout, 0),
		previews: map[string]preview{},
		prefs:    newPrefs(prefsPath),
	}
}

type preview struct {
	html    string
	scripts bool
	remote  bool
}

// listen binds addr on a loopback address. If the port is taken it falls
// back to a free one, so a second instance still works. (Any error triggers
// the fallback: Windows reports a busy port differently from Unix.)
func listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		host, port, serr := net.SplitHostPort(addr)
		if serr != nil || port == "0" {
			return nil, err
		}
		ln2, err2 := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err2 != nil {
			return nil, err
		}
		ln = ln2
	}
	if a, ok := ln.Addr().(*net.TCPAddr); !ok || !a.IP.IsLoopback() {
		ln.Close()
		return nil, fmt.Errorf("devknife only serves on a loopback address (127.0.0.1 or ::1), not %s", ln.Addr())
	}
	return ln, nil
}

// IsLoopbackAddr reports whether host:port names a loopback interface.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func urlWithKey(host, port, key string) string {
	return "http://" + host + ":" + port + "/?k=" + key
}

// ListenAndServe starts the UI server and blocks until SIGINT/SIGTERM.
// onReady is called with the URL (including the access key) once the
// listener is bound.
func ListenAndServe(reg *core.Registry, addr, version string, onReady func(url string)) error {
	ln, err := listen(addr)
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	path := DefaultPrefsPath()
	s := newServer(reg, version, path, loadKey(path))
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if onReady != nil {
		onReady(urlWithKey("localhost", port, s.key))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		// let in-flight saves (PATCH /api/prefs) finish before exiting
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Start binds addr (use port 0 for a free port) and serves in the background.
// It returns the URL to open (it carries the access key) and the key itself.
//
// shutdown stops the server after letting in-flight requests finish (up to
// 2 s), so a settings save sent just before the window closed still lands.
func Start(reg *core.Registry, addr, version string) (url, key string, shutdown func(), err error) {
	ln, err := listen(addr)
	if err != nil {
		return "", "", nil, err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	path := DefaultPrefsPath()
	s := newServer(reg, version, path, loadKey(path))
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	shutdown = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return urlWithKey("127.0.0.1", port, s.key), s.key, shutdown, nil
}

// NewHandler returns the HTTP handler with in-memory preferences (tests).
// Requests must send key in the X-Devknife-Key header.
func NewHandler(reg *core.Registry, version, key string) http.Handler {
	return newServer(reg, version, "", key).Handler()
}

// loadKey returns the access key stored next to the preferences file,
// creating it on first use. A stable key keeps browser-mode tabs and
// bookmarks working across restarts; the file is private to the user, like
// the preferences it protects. Without a config dir the key lives only as
// long as the process.
func loadKey(prefsPath string) string {
	if prefsPath != "" {
		p := filepath.Join(filepath.Dir(prefsPath), "access-key")
		if b, err := os.ReadFile(p); err == nil {
			if k := strings.TrimSpace(string(b)); len(k) == 64 {
				if _, err := hex.DecodeString(k); err == nil {
					return k
				}
			}
		}
		k := randomHex(32)
		if os.MkdirAll(filepath.Dir(p), 0o700) == nil {
			_ = os.WriteFile(p, []byte(k+"\n"), 0o600) // unwritable: the key just won't outlive the process
		}
		return k
	}
	return randomHex(32)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux.HandleFunc("GET /api/tools", s.tools)
	mux.HandleFunc("POST /api/run/{id}", s.run)
	mux.HandleFunc("POST /api/detect", s.detect)
	mux.HandleFunc("GET /api/prefs", s.prefs.get)
	mux.HandleFunc("PATCH /api/prefs", s.prefs.patch)
	mux.HandleFunc("GET /preview/{id}", s.preview)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; frame-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
	return s.guard(mux)
}

// guard enforces loopback Host, same-origin writes and the access key.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "[::1]" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && r.Method != http.MethodGet {
			ok := false
			for _, h := range []string{"localhost", "127.0.0.1", "[::1]"} {
				if o == "http://"+h+":"+portOf(r.Host) || o == "http://"+h {
					ok = true
				}
			}
			if !ok {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		api := strings.HasPrefix(r.URL.Path, "/api/")
		if api && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch) && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		// The Host and Origin checks stop web pages; the key stops other
		// local processes. Previews need no key: their IDs are random and
		// unguessable, and an <iframe> can't send headers.
		if api && subtle.ConstantTimeCompare([]byte(r.Header.Get(KeyHeader)), []byte(s.key)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or wrong access key: open devknife from the link that `devknife ui` prints, or from the desktop app"})
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func portOf(hostport string) string {
	_, p, err := net.SplitHostPort(hostport)
	if err != nil {
		return ""
	}
	return p
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *server) tools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    s.version,
		"categories": core.CategoryOrder,
		"tools":      s.reg.All(),
	})
}

type runReq struct {
	Input string            `json:"input"`
	Opts  map[string]string `json:"opts"`
	Data  string            `json:"data,omitempty"` // base64 file bytes
}

type runResp struct {
	*core.Result
	Error      string  `json:"error,omitempty"`
	ErrorLine  int     `json:"errorLine,omitempty"`
	ErrorCol   int     `json:"errorCol,omitempty"`
	PreviewURL string  `json:"previewURL,omitempty"`
	Millis     float64 `json:"ms"`
}

func (s *server) run(w http.ResponseWriter, r *http.Request) {
	t, ok := s.reg.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, runResp{Error: "unknown tool"})
		return
	}
	var req runReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, runResp{Error: "bad request: " + err.Error()})
		return
	}
	creq := core.Request{Input: req.Input, Opts: req.Opts}
	if req.Data != "" {
		b, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, runResp{Error: "bad file data"})
			return
		}
		creq.Data = b
	}
	start := time.Now()
	// The UI aborts superseded runs, which cancels r.Context(): a run still
	// waiting for a slot is dropped instead of computed for nobody.
	res, err := s.runner.Run(r.Context(), t, creq)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if errors.Is(err, context.Canceled) {
		return // the client is gone
	}
	if err == nil {
		if n := len(res.Output) + len(res.HTML); n > MaxResult {
			err = core.OutputTooLarge(n, MaxResult)
		}
	}
	if errors.Is(err, core.ErrTooLarge) {
		err = fmt.Errorf("%w. Run it from the terminal instead: devknife %s -f INPUT -o OUTPUT", err, t.ID)
	}
	if err != nil {
		resp := runResp{Error: err.Error(), Millis: ms}
		resp.ErrorLine, resp.ErrorCol = errorPos(err.Error())
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp := runResp{Result: res, Millis: ms}
	if res.HTML != "" {
		var p preview
		p.html = res.HTML
		if m, ok := res.Data.(map[string]any); ok {
			p.scripts, _ = m["scripts"].(bool)
			p.remote, _ = m["remote"].(bool)
		}
		resp.PreviewURL = "/preview/" + s.storePreview(p)
	}
	writeJSON(w, http.StatusOK, resp)
}

// errorPos extracts "line N, column M" from tool errors for editor markers.
func errorPos(msg string) (int, int) {
	var line, col int
	if i := strings.Index(msg, "line "); i >= 0 {
		fmt.Sscanf(msg[i:], "line %d, column %d", &line, &col)
		if col == 0 {
			fmt.Sscanf(msg[i:], "line %d col %d", &line, &col)
		}
	}
	return line, col
}

// storePreview keeps the latest previews, bounded by count and total size.
func (s *server) storePreview(p preview) string {
	id := randomHex(12)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previews[id] = p
	s.order = append(s.order, id)
	s.previewBytes += len(p.html)
	for len(s.order) > 1 && (len(s.order) > maxPreviews || s.previewBytes > maxPreviewBytes) {
		s.previewBytes -= len(s.previews[s.order[0]].html)
		delete(s.previews, s.order[0])
		s.order = s.order[1:]
	}
	return id
}

// PreviewCSP is the policy for rendered HTML. Without remote, nothing in the
// preview reaches the network (a tracking pixel in a README stays dark).
// Scripts, when allowed, may only be inline.
func PreviewCSP(scripts, remote bool) string {
	sandbox := "sandbox allow-popups allow-modals"
	if scripts {
		sandbox += " allow-scripts allow-forms"
	}
	return sandbox + "; " + PreviewMetaCSP(scripts, remote)
}

// PreviewMetaCSP is PreviewCSP without the sandbox directive, which a
// <meta http-equiv> policy can't carry (used for CLI --open files).
func PreviewMetaCSP(scripts, remote bool) string {
	var csp string
	if remote {
		csp = "default-src * data: blob:; style-src * data: 'unsafe-inline'"
	} else {
		csp = "default-src 'none'; img-src data: blob:; media-src data: blob:; font-src data:; style-src data: 'unsafe-inline'"
	}
	switch {
	case !scripts:
		csp += "; script-src 'none'"
	case remote:
		csp += "; script-src * data: blob: 'unsafe-inline' 'unsafe-eval'"
	default:
		csp += "; script-src 'unsafe-inline' 'unsafe-eval'"
	}
	return csp + "; form-action 'none'; base-uri 'none'"
}

// preview serves rendered HTML on an opaque, sandboxed origin: it cannot
// read the app's storage or call the API, even with scripts enabled.
func (s *server) preview(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	p, ok := s.previews[r.PathValue("id")]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "preview expired", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Security-Policy", PreviewCSP(p.scripts, p.remote))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(p.html))
}

type detectReq struct {
	Input string `json:"input"`
}

func (s *server) detect(w http.ResponseWriter, r *http.Request) {
	var req detectReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	type hit struct {
		ID    string            `json:"id"`
		Name  string            `json:"name"`
		Score int               `json:"score"`
		Note  string            `json:"note"`
		Opts  map[string]string `json:"opts,omitempty"`
	}
	var sugg []core.Suggestion
	if err := s.runner.Do(r.Context(), func() { sugg = s.reg.Detect(req.Input) }); err != nil {
		if !errors.Is(err, context.Canceled) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		}
		return
	}
	out := []hit{}
	for i, h := range sugg {
		if i == 6 {
			break
		}
		out = append(out, hit{h.ID, h.Name, h.Score, h.Note, h.Opts})
	}
	writeJSON(w, http.StatusOK, out)
}
