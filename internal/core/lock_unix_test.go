//go:build unix

package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// shortenLockTimeout swaps LockTimeout for the duration of a test so a
// held lock is reported in milliseconds rather than the production 10 s.
func shortenLockTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := LockTimeout
	LockTimeout = d
	t.Cleanup(func() { LockTimeout = orig })
}

// TestLockTimeout holds the store lock on one descriptor and drives a write
// through Mutate on another. The write must come back with ErrStoreLocked,
// worded as the CLI prints it, and must leave the item file byte-identical.
func TestLockTimeout(t *testing.T) {
	s := tempStore(t)
	item := NewItem("ask-3c89", "x", UrgencyNormal, StatusOpen, time.Now().UTC())
	if err := s.Save(item); err != nil {
		t.Fatal(err)
	}
	path := s.itemPath(item.ID)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	held, err := s.Lock()
	if err != nil {
		t.Fatalf("take lock: %v", err)
	}
	shortenLockTimeout(t, 100*time.Millisecond)

	ran := false
	start := time.Now()
	_, err = s.Mutate(item.ID, time.Now().UTC(), func(it *Item, now time.Time) error {
		ran = true
		return Resolve(it, "", now)
	})
	waited := time.Since(start)
	if !errors.Is(err, ErrStoreLocked) {
		t.Fatalf("Mutate with the lock held: err = %v, want ErrStoreLocked", err)
	}
	want := "store is locked by another ask process (waited 100ms); nothing was written"
	if err.Error() != want {
		t.Fatalf("error text:\n got %q\nwant %q", err.Error(), want)
	}
	if ran {
		t.Fatal("the change ran without the lock")
	}
	if waited < 100*time.Millisecond {
		t.Fatalf("gave up after %s, before the 100ms timeout", waited)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("item file changed under a timed-out write:\nbefore %s\nafter  %s", before, after)
	}

	// Released, the same write goes through: the refusal above was the
	// lock, not something else about this item.
	held.Unlock()
	ch, err := s.Mutate(item.ID, time.Now().UTC(), func(it *Item, now time.Time) error {
		return Resolve(it, "", now)
	})
	if err != nil {
		t.Fatalf("Mutate after unlock: %v", err)
	}
	if !ch.Changed || ch.Item.Status != StatusResolved {
		t.Fatalf("after unlock: changed=%v status=%s", ch.Changed, ch.Item.Status)
	}
}

// TestAtomicWriteConcurrentWritersNeverCollide runs fifty unlocked writers
// of one path, each with its own payload. With a temp name shared between
// writers, one writer's rename can take the temp file out from under
// another (a failed call), or two can interleave in it (a torn file).
func TestAtomicWriteConcurrentWritersNeverCollide(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	const writers = 50

	payloads := make([][]byte, writers)
	for i := range payloads {
		// Different lengths as well as contents, so an interleaving of two
		// payloads cannot happen to equal a third.
		payloads[i] = []byte(fmt.Sprintf("writer-%02d:%s\n", i, strings.Repeat(string(rune('a'+i%26)), 4096+i)))
	}

	start := make(chan struct{})
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = atomicWrite(path, payloads[i])
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d: %v", i, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	whole := false
	for _, p := range payloads {
		if bytes.Equal(got, p) {
			whole = true
			break
		}
	}
	if !whole {
		t.Errorf("file is not exactly one writer's payload (%d bytes, starts %q)", len(got), got[:min(len(got), 24)])
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "target.json" {
			t.Errorf("leftover file %s", e.Name())
		}
	}
}

// TestHarvestTakesBothLocks holds first the source's lock, then the
// target's, and checks Harvest refuses each time without copying anything.
func TestHarvestTakesBothLocks(t *testing.T) {
	target, source := tempStore(t), tempStore(t)
	if err := source.Save(NewItem("ask-aaaa", "x", UrgencyNormal, StatusOpen, time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	shortenLockTimeout(t, 100*time.Millisecond)

	for _, tc := range []struct {
		name string
		held *FileStore
	}{{"source", source}, {"target", target}} {
		lock, err := tc.held.Lock()
		if err != nil {
			t.Fatalf("take %s lock: %v", tc.name, err)
		}
		_, err = Harvest(target, source, true)
		lock.Unlock()
		if !errors.Is(err, ErrStoreLocked) {
			t.Fatalf("Harvest with the %s lock held: err = %v, want ErrStoreLocked", tc.name, err)
		}
		if ids, _ := target.ListIDs(); len(ids) != 0 {
			t.Fatalf("Harvest with the %s lock held copied %v", tc.name, ids)
		}
		if ids, _ := source.ListIDs(); len(ids) != 1 {
			t.Fatalf("Harvest with the %s lock held removed the source item", tc.name)
		}
	}

	res, err := Harvest(target, source, true)
	if err != nil {
		t.Fatalf("Harvest with both locks free: %v", err)
	}
	if len(res.Harvested) != 1 {
		t.Fatalf("harvested %v, want one item", res.Harvested)
	}
}
