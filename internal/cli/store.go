package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/aac/ask/internal/core"
)

// openStore opens the store rooted at root, reporting any failure on
// stderr with the `ask <verb>: ` prefix and returning the exit code the
// caller should propagate.
//
// The split that matters is between the two config-less shapes (spec §2,
// act-55ae5b):
//
//   - no config.json and no items → exit 5, "ask not initialized". The
//     directory is not a store and never was.
//   - no config.json but items present → exit 7, the stranded-store
//     message with the item count. Those asks are still on disk and still
//     want a human; reporting them as "not initialized" is how a full
//     inbox gets counted as zero.
//
// Consumers branch on 5 vs 7, not on the English.
func openStore(verb, root string) (*core.FileStore, int) {
	store, err := core.OpenStore(root, nil)
	if err == nil {
		return store, 0
	}
	fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
	return nil, storeErrExit(err)
}

// storeErrExit maps an OpenStore failure to its spec §2 exit code: 7 for a
// stranded store, 5 for every other I/O or not-initialized failure.
func storeErrExit(err error) int {
	var stranded *core.StrandedStoreError
	if errors.As(err, &stranded) {
		return 7
	}
	return 5
}

// openStoreCwd is openStore rooted at the current working directory — the
// shape every verb but `harvest --from` needs.
func openStoreCwd(verb string) (*core.FileStore, int) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ask %s: %v\n", verb, err)
		return nil, 5
	}
	return openStore(verb, cwd)
}
