package cli

// Tests for the reads of a fire: `ask fires`, and the lines `ask show`
// prints under each subscription. The ones that need a handler to really
// run drive a built ask as a subprocess (see fire_boundary_test.go); the
// rest read a fire record written here as a fixture.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// firesFixedNow is the clock the fixture tests read ages against.
var firesFixedNow = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)

// firesClock pins the clock unfinished fires are aged against.
func firesClock(t *testing.T, now time.Time) {
	t.Helper()
	orig := firesNow
	firesNow = func() time.Time { return now }
	t.Cleanup(func() { firesNow = orig })
}

// firesSubscribed seeds a store with one open item subscribed to events
// and returns the directory, the item id and the stored subscription.
func firesSubscribed(t *testing.T, events string) (string, string, core.Subscription) {
	t.Helper()
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusOpen)
	if code, _, errOut := subsRun(t, "subscribe", id, "--on", events, "--handler", "rec"); code != 0 {
		t.Fatalf("subscribe: exit %d: %s", code, errOut)
	}
	return dir, id, subsRead(t, dir, id).Subscriptions[0]
}

// firesStartedLine and firesResultLine are record lines in the shape the
// firing code writes.
func firesStartedLine(fire, sub, event, from, to string, at, ts time.Time) string {
	return fmt.Sprintf(`{"v":1,"fire":%q,"sub":%q,"handler":"rec","event":%q,"from":%q,"to":%q,"at":%q,"phase":"started","ts":%q}`,
		fire, sub, event, from, to, at.Format(time.RFC3339Nano), ts.Format(time.RFC3339Nano))
}

func firesResultLine(fire, sub, state, exitCode, detail string, ms int, ts time.Time, output string) string {
	if detail != "null" {
		d, _ := json.Marshal(detail)
		detail = string(d)
	}
	out, _ := json.Marshal(output)
	return fmt.Sprintf(`{"v":1,"fire":%q,"sub":%q,"phase":"result","state":%q,"exit_code":%s,"detail":%s,"duration_ms":%d,"ts":%q,"output":%s}`,
		fire, sub, state, exitCode, detail, ms, ts.Format(time.RFC3339Nano), out)
}

// firesWrite writes an item's fire record.
func firesWrite(t *testing.T, dir, id string, lines ...string) {
	t.Helper()
	fdir := filepath.Join(dir, ".ask", "fires")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fdir, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// firesShowLines returns the lines `ask show` prints under the item's one
// subscription, without their indentation.
func firesShowLines(t *testing.T, id string) []string {
	t.Helper()
	code, out, errOut := subsRun(t, "show", id)
	if code != 0 {
		t.Fatalf("show: exit %d: %s", code, errOut)
	}
	_, section, ok := strings.Cut(out, "\nSubscriptions:\n")
	if !ok {
		t.Fatalf("show has no Subscriptions section:\n%s", out)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(section, "\n"), "\n")[1:] {
		rest, ok := strings.CutPrefix(l, "      ")
		if !ok {
			t.Fatalf("line under a subscription is not indented six spaces: %q\n%s", l, out)
		}
		lines = append(lines, rest)
	}
	return lines
}

// TestShowFireStates builds each state from a fixture fires file and
// matches the line `ask show` prints for it.
func TestShowFireStates(t *testing.T) {
	firesClock(t, firesFixedNow)
	at := time.Date(2026, 3, 4, 11, 0, 0, 123456789, time.UTC)
	const atText = "2026-03-04T11:00:00Z"
	ago := func(d time.Duration) time.Time { return firesFixedNow.Add(-d) }

	for _, tc := range []struct {
		name   string
		record func(sub string) []string
		want   []string
	}{
		{"none", nil, []string{"not fired yet: no resolve, close or reopen since this was added"}},
		{"ok", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, at),
				firesResultLine("aaaaaaaa", sub, "ok", "0", "null", 400, at, "HANDLER-SAID-THIS\n"),
			}
		}, []string{atText + "  resolve   ok, exit 0 in 0.4 s"}},
		{"failed", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "close", "open", "closed", at, at),
				firesResultLine("aaaaaaaa", sub, "failed", "22", "null", 7200, at, "boom"),
			}
		}, []string{atText + "  close     failed, exit 22 in 7.2 s"}},
		{"failed on a signal", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, at),
				firesResultLine("aaaaaaaa", sub, "failed", "null", "signal: killed", 149, at, ""),
			}
		}, []string{atText + "  resolve   failed, signal: killed in 0.1 s"}},
		{"no handler", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, at),
				firesResultLine("aaaaaaaa", sub, "not_run", "null", "no handler named rec", 1, at, ""),
			}
		}, []string{atText + "  resolve   not run: no handler named rec on this machine"}},
		{"not runnable", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, at),
				firesResultLine("aaaaaaaa", sub, "not_run", "null", "not runnable", 1, at, ""),
			}
		}, []string{atText + "  resolve   not run: the handler rec is not an executable file on this machine"}},
		{"spawn failed", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "reopen", "resolved", "open", at, at),
				firesResultLine("aaaaaaaa", sub, "spawn_failed", "null", "runner protocol mismatch", 0, at, ""),
			}
		}, []string{atText + "  reopen    not run: could not be started: runner protocol mismatch"}},
		{"a detail is kept to its line", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, at),
				firesResultLine("aaaaaaaa", sub, "spawn_failed", "null", "fork\n      2026-01-01T00:00:00Z  resolve   ok, exit 0 in 0.1 s\x1b[2J", 0, at, ""),
			}
		}, []string{atText + "  resolve   not run: could not be started: fork       2026-01-01T00:00:00Z  resolve   ok, exit 0 in 0.1 s [2J"}},
		{"invisible characters in a detail do not reach the line", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, at),
				firesResultLine("aaaaaaaa", sub, "spawn_failed", "null", "a\u202eb\u2066c\u200bd\u2028e\u009bf", 0, at, ""),
			}
		}, []string{atText + "  resolve   not run: could not be started: a b c d e f"}},
		{"timeout", func(sub string) []string {
			return []string{
				firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, at),
				firesResultLine("aaaaaaaa", sub, "timeout", "null", "null", 62011, at, "partial"),
			}
		}, []string{atText + "  resolve   killed at the 60 s timeout"}},
		{"started, young", func(sub string) []string {
			return []string{firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, ago(12*time.Second))}
		}, []string{atText + "  resolve   started, no result after 12 s (it is running)"}},
		{"started, just under the window", func(sub string) []string {
			return []string{firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, ago(89*time.Second))}
		}, []string{atText + "  resolve   started, no result after 89 s (it is running)"}},
		{"started, at the window", func(sub string) []string {
			return []string{firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, ago(90*time.Second))}
		}, []string{atText + "  resolve   started, no result after 90 s (it died; it is not retried)"}},
		{"started, old", func(sub string) []string {
			return []string{firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", at, ago(3*time.Hour+5*time.Minute))}
		}, []string{atText + "  resolve   started, no result after 3 h (it died; it is not retried)"}},
		{"the last three of five, oldest first, another subscription's left out", func(sub string) []string {
			var lines []string
			for i := 0; i < 5; i++ {
				fire := fmt.Sprintf("%08x", i)
				when := at.Add(time.Duration(i) * time.Minute)
				lines = append(lines,
					firesStartedLine(fire, sub, "resolve", "open", "resolved", when, when),
					firesResultLine(fire, sub, "failed", fmt.Sprint(i), "null", 1000, when, ""))
			}
			late := at.Add(time.Hour)
			return append(lines,
				firesStartedLine("ffffffff", "0badf00d", "resolve", "open", "resolved", late, late),
				firesResultLine("ffffffff", "0badf00d", "ok", "0", "null", 5, late, ""))
		}, []string{
			"2026-03-04T11:02:00Z  resolve   failed, exit 2 in 1.0 s",
			"2026-03-04T11:03:00Z  resolve   failed, exit 3 in 1.0 s",
			"2026-03-04T11:04:00Z  resolve   failed, exit 4 in 1.0 s",
		}},
		{"a time with an offset is printed in UTC", func(sub string) []string {
			east := at.In(time.FixedZone("east", 5*3600))
			return []string{
				firesStartedLine("aaaaaaaa", sub, "resolve", "open", "resolved", east, east),
				firesResultLine("aaaaaaaa", sub, "ok", "0", "null", 400, east, ""),
			}
		}, []string{atText + "  resolve   ok, exit 0 in 0.4 s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, id, sub := firesSubscribed(t, "any")
			if tc.record != nil {
				firesWrite(t, dir, id, tc.record(sub.ID)...)
			}
			if got := firesShowLines(t, id); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("show lines:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestShowMissedFire resolves a subscribed item the way a build that does
// not fire does: the item file gets its new status and time, and nothing
// is written to the fire record.
func TestShowMissedFire(t *testing.T) {
	firesClock(t, firesFixedNow)
	// rewrite stamps the item as such a build would.
	rewrite := func(t *testing.T, dir, id string, fn func(it *core.Item)) {
		t.Helper()
		it := loadItem(t, dir, id)
		fn(it)
		seedStore(t, dir, it)
	}
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }

	t.Run("resolved after the subscription", func(t *testing.T) {
		dir, id, sub := firesSubscribed(t, "any")
		when := sub.AddedAt.Add(time.Minute)
		rewrite(t, dir, id, func(it *core.Item) { it.Status, it.ResolvedAt = core.StatusResolved, &when })
		want := []string{"missed: resolve at " + stamp(when) + " by a build that does not fire; not retried"}
		if got := firesShowLines(t, id); !reflect.DeepEqual(got, want) {
			t.Errorf("show lines:\n got %q\nwant %q", got, want)
		}
		if _, err := os.Stat(filepath.Join(dir, ".ask", "fires")); !os.IsNotExist(err) {
			t.Errorf("the fixture wrote a fire record, or show created one: %v", err)
		}
	})

	t.Run("resolved then closed, both missed", func(t *testing.T) {
		dir, id, sub := firesSubscribed(t, "any")
		resolved, closed := sub.AddedAt.Add(time.Minute), sub.AddedAt.Add(2*time.Minute)
		rewrite(t, dir, id, func(it *core.Item) {
			it.Status, it.ResolvedAt, it.ClosedAt = core.StatusClosed, &resolved, &closed
		})
		want := []string{
			"missed: resolve at " + stamp(resolved) + " by a build that does not fire; not retried",
			"missed: close at " + stamp(closed) + " by a build that does not fire; not retried",
		}
		if got := firesShowLines(t, id); !reflect.DeepEqual(got, want) {
			t.Errorf("show lines:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("resolved before the subscription is not a miss", func(t *testing.T) {
		dir, id, sub := firesSubscribed(t, "any")
		when := sub.AddedAt.Add(-time.Minute)
		rewrite(t, dir, id, func(it *core.Item) { it.Status, it.ResolvedAt = core.StatusResolved, &when })
		want := []string{"not fired yet: no resolve, close or reopen since this was added"}
		if got := firesShowLines(t, id); !reflect.DeepEqual(got, want) {
			t.Errorf("show lines:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("an event the subscription does not name is not a miss", func(t *testing.T) {
		dir, id, sub := firesSubscribed(t, "close,reopen")
		when := sub.AddedAt.Add(time.Minute)
		rewrite(t, dir, id, func(it *core.Item) { it.Status, it.ResolvedAt = core.StatusResolved, &when })
		want := []string{"not fired yet: no close or reopen since this was added"}
		if got := firesShowLines(t, id); !reflect.DeepEqual(got, want) {
			t.Errorf("show lines:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("a fire recorded for that change is not a miss", func(t *testing.T) {
		dir, id, sub := firesSubscribed(t, "any")
		when := sub.AddedAt.Add(time.Minute)
		rewrite(t, dir, id, func(it *core.Item) { it.Status, it.ResolvedAt = core.StatusResolved, &when })
		firesWrite(t, dir, id,
			firesStartedLine("aaaaaaaa", sub.ID, "resolve", "open", "resolved", when, when),
			firesResultLine("aaaaaaaa", sub.ID, "ok", "0", "null", 300, when, ""))
		want := []string{stamp(when) + "  resolve   ok, exit 0 in 0.3 s"}
		if got := firesShowLines(t, id); !reflect.DeepEqual(got, want) {
			t.Errorf("show lines:\n got %q\nwant %q", got, want)
		}
	})

	t.Run("an earlier resolve fired, the current one was missed", func(t *testing.T) {
		dir, id, sub := firesSubscribed(t, "any")
		first, second := sub.AddedAt.Add(time.Minute), sub.AddedAt.Add(time.Hour)
		rewrite(t, dir, id, func(it *core.Item) { it.Status, it.ResolvedAt = core.StatusResolved, &second })
		firesWrite(t, dir, id,
			firesStartedLine("aaaaaaaa", sub.ID, "resolve", "open", "resolved", first, first),
			firesResultLine("aaaaaaaa", sub.ID, "ok", "0", "null", 300, first, ""))
		want := []string{
			stamp(first) + "  resolve   ok, exit 0 in 0.3 s",
			"missed: resolve at " + stamp(second) + " by a build that does not fire; not retried",
		}
		if got := firesShowLines(t, id); !reflect.DeepEqual(got, want) {
			t.Errorf("show lines:\n got %q\nwant %q", got, want)
		}
	})
}

// TestShowNeverPrintsHandlerOutput runs a real handler that prints a
// marker and fails. `ask show` states the failure without the marker;
// `ask fires` has it, in text without the escape sequence the handler
// printed and in JSON as stored.
func TestShowNeverPrintsHandlerOutput(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	// The second line carries an escape sequence and, as UTF-8 bytes in
	// octal, a right-to-left override (U+202E).
	w.install("loud", "#!/bin/sh\nprintf 'HANDLER-MARKER one\\n\\033[31mHANDLER-MARKER\\342\\200\\256two\\n'\nexit 3\n")
	id := w.newItem("loud one")
	w.subscribe(id, "resolve", "loud")
	w.mustRun(0, "resolve", id)
	w.waitResults(id, 1)

	show := w.mustRun(0, "show", id)
	if !regexp.MustCompile(`\n      \S+Z  resolve   failed, exit 3 in \d+\.\d s\n`).MatchString(show) {
		t.Errorf("show does not state the failure:\n%s", show)
	}
	if strings.Contains(show, "MARKER") {
		t.Errorf("show printed handler output:\n%s", show)
	}
	if out := w.mustRun(0, "show", id, "--json"); strings.Contains(out, "MARKER") || strings.Contains(out, "failed") {
		t.Errorf("show --json carries the fire:\n%s", out)
	}

	text := w.mustRun(0, "fires", id)
	if !strings.Contains(text, "  output:\n    HANDLER-MARKER one\n     [31mHANDLER-MARKER two\n") {
		t.Errorf("fires does not print the stored output indented:\n%q", text)
	}
	if strings.ContainsRune(text, 0x1b) || strings.ContainsRune(text, 0x202e) {
		t.Errorf("fires printed an escape character or a direction override:\n%q", text)
	}
	var got []core.Fire
	if err := json.Unmarshal([]byte(w.mustRun(0, "fires", id, "--json")), &got); err != nil || len(got) != 1 {
		t.Fatalf("fires --json: %v, %d fires", err, len(got))
	}
	if want := "HANDLER-MARKER one\n\x1b[31mHANDLER-MARKER\u202etwo\n"; got[0].Output != want {
		t.Errorf("fires --json output = %q, want %q", got[0].Output, want)
	}
}

// firesKeysRe is the key order of one element of `ask fires --json`.
var firesKeysRe = regexp.MustCompile(`^\{"item":"[^"]+","fire":"[0-9a-f]{8}","sub":"[0-9a-f]{8}","handler":"[^"]+","event":"[a-z]+","from":"[a-z]+","to":"[a-z]+","at":"[0-9T:.-]+Z","started_at":"[0-9T:.-]+Z","finished_at":("[0-9T:.-]+Z"|null),"state":"[a-z_]+","exit_code":(-?[0-9]+|null),"detail":(".*"|null),"duration_ms":([0-9]+|null),"output":".*"\}$`)

// TestFiresVerb drives `ask fires` over real fires: one item's, oldest
// first, and the whole store's; text and JSON; and its refusals.
func TestFiresVerb(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\necho \"recorded $ASK_EVENT\"\n")

	// Nothing has fired: an empty array, an empty text listing, exit 0.
	quiet := w.newItem("quiet")
	for _, args := range [][]string{{"fires", "--json"}, {"fires", quiet, "--json"}} {
		if out := w.mustRun(0, args...); strings.TrimSpace(out) != "[]" {
			t.Errorf("ask %s = %q, want []", strings.Join(args, " "), out)
		}
	}
	if out := w.mustRun(0, "fires"); out != "" {
		t.Errorf("fires with nothing fired printed %q", out)
	}

	first := w.newItem("first")
	sub := w.subscribe(first, "any", "record")
	w.mustRun(0, "resolve", first)
	w.waitResults(first, 1)
	w.mustRun(0, "reopen", first)
	w.waitResults(first, 2)

	out := w.mustRun(0, "fires", first, "--json")
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil || len(raw) != 2 {
		t.Fatalf("fires --json: %v, %d elements\n%s", err, len(raw), out)
	}
	for _, el := range raw {
		var compact bytes.Buffer
		if err := json.Compact(&compact, el); err != nil {
			t.Fatal(err)
		}
		if !firesKeysRe.Match(compact.Bytes()) {
			t.Errorf("element is not in the documented key order:\n%s", compact.String())
		}
	}
	var fires []core.Fire
	if err := json.Unmarshal([]byte(out), &fires); err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct{ event, from, to string }{{"resolve", "open", "resolved"}, {"reopen", "resolved", "open"}} {
		f := fires[i]
		if f.Item != first || f.Sub != sub || f.Handler != "record" || f.Event != want.event ||
			string(f.From) != want.from || string(f.To) != want.to || f.State != "ok" ||
			f.ExitCode == nil || *f.ExitCode != 0 || f.Detail != nil || f.DurationMs == nil ||
			f.FinishedAt == nil || f.FinishedAt.Before(f.StartedAt) || f.Output != "recorded "+want.event+"\n" {
			t.Errorf("fire %d = %+v", i, f)
		}
	}
	// A prefix addresses the item as it does for every other verb.
	if got := w.mustRun(0, "fires", strings.TrimPrefix(first, "ask-"), "--json"); got != out {
		t.Errorf("fires by prefix differs from fires by id:\n%s\n%s", got, out)
	}

	text := w.mustRun(0, "fires", first)
	blockRe := regexp.MustCompile(`^fire [0-9a-f]{8}  item ` + first + `  subscription ` + sub + `  handler record\n` +
		`  \S+Z  resolve  open -> resolved\n  ok, exit 0 in \d+\.\d s\n  output:\n    recorded resolve\n\n` +
		`fire [0-9a-f]{8}  item ` + first + `  subscription ` + sub + `  handler record\n` +
		`  \S+Z  reopen  resolved -> open\n  ok, exit 0 in \d+\.\d s\n  output:\n    recorded reopen\n$`)
	if !blockRe.MatchString(text) {
		t.Errorf("fires text:\n%s", text)
	}

	// A fire with no result line reads as started, with nulls after it.
	other := w.newItem("unfinished")
	osub := w.subscribe(other, "resolve", "record")
	if err := os.MkdirAll(filepath.Dir(w.firesPath(other)), 0o755); err != nil {
		t.Fatal(err)
	}
	began := time.Now().UTC().Add(-time.Hour)
	line := firesStartedLine("0123abcd", osub, "resolve", "open", "resolved", began, began) + "\nnot a record line\n"
	if err := os.WriteFile(w.firesPath(other), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := w.run("fires", "--json")
	if code != 0 {
		t.Fatalf("fires --json: exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, other+": 1 line(s)") {
		t.Errorf("stderr does not count the line left out: %q", errOut)
	}
	if err := json.Unmarshal([]byte(out), &fires); err != nil || len(fires) != 3 {
		t.Fatalf("store-wide fires --json: %v, %d fires\n%s", err, len(fires), out)
	}
	// Oldest first across items: the unfinished one started an hour ago.
	if f := fires[0]; f.Item != other || f.Fire != "0123abcd" || f.State != "started" ||
		f.FinishedAt != nil || f.ExitCode != nil || f.Detail != nil || f.DurationMs != nil || f.Output != "" {
		t.Errorf("unfinished fire = %+v", f)
	}
	if fires[1].Event != "resolve" || fires[2].Event != "reopen" || fires[1].Item != first {
		t.Errorf("store-wide order: %+v", fires)
	}
	if text := w.mustRun(0, "fires", other); !regexp.MustCompile(`\n  started, no result after 60 min \(it died; it is not retried\)\n$`).MatchString(text) {
		t.Errorf("unfinished fire in text:\n%s", text)
	}

	// Refusals.
	if code, _, errOut := w.run("fires", "ask-0000"); code != 3 || !strings.Contains(errOut, "not found") {
		t.Errorf("unknown id: exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := w.run("fires", first, other); code != 2 {
		t.Errorf("two ids: exit %d, want 2", code)
	}
	if code, _, _ := w.run("fires", "--nope"); code != 2 {
		t.Errorf("unknown flag: exit %d, want 2", code)
	}
	if help := w.mustRun(0, "help"); !strings.Contains(help, "\n  fires ") {
		t.Errorf("help does not list fires:\n%s", help)
	}
}

// TestFiresReadCreatesNothing: neither read creates .ask/fires/, or
// anything else, in a store where nothing has fired.
func TestFiresReadCreatesNothing(t *testing.T) {
	dir, id, _ := firesSubscribed(t, "any")
	before := treeOf(t, dir)
	for _, args := range [][]string{
		{"fires"}, {"fires", "--json"}, {"fires", id}, {"fires", id, "--json"}, {"show", id}, {"show", id, "--json"},
	} {
		if code, _, errOut := subsRun(t, args...); code != 0 {
			t.Fatalf("ask %s: exit %d: %s", strings.Join(args, " "), code, errOut)
		}
	}
	if after := treeOf(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("the reads changed the store:\nbefore %q\n after %q", before, after)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".ask", "fires")); !os.IsNotExist(err) {
		t.Errorf(".ask/fires exists after the reads: %v", err)
	}
}

// TestHarvestedItemDoesNotShowMissed resolves a subscribed item so that
// it fires, harvests it into a second store, and reads it there: the fire
// came along, so `ask show` reports it and does not call it missed.
func TestHarvestedItemDoesNotShowMissed(t *testing.T) {
	bin, _ := fireBinaries(t)
	for _, clean := range []bool{false, true} {
		t.Run(fmt.Sprintf("clean=%v", clean), func(t *testing.T) {
			src := newFireWorld(t, bin)
			src.install("record", "#!/bin/sh\necho recorded\n")
			id := src.newItem("travels")
			src.subscribe(id, "any", "record")
			src.mustRun(0, "resolve", id)
			src.waitResults(id, 1)
			record, err := os.ReadFile(src.firesPath(id))
			if err != nil {
				t.Fatal(err)
			}

			dst := &fireWorld{t: t, bin: bin, home: src.home, proj: filepath.Join(filepath.Dir(src.proj), "target"), callbacks: src.callbacks}
			if err := os.MkdirAll(dst.proj, 0o755); err != nil {
				t.Fatal(err)
			}
			dst.mustRun(0, "init")
			args := []string{"harvest", "--from", src.proj}
			if clean {
				args = append(args, "--clean")
			}
			dst.mustRun(0, args...)

			show := dst.mustRun(0, "show", id)
			if !regexp.MustCompile(`\n      \S+Z  resolve   ok, exit 0 in \d+\.\d s\n`).MatchString(show) {
				t.Errorf("show in the target store has no ok line:\n%s", show)
			}
			if strings.Contains(show, "missed") {
				t.Errorf("show in the target store reports a miss:\n%s", show)
			}
			got, err := os.ReadFile(dst.firesPath(id))
			if err != nil || !bytes.Equal(got, record) {
				t.Errorf("fire record in the target: %v\n got %q\nwant %q", err, got, record)
			}
			if fi, err := os.Stat(dst.firesPath(id)); err == nil && fi.Mode().Perm() != 0o600 {
				t.Errorf("fire record in the target has mode %v, want 0600", fi.Mode().Perm())
			}
			_, err = os.Stat(src.firesPath(id))
			if clean && !os.IsNotExist(err) {
				t.Errorf("--clean left the source fire record: %v", err)
			}
			if !clean && err != nil {
				t.Errorf("without --clean the source fire record is gone: %v", err)
			}
		})
	}
}

// TestHarvestRefusesWhileAHandlerRuns harvests a store while a fire's
// handler is still running (act-afab33). The runner takes no lock, so a
// harvest at that moment would carry a started line with no result into
// the target, and its result line would land in the source afterwards
// (with --clean, re-creating the source record holding it alone). The
// harvest refuses with nothing written either side, and once the handler
// has finished the same harvest carries a record holding the result.
func TestHarvestRefusesWhileAHandlerRuns(t *testing.T) {
	bin, _ := fireBinaries(t)
	for _, clean := range []bool{false, true} {
		t.Run(fmt.Sprintf("clean=%v", clean), func(t *testing.T) {
			src := newFireWorld(t, bin)
			src.install("slow", "#!/bin/sh\nsleep 2\necho done\n")
			id := src.newItem("moves mid-fire")
			src.subscribe(id, "resolve", "slow")
			src.mustRun(0, "resolve", id)
			if n := len(firePhase(src.fireLines(id), "result")); n != 0 {
				t.Fatalf("the handler finished before the harvest could run (%d result line(s))", n)
			}

			dst := &fireWorld{t: t, bin: bin, home: src.home, proj: filepath.Join(filepath.Dir(src.proj), "target"), callbacks: src.callbacks}
			if err := os.MkdirAll(dst.proj, 0o755); err != nil {
				t.Fatal(err)
			}
			dst.mustRun(0, "init")
			args := []string{"harvest", "--from", src.proj}
			if clean {
				args = append(args, "--clean")
			}
			code, out, errOut := dst.run(args...)
			if code != 5 {
				t.Fatalf("harvest while the handler runs: exit %d, want 5\nstdout: %s\nstderr: %s", code, out, errOut)
			}
			if !strings.Contains(errOut, "still running") || !strings.Contains(errOut, id) || !strings.Contains(errOut, "handler slow") {
				t.Errorf("refusal does not name the item and its running handler: %s", errOut)
			}
			for _, p := range []string{dst.itemPath(id), dst.firesPath(id)} {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Errorf("the refused harvest wrote %s: %v", p, err)
				}
			}
			if _, err := os.Stat(src.itemPath(id)); err != nil {
				t.Errorf("the refused harvest removed the source item: %v", err)
			}

			src.waitResults(id, 1)
			dst.mustRun(0, args...)
			fires := dst.mustRun(0, "fires", id, "--json")
			var got []core.Fire
			if err := json.Unmarshal([]byte(fires), &got); err != nil {
				t.Fatalf("decode fires: %v\n%s", err, fires)
			}
			if len(got) != 1 || got[0].State != "ok" {
				t.Errorf("fires in the target after the handler finished: %s", fires)
			}
			_, err := os.Stat(src.firesPath(id))
			if clean && !os.IsNotExist(err) {
				t.Errorf("--clean left a source fire record: %v", err)
			}
		})
	}
}

// TestShowKeepsAStoredSubscriptionToItsLine: a subs file is a file on
// disk. A handler name or event in it that carries an escape sequence, a
// line break or a direction override does not reach the terminal.
func TestShowKeepsAStoredSubscriptionToItsLine(t *testing.T) {
	dir, id, sub := firesSubscribed(t, "resolve")
	body := fmt.Sprintf(`{"schema_version":"1","item":%q,"subscriptions":[{"id":%q,"events":["resolve\u001b[1m"],"handler":"rec\u001b[2J\n  - forged\u202e","args":{},"added_at":"2026-01-01T00:00:00Z"}]}`, id, sub.ID)
	if err := os.WriteFile(subsFilePath(dir, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := subsRun(t, "show", id)
	if code != 0 {
		t.Fatalf("show: exit %d: %s", code, errOut)
	}
	if strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x202e) || strings.Contains(out, "\n  - forged") {
		t.Errorf("show printed what the subs file planted:\n%q", out)
	}
	if !strings.Contains(out, "\n  - "+sub.ID+" rec [2J   - forged on resolve [1m added 2026-01-01T00:00:00Z\n") {
		t.Errorf("show does not print the subscription on one line:\n%q", out)
	}
}

// TestStoredSubscriptionFieldsNeverReachTheTerminalRaw: every field of a
// stored subscription that `show`, `subscribe` and `unsubscribe` print in
// text comes from a file on disk. An id that goes on past its eight hex
// characters, and an argument key, are kept to their line like the rest.
func TestStoredSubscriptionFieldsNeverReachTheTerminalRaw(t *testing.T) {
	dir, id, _ := firesSubscribed(t, "resolve")
	plant := func(t *testing.T) {
		t.Helper()
		body := fmt.Sprintf(`{"schema_version":"1","item":%q,"subscriptions":[`+
			`{"id":"aaaaaaaa\u001b[2J\u202e","events":["resolve"],"handler":"rec","args":{"k\u001b[1m\nforged":"v"},"added_at":"2026-01-01T00:00:00Z"},`+
			`{"id":"aaaabbbb\u001b[2J","events":["close"],"handler":"rec","args":{},"added_at":"2026-01-01T00:00:00Z"}]}`, id)
		if err := os.WriteFile(subsFilePath(dir, id), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw := func(s string) bool {
		return strings.ContainsRune(s, 0x1b) || strings.ContainsRune(s, 0x202e) || strings.Contains(s, "\nforged")
	}
	plant(t)
	for _, args := range [][]string{
		{"show", id},
		// Identical to the second stored subscription: reported as existing, by its stored id.
		{"subscribe", id, "--on", "close", "--handler", "rec"},
		// Ambiguous: the message names both stored ids.
		{"unsubscribe", id, "aaaa"},
		{"unsubscribe", id, "aaaaa"},
	} {
		code, out, errOut := subsRun(t, args...)
		if raw(out) || raw(errOut) {
			t.Errorf("ask %s (exit %d) printed a stored field raw:\nstdout %q\nstderr %q", strings.Join(args, " "), code, out, errOut)
		}
	}
}
