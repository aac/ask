package cli

// Boundary tests for firing. A fire re-executes the running binary, which
// under `go test` is the test binary, so nothing here runs in-process:
// these tests build ask from the current source once and drive it as a
// subprocess, with HOME pointed at a temp directory that holds the
// handlers.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// fireShortTimeout is the handler timeout linked into the second build, so
// the timeout path runs in a couple of seconds instead of a minute.
const fireShortTimeout = 500 * time.Millisecond

var fireBuild struct {
	once  sync.Once
	dir   string
	bin   string // ask, as shipped
	short string // ask with the handler timeout set to fireShortTimeout
	err   error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fireBuild.dir != "" {
		_ = os.RemoveAll(fireBuild.dir)
	}
	os.Exit(code)
}

// fireBinaries builds the two binaries on first use and returns their
// paths.
func fireBinaries(t *testing.T) (bin, short string) {
	t.Helper()
	fireBuild.once.Do(func() {
		dir, err := os.MkdirTemp("", "ask-fire-build-")
		if err != nil {
			fireBuild.err = err
			return
		}
		fireBuild.dir = dir
		fireBuild.bin = filepath.Join(dir, "bin", "ask")
		fireBuild.short = filepath.Join(dir, "short", "ask")
		builds := [][]string{
			{"build", "-o", fireBuild.bin, "github.com/aac/ask/cmd/ask"},
			{"build", "-ldflags", "-X github.com/aac/ask/internal/core.fireTimeoutOverride=" + fireShortTimeout.String(),
				"-o", fireBuild.short, "github.com/aac/ask/cmd/ask"},
		}
		for _, args := range builds {
			if out, err := exec.Command("go", args...).CombinedOutput(); err != nil {
				fireBuild.err = fmt.Errorf("go %s: %v\n%s", strings.Join(args, " "), err, out)
				return
			}
		}
	})
	if fireBuild.err != nil {
		t.Fatalf("build ask: %v", fireBuild.err)
	}
	return fireBuild.bin, fireBuild.short
}

// fireMarkerPath is the PATH every command in these tests is run with. A
// handler must never see it.
const fireMarkerPath = "/fire-test-marker-path"

// fireWorld is one HOME and one initialized project.
type fireWorld struct {
	t         *testing.T
	bin       string
	home      string
	proj      string
	callbacks string
	extraEnv  []string
}

func newFireWorld(t *testing.T, bin string) *fireWorld {
	t.Helper()
	// Resolve symlinks so paths compare equal to what a child reports.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &fireWorld{t: t, bin: bin, home: filepath.Join(base, "home"), proj: filepath.Join(base, "proj")}
	w.callbacks = filepath.Join(w.home, ".config", "ask", "callbacks")
	for _, d := range []string{w.callbacks, w.proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w.mustRun(0, "init")
	return w
}

func (w *fireWorld) cmd(args ...string) *exec.Cmd {
	c := exec.Command(w.bin, args...)
	c.Dir = w.proj
	c.Env = append([]string{
		"HOME=" + w.home,
		"PATH=" + fireMarkerPath,
		"CLAUDE_FIRE_TEST=must-not-reach-a-handler",
		"FIRE_TEST_SECRET=must-not-reach-a-handler",
	}, w.extraEnv...)
	return c
}

// run runs ask and returns its exit code, stdout and stderr.
func (w *fireWorld) run(args ...string) (int, string, string) {
	w.t.Helper()
	c := w.cmd(args...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		w.t.Fatalf("ask %s: %v", strings.Join(args, " "), err)
	}
	return c.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

// mustRun is run with the exit code checked; it returns stdout.
func (w *fireWorld) mustRun(want int, args ...string) string {
	w.t.Helper()
	code, stdout, stderr := w.run(args...)
	if code != want {
		w.t.Fatalf("ask %s: exit %d, want %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, want, stdout, stderr)
	}
	return stdout
}

// newItem files an item and returns its id.
func (w *fireWorld) newItem(title string, extra ...string) string {
	w.t.Helper()
	out := w.mustRun(0, append([]string{"new", title, "--json"}, extra...)...)
	var it core.Item
	if err := json.Unmarshal([]byte(out), &it); err != nil {
		w.t.Fatalf("decode new item: %v\n%s", err, out)
	}
	return it.ID
}

// install writes an executable handler.
func (w *fireWorld) install(name, script string) string {
	w.t.Helper()
	path := filepath.Join(w.callbacks, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		w.t.Fatal(err)
	}
	return path
}

func (w *fireWorld) itemPath(id string) string {
	return filepath.Join(w.proj, ".ask", "items", id+".json")
}

func (w *fireWorld) firesPath(id string) string {
	return filepath.Join(w.proj, ".ask", "fires", id+".jsonl")
}

// fireLine is the union of both record line shapes, for reading.
type fireLine struct {
	V          int     `json:"v"`
	Fire       string  `json:"fire"`
	Sub        string  `json:"sub"`
	Handler    string  `json:"handler"`
	Event      string  `json:"event"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	At         string  `json:"at"`
	Phase      string  `json:"phase"`
	Ts         string  `json:"ts"`
	State      string  `json:"state"`
	ExitCode   *int    `json:"exit_code"`
	Detail     *string `json:"detail"`
	DurationMs int64   `json:"duration_ms"`
	Output     string  `json:"output"`
	raw        string
}

// fireLines reads an item's fire record. A missing file is no lines.
func (w *fireWorld) fireLines(id string) []fireLine {
	w.t.Helper()
	b, err := os.ReadFile(w.firesPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		w.t.Fatal(err)
	}
	var lines []fireLine
	for _, raw := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		var l fireLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			w.t.Fatalf("fire record line is not JSON: %v\n%s", err, raw)
		}
		l.raw = raw
		lines = append(lines, l)
	}
	return lines
}

func firePhase(lines []fireLine, phase string) []fireLine {
	var out []fireLine
	for _, l := range lines {
		if l.Phase == phase {
			out = append(out, l)
		}
	}
	return out
}

// waitResults waits until the item's record holds n result lines and
// returns the whole record.
func (w *fireWorld) waitResults(id string, n int) []fireLine {
	w.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		lines := w.fireLines(id)
		if len(firePhase(lines, "result")) >= n {
			return lines
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("waited 15s for %d result line(s) on %s; record:\n%s", n, id, fireDump(lines))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func fireDump(lines []fireLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.raw + "\n")
	}
	return b.String()
}

func fireDetail(l fireLine) string {
	if l.Detail == nil {
		return "<null>"
	}
	return *l.Detail
}

func fireExit(l fireLine) string {
	if l.ExitCode == nil {
		return "<null>"
	}
	return strconv.Itoa(*l.ExitCode)
}

// subscribe subscribes handler to events on id and returns the
// subscription id.
func (w *fireWorld) subscribe(id, events, handler string, extra ...string) string {
	w.t.Helper()
	out := w.mustRun(0, append([]string{"subscribe", id, "--on", events, "--handler", handler, "--json"}, extra...)...)
	var got struct {
		Subscription core.Subscription `json:"subscription"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		w.t.Fatalf("decode subscribe output: %v\n%s", err, out)
	}
	return got.Subscription.ID
}

var (
	fireStartedRe = regexp.MustCompile(`^\{"v":1,"fire":"[0-9a-f]{8}","sub":"[0-9a-f]{8}","handler":"[a-z0-9_-]+","event":"(resolve|close|reopen)","from":"(open|resolved)","to":"(open|resolved|closed)","at":"[0-9T:.-]+Z","phase":"started","ts":"[0-9T:.-]+Z"\}$`)
	fireResultRe  = regexp.MustCompile(`^\{"v":1,"fire":"[0-9a-f]{8}","sub":"[0-9a-f]{8}","phase":"result","state":"(ok|failed|not_run|spawn_failed|timeout)","exit_code":(-?[0-9]+|null),"detail":(".*"|null),"duration_ms":[0-9]+,"ts":"[0-9T:.-]+Z","output":".*"\}$`)
)

// checkLineShapes holds every line of a record to the two exact shapes.
func checkLineShapes(t *testing.T, lines []fireLine) {
	t.Helper()
	for _, l := range lines {
		re := fireStartedRe
		if l.Phase == "result" {
			re = fireResultRe
		}
		if !re.MatchString(l.raw) {
			t.Errorf("%s line is not in the recorded shape:\n%s", l.Phase, l.raw)
		}
		for _, ts := range []string{l.At, l.Ts} {
			if ts == "" {
				continue
			}
			if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
				t.Errorf("time %q is not RFC 3339: %v", ts, err)
			}
		}
	}
}

// TestFireResolveCloseReopenOnceEachCLI walks one subscribed item through
// resolve, reopen, resolve and close, and a second one through close from
// open. Each transition must add exactly one started line and one ok
// result line, and run the handler once with that transition.
func TestFireResolveCloseReopenOnceEachCLI(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\necho \"$ASK_ID $ASK_EVENT $ASK_FROM_STATUS $ASK_TO_STATUS\" >> \"$HOME/fire.log\"\n")
	id := w.newItem("walk")
	sub := w.subscribe(id, "any", "record")

	steps := []struct {
		args            []string
		event, from, to string
	}{
		{[]string{"resolve", id}, "resolve", "open", "resolved"},
		{[]string{"reopen", id}, "reopen", "resolved", "open"},
		{[]string{"resolve", id}, "resolve", "open", "resolved"},
		{[]string{"close", id}, "close", "resolved", "closed"},
	}
	var wantLog []string
	for i, st := range steps {
		w.mustRun(0, st.args...)
		lines := w.waitResults(id, i+1)
		started, results := firePhase(lines, "started"), firePhase(lines, "result")
		if len(started) != i+1 || len(results) != i+1 {
			t.Fatalf("after %v: %d started and %d result lines, want %d each\n%s", st.args, len(started), len(results), i+1, fireDump(lines))
		}
		s, r := started[i], results[i]
		if s.Event != st.event || s.From != st.from || s.To != st.to || s.Sub != sub || s.Handler != "record" {
			t.Errorf("after %v: started line %s", st.args, s.raw)
		}
		if r.Fire != s.Fire || r.Sub != sub || r.State != "ok" || fireExit(r) != "0" || r.Detail != nil {
			t.Errorf("after %v: result line %s (started %s)", st.args, r.raw, s.raw)
		}
		wantLog = append(wantLog, strings.Join([]string{id, st.event, st.from, st.to}, " "))
	}
	lines := w.fireLines(id)
	checkLineShapes(t, lines)
	seen := map[string]bool{}
	for _, s := range firePhase(lines, "started") {
		if seen[s.Fire] {
			t.Errorf("fire id %s used twice", s.Fire)
		}
		seen[s.Fire] = true
	}

	other := w.newItem("dismissed")
	w.subscribe(other, "close", "record")
	w.mustRun(0, "close", other)
	ol := w.waitResults(other, 1)
	if s := firePhase(ol, "started"); len(s) != 1 || s[0].Event != "close" || s[0].From != "open" || s[0].To != "closed" {
		t.Errorf("close from open: record\n%s", fireDump(ol))
	}
	wantLog = append(wantLog, other+" close open closed")

	b, err := os.ReadFile(filepath.Join(w.home, "fire.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSpace(string(b)), "\n"); !reflect.DeepEqual(got, wantLog) {
		t.Errorf("handler runs:\n got %q\nwant %q", got, wantLog)
	}
}

// TestFireEnvironmentIsBuilt: a handler's environment is built, not
// inherited. Its variable names are exactly HOME, LANG, PATH and the ASK_*
// set, apart from the ones a shell adds for itself.
func TestFireEnvironmentIsBuilt(t *testing.T) {
	bin, _ := fireBinaries(t)
	script := "#!/bin/sh\n/usr/bin/env > \"$HOME/fire.env\"\npwd -P > \"$HOME/fire.pwd\"\n"
	shellOwn := map[string]bool{"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true}

	for _, tc := range []struct {
		name, lang, wantLang string
	}{
		{"caller LANG", "xx_YY.UTF-8", "xx_YY.UTF-8"},
		{"no LANG", "", "en_US.UTF-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newFireWorld(t, bin)
			if tc.lang != "" {
				w.extraEnv = []string{"LANG=" + tc.lang}
			}
			w.install("record", script)
			id := w.newItem("env")
			sub := w.subscribe(id, "resolve", "record", "--arg", "thread=abc", "--arg", "seat_2=x y")
			w.mustRun(0, "resolve", id)
			lines := w.waitResults(id, 1)
			started, result := firePhase(lines, "started")[0], firePhase(lines, "result")[0]
			if result.State != "ok" {
				t.Fatalf("handler did not run clean:\n%s", fireDump(lines))
			}

			b, err := os.ReadFile(filepath.Join(w.home, "fire.env"))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, kv := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
				k, v, _ := strings.Cut(kv, "=")
				if strings.HasPrefix(k, "CLAUDE_") {
					t.Errorf("handler saw %s", k)
				}
				if !shellOwn[k] {
					got[k] = v
				}
			}
			want := map[string]string{
				"PATH":            "/usr/bin:/bin:" + filepath.Dir(bin) + ":" + filepath.Join(w.home, ".local", "bin"),
				"HOME":            w.home,
				"LANG":            tc.wantLang,
				"ASK_ID":          id,
				"ASK_EVENT":       "resolve",
				"ASK_SUB_ID":      sub,
				"ASK_FIRE_ID":     started.Fire,
				"ASK_FROM_STATUS": "open",
				"ASK_TO_STATUS":   "resolved",
				"ASK_AT":          started.At,
				"ASK_STORE":       w.proj,
				"ASK_ARG_THREAD":  "abc",
				"ASK_ARG_SEAT_2":  "x y",
			}
			if !reflect.DeepEqual(got, want) {
				var names []string
				for k := range got {
					names = append(names, k)
				}
				sort.Strings(names)
				t.Errorf("handler environment:\n got %v\nwant %v\n(names: %v)", got, want, names)
			}
			if strings.Contains(got["PATH"], fireMarkerPath) {
				t.Errorf("handler PATH carries the caller's: %s", got["PATH"])
			}
			pwd, err := os.ReadFile(filepath.Join(w.home, "fire.pwd"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(pwd)) != w.proj {
				t.Errorf("handler cwd %q, want the project root %q", strings.TrimSpace(string(pwd)), w.proj)
			}
		})
	}
}

// firePayloadDoc is the stdin object as a handler receives it.
type firePayloadDoc struct {
	Item  json.RawMessage   `json:"item"`
	Event string            `json:"event"`
	From  string            `json:"from"`
	To    string            `json:"to"`
	At    string            `json:"at"`
	Args  map[string]string `json:"args"`
}

// TestFireStdinIsItemAsOfFire: the handler's stdin is one JSON object
// whose item is the item after the change, as the store now holds it.
func TestFireStdinIsItemAsOfFire(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\ncat > \"$HOME/fire.stdin\"\n")
	id := w.newItem("stdin", "--body", "the body")
	w.subscribe(id, "resolve", "record", "--arg", "thread=abc")
	w.mustRun(0, "resolve", id, "--note", "done by hand")
	lines := w.waitResults(id, 1)
	started := firePhase(lines, "started")[0]

	b, err := os.ReadFile(filepath.Join(w.home, "fire.stdin"))
	if err != nil {
		t.Fatal(err)
	}
	var doc firePayloadDoc
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdin is not a JSON object: %v\n%s", err, b)
	}
	if dec.More() {
		t.Errorf("stdin holds more than one JSON value:\n%s", b)
	}
	if doc.Event != "resolve" || doc.From != "open" || doc.To != "resolved" || doc.At != started.At {
		t.Errorf("stdin event fields: %+v (started line %s)", doc, started.raw)
	}
	if !reflect.DeepEqual(doc.Args, map[string]string{"thread": "abc"}) {
		t.Errorf("stdin args: %v", doc.Args)
	}
	var got, shown any
	if err := json.Unmarshal(doc.Item, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(w.mustRun(0, "show", id, "--json")), &shown); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, shown) {
		t.Errorf("stdin item is not the stored item:\n got %s\nshow %v", doc.Item, shown)
	}
	item := got.(map[string]any)
	if item["status"] != "resolved" || item["resolution_note"] != "done by hand" || item["resolved_at"] != doc.At {
		t.Errorf("stdin item is not the item as of the fire: %s", doc.Item)
	}
}

// TestFireStdinLargerThanOnePipeBuffer: an item whose JSON is larger than
// any pipe buffer still arrives whole.
func TestFireStdinLargerThanOnePipeBuffer(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("count", "#!/bin/sh\ntee \"$HOME/fire.stdin\" | wc -c > \"$HOME/fire.count\"\n")
	body := strings.Repeat("0123456789abcdef", 8000) // 128,000 bytes
	id := w.newItem("large", "--body", body)
	w.subscribe(id, "resolve", "count")
	w.mustRun(0, "resolve", id)
	lines := w.waitResults(id, 1)
	if r := firePhase(lines, "result")[0]; r.State != "ok" {
		t.Fatalf("handler did not run clean:\n%s", fireDump(lines))
	}

	payload, err := os.ReadFile(filepath.Join(w.home, "fire.stdin"))
	if err != nil {
		t.Fatal(err)
	}
	countRaw, err := os.ReadFile(filepath.Join(w.home, "fire.count"))
	if err != nil {
		t.Fatal(err)
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countRaw)))
	if err != nil {
		t.Fatalf("handler byte count %q: %v", countRaw, err)
	}
	if len(payload) <= 100000 {
		t.Fatalf("payload is %d bytes; the test needs more than 100,000", len(payload))
	}
	if count != len(payload) {
		t.Errorf("handler counted %d bytes, payload is %d", count, len(payload))
	}
	var doc struct {
		Item core.Item `json:"item"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("payload is not whole JSON: %v", err)
	}
	if doc.Item.Body != body {
		t.Errorf("payload body is %d bytes, want %d", len(doc.Item.Body), len(body))
	}
}

// fireFailingHandlers are the ways a fire can go wrong, each with the
// state it must record. setup runs after the item is subscribed.
type fireFailure struct {
	name   string
	short  bool // needs the short-timeout build
	script string
	after  func(t *testing.T, path string)
	state  string
	exit   string
	detail *regexp.Regexp
}

func fireFailures() []fireFailure {
	return []fireFailure{
		{
			name:   "missing handler",
			script: "#!/bin/sh\nexit 0\n",
			after: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
			state: "not_run", exit: "<null>", detail: regexp.MustCompile(`^no handler named h$`),
		},
		{
			name:   "not executable",
			script: "#!/bin/sh\nexit 0\n",
			after: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			state: "not_run", exit: "<null>", detail: regexp.MustCompile(`^not runnable$`),
		},
		{
			name:   "no interpreter line",
			script: "exit 0\n",
			state:  "spawn_failed", exit: "<null>", detail: regexp.MustCompile(`exec format error`),
		},
		{
			name:   "exit 3",
			script: "#!/bin/sh\necho about to fail\nexit 3\n",
			state:  "failed", exit: "3", detail: regexp.MustCompile(`^<null>$`),
		},
		{
			name:   "overrun",
			short:  true,
			script: "#!/bin/sh\necho started\nsleep 60\n",
			state:  "timeout", exit: "<null>", detail: regexp.MustCompile(`^<null>$`),
		},
	}
}

// TestFireResultStates: each way a fire can go wrong writes its own state.
func TestFireResultStates(t *testing.T) {
	bin, short := fireBinaries(t)
	for _, tc := range fireFailures() {
		t.Run(tc.name, func(t *testing.T) {
			b := bin
			if tc.short {
				b = short
			}
			w := newFireWorld(t, b)
			path := w.install("h", tc.script)
			id := w.newItem("states")
			w.subscribe(id, "resolve", "h")
			if tc.after != nil {
				tc.after(t, path)
			}
			began := time.Now()
			w.mustRun(0, "resolve", id)
			lines := w.waitResults(id, 1)
			checkLineShapes(t, lines)
			r := firePhase(lines, "result")[0]
			if r.State != tc.state || fireExit(r) != tc.exit || !tc.detail.MatchString(fireDetail(r)) {
				t.Errorf("result: state %q exit %s detail %q; want state %q exit %s detail %s\n%s",
					r.State, fireExit(r), fireDetail(r), tc.state, tc.exit, tc.detail, r.raw)
			}
			switch tc.name {
			case "exit 3":
				if r.Output != "about to fail\n" {
					t.Errorf("output %q", r.Output)
				}
			case "overrun":
				if r.Output != "started\n" {
					t.Errorf("output %q", r.Output)
				}
				if took := time.Since(began); took > 10*time.Second {
					t.Errorf("timeout result took %s with a %s timeout", took, fireShortTimeout)
				}
				if r.DurationMs < fireShortTimeout.Milliseconds() {
					t.Errorf("duration_ms %d is under the %s timeout", r.DurationMs, fireShortTimeout)
				}
			}
			if len(firePhase(lines, "started")) != 1 || len(firePhase(lines, "result")) != 1 {
				t.Errorf("want one started and one result line:\n%s", fireDump(lines))
			}
		})
	}
}

func pidGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pid in %s after 10s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestFireTimeoutKillsHandlerAndChild: a handler that ignores SIGTERM and
// has a child does not outlive the timeout, and neither does the child.
func TestFireTimeoutKillsHandlerAndChild(t *testing.T) {
	_, short := fireBinaries(t)
	w := newFireWorld(t, short)
	w.install("stubborn", `#!/bin/sh
trap '' TERM
sleep 300 &
echo $! > "$HOME/child.pid"
echo $$ > "$HOME/handler.pid"
wait
`)
	id := w.newItem("stubborn")
	w.subscribe(id, "resolve", "stubborn")
	w.mustRun(0, "resolve", id)
	handler := readPid(t, filepath.Join(w.home, "handler.pid"))
	child := readPid(t, filepath.Join(w.home, "child.pid"))
	if pidGone(handler) || pidGone(child) {
		t.Fatalf("handler %d or child %d is already gone before the timeout", handler, child)
	}
	lines := w.waitResults(id, 1)
	if r := firePhase(lines, "result")[0]; r.State != "timeout" {
		t.Fatalf("result state %q, want timeout\n%s", r.State, r.raw)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !(pidGone(handler) && pidGone(child)) {
		if time.Now().After(deadline) {
			t.Fatalf("after the timeout result: handler %d gone=%v, child %d gone=%v", handler, pidGone(handler), child, pidGone(child))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestFireChildHoldingOutputDoesNotHoldRunner: a handler that exits while
// something it started still holds its output gets its result promptly,
// from the handler's own exit.
func TestFireChildHoldingOutputDoesNotHoldRunner(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("leaky", "#!/bin/sh\necho handler done\nsleep 20 &\necho $! > \"$HOME/child.pid\"\nexit 0\n")
	id := w.newItem("leaky")
	w.subscribe(id, "resolve", "leaky")
	began := time.Now()
	w.mustRun(0, "resolve", id)
	lines := w.waitResults(id, 1)
	took := time.Since(began)
	t.Cleanup(func() { _ = syscall.Kill(readPid(t, filepath.Join(w.home, "child.pid")), syscall.SIGKILL) })
	r := firePhase(lines, "result")[0]
	if r.State != "ok" || r.Output != "handler done\n" {
		t.Errorf("result %s", r.raw)
	}
	if took > 10*time.Second {
		t.Errorf("result took %s; the child holding the output kept the runner", took)
	}
}

var resolvedAtRe = regexp.MustCompile(`"resolved_at": "[^"]+"`)

// TestFireNeverChangesTheWrite: for each failing handler, the resolve that
// fired it exits the same, prints the same, and leaves the same item file
// as the same resolve of the same item with no subscription. The only
// bytes that differ between the two files are the resolved_at instant,
// which is each run's own clock reading.
func TestFireNeverChangesTheWrite(t *testing.T) {
	bin, short := fireBinaries(t)
	for _, tc := range fireFailures() {
		t.Run(tc.name, func(t *testing.T) {
			b := bin
			if tc.short {
				b = short
			}
			w := newFireWorld(t, b)
			id := w.newItem("unchanged", "--body", "body")

			// The control: a second project holding a copy of the same
			// item file, never subscribed.
			plain := &fireWorld{t: t, bin: b, home: w.home, proj: filepath.Join(filepath.Dir(w.proj), "plain")}
			if err := os.MkdirAll(plain.proj, 0o755); err != nil {
				t.Fatal(err)
			}
			plain.mustRun(0, "init")
			orig, err := os.ReadFile(w.itemPath(id))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(plain.itemPath(id), orig, 0o644); err != nil {
				t.Fatal(err)
			}

			path := w.install("h", tc.script)
			w.subscribe(id, "resolve", "h")
			if tc.after != nil {
				tc.after(t, path)
			}

			code, stdout, stderr := w.run("resolve", id, "--note", "n")
			atReturn, err := os.ReadFile(w.itemPath(id))
			if err != nil {
				t.Fatal(err)
			}
			pcode, pstdout, pstderr := plain.run("resolve", id, "--note", "n")
			want, err := os.ReadFile(plain.itemPath(id))
			if err != nil {
				t.Fatal(err)
			}
			if code != pcode || stdout != pstdout || stderr != pstderr {
				t.Errorf("resolve with a failing handler: exit %d stdout %q stderr %q\nwithout a subscription: exit %d stdout %q stderr %q",
					code, stdout, stderr, pcode, pstdout, pstderr)
			}
			if code != 0 {
				t.Errorf("resolve exit %d, want 0", code)
			}

			lines := w.waitResults(id, 1)
			if r := firePhase(lines, "result")[0]; r.State != tc.state {
				t.Fatalf("result state %q, want %q: the handler did not fail the way this case means", r.State, tc.state)
			}
			afterFire, err := os.ReadFile(w.itemPath(id))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(atReturn, afterFire) {
				t.Errorf("item file changed after resolve returned:\n at return: %s\nafter fire: %s", atReturn, afterFire)
			}
			norm := func(b []byte) []byte {
				if !resolvedAtRe.Match(b) {
					t.Fatalf("item file has no resolved_at:\n%s", b)
				}
				return resolvedAtRe.ReplaceAll(b, []byte(`"resolved_at": "T"`))
			}
			if !bytes.Equal(norm(afterFire), norm(want)) {
				t.Errorf("item file differs from the unsubscribed resolve:\n got %s\nwant %s", afterFire, want)
			}
			if plainLines := plain.fireLines(id); plainLines != nil {
				t.Errorf("the unsubscribed project has a fire record:\n%s", fireDump(plainLines))
			}
		})
	}
}

// TestFireResolverReturnsWhileHandlerRuns: resolve does not wait for the
// handler, even with its stdout piped to a reader that waits for EOF.
func TestFireResolverReturnsWhileHandlerRuns(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("slow", "#!/bin/sh\nsleep 3\necho slept\n")
	id := w.newItem("slow")
	w.subscribe(id, "resolve", "slow")

	c := w.cmd("resolve", id)
	stdout, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(stdout) // returns at EOF: when nothing holds the pipe
	if err := c.Wait(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	took := time.Since(began)
	if took >= time.Second {
		t.Errorf("resolve took %s to return and close its stdout; want under 1s", took)
	}
	if want := id + ": open -> resolved\n"; string(out) != want {
		t.Errorf("resolve stdout %q, want %q", out, want)
	}
	if n := len(firePhase(w.fireLines(id), "result")); n != 0 {
		t.Errorf("%d result line(s) already present when resolve returned after %s", n, took)
	}
	lines := w.waitResults(id, 1)
	r := firePhase(lines, "result")[0]
	if r.State != "ok" || r.Output != "slept\n" || r.DurationMs < 3000 {
		t.Errorf("result %s", r.raw)
	}
}

// TestFireSurvivesProcessGroupKill: a supervisor that signals the
// resolver's process group once the resolver has exited does not stop the
// fire. The runner is in a session of its own.
func TestFireSurvivesProcessGroupKill(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("late", "#!/bin/sh\nsleep 1\necho ran > \"$HOME/fire.marker\"\n")
	id := w.newItem("group")
	w.subscribe(id, "resolve", "late")

	c := w.cmd("resolve", id)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Run(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// ESRCH is the expected answer: the group emptied when the resolver
	// exited. Anything still in it would get the signal.
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signal the resolver's process group: %v", err)
	}

	lines := w.waitResults(id, 1)
	if r := firePhase(lines, "result")[0]; r.State != "ok" {
		t.Errorf("result %s", r.raw)
	}
	if b, err := os.ReadFile(filepath.Join(w.home, "fire.marker")); err != nil || string(b) != "ran\n" {
		t.Errorf("handler marker: %q, %v", b, err)
	}
}

// TestFireRunnerDoesNotHoldLock: while a handler runs, the store takes
// another write at once.
func TestFireRunnerDoesNotHoldLock(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("slow", "#!/bin/sh\nsleep 3\n")
	id := w.newItem("lock")
	w.subscribe(id, "resolve", "slow")
	w.mustRun(0, "resolve", id)

	began := time.Now()
	w.mustRun(0, "update", id, "--title", "retitled while the handler sleeps")
	took := time.Since(began)
	if took >= time.Second {
		t.Errorf("update took %s while a handler was running; want under 1s", took)
	}
	if n := len(firePhase(w.fireLines(id), "result")); n != 0 {
		t.Fatalf("the handler had already finished (%d result lines); the update proved nothing", n)
	}
	w.waitResults(id, 1)
}

// TestFireNoOpFiresNothing: a call that changes no status starts no fire
// and creates no fires directory.
func TestFireNoOpFiresNothing(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\necho ran >> \"$HOME/fire.log\"\n")
	id := w.newItem("noop")
	w.subscribe(id, "any", "record")

	w.mustRun(6, "reopen", id)                  // already open
	w.mustRun(0, "update", id, "--title", "t2") // a change, but not of status
	if _, err := os.Stat(filepath.Join(w.proj, ".ask", "fires")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".ask/fires exists before any status change (stat err %v)", err)
	}

	w.mustRun(0, "resolve", id)
	w.waitResults(id, 1)
	w.mustRun(6, "resolve", id) // already resolved
	w.mustRun(0, "close", id)
	w.waitResults(id, 2)
	w.mustRun(6, "close", id) // already closed
	w.mustRun(2, "resolve", id)
	w.mustRun(2, "reopen", id)
	time.Sleep(300 * time.Millisecond)
	lines := w.fireLines(id)
	if len(lines) != 4 {
		t.Errorf("want 4 record lines (two fires), got %d:\n%s", len(lines), fireDump(lines))
	}
	b, _ := os.ReadFile(filepath.Join(w.home, "fire.log"))
	if string(b) != "ran\nran\n" {
		t.Errorf("handler ran %q, want twice", b)
	}
}

// TestFireSubscribeVersusResolve races a subscribe against a resolve of
// the same item, 50 times. Whoever takes the store lock first decides:
// either the subscription was there for the resolve and fires exactly
// once, or the subscriber is told (exit 8) that the item was already
// resolved and nothing fires. Never both, never neither.
func TestFireSubscribeVersusResolve(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\necho \"$ASK_ID\" >> \"$HOME/fire.log\"\n")

	const pairs = 50
	type outcome struct {
		id      string
		subCode int
	}
	var outcomes []outcome
	for i := 0; i < pairs; i++ {
		id := w.newItem(fmt.Sprintf("race %d", i))
		var wg sync.WaitGroup
		var subCode, resCode int
		var subErr, resErr string
		wg.Add(2)
		go func() {
			defer wg.Done()
			subCode, _, subErr = w.run("subscribe", id, "--on", "resolve", "--handler", "record")
		}()
		go func() {
			defer wg.Done()
			resCode, _, resErr = w.run("resolve", id)
		}()
		wg.Wait()
		if resCode != 0 {
			t.Fatalf("pair %d: resolve exit %d: %s", i, resCode, resErr)
		}
		if subCode != 0 && subCode != 8 {
			t.Fatalf("pair %d: subscribe exit %d: %s", i, subCode, subErr)
		}
		outcomes = append(outcomes, outcome{id, subCode})
	}

	fired, told := 0, 0
	for i, o := range outcomes {
		var it core.Item
		if err := json.Unmarshal([]byte(w.mustRun(0, "show", o.id, "--json")), &it); err != nil {
			t.Fatal(err)
		}
		if it.Status != core.StatusResolved {
			t.Errorf("pair %d: %s ended %s", i, o.id, it.Status)
		}
		var lines []fireLine
		if o.subCode == 0 {
			lines = w.waitResults(o.id, 1)
			fired++
		} else {
			lines = w.fireLines(o.id)
			told++
		}
		started := len(firePhase(lines, "started"))
		switch {
		case o.subCode == 0 && started != 1:
			t.Errorf("pair %d: subscribe exit 0 but %d fires on %s", i, started, o.id)
		case o.subCode == 8 && started != 0:
			t.Errorf("pair %d: subscribe exit 8 and %d fires on %s: both", i, started, o.id)
		}
	}
	t.Logf("%d pairs: %d fired once, %d subscribers told exit 8", pairs, fired, told)

	// The handler ran once per fire and never for a told subscriber.
	time.Sleep(200 * time.Millisecond)
	ran := map[string]int{}
	if f, err := os.Open(filepath.Join(w.home, "fire.log")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			ran[sc.Text()]++
		}
		_ = f.Close()
	}
	for i, o := range outcomes {
		want := 0
		if o.subCode == 0 {
			want = 1
		}
		if ran[o.id] != want {
			t.Errorf("pair %d: handler ran %d time(s) for %s, want %d", i, ran[o.id], o.id, want)
		}
	}
}

// fireArgv is a well-formed runner argument list for a fire of id.
func fireArgv(version, id, sub, fire string) []string {
	return []string{"__fire", version, id, sub, fire, "resolve", "open", "resolved", "2026-01-02T03:04:05Z"}
}

// TestFireRunnerProtocolMismatch: a runner handed a protocol version it
// does not know records that instead of guessing. This is what a
// long-lived process that outlives an upgrade of its own binary produces.
func TestFireRunnerProtocolMismatch(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\necho ran > \"$HOME/fire.marker\"\n")
	id := w.newItem("mismatch")
	sub := w.subscribe(id, "resolve", "record")

	code, stdout, stderr := w.run(fireArgv("v2", id, sub, "0123abcd")...)
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("runner: exit %d stdout %q stderr %q; want 0 and silence", code, stdout, stderr)
	}
	lines := w.fireLines(id)
	checkLineShapes(t, lines)
	if len(lines) != 1 || lines[0].Phase != "result" || lines[0].State != "spawn_failed" ||
		fireDetail(lines[0]) != "runner protocol mismatch" || lines[0].Fire != "0123abcd" || lines[0].Sub != sub {
		t.Errorf("record:\n%s", fireDump(lines))
	}
	if _, err := os.Stat(filepath.Join(w.home, "fire.marker")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the handler ran under a protocol mismatch")
	}
}

// TestFireRunnerRefusesArgumentsNoStatusChangeProduces: the runner's event,
// statuses and time reach the handler's environment and stdin, so a runner
// handed values no status change can produce records a mismatch and runs
// nothing.
func TestFireRunnerRefusesArgumentsNoStatusChangeProduces(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\necho ran > \"$HOME/fire.marker\"\n")
	id := w.newItem("bad args")
	sub := w.subscribe(id, "resolve,reopen", "record")

	bad := [][]string{
		{"resolve\nINJECTED=1", "open", "resolved", "2026-01-02T03:04:05Z"},
		{"close", "open", "resolved", "2026-01-02T03:04:05Z"},
		{"resolve", "open", "done", "2026-01-02T03:04:05Z"},
		{"resolve", "resolved", "resolved", "2026-01-02T03:04:05Z"},
		{"resolve", "open", "resolved", "yesterday"},
		// A real transition, but not one this subscription names.
		{"close", "open", "closed", "2026-01-02T03:04:05Z"},
	}
	for _, tail := range bad {
		w.mustRun(0, append([]string{"__fire", "v1", id, sub, "0123abcd"}, tail...)...)
	}
	lines := w.fireLines(id)
	checkLineShapes(t, lines)
	if len(lines) != len(bad) {
		t.Fatalf("%d record lines for %d runs:\n%s", len(lines), len(bad), fireDump(lines))
	}
	for _, l := range lines {
		if l.State != "spawn_failed" || fireDetail(l) != "runner protocol mismatch" {
			t.Errorf("record line %s", l.raw)
		}
	}
	if _, err := os.Stat(filepath.Join(w.home, "fire.marker")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the handler ran on arguments no status change produces")
	}
}

// TestFireRunnerRechecksStoredArguments: a subs file edited by hand into
// arguments subscribe would refuse does not put them in a handler's
// environment.
func TestFireRunnerRechecksStoredArguments(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\necho ran > \"$HOME/fire.marker\"\n")
	id := w.newItem("edited subs")
	w.subscribe(id, "resolve", "record", "--arg", "thread=abc")
	subsPath := filepath.Join(w.proj, ".ask", "subs", id+".json")
	b, err := os.ReadFile(subsPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), `"thread"`, `"x=1\nLD_PRELOAD"`, 1)
	if edited == string(b) {
		t.Fatalf("subs file has no thread argument to edit:\n%s", b)
	}
	if err := os.WriteFile(subsPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	w.mustRun(0, "resolve", id)
	lines := w.waitResults(id, 1)
	r := firePhase(lines, "result")[0]
	if r.State != "spawn_failed" || !strings.Contains(fireDetail(r), "invalid argument key") {
		t.Errorf("result line %s", r.raw)
	}
	if _, err := os.Stat(filepath.Join(w.home, "fire.marker")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the handler ran with arguments subscribe would refuse")
	}
}

// TestFireSubscriptionRemovedBeforeRun: a subscription removed between the
// status change and the runner's start is not run, and the record says so.
func TestFireSubscriptionRemovedBeforeRun(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("record", "#!/bin/sh\necho ran > \"$HOME/fire.marker\"\n")
	id := w.newItem("removed")
	sub := w.subscribe(id, "resolve", "record")
	w.mustRun(0, "unsubscribe", id, sub)

	w.mustRun(0, fireArgv("v1", id, sub, "0123abcd")...)
	lines := w.fireLines(id)
	checkLineShapes(t, lines)
	if len(lines) != 1 || lines[0].State != "not_run" || fireDetail(lines[0]) != "subscription removed before it ran" {
		t.Errorf("record:\n%s", fireDump(lines))
	}
	if _, err := os.Stat(filepath.Join(w.home, "fire.marker")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the handler of a removed subscription ran")
	}
}

// TestFireRunnerVerbHidden: the runner verb is not in help, and with
// arguments that name no fire it writes nothing anywhere.
func TestFireRunnerVerbHidden(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	if help := w.mustRun(0, "help"); strings.Contains(help, "__fire") {
		t.Errorf("help lists the runner verb:\n%s", help)
	}
	id := w.newItem("hidden")
	for _, args := range [][]string{
		{"__fire"},
		{"__fire", "v1", id},
		{"__fire", "v1", "../../etc", "0123abcd", "0123abcd", "resolve", "open", "resolved", "2026-01-02T03:04:05Z"},
		{"__fire", "v1", id, "nothex!!", "0123abcd", "resolve", "open", "resolved", "2026-01-02T03:04:05Z"},
	} {
		code, stdout, stderr := w.run(args...)
		if code != 2 || stdout != "" || stderr != "" {
			t.Errorf("ask %v: exit %d stdout %q stderr %q; want 2 and silence", args, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(w.proj, ".ask", "fires")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".ask/fires was created by a runner that named no fire")
	}
}

// TestFireOutputKeepsTheLastBytes: the result line keeps the last 4096
// bytes of the handler's combined stdout and stderr.
func TestFireOutputKeepsTheLastBytes(t *testing.T) {
	bin, _ := fireBinaries(t)
	w := newFireWorld(t, bin)
	w.install("loud", "#!/bin/sh\ni=0\nwhile [ $i -lt 1000 ]; do echo \"line $i\"; i=$((i+1)); done\necho to-stderr >&2\necho LAST\n")
	id := w.newItem("loud")
	w.subscribe(id, "resolve", "loud")
	w.mustRun(0, "resolve", id)
	lines := w.waitResults(id, 1)
	r := firePhase(lines, "result")[0]
	if len(r.Output) != core.MaxFireOutput {
		t.Errorf("output is %d bytes, want %d", len(r.Output), core.MaxFireOutput)
	}
	if !strings.HasSuffix(r.Output, "line 999\nto-stderr\nLAST\n") {
		t.Errorf("output does not end with the handler's last lines: %q", r.Output[len(r.Output)-60:])
	}
}
