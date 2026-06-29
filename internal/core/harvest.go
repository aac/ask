package core

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

// ErrIDCollision is returned by Harvest when the source store contains one
// or more ids that already exist in the target store. The error wraps the
// list of colliding ids so callers can surface them to the human; neither
// store is mutated when this error fires (Harvest is preflighted).
var ErrIDCollision = errors.New("id collisions in target")

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
//   - Copy pass: each item is Load'd from source and Save'd to target via
//     the canonical schema (so any drift in the source's on-disk format
//     surfaces here, not silently later).
//   - Optional cleanup: when clean is true, after every copy succeeds the
//     source items are removed. The two passes are ordered (all writes
//     first, then all deletes) so a copy failure never leaves a source
//     item deleted without a corresponding target copy.
//
// Returns the (sorted) list of ids harvested in HarvestResult.Harvested.
// An empty source returns an empty list with no error.
func Harvest(target, source *FileStore, clean bool) (*HarvestResult, error) {
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

	harvested := []string{}
	for _, id := range srcIDs {
		it, err := source.Load(id)
		if err != nil {
			return nil, fmt.Errorf("read source %s: %w", id, err)
		}
		if err := target.Save(it); err != nil {
			return nil, fmt.Errorf("write target %s: %w", id, err)
		}
		harvested = append(harvested, id)
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
