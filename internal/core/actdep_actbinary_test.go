package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestActIDRe_MatchesIDsTheActBinaryMints is the cross-tool check that
// actIDRe still describes the ids `act` generates. ask hard-codes the shape
// (see actdep.go); if act ever changes its id format, the `act dep add`
// hint `ask new --blocks` prints would stop silently. This test asks a real
// act binary to mint ids in a throwaway tracker and fails if any of them
// falls outside actIDRe.
//
// It needs `act` on PATH and skips without it. Isolation from any real
// tracker: act runs with its cwd in a fresh temp git repo, every inherited
// ACT_* variable is dropped, ACT_TRACKER_REMOTE is unset and
// XDG_CONFIG_HOME points into the temp dir (so the per-machine
// tracker-remote template is never read), ACT_NO_FETCH=1 is set, and git
// ignores global and system config. A fresh tracker has no remote, so
// nothing can be pushed.
func TestActIDRe_MatchesIDsTheActBinaryMints(t *testing.T) {
	actBin, err := exec.LookPath("act")
	if err != nil {
		t.Skip("act binary not on PATH; skipping the check that actIDRe matches the ids act mints")
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH; act needs a git working tree")
	}

	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(tmp, "xdg")
	if err := os.MkdirAll(xdg, 0o755); err != nil {
		t.Fatal(err)
	}

	env := []string{"ACT_NO_FETCH=1", "XDG_CONFIG_HOME=" + xdg,
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "ACT_") || strings.HasPrefix(k, "GIT_") || k == "XDG_CONFIG_HOME" {
			continue
		}
		env = append(env, kv)
	}

	run := func(bin string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			var stderr []byte
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = ee.Stderr
			}
			t.Fatalf("%s %s: %v\nstdout: %s\nstderr: %s", filepath.Base(bin), strings.Join(args, " "), err, out, stderr)
		}
		return out
	}

	run(gitBin, "init", "-q")
	run(gitBin, "config", "user.email", "ask-test@example.invalid")
	run(gitBin, "config", "user.name", "ask test")
	run(gitBin, "config", "commit.gpgsign", "false")
	run(actBin, "init", "--json")

	for _, title := range []string{"actIDRe probe one", "actIDRe probe two", "actIDRe probe three"} {
		out := run(actBin, "create", title, "--json")
		var got struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("act create --json: decoding %q: %v", out, err)
		}
		if got.ID == "" {
			t.Fatalf("act create --json returned no id: %s", out)
		}
		t.Logf("act minted %s", got.ID)
		if !actIDRe.MatchString(got.ID) {
			t.Errorf("act minted id %q, which actIDRe (%s) does not match: act's id format changed, so `ask new --blocks` no longer prints the act dep add hint for it; update actIDRe in actdep.go", got.ID, actIDRe)
		}
	}
}
