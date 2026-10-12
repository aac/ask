package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Subscriptions live beside the items, one file per subscribed item:
// .ask/subs/<item-id>.json. The Item schema does not change; an item with
// no subscriptions has no file.
const subsSubdir = "subs"

// SubsSchemaVersion is the schema_version every subs file carries.
const SubsSchemaVersion = "1"

// MaxSubscriptions is the most subscriptions one item may hold.
const MaxSubscriptions = 32

// MaxSubArgs is the most arguments one subscription may carry.
const MaxSubArgs = 16

// MaxSubArgValue is the longest argument value, in bytes.
const MaxSubArgValue = 1024

// Events a subscription can name, in their canonical order. "any" on the
// command line is stored as all three.
const (
	EventResolve = "resolve"
	EventClose   = "close"
	EventReopen  = "reopen"
	EventAny     = "any"
)

var allEvents = []string{EventResolve, EventClose, EventReopen}

var subArgKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ErrSubNotFound is returned when a subscription id prefix matches no
// subscription on the item (CLI exit 3).
var ErrSubNotFound = errors.New("subscription not found")

// ErrSubAmbiguous is returned when a subscription id prefix matches more
// than one subscription on the item (CLI exit 4).
var ErrSubAmbiguous = errors.New("subscription id prefix is ambiguous")

// Subscription is one stored "when this item changes, run this handler".
type Subscription struct {
	ID      string            `json:"id"`
	Events  []string          `json:"events"`
	Handler string            `json:"handler"`
	Args    map[string]string `json:"args"`
	AddedAt time.Time         `json:"added_at"`
}

// Names reports whether the subscription names event.
func (s *Subscription) Names(event string) bool {
	for _, e := range s.Events {
		if e == event {
			return true
		}
	}
	return false
}

// sameAs reports whether two subscriptions are identical in what they do:
// same handler, same arguments, same event set.
func (s *Subscription) sameAs(o *Subscription) bool {
	if s.Handler != o.Handler || len(s.Events) != len(o.Events) || len(s.Args) != len(o.Args) {
		return false
	}
	for i := range s.Events {
		if s.Events[i] != o.Events[i] {
			return false
		}
	}
	for k, v := range s.Args {
		if ov, ok := o.Args[k]; !ok || ov != v {
			return false
		}
	}
	return true
}

// SubsFile is the on-disk shape of .ask/subs/<item-id>.json.
type SubsFile struct {
	SchemaVersion string         `json:"schema_version"`
	Item          string         `json:"item"`
	Subscriptions []Subscription `json:"subscriptions"`
}

// ParseEvents turns a comma-separated --on value into the canonical,
// de-duplicated event list (resolve, close, reopen order). "any" stands
// for all three.
func ParseEvents(s string) ([]string, error) {
	want := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		e := strings.TrimSpace(part)
		switch e {
		case EventResolve, EventClose, EventReopen:
			want[e] = true
		case EventAny:
			for _, a := range allEvents {
				want[a] = true
			}
		case "":
			return nil, fmt.Errorf("empty event in %q (want resolve, close, reopen or any)", s)
		default:
			return nil, fmt.Errorf("unknown event %q (want resolve, close, reopen or any)", e)
		}
	}
	out := make([]string, 0, len(allEvents))
	for _, e := range allEvents {
		if want[e] {
			out = append(out, e)
		}
	}
	return out, nil
}

// ValidateSubArgs checks a subscription's arguments: at most MaxSubArgs,
// each key matching `^[a-z][a-z0-9_]{0,31}$`, each value at most
// MaxSubArgValue bytes with no NUL. Arguments are data and are never
// interpreted.
func ValidateSubArgs(args map[string]string) error {
	if len(args) > MaxSubArgs {
		return fmt.Errorf("%d arguments, at most %d allowed", len(args), MaxSubArgs)
	}
	for k, v := range args {
		if !subArgKeyRe.MatchString(k) {
			return fmt.Errorf("invalid argument key %q (want ^[a-z][a-z0-9_]{0,31}$)", k)
		}
		if len(v) > MaxSubArgValue {
			return fmt.Errorf("argument %s: value is %d bytes, at most %d allowed", k, len(v), MaxSubArgValue)
		}
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("argument %s: value contains a NUL byte", k)
		}
	}
	return nil
}

// subsPath is the only place an item id becomes a subs file path; like
// itemPath it refuses any id not in the generated shape.
func (s *FileStore) subsPath(id string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.root, subsSubdir, id+".json"), nil
}

// ErrStraySubs is returned by LoadSubs, and by every write of a subs file,
// when the store's subs directory holds no <id>.json for the item but does
// hold an entry whose name matches it under case folding: ASK-AAAB.json
// against ask-aaab. On a case-insensitive filesystem that entry would be
// read as the item's subscriptions and written back under its own name
// (act-7c6b02). The message names each such file; nothing is read from it
// or written to it.
var ErrStraySubs = errors.New("stray file(s) in the way of the item's subs file, differing from its name only in case")

// straySubsNames picks, out of the names in a subs directory, the ones in
// the way of id's subs file: those matching <id>.json under case folding
// but not exactly. When <id>.json itself is among the names there are
// none, since the real file wins and the others (which only a
// case-sensitive filesystem can hold beside it) are left alone, as a
// stray in items/ is beside a real item.
func straySubsNames(names []string, id string) []string {
	want := id + ".json"
	var strays []string
	for _, n := range names {
		if n == want {
			return nil
		}
		if strings.EqualFold(n, want) {
			strays = append(strays, n)
		}
	}
	sort.Strings(strays)
	return strays
}

// checkSubsStray returns ErrStraySubs when a stray stands where id's subs
// file would be. It compares directory entry names and never probes the
// filesystem with the path, so the answer is the same whether or not the
// filesystem folds case. A missing subs directory holds no stray.
func (s *FileStore) checkSubsStray(id string) error {
	entries, err := os.ReadDir(filepath.Join(s.root, subsSubdir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s dir: %w", subsSubdir, err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	strays := straySubsNames(names, id)
	if len(strays) == 0 {
		return nil
	}
	for i, n := range strays {
		strays[i] = ".ask/" + subsSubdir + "/" + n
	}
	return fmt.Errorf("subscriptions of %s: %w: %s", id, ErrStraySubs, strings.Join(strays, ", "))
}

// LoadSubs reads the subscriptions of the item with full id. An item with
// no subs file has none: the result is an empty SubsFile, and nothing is
// created on disk. A file that does not parse, carries another
// schema_version, or names another item is an error, and so is a stray
// file in the way of the item's own (ErrStraySubs).
func (s *FileStore) LoadSubs(id string) (*SubsFile, error) {
	path, err := s.subsPath(id)
	if err != nil {
		return nil, fmt.Errorf("load subscriptions: %w", err)
	}
	if err := s.checkSubsStray(id); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &SubsFile{SchemaVersion: SubsSchemaVersion, Item: id, Subscriptions: []Subscription{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read subscriptions of %s: %w", id, err)
	}
	var f SubsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("subscriptions of %s corrupt: %w", id, err)
	}
	if f.SchemaVersion != SubsSchemaVersion {
		return nil, fmt.Errorf("subscriptions of %s: %w %q", id, ErrUnsupportedSchemaVersion, f.SchemaVersion)
	}
	if f.Item != id {
		return nil, fmt.Errorf("subscriptions of %s: %s.json names item %q", id, id, f.Item)
	}
	if f.Subscriptions == nil {
		f.Subscriptions = []Subscription{}
	}
	return &f, nil
}

// saveSubs writes f atomically, or removes the file when f holds no
// subscriptions. The caller holds the store lock. It refuses with
// ErrStraySubs rather than write into, or remove, a stray in the way.
func (s *FileStore) saveSubs(f *SubsFile) error {
	path, err := s.subsPath(f.Item)
	if err != nil {
		return fmt.Errorf("save subscriptions: %w", err)
	}
	if err := s.checkSubsStray(f.Item); err != nil {
		return err
	}
	if len(f.Subscriptions) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal subscriptions of %s: %w", f.Item, err)
	}
	dir := filepath.Join(s.root, subsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return atomicWrite(path, b)
}

// newSubID returns 8 lowercase hex characters not already used on f.
func newSubID(f *SubsFile) (string, error) {
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("subscription id: %w", err)
		}
		id := hex.EncodeToString(b[:])
		taken := false
		for _, sub := range f.Subscriptions {
			if sub.ID == id {
				taken = true
				break
			}
		}
		if !taken {
			return id, nil
		}
	}
}

// SubscribeResult is what Subscribe reports.
type SubscribeResult struct {
	// Item is the subscribed item as it was under the lock.
	Item *Item
	// Subscription is the stored subscription: the new one, or the
	// identical one already present when Existing is true.
	Subscription Subscription
	// Existing reports that an identical subscription was already stored
	// and nothing was added.
	Existing bool
}

// Subscribe stores a subscription on the item idOrPrefix resolves to, under
// the store lock. The caller has already checked the handler name, the
// handler and the arguments; events must come from ParseEvents. It never
// runs a handler.
//
// Errors: ErrIDNotFound / ErrIDAmbiguous when the id does not resolve;
// *RefusedError for a closed item or a subscription past MaxSubscriptions
// (nothing stored); ErrStoreLocked (wrapped) or I/O otherwise. An
// identical subscription is not stored twice: the existing one is returned
// with Existing set.
func (s *FileStore) Subscribe(idOrPrefix string, events []string, handler string, args map[string]string, now time.Time) (*SubscribeResult, error) {
	lock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer lock.Unlock()

	ids, err := s.ListIDs()
	if err != nil {
		return nil, err
	}
	full, err := ResolvePrefix(idOrPrefix, ids)
	if err != nil {
		return nil, err
	}
	it, err := s.Load(full)
	if err != nil {
		return nil, err
	}
	if it.Status == StatusClosed {
		return nil, &RefusedError{Err: fmt.Errorf("%s is closed; a closed item never changes again, so nothing was stored", it.ID)}
	}
	f, err := s.LoadSubs(full)
	if err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]string{}
	}
	sub := Subscription{Events: events, Handler: handler, Args: args, AddedAt: now.UTC()}
	for _, have := range f.Subscriptions {
		if have.sameAs(&sub) {
			return &SubscribeResult{Item: it, Subscription: have, Existing: true}, nil
		}
	}
	if len(f.Subscriptions) >= MaxSubscriptions {
		return nil, &RefusedError{Err: fmt.Errorf("%s already has %d subscriptions, the most one item may hold; nothing was stored", it.ID, MaxSubscriptions)}
	}
	if sub.ID, err = newSubID(f); err != nil {
		return nil, err
	}
	f.Subscriptions = append(f.Subscriptions, sub)
	if err := s.saveSubs(f); err != nil {
		return nil, err
	}
	return &SubscribeResult{Item: it, Subscription: sub}, nil
}

// Unsubscribe removes, under the store lock, the one subscription on the
// item idOrPrefix resolves to whose id starts with subPrefix. The subs file
// is removed with its last subscription. It returns the item id and the
// removed subscription.
//
// Errors: ErrIDNotFound / ErrIDAmbiguous for the item; ErrSubNotFound /
// ErrSubAmbiguous (wrapped) for the subscription; ErrStoreLocked or I/O
// otherwise.
func (s *FileStore) Unsubscribe(idOrPrefix, subPrefix string) (string, *Subscription, error) {
	lock, err := s.Lock()
	if err != nil {
		return "", nil, err
	}
	defer lock.Unlock()

	ids, err := s.ListIDs()
	if err != nil {
		return "", nil, err
	}
	full, err := ResolvePrefix(idOrPrefix, ids)
	if err != nil {
		return "", nil, err
	}
	f, err := s.LoadSubs(full)
	if err != nil {
		return "", nil, err
	}
	var matches []int
	for i, sub := range f.Subscriptions {
		if strings.HasPrefix(sub.ID, subPrefix) {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return full, nil, fmt.Errorf("%w: %s has no subscription %q", ErrSubNotFound, full, subPrefix)
	case 1:
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = f.Subscriptions[m].ID
		}
		return full, nil, fmt.Errorf("%w: %q matches %s", ErrSubAmbiguous, subPrefix, strings.Join(names, ", "))
	}
	removed := f.Subscriptions[matches[0]]
	f.Subscriptions = append(f.Subscriptions[:matches[0]], f.Subscriptions[matches[0]+1:]...)
	if err := s.saveSubs(f); err != nil {
		return full, nil, err
	}
	return full, &removed, nil
}
