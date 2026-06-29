package main

import (
	"os"

	"github.com/aac/ask/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
