package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Change is what Mutate reports about one write-path call.
type Change struct {
	// Item is the item after fn ran: what was saved, or the unchanged item
	// when Changed is false.
	Item *Item
	// From is the item's status before fn ran.
	From Status
	// Changed reports whether fn altered the item. An unaltered item is not
	// re-saved, so a no-op never touches the file.
	Changed bool
	// Fires lists the fires this change started, one per subscription
	// whose events include the status change. Each has its started line
	// in the item's fires file and its own detached runner; none has been
	// waited for.
	Fires []FireStarted
}

// RefusedError wraps an error returned by a Mutate callback: the item was
// loaded and the requested change was refused (an invalid transition, for
// instance). Nothing was written. The CLI maps it to exit 2 and MCP to
// code 2. Its text is the wrapped error's, unprefixed.
type RefusedError struct {
	Err error
}

func (e *RefusedError) Error() string { return e.Err.Error() }
func (e *RefusedError) Unwrap() error { return e.Err }

// Mutate is the one write path for an existing item, shared by the CLI and
// the MCP server. Under the store lock it resolves idOrPrefix against the
// items on disk, loads the item, runs fn on it, and saves the result if fn
// changed anything. Holding the lock across the whole sequence is what
// keeps two writers of one item from overwriting each other (spec §8).
//
// now is the command's single clock reading; it is handed to fn so a
// transition stamps the same instant the caller reports.
//
// Errors, by what the caller should do with them:
//   - ErrIDNotFound / ErrIDAmbiguous: the id did not resolve (exit 3 / 4).
//   - *RefusedError: fn refused the change (exit 2).
//   - ErrStoreLocked (wrapped): the lock timed out (exit 5).
//   - anything else: I/O (exit 5).
//
// Nothing is written on any error path.
//
// A saved status change also fires the item's subscriptions (see
// recordFires and startFires): the started lines are appended under the
// lock, and the runners are started after it is released and never waited
// for. A change that leaves the status alone, and a no-op, fire nothing.
// Firing cannot fail or alter the write, and a handler is never retried.
func (s *FileStore) Mutate(idOrPrefix string, now time.Time, fn func(it *Item, now time.Time) error) (*Change, error) {
	ch, err := s.mutateLocked(idOrPrefix, now, fn)
	if err != nil {
		return nil, err
	}
	// The lock is released: start the handlers this change fires.
	s.startFires(ch.Item.ID, ch.Fires)
	return ch, nil
}

// mutateLocked is the locked half of Mutate: everything up to and
// including the started lines of the fires the change causes.
func (s *FileStore) mutateLocked(idOrPrefix string, now time.Time, fn func(it *Item, now time.Time) error) (*Change, error) {
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
	before, err := json.Marshal(it)
	if err != nil {
		return nil, fmt.Errorf("marshal item %s: %w", it.ID, err)
	}
	from := it.Status
	if err := fn(it, now); err != nil {
		return nil, &RefusedError{Err: err}
	}
	after, err := json.Marshal(it)
	if err != nil {
		return nil, fmt.Errorf("marshal item %s: %w", it.ID, err)
	}
	if bytes.Equal(before, after) {
		return &Change{Item: it, From: from}, nil
	}
	if err := s.Save(it); err != nil {
		return nil, err
	}
	return &Change{Item: it, From: from, Changed: true, Fires: s.recordFires(it, from, now)}, nil
}

// lockStores takes the locks of two distinct stores in path order, so two
// processes that each need both can never hold one apiece. The returned
// function releases both.
func lockStores(a, b *FileStore) (func(), error) {
	if a.root == b.root {
		l, err := a.Lock()
		if err != nil {
			return nil, err
		}
		return l.Unlock, nil
	}
	first, second := a, b
	if second.root < first.root {
		first, second = second, first
	}
	l1, err := first.Lock()
	if err != nil {
		return nil, err
	}
	l2, err := second.Lock()
	if err != nil {
		l1.Unlock()
		return nil, err
	}
	return func() {
		l2.Unlock()
		l1.Unlock()
	}, nil
}
