package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devknife/internal/core"
	"devknife/internal/tools"
)

const testKey = "test-key"

func do(t *testing.T, h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1:7777"+path, strings.NewReader(body))
	req.Host = "127.0.0.1:7777"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(KeyHeader, testKey)
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPI(t *testing.T) {
	h := NewHandler(tools.Registry(), "test", testKey)

	rec := do(t, h, "GET", "/api/tools", "", nil)
	var meta struct {
		Tools      []map[string]any `json:"tools"`
		Categories []string         `json:"categories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil || len(meta.Tools) < 40 || len(meta.Categories) != len(core.CategoryOrder) {
		t.Fatalf("tools: %v %d %s", err, len(meta.Tools), rec.Body.String()[:min(200, rec.Body.Len())])
	}

	rec = do(t, h, "POST", "/api/run/json", `{"input":"{\"b\":1,\"a\":[1,2]}","opts":{"sort":"true"}}`, nil)
	var res map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["output"] != "{\n  \"a\": [\n    1,\n    2\n  ],\n  \"b\": 1\n}" {
		t.Fatalf("run json: %s", rec.Body.String())
	}

	rec = do(t, h, "POST", "/api/run/json", `{"input":"{\"a\":1,,}","opts":{"lenient":"false"}}`, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["error"] == nil || res["errorLine"] == nil {
		t.Fatalf("expected positioned error: %s", rec.Body.String())
	}

	rec = do(t, h, "POST", "/api/detect", `{"input":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc"}`, nil)
	var hits []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &hits)
	if len(hits) == 0 || hits[0]["id"] != "jwt" {
		t.Fatalf("detect: %s", rec.Body.String())
	}

	// preview pages are sandboxed on an opaque origin and can't reach the network
	rec = do(t, h, "POST", "/api/run/html-preview", `{"input":"<b>hi</b>","opts":{}}`, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	u, _ := res["previewURL"].(string)
	if !strings.HasPrefix(u, "/preview/") {
		t.Fatalf("no preview url: %s", rec.Body.String())
	}
	rec = do(t, h, "GET", u, "", map[string]string{KeyHeader: ""}) // iframes can't send the key
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox", "script-src 'none'", "default-src 'none'", "img-src data: blob:"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("preview CSP %q lacks %q", csp, want)
		}
	}
	if rec.Body.String() != "<b>hi</b>" {
		t.Fatalf("preview body: %q", rec.Body.String())
	}
	// opting in to remote content loosens only the network directives
	rec = do(t, h, "POST", "/api/run/markdown", `{"input":"# hi","opts":{"remote":"true"}}`, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	rec = do(t, h, "GET", res["previewURL"].(string), "", nil)
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src *") || !strings.Contains(csp, "script-src 'none'") {
		t.Fatalf("remote preview CSP: %q", csp)
	}

	// prefs are patched key by key
	if rec = do(t, h, "PATCH", "/api/prefs", `{"set":{"fav":["json"],"theme":"dark"}}`, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("patch prefs: %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, h, "PATCH", "/api/prefs", `{"set":{"recent":["jwt"]},"del":["theme"]}`, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("patch prefs: %d", rec.Code)
	}
	if rec = do(t, h, "GET", "/api/prefs", "", nil); rec.Body.String() != `{"fav":["json"],"recent":["jwt"]}` {
		t.Fatalf("get prefs: %s", rec.Body.String())
	}
	if rec = do(t, h, "PATCH", "/api/prefs", `[1]`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("patch must be an object: %d", rec.Code)
	}

	// static UI
	rec = do(t, h, "GET", "/", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "app.js") || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("index: %d", rec.Code)
	}
}

func TestGuards(t *testing.T) {
	h := NewHandler(tools.Registry(), "test", testKey)
	cases := []struct {
		name   string
		method string
		hdr    map[string]string
		ctype  bool
		want   int
	}{
		{"dns rebinding host", "GET", map[string]string{"Host": "evil.example:7777"}, false, 403},
		{"cross-site post", "POST", map[string]string{"Origin": "https://evil.example"}, true, 403},
		{"form post without json", "POST", map[string]string{"Content-Type": "text/plain"}, false, 415},
		{"same origin post", "POST", map[string]string{"Origin": "http://127.0.0.1:7777"}, true, 200},
		{"localhost host", "GET", map[string]string{"Host": "localhost:7777"}, false, 200},
		{"no access key", "GET", map[string]string{KeyHeader: ""}, false, 401},
		{"wrong access key", "GET", map[string]string{KeyHeader: "guess"}, false, 401},
		{"no key for prefs", "GET", map[string]string{KeyHeader: "", "path": "/api/prefs"}, false, 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, body := "/api/tools", ""
			if p, ok := c.hdr["path"]; ok {
				path = p
				delete(c.hdr, "path")
			}
			if c.method == "POST" {
				path = "/api/detect"
				if c.ctype {
					body = `{"input":"x"}`
				}
			}
			req := httptest.NewRequest(c.method, "http://127.0.0.1:7777"+path, strings.NewReader(body))
			req.Host = "127.0.0.1:7777"
			req.Header.Set(KeyHeader, testKey)
			if c.ctype {
				req.Header.Set("Content-Type", "application/json")
			}
			for k, v := range c.hdr {
				if k == "Host" {
					req.Host = v
				} else {
					req.Header.Set(k, v)
				}
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("got %d want %d: %s", rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

func TestPrefsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ui-state.json")
	_ = os.WriteFile(path, []byte(`{"fav":["json"],"theme":"dark"}`), 0o600)

	s := newServer(tools.Registry(), "test", path, testKey)
	h := s.Handler()
	if rec := do(t, h, "PATCH", "/api/prefs", `{"set":{"theme":"light"}}`, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	b, _ := os.ReadFile(path)
	if string(b) != `{"fav":["json"],"theme":"light"}` {
		t.Fatalf("file after patch: %s", b)
	}
	// the state found at startup is kept as a backup
	if b, _ := os.ReadFile(path + ".bak"); string(b) != `{"fav":["json"],"theme":"dark"}` {
		t.Fatalf("backup: %s", b)
	}
	// a damaged file is set aside, and the backup is used instead of {}
	_ = os.WriteFile(path, []byte(`{"fav":[`), 0o600)
	p := newPrefs(path)
	if string(p.data["theme"]) != `"dark"` {
		t.Fatalf("expected the backup to be loaded, got %v", p.data)
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("damaged file was not kept: %v", err)
	}
	// state over the limit is refused with a message, not silently dropped
	big := `{"set":{"x":"` + strings.Repeat("a", maxPrefs-20) + `"}}`
	if rec := do(t, h, "PATCH", "/api/prefs", big, nil); rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "error") {
		t.Fatalf("oversized prefs: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoopbackOnly(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:7777": true, "localhost:7777": true, "[::1]:7777": true,
		"0.0.0.0:7777": false, "192.168.1.5:7777": false, ":7777": false,
	} {
		if got := IsLoopbackAddr(addr); got != want {
			t.Errorf("IsLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
	if _, err := listen("0.0.0.0:0"); err == nil {
		t.Fatal("listen accepted a non-loopback address")
	}
}

func TestPreviewCacheBounded(t *testing.T) {
	s := newServer(tools.Registry(), "test", "", testKey)
	chunk := strings.Repeat("x", maxPreviewBytes/4)
	for i := 0; i < 10; i++ {
		s.storePreview(preview{html: chunk})
	}
	if s.previewBytes > maxPreviewBytes || len(s.previews) != len(s.order) {
		t.Fatalf("preview cache holds %d bytes in %d entries", s.previewBytes, len(s.previews))
	}
}
