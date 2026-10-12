// Package cli — the hidden `ask __fire` verb. A status change that fires a
// subscription re-executes ask as this verb, detached, once per
// subscription; it runs the handler and writes the fire's result line.
// Nobody types it, and it is not in help.
package cli

import (
	"os"

	"github.com/aac/ask/internal/core"
)

// runFire implements
//
//	ask __fire v1 <item> <sub> <fire> <event> <from> <to> <at>
//
// run with the project root as its working directory. It prints nothing:
// its standard streams are /dev/null and everything it has to say goes
// into the fire record. See core.RunFire.
func runFire(args []string) int {
	cwd, err := os.Getwd()
	if err != nil {
		return 5
	}
	return core.RunFire(cwd, args)
}
