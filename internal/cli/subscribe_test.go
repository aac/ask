package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// subsHome points HOME at a fresh directory with an empty callbacks
// directory and returns that callbacks directory.
func subsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "ask", "callbacks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// subsInstall writes an executable handler script named name into the
// callbacks directory.
func subsInstall(t *testing.T, callbacks, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(callbacks, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// subsSeed creates a store in a fresh directory, chdirs into it, and
// writes one item with the given status. It returns the directory and the
// item id.
func subsSeed(t *testing.T, status core.Status) (string, string) {
	t.Helper()
	dir := t.TempDir()
	inDir(t, dir)
	now := time.Now().UTC()
	it := core.NewItem("ask-1111", "Set up OAuth", core.UrgencyNormal, status, now)
	switch status {
	case core.StatusResolved:
		it.ResolvedAt = &now
	case core.StatusClosed:
		it.ClosedAt = &now
	}
	seedStore(t, dir, it)
	return dir, it.ID
}

// subsRun runs the CLI and returns its exit code, stdout and stderr.
func subsRun(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() { code = Run(args) })
	})
	return code, stdout, stderr
}

func subsFilePath(dir, id string) string {
	return filepath.Join(dir, ".ask", "subs", id+".json")
}

// subsRead parses an item's subs file; it fails the test if there is none.
func subsRead(t *testing.T, dir, id string) *core.SubsFile {
	t.Helper()
	b, err := os.ReadFile(subsFilePath(dir, id))
	if err != nil {
		t.Fatalf("read subs file: %v", err)
	}
	var f core.SubsFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("parse subs file: %v\n%s", err, b)
	}
	return &f
}

// subsAssertNoDir fails if .ask/subs/ exists under dir.
func subsAssertNoDir(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, ".ask", "subs")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".ask/subs exists (stat err %v), want nothing stored", err)
	}
}

func TestSubscribeStoresAndReportsStatus(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusOpen)

	code, out, errOut := subsRun(t, "subscribe", id, "--on", "resolve,close", "--handler", "rec", "--arg", "thread=abc", "--arg", "seat=two")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errOut)
	}
	re := regexp.MustCompile(`^ask-1111: subscribed ([0-9a-f]{8}) \(resolve,close -> rec\); status open\n$`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("stdout %q does not match %s", out, re)
	}
	f := subsRead(t, dir, id)
	if f.SchemaVersion != "1" || f.Item != id || len(f.Subscriptions) != 1 {
		t.Fatalf("subs file = %+v", f)
	}
	sub := f.Subscriptions[0]
	if sub.ID != m[1] || sub.Handler != "rec" || strings.Join(sub.Events, ",") != "resolve,close" ||
		sub.Args["thread"] != "abc" || sub.Args["seat"] != "two" || len(sub.Args) != 2 || sub.AddedAt.IsZero() {
		t.Errorf("stored subscription = %+v", sub)
	}

	// --json, and `any` stored as all three events.
	code, out, errOut = subsRun(t, "subscribe", id, "--on", "any", "--handler", "rec", "--json")
	if code != 0 {
		t.Fatalf("--json: exit %d; stderr %q", code, errOut)
	}
	var got subscribeJSONOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output %q: %v", out, err)
	}
	if got.ID != id || got.Status != core.StatusOpen || strings.Join(got.Subscription.Events, ",") != "resolve,close,reopen" ||
		got.Subscription.Args == nil {
		t.Errorf("--json = %+v", got)
	}
	if n := len(subsRead(t, dir, id).Subscriptions); n != 2 {
		t.Errorf("%d subscriptions stored, want 2", n)
	}
}

func TestSubscribeResolvedExits8AndStores(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusResolved)

	code, out, errOut := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "rec")
	if code != 8 {
		t.Fatalf("exit %d, want 8; stderr %q", code, errOut)
	}
	if !strings.Contains(errOut, "already resolved") {
		t.Errorf("stderr %q does not say the item is already resolved", errOut)
	}
	if !strings.Contains(out, "status resolved") {
		t.Errorf("stdout %q does not report the status", out)
	}
	if n := len(subsRead(t, dir, id).Subscriptions); n != 1 {
		t.Fatalf("%d subscriptions stored, want 1", n)
	}

	// A subscription that does not name resolve is an ordinary 0.
	if code, _, errOut := subsRun(t, "subscribe", id, "--on", "reopen", "--handler", "rec"); code != 0 {
		t.Errorf("--on reopen on a resolved item: exit %d, want 0; stderr %q", code, errOut)
	}
}

func TestSubscribeClosedExits2StoresNothing(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusClosed)

	code, out, errOut := subsRun(t, "subscribe", id, "--on", "any", "--handler", "rec")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if out != "" || !strings.Contains(errOut, "closed") {
		t.Errorf("stdout %q stderr %q", out, errOut)
	}
	subsAssertNoDir(t, dir)
}

func TestSubscribeUnknownHandlerExits2ListsInstalled(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "beta")
	subsInstall(t, cb, "alpha")
	// Neither of these is a usable handler, so neither is listed.
	if err := os.WriteFile(filepath.Join(cb, "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	subsInstall(t, cb, "Upper")
	dir, id := subsSeed(t, core.StatusOpen)

	code, _, errOut := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "gamma")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "unknown handler") || !strings.Contains(errOut, "installed handlers: alpha, beta\n") {
		t.Errorf("stderr %q does not list exactly the installed handlers", errOut)
	}
	subsAssertNoDir(t, dir)

	// With nothing installed (no callbacks directory at all) it says so.
	if err := os.RemoveAll(cb); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "gamma")
	if code != 2 || !strings.Contains(errOut, "installed handlers: none") {
		t.Errorf("no callbacks dir: exit %d stderr %q", code, errOut)
	}
}

func TestSubscribeRejectsBadNames(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "alpha")
	dir, id := subsSeed(t, core.StatusOpen)

	for _, name := range []string{"", ".", "-alpha", "ALPHA", "Alpha", "a/b", "../alpha"} {
		code, _, errOut := subsRun(t, "subscribe", id, "--on", "resolve", "--handler="+name)
		if code != 2 {
			t.Errorf("handler %q: exit %d, want 2", name, code)
		}
		if !strings.Contains(errOut, "invalid handler name") {
			t.Errorf("handler %q: stderr %q", name, errOut)
		}
	}
	subsAssertNoDir(t, dir)
}

func TestSubscribeRejectsBadArgs(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusOpen)

	seventeen := []string{}
	for i := 0; i < 17; i++ {
		seventeen = append(seventeen, "--arg", fmt.Sprintf("k%d=v", i))
	}
	cases := map[string][]string{
		"empty key":       {"--arg", "=v"},
		"no equals":       {"--arg", "thread"},
		"upper-case key":  {"--arg", "Thread=v"},
		"leading digit":   {"--arg", "1k=v"},
		"duplicate key":   {"--arg", "k=1", "--arg", "k=2"},
		"17th argument":   seventeen,
		"1025-byte value": {"--arg", "k=" + strings.Repeat("x", 1025)},
		"NUL in value":    {"--arg", "k=a\x00b"},
	}
	for name, extra := range cases {
		args := append([]string{"subscribe", id, "--on", "resolve", "--handler", "rec"}, extra...)
		if code, _, errOut := subsRun(t, args...); code != 2 {
			t.Errorf("%s: exit %d, want 2; stderr %q", name, code, errOut)
		}
	}
	// A key containing `=` cannot be spelled on the command line (the
	// first `=` splits key from value), so check the shared rule directly.
	if err := core.ValidateSubArgs(map[string]string{"a=b": "x"}); err == nil {
		t.Error("ValidateSubArgs accepted a key with =")
	}
	// Sixteen arguments and a 1024-byte value are within the limits.
	sixteen := append([]string{"subscribe", id, "--on", "resolve", "--handler", "rec"}, seventeen[:32]...)
	sixteen[len(sixteen)-1] = "k15=" + strings.Repeat("x", 1024)
	if code, _, errOut := subsRun(t, sixteen...); code != 0 {
		t.Errorf("16 arguments: exit %d, want 0; stderr %q", code, errOut)
	}
	if n := len(subsRead(t, dir, id).Subscriptions); n != 1 {
		t.Errorf("%d subscriptions stored, want only the valid one", n)
	}
}

func TestSubscribeRejectsBadEvents(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusOpen)
	for _, on := range []string{"", "update", "resolve,", "Resolve"} {
		if code, _, _ := subsRun(t, "subscribe", id, "--on="+on, "--handler", "rec"); code != 2 {
			t.Errorf("--on %q: exit %d, want 2", on, code)
		}
	}
	if code, _, _ := subsRun(t, "subscribe", "--on", "resolve", "--handler", "rec"); code != 2 {
		t.Errorf("no id: exit %d, want 2", code)
	}
	subsAssertNoDir(t, dir)
}

func TestSubscribeIdenticalExits6(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusOpen)

	code, out, _ := subsRun(t, "subscribe", id, "--on", "resolve,close", "--handler", "rec", "--arg", "a=1", "--arg", "b=2")
	if code != 0 {
		t.Fatalf("first: exit %d", code)
	}
	first := subsRead(t, dir, id).Subscriptions[0].ID
	// Same handler, same arguments in another order, same event set in
	// another order: identical.
	code, out, errOut := subsRun(t, "subscribe", id, "--on", "close,resolve", "--handler", "rec", "--arg", "b=2", "--arg", "a=1")
	if code != 6 {
		t.Fatalf("identical: exit %d, want 6; stderr %q", code, errOut)
	}
	if !strings.Contains(out, "already subscribed "+first) {
		t.Errorf("stdout %q does not print the existing id %s", out, first)
	}
	if n := len(subsRead(t, dir, id).Subscriptions); n != 1 {
		t.Errorf("%d subscriptions stored, want 1", n)
	}
	// A different argument value is a different subscription.
	if code, _, _ := subsRun(t, "subscribe", id, "--on", "resolve,close", "--handler", "rec", "--arg", "a=1", "--arg", "b=3"); code != 0 {
		t.Errorf("different args: exit %d, want 0", code)
	}
}

func TestSubscribeIdenticalOnResolvedExits8StoresNothing(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusResolved)

	if code, _, _ := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "rec"); code != 8 {
		t.Fatalf("first: exit %d, want 8", code)
	}
	before, err := os.ReadFile(subsFilePath(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	existing := subsRead(t, dir, id).Subscriptions[0].ID

	code, out, _ := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "rec")
	if code != 8 {
		t.Fatalf("identical on resolved: exit %d, want 8", code)
	}
	if !strings.Contains(out, existing) {
		t.Errorf("stdout %q does not print the existing id %s", out, existing)
	}
	after, err := os.ReadFile(subsFilePath(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("subs file changed:\nbefore %s\nafter  %s", before, after)
	}
}

func TestSubscribeRefuses33rd(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusOpen)
	for i := 0; i < core.MaxSubscriptions; i++ {
		if code, _, errOut := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "rec", "--arg", fmt.Sprintf("n=%d", i)); code != 0 {
			t.Fatalf("subscription %d: exit %d; stderr %q", i+1, code, errOut)
		}
	}
	before, _ := os.ReadFile(subsFilePath(dir, id))
	code, _, errOut := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "rec", "--arg", "n=33")
	if code != 2 {
		t.Fatalf("33rd: exit %d, want 2; stderr %q", code, errOut)
	}
	after, _ := os.ReadFile(subsFilePath(dir, id))
	if !bytes.Equal(before, after) {
		t.Error("the 33rd subscription changed the subs file")
	}
	// An identical one is still reported, not refused.
	if code, _, _ := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "rec", "--arg", "n=0"); code != 6 {
		t.Errorf("identical at the cap: exit %d, want 6", code)
	}
}

func TestSubscribeFollowsSymlinkToExecutable(t *testing.T) {
	cb := subsHome(t)
	target := filepath.Join(t.TempDir(), "real-handler")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(cb, "linked")); err != nil {
		t.Fatal(err)
	}
	dir, id := subsSeed(t, core.StatusOpen)
	if code, _, errOut := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "linked"); code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errOut)
	}
	if h := subsRead(t, dir, id).Subscriptions[0].Handler; h != "linked" {
		t.Errorf("stored handler %q, want the name under the callbacks directory", h)
	}
}

func TestSubscribeRefusesDirectoryAndNonExecutable(t *testing.T) {
	cb := subsHome(t)
	if err := os.Mkdir(filepath.Join(cb, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cb, "noexec"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink to a directory is followed, and refused as a directory.
	if err := os.Symlink(t.TempDir(), filepath.Join(cb, "dirlink")); err != nil {
		t.Fatal(err)
	}
	dir, id := subsSeed(t, core.StatusOpen)
	for _, name := range []string{"adir", "noexec", "dirlink"} {
		code, _, errOut := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", name)
		if code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
		if !strings.Contains(errOut, "unknown handler") {
			t.Errorf("%s: stderr %q", name, errOut)
		}
	}
	subsAssertNoDir(t, dir)
}

func TestUnsubscribe(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusOpen)
	for _, on := range []string{"resolve", "close"} {
		if code, _, _ := subsRun(t, "subscribe", id, "--on", on, "--handler", "rec"); code != 0 {
			t.Fatalf("subscribe --on %s: exit %d", on, code)
		}
	}
	subs := subsRead(t, dir, id).Subscriptions
	first, second := subs[0].ID, subs[1].ID

	if code, _, _ := subsRun(t, "unsubscribe", id, "XYZ"); code != 2 {
		t.Errorf("bad subscription id: exit %d, want 2", code)
	}
	if code, _, _ := subsRun(t, "unsubscribe", id); code != 2 {
		t.Errorf("missing subscription id: exit %d, want 2", code)
	}
	if code, _, _ := subsRun(t, "unsubscribe", "ask-9999", first); code != 3 {
		t.Errorf("unknown item: exit %d, want 3", code)
	}
	missing := "00000000"
	if missing == first || missing == second {
		missing = "ffffffff"
	}
	if code, _, _ := subsRun(t, "unsubscribe", id, missing); code != 3 {
		t.Errorf("unknown subscription: exit %d, want 3", code)
	}

	code, out, errOut := subsRun(t, "unsubscribe", id, first[:5])
	if code != 0 {
		t.Fatalf("unsubscribe by prefix: exit %d; stderr %q", code, errOut)
	}
	if out != id+": unsubscribed "+first+"\n" {
		t.Errorf("stdout %q", out)
	}
	if left := subsRead(t, dir, id).Subscriptions; len(left) != 1 || left[0].ID != second {
		t.Fatalf("left %+v, want only %s", left, second)
	}

	code, out, _ = subsRun(t, "unsubscribe", id, second, "--json")
	if code != 0 {
		t.Fatalf("unsubscribe --json: exit %d", code)
	}
	var got unsubscribeJSONOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.ID != id || got.Unsubscribed.ID != second {
		t.Errorf("--json %q: %+v %v", out, got, err)
	}
	if _, err := os.Stat(subsFilePath(dir, id)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("subs file still present after the last subscription went: %v", err)
	}
}

func TestUnsubscribeAmbiguousPrefixExits4(t *testing.T) {
	subsHome(t)
	dir, id := subsSeed(t, core.StatusOpen)
	f := core.SubsFile{SchemaVersion: "1", Item: id, Subscriptions: []core.Subscription{
		{ID: "aaaa0001", Events: []string{"resolve"}, Handler: "rec", Args: map[string]string{}, AddedAt: time.Now().UTC()},
		{ID: "aaaa0002", Events: []string{"close"}, Handler: "rec", Args: map[string]string{}, AddedAt: time.Now().UTC()},
	}}
	b, _ := json.Marshal(f)
	if err := os.MkdirAll(filepath.Dir(subsFilePath(dir, id)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subsFilePath(dir, id), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := subsRun(t, "unsubscribe", id, "aaaa"); code != 4 {
		t.Errorf("ambiguous: exit %d, want 4; stderr %q", code, errOut)
	}
	if code, _, _ := subsRun(t, "unsubscribe", id, "aaaa0002"); code != 0 {
		t.Errorf("full id: exit %d, want 0", code)
	}
}

func TestShowSubscriptions(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir, id := subsSeed(t, core.StatusOpen)

	_, out, _ := subsRun(t, "show", id)
	if strings.Contains(out, "Subscriptions:") {
		t.Errorf("show without subscriptions prints the section:\n%s", out)
	}

	if code, _, _ := subsRun(t, "subscribe", id, "--on", "resolve,reopen", "--handler", "rec", "--arg", "thread=abc def"); code != 0 {
		t.Fatalf("subscribe: exit %d", code)
	}
	sub := subsRead(t, dir, id).Subscriptions[0]
	code, out, _ := subsRun(t, "show", id)
	if code != 0 {
		t.Fatalf("show: exit %d", code)
	}
	want := fmt.Sprintf("\nSubscriptions:\n  - %s rec on resolve,reopen thread=\"abc def\" added %s\n",
		sub.ID, sub.AddedAt.UTC().Format(time.RFC3339))
	if !strings.HasSuffix(out, want) {
		t.Errorf("show output:\n%s\nwant it to end with:\n%s", out, want)
	}
	if !regexp.MustCompile(`added \d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ\n$`).MatchString(out) {
		t.Errorf("added time is not RFC 3339 UTC: %q", out)
	}
}

func TestShowJSONUnchangedBySubscriptions(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	_, id := subsSeed(t, core.StatusOpen)

	code, before, _ := subsRun(t, "show", id, "--json")
	if code != 0 {
		t.Fatalf("show --json: exit %d", code)
	}
	if code, _, _ := subsRun(t, "subscribe", id, "--on", "any", "--handler", "rec", "--arg", "k=v"); code != 0 {
		t.Fatalf("subscribe: exit %d", code)
	}
	_, after, _ := subsRun(t, "show", id, "--json")
	if before != after {
		t.Errorf("show --json changed:\nbefore %s\nafter  %s", before, after)
	}
}

func TestReadsNeverCreateSubsDir(t *testing.T) {
	subsHome(t)
	dir, id := subsSeed(t, core.StatusOpen)
	for _, args := range [][]string{{"show", id}, {"show", id, "--json"}, {"list"}, {"list", "--json"}} {
		if code, _, _ := subsRun(t, args...); code != 0 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	subsAssertNoDir(t, dir)
}

func TestHarvestCopiesSubscriptions(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	mainDir := t.TempDir()
	harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A")
	srcDir, id := subsSeed(t, core.StatusOpen) // chdirs into srcDir
	plain := core.NewItem("ask-2222", "no subscriptions", core.UrgencyNormal, core.StatusOpen, time.Now().UTC())
	seedStore(t, srcDir, plain)
	if code, _, _ := subsRun(t, "subscribe", id, "--on", "resolve", "--handler", "rec", "--arg", "thread=abc"); code != 0 {
		t.Fatalf("subscribe: exit %d", code)
	}
	srcBytes, err := os.ReadFile(subsFilePath(srcDir, id))
	if err != nil {
		t.Fatal(err)
	}

	inDir(t, mainDir)
	if code, _, errOut := subsRun(t, "harvest", "--from", srcDir, "--clean"); code != 0 {
		t.Fatalf("harvest: exit %d; stderr %q", code, errOut)
	}
	got, err := os.ReadFile(subsFilePath(mainDir, id))
	if err != nil {
		t.Fatalf("target subs file: %v", err)
	}
	var a, b core.SubsFile
	_ = json.Unmarshal(srcBytes, &a)
	_ = json.Unmarshal(got, &b)
	if len(b.Subscriptions) != 1 || b.Item != id || b.Subscriptions[0].ID != a.Subscriptions[0].ID ||
		b.Subscriptions[0].Args["thread"] != "abc" {
		t.Errorf("target subs %s, want the source's %s", got, srcBytes)
	}
	if _, err := os.Stat(subsFilePath(mainDir, "ask-2222")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an item without subscriptions got a subs file: %v", err)
	}
	if _, err := os.Stat(subsFilePath(srcDir, id)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("--clean left the source subs file: %v", err)
	}
	// The harvested subscription is the one `unsubscribe` sees in the target.
	if code, _, _ := subsRun(t, "unsubscribe", id, a.Subscriptions[0].ID); code != 0 {
		t.Errorf("unsubscribe in target: exit %d", code)
	}
}

func TestHarvestRefusesUnreadableSubscriptions(t *testing.T) {
	subsHome(t)
	mainDir := t.TempDir()
	harvestSeedAt(t, mainDir, "01HXMAIN0000000000000000A")
	srcDir, id := subsSeed(t, core.StatusOpen)
	if err := os.MkdirAll(filepath.Dir(subsFilePath(srcDir, id)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subsFilePath(srcDir, id), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	inDir(t, mainDir)
	if code, _, _ := subsRun(t, "harvest", "--from", srcDir, "--clean"); code != 5 {
		t.Fatalf("harvest with a corrupt subs file: exit %d, want 5", code)
	}
	if _, err := os.Stat(filepath.Join(mainDir, ".ask", "items", id+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("item copied despite unreadable subscriptions: %v", err)
	}
}

// TestSubscribeVersusResolve starts, for each of 50 items, one
// `subscribe --on resolve` and one `resolve` together. Whichever takes the
// store lock first, every item ends resolved, every subscribe exits 0 (it
// ran first) or 8 (the resolve ran first), and every subscription that
// got 0 is in its item's subs file.
func TestSubscribeVersusResolve(t *testing.T) {
	cb := subsHome(t)
	subsInstall(t, cb, "rec")
	dir := t.TempDir()
	inDir(t, dir)
	const n = 50
	ids := make([]string, n)
	items := make([]*core.Item, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("ask-%04x", 0x1000+i)
		items[i] = core.NewItem(ids[i], fmt.Sprintf("item %d", i), core.UrgencyNormal, core.StatusOpen, time.Now().UTC())
	}
	seedStore(t, dir, items...)

	codes := runConcurrently(t, 2*n, func(i int) []string {
		if i%2 == 0 {
			return []string{"subscribe", ids[i/2], "--on", "resolve", "--handler", "rec"}
		}
		return []string{"resolve", ids[i/2]}
	})
	for i := 0; i < n; i++ {
		sub, res := codes[2*i], codes[2*i+1]
		if res != 0 {
			t.Errorf("%s: resolve exit %d, want 0", ids[i], res)
		}
		if sub != 0 && sub != 8 {
			t.Errorf("%s: subscribe exit %d, want 0 or 8", ids[i], sub)
		}
		if st := loadItem(t, dir, ids[i]).Status; st != core.StatusResolved {
			t.Errorf("%s: status %s, want resolved", ids[i], st)
		}
		if sub == 0 || sub == 8 {
			f := subsRead(t, dir, ids[i])
			if len(f.Subscriptions) != 1 || f.Subscriptions[0].Handler != "rec" {
				t.Errorf("%s: subscribe exited %d but the subs file holds %+v", ids[i], sub, f.Subscriptions)
			}
		}
	}
}
