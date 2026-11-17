package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"devknife/internal/tools"
)

func TestServe(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"base64","arguments":{"input":"hi"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"uuid","arguments":{"type":"v5","namespace":"dns","name":"example.com"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"json","arguments":{"input":"{","lenient":false}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"qr","arguments":{"input":"x"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"bogus"}`,
		`not json`,
	}, "\n")
	var out bytes.Buffer
	if err := Serve(tools.Registry(), strings.NewReader(in), &out, "test"); err != nil {
		t.Fatal(err)
	}
	resps := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad line %q", line)
		}
		id, _ := json.Marshal(m["id"])
		resps[string(id)] = m
	}
	if len(resps) != 8 { // 7 requests + parse error (notification gets no reply)
		t.Fatalf("got %d responses:\n%s", len(resps), out.String())
	}
	result := func(id string) map[string]any { r, _ := resps[id]["result"].(map[string]any); return r }
	text := func(id string) string {
		c := result(id)["content"].([]any)[0].(map[string]any)
		return c["text"].(string)
	}
	if result("1")["serverInfo"].(map[string]any)["name"] != "devknife" {
		t.Fatal("initialize")
	}
	list := result("2")["tools"].([]any)
	if len(list) < 40 {
		t.Fatalf("tools/list: %d", len(list))
	}
	for _, x := range list {
		tt := x.(map[string]any)
		schema := tt["inputSchema"].(map[string]any)
		if schema["type"] != "object" {
			t.Fatalf("%v: bad schema", tt["name"])
		}
	}
	if !strings.HasPrefix(text("3"), "aGk=") {
		t.Fatalf("base64: %q", text("3"))
	}
	if !strings.HasPrefix(text("4"), "cfbff0d1-9375-5685-968c-48ce8b15ae17") {
		t.Fatalf("uuid: %q", text("4"))
	}
	if result("5")["isError"] != true {
		t.Fatal("json error should be a tool error")
	}
	qr := result("6")["content"].([]any)
	if len(qr) < 2 || qr[1].(map[string]any)["type"] != "image" {
		t.Fatal("qr should return an image part")
	}
	if resps["7"]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatal("unknown method")
	}
	if resps["null"]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatal("parse error")
	}
}

// serveAsync runs Serve on pipes and returns a writer for requests and a
// channel of decoded responses.
func serveAsync(t *testing.T) (*io.PipeWriter, <-chan map[string]any, <-chan error) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(tools.Registry(), inR, outW, "test"); outW.Close() }()
	resps := make(chan map[string]any, 16)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				resps <- m
			}
		}
		close(resps)
	}()
	return inW, resps, done
}

func next(t *testing.T, resps <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m, ok := <-resps:
		if !ok {
			t.Fatal("server closed")
		}
		return m
	case <-time.After(10 * time.Second):
		t.Fatal("no response within 10s")
	}
	return nil
}

// A slow call no longer holds up ping, and a cancelled call gets no reply.
func TestConcurrentCallsAndCancel(t *testing.T) {
	in, resps, done := serveAsync(t)
	slow := `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"bcrypt","arguments":{"input":"pw","cost":14}}}`
	fmt.Fprintf(in, slow+"\n", 1)
	fmt.Fprintln(in, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if m := next(t, resps); m["id"] != float64(2) {
		t.Fatalf("ping should answer first, got id %v", m["id"])
	}
	if m := next(t, resps); m["id"] != float64(1) {
		t.Fatalf("then the bcrypt call, got id %v", m["id"])
	}

	fmt.Fprintf(in, slow+"\n", 3)
	fmt.Fprintln(in, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3,"reason":"user"}}`)
	fmt.Fprintln(in, `{"jsonrpc":"2.0","id":4,"method":"ping"}`)
	if m := next(t, resps); m["id"] != float64(4) {
		t.Fatalf("expected ping, got %v", m)
	}
	in.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for m := range resps {
		if m["id"] == float64(3) {
			t.Fatal("a cancelled request must not be answered")
		}
	}
}

func TestOversizedLineAndVersion(t *testing.T) {
	old := maxLine
	maxLine = 1024
	t.Cleanup(func() { maxLine = old })
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"base64","arguments":{"input":"` + strings.Repeat("a", 5000) + `"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
	}, "\n")
	var out bytes.Buffer
	if err := Serve(tools.Registry(), strings.NewReader(in), &out, "test"); err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		_ = json.Unmarshal([]byte(line), &m)
		id, _ := json.Marshal(m["id"])
		got[string(id)] = m
	}
	if e, _ := got["null"]["error"].(map[string]any); e == nil || e["code"] != float64(-32600) {
		t.Fatalf("oversized line: %v", got["null"])
	}
	if got["2"]["result"] == nil {
		t.Fatal("the session must survive an oversized line")
	}
	version := func(id string) any { return got[id]["result"].(map[string]any)["protocolVersion"] }
	if v := version("3"); v != protocolVersion {
		t.Fatalf("unknown client version answered with %v", v)
	}
	if v := version("4"); v != "2025-03-26" {
		t.Fatalf("supported client version answered with %v", v)
	}
}
