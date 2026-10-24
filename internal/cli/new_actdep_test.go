package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/aac/ask/internal/core"
)

// TestNewBlocksPrintsActDepCommand pins act-58f80e: `ask new --blocks
// act-X` writes the act-side `act dep add act-X --external <id>` command
// to stderr, one line per act-shaped ref, and leaves stdout as the bare
// id. A non-act ref and a repeated ref add no line.
func TestNewBlocksPrintsActDepCommand(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if code := Run([]string{"new", "t",
				"--blocks", "act-3c89", "--blocks", "linear-eng-1234",
				"--blocks", "act-58f80e", "--blocks", "act-3c89"}); code != 0 {
				t.Errorf("new exit: %d", code)
			}
		})
	})
	it := newSingleItem(t, dir)
	if stdout != it.ID+"\n" {
		t.Fatalf("stdout = %q, want only the id %q", stdout, it.ID+"\n")
	}
	want := core.ActDepHintPrefix + "act dep add act-3c89 --external " + it.ID + "\n" +
		core.ActDepHintPrefix + "act dep add act-58f80e --external " + it.ID + "\n"
	if stderr != want {
		t.Fatalf("stderr:\n got %q\nwant %q", stderr, want)
	}
}

// TestNewBlocksActDepJSONStdoutIsItem pins that under --json the hint
// still goes to stderr and stdout stays exactly the Item object.
func TestNewBlocksActDepJSONStdoutIsItem(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			if code := Run([]string{"new", "t", "--json", "--blocks", "act-3c89"}); code != 0 {
				t.Errorf("new exit: %d", code)
			}
		})
	})
	var got core.Item
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not an Item: %v\n%s", err, stdout)
	}
	if strings.Contains(stdout, "act dep add") {
		t.Fatalf("stdout carries the hint: %s", stdout)
	}
	if want := core.ActDepHintPrefix + "act dep add act-3c89 --external " + got.ID + "\n"; stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

// TestNewNoActBlocksNoHint pins that an ask with no act-shaped ref
// prints nothing extra.
func TestNewNoActBlocksNoHint(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	initStore(t, dir)

	stderr := captureStderr(t, func() {
		_ = captureStdout(t, func() {
			if code := Run([]string{"new", "t", "--blocks", "linear-eng-1234", "--blocks", "ACT-3C89"}); code != 0 {
				t.Errorf("new exit: %d", code)
			}
		})
	})
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}
