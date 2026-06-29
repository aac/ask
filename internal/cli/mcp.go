// Package cli — `ask mcp` is the entry point for the stdio JSON-RPC server
// implemented in internal/mcp. The CLI wrapper is intentionally thin: it
// chains into mcp.Run(), which owns the read/dispatch/write loop and
// translates tool calls into core operations.
package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/aac/ask/internal/mcp"
)

// runMCP delegates to internal/mcp.Run. Exit codes per spec §2 are
// proxied as-is (0 on clean EOF, 5 on transport-level I/O failure).
//
// Flags:
//
//	--workdir DIR   chdir to DIR before serving. Escape hatch for hosts
//	                whose MCP launcher cwd is not the user's workspace
//	                (Codex Desktop / Codex VS Code upstream bugs
//	                openai/codex#9989, #16390). Mirrors `act mcp --workdir`.
//
// Exit 2 on bad flag or chdir failure (validation error per spec §2).
func runMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	workdir := fs.String("workdir", "", "chdir to DIR before serving")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *workdir != "" {
		if err := os.Chdir(*workdir); err != nil {
			fmt.Fprintf(os.Stderr, "ask mcp: chdir %s: %v\n", *workdir, err)
			return 2
		}
	}

	return mcp.Run()
}
