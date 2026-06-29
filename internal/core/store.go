package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileStore is the on-disk JSON store at <root>/.ask/. Each item lives in
// its own file under .ask/items/<id>.json. All mutations (config and item
// writes) go through atomicWrite: write to <target>.tmp then os.Rename,
// which is atomic for same-volume renames on every supported platform
// (spec §8).
type FileStore struct {
	root   string // absolute path to the .ask/ directory (not the project root).
	config *ProjectConfig
}

const (
	askDir      = ".ask"
	itemsSubdir = "items"
	configFile  = "config.json"
)

// OpenStore creates or opens an .ask/ directory at projectRoot. projectRoot
// is the directory that contains (or should contain) .ask/.
//
// First-open path: if .ask/config.json does not exist, the caller-supplied
// cfg is written verbatim. The caller (e.g. `ask init`) is responsible for
// composing cfg with a fresh ULID project_id and the desired display_name.
// If cfg is nil on this path, OpenStore returns a "not initialized" error
// — the store will not invent a config.
//
// Subsequent-open path: if .ask/config.json exists, cfg is ignored and the
// on-disk config is loaded and validated as JSON. A corrupt config.json
// returns an error (mapped to CLI exit code 5 in the caller, per spec §7).
func OpenStore(projectRoot string, cfg *ProjectConfig) (*FileStore, error) {
	askPath := filepath.Join(projectRoot, askDir)
	if err := os.MkdirAll(filepath.Join(askPath, itemsSubdir), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", askPath, err)
	}
	cfgPath := filepath.Join(askPath, configFile)
	_, statErr := os.Stat(cfgPath)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		if cfg == nil {
			return nil, fmt.Errorf("ask not initialized at %s (run 'ask init')", projectRoot)
		}
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal config: %w", err)
		}
		if err := atomicWrite(cfgPath, b); err != nil {
			return nil, fmt.Errorf("write config: %w", err)
		}
	case statErr != nil:
		return nil, fmt.Errorf("stat %s: %w", cfgPath, statErr)
	default:
		b, err := os.ReadFile(cfgPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", cfgPath, err)
		}
		var existing ProjectConfig
		if err := json.Unmarshal(b, &existing); err != nil {
			return nil, fmt.Errorf("config.json corrupt at %s: %w", cfgPath, err)
		}
		cfg = &existing
	}
	return &FileStore{root: askPath, config: cfg}, nil
}

// Root returns the absolute path to the .ask/ directory.
func (s *FileStore) Root() string { return s.root }

// Config returns the loaded or freshly-written project config. Never nil
// for a successfully opened store.
func (s *FileStore) Config() *ProjectConfig { return s.config }

// SaveConfig atomically rewrites .ask/config.json to reflect cfg and
// updates the in-memory pointer. Used by `ask init` when --name changes
// the display_name on re-init (spec §6 case 2). The atomic-rename pattern
// matches Save (spec §8).
func (s *FileStore) SaveConfig(cfg *ProjectConfig) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := atomicWrite(filepath.Join(s.root, configFile), b); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	s.config = cfg
	return nil
}

func (s *FileStore) itemPath(id string) string {
	return filepath.Join(s.root, itemsSubdir, id+".json")
}

// Save writes item to .ask/items/<id>.json atomically. The Item is written
// as-is except for SchemaVersion: a zero value is upgraded to
// CurrentSchemaVersion in-place so every item on disk carries the field
// regardless of how it was constructed (spec §1.1, §12). Save makes no
// state-transition decisions (resolve/reopen/close live in
// transitions.go). Callers that need to mutate state should compose a
// transition helper and then call Save.
func (s *FileStore) Save(item *Item) error {
	if item.SchemaVersion == "" {
		item.SchemaVersion = CurrentSchemaVersion
	}
	b, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal item %s: %w", item.ID, err)
	}
	return atomicWrite(s.itemPath(item.ID), b)
}

// Load reads a single item by its full id. Returns ErrIDNotFound if the
// underlying file is missing (CLI maps to exit 3 per spec §4.3). A corrupt
// item file returns a wrapped JSON error (CLI maps to exit 5 per spec §7).
func (s *FileStore) Load(id string) (*Item, error) {
	b, err := os.ReadFile(s.itemPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrIDNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read item %s: %w", id, err)
	}
	var it Item
	if err := json.Unmarshal(b, &it); err != nil {
		return nil, fmt.Errorf("item %s corrupt: %w", id, err)
	}
	return &it, nil
}

// ListIDs returns the ids of every item currently in .ask/items/, derived
// from filenames. Order is filesystem-defined (i.e. unspecified); callers
// that need a deterministic order should sort, or use List which sorts by
// the spec §3.2 default ordering.
func (s *FileStore) ListIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, itemsSubdir))
	if err != nil {
		return nil, fmt.Errorf("read items dir: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		// Skip in-flight atomic writes (<id>.json.tmp would not end in .json,
		// but be defensive about any other .tmp-flavoured leftovers).
		if strings.HasSuffix(name, ".tmp") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	return ids, nil
}

// List returns every item in the store, sorted per spec §3.2:
//
//  1. urgency descending — blocker (0) before normal (1) before fyi (2).
//  2. created_at ascending (oldest first within a given urgency).
//  3. id ascending (lexical) as a stable tiebreaker.
//
// This is the order both plain-text `ask list` and `ask list --json` use.
// Filtering by status (default: hide closed) is the caller's responsibility;
// List returns every item on disk.
func (s *FileStore) List() ([]*Item, error) {
	ids, err := s.ListIDs()
	if err != nil {
		return nil, err
	}
	out := make([]*Item, 0, len(ids))
	for _, id := range ids {
		it, err := s.Load(id)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ui, uj := urgencyRank(out[i].Urgency), urgencyRank(out[j].Urgency)
		if ui != uj {
			return ui < uj
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// urgencyRank assigns the spec §3.2 ordering value to an urgency. Unknown
// values sort after fyi so a malformed item never displaces a known one
// at the top of the list.
func urgencyRank(u Urgency) int {
	switch u {
	case UrgencyBlocker:
		return 0
	case UrgencyNormal:
		return 1
	case UrgencyFYI:
		return 2
	default:
		return 3
	}
}

// atomicWrite writes data to path via <path>.tmp + os.Rename. The .tmp
// file is in the same directory as path so the rename is same-volume and
// atomic on every supported platform (spec §8).
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort cleanup; ignore the error since the original write
		// failure is what the caller needs to see.
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
