package core

import (
	"crypto/rand"
	"encoding/hex"
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
// writes) go through atomicWrite: write to a uniquely named temp file
// beside the target, then os.Rename, which is atomic for same-volume
// renames (spec §8). Writers of an existing item also hold the store lock
// (see Mutate).
type FileStore struct {
	root   string // absolute path to the .ask/ directory (not the project root).
	config *ProjectConfig
}

const (
	askDir      = ".ask"
	itemsSubdir = "items"
	configFile  = "config.json"
)

// ErrNotInitialized is returned by OpenStore when a read-path open (cfg
// nil) finds no .ask/config.json and no item files to speak of: this
// directory simply is not an ask store. The CLI maps it to exit 5 (spec
// §2).
var ErrNotInitialized = errors.New("ask not initialized")

// ErrInvalidID is returned by every FileStore method that turns an id into
// a path when the id is not exactly the generated shape (spec §1.1,
// `^ask-[0-9a-f]{4}$`). Such an id could address a file outside
// .ask/items/, or — differing only in case — another item's file, so it
// is refused before any path is built.
var ErrInvalidID = errors.New("invalid ask id")

// ErrIDMismatch is returned by Load when an item file's `id` member is not
// the id the file is named for (<id>.json). The file's contents are not
// trusted to say where the item lives — a store can be handed to harvest
// by another agent — so such an item is unreadable, on the same footing as
// a corrupt file (spec §7.2; CLI exit 5).
var ErrIDMismatch = errors.New("item id does not match its file name")

// StrandedStoreError is returned by OpenStore when a read-path open finds
// no .ask/config.json but .ask/items/ does hold item files. That store is
// not empty — it holds ItemCount asks nobody can reach until the config is
// restored — so it must not present as "not initialized" (spec §2, exit 7;
// act-55ae5b). Consumers branch on the exit code, not on the message.
type StrandedStoreError struct {
	// Root is the project directory containing the stranded .ask/.
	Root string
	// ItemCount is the number of .ask/items/*.json files found.
	ItemCount int
}

func (e *StrandedStoreError) Error() string {
	return fmt.Sprintf(
		"%s/.ask/config.json is missing but .ask/items/ holds %d item file(s): "+
			"this store is stranded, not empty (run 'ask init' here to restore access; items are preserved)",
		e.Root, e.ItemCount)
}

// OpenStore creates or opens an .ask/ directory at projectRoot. projectRoot
// is the directory that contains (or should contain) .ask/.
//
// A read-path open (cfg nil) never mutates the filesystem: it stats
// config.json first and returns an error without creating .ask/ or
// .ask/items/. Creating a store is exclusively the job of the write path
// below, so a failed read can no longer leave a husk behind that later
// scans mistake for a real store (act-55ae5b).
//
// First-open path: if .ask/config.json does not exist, the caller-supplied
// cfg is written verbatim (creating .ask/items/ along the way). The caller
// (e.g. `ask init`) is responsible for composing cfg with a fresh ULID
// project_id and the desired display_name. If cfg is nil on this path,
// OpenStore returns ErrNotInitialized (empty/absent store) or
// *StrandedStoreError (items present, config gone) — the store will not
// invent a config.
//
// Subsequent-open path: if .ask/config.json exists, cfg is ignored and the
// on-disk config is loaded and validated as JSON. A corrupt config.json
// returns an error (mapped to CLI exit code 5 in the caller, per spec §7).
func OpenStore(projectRoot string, cfg *ProjectConfig) (*FileStore, error) {
	askPath := filepath.Join(projectRoot, askDir)
	itemsPath := filepath.Join(askPath, itemsSubdir)
	cfgPath := filepath.Join(askPath, configFile)
	_, statErr := os.Stat(cfgPath)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		if cfg == nil {
			// Read path. Nothing below this point writes: distinguish a
			// stranded store from an absent one and return.
			if n := countItemFiles(itemsPath); n > 0 {
				return nil, &StrandedStoreError{Root: projectRoot, ItemCount: n}
			}
			return nil, fmt.Errorf("%w at %s (run 'ask init')", ErrNotInitialized, projectRoot)
		}
		if err := os.MkdirAll(itemsPath, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", askPath, err)
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

// checkID refuses any id that is not exactly the shape NewID generates
// (spec §1.1, `^ask-[0-9a-f]{4}$`): idPrefix followed by idHexLen
// lowercase hex characters, nothing else. It is the check behind itemPath,
// so Load, Save and harvest's delete all refuse anything else. The exact
// shape is also the path-safety guarantee (no separator, dot or NUL can
// appear), and lowercase-only is what keeps two ids from naming one file
// on a case-insensitive filesystem (act-1e046d).
func checkID(id string) error {
	if len(id) != len(idPrefix)+idHexLen || !strings.HasPrefix(id, idPrefix) {
		return fmt.Errorf("%w %q", ErrInvalidID, id)
	}
	for _, c := range []byte(id[len(idPrefix):]) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return fmt.Errorf("%w %q", ErrInvalidID, id)
		}
	}
	return nil
}

// itemPath is the only place an id becomes a path. It returns ErrInvalidID
// rather than a path for any id checkID refuses, so no caller can read,
// write or delete outside .ask/items/ through an id.
func (s *FileStore) itemPath(id string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.root, itemsSubdir, id+".json"), nil
}

// isItemFile reports whether a directory entry under .ask/items/ is one of
// our per-item JSON files. Directories, non-.json entries, and .tmp
// leftovers from an interrupted atomic write are not.
func isItemFile(e os.DirEntry) bool {
	if e.IsDir() {
		return false
	}
	name := e.Name()
	return strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".tmp")
}

// countItemFiles returns the number of per-item JSON files in itemsPath. A
// missing or unreadable directory counts as zero — this is a diagnostic
// used to classify a config-less .ask/, not a load path, so it reports "no
// items to strand" rather than failing.
func countItemFiles(itemsPath string) int {
	entries, err := os.ReadDir(itemsPath)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if isItemFile(e) {
			n++
		}
	}
	return n
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
	// Never write an item under a schema marker this binary does not
	// implement (spec §1.1.1). Load already refuses such items, so this
	// only fires for a struct-literal caller; it keeps "no write path can
	// produce or overwrite a non-v1 item" true by construction.
	if err := checkSchemaVersion(item.SchemaVersion); err != nil {
		return fmt.Errorf("save item %s: %w", item.ID, err)
	}
	path, err := s.itemPath(item.ID)
	if err != nil {
		return fmt.Errorf("save item: %w", err)
	}
	b, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal item %s: %w", item.ID, err)
	}
	// Create .ask/items/ here rather than in OpenStore: opening a store is
	// a read and must not touch the filesystem (act-55ae5b), so the first
	// write is what materializes the directory.
	itemsPath := filepath.Join(s.root, itemsSubdir)
	if err := os.MkdirAll(itemsPath, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", itemsPath, err)
	}
	return atomicWrite(path, b)
}

// Load reads a single item by its full id. Returns ErrIDNotFound if the
// underlying file is missing (CLI maps to exit 3 per spec §4.3). A corrupt
// item file returns a wrapped JSON error, and an item whose schema_version
// is not CurrentSchemaVersion returns a wrapped ErrUnsupportedSchemaVersion
// (CLI maps both to exit 5 per spec §1.1.1, §7). So does a file whose `id`
// member is not id itself (ErrIDMismatch): the returned item's ID always
// equals the id asked for. An id that is not in the generated shape is
// refused with ErrInvalidID before the filesystem is touched.
func (s *FileStore) Load(id string) (*Item, error) {
	path, err := s.itemPath(id)
	if err != nil {
		return nil, fmt.Errorf("load item: %w", err)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrIDNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read item %s: %w", id, err)
	}
	var it Item
	if err := json.Unmarshal(b, &it); err != nil {
		// A well-formed item from a different schema is not "corrupt";
		// say which it is so the reader repairs the right thing.
		if errors.Is(err, ErrUnsupportedSchemaVersion) {
			return nil, fmt.Errorf("item %s: %w", id, err)
		}
		return nil, fmt.Errorf("item %s corrupt: %w", id, err)
	}
	if it.ID != id {
		return nil, fmt.Errorf("item %s: %w: %s.json holds id %q", id, ErrIDMismatch, id, it.ID)
	}
	return &it, nil
}

// ListIDs returns the ids of every item currently in .ask/items/, derived
// from filenames. Order is filesystem-defined (i.e. unspecified); callers
// that need a deterministic order should sort, or use List which sorts by
// the spec §3.2 default ordering.
// A missing .ask/items/ directory is not an error: a store whose config
// exists but which has never been written to is legitimately empty, and
// reads must not create the directory to find that out (act-55ae5b).
func (s *FileStore) ListIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, itemsSubdir))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read items dir: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		// Skips directories, non-.json entries, and .tmp leftovers from an
		// interrupted atomic write.
		if !isItemFile(e) {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	return ids, nil
}

// SkippedItem names one item file List could not read, and why. Err is the
// error Load returned for that id: a JSON decode failure, a wrapped
// ErrUnsupportedSchemaVersion, a wrapped ErrIDMismatch, or an I/O error on
// the file itself. For a stray file, whose name is not a valid id, Err
// wraps ErrInvalidID and names the file (<ID>.json). ID is always the
// filename id (the name stem), never the file's own claim.
type SkippedItem struct {
	ID  string
	Err error
}

// String is the one wording for a skip, shared by the CLI's stderr warning
// and the MCP warning part (each prefixes `ask list: `). The id is stated
// explicitly rather than left to Err's own text, so the reader can always
// find it in the same place.
func (s SkippedItem) String() string {
	return fmt.Sprintf("skipped %s: %v", s.ID, s.Err)
}

// List returns every readable item in the store, sorted per spec §3.2:
//
//  1. urgency descending — blocker (0) before normal (1) before fyi (2).
//  2. created_at ascending (oldest first within a given urgency).
//  3. id ascending (lexical) as a stable tiebreaker.
//
// This is the order both plain-text `ask list` and `ask list --json` use.
// Filtering by status (default: hide closed) is the caller's responsibility;
// List returns every readable item on disk.
//
// An item file that cannot be read — corrupt JSON, or a schema_version
// this binary does not implement (spec §1.1.1) — does not fail the call.
// It is left out of the items and reported in skipped (sorted by id), so
// one bad file cannot lock the whole inbox (spec §7.2). List does not
// decide whether that is fatal: the caller applies the §7.2 rule (an
// error only when something was skipped and nothing was readable) and
// owes its reader a warning per skipped id. The returned error is
// reserved for failing to enumerate the store at all.
//
// A file that disappears between the directory read and its Load is not
// unreadable — it is gone, as if removed a moment earlier — and is
// dropped without a skip entry.
func (s *FileStore) List() (items []*Item, skipped []SkippedItem, err error) {
	ids, err := s.ListIDs()
	if err != nil {
		return nil, nil, err
	}
	out := make([]*Item, 0, len(ids))
	for _, id := range ids {
		if checkID(id) != nil {
			// A stray: its name is not an id, so the skip names the
			// file, in the words ResolvePrefix uses for the same file.
			skipped = append(skipped, SkippedItem{ID: id, Err: fmt.Errorf(
				"%w: stray file in .ask/items/ not named for an id: %s.json", ErrInvalidID, id)})
			continue
		}
		it, err := s.Load(id)
		if errors.Is(err, ErrIDNotFound) {
			continue
		}
		if err != nil {
			skipped = append(skipped, SkippedItem{ID: id, Err: err})
			continue
		}
		out = append(out, it)
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].ID < skipped[j].ID })
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
	return out, skipped, nil
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

// atomicWrite writes data to path by writing a temp file beside it and
// renaming that over path. The temp file is in the same directory so the
// rename is same-volume and atomic (spec §8). Its name is unique per write
// (<path>.<random>.tmp), so two writers of one path never share a temp
// file; it still ends in .tmp, which is what isItemFile skips.
func atomicWrite(path string, data []byte) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("temp name for %s: %w", path, err)
	}
	tmp := path + "." + hex.EncodeToString(suffix[:]) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
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
