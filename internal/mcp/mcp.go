// Package mcp exposes every devknife tool over the Model Context Protocol
// (JSON-RPC 2.0 over stdio, newline-delimited), so AI agents can format,
// convert, decode and generate without shelling out.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"devknife/internal/core"
)

// protocolVersion is the newest MCP revision this server implements; it is
// also the answer when a client asks for a version we don't know.
const protocolVersion = "2025-06-18"

// supportedVersions are the revisions whose requests this server handles.
var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// maxLine bounds one JSON-RPC message; longer lines are skipped and
// answered with an error instead of ending the session. (A var for tests.)
var maxLine = 64 << 20

const (
	// callTimeout bounds one tools/call, so a slow call can't hold up the agent.
	callTimeout = 30 * time.Second
	// MaxResult caps the text of one result; agents can't use more anyway.
	MaxResult = 32 << 20
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

// ToolName maps a devknife id to an MCP tool name ([a-zA-Z0-9_-]).
func ToolName(id string) string { return id }

// InputSchema builds a JSON schema describing a tool's input and options.
func InputSchema(t *core.Tool) map[string]any {
	props := map[string]any{}
	var required []string
	if t.Input != core.InputNone {
		desc := "The text to process."
		if t.Placeholder != "" {
			desc += " " + t.Placeholder
		}
		if t.Input == core.InputImage {
			desc = "Text input. For image tools you may instead pass `data_base64` with the file bytes."
			props["data_base64"] = map[string]any{"type": "string", "description": "Base64-encoded file bytes (images, binary files)."}
		}
		props["input"] = map[string]any{"type": "string", "description": desc}
		if t.Input == core.InputText {
			required = append(required, "input")
		}
	}
	for _, o := range t.Options {
		p := map[string]any{"description": o.Label}
		if o.Help != "" {
			p["description"] = o.Label + ". " + o.Help
		}
		switch o.Type {
		case core.OptBool:
			p["type"] = "boolean"
			p["default"] = o.Default == "true"
		case core.OptNumber:
			p["type"] = "number"
			if f, err := strconv.ParseFloat(o.Default, 64); err == nil {
				p["default"] = f
			}
		case core.OptSelect:
			p["type"] = "string"
			p["enum"] = o.Choices
			p["default"] = o.Default
		default:
			p["type"] = "string"
			if o.Default != "" {
				p["default"] = o.Default
			}
		}
		props[o.Key] = p
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// Serve runs the MCP server until the input stream closes.
//
// tools/call requests run concurrently (bounded by a core.Runner), so a slow
// call never delays ping or other calls; notifications/cancelled drops a call
// that hasn't answered yet. Everything else is answered in order.
func Serve(reg *core.Registry, in io.Reader, out io.Writer, version string) error {
	var mu sync.Mutex
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	send := func(r rpcResp) {
		mu.Lock()
		defer mu.Unlock()
		r.JSONRPC = "2.0"
		_ = enc.Encode(r)
	}
	runner := core.NewRunner(callTimeout, 0)
	var wg sync.WaitGroup
	var cmu sync.Mutex
	inflight := map[string]context.CancelFunc{} // request id → cancel
	defer wg.Wait()

	br := bufio.NewReaderSize(in, 1<<20)
	for {
		raw, tooLong, rerr := readLine(br, maxLine)
		if tooLong {
			send(rpcResp{ID: json.RawMessage("null"), Error: &rpcErr{-32600, fmt.Sprintf("request too large (over %d MB); it was skipped", maxLine>>20)}})
		} else if line := bytes.TrimSpace(raw); len(line) > 0 {
			var req rpcReq
			if err := json.Unmarshal(line, &req); err != nil {
				send(rpcResp{ID: json.RawMessage("null"), Error: &rpcErr{-32700, "parse error: " + err.Error()}})
			} else if len(req.ID) == 0 { // notification
				if req.Method == "notifications/cancelled" {
					var p struct {
						RequestID json.RawMessage `json:"requestId"`
					}
					if json.Unmarshal(req.Params, &p) == nil {
						cmu.Lock()
						if cancel, ok := inflight[string(p.RequestID)]; ok {
							cancel()
						}
						cmu.Unlock()
					}
				}
			} else if req.Method == "tools/call" {
				ctx, cancel := context.WithCancel(context.Background())
				id := string(req.ID)
				cmu.Lock()
				inflight[id] = cancel
				cmu.Unlock()
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() {
						cmu.Lock()
						delete(inflight, id)
						cmu.Unlock()
						cancel()
					}()
					res, rerr := handleCall(ctx, reg, runner, req)
					if ctx.Err() != nil {
						return // cancelled: the spec says not to answer
					}
					if rerr != nil {
						send(rpcResp{ID: req.ID, Error: rerr})
					} else {
						send(rpcResp{ID: req.ID, Result: res})
					}
				}()
			} else {
				res, herr := handle(reg, req, version)
				if herr != nil {
					send(rpcResp{ID: req.ID, Error: herr})
				} else {
					send(rpcResp{ID: req.ID, Result: res})
				}
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			return rerr
		}
	}
}

// readLine reads one newline-terminated line. A line longer than max is
// consumed and discarded (tooLong), so one oversized message can't end the
// session the way bufio.Scanner's "token too long" does.
func readLine(r *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, tooLong, err
	}
}

func handle(reg *core.Registry, req rpcReq, version string) (any, *rpcErr) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		// Agree to the client's revision only if we implement it; otherwise
		// offer ours and let the client decide (MCP lifecycle spec).
		pv := protocolVersion
		if slices.Contains(supportedVersions, p.ProtocolVersion) {
			pv = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "devknife", "version": version},
			"instructions":    "Offline developer utilities: formatters, converters, encoders, decoders, generators and inspectors. Use `detect` when unsure which tool fits an input.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		var list []map[string]any
		list = append(list, map[string]any{
			"name":        "detect",
			"description": "Guess which devknife tools fit a piece of text (JSON, JWT, base64, cron, color, timestamp, curl…), ranked by confidence.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}, "required": []string{"input"}},
			"annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		})
		for _, t := range reg.All() {
			list = append(list, map[string]any{
				"name":        ToolName(t.ID),
				"title":       t.Name,
				"description": t.Name + ": " + t.Description,
				"inputSchema": InputSchema(t),
				"annotations": map[string]any{"title": t.Name, "readOnlyHint": true, "openWorldHint": false, "idempotentHint": !nonDeterministic(t.ID)},
			})
		}
		return map[string]any{"tools": list}, nil
	}
	return nil, &rpcErr{-32601, "method not found: " + req.Method}
}

func handleCall(ctx context.Context, reg *core.Registry, runner *core.Runner, req rpcReq) (any, *rpcErr) {
	var p struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, &rpcErr{-32602, "invalid params: " + err.Error()}
	}
	return callTool(ctx, reg, runner, p.Name, p.Arguments), nil
}

func nonDeterministic(id string) bool {
	switch id {
	case "uuid", "random", "lorem", "bcrypt", "time", "cron":
		return true
	}
	return false
}

func errResult(msg string) map[string]any {
	return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": msg}}}
}

func argString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var b bool
	if json.Unmarshal(v, &b) == nil {
		return strconv.FormatBool(b)
	}
	var f float64
	if json.Unmarshal(v, &f) == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strings.TrimSpace(string(v))
}

func callTool(ctx context.Context, reg *core.Registry, runner *core.Runner, name string, args map[string]json.RawMessage) map[string]any {
	if name == "detect" {
		var hits []core.Suggestion
		if err := runner.Do(ctx, func() { hits = reg.Detect(argString(args["input"])) }); err != nil {
			return errResult(err.Error())
		}
		var b strings.Builder
		for i, h := range hits {
			if i == 8 {
				break
			}
			fmt.Fprintf(&b, "%s (%d%%): %s", h.ID, h.Score, h.Note)
			for k, v := range h.Opts {
				fmt.Fprintf(&b, " [%s=%s]", k, v)
			}
			b.WriteString("\n")
		}
		if b.Len() == 0 {
			b.WriteString("No specific tool detected.")
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": b.String()}}}
	}
	t, ok := reg.Get(name)
	if !ok {
		return errResult("unknown tool " + name)
	}
	req := core.Request{Opts: map[string]string{}}
	for k, v := range args {
		switch k {
		case "input":
			req.Input = argString(v)
		case "data_base64":
			b, err := decodeB64(argString(v))
			if err != nil {
				return errResult("data_base64: " + err.Error())
			}
			req.Data = b
		default:
			req.Opts[k] = argString(v)
		}
	}
	res, err := runner.Run(ctx, t, req)
	if err == nil {
		if n := len(res.Output) + len(res.HTML); n > MaxResult {
			err = core.OutputTooLarge(n, MaxResult)
		}
	}
	if errors.Is(err, core.ErrTooLarge) {
		return errResult(err.Error() + "; use the devknife CLI with -o FILE for results this big")
	}
	if err != nil {
		return errResult(err.Error())
	}
	return map[string]any{"content": resultContent(res)}
}

func resultContent(res *core.Result) []map[string]any {
	var parts []map[string]any
	var b strings.Builder
	if res.Output != "" {
		b.WriteString(res.Output)
	}
	if len(res.Fields) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		for _, f := range res.Fields {
			if f.Value != "" {
				fmt.Fprintf(&b, "%s: %s\n", f.Label, f.Value)
			}
		}
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(&b, "\nWarning: %s", w)
	}
	if res.HTML != "" && res.Output == "" {
		b.WriteString(res.HTML)
	}
	if b.Len() > 0 {
		parts = append(parts, map[string]any{"type": "text", "text": b.String()})
	}
	if strings.HasPrefix(res.Image, "data:") {
		meta, data, ok := strings.Cut(strings.TrimPrefix(res.Image, "data:"), ",")
		if ok && strings.HasSuffix(meta, ";base64") {
			parts = append(parts, map[string]any{"type": "image", "mimeType": strings.TrimSuffix(meta, ";base64"), "data": data})
		}
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]any{"type": "text", "text": "(empty result)"})
	}
	return parts
}

func decodeB64(s string) ([]byte, error) {
	if i := strings.Index(s, ";base64,"); i >= 0 {
		s = s[i+8:]
	}
	s = strings.TrimRight(strings.Join(strings.Fields(s), ""), "=")
	return base64.RawStdEncoding.DecodeString(strings.NewReplacer("-", "+", "_", "/").Replace(s))
}
