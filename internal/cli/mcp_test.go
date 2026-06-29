package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStdin replaces os.Stdin with r for the test's lifetime. mcp.Run reads
// from os.Stdin; we feed it an immediately-closed pipe so the JSON-RPC loop
// hits EOF and returns cleanly (exit 0). Saved/restored via t.Cleanup.
func withStdin(t *testing.T, r *os.File) {
	t.Helper()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })
}

// withCwd saves the current working directory and restores it via t.Cleanup.
// runMCP calls os.Chdir for --workdir; without restore, subsequent tests
// in the same process inherit the test's chdir.
func withCwd(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
}

// silenceStdout redirects os.Stdout to /dev/null for the test's lifetime so
// the MCP server's framing output doesn't pollute go test output.
func silenceStdout(t *testing.T) {
	t.Helper()
	orig := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	os.Stdout = devnull
	t.Cleanup(func() {
		os.Stdout = orig
		_ = devnull.Close()
	})
}

// captureStderrPipe redirects os.Stderr to a pipe and returns a func that
// yields the captured bytes (after restoring os.Stderr). Distinct from
// init_test.go's captureStderr(t, fn) which scopes capture around a callback.
func captureStderrPipe(t *testing.T) func() string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	return func() string {
		os.Stderr = orig
		_ = w.Close()
		b, _ := io.ReadAll(r)
		_ = r.Close()
		return string(b)
	}
}

// closedStdinPipe returns the read end of a pipe whose write end is closed
// immediately, so the reader sees EOF on first read.
func closedStdinPipe(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_ = w.Close()
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// TestRunMCPWorkdirChdirs verifies that --workdir chdirs into the given path
// before delegating to mcp.Run, by checking os.Getwd() after the call.
func TestRunMCPWorkdirChdirs(t *testing.T) {
	withCwd(t)
	silenceStdout(t)
	withStdin(t, closedStdinPipe(t))

	dir := t.TempDir()
	// On macOS /var is a symlink to /private/var; resolve so the cwd
	// comparison is on canonical paths.
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}

	code := runMCP([]string{"--workdir", dir})
	if code != 0 {
		t.Fatalf("runMCP exit = %d, want 0", code)
	}

	got, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("eval symlinks got: %v", err)
	}
	if gotResolved != want {
		t.Fatalf("cwd after runMCP = %q, want %q", gotResolved, want)
	}
}

// TestRunMCPWorkdirMissing verifies a non-existent --workdir exits 2 with a
// chdir error on stderr.
func TestRunMCPWorkdirMissing(t *testing.T) {
	withCwd(t)
	silenceStdout(t)
	withStdin(t, closedStdinPipe(t))
	getStderr := captureStderrPipe(t)

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	code := runMCP([]string{"--workdir", missing})
	stderr := getStderr()

	if code != 2 {
		t.Fatalf("runMCP exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "ask mcp: chdir") {
		t.Fatalf("stderr missing chdir diagnostic: %q", stderr)
	}
	if !strings.Contains(stderr, missing) {
		t.Fatalf("stderr missing path %q: %q", missing, stderr)
	}
}

// TestRunMCPUnknownFlag verifies an unknown flag exits 2.
func TestRunMCPUnknownFlag(t *testing.T) {
	withCwd(t)
	silenceStdout(t)
	withStdin(t, closedStdinPipe(t))
	// flag.ContinueOnError writes its own diagnostic to the FlagSet's
	// output (which defaults to os.Stderr); swallow it.
	_ = captureStderrPipe(t)

	code := runMCP([]string{"--bogus"})
	if code != 2 {
		t.Fatalf("runMCP exit = %d, want 2", code)
	}
}
