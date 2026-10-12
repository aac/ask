package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Fire records live beside the items, one append-only file per item that
// has ever fired: .ask/fires/<item-id>.jsonl. Each fire is two JSON lines
// paired by their fire id: a "started" line, written by the process that
// changed the item while it still held the store lock, and a "result"
// line, written by the runner when the handler is done. Only a fire
// creates the directory.
const firesSubdir = "fires"

// FireRecordVersion is the "v" every fire record line carries.
const FireRecordVersion = 1

// MaxFireOutput is how much of a handler's combined stdout and stderr a
// result line keeps: the last MaxFireOutput bytes.
const MaxFireOutput = 4096

// The phases of a fire record line.
const (
	FirePhaseStarted = "started"
	FirePhaseResult  = "result"
)

// The states a result line can carry.
const (
	// FireOK: the handler exited 0.
	FireOK = "ok"
	// FireFailed: the handler exited non-zero (exit_code), or died on a
	// signal (exit_code null, the signal in detail).
	FireFailed = "failed"
	// FireNotRun: the handler was never started. Detail is one of
	// "no handler named <name>", "not runnable" or
	// "subscription removed before it ran".
	FireNotRun = "not_run"
	// FireSpawnFailed: starting the runner or the handler failed. Detail
	// is the operating system's error, or "runner protocol mismatch".
	FireSpawnFailed = "spawn_failed"
	// FireTimedOut: the handler was still running at the timeout and was
	// killed.
	FireTimedOut = "timeout"
)

// Result details that are fixed strings.
const (
	FireDetailProtocolMismatch = "runner protocol mismatch"
	FireDetailSubRemoved       = "subscription removed before it ran"
	FireDetailNotRunnable      = "not runnable"
)

// FireStarted is the first line of a fire. Field order is the on-disk key
// order.
type FireStarted struct {
	V       int    `json:"v"`
	Fire    string `json:"fire"`
	Sub     string `json:"sub"`
	Handler string `json:"handler"`
	Event   string `json:"event"`
	From    Status `json:"from"`
	To      Status `json:"to"`
	// At is the transition time, the instant the item's own timestamps
	// carry for this change.
	At    time.Time `json:"at"`
	Phase string    `json:"phase"`
	// Ts is when this line was written.
	Ts time.Time `json:"ts"`
}

// FireResult is the second line of a fire. Field order is the on-disk key
// order. ExitCode and Detail are null when they do not apply.
type FireResult struct {
	V          int       `json:"v"`
	Fire       string    `json:"fire"`
	Sub        string    `json:"sub"`
	Phase      string    `json:"phase"`
	State      string    `json:"state"`
	ExitCode   *int      `json:"exit_code"`
	Detail     *string   `json:"detail"`
	DurationMs int64     `json:"duration_ms"`
	Ts         time.Time `json:"ts"`
	Output     string    `json:"output"`
}

var hex8Re = regexp.MustCompile(`^[0-9a-f]{8}$`)

// newFireID returns 8 lowercase hex characters.
func newFireID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("fire id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// firesPath is the only place an item id becomes a fires file path; like
// itemPath it refuses any id not in the generated shape.
func (s *FileStore) firesPath(id string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.root, firesSubdir, id+".jsonl"), nil
}

// appendFireLine appends one record line to the item's fires file with a
// single write(2) on an append-mode descriptor, so lines from the process
// that changed the item and from any number of runners never interleave.
// It takes no lock.
func (s *FileStore) appendFireLine(itemID string, line any) error {
	path, err := s.firesPath(itemID)
	if err != nil {
		return fmt.Errorf("fire record: %w", err)
	}
	b, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal fire record of %s: %w", itemID, err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("append to %s: %w", path, werr)
	}
	if cerr != nil {
		return fmt.Errorf("close %s: %w", path, cerr)
	}
	return nil
}

// appendFireResult writes the result line of one fire.
func (s *FileStore) appendFireResult(itemID, fireID, subID, state string, exitCode *int, detail string, began time.Time, output string) error {
	var d *string
	if detail != "" {
		d = &detail
	}
	now := time.Now().UTC()
	return s.appendFireLine(itemID, FireResult{
		V:          FireRecordVersion,
		Fire:       fireID,
		Sub:        subID,
		Phase:      FirePhaseResult,
		State:      state,
		ExitCode:   exitCode,
		Detail:     d,
		DurationMs: now.Sub(began).Milliseconds(),
		Ts:         now,
		Output:     output,
	})
}
