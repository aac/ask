package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHarvestRunningFire: a source fire with a started line and no result
// refuses the harvest while it is younger than FireRunningWindow, and
// does not once it is older (the runner died; the result will never come)
// or once its result line is there (act-afab33).
func TestHarvestRunningFire(t *testing.T) {
	started := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	const startedLine = `{"v":1,"fire":"cafbd927","sub":"6d69ca27","handler":"slow","event":"resolve","from":"open","to":"resolved","at":"2026-05-15T10:29:59.9Z","phase":"started","ts":"2026-05-15T10:30:00Z"}` + "\n"
	const resultLine = `{"v":1,"fire":"cafbd927","sub":"6d69ca27","phase":"result","state":"ok","exit_code":0,"detail":null,"duration_ms":2000,"ts":"2026-05-15T10:30:02Z","output":"done\n"}` + "\n"
	cases := []struct {
		name    string
		record  string
		after   time.Duration
		refused bool
	}{
		{"running", startedLine, 30 * time.Second, true},
		{"just started, clock behind", startedLine, -2 * time.Second, true},
		{"dead", startedLine, FireRunningWindow + time.Second, false},
		{"finished", startedLine + resultLine, 30 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func(old func() time.Time) { harvestNow = old }(harvestNow)
			harvestNow = func() time.Time { return started.Add(tc.after) }

			srcDir, tgtDir := t.TempDir(), t.TempDir()
			source := harvestSeed(t, srcDir, "01HXSOURCE000000000000000A",
				&Item{ID: "ask-3c89", Title: "slow", Urgency: UrgencyNormal, Status: StatusResolved, CreatedAt: started})
			target := harvestSeed(t, tgtDir, "01HXTARGET000000000000000B")
			if err := os.MkdirAll(filepath.Join(srcDir, ".ask", "fires"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(srcDir, ".ask", "fires", "ask-3c89.jsonl"), []byte(tc.record), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := Harvest(target, source, true)
			if !tc.refused {
				if err != nil {
					t.Fatalf("Harvest: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrFireRunning) {
				t.Fatalf("Harvest: %v, want ErrFireRunning", err)
			}
			if msg := err.Error(); !strings.Contains(msg, "ask-3c89 (fire cafbd927, handler slow") {
				t.Errorf("refusal does not name the item and fire: %s", msg)
			}
			if _, err := os.Stat(filepath.Join(tgtDir, ".ask", "items", "ask-3c89.json")); !os.IsNotExist(err) {
				t.Errorf("refused harvest wrote the target item: %v", err)
			}
			if _, err := os.Stat(filepath.Join(srcDir, ".ask", "fires", "ask-3c89.jsonl")); err != nil {
				t.Errorf("refused harvest removed the source record: %v", err)
			}
		})
	}
}

// TestHarvestRunningFireHandlerNameIsKeptToItsLine: the handler name in a
// refusal comes from a fire record, a file on disk that may have been
// harvested from elsewhere. An escape sequence, a line break or a
// direction override in it does not reach the terminal.
func TestHarvestRunningFireHandlerNameIsKeptToItsLine(t *testing.T) {
	started := time.Date(2026, 5, 15, 10, 30, 0, 0, time.UTC)
	defer func(old func() time.Time) { harvestNow = old }(harvestNow)
	harvestNow = func() time.Time { return started.Add(10 * time.Second) }
	const line = `{"v":1,"fire":"cafbd927","sub":"6d69ca27","handler":"slow\u001b[2J\n  forged\u202e` + `xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx` + `","event":"resolve","from":"open","to":"resolved","at":"2026-05-15T10:29:59.9Z","phase":"started","ts":"2026-05-15T10:30:00Z"}` + "\n"

	srcDir, tgtDir := t.TempDir(), t.TempDir()
	source := harvestSeed(t, srcDir, "01HXSOURCE000000000000000A",
		&Item{ID: "ask-3c89", Title: "slow", Urgency: UrgencyNormal, Status: StatusResolved, CreatedAt: started})
	target := harvestSeed(t, tgtDir, "01HXTARGET000000000000000B")
	if err := os.MkdirAll(filepath.Join(srcDir, ".ask", "fires"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".ask", "fires", "ask-3c89.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Harvest(target, source, false)
	if !errors.Is(err, ErrFireRunning) {
		t.Fatalf("Harvest: %v, want ErrFireRunning", err)
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\x1b\n\u202e") {
		t.Errorf("refusal carries what the record planted: %q", msg)
	}
	if !strings.Contains(msg, `handler "slow\x1b[2J\n  forged\u202exxx`) || !strings.Contains(msg, `xxx...", started 10 s ago)`) {
		t.Errorf("refusal does not quote and cut the handler name: %q", msg)
	}
}
