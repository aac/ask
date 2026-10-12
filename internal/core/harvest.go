package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// message names each such file; neither store is mutated.
var ErrStrayTarget = errors.New("target file differs from a source item's only in case")

// strayTargetTwins returns one "<file> (source <id>)" entry for every name
// in target's items directory that a source id's file name matches under
// case folding but not exactly. It compares names and never probes the
// filesystem, so the answer is the same whether or not the filesystem
// folds case. Directories count: a name is in the way whatever it names.
func strayTargetTwins(target *FileStore, srcIDs []string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(target.root, itemsSubdir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read target items dir: %w", err)
	}
	var twins []string
	for _, e := range entries {
		for _, id := range srcIDs {
			if want := id + ".json"; e.Name() != want && strings.EqualFold(e.Name(), want) {
				twins = append(twins, fmt.Sprintf("%s (source %s)", e.Name(), id))
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
//     reported together with either of the other two.
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
//     pass; an unreadable subs file fails the harvest the same way.
//   - Copy pass: each loaded item is Save'd to target, then its
//     subscriptions, if it has any.
//   - Optional cleanup: when clean is true, after every copy succeeds the
//     source files (item and subscriptions) are removed — by the id each file was listed under,
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
	twins, err := strayTargetTwins(target, srcIDs)
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
	for _, id := range srcIDs {
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
		items = append(items, it)
		subs = append(subs, f)
	}
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
	}
	harvested := append([]string{}, srcIDs...)

	if clean {
		for _, id := range harvested {
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
		}
	}

	return &HarvestResult{Harvested: harvested, Cleaned: clean}, nil
}
