package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestTransitionEvent(t *testing.T) {
	for _, tc := range []struct {
		from, to Status
		want     string
	}{
		{StatusOpen, StatusResolved, EventResolve},
		{StatusResolved, StatusOpen, EventReopen},
		{StatusOpen, StatusClosed, EventClose},
		{StatusResolved, StatusClosed, EventClose},
		{StatusOpen, StatusOpen, ""},
		{StatusResolved, StatusResolved, ""},
		{StatusClosed, StatusClosed, ""},
		{StatusClosed, StatusOpen, ""},
		{StatusClosed, StatusResolved, ""},
	} {
		if got := transitionEvent(tc.from, tc.to); got != tc.want {
			t.Errorf("transitionEvent(%s, %s) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
}

// fireTestStore is a store holding one open item with a subscription on
// each of the given event sets.
func fireTestStore(t *testing.T, eventSets ...[]string) (*FileStore, string) {
	t.Helper()
	root := t.TempDir()
	store, err := OpenStore(root, &ProjectConfig{ProjectID: "01TESTPROJECT0000000000000", DisplayName: "t", CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	it := NewItem("ask-1111", "fire", UrgencyNormal, StatusOpen, now)
	if err := store.Save(it); err != nil {
		t.Fatal(err)
	}
	for i, events := range eventSets {
		if _, err := store.Subscribe(it.ID, events, "h", map[string]string{"n": string(rune('a' + i))}, now); err != nil {
			t.Fatal(err)
		}
	}
	return store, it.ID
}

func readFireFile(t *testing.T, store *FileStore, id string) (started []FireStarted, results []FireResult) {
	t.Helper()
	f, err := os.Open(filepath.Join(store.Root(), firesSubdir, id+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var phase struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal(sc.Bytes(), &phase); err != nil {
			t.Fatalf("fire record line is not JSON: %v\n%s", err, sc.Text())
		}
		if phase.Phase == FirePhaseStarted {
			var s FireStarted
			if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
				t.Fatal(err)
			}
			started = append(started, s)
		} else {
			var r FireResult
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				t.Fatal(err)
			}
			results = append(results, r)
		}
	}
	return started, results
}

// TestFirePicksSubscriptionsByEvent: a status change records a started
// line for exactly the subscriptions whose events include it, stamped with
// the transition time the item itself carries.
func TestFirePicksSubscriptionsByEvent(t *testing.T) {
	store, id := fireTestStore(t, []string{EventResolve}, []string{EventClose}, []string{EventResolve, EventClose, EventReopen})
	now := time.Now().UTC()
	ch, err := store.Mutate(id, now, func(it *Item, now time.Time) error { return Resolve(it, "", now) })
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Fires) != 2 {
		t.Fatalf("resolve fired %d subscriptions, want 2 (the resolve one and the any one)", len(ch.Fires))
	}
	started, _ := readFireFile(t, store, id)
	if len(started) != 2 {
		t.Fatalf("%d started lines, want 2", len(started))
	}
	subs, err := store.LoadSubs(id)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []Subscription{subs.Subscriptions[0], subs.Subscriptions[2]} {
		s := started[i]
		if s.Sub != want.ID || s.Handler != "h" || s.Event != EventResolve || s.From != StatusOpen || s.To != StatusResolved {
			t.Errorf("started line %d: %+v", i, s)
		}
		if !s.At.Equal(*ch.Item.ResolvedAt) {
			t.Errorf("started line %d: at %s, item resolved_at %s", i, s.At, ch.Item.ResolvedAt)
		}
		if !hex8Re.MatchString(s.Fire) || s.V != FireRecordVersion {
			t.Errorf("started line %d: fire %q v %d", i, s.Fire, s.V)
		}
	}
	if started[0].Fire == started[1].Fire {
		t.Errorf("two fires share the id %s", started[0].Fire)
	}
}

// TestFireRunnerThatCannotStartIsRecordedByTheResolver: when the runner
// cannot be started at all, the process that changed the item writes the
// result line itself, and the change stands. Inside a test binary there is
// no ask to re-execute, which is exactly that case.
func TestFireRunnerThatCannotStartIsRecordedByTheResolver(t *testing.T) {
	store, id := fireTestStore(t, []string{EventResolve})
	ch, err := store.Mutate(id, time.Now().UTC(), func(it *Item, now time.Time) error { return Resolve(it, "", now) })
	if err != nil {
		t.Fatalf("a fire that cannot start failed the write: %v", err)
	}
	if !ch.Changed || ch.Item.Status != StatusResolved {
		t.Fatalf("change: %+v", ch)
	}
	started, results := readFireFile(t, store, id)
	if len(started) != 1 || len(results) != 1 {
		t.Fatalf("%d started and %d result lines, want one each", len(started), len(results))
	}
	r := results[0]
	if r.State != FireSpawnFailed || r.Fire != started[0].Fire || r.Sub != started[0].Sub || r.ExitCode != nil {
		t.Errorf("result line: %+v", r)
	}
	if r.Detail == nil || *r.Detail != "no runner inside a test binary" {
		t.Errorf("result detail: %v", r.Detail)
	}
}

// TestFireNothingWithoutAStatusChange: a no-op, a text edit and a status
// change nobody subscribed to all leave no fires directory behind.
func TestFireNothingWithoutAStatusChange(t *testing.T) {
	store, id := fireTestStore(t, []string{EventClose})
	now := time.Now().UTC()
	for name, fn := range map[string]func(it *Item, now time.Time) error{
		"reopen of an open item": func(it *Item, now time.Time) error { return Reopen(it, "", now) },
		"title edit":             func(it *Item, now time.Time) error { it.Title = "edited"; return nil },
		"unsubscribed resolve":   func(it *Item, now time.Time) error { return Resolve(it, "", now) },
	} {
		ch, err := store.Mutate(id, now, fn)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ch.Fires) != 0 {
			t.Errorf("%s fired %d subscriptions", name, len(ch.Fires))
		}
	}
	if _, err := os.Stat(filepath.Join(store.Root(), firesSubdir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".ask/fires exists though nothing fired (stat err %v)", err)
	}
}

// TestFireUnreadableSubsNeverFailTheWrite: a subs file that cannot be read
// fires nothing and leaves the status change in place.
func TestFireUnreadableSubsNeverFailTheWrite(t *testing.T) {
	store, id := fireTestStore(t, []string{EventResolve})
	if err := os.WriteFile(filepath.Join(store.Root(), subsSubdir, id+".json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, err := store.Mutate(id, time.Now().UTC(), func(it *Item, now time.Time) error { return Resolve(it, "", now) })
	if err != nil {
		t.Fatalf("resolve with an unreadable subs file: %v", err)
	}
	if !ch.Changed || len(ch.Fires) != 0 {
		t.Errorf("change: changed=%v fires=%d", ch.Changed, len(ch.Fires))
	}
	got, err := store.Load(id)
	if err != nil || got.Status != StatusResolved {
		t.Errorf("item after resolve: %+v, %v", got, err)
	}
}

func TestTailBufferKeepsTheLastBytes(t *testing.T) {
	tb := &tailBuffer{max: 8}
	for _, s := range []string{"abc", "defgh", "ij", "klmnopqrstuvwxyz", "12"} {
		if n, err := tb.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if got := tb.String(); got != "uvwxyz12" {
		t.Errorf("tail %q, want %q", got, "uvwxyz12")
	}
}

// TestFireRecordNeverFollowsASymlink: a symlink planted where the fire
// record, or the fires directory, should be is not written through. The
// status change still stands; the fire is simply not recorded or run.
func TestFireRecordNeverFollowsASymlink(t *testing.T) {
	resolve := func(it *Item, now time.Time) error { return Resolve(it, "", now) }

	t.Run("file", func(t *testing.T) {
		store, id := fireTestStore(t, []string{EventResolve})
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte("untouched\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(store.Root(), firesSubdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(dir, id+".jsonl")); err != nil {
			t.Fatal(err)
		}
		ch, err := store.Mutate(id, time.Now().UTC(), resolve)
		if err != nil || !ch.Changed || len(ch.Fires) != 0 {
			t.Fatalf("resolve over a symlinked record: change %+v, err %v", ch, err)
		}
		if b, _ := os.ReadFile(victim); string(b) != "untouched\n" {
			t.Errorf("the symlink's target was written: %q", b)
		}
	})

	t.Run("directory", func(t *testing.T) {
		store, id := fireTestStore(t, []string{EventResolve})
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(store.Root(), firesSubdir)); err != nil {
			t.Fatal(err)
		}
		ch, err := store.Mutate(id, time.Now().UTC(), resolve)
		if err != nil || !ch.Changed || len(ch.Fires) != 0 {
			t.Fatalf("resolve over a symlinked fires directory: change %+v, err %v", ch, err)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("the symlink's target directory was written: %v", entries)
		}
	})
}

// TestFireRecordIsOwnerOnly: a result line holds handler output, so the
// record is not readable by other users of the host.
func TestFireRecordIsOwnerOnly(t *testing.T) {
	store, id := fireTestStore(t, []string{EventResolve})
	if _, err := store.Mutate(id, time.Now().UTC(), func(it *Item, now time.Time) error { return Resolve(it, "", now) }); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(store.Root(), firesSubdir, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("fire record mode %o, want 600", perm)
	}
}

// TestFireRecordFileIsHeldToARegularOwnerOnlyFile: a record file that
// already exists with wider permissions is tightened on the next append,
// and something at the path that is not a regular file is refused without
// blocking the writer, which holds the store lock.
func TestFireRecordFileIsHeldToARegularOwnerOnlyFile(t *testing.T) {
	resolve := func(it *Item, now time.Time) error { return Resolve(it, "", now) }

	t.Run("existing wide file", func(t *testing.T) {
		store, id := fireTestStore(t, []string{EventResolve})
		dir := filepath.Join(store.Root(), firesSubdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if ch, err := store.Mutate(id, time.Now().UTC(), resolve); err != nil || len(ch.Fires) != 1 {
			t.Fatalf("resolve: change %+v, err %v", ch, err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("fire record mode %o after an append, want 600", perm)
		}
	})

	t.Run("fifo", func(t *testing.T) {
		store, id := fireTestStore(t, []string{EventResolve})
		dir := filepath.Join(store.Root(), firesSubdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(filepath.Join(dir, id+".jsonl"), 0o644); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		var ch *Change
		var err error
		go func() {
			ch, err = store.Mutate(id, time.Now().UTC(), resolve)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("resolve is blocked on a FIFO at the fire record path, holding the store lock")
		}
		if err != nil || !ch.Changed || len(ch.Fires) != 0 {
			t.Errorf("resolve over a FIFO: change %+v, err %v", ch, err)
		}
	})
}
