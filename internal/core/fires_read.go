package core

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// FireStateStarted is the state of a fire whose result line is not on
// disk: its handler is still running, or the runner died before writing
// one. It is never stored; only a read reports it.
const FireStateStarted = "started"

// Fire is one fire as a read reports it: the started line and, when the
// runner has written it, the result line. Field order is the key order of
// `ask fires --json`.
type Fire struct {
	Item    string `json:"item"`
	Fire    string `json:"fire"`
	Sub     string `json:"sub"`
	Handler string `json:"handler"`
	Event   string `json:"event"`
	From    Status `json:"from"`
	To      Status `json:"to"`
	// At is the transition time, the instant the item's own timestamps
	// carry for the change that fired.
	At time.Time `json:"at"`
	// StartedAt is when the started line was written; FinishedAt when the
	// result line was.
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	// State is one of the result states, or FireStateStarted when there
	// is no result line. Everything after it is null until there is one,
	// except Output, which is then empty.
	State      string  `json:"state"`
	ExitCode   *int    `json:"exit_code"`
	Detail     *string `json:"detail"`
	DurationMs *int64  `json:"duration_ms"`
	Output     string  `json:"output"`
}

// fireRecordLine is the union of both record line shapes, for reading.
type fireRecordLine struct {
	V          int       `json:"v"`
	Fire       string    `json:"fire"`
	Sub        string    `json:"sub"`
	Handler    string    `json:"handler"`
	Event      string    `json:"event"`
	From       Status    `json:"from"`
	To         Status    `json:"to"`
	At         time.Time `json:"at"`
	Phase      string    `json:"phase"`
	Ts         time.Time `json:"ts"`
	State      string    `json:"state"`
	ExitCode   *int      `json:"exit_code"`
	Detail     *string   `json:"detail"`
	DurationMs int64     `json:"duration_ms"`
	Output     string    `json:"output"`
}

// maxFireLine is the longest line a fire record can hold: a result line
// with MaxFireOutput bytes of output, every one of them escaped at its
// longest, and room for the rest. A longer line is not a record line.
const maxFireLine = 64 << 10

// parseFireLine reports whether raw is a record line: JSON in the known
// version, with a fire id and a subscription id in the generated shape and
// one of the two phases.
func parseFireLine(raw []byte) (fireRecordLine, bool) {
	var l fireRecordLine
	if len(raw) > maxFireLine || json.Unmarshal(raw, &l) != nil || l.V != FireRecordVersion ||
		!hex8Re.MatchString(l.Fire) || !hex8Re.MatchString(l.Sub) ||
		(l.Phase != FirePhaseStarted && l.Phase != FirePhaseResult) {
		return l, false
	}
	return l, true
}

// fireRecordLines returns record with every line that is not a record
// line taken out, and how many were. The lines kept are as they were
// written. Harvest carries a record into another store through this, so
// that what arrives there is fire record lines and nothing else.
func fireRecordLines(record []byte) (kept []byte, dropped int) {
	kept = []byte{}
	for _, raw := range bytes.Split(record, []byte{'\n'}) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		if _, ok := parseFireLine(raw); !ok {
			dropped++
			continue
		}
		kept = append(append(kept, raw...), '\n')
	}
	return kept, dropped
}

// readFiresFile returns the bytes of the item's fires file, and false
// when the item has none. Reading creates nothing.
//
// The file is found by its exact name among the directory's entries and
// never by probing the path, so on a filesystem that folds case a file
// named for another spelling of the id is not read as this item's. A
// symlink where the directory or the file should be is refused, not
// followed, as it is on append.
func (s *FileStore) readFiresFile(id string) ([]byte, bool, error) {
	path, err := s.firesPath(id)
	if err != nil {
		return nil, false, fmt.Errorf("fire record: %w", err)
	}
	dir := filepath.Dir(path)
	if fi, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("stat %s: %w", dir, err)
	} else if !fi.IsDir() {
		return nil, false, fmt.Errorf("%s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, fmt.Errorf("read %s dir: %w", firesSubdir, err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == filepath.Base(path) {
			found = true
			break
		}
	}
	if !found {
		return nil, false, nil
	}
	// O_NONBLOCK so that a FIFO planted at the path fails the regular-file
	// check below instead of blocking the open.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil {
		return nil, false, fmt.Errorf("fire record %s: %w", path, err)
	} else if !fi.Mode().IsRegular() {
		return nil, false, fmt.Errorf("fire record %s: not a regular file", path)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return buf.Bytes(), true, nil
}

// LoadFires reads the fires of the item with full id, oldest first (the
// order their started lines were written in). An item that never fired
// has none, and nothing is created on disk.
//
// The record is read from disk, not trusted. A line that is not a record
// line (see parseFireLine), a started line that repeats a fire id, and a
// result line that pairs with no started line are left out; skipped counts
// them. A fire's first
// result line is the one reported.
func (s *FileStore) LoadFires(id string) (fires []Fire, skipped int, err error) {
	b, ok, err := s.readFiresFile(id)
	if err != nil || !ok {
		return []Fire{}, 0, err
	}
	fires = []Fire{}
	index := map[string]int{}
	for _, raw := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		l, ok := parseFireLine(raw)
		if !ok {
			skipped++
			continue
		}
		at, seen := index[l.Fire]
		switch l.Phase {
		case FirePhaseStarted:
			if seen {
				skipped++
				continue
			}
			index[l.Fire] = len(fires)
			fires = append(fires, Fire{
				Item: id, Fire: l.Fire, Sub: l.Sub, Handler: l.Handler,
				Event: l.Event, From: l.From, To: l.To,
				At: l.At.UTC(), StartedAt: l.Ts.UTC(), State: FireStateStarted,
			})
		case FirePhaseResult:
			if !seen || fires[at].Sub != l.Sub || fires[at].FinishedAt != nil {
				skipped++
				continue
			}
			f := &fires[at]
			ts, ms := l.Ts.UTC(), l.DurationMs
			f.FinishedAt, f.DurationMs = &ts, &ms
			f.State, f.ExitCode, f.Detail, f.Output = l.State, l.ExitCode, l.Detail, l.Output
		}
	}
	return fires, skipped, nil
}

// FiredItemIDs lists, sorted, the ids of the items that have a fires
// file. A store where nothing ever fired has none, and nothing is created
// on disk. Entries not named <id>.jsonl for an id in the generated shape
// are not fires files and are left out.
func (s *FileStore) FiredItemIDs() ([]string, error) {
	dir := filepath.Join(s.root, firesSubdir)
	if fi, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s dir: %w", firesSubdir, err)
	}
	ids := []string{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || checkID(id) != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// writeFiresFile puts a whole fires file in place for the item, replacing
// one already there. Only Harvest calls it, with both stores locked, for
// an item the target did not hold: a fires file already under that name
// belongs to no item. The file is 0600, like one an append creates, and a
// symlink where the fires directory should be is refused.
func (s *FileStore) writeFiresFile(id string, data []byte) error {
	path, err := s.firesPath(id)
	if err != nil {
		return fmt.Errorf("fire record: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if fi, err := os.Lstat(dir); err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("temp name for %s: %w", path, err)
	}
	tmp := path + "." + hex.EncodeToString(suffix[:]) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
