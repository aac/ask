package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aac/ask/internal/core"
)

// runConcurrently starts n calls of Run at once, each with the argv built
// by argv(i), and returns their exit codes. Stdout and stderr are captured
// for the whole batch so the writers' output stays out of the test log.
func runConcurrently(t *testing.T, n int, argv func(i int) []string) []int {
	t.Helper()
	codes := make([]int, n)
	captureStderr(t, func() {
		captureStdout(t, func() {
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					codes[i] = Run(argv(i))
				}(i)
			}
			close(start)
			wg.Wait()
		})
	})
	return codes
}

// assertNoTempFiles fails if an interrupted or abandoned atomic write left
// a temp file in the store's items directory.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".ask", "items"))
	if err != nil {
		t.Fatalf("read items dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

// TestConcurrentUpdatesAllLand runs twenty `update --body-append` writers
// on one item at once. Each is a load, append, save; without the store lock
// held across all three, two writers load the same body and the later save
// drops the earlier note.
func TestConcurrentUpdatesAllLand(t *testing.T) {
	dir := seedOne(t, "ask-1111", "Set up OAuth", "original")
	const writers = 20

	codes := runConcurrently(t, writers, func(i int) []string {
		return []string{"update", "ask-1111", "--body-append", fmt.Sprintf("note-%02d", i)}
	})
	for i, code := range codes {
		if code != 0 {
			t.Errorf("writer %d: exit %d, want 0", i, code)
		}
	}

	body := loadItem(t, dir, "ask-1111").Body
	if !strings.HasPrefix(body, "original") {
		t.Errorf("original body lost: %q", body)
	}
	for i := 0; i < writers; i++ {
		note := fmt.Sprintf("note-%02d", i)
		if n := strings.Count(body, note); n != 1 {
			t.Errorf("%s appears %d times in the final body, want 1", note, n)
		}
	}
	assertNoTempFiles(t, dir)
}

// TestConcurrentResolveOneWinner runs ten resolvers of one open item at
// once. Exactly one makes the transition (exit 0); the other nine find it
// already resolved (exit 6). Without the lock several can each load the
// item as open and each report the transition as theirs.
func TestConcurrentResolveOneWinner(t *testing.T) {
	dir := seedOne(t, "ask-2222", "Invite the bot", "")
	const resolvers = 10

	codes := runConcurrently(t, resolvers, func(i int) []string {
		return []string{"resolve", "ask-2222", "--note", fmt.Sprintf("resolver-%d", i)}
	})
	count := map[int]int{}
	for _, code := range codes {
		count[code]++
	}
	if count[0] != 1 || count[6] != resolvers-1 {
		t.Errorf("exit codes %v: want exactly one 0 and %d 6s", codes, resolvers-1)
	}
	if st := loadItem(t, dir, "ask-2222").Status; st != core.StatusResolved {
		t.Errorf("final status %s, want resolved", st)
	}
	assertNoTempFiles(t, dir)
}

// TestReadsTakeNoLock pins the two halves of "reads take no lock": a read
// never creates .ask/.lock, and a read returns normally while a writer
// holds the lock.
func TestReadsTakeNoLock(t *testing.T) {
	dir := seedOne(t, "ask-3333", "Rotate the key", "body")
	lockPath := filepath.Join(dir, ".ask", ".lock")

	reads := [][]string{
		{"show", "ask-3333"},
		{"show", "ask-3333", "--json"},
		{"list"},
		{"list", "--json"},
	}
	runReads := func(when string) {
		for _, argv := range reads {
			var code int
			out := captureStdout(t, func() { code = Run(argv) })
			if code != 0 {
				t.Errorf("%s: ask %s exit %d, want 0", when, strings.Join(argv, " "), code)
			}
			if !strings.Contains(out, "ask-3333") {
				t.Errorf("%s: ask %s did not print the item: %q", when, strings.Join(argv, " "), out)
			}
		}
	}

	runReads("fresh store")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("a read created %s (stat err: %v)", lockPath, err)
	}

	// Hold the lock the way a writer would. A read that tried to take it
	// would time out (shortened here) and exit 5.
	orig := core.LockTimeout
	core.LockTimeout = 100 * time.Millisecond
	t.Cleanup(func() { core.LockTimeout = orig })
	store, err := core.OpenStore(dir, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	held, err := store.Lock()
	if err != nil {
		t.Fatalf("take lock: %v", err)
	}
	defer held.Unlock()

	runReads("lock held")

	// The held lock is real: a write is refused with exit 5 while it stands.
	var code int
	stderr := captureStderr(t, func() {
		captureStdout(t, func() { code = Run([]string{"resolve", "ask-3333"}) })
	})
	if code != 5 {
		t.Fatalf("resolve with the lock held: exit %d, want 5", code)
	}
	want := "ask resolve: store is locked by another ask process (waited 100ms); nothing was written\n"
	if stderr != want {
		t.Fatalf("stderr:\n got %q\nwant %q", stderr, want)
	}
}
