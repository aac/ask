package cli

import (
	"encoding/json"
	"fmt"
	"os"
)

// emitJSON prints v as indented JSON on stdout. Used by `--json` flag
// handlers across CLI verbs (spec §1.4, §1.5, §1.6).
func emitJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	fmt.Println(string(b))
}
