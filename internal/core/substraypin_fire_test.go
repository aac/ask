package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSubsStrayFireStatusChangeFiresNothing: an item with a subs stray in
// the way goes through every status change. Each one succeeds and stands,
// fires nothing and records nothing, and the stray is left as it was. The
// stray subscribes to every event, so reading it as the item's would fire.
func TestSubsStrayFireStatusChangeFiresNothing(t *testing.T) {
	base := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	for _, stray := range []string{"ASK-AAAB.json", "ask-aaab.JSON", "Ask-aaab.json"} {
		s := harvestSeed(t, t.TempDir(), "01HXMAIN0000000000000000A",
			NewItem("ask-aaab", "has a stray", UrgencyNormal, StatusOpen, base))
		planted := substraypinPlant(t, s, stray)

		for _, step := range []struct {
			name string
			fn   func(it *Item, now time.Time) error
			want Status
		}{
			{"resolve", func(it *Item, now time.Time) error { return Resolve(it, "", now) }, StatusResolved},
			{"reopen", func(it *Item, now time.Time) error { return Reopen(it, "", now) }, StatusOpen},
			{"close", func(it *Item, now time.Time) error { return Close(it, "", now) }, StatusClosed},
		} {
			ch, err := s.Mutate("ask-aaab", base.Add(time.Hour), step.fn)
			if err != nil {
				t.Fatalf("%s: %s beside a subs stray failed: %v", stray, step.name, err)
			}
			if !ch.Changed || ch.Item.Status != step.want {
				t.Errorf("%s: %s: changed=%v status=%s, want %s", stray, step.name, ch.Changed, ch.Item.Status, step.want)
			}
			if len(ch.Fires) != 0 {
				t.Errorf("%s: %s fired %d subscriptions: %+v", stray, step.name, len(ch.Fires), ch.Fires)
			}
			if got, err := s.Load("ask-aaab"); err != nil || got.Status != step.want {
				t.Errorf("%s: item after %s: %+v, %v", stray, step.name, got, err)
			}
			if started, results := readFireFile(t, s, "ask-aaab"); len(started) != 0 || len(results) != 0 {
				t.Errorf("%s: %s recorded %d started and %d result lines", stray, step.name, len(started), len(results))
			}
		}
		if _, err := os.Stat(filepath.Join(s.Root(), firesSubdir)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: .ask/fires exists though nothing fired (stat err %v)", stray, err)
		}
		if b, err := os.ReadFile(filepath.Join(s.Root(), subsSubdir, stray)); err != nil || string(b) != planted {
			t.Errorf("%s: stray was touched: %q, %v", stray, b, err)
		}
	}
}
