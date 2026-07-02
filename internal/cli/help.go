package cli

import (
	"fmt"
	"os"
)

const topLevelHelp = `ask — agent-to-human request inbox

Usage: ask <command> [flags]

Commands:
  init           Initialize .ask/ in this project.
  new            File a new request.
  list           List items in this project.
  show           Show one item's full detail.
  resolve        Mark an item resolved (typically called by an agent).
  reopen         Reopen a resolved item (verifier failed).
  close          Close an item (final, or cancel/dismiss from open).
  harvest        Copy items from a worktree's .ask/ into this store
                 (for orchestrators surfacing worktree-filed asks).
  mcp            Start MCP server on stdio.
  help [topic]   Show this help, or a topic: workflow, verifier, mcp, schema.
  version        Print binary version.

First time? See README.md for setup. The ask skill ships with the plugin
(/plugin install ask@ask); a source install copies it from the checkout.

For the agent-facing rules and discipline, see the ask skill at
~/.claude/skills/ask/SKILL.md.
`

func runHelp(args []string) int {
	if len(args) == 0 {
		fmt.Print(topLevelHelp)
		return 0
	}
	topic := args[0]
	body, ok := helpTopics[topic]
	if !ok {
		fmt.Fprintf(os.Stderr, "ask: unknown help topic %q. Known: workflow, verifier, mcp, schema.\n", topic)
		return 2
	}
	fmt.Println(body)
	return 0
}
