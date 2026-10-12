package core

import (
	"path/filepath"
	"time"
)

// RunnerVerb is the hidden CLI verb a fire re-executes ask as.
const RunnerVerb = "__fire"

// RunnerProtocol is the version word that follows RunnerVerb. The process
// that changes an item and the runner it starts can be different builds: a
// long-lived server re-executes whatever file is at its own path, which an
// upgrade may have replaced. A runner handed a version it does not know
// records spawn_failed with FireDetailProtocolMismatch instead of
// guessing. The item, subscription and fire ids keep their positions in
// every version so that record can always be written.
const RunnerProtocol = "v1"

// transitionEvent names the event a status change is, or "" when the
// change is not one a subscription can name: resolve for open to
// resolved, close for anything to closed, reopen for resolved to open.
func transitionEvent(from, to Status) string {
	switch {
	case from == to:
		return ""
	case to == StatusClosed:
		return EventClose
	case from == StatusOpen && to == StatusResolved:
		return EventResolve
	case from == StatusResolved && to == StatusOpen:
		return EventReopen
	}
	return ""
}

// recordFires is the firing decision. Mutate calls it under the store
// lock, after the save of a real status change: it picks the subscriptions
// whose events include this one and appends one started line per
// subscription to the item's fires file. It returns the fires whose
// started line is on disk; the caller starts their runners once the lock
// is released.
//
// Nothing here can fail or alter the write. An unreadable subs file fires
// nothing, and a subscription whose started line cannot be written is
// skipped, since a fire that cannot be recorded should not run.
func (s *FileStore) recordFires(it *Item, from Status, at time.Time) []FireStarted {
	event := transitionEvent(from, it.Status)
	if event == "" {
		return nil
	}
	f, err := s.LoadSubs(it.ID)
	if err != nil {
		return nil
	}
	var fires []FireStarted
	for _, sub := range f.Subscriptions {
		if !sub.Names(event) {
			continue
		}
		id, err := newFireID()
		if err != nil {
			continue
		}
		line := FireStarted{
			V:       FireRecordVersion,
			Fire:    id,
			Sub:     sub.ID,
			Handler: sub.Handler,
			Event:   event,
			From:    from,
			To:      it.Status,
			At:      at.UTC(),
			Phase:   FirePhaseStarted,
			Ts:      time.Now().UTC(),
		}
		if err := s.appendFireLine(it.ID, line); err != nil {
			continue
		}
		fires = append(fires, line)
	}
	return fires
}

// startFires starts one detached runner per recorded fire. It is called
// with the store lock released and does not wait for any of them. A runner
// that cannot be started gets its result line here: spawn_failed, with the
// operating system's error as the detail.
func (s *FileStore) startFires(itemID string, fires []FireStarted) {
	projectRoot := filepath.Dir(s.root)
	for _, f := range fires {
		began := time.Now()
		argv := []string{
			RunnerVerb, RunnerProtocol, itemID, f.Sub, f.Fire,
			f.Event, string(f.From), string(f.To), f.At.Format(time.RFC3339Nano),
		}
		if err := startRunner(projectRoot, argv); err != nil {
			_ = s.appendFireResult(itemID, f.Fire, f.Sub, FireSpawnFailed, nil, err.Error(), began, "")
		}
	}
}
