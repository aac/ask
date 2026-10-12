package mcp

// Boundary tests for firing through the MCP server. A fire re-executes the
// running binary, so these tests build ask from the current source once,
// start it as `ask mcp`, and speak JSON-RPC over its pipes.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

var fireBuild struct {
	once sync.Once
	dir  string
	bin  string
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fireBuild.dir != "" {
		_ = os.RemoveAll(fireBuild.dir)
	}
	os.Exit(code)
}

// fireBinary builds ask on first use and returns its path.
func fireBinary(t *testing.T) string {
	t.Helper()
	fireBuild.once.Do(func() {
		dir, err := os.MkdirTemp("", "ask-fire-build-")
		if err != nil {
			fireBuild.err = err
			return
		}
		fireBuild.dir = dir
		fireBuild.bin = filepath.Join(dir, "ask")
		if out, err := exec.Command("go", "build", "-o", fireBuild.bin, "github.com/aac/ask/cmd/ask").CombinedOutput(); err != nil {
			fireBuild.err = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if fireBuild.err != nil {
		t.Fatalf("build ask: %v", fireBuild.err)
	}
	return fireBuild.bin
}

// fireServer is one HOME, one initialized project and one `ask mcp`
// process serving it.
type fireServer struct {
	t      *testing.T
	bin    string
	home   string
	proj   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr bytes.Buffer
	lines  []string // every line the server wrote to stdout
	nextID int
}

func newFireServer(t *testing.T) *fireServer {
	t.Helper()
	base := t.TempDir()
	s := &fireServer{t: t, bin: fireBinary(t), home: filepath.Join(base, "home"), proj: filepath.Join(base, "proj")}
	for _, d := range []string{filepath.Join(s.home, ".config", "ask", "callbacks"), s.proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.cli("init")
	return s
}

func (s *fireServer) env() []string {
	return []string{"HOME=" + s.home, "PATH=/fire-test-marker-path"}
}

// cli runs one ask command in the project and returns its stdout.
func (s *fireServer) cli(args ...string) string {
	s.t.Helper()
	c := exec.Command(s.bin, args...)
	c.Dir = s.proj
	c.Env = s.env()
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		s.t.Fatalf("ask %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

func (s *fireServer) install(name, script string) {
	s.t.Helper()
	path := filepath.Join(s.home, ".config", "ask", "callbacks", name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		s.t.Fatal(err)
	}
}

// subscribedItem files an item and subscribes handler to every event.
func (s *fireServer) subscribedItem(handler string) string {
	s.t.Helper()
	var it core.Item
	if err := json.Unmarshal([]byte(s.cli("new", "fired over mcp", "--json")), &it); err != nil {
		s.t.Fatal(err)
	}
	s.cli("subscribe", it.ID, "--on", "any", "--handler", handler)
	return it.ID
}

// start launches `ask mcp` in the project.
func (s *fireServer) start() {
	s.t.Helper()
	s.cmd = exec.Command(s.bin, "mcp")
	s.cmd.Dir = s.proj
	s.cmd.Env = s.env()
	s.cmd.Stderr = &s.stderr
	in, err := s.cmd.StdinPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	out, err := s.cmd.StdoutPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	s.stdin, s.stdout = in, bufio.NewReader(out)
	if err := s.cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() {
		_ = s.stdin.Close()
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	})
}

// call sends one tools/call and returns the tool result.
func (s *fireServer) call(tool string, args map[string]any) toolResult {
	s.t.Helper()
	s.nextID++
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": s.nextID, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.stdin.Write(append(req, '\n')); err != nil {
		s.t.Fatalf("write request: %v", err)
	}
	line, err := s.stdout.ReadString('\n')
	if err != nil {
		s.t.Fatalf("read response to %s: %v\nserver stderr: %s", tool, err, s.stderr.String())
	}
	s.lines = append(s.lines, strings.TrimRight(line, "\n"))
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		s.t.Fatalf("response to %s is not JSON-RPC: %v\n%s", tool, err, line)
	}
	return extractToolResult(s.t, resp)
}

// stop closes the server's stdin, reads whatever else it wrote, and waits
// for it to exit.
func (s *fireServer) stop() {
	s.t.Helper()
	_ = s.stdin.Close()
	rest, err := io.ReadAll(s.stdout)
	if err != nil {
		s.t.Fatalf("drain server stdout: %v", err)
	}
	for _, l := range strings.Split(string(rest), "\n") {
		if l != "" {
			s.lines = append(s.lines, l)
		}
	}
	if err := s.cmd.Wait(); err != nil {
		s.t.Fatalf("ask mcp exit: %v\nstderr: %s", err, s.stderr.String())
	}
}

// fireRecord is one line of .ask/fires/<id>.jsonl, either shape.
type fireRecord struct {
	Fire     string `json:"fire"`
	Event    string `json:"event"`
	From     string `json:"from"`
	To       string `json:"to"`
	Phase    string `json:"phase"`
	State    string `json:"state"`
	ExitCode *int   `json:"exit_code"`
	Output   string `json:"output"`
}

func (s *fireServer) records(id string) (started, results []fireRecord) {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.proj, ".ask", "fires", id+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		s.t.Fatal(err)
	}
	for _, raw := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		var r fireRecord
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			s.t.Fatalf("fire record line is not JSON: %v\n%s", err, raw)
		}
		if r.Phase == "started" {
			started = append(started, r)
		} else {
			results = append(results, r)
		}
	}
	return started, results
}

// waitResults waits until the item's record holds n result lines.
func (s *fireServer) waitResults(id string, n int) (started, results []fireRecord) {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		started, results = s.records(id)
		if len(results) >= n {
			return started, results
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("waited 15s for %d result line(s) on %s; have %d started, %d results", n, id, len(started), len(results))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestFireResolveCloseReopenOnceEachMCP walks one subscribed item through
// ask_resolve, ask_reopen, ask_resolve and ask_close on a live server.
// Each transition adds exactly one started line and one ok result line,
// and its response envelope is what it is without a subscription: one
// text part holding the item.
func TestFireResolveCloseReopenOnceEachMCP(t *testing.T) {
	s := newFireServer(t)
	s.install("record", "#!/bin/sh\necho \"$ASK_EVENT $ASK_FROM_STATUS $ASK_TO_STATUS\" >> \"$HOME/fire.log\"\n")
	id := s.subscribedItem("record")
	s.start()

	steps := []struct {
		tool            string
		event, from, to string
	}{
		{"ask_resolve", "resolve", "open", "resolved"},
		{"ask_reopen", "reopen", "resolved", "open"},
		{"ask_resolve", "resolve", "open", "resolved"},
		{"ask_close", "close", "resolved", "closed"},
	}
	var wantLog []string
	for i, st := range steps {
		tr := s.call(st.tool, map[string]any{"id": id})
		if tr.IsError || len(tr.Content) != 1 {
			t.Fatalf("%s: isError=%v, %d content parts: %+v", st.tool, tr.IsError, len(tr.Content), tr.Content)
		}
		var it core.Item
		if err := json.Unmarshal([]byte(tr.Content[0].Text), &it); err != nil {
			t.Fatalf("%s: content is not the item: %v\n%s", st.tool, err, tr.Content[0].Text)
		}
		if string(it.Status) != st.to {
			t.Errorf("%s: item status %s, want %s", st.tool, it.Status, st.to)
		}
		started, results := s.waitResults(id, i+1)
		if len(started) != i+1 || len(results) != i+1 {
			t.Fatalf("after %s: %d started and %d result lines, want %d each", st.tool, len(started), len(results), i+1)
		}
		st0, r := started[i], results[i]
		if st0.Event != st.event || st0.From != st.from || st0.To != st.to {
			t.Errorf("after %s: started line %+v", st.tool, st0)
		}
		if r.Fire != st0.Fire || r.State != "ok" || r.ExitCode == nil || *r.ExitCode != 0 {
			t.Errorf("after %s: result line %+v", st.tool, r)
		}
		wantLog = append(wantLog, st.event+" "+st.from+" "+st.to)
	}

	// A no-op over MCP fires nothing.
	if tr := s.call("ask_close", map[string]any{"id": id}); tr.IsError || len(tr.Content) != 2 {
		t.Errorf("ask_close on a closed item: isError=%v, %d content parts", tr.IsError, len(tr.Content))
	}
	s.stop()
	if started, results := s.records(id); len(started) != len(steps) || len(results) != len(steps) {
		t.Errorf("after a no-op: %d started and %d result lines, want %d each", len(started), len(results), len(steps))
	}
	b, err := os.ReadFile(filepath.Join(s.home, "fire.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != strings.Join(wantLog, "\n") {
		t.Errorf("handler runs:\n got %q\nwant %q", got, strings.Join(wantLog, "\n"))
	}
}

// TestFireHandlerOutputNeverReachesServerStdout: the server's stdout is
// the JSON-RPC channel, so nothing a handler prints may land on it. The
// handler prints a marker to stdout and to stderr. Every line the server
// wrote must parse as JSON-RPC and none may contain the marker; and the
// marker must be in the fire record's output, so that "not on the
// server's stdout" cannot mean "the handler never printed".
func TestFireHandlerOutputNeverReachesServerStdout(t *testing.T) {
	const marker = "FIRE-MARKER-7f3a"
	s := newFireServer(t)
	s.install("noisy", "#!/bin/sh\necho \"out "+marker+"\"\necho \"err "+marker+"\" >&2\n")
	id := s.subscribedItem("noisy")
	s.start()

	if tr := s.call("ask_resolve", map[string]any{"id": id}); tr.IsError {
		t.Fatalf("ask_resolve: %+v", tr.Content)
	}
	// Keep the server up until the handler has finished, then make it
	// answer once more: anything the handler leaked is on stdout by now.
	_, results := s.waitResults(id, 1)
	if tr := s.call("ask_show", map[string]any{"id": id}); tr.IsError {
		t.Fatalf("ask_show: %+v", tr.Content)
	}
	s.stop()

	if len(s.lines) != 2 {
		t.Errorf("server wrote %d lines for 2 requests:\n%s", len(s.lines), strings.Join(s.lines, "\n"))
	}
	for _, l := range s.lines {
		var resp struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal([]byte(l), &resp); err != nil || resp.JSONRPC != "2.0" || len(resp.ID) == 0 {
			t.Errorf("server stdout line is not a JSON-RPC response (%v):\n%s", err, l)
		}
		if strings.Contains(l, marker) {
			t.Errorf("handler output reached the server's stdout:\n%s", l)
		}
	}
	if strings.Contains(s.stderr.String(), marker) {
		t.Errorf("handler output reached the server's stderr:\n%s", s.stderr.String())
	}
	if want := "out " + marker + "\nerr " + marker + "\n"; results[0].Output != want {
		t.Errorf("fire record output %q, want %q", results[0].Output, want)
	}
	if results[0].State != "ok" {
		t.Errorf("fire state %q, want ok", results[0].State)
	}
}
