package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrIDCollision is returned by Harvest when the source store contains one
// or more ids that already exist in the target store. The error wraps the
// list of colliding ids so callers can surface them to the human; neither
// store is mutated when this error fires (Harvest is preflighted).
var ErrIDCollision = errors.New("id collisions in target")

// ErrUnreadableSource is returned by Harvest when one or more source items
// cannot be read (corrupt JSON, an unsupported schema_version, an I/O
// error). The message names every such id with its reason; neither store
// is mutated when this error fires.
var ErrUnreadableSource = errors.New("unreadable source item(s)")

// ErrStrayTarget is returned by Harvest when the target's items directory
// holds an entry whose name matches a source item's file name
// (`<id>.json`) case-insensitively without being exactly that name:
// ASK-AAAB.json against ask-aaab. On a case-insensitive filesystem the
// copy would be written into that entry and keep its name, so the
// harvested ask would be unreadable in the target (act-f9ab23). The
// same holds for the target's subs directory and the subs file of a
// source item that has subscriptions; such a file is named as
// subs/<file> (act-48158e). And for the target's fires directory and the
// fires file of a source item that has one, named as fires/<file>. The
// message names each such file; neither store is mutated.
var ErrStrayTarget = errors.New("target file differs from a source item's only in case")

// ErrFireRunning is returned by Harvest when a source item has a fire
// whose handler may still be running: a started line with no result line,
// written less than FireRunningWindow ago. The runner takes no lock, so
// its result line would land in the source store after the copy, and with
// --clean re-create the source record holding that line alone, while the
// target kept a fire that reads as died (act-afab33). The message names
// each such item and fire; neither store is mutated. A started line older
// than the window is a dead fire and does not refuse.
var ErrFireRunning = errors.New("a handler is still running")

// harvestNow is the clock Harvest measures a fire's age against. Tests
// replace it.
var harvestNow = time.Now

// runningFires returns one "<id> (fire <fire>, handler <name>, started
// <n> s ago)" entry for every fire in record with no result line that
// started within FireRunningWindow of now. A started time ahead of now
// by less than the window counts as running, as `ask show` reads it;
// further ahead it is a broken clock, not a running handler.
func runningFires(id string, record []byte, now time.Time) []string {
	fires, _ := parseFires(id, record)
	var out []string
	for _, f := range fires {
		if f.State != FireStateStarted {
			continue
		}
		age := now.Sub(f.StartedAt)
		if age >= FireRunningWindow || age <= -FireRunningWindow {
			continue
		}
		if age < 0 {
			age = 0
		}
		out = append(out, fmt.Sprintf("%s (fire %s, handler %s, started %d s ago)",
			id, f.Fire, safeHandlerName(f.Handler), int(age/time.Second)))
	}
	return out
}

// safeHandlerName is a handler name read from a fire record, made safe
// for the line of text a refusal prints. The record is a file on disk,
// possibly harvested from another store, so the name is not trusted: one
// in the handler name shape is printed as is, and anything else is cut to
// 64 bytes and printed Go-quoted, which escapes control characters,
// escape sequences and the invisible ones that reorder or hide text. The
// id and fire id need no such care: the id came from the listing and was
// checked, and parseFireLine admits only 8-hex fire ids.
func safeHandlerName(name string) string {
	if handlerNameRe.MatchString(name) {
		return name
	}
	if len(name) > 64 {
		cut := 64
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut] + "..."
	}
	return fmt.Sprintf("%q", name)
}

// strayTargetTwins returns one "<label><file> (source <id>)" entry for
// every name in target's subdir (items, subs or fires) that a source id's
// file name (<id><ext>) matches under case folding but not exactly. It compares names and
// never probes the filesystem, so the answer is the same whether or not
// the filesystem folds case. Directories count: a name is in the way
// whatever it names.
func strayTargetTwins(target *FileStore, subdir, label, ext string, srcIDs []string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(target.root, subdir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read target %s dir: %w", subdir, err)
	}
	var twins []string
	for _, e := range entries {
		for _, id := range srcIDs {
			if want := id + ext; e.Name() != want && strings.EqualFold(e.Name(), want) {
				twins = append(twins, fmt.Sprintf("%s%s (source %s)", label, e.Name(), id))
			}
		}
	}
	sort.Strings(twins)
	return twins, nil
}

// unreadableItems is the per-item failure list behind ErrUnreadableSource:
// one line of text for the human, while Unwrap keeps each cause (e.g.
// ErrUnsupportedSchemaVersion) reachable through errors.Is.
type unreadableItems []error

func (u unreadableItems) Error() string {
	parts := make([]string, len(u))
	for i, e := range u {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

func (u unreadableItems) Unwrap() []error { return u }

// HarvestResult summarizes a Harvest operation. Harvested holds the ids
// successfully copied from source to target (sorted, never nil — `[]` in
// the empty case so JSON output is `[]` not `null`). Cleaned reports
// whether the source-side delete pass ran on those ids.
type HarvestResult struct {
	Harvested []string `json:"harvested"`
	Cleaned   bool     `json:"cleaned"`
}

// Harvest copies every item from source into target. It is the primitive
// that lets an orchestrator surface asks filed in a worktree-dispatched
// subagent's .ask/ (the source) into the main checkout's .ask/ (the
// target) so the human sees them. Worktree .ask/ stores are independent
// from main's because .ask/ is gitignored and each worktree's `ask init`
// creates its own project_id.
//
// Behavior:
//   - Pre-flight: if any source id already exists in target, returns
//     ErrIDCollision wrapping the colliding ids. Neither store is touched.
//     When the validation pass below also fails, the one error carries
//     both (it is ErrIDCollision and ErrUnreadableSource).
//     A target file whose name differs from a source item's only in case
//     is refused the same way (ErrStrayTarget, naming the file), and is
//     reported together with either of the other two. So is a file in
//     the target's subs directory that differs only in case from the subs
//     file of a source item that has subscriptions (act-48158e).
//   - Validation pass: every source item is Load'd via the canonical
//     schema before anything is written. If any is unreadable, returns
//     ErrUnreadableSource naming every unreadable id; neither store is
//     touched. Harvest never skips an unreadable item — skipping plus
//     clean would lose an ask.
//     That includes a file whose `id` member is not the id it is named
//     for (ErrIDMismatch): the preflight above compared file names, so a
//     file claiming another id would otherwise be written wherever its
//     contents say (act-7ee74d).
//     Each item's subscriptions (.ask/subs/<id>.json) are read in the same
//     pass; an unreadable subs file fails the harvest the same way. So is
//     its fire record (.ask/fires/<id>.jsonl), and so does that fail it.
//     A fire record holding a fire that may still be running (a started
//     line with no result, younger than FireRunningWindow) refuses the
//     harvest too (ErrFireRunning, naming the item and fire): the runner
//     takes no lock and would write its result into the source after the
//     copy (act-afab33).
//   - Copy pass: each loaded item is Save'd to target, then its
//     subscriptions, if it has any, then its fire record, if it has one:
//     its record lines as they were written, and no other line (see
//     fireRecordLines), so nothing but fire record lines is written into
//     the target. Without the record, an item whose handler ran would
//     read in the target as one whose fire was missed. Harvest cannot
//     tell whether a handler really ran: a carried record is the source
//     store's account, as its items and subscriptions are.
//   - Optional cleanup: when clean is true, after every copy succeeds the
//     source files (item, subscriptions and fire record) are removed — by the id each file was listed under,
//     never by an id read out of its contents. The two passes are ordered (all writes
//     first, then all deletes) so a copy failure never leaves a source
//     item deleted without a corresponding target copy.
//
// Both stores' locks are held for the whole operation, taken in path order
// (spec §8), so no other writer changes either store between the pre-flight
// and the last copy or delete.
//
// Returns the (sorted) list of ids harvested in HarvestResult.Harvested.
// An empty source returns an empty list with no error.
func Harvest(target, source *FileStore, clean bool) (*HarvestResult, error) {
	unlock, err := lockStores(target, source)
	if err != nil {
		return nil, err
	}
	defer unlock()

	srcIDs, err := source.ListIDs()
	if err != nil {
		return nil, fmt.Errorf("list source items: %w", err)
	}
	sort.Strings(srcIDs)

	tgtIDs, err := target.ListIDs()
	if err != nil {
		return nil, fmt.Errorf("list target items: %w", err)
	}
	tgtSet := make(map[string]struct{}, len(tgtIDs))
	for _, id := range tgtIDs {
		tgtSet[id] = struct{}{}
	}
	var collisions []string
	for _, id := range srcIDs {
		if _, ok := tgtSet[id]; ok {
			collisions = append(collisions, id)
		}
	}
	twins, err := strayTargetTwins(target, itemsSubdir, "", ".json", srcIDs)
	if err != nil {
		return nil, err
	}
	// A collision does not return yet: the validation pass below still
	// runs, so one refused harvest names every problem in the source
	// instead of the collision alone (act-16ad85).

	// Validate before writing: read every source item first, so one
	// unreadable item fails the whole harvest with the target untouched
	// rather than leaving the items that sorted ahead of it copied
	// (act-f80d7a). Every unreadable id is reported, not just the first.
	items := make([]*Item, 0, len(srcIDs))
	subs := make([]*SubsFile, 0, len(srcIDs))
	var unreadable unreadableItems
	fires := make([][]byte, 0, len(srcIDs)) // nil for an item with no fire record
	var subbed []string                     // readable source ids that have subscriptions
	var fired []string                      // readable source ids that have a fire record
	var running []string                    // fires whose handler may still be running
	now := harvestNow()
	for _, id := range srcIDs {
		if checkID(id) != nil {
			// A stray: its name is not an id, so the report names the
			// file, in the words List and ResolvePrefix use (act-5f279f).
			unreadable = append(unreadable, fmt.Errorf(
				"%w: stray file in .ask/items/ not named for an id: %s.json", ErrInvalidID, id))
			continue
		}
		it, err := source.Load(id)
		if err != nil {
			unreadable = append(unreadable, fmt.Errorf("%s: %w", id, err))
			continue
		}
		f, err := source.LoadSubs(id)
		if err != nil {
			unreadable = append(unreadable, fmt.Errorf("%s: %w", id, err))
			continue
		}
		record, hasRecord, err := source.readFiresFile(id)
		if err != nil {
			unreadable = append(unreadable, fmt.Errorf("%s: %w", id, err))
			continue
		}
		if hasRecord {
			// Never nil for a record that exists, even an empty one: nil
			// is how the passes below know there is none.
			record, _ = fireRecordLines(record)
			fired = append(fired, id)
			running = append(running, runningFires(id, record, now)...)
		}
		items = append(items, it)
		subs = append(subs, f)
		fires = append(fires, record)
		if len(f.Subscriptions) > 0 {
			subbed = append(subbed, id)
		}
	}
	// Only a subs file the copy pass would write can land in a stray: an
	// item with no subscriptions writes nothing under subs/.
	subsTwins, err := strayTargetTwins(target, subsSubdir, subsSubdir+"/", ".json", subbed)
	if err != nil {
		return nil, err
	}
	twins = append(twins, subsTwins...)
	firesTwins, err := strayTargetTwins(target, firesSubdir, firesSubdir+"/", ".jsonl", fired)
	if err != nil {
		return nil, err
	}
	twins = append(twins, firesTwins...)
	var refusal error
	switch {
	case len(collisions) > 0 && len(unreadable) > 0:
		refusal = fmt.Errorf("%w: %v; %w (%d), nothing copied: %w",
			ErrIDCollision, collisions, ErrUnreadableSource, len(unreadable), unreadable)
	case len(collisions) > 0:
		refusal = fmt.Errorf("%w: %v", ErrIDCollision, collisions)
	case len(unreadable) > 0:
		refusal = fmt.Errorf("%w (%d), nothing copied: %w",
			ErrUnreadableSource, len(unreadable), unreadable)
	}
	if len(running) > 0 {
		r := fmt.Errorf("%w, nothing copied: %s; its result would be written to the source store after the copy, so retry once it has finished, at most %d s after it started",
			ErrFireRunning, strings.Join(running, ", "), int(FireRunningWindow/time.Second))
		if refusal != nil {
			refusal = fmt.Errorf("%w; %w", refusal, r)
		} else {
			refusal = r
		}
	}
	if len(twins) > 0 {
		stray := fmt.Errorf("%w, nothing copied: %s", ErrStrayTarget, strings.Join(twins, ", "))
		if refusal != nil {
			return nil, fmt.Errorf("%w; %w", stray, refusal)
		}
		return nil, stray
	}
	if refusal != nil {
		return nil, refusal
	}

	// items[i] was loaded from the file listed as srcIDs[i] (no id was
	// skipped, or we returned above). Everything below that names a
	// source file uses srcIDs — the listing — not it.ID.
	for i, it := range items {
		if err := target.Save(it); err != nil {
			return nil, fmt.Errorf("write target %s: %w", srcIDs[i], err)
		}
		if len(subs[i].Subscriptions) > 0 {
			if err := target.saveSubs(subs[i]); err != nil {
				return nil, fmt.Errorf("write target subscriptions %s: %w", srcIDs[i], err)
			}
		}
		if fires[i] != nil {
			if err := target.writeFiresFile(srcIDs[i], fires[i]); err != nil {
				return nil, fmt.Errorf("write target fire record %s: %w", srcIDs[i], err)
			}
		}
	}
	harvested := append([]string{}, srcIDs...)

	if clean {
		for i, id := range harvested {
			path, err := source.itemPath(id)
			if err != nil {
				return nil, fmt.Errorf("cleanup source: %w", err)
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("cleanup source %s: %w", id, err)
			}
			subsPath, err := source.subsPath(id)
			if err != nil {
				return nil, fmt.Errorf("cleanup source: %w", err)
			}
			if err := os.Remove(subsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("cleanup source subscriptions %s: %w", id, err)
			}
			// Only a record that was read, and so copied, is removed.
			if fires[i] == nil {
				continue
			}
			firesPath, err := source.firesPath(id)
			if err != nil {
				return nil, fmt.Errorf("cleanup source: %w", err)
			}
			if err := os.Remove(firesPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("cleanup source fire record %s: %w", id, err)
			}
		}
	}

	return &HarvestResult{Harvested: harvested, Cleaned: clean}, nil
}
