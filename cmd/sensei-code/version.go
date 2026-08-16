package main

import (
	"fmt"
	"io"
)

// Version is the canonical Sensei Code version reported by --version.
const Version = "0.1.0"

// runVersion answers --version before anything else looks at the workspace.
//
// Every other command needs a Git repository and a loaded configuration, so
// asking a binary which version it is must not depend on where it is run from:
// outside a repository gitx.Discover fails and the process would exit 1 with a
// "not a git repository" error instead of printing the version.
func runVersion(args []string, out io.Writer) bool {
	if len(args) == 0 || args[0] != "--version" {
		return false
	}
	fmt.Fprintf(out, "sensei-code %s\n", Version)
	return true
}
