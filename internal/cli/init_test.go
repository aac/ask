package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aac/ask/internal/core"
)

func TestInitCreatesAskDir(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	if code := Run([]string{"init"}); code != 0 {
		t.Fatalf("init exit: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ask", "config.json")); err != nil {
		t.Fatalf(".ask/config.json missing: %v", err)
	}
}

func TestInitAppendsGitignore(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules\n"), 0o644)
	// fake git repo
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	Run([]string{"init"})

	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(b), ".ask/") {
		t.Fatalf(".gitignore did not get .ask/ appended: %s", string(b))
	}
}

// TestInitIdempotent verifies that a second `ask init` in an unchanged
// directory exits 6 with a stderr warning (spec §6 case 2, §2 exit code 6).
func TestInitIdempotent(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)
	if code := Run([]string{"init"}); code != 0 {
		t.Fatalf("first init exit: %d", code)
	}

	stderr := captureStderr(t, func() {
		if code := Run([]string{"init"}); code != 6 {
			t.Fatalf("re-init should exit 6, got %d", code)
		}
	})
	if !strings.Contains(stderr, "ask init: already initialized") {
		t.Fatalf("expected idempotent warning on stderr, got: %q", stderr)
	}
}

// TestInitNameOnFirstRun verifies --name overrides the default basename on
// first init (spec §6.1).
func TestInitNameOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	if code := Run([]string{"init", "--name=foo"}); code != 0 {
		t.Fatalf("init exit: %d", code)
	}
	cfg := readConfig(t, dir)
	if cfg.DisplayName != "foo" {
		t.Fatalf("display_name: got %q, want %q", cfg.DisplayName, "foo")
	}
}

// TestInitNameUpdatesOnReinit verifies --name with a new value on an
// initialized dir rewrites config.json atomically and exits 0 (spec §6 case
// 2: "a field changed").
func TestInitNameUpdatesOnReinit(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	if code := Run([]string{"init", "--name=foo"}); code != 0 {
		t.Fatalf("first init exit: %d", code)
	}
	originalProjectID := readConfig(t, dir).ProjectID

	if code := Run([]string{"init", "--name=bar"}); code != 0 {
		t.Fatalf("re-init with --name=bar exit: %d (want 0)", code)
	}
	cfg := readConfig(t, dir)
	if cfg.DisplayName != "bar" {
		t.Fatalf("display_name: got %q, want %q", cfg.DisplayName, "bar")
	}
	if cfg.ProjectID != originalProjectID {
		t.Fatalf("project_id should be preserved across re-init: got %q want %q", cfg.ProjectID, originalProjectID)
	}
	// Verify atomic-rename leaves no .tmp leftover in .ask/.
	entries, _ := os.ReadDir(filepath.Join(dir, ".ask"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("found .tmp leftover after atomic rewrite: %s", e.Name())
		}
	}
}

// TestInitNameSameValueIsNoOp verifies that re-running with --name set to
// the existing display_name is an idempotent no-op (exit 6 + warning).
func TestInitNameSameValueIsNoOp(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	if code := Run([]string{"init", "--name=foo"}); code != 0 {
		t.Fatalf("first init exit: %d", code)
	}
	stderr := captureStderr(t, func() {
		if code := Run([]string{"init", "--name=foo"}); code != 6 {
			t.Fatalf("re-init with unchanged name should exit 6, got %d", code)
		}
	})
	if !strings.Contains(stderr, "ask init: already initialized") {
		t.Fatalf("expected idempotent warning on stderr, got: %q", stderr)
	}
}

// TestInitJSONFirstRun verifies --json emits the §1.7 shape with
// initialized=true on first init.
func TestInitJSONFirstRun(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	var code int
	stdout := captureStdout(t, func() {
		code = Run([]string{"init", "--name=foo", "--json"})
	})
	if code != 0 {
		t.Fatalf("init --json exit: %d", code)
	}
	var out initJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("invalid JSON on stdout %q: %v", stdout, err)
	}
	if !out.Initialized {
		t.Fatalf("first init: initialized=false, want true")
	}
	if out.DisplayName != "foo" {
		t.Fatalf("display_name: got %q, want %q", out.DisplayName, "foo")
	}
	if out.ProjectID == "" {
		t.Fatal("project_id missing")
	}
}

// TestInitJSONReinit verifies --json emits initialized=false on a re-init
// (both no-op and field-change paths still print the shape).
func TestInitJSONReinit(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(dir)

	Run([]string{"init", "--name=foo"})

	// No-op re-init: exit 6, still emits JSON on stdout with initialized=false.
	var code int
	stdout := captureStdout(t, func() {
		code = Run([]string{"init", "--json"})
	})
	if code != 6 {
		t.Fatalf("no-op re-init --json exit: %d (want 6)", code)
	}
	var out initJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("invalid JSON on stdout %q: %v", stdout, err)
	}
	if out.Initialized {
		t.Fatalf("re-init: initialized=true, want false")
	}
	if out.DisplayName != "foo" {
		t.Fatalf("display_name should reflect on-disk value: got %q", out.DisplayName)
	}
}

// captureStderr mirrors list_test.go's captureStdout but for os.Stderr,
// used to inspect the idempotent-no-op warning emitted by `ask init`.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	os.Stderr = orig
	return <-done
}

// readConfig reads the persisted .ask/config.json from dir and returns it
// (used to verify on-disk state after init operations).
func readConfig(t *testing.T, dir string) *core.ProjectConfig {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".ask", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg core.ProjectConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}
