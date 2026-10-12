package core

import (
	"errors"
	"fmt"
	"os"
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
//   - Validation pass: every source item is Load'd via the canonical
//     schema before anything is written. If any is unreadable, returns
//     ErrUnreadableSource naming every unreadable id; neither store is
//     touched. Harvest never skips an unreadable item — skipping plus
//     clean would lose an ask.
//   - Copy pass: each loaded item is Save'd to target.
//   - Optional cleanup: when clean is true, after every copy succeeds the
//     source items are removed. The two passes are ordered (all writes
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
	if len(collisions) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrIDCollision, collisions)
	}

	// Validate before writing: read every source item first, so one
	// unreadable item fails the whole harvest with the target untouched
	// rather than leaving the items that sorted ahead of it copied
	// (act-f80d7a). Every unreadable id is reported, not just the first.
	items := make([]*Item, 0, len(srcIDs))
	var unreadable unreadableItems
	for _, id := range srcIDs {
		it, err := source.Load(id)
		if err != nil {
			unreadable = append(unreadable, fmt.Errorf("%s: %w", id, err))
			continue
		}
		items = append(items, it)
	}
	if len(unreadable) > 0 {
		return nil, fmt.Errorf("%w (%d), nothing copied: %w",
			ErrUnreadableSource, len(unreadable), unreadable)
	}

	harvested := []string{}
	for _, it := range items {
		if err := target.Save(it); err != nil {
			return nil, fmt.Errorf("write target %s: %w", it.ID, err)
		}
		harvested = append(harvested, it.ID)
	}

	if clean {
		for _, id := range harvested {
			path := source.itemPath(id)
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("cleanup source %s: %w", id, err)
			}
		}
	}

	return &HarvestResult{Harvested: harvested, Cleaned: clean}, nil
}
