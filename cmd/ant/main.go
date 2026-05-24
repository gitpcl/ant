// Command ant is the thin CLI front door for the Ant colony engine. It only
// parses flags, calls into internal/engine, and renders results — all logic
// lives in the engine (TECHSPEC §3 hard rule). The enterprise layer imports the
// engine as a library; this CLI is one of several front doors over it.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:]))
}

// run executes the command tree against args and returns the process exit code
// (TECHSPEC §7.1). It is split from main so tests can drive the CLI and assert
// exit codes without spawning a process. All exit-code classification is owned
// by the engine; this layer only relays it.
func run(args []string) int {
	root := newRootCmd()
	root.SetArgs(args)
	return executeWithExitCode(root)
}
