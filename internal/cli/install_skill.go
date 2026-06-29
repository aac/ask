package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aac/ask/internal/skill"
	"github.com/aac/ask/internal/version"
)

// InstallSkillOptions controls `ask install-skill`. Dest is the target
// skills directory on disk; when empty the destination is derived from
// Target (default "claude" → ~/.claude/skills/ask, "codex" →
// ~/.codex/skills/ask). When Dest is non-empty it overrides Target.
// Force overwrites existing files that differ from the embedded copy
// without prompting. AsJSON selects the output rendering branch. Check
// switches to read-only mode: compare embedded vs installed bytes,
// never write, exit 0 if everything matches and 1 if anything drifts or
// is missing.
type InstallSkillOptions struct {
	Dest   string
	Target string
	Force  bool
	AsJSON bool
	Check  bool
}

// InstallSkillResult is the success payload. Written records absolute
// paths of files that were freshly written (new) or rewritten (changed
// with --force). Skipped records files whose contents already match the
// embedded copy verbatim. Refused records files that already exist with
// different contents and were left untouched because --force was not set;
// when Refused is non-empty the exit code is 1 so an agent can detect
// the partial state.
type InstallSkillResult struct {
	Dest    string   `json:"dest"`
	Status  string   `json:"status"`
	Written []string `json:"written"`
	Skipped []string `json:"skipped"`
	Refused []string `json:"refused"`
}

// CheckSkillResult is the payload returned by `ask install-skill --check`.
// It mirrors the per-file classification of the install path but in a
// read-only shape: every embedded file lands in exactly one of Match,
// Drift, or Missing. Exit code is 0 iff Drift and Missing are both
// empty.
//
// The version-stamp line of SKILL.md is part of the comparison: an
// installed SKILL.md that was written by an older binary version will
// show up under Drift, which is the intended "your installed skill is
// stale relative to the running binary" signal.
type CheckSkillResult struct {
	Dest    string   `json:"dest"`
	Version string   `json:"version"`
	Match   []string `json:"match"`
	Drift   []string `json:"drift"`
	Missing []string `json:"missing"`
}

// RunInstallSkill writes the embedded skill tree (SKILL.md plus
// references/*.md) into opts.Dest, creating parent directories as
// needed. The operation is idempotent: re-running with no source change
// and no destination change is a no-op. Policy:
//
//   - destination missing → write, record under Written.
//   - destination present and bytes-equal to embedded → skip, record under Skipped.
//   - destination present and bytes-differ:
//   - if opts.Force: overwrite, record under Written.
//   - else: leave untouched, record under Refused; exit code becomes 1.
//
// Files in opts.Dest that are not part of the embedded tree are left alone:
// the ask repo owns the canonical skill; users may extend, but install
// never destroys.
//
// Exit codes: 0 = clean, 1 = partial (refused files present),
// 2 = bad input (unresolvable home dir), 3 = filesystem error.
func RunInstallSkill(opts InstallSkillOptions) (any, int) {
	dest, code, env := resolveDest(opts.Dest, opts.Target)
	if env != nil {
		return env, code
	}

	if opts.Check {
		return runCheckSkill(dest)
	}

	res := InstallSkillResult{
		Dest:    dest,
		Written: []string{},
		Skipped: []string{},
		Refused: []string{},
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return map[string]any{
			"error":   "write_failed",
			"message": fmt.Sprintf("ask install-skill: mkdir %s: %v", dest, err),
		}, 3
	}

	root := skill.FS()
	walkErr := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if d.IsDir() {
			if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
				return fmt.Errorf("mkdir %s: %w", target, mkErr)
			}
			return nil
		}
		want, rerr := fs.ReadFile(root, p)
		if rerr != nil {
			return fmt.Errorf("read embedded %s: %w", p, rerr)
		}
		existing, statErr := os.ReadFile(target)
		switch {
		case statErr == nil:
			if bytes.Equal(existing, want) {
				res.Skipped = append(res.Skipped, target)
				return nil
			}
			if !opts.Force {
				res.Refused = append(res.Refused, target)
				return nil
			}
		case errors.Is(statErr, os.ErrNotExist):
			// fall through and write
		default:
			return fmt.Errorf("stat %s: %w", target, statErr)
		}
		if werr := os.WriteFile(target, want, 0o644); werr != nil {
			return fmt.Errorf("write %s: %w", target, werr)
		}
		res.Written = append(res.Written, target)
		return nil
	})
	if walkErr != nil {
		return map[string]any{
			"error":   "write_failed",
			"message": fmt.Sprintf("ask install-skill: %v", walkErr),
		}, 3
	}

	if len(res.Refused) > 0 {
		res.Status = "partial"
		return res, 1
	}
	res.Status = "clean"
	return res, 0
}

// resolveDest applies the default destination when the caller passes an
// empty Dest. An explicit Dest always wins (and Target is ignored). When
// Dest is empty, Target picks the host: "" or "claude" →
// ~/.claude/skills/ask, "codex" → ~/.codex/skills/ask. An unknown
// Target returns a 2 ("bad input") envelope. On home-dir lookup failure
// it returns a 2 envelope so both the write and check paths share
// identical error shape.
func resolveDest(dest, target string) (string, int, map[string]any) {
	if dest != "" {
		return dest, 0, nil
	}
	var hostDir string
	switch target {
	case "", "claude":
		hostDir = ".claude"
	case "codex":
		hostDir = ".codex"
	default:
		return "", 2, map[string]any{
			"error":   "bad_flag",
			"message": fmt.Sprintf("ask install-skill: unknown --target %q; valid: claude, codex", target),
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", 2, map[string]any{
			"error":   "bad_flag",
			"message": fmt.Sprintf("ask install-skill: cannot resolve home dir: %v; pass --dest <path>", err),
		}
	}
	return filepath.Join(home, hostDir, "skills", "ask"), 0, nil
}

// runCheckSkill is the read-only verification path for
// `ask install-skill --check`. It never writes; it classifies every
// embedded file against its on-disk counterpart and returns exit 1 if
// any file is missing or drifted.
func runCheckSkill(dest string) (any, int) {
	res := CheckSkillResult{
		Dest:    dest,
		Version: version.Binary,
		Match:   []string{},
		Drift:   []string{},
		Missing: []string{},
	}

	root := skill.FS()
	walkErr := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." || d.IsDir() {
			return nil
		}
		want, rerr := fs.ReadFile(root, p)
		if rerr != nil {
			return fmt.Errorf("read embedded %s: %w", p, rerr)
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		existing, statErr := os.ReadFile(target)
		switch {
		case errors.Is(statErr, os.ErrNotExist):
			res.Missing = append(res.Missing, target)
			return nil
		case statErr != nil:
			return fmt.Errorf("stat %s: %w", target, statErr)
		}
		if bytes.Equal(existing, want) {
			res.Match = append(res.Match, target)
		} else {
			res.Drift = append(res.Drift, target)
		}
		return nil
	})
	if walkErr != nil {
		return map[string]any{
			"error":   "check_failed",
			"message": fmt.Sprintf("ask install-skill --check: %v", walkErr),
		}, 3
	}

	if len(res.Drift) > 0 || len(res.Missing) > 0 {
		return res, 1
	}
	return res, 0
}

// runInstallSkill dispatches `ask install-skill`. It writes the embedded
// canonical skill tree to ~/.claude/skills/ask (or --dest), making the
// ask binary itself the distribution mechanism for the workflow doc.
func runInstallSkill(args []string) int {
	fs := flag.NewFlagSet("install-skill", flag.ContinueOnError)
	dest := fs.String("dest", "", "destination skills directory; overrides --target")
	target := fs.String("target", "claude", "skill host: claude (~/.claude/skills/ask) or codex (~/.codex/skills/ask)")
	force := fs.Bool("force", false, "overwrite existing files that differ from the embedded copy")
	asJSON := fs.Bool("json", false, "emit JSON output instead of human-friendly text")
	check := fs.Bool("check", false, "read-only: report whether installed skill matches the embedded copy; never write")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	out, code := RunInstallSkill(InstallSkillOptions{
		Dest:   *dest,
		Target: *target,
		Force:  *force,
		AsJSON: *asJSON,
		Check:  *check,
	})

	if code >= 2 {
		if *asJSON {
			b, err := json.MarshalIndent(out, "", "  ")
			if err == nil {
				fmt.Println(string(b))
			}
		} else if env, ok := out.(map[string]any); ok {
			if msg, _ := env["message"].(string); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
		}
		return code
	}

	if *check {
		res, ok := out.(CheckSkillResult)
		if !ok {
			fmt.Fprintf(os.Stderr, "ask install-skill --check: unexpected output type %T\n", out)
			return 1
		}
		if *asJSON {
			b, jerr := json.MarshalIndent(res, "", "  ")
			if jerr != nil {
				fmt.Fprintf(os.Stderr, "ask install-skill --check: json marshal: %v\n", jerr)
				return 1
			}
			fmt.Println(string(b))
			return code
		}
		renderCheckSkillHuman(os.Stdout, res, code)
		return code
	}

	res, ok := out.(InstallSkillResult)
	if !ok {
		fmt.Fprintf(os.Stderr, "ask install-skill: unexpected output type %T\n", out)
		return 1
	}

	if *asJSON {
		b, jerr := json.MarshalIndent(res, "", "  ")
		if jerr != nil {
			fmt.Fprintf(os.Stderr, "ask install-skill: json marshal: %v\n", jerr)
			return 1
		}
		fmt.Println(string(b))
		return code
	}
	renderInstallSkillHuman(os.Stdout, res, code)
	return code
}

// renderInstallSkillHuman prints the human-friendly install summary.
// One section per outcome class — agents reading stdout get the same
// information as JSON consumers.
func renderInstallSkillHuman(w io.Writer, res InstallSkillResult, code int) {
	if len(res.Refused) > 0 {
		fmt.Fprintf(w, "%d file(s) modified locally; re-run with --force to overwrite, or diff against %s/ to merge.\n", len(res.Refused), res.Dest)
	}
	fmt.Fprintf(w, "ask install-skill %s → %s\n", version.Binary, res.Dest)
	if len(res.Written) > 0 {
		fmt.Fprintln(w, "  written:")
		for _, p := range res.Written {
			fmt.Fprintln(w, "    "+p)
		}
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintln(w, "  unchanged (already matches embedded copy):")
		for _, p := range res.Skipped {
			fmt.Fprintln(w, "    "+p)
		}
	}
	if len(res.Refused) > 0 {
		fmt.Fprintln(w, "  refused (differs from embedded copy; pass --force to overwrite):")
		for _, p := range res.Refused {
			fmt.Fprintln(w, "    "+p)
		}
	}
	if code == 0 && len(res.Written) == 0 && len(res.Skipped) == 0 && len(res.Refused) == 0 {
		fmt.Fprintln(w, "  (no files to install)")
	}
	if code != 0 {
		fmt.Fprintln(w, strings.TrimSpace("install incomplete; see refused list above"))
	}
	if len(res.Written) > 0 {
		fmt.Fprintf(w, "note: restart Claude Code or reload skills for %s/ to take effect.\n", res.Dest)
	}
}

// renderCheckSkillHuman prints the human-friendly --check summary. The
// shape mirrors renderInstallSkillHuman so output between the two modes
// reads consistently.
func renderCheckSkillHuman(w io.Writer, res CheckSkillResult, code int) {
	fmt.Fprintf(w, "ask install-skill --check %s → %s\n", res.Version, res.Dest)
	if len(res.Match) > 0 {
		fmt.Fprintln(w, "  match:")
		for _, p := range res.Match {
			fmt.Fprintln(w, "    "+p)
		}
	}
	if len(res.Drift) > 0 {
		fmt.Fprintln(w, "  drift (installed differs from embedded copy):")
		for _, p := range res.Drift {
			fmt.Fprintln(w, "    "+p)
		}
	}
	if len(res.Missing) > 0 {
		fmt.Fprintln(w, "  missing (no installed copy at expected path):")
		for _, p := range res.Missing {
			fmt.Fprintln(w, "    "+p)
		}
	}
	if code == 0 && len(res.Match) == 0 && len(res.Drift) == 0 && len(res.Missing) == 0 {
		fmt.Fprintln(w, "  (no embedded files to check)")
	}
	if code != 0 {
		fmt.Fprintln(w, "skill out of date; re-run `ask install-skill` (or with --force) to update.")
	}
}
