//go:build unix

package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockFile is the store's one advisory lock, at <root>/.ask/.lock. It is
// created by the first write that takes it and never removed; its content
// is never read.
const lockFile = ".lock"

// LockTimeout is how long a writer waits for the store lock before giving
// up. Every locked section is a load, a change and a save of one small
// file, so the wait only runs out when another process is stuck while
// holding the lock. A variable so tests can shorten it.
var LockTimeout = 10 * time.Second

// lockPollInterval is the pause between non-blocking attempts on the lock.
const lockPollInterval = 25 * time.Millisecond

// ErrStoreLocked is returned (wrapped, with the time waited) when the store
// lock could not be taken within LockTimeout. Nothing has been written when
// it is returned. The CLI maps it to exit 5 and MCP to code 5 (spec §2).
var ErrStoreLocked = errors.New("store is locked by another ask process")

// StoreLock is a held store lock. Release it with Unlock.
type StoreLock struct {
	f *os.File
}

// Lock takes the store's exclusive advisory lock (spec §8), polling a
// non-blocking flock every lockPollInterval for at most LockTimeout. Writers
// hold it across their whole load, change, save sequence; reads never call
// it, so a read neither waits on a writer nor creates the lock file.
//
// The lock belongs to the open file description, so a second Lock in the
// same process contends with the first exactly as another process would.
func (s *FileStore) Lock() (*StoreLock, error) {
	path := filepath.Join(s.root, lockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	timeout := LockTimeout
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &StoreLock{f: f}, nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%w (waited %s); nothing was written", ErrStoreLocked, timeout)
		}
		time.Sleep(lockPollInterval)
	}
}

// Unlock releases the lock. Closing the descriptor is what drops it, so a
// process that dies while holding the lock releases it too.
func (l *StoreLock) Unlock() {
	_ = l.f.Close()
}
