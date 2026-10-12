package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vtoShowTimeout runs `ask show --json <id>` and returns the
// verifier.timeout_seconds it prints, decoded without ask's own Item type
// so the assertion is on the bytes a caller receives.
func vtoShowTimeout(t *testing.T, id string) int {
	t.Helper()
	var code int
	out := captureStdout(t, func() { code = Run([]string{"show", "--json", id}) })
	if code != 0 {
		t.Fatalf("show --json %s exit: %d", id, code)
	}
	var got struct {
		Verifier *struct {
			TimeoutSeconds *int `json:"timeout_seconds"`
		} `json:"verifier"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("show --json output: %v\nout=%q", err, out)
	}
	if got.Verifier == nil || got.Verifier.TimeoutSeconds == nil {
		t.Fatalf("show --json has no verifier.timeout_seconds: %s", out)
	}
	return *got.Verifier.TimeoutSeconds
}

// vtoFileTimeout reads the stored value from the item file directly: the
// read path maps a stored 0 to 60, so only the raw file can show which
// one `ask new` wrote.
func vtoFileTimeout(t *testing.T, dir, id string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".ask", "items", id+".json"))
	if err != nil {
		t.Fatalf("read item file: %v", err)
	}
	var raw struct {
		Verifier struct {
			TimeoutSeconds int `json:"timeout_seconds"`
		} `json:"verifier"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("decode item file: %v", err)
	}
	if !strings.Contains(string(b), `"timeout_seconds"`) {
		t.Fatalf("item file has no timeout_seconds: %s", b)
	}
	return raw.Verifier.TimeoutSeconds
}

// TestNewVerifierTimeoutDefault pins spec §1.2 on the CLI path: a
// verifier without --timeout is stored with timeout_seconds 60; an
// explicit --timeout is stored as given.
func TestNewVerifierTimeoutDefault(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no --timeout", []string{"new", "t", "--verifier", "true"}, 60},
		{"--timeout 5", []string{"new", "t", "--verifier", "true", "--timeout", "5"}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initStore(t, dir)
			inDir(t, dir)
			var code int
			captureStdout(t, func() { code = Run(tc.args) })
			if code != 0 {
				t.Fatalf("new exit: %d", code)
			}
			it := newSingleItem(t, dir)
			if got := vtoFileTimeout(t, dir, it.ID); got != tc.want {
				t.Fatalf("stored timeout_seconds = %d, want %d", got, tc.want)
			}
			if got := vtoShowTimeout(t, it.ID); got != tc.want {
				t.Fatalf("show --json timeout_seconds = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestShowStoredZeroTimeoutAsDefault: an item written before the default
// was applied (timeout_seconds 0 on disk) still shows, as 60, in both
// output forms, and showing it does not rewrite the file.
func TestShowStoredZeroTimeoutAsDefault(t *testing.T) {
	dir := t.TempDir()
	initStore(t, dir)
	inDir(t, dir)
	path := filepath.Join(dir, ".ask", "items", "ask-0a1b.json")
	onDisk := `{"schema_version":"1","id":"ask-0a1b","title":"old","body":"","urgency":"normal","status":"open",` +
		`"filed_by":null,"recipient":null,"tracker_ref":null,` +
		`"verifier":{"type":"shell","command":"true","timeout_seconds":0},` +
		`"links":[],"blocks":[],"resolution_note":null,"verification_output":null,` +
		`"created_at":"2026-10-11T00:00:00Z","resolved_at":null,"verified_at":null,"closed_at":null}`
	if err := os.WriteFile(path, []byte(onDisk), 0o644); err != nil {
		t.Fatalf("write item: %v", err)
	}

	if got := vtoShowTimeout(t, "ask-0a1b"); got != 60 {
		t.Fatalf("show --json timeout_seconds = %d for a stored 0, want 60", got)
	}
	var code int
	out := captureStdout(t, func() { code = Run([]string{"show", "ask-0a1b"}) })
	if code != 0 || !strings.Contains(out, "timeout=60s") {
		t.Fatalf("show exit %d, want timeout=60s in:\n%s", code, out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if string(after) != onDisk {
		t.Fatalf("show rewrote the item file:\n was %s\n now %s", onDisk, after)
	}
}
