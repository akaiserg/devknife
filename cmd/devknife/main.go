// Command devknife is an offline developer toolbox: a native desktop app,
// a pipe-friendly CLI and an MCP server in one binary.
package main

import (
	"os"

	"devknife/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
