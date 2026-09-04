package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/globulario/sensei-code/internal/control"
	"github.com/globulario/sensei-code/internal/gitx"
)

// runSubmit places one objective into the control process running for this
// repository.
//
// It builds no engine, starts no workflow and runs no provider. It connects to
// a local socket, says one thing, and prints what the owner answered. That
// separation is the point: exactly one process owns the engine for a
// repository, and a submitter that constructed its own would be a second owner
// executing a task the first one knows nothing about.
//
// It is a separate verb from `run`, which is the headless entry that owns its
// own engine for the duration of one task. `submit` hands work to an owner that
// already exists and outlives the command.
func runSubmit(repo gitx.Repo, args []string) error {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	task := fs.String("task", "", "the objective to place into the running control process")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Positional words are accepted too, so `sensei-code submit fix the parser`
	// works the way a person expects rather than failing on a missing flag.
	objective := strings.TrimSpace(*task)
	if objective == "" {
		objective = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if objective == "" {
		fmt.Fprintln(os.Stderr, "sensei-code submit: --task is required")
		fs.Usage()
		return fmt.Errorf("no objective")
	}

	accepted, err := control.SubmitLocalObjective(repo.Root, objective)
	if err != nil {
		return err
	}
	fmt.Println("Objective placed with the control process for", accepted.Workspace)
	fmt.Println("  task        ", accepted.TaskID)
	// Printed rather than assumed. Local access is real authority over this
	// process and it is not evidence that a person typed, so the operator sees
	// what was actually recorded instead of inferring it from the fact that the
	// command succeeded.
	fmt.Println("  provenance  ", accepted.Provenance)
	fmt.Println()
	fmt.Println("  Follow it in the control process's output, or through the remote")
	fmt.Println("  surface with get_work / inspect_task.")
	return nil
}
