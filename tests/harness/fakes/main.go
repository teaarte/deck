// Command fakes serves the fake programs of the tests built with the race detector: MCP servers, the app-server of
// Codex, ACP agents and models.
package main

import (
	"fmt"
	"os"

	"deck2/tests/harness"
)

func main() {
	if !harness.Serve() {
		fmt.Fprintln(os.Stderr, "fakes: the environment names no fake")
		os.Exit(2)
	}
}
