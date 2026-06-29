package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aac/ask/internal/core"
	"github.com/oklog/ulid/v2"
)

// initJSONOutput is the spec §1.7 JSON shape emitted by `ask init --json`.
// `initialized` is true on first init, false on idempotent re-init (whether
// or not display_name changed).
type initJSONOutput struct {
	ProjectID   string    `json:"project_id"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	Initialized bool      `json:"initialized"`
}

// runInit implements `ask init [--name=<display>] [--json]`. See spec §6
// (idempotency), §1.7 (output shape), §2 (exit codes — exit 6 for
// no-field-change re-init).
//
// Behavior summary:
//   - .ask/ absent: create everything, exit 0, initialized=true.
//   - .ask/ present, --name changes display_name: atomic-rewrite config.json,
//     exit 0, initialized=false.
//   - .ask/ present, no field changes: exit 6 with stderr warning
//     "ask init: already initialized", initialized=false.
//   - .ask/ present but config.json corrupt/missing: exit 5 (OpenStore).
//
// The gitignore step runs idempotently in every successful path.
func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = noUsage
	name := fs.String("name", "", "Display name for this project (defaults to basename of cwd)")
	asJSON := fs.Bool("json", false, "Emit the §1.7 JSON shape on success")
	if err := fs.Parse(args); err != nil {
		return handleParseErr(err, fs, "init",
			"ask init [flags]",
			"Initialize a .ask/ directory in the current project. Idempotent.")
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask init: %v\n", err)
		return 5
	}
	askPath := filepath.Join(cwd, ".ask")

	// Distinguish first-init from re-init by stat-ing config.json before
	// OpenStore (which would create the file on the first-init path).
	cfgPath := filepath.Join(askPath, "config.json")
	_, statErr := os.Stat(cfgPath)
	firstInit := os.IsNotExist(statErr)

	var newCfg *core.ProjectConfig
	if firstInit {
		display := *name
		if display == "" {
			display = filepath.Base(cwd)
		}
		newCfg = &core.ProjectConfig{
			ProjectID:   ulid.Make().String(),
			DisplayName: display,
			CreatedAt:   time.Now().UTC(),
		}
	}

	store, err := core.OpenStore(cwd, newCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask init: %v\n", err)
		return 5
	}

	// On re-init with --name that differs from the on-disk display_name,
	// rewrite config.json (atomic) and report exit 0. Otherwise re-init is
	// an exit-6 no-op (after the gitignore step).
	changed := false
	if !firstInit && *name != "" && *name != store.Config().DisplayName {
		updated := *store.Config()
		updated.DisplayName = *name
		if err := store.SaveConfig(&updated); err != nil {
			fmt.Fprintf(os.Stderr, "ask init: %v\n", err)
			return 5
		}
		changed = true
	}

	// Append .ask/ to .gitignore if we're in a git repo and it's not already
	// ignored. Failure here is a warning, not a fatal: the .ask/ store is
	// already written and usable. Runs idempotently on every path.
	if isGitRepo(cwd) {
		if err := ensureGitignored(cwd, ".ask/"); err != nil {
			fmt.Fprintf(os.Stderr, "ask init: warning: %v\n", err)
		}
	}

	cfg := store.Config()
	exitCode := 0
	if !firstInit && !changed {
		// Idempotent no-op: spec §1.7 + §2 exit-6 with stderr warning.
		fmt.Fprintln(os.Stderr, "ask init: already initialized")
		exitCode = 6
	}

	if *asJSON {
		emitJSON(initJSONOutput{
			ProjectID:   cfg.ProjectID,
			DisplayName: cfg.DisplayName,
			CreatedAt:   cfg.CreatedAt,
			Initialized: firstInit,
		})
	} else {
		// Spec §1.7 plain-text output. First init vs re-init has distinct
		// wording so scripts can grep, though scripts should prefer exit
		// codes (0 vs 6) and --json.
		if firstInit {
			fmt.Printf("initialized .ask/ (project_id=%s)\n", cfg.ProjectID)
		} else {
			fmt.Printf(".ask/ already initialized (project_id=%s)\n", cfg.ProjectID)
		}
	}
	return exitCode
}

// isGitRepo reports whether root contains a .git entry (file or directory).
// The spec §6 says detection walks up to find a .git; Task 8's contract is
// the simpler "current dir contains .git", which matches the test scaffold.
func isGitRepo(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// ensureGitignored appends line to <root>/.gitignore if a line matching it
// (with or without trailing slash) is not already present. A missing
// .gitignore is created.
func ensureGitignored(root, line string) error {
	path := filepath.Join(root, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(existing), "\n") {
		trimmed := strings.TrimSpace(l)
		if trimmed == strings.TrimSuffix(line, "/") || trimmed == line {
			return nil
		}
	}
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		existing = append(existing, '\n')
	}
	existing = append(existing, []byte(line+"\n")...)
	return os.WriteFile(path, existing, 0o644)
}
