// Package mcp implements a minimal stdio JSON-RPC 2.0 server exposing the
// ask CLI verbs as MCP tools. Wire protocol: newline-delimited JSON-RPC 2.0
// on stdin, responses on stdout. Three methods are implemented:
//
//   - initialize  — handshake; advertises tool capabilities.
//   - tools/list  — returns the registered tool descriptors with input
//     schemas mirroring the CLI flag set (spec §5).
//   - tools/call  — dispatches into the matching ask_* handler and returns
//     the JSON body wrapped in the MCP content envelope, or surfaces an
//     error envelope (spec §5.5) with isError: true.
//
// Tool errors are returned in the result envelope (`isError: true`) rather
// than as JSON-RPC errors, matching the MCP convention. JSON-RPC framing
// errors (parse error, unknown method) are returned as JSON-RPC error
// objects per the spec (-32700/-32600/-32601/-32602/-32603).
//
// Handlers call into internal/core directly (no shell-out to the CLI layer),
// so the MCP surface is a thin parallel to the CLI dispatch in
// internal/cli/root.go.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/aac/ask/internal/version"
)

// protocolVersion is the MCP wire version we advertise during initialize.
// Spec §5: server name ask-mcp, protocol version 2024-11-05.
const protocolVersion = "2024-11-05"

// serverName is echoed in the initialize response so MCP clients can render an
// identifying label. The version echoed alongside it is version.Binary — the
// single stamped source that `ask version` also reports — never a separate
// literal (a hardcoded copy drifts every release; verify-release check 7 gates it).
const serverName = "ask-mcp"

// Server is a stdio MCP host. It owns the JSON-RPC framing, the tool
// registry, and the per-tool dispatch glue. One Server is single-threaded:
// Serve reads, dispatches, and writes one request at a time. This matches
// the stdio transport's serial nature and keeps the store's mutations
// race-free.
type Server struct {
	repoRoot string
	in       io.Reader
	out      io.Writer
}

// NewServer constructs a Server rooted at repoRoot. repoRoot is used as
// the cwd-equivalent for every tool dispatch (each handler calls
// core.OpenStore(repoRoot, nil)); the caller is responsible for ensuring
// repoRoot points at a directory that already contains .ask/ (or will
// receive an exit-5-mapped not-initialized error from the first tool that
// touches the store).
func NewServer(repoRoot string, in io.Reader, out io.Writer) *Server {
	return &Server{repoRoot: repoRoot, in: in, out: out}
}

// Run starts the JSON-RPC loop on stdio using the process's cwd as the
// repo root. Returns 0 on clean EOF, 5 on a transport-level I/O failure.
// Mirrors the act/internal/mcp.Run() entry point.
func Run() int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ask mcp:", err)
		return 5
	}
	s := NewServer(cwd, os.Stdin, os.Stdout)
	if err := s.Serve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "ask mcp:", err)
		return 5
	}
	return 0
}

// jsonRPCRequest is the inbound shape on stdin. id is json.RawMessage so we
// round-trip numbers and strings unmodified per JSON-RPC 2.0.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is the success/error envelope we emit. Exactly one of
// Result/Error is set per the spec; the omitempty tags keep the wire form
// clean.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

// jsonRPCError mirrors the spec's error object. Data is optional and used
// for free-form diagnostics.
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes. We use a tight subset; everything beyond
// these codes belongs in the tool-result envelope (spec §5.5).
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// toolDescriptor is one entry in the tools/list response. InputSchema is a
// freeform JSON Schema object describing the tool's argument shape.
type toolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// toolResult is the tools/call response envelope (spec §5.1). Content is a
// list of content parts; we use a single text part containing the JSON
// body of the underlying tool. IsError signals to MCP clients that the
// tool returned an error envelope rather than a successful result.
type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// toolContent is one content part. Only "text" parts are produced.
type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// errEnvelope is the spec §5.5 error shape: {code, message}. code is the
// CLI exit-code taxonomy value (§2): 2/3/4/5/6. The CLI's stderr message
// is reused verbatim for message.
type errEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve drives the read/dispatch/write loop. It terminates cleanly on EOF
// or on ctx.Done(). Bad JSON is reported as a Parse Error and the loop
// continues; a malformed-but-parsed request returns Invalid Request.
func (s *Server) Serve(ctx context.Context) error {
	r := bufio.NewReader(s.in)
	enc := json.NewEncoder(s.out)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := r.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				if len(line) == 0 {
					return nil
				}
				// fall through and process trailing partial line
			} else {
				return err
			}
		}
		line = trimLine(line)
		if len(line) == 0 {
			if err == io.EOF {
				return nil
			}
			continue
		}
		var req jsonRPCRequest
		if jerr := json.Unmarshal(line, &req); jerr != nil {
			s.writeError(enc, nil, errParse, "parse error", jerr.Error())
			if err == io.EOF {
				return nil
			}
			continue
		}
		s.dispatch(ctx, enc, req)
		if err == io.EOF {
			return nil
		}
	}
}

// trimLine drops trailing CR/LF; matches the behaviour of bufio.Scanner
// without the 64KiB token cap.
func trimLine(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// dispatch routes one parsed request to the correct handler. Notifications
// (id absent) are silently ignored except for handshake errors.
func (s *Server) dispatch(ctx context.Context, enc *json.Encoder, req jsonRPCRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(enc, req)
	case "initialized", "notifications/initialized":
		// Notification — no response.
	case "tools/list":
		s.handleToolsList(enc, req)
	case "tools/call":
		s.handleToolsCall(ctx, enc, req)
	case "ping":
		s.writeResult(enc, req.ID, map[string]any{})
	default:
		s.writeError(enc, req.ID, errMethodNotFound, "method not found", req.Method)
	}
}

// handleInitialize emits the canonical handshake response. We advertise
// only the `tools` capability; resources, prompts, sampling are
// unimplemented and omitted so clients don't try to call them.
func (s *Server) handleInitialize(enc *json.Encoder, req jsonRPCRequest) {
	res := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": version.Binary,
		},
	}
	s.writeResult(enc, req.ID, res)
}

// handleToolsList returns the static tool registry. The list shape and
// schemas are stable; clients are expected to cache them per-session.
func (s *Server) handleToolsList(enc *json.Encoder, req jsonRPCRequest) {
	s.writeResult(enc, req.ID, map[string]any{"tools": tools()})
}

// handleToolsCall dispatches to the matching tool implementation. The
// params shape is `{name: string, arguments: object}`; missing arguments
// default to an empty object so tools without inputs work without
// ceremony.
func (s *Server) handleToolsCall(ctx context.Context, enc *json.Encoder, req jsonRPCRequest) {
	_ = ctx
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(enc, req.ID, errInvalidParams, "invalid params", err.Error())
		return
	}
	if p.Name == "" {
		s.writeError(enc, req.ID, errInvalidParams, "missing tool name", nil)
		return
	}
	args := p.Arguments
	if len(args) == 0 {
		args = []byte("{}")
	}
	content, isErr := s.invoke(p.Name, args)
	tr := toolResult{
		Content: content,
		IsError: isErr,
	}
	s.writeResult(enc, req.ID, tr)
}

// invoke is the central tool dispatcher. It returns the list of content
// parts that should populate the toolResult envelope's `content` field
// and a flag indicating whether the call resulted in an error envelope.
//
// Most tool calls return a single text content part carrying either the
// JSON-encoded success body or the spec §5.5 {code, message} error
// object. Idempotent no-op lifecycle calls (ask_resolve on resolved,
// ask_close on closed, ask_reopen on open) return two text parts:
// the unchanged Item JSON followed by a human-readable warning string
// (`ask <verb>: already <status>`). Per spec §5.5 these arrive with
// isError=false because the call succeeded — the warning is an
// MCP-idiomatic affordance, not an error.
//
// Unknown tool names return a spec §5.5-style error envelope with
// code=2 (validation) so the client's framing remains consistent with
// regular tool errors.
func (s *Server) invoke(name string, args json.RawMessage) ([]toolContent, bool) {
	switch name {
	case "ask_new":
		body, isErr := callNew(s.repoRoot, args)
		return textContent(body), isErr
	case "ask_list":
		body, isErr := callList(s.repoRoot, args)
		return textContent(body), isErr
	case "ask_show":
		body, isErr := callShow(s.repoRoot, args)
		return textContent(body), isErr
	case "ask_resolve":
		return callResolve(s.repoRoot, args)
	case "ask_reopen":
		return callReopen(s.repoRoot, args)
	case "ask_close":
		return callClose(s.repoRoot, args)
	default:
		return textContent(encodeErr(2, fmt.Sprintf("ask mcp: unknown tool %q", name))), true
	}
}

// textContent wraps a single JSON-encoded body string in the canonical
// one-element content slice. The mutate path constructs its own slice
// directly so it can append the no-op warning part.
func textContent(s string) []toolContent {
	return []toolContent{{Type: "text", Text: s}}
}

// encodeErr marshals an errEnvelope into its JSON wire string. Failure is
// not expected (the shape is trivial) but is reported via a fallback
// string so callers always get a usable text body.
func encodeErr(code int, msg string) string {
	b, err := json.Marshal(errEnvelope{Code: code, Message: msg})
	if err != nil {
		return fmt.Sprintf(`{"code":%d,"message":%q}`, code, msg)
	}
	return string(b)
}

// encodeJSON marshals v into a string ready for the text field of a
// toolContent. A marshal failure is surfaced as an internal-error
// envelope; this path is only reachable for genuinely pathological
// payloads (cyclic graphs, etc.) and is not expected in practice.
func encodeJSON(v any) (string, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return encodeErr(5, "ask mcp: marshal: "+err.Error()), true
	}
	return string(b), false
}

// writeResult emits a JSON-RPC success response. Notifications (nil id)
// produce no output, matching the spec.
func (s *Server) writeResult(enc *json.Encoder, id json.RawMessage, result any) {
	if id == nil {
		return
	}
	_ = enc.Encode(jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
}

// writeError emits a JSON-RPC error response. Parse errors are emitted
// even without a request id (per the spec); other framing errors with
// nil id are silently dropped because notifications don't get responses.
func (s *Server) writeError(enc *json.Encoder, id json.RawMessage, code int, msg string, data any) {
	if id == nil && code != errParse {
		return
	}
	_ = enc.Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: msg, Data: data},
	})
}
