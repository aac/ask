package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aac/ask/internal/skill"
	"github.com/aac/ask/internal/version"
)

// TestRunInstallSkillFreshDest: a clean destination ends up with the
// full embedded tree, byte-for-byte.
func TestRunInstallSkillFreshDest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out = %+v", code, out)
	}

	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected output type %T", out)
	}
	if res.Dest != dest {
		t.Errorf("Dest = %q, want %q", res.Dest, dest)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("expected no skipped on fresh dest; got %v", res.Skipped)
	}
	if len(res.Refused) != 0 {
		t.Errorf("expected no refused on fresh dest; got %v", res.Refused)
	}

	root := skill.FS()
	if walkErr := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." || d.IsDir() {
			return nil
		}
		want, rerr := fs.ReadFile(root, p)
		if rerr != nil {
			t.Fatalf("read embedded %s: %v", p, rerr)
		}
		got, gerr := os.ReadFile(filepath.Join(dest, filepath.FromSlash(p)))
		if gerr != nil {
			t.Fatalf("read installed %s: %v", p, gerr)
		}
		if string(got) != string(want) {
			t.Errorf("installed %s differs from embedded copy", p)
		}
		return nil
	}); walkErr != nil {
		t.Fatalf("walk embedded: %v", walkErr)
	}

	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatalf("expected SKILL.md at install root: %v", err)
	}
	refs, err := os.ReadDir(filepath.Join(dest, "references"))
	if err != nil {
		t.Fatalf("read references dir: %v", err)
	}
	if len(refs) == 0 {
		t.Error("references directory is empty after install")
	}
}

// TestRunInstallSkillIdempotent: re-running against a populated dest
// puts every file under Skipped.
func TestRunInstallSkillIdempotent(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")

	if _, code := RunInstallSkill(InstallSkillOptions{Dest: dest}); code != 0 {
		t.Fatalf("first install exit = %d, want 0", code)
	}
	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest})
	if code != 0 {
		t.Fatalf("second install exit = %d, want 0; out = %+v", code, out)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected output type %T", out)
	}
	if len(res.Written) != 0 {
		t.Errorf("second install wrote files: %v (expected all skipped)", res.Written)
	}
	if len(res.Skipped) == 0 {
		t.Error("expected at least one skipped file on idempotent re-run")
	}
}

// TestRunInstallSkillRefusesOnDiff: a destination file with different
// contents is left alone, listed under Refused, exit code becomes 1.
func TestRunInstallSkillRefusesOnDiff(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tampered := []byte("# this file was edited locally\n")
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), tampered, 0o644); err != nil {
		t.Fatalf("seed tampered SKILL.md: %v", err)
	}

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest})
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out = %+v", code, out)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected output type %T", out)
	}
	foundRefused := false
	for _, p := range res.Refused {
		if strings.HasSuffix(p, "SKILL.md") {
			foundRefused = true
		}
	}
	if !foundRefused {
		t.Errorf("expected SKILL.md under Refused; got refused=%v written=%v skipped=%v", res.Refused, res.Written, res.Skipped)
	}
	got, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatalf("read post-install SKILL.md: %v", err)
	}
	if string(got) != string(tampered) {
		t.Error("tampered SKILL.md was overwritten without --force")
	}
}

// TestRunInstallSkillForceOverwrites: --force replaces a diverged file
// with the embedded copy and reports it under Written.
func TestRunInstallSkillForceOverwrites(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest, Force: true})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out = %+v", code, out)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected output type %T", out)
	}
	if len(res.Refused) != 0 {
		t.Errorf("--force should leave no refused; got %v", res.Refused)
	}
	want, err := skill.SkillMD()
	if err != nil {
		t.Fatalf("read embedded SKILL.md: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed SKILL.md: %v", err)
	}
	if string(got) != string(want) {
		t.Error("--force did not restore SKILL.md to embedded copy")
	}
}

// TestRunInstallSkillCreatesParentDir: a destination several levels deep
// is created on the fly.
func TestRunInstallSkillCreatesParentDir(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "a", "b", "c", "skills", "ask")
	if _, code := RunInstallSkill(InstallSkillOptions{Dest: dest}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing under nested dest: %v", err)
	}
}

// TestInstalledSkillCarriesVersionStamp: after install-skill the
// on-disk SKILL.md frontmatter's `version:` line matches the running
// binary's version. This is the round-trip that distribution-readiness
// §4 calls out as the "self-identification" guarantee.
func TestInstalledSkillCarriesVersionStamp(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if _, code := RunInstallSkill(InstallSkillOptions{Dest: dest}); code != 0 {
		t.Fatalf("install exit = %d, want 0", code)
	}
	got, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed SKILL.md: %v", err)
	}
	wantLine := "\nversion: " + version.Binary + "\n"
	if !strings.Contains(string(got), wantLine) {
		// Show the frontmatter for debugging.
		end := len(got)
		if end > 400 {
			end = 400
		}
		t.Errorf("installed SKILL.md missing version stamp %q\nfirst %d bytes:\n%s",
			wantLine, end, string(got[:end]))
	}
}

// TestRunCheckSkillAllMatch: install then --check yields exit 0 with
// every embedded file under Match and nothing under Drift or Missing.
func TestRunCheckSkillAllMatch(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if _, code := RunInstallSkill(InstallSkillOptions{Dest: dest}); code != 0 {
		t.Fatalf("install exit = %d, want 0", code)
	}

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest, Check: true})
	if code != 0 {
		t.Fatalf("--check exit = %d, want 0; out = %+v", code, out)
	}
	res, ok := out.(CheckSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if len(res.Drift) != 0 {
		t.Errorf("expected no drift; got %v", res.Drift)
	}
	if len(res.Missing) != 0 {
		t.Errorf("expected no missing; got %v", res.Missing)
	}
	if len(res.Match) == 0 {
		t.Error("expected at least one matched file")
	}
	if res.Version != version.Binary {
		t.Errorf("Version = %q, want %q", res.Version, version.Binary)
	}
	if res.Dest != dest {
		t.Errorf("Dest = %q, want %q", res.Dest, dest)
	}
}

// TestRunCheckSkillDetectsDrift: install, mutate a reference file,
// --check returns exit 1 with the mutated file under Drift.
func TestRunCheckSkillDetectsDrift(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if _, code := RunInstallSkill(InstallSkillOptions{Dest: dest}); code != 0 {
		t.Fatalf("install exit = %d, want 0", code)
	}

	// Find any reference file to tamper with.
	refsDir := filepath.Join(dest, "references")
	entries, err := os.ReadDir(refsDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("read references dir: %v (entries=%d)", err, len(entries))
	}
	var tamperedPath string
	for _, e := range entries {
		if !e.IsDir() {
			tamperedPath = filepath.Join(refsDir, e.Name())
			break
		}
	}
	if tamperedPath == "" {
		t.Fatal("no reference file to tamper with")
	}
	if err := os.WriteFile(tamperedPath, []byte("locally edited\n"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest, Check: true})
	if code != 1 {
		t.Fatalf("--check exit = %d, want 1; out = %+v", code, out)
	}
	res, ok := out.(CheckSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if len(res.Missing) != 0 {
		t.Errorf("expected no missing; got %v", res.Missing)
	}
	found := false
	for _, p := range res.Drift {
		if p == tamperedPath {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %s under Drift; got drift=%v match=%v", tamperedPath, res.Drift, res.Match)
	}
}

// TestRunCheckSkillReportsMissing: --check against an empty dest reports
// every embedded file under Missing and exits 1.
func TestRunCheckSkillReportsMissing(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "empty")

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest, Check: true})
	if code != 1 {
		t.Fatalf("--check exit = %d, want 1; out = %+v", code, out)
	}
	res, ok := out.(CheckSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if len(res.Match) != 0 {
		t.Errorf("expected no matches; got %v", res.Match)
	}
	if len(res.Drift) != 0 {
		t.Errorf("expected no drift; got %v", res.Drift)
	}
	if len(res.Missing) == 0 {
		t.Error("expected Missing to list embedded files; got empty")
	}
	// Must include SKILL.md.
	wantSkill := filepath.Join(dest, "SKILL.md")
	found := false
	for _, p := range res.Missing {
		if p == wantSkill {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %s under Missing; got %v", wantSkill, res.Missing)
	}
}

// TestRunCheckSkillNeverWrites: --check leaves the destination
// untouched even when files are missing or drifted.
func TestRunCheckSkillNeverWrites(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	// Don't pre-create — confirm --check doesn't make the dir either.
	if _, code := RunInstallSkill(InstallSkillOptions{Dest: dest, Check: true}); code != 1 {
		t.Fatalf("--check exit = %d, want 1", code)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("expected dest %s to remain non-existent after --check; stat err = %v", dest, err)
	}
}

// TestRunCheckSkillDetectsStaleVersionStamp: simulate a SKILL.md
// installed by an older binary by writing one that carries
// `version: v0.0.1-old` in its frontmatter. The running binary's
// version differs, so --check must classify SKILL.md as drift. This is
// the load-bearing "stale relative to current binary" signal.
func TestRunCheckSkillDetectsStaleVersionStamp(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if _, code := RunInstallSkill(InstallSkillOptions{Dest: dest}); code != 0 {
		t.Fatalf("install exit = %d, want 0", code)
	}

	// Rewrite the installed SKILL.md with a frozen "old" version stamp.
	skillPath := filepath.Join(dest, "SKILL.md")
	current, err := skill.SkillMD()
	if err != nil {
		t.Fatalf("read embedded: %v", err)
	}
	oldStamp := strings.Replace(string(current),
		"version: "+version.Binary,
		"version: v0.0.0-test-stale",
		1)
	if oldStamp == string(current) {
		t.Fatal("test setup failed: could not find current version line to rewrite")
	}
	if err := os.WriteFile(skillPath, []byte(oldStamp), 0o644); err != nil {
		t.Fatalf("write stale SKILL.md: %v", err)
	}

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest, Check: true})
	if code != 1 {
		t.Fatalf("--check exit = %d, want 1 (stale version should drift); out = %+v", code, out)
	}
	res, ok := out.(CheckSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	found := false
	for _, p := range res.Drift {
		if p == skillPath {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SKILL.md under Drift due to stale version stamp; got drift=%v match=%v", res.Drift, res.Match)
	}
}

// TestRenderInstallSkillRefusedTopLine: a refused install renders the
// remediation summary as the very first line of output.
func TestRenderInstallSkillRefusedTopLine(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("# tampered\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}

	var buf bytes.Buffer
	renderInstallSkillHuman(&buf, res, code)
	got := buf.String()

	wantPrefix := "1 file(s) modified locally; re-run with --force to overwrite, or diff against " + dest + "/ to merge.\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("render did not begin with refused summary line.\nwant prefix: %q\ngot:\n%s", wantPrefix, got)
	}
}

// TestRenderInstallSkillReloadHintOnWritten: a fresh install (everything
// Written) renders the reload hint.
func TestRenderInstallSkillReloadHintOnWritten(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}

	var buf bytes.Buffer
	renderInstallSkillHuman(&buf, res, code)
	got := buf.String()

	wantHint := "note: restart Claude Code or reload skills for " + dest + "/ to take effect."
	if !strings.Contains(got, wantHint) {
		t.Errorf("render missing reload hint.\nwant substring: %q\ngot:\n%s", wantHint, got)
	}
}

// TestRenderInstallSkillReloadHintAbsentOnSkipped: an idempotent re-run
// (everything Skipped, nothing Written) omits the reload hint.
func TestRenderInstallSkillReloadHintAbsentOnSkipped(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if _, code := RunInstallSkill(InstallSkillOptions{Dest: dest}); code != 0 {
		t.Fatalf("first install exit = %d, want 0", code)
	}
	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest})
	if code != 0 {
		t.Fatalf("second install exit = %d, want 0", code)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if len(res.Written) != 0 {
		t.Fatalf("setup invariant: expected zero Written on re-run; got %v", res.Written)
	}

	var buf bytes.Buffer
	renderInstallSkillHuman(&buf, res, code)
	got := buf.String()

	if strings.Contains(got, "restart Claude Code or reload skills") {
		t.Errorf("idempotent re-run should NOT render reload hint; got:\n%s", got)
	}
}

// TestInstallSkillJSONShapeCleanRun: on a clean install the JSON encoding
// of InstallSkillResult always emits `refused` as an empty list, never
// omits it, and includes status="clean".
func TestInstallSkillJSONShapeCleanRun(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	refused, present := parsed["refused"]
	if !present {
		t.Errorf("expected `refused` key in JSON, got: %s", string(raw))
	}
	refusedList, ok := refused.([]any)
	if !ok {
		t.Errorf("expected `refused` to be a list (possibly empty); got %T (%v)", refused, refused)
	}
	if len(refusedList) != 0 {
		t.Errorf("expected `refused` empty on clean run; got %v", refusedList)
	}

	status, _ := parsed["status"].(string)
	if status != "clean" {
		t.Errorf("status = %q, want \"clean\"", status)
	}
}

// TestInstallSkillJSONStatusPartial: a refused install yields
// status="partial" in the JSON shape.
func TestInstallSkillJSONStatusPartial(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "skills", "ask")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("# tampered\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	status, _ := parsed["status"].(string)
	if status != "partial" {
		t.Errorf("status = %q, want \"partial\"; raw=%s", status, string(raw))
	}

	refused, ok := parsed["refused"].([]any)
	if !ok || len(refused) == 0 {
		t.Errorf("expected non-empty `refused` on partial run; raw=%s", string(raw))
	}
}

// TestResolveDestDefaults: with empty Dest and empty Target the
// destination is ~/.claude/skills/ask — bit-for-bit the pre-Codex
// default. This is the "do not regress Claude Code users" guarantee.
func TestResolveDestDefaults(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir on this host: %v", err)
	}
	dest, code, env := resolveDest("", "")
	if code != 0 || env != nil {
		t.Fatalf("resolveDest(\"\", \"\") = (%q, %d, %v); want (_, 0, nil)", dest, code, env)
	}
	want := filepath.Join(home, ".claude", "skills", "ask")
	if dest != want {
		t.Errorf("default dest = %q, want %q", dest, want)
	}
}

// TestResolveDestTargetClaude: --target claude maps to the same
// destination as the no-flag default.
func TestResolveDestTargetClaude(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir on this host: %v", err)
	}
	dest, code, env := resolveDest("", "claude")
	if code != 0 || env != nil {
		t.Fatalf("resolveDest(\"\", \"claude\") = (%q, %d, %v); want (_, 0, nil)", dest, code, env)
	}
	want := filepath.Join(home, ".claude", "skills", "ask")
	if dest != want {
		t.Errorf("claude dest = %q, want %q", dest, want)
	}
}

// TestResolveDestTargetCodex: --target codex maps to ~/.codex/skills/ask.
func TestResolveDestTargetCodex(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir on this host: %v", err)
	}
	dest, code, env := resolveDest("", "codex")
	if code != 0 || env != nil {
		t.Fatalf("resolveDest(\"\", \"codex\") = (%q, %d, %v); want (_, 0, nil)", dest, code, env)
	}
	want := filepath.Join(home, ".codex", "skills", "ask")
	if dest != want {
		t.Errorf("codex dest = %q, want %q", dest, want)
	}
}

// TestResolveDestExplicitDestOverridesTarget: --dest wins over --target
// even when --target is set to a non-default value.
func TestResolveDestExplicitDestOverridesTarget(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit", "skills", "ask")
	dest, code, env := resolveDest(explicit, "codex")
	if code != 0 || env != nil {
		t.Fatalf("resolveDest(explicit, \"codex\") = (%q, %d, %v); want (explicit, 0, nil)", dest, code, env)
	}
	if dest != explicit {
		t.Errorf("dest = %q, want %q (explicit --dest must override --target)", dest, explicit)
	}
}

// TestResolveDestUnknownTarget: an unrecognized --target value is
// rejected with exit 2 ("bad input") and an error envelope, before any
// filesystem work happens.
func TestResolveDestUnknownTarget(t *testing.T) {
	dest, code, env := resolveDest("", "vscode")
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if env == nil {
		t.Fatal("expected error envelope; got nil")
	}
	if dest != "" {
		t.Errorf("dest = %q, want empty on error", dest)
	}
	if got, _ := env["error"].(string); got != "bad_flag" {
		t.Errorf("error = %q, want \"bad_flag\"", got)
	}
	msg, _ := env["message"].(string)
	if !strings.Contains(msg, "vscode") {
		t.Errorf("message should mention the bad target; got %q", msg)
	}
}

// TestRunInstallSkillTargetCodex: --target codex with an explicit Dest
// (so we don't write into the real home dir) installs the embedded tree
// at that path. Exercises the end-to-end RunInstallSkill plumbing with
// Target set.
func TestRunInstallSkillTargetCodex(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "codex-skills", "ask")
	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest, Target: "codex"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out = %+v", code, out)
	}
	res, ok := out.(InstallSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if res.Dest != dest {
		t.Errorf("Dest = %q, want %q (--dest must win over --target)", res.Dest, dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing under codex dest: %v", err)
	}
}

// TestRunInstallSkillCheckUnderCodexTarget: --check honours --target
// when Dest is empty. Use --dest to redirect into a tempdir but with
// --target=codex set; semantics should match the claude path: empty
// dest reports every embedded file missing, exit 1.
func TestRunInstallSkillCheckUnderCodexTarget(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "codex-empty")
	out, code := RunInstallSkill(InstallSkillOptions{Dest: dest, Target: "codex", Check: true})
	if code != 1 {
		t.Fatalf("--check exit = %d, want 1; out = %+v", code, out)
	}
	res, ok := out.(CheckSkillResult)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if len(res.Missing) == 0 {
		t.Error("expected Missing to list embedded files on empty codex dest")
	}
	wantSkill := filepath.Join(dest, "SKILL.md")
	found := false
	for _, p := range res.Missing {
		if p == wantSkill {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %s under Missing; got %v", wantSkill, res.Missing)
	}
}

// TestRunInstallSkillUnknownTargetEnvelope: end-to-end, an unknown
// --target value bubbles up through RunInstallSkill as the bad_flag
// envelope and exit 2. No filesystem write occurs.
func TestRunInstallSkillUnknownTargetEnvelope(t *testing.T) {
	out, code := RunInstallSkill(InstallSkillOptions{Target: "vscode"})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	env, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected type %T", out)
	}
	if got, _ := env["error"].(string); got != "bad_flag" {
		t.Errorf("error = %q, want \"bad_flag\"", got)
	}
}
