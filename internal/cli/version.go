package cli

import (
	"fmt"

	"github.com/aac/ask/internal/version"
)

// BinaryVersion is the build-time version, mirrored from internal/version
// for backwards compatibility with anything reading it from this package.
// The canonical injection target is internal/version.Binary.
var BinaryVersion = version.Binary

func runVersion(_ []string) int {
	fmt.Println(version.Binary)
	return 0
}
