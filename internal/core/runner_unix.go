//go:build unix

package core

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// FireTimeout is how long a handler may run. At the timeout its process
// group is sent SIGTERM, and SIGKILL fireKillGrace later.
const FireTimeout = 60 * time.Second

// fireKillGrace is the pause between the SIGTERM and the SIGKILL of a
// handler that ran into the timeout.
const fireKillGrace = 2 * time.Second

// fireDrainGrace is how long the runner keeps reading a finished handler's
// output when something the handler started still holds it open.
const fireDrainGrace = 2 * time.Second

// fireTimeoutOverride replaces FireTimeout when it parses as a duration.
// It is empty in every shipped build; the boundary tests set it at link
// time (-ldflags -X) to reach the timeout path without a minute's wait.
var fireTimeoutOverride string

func fireTimeout() time.Duration {
	if d, err := time.ParseDuration(fireTimeoutOverride); err == nil && d > 0 {
		return d
	}
	return FireTimeout
}

// runnerExecutable is the file a fire re-executes: this binary. Inside a
// test binary there is no ask to re-execute (the test binary would run its
// tests again), so it reports an error and the fire is recorded as
// spawn_failed.
var runnerExecutable = func() (string, error) {
	if testing.Testing() {
		return "", errors.New("no runner inside a test binary")
	}
	return os.Executable()
}

// startRunner starts `ask __fire <argv...>` detached and returns without
// waiting for it: working directory the project root, standard input,
// output and error all /dev/null, no other descriptor inherited (every
// descriptor this process opens is close-on-exec), and in a new session.
//
// The new session is deliberate. A process supervisor that kills a
// finished job's process group, or a terminal that hangs up its session,
// would otherwise take the runner down with the command that started it,
// before the handler has run.
func startRunner(projectRoot string, argv []string) error {
	exe, err := runnerExecutable()
	if err != nil {
		return err
	}
	cmd := &exec.Cmd{
		Path:        exe,
		Args:        append([]string{exe}, argv...),
		Dir:         projectRoot,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the runner if this process outlives it (a long-lived server);
	// a command that exits first leaves it to init.
	go func() { _ = cmd.Wait() }()
	return nil
}

// firePayload is the JSON object a handler reads on standard input. Field
// order is the key order.
type firePayload struct {
	Item  *Item             `json:"item"`
	Event string            `json:"event"`
	From  string            `json:"from"`
	To    string            `json:"to"`
	At    string            `json:"at"`
	Args  map[string]string `json:"args"`
}

// RunFire is the runner: the body of the hidden verb
//
//	ask __fire v1 <item> <sub> <fire> <event> <from> <to> <at>
//
// args is everything after the verb and projectRoot the directory holding
// .ask/, which is also the runner's working directory. It loads the item
// and the subscription itself, runs the handler, and appends the fire's
// result line. It takes no store lock at any point.
//
// The return value is the runner's exit code, which nothing reads: 0 once
// a result line is written, 2 when the arguments do not even name a fire,
// 5 when the store or the record cannot be reached.
func RunFire(projectRoot string, args []string) int {
	began := time.Now()
	if len(args) < 4 {
		return 2
	}
	version, itemID, subID, fireID := args[0], args[1], args[2], args[3]
	if checkID(itemID) != nil || !hex8Re.MatchString(subID) || !hex8Re.MatchString(fireID) {
		return 2
	}
	projectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return 5
	}
	store, err := OpenStore(projectRoot, nil)
	if err != nil {
		return 5
	}
	result := func(state string, exitCode *int, detail, output string) int {
		if err := store.appendFireResult(itemID, fireID, subID, state, exitCode, detail, began, output); err != nil {
			return 5
		}
		return 0
	}
	if version != RunnerProtocol || len(args) != 8 {
		return result(FireSpawnFailed, nil, FireDetailProtocolMismatch, "")
	}
	event, from, to, at := args[4], args[5], args[6], args[7]
	// These four reach the handler's environment and stdin, so they are
	// held to what a status change can produce.
	if _, err := time.Parse(time.RFC3339Nano, at); err != nil ||
		!Status(from).Valid() || !Status(to).Valid() || transitionEvent(Status(from), Status(to)) != event {
		return result(FireSpawnFailed, nil, FireDetailProtocolMismatch, "")
	}

	subs, err := store.LoadSubs(itemID)
	if err != nil {
		return result(FireSpawnFailed, nil, err.Error(), "")
	}
	var sub *Subscription
	for i := range subs.Subscriptions {
		if subs.Subscriptions[i].ID == subID {
			sub = &subs.Subscriptions[i]
			break
		}
	}
	if sub == nil {
		return result(FireNotRun, nil, FireDetailSubRemoved, "")
	}
	// The subs file is read from disk, not trusted. The subscription has
	// to name the event it is being run for, and its arguments become
	// environment variable names and values.
	if !sub.Names(event) {
		return result(FireSpawnFailed, nil, FireDetailProtocolMismatch, "")
	}
	if err := ValidateSubArgs(sub.Args); err != nil {
		return result(FireSpawnFailed, nil, err.Error(), "")
	}
	it, err := store.Load(itemID)
	if err != nil {
		return result(FireSpawnFailed, nil, err.Error(), "")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return result(FireSpawnFailed, nil, err.Error(), "")
	}
	// The handler is started by its path under the callbacks directory,
	// not a symlink's target, so $0 keeps the name it was subscribed by.
	path := filepath.Join(home, ".config", "ask", "callbacks", sub.Handler)
	fi, err := os.Stat(path)
	switch {
	case ValidateHandlerName(sub.Handler) != nil || err != nil:
		return result(FireNotRun, nil, "no handler named "+sub.Handler, "")
	case !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0:
		return result(FireNotRun, nil, FireDetailNotRunnable, "")
	}

	stdin, err := json.Marshal(firePayload{Item: it, Event: event, From: from, To: to, At: at, Args: sub.Args})
	if err != nil {
		return result(FireSpawnFailed, nil, err.Error(), "")
	}
	env, err := handlerEnv(home, projectRoot, itemID, sub, fireID, event, from, to, at)
	if err != nil {
		return result(FireSpawnFailed, nil, err.Error(), "")
	}
	state, exitCode, detail, output := runHandler(path, projectRoot, env, stdin, fireTimeout())
	return result(state, exitCode, detail, output)
}

// handlerEnv builds a handler's whole environment. Nothing is inherited
// from the process that changed the item except HOME and LANG.
func handlerEnv(home, projectRoot, itemID string, sub *Subscription, fireID, event, from, to, at string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "en_US.UTF-8"
	}
	env := []string{
		"PATH=/usr/bin:/bin:" + filepath.Dir(exe) + ":" + filepath.Join(home, ".local", "bin"),
		"HOME=" + home,
		"LANG=" + lang,
		"ASK_ID=" + itemID,
		"ASK_EVENT=" + event,
		"ASK_SUB_ID=" + sub.ID,
		"ASK_FIRE_ID=" + fireID,
		"ASK_FROM_STATUS=" + from,
		"ASK_TO_STATUS=" + to,
		"ASK_AT=" + at,
		"ASK_STORE=" + projectRoot,
	}
	keys := make([]string, 0, len(sub.Args))
	for k := range sub.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, "ASK_ARG_"+strings.ToUpper(k)+"="+sub.Args[k])
	}
	return env, nil
}

// runHandler runs one handler to its end or to the timeout and reports the
// result line's state, exit code, detail and output.
//
// The handler runs in its own process group so that the timeout can signal
// it together with everything it started. Its standard input is a pipe the
// runner keeps writing for as long as the handler reads, so a payload
// larger than one pipe buffer arrives whole; its standard output and error
// share one pipe, of which the last MaxFireOutput bytes are kept.
func runHandler(path, dir string, env []string, stdin []byte, timeout time.Duration) (state string, exitCode *int, detail, output string) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return FireSpawnFailed, nil, err.Error(), ""
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return FireSpawnFailed, nil, err.Error(), ""
	}
	cmd := &exec.Cmd{
		Path:        path,
		Args:        []string{path},
		Dir:         dir,
		Env:         env,
		Stdin:       inR,
		Stdout:      outW,
		Stderr:      outW,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	err = cmd.Start()
	_ = inR.Close()
	_ = outW.Close()
	if err != nil {
		_ = inW.Close()
		_ = outR.Close()
		return FireSpawnFailed, nil, err.Error(), ""
	}
	pgid := cmd.Process.Pid

	go func() {
		// A handler that exits without reading leaves this write failing
		// with EPIPE, which is its business.
		_, _ = inW.Write(stdin)
		_ = inW.Close()
	}()
	tail := &tailBuffer{max: MaxFireOutput}
	drained := make(chan struct{})
	go func() {
		_, _ = io.Copy(tail, outR)
		close(drained)
	}()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	var werr error
	timedOut := false
	select {
	case werr = <-waited:
	case <-time.After(timeout):
		timedOut = true
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		reaped := false
		deadline := time.After(fireKillGrace)
	grace:
		for {
			select {
			case werr = <-waited:
				reaped = true
			case <-deadline:
				break grace
			case <-time.After(20 * time.Millisecond):
			}
			// With the handler reaped, the group is gone once nothing
			// it started is left.
			if reaped && errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
				break grace
			}
		}
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		if !reaped {
			werr = <-waited
		}
	}

	// The handler is gone. Something it started may still hold its output
	// or its input; neither keeps the runner past a short grace.
	select {
	case <-drained:
	case <-time.After(fireDrainGrace):
	}
	_ = outR.Close()
	_ = inW.Close()
	<-drained
	output = tail.String()

	if timedOut {
		return FireTimedOut, nil, "", output
	}
	if werr == nil {
		zero := 0
		return FireOK, &zero, "", output
	}
	var exitErr *exec.ExitError
	if errors.As(werr, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return FireFailed, &code, "", output
		}
		return FireFailed, nil, exitErr.String(), output
	}
	return FireFailed, nil, werr.Error(), output
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }
