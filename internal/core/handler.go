package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// handlerNameRe is the rule for a callback handler's name. It is also the
// path-safety guarantee: no separator, no dot-only name, no leading dash,
// and lowercase only, so two names can never address one file on a
// case-insensitive filesystem.
var handlerNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ErrBadHandlerName is returned by ValidateHandlerName for a name that
// breaks handlerNameRe.
var ErrBadHandlerName = errors.New("invalid handler name")

// ErrUnknownHandler is returned by CheckHandler when no usable executable
// is installed under the name.
var ErrUnknownHandler = errors.New("unknown handler")

// ValidateHandlerName checks a handler name against the name rule
// `^[a-z0-9][a-z0-9_-]{0,63}$`.
func ValidateHandlerName(name string) error {
	if !handlerNameRe.MatchString(name) {
		return fmt.Errorf("%w %q (want ^[a-z0-9][a-z0-9_-]{0,63}$)", ErrBadHandlerName, name)
	}
	return nil
}

// CallbacksDir is the directory handlers are installed in, on the host
// where the store lives: ~/.config/ask/callbacks.
func CallbacksDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate callbacks directory: %w", err)
	}
	return filepath.Join(home, ".config", "ask", "callbacks"), nil
}

// CheckHandler reports whether name is an installed handler: the name
// passes the name rule, and <CallbacksDir>/<name> is, after following
// symlinks on purpose, a regular file with an execute bit. It returns the
// handler's path (under the callbacks directory, not the symlink's target).
func CheckHandler(name string) (string, error) {
	if err := ValidateHandlerName(name); err != nil {
		return "", err
	}
	dir, err := CallbacksDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%w %q: %s does not exist", ErrUnknownHandler, name, path)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%w %q: %s is not a regular file", ErrUnknownHandler, name, path)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%w %q: %s is not executable", ErrUnknownHandler, name, path)
	}
	return path, nil
}

// InstalledHandlers lists, sorted, the names under the callbacks directory
// that CheckHandler accepts. A missing directory lists nothing.
func InstalledHandlers() []string {
	dir, err := CallbacksDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if _, err := CheckHandler(e.Name()); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
