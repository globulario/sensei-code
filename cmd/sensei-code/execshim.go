package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/globulario/sensei-code/internal/workflow"
)

// runExecShim is the MCP server command an implementer under the execution
// profile is launched with: a relay from the client's stdio to the one
// execution service the engine started for this invocation.
//
// It runs before the repository is discovered and reads no configuration.
// The client starts it in the candidate worktree, and a configuration loaded
// from there would be one the candidate wrote; its only inputs are the socket
// and service identity the engine put in the client configuration outside
// the worktree.
func runExecShim(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("exec-shim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", "", "the execution service socket")
	service := fs.String("service", "", "the execution service identity")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	if err := workflow.ExecShim(context.Background(), *socket, *service, stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "sensei-code exec-shim:", err)
		return 1
	}
	return 0
}

func execShimRequested(args []string) bool {
	return len(args) > 0 && args[0] == "exec-shim"
}

func runExecShimMain() {
	os.Exit(runExecShim(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
}
