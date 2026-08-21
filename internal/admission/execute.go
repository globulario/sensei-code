// SPDX-License-Identifier: AGPL-3.0-only

package admission

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes one exact invocation in the canonical Sensei admission chain.
// Implementations receive structured argv rather than a shell command so the
// authority-bearing arguments composed by Chain cannot be reinterpreted by a
// shell layer.
type Runner interface {
	Run(context.Context, Invocation) (output string, exitCode int, err error)
}

// CommandRunner executes Sensei's canonical admission CLI. Command defaults to
// "sensei" when empty, but may be set explicitly by tests or installations that
// pin a particular binary path.
type CommandRunner struct {
	Command string
}

func (r CommandRunner) Run(ctx context.Context, invocation Invocation) (string, int, error) {
	command := strings.TrimSpace(r.Command)
	if command == "" {
		command = "sensei"
	}

	cmd := exec.CommandContext(ctx, command, invocation.Args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0, nil
	}

	// A process exit is a governance result owned by Sensei. Preserve the exit
	// code for Interpret instead of collapsing a refusal into an infrastructure
	// error. Launch/context/transport failures remain Go errors.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(output), exitErr.ExitCode(), nil
	}

	return string(output), -1, err
}

// RunResult records the prefix of the canonical admission chain that actually
// ran. Complete means every step returned its documented success exit code.
type RunResult struct {
	Outcomes  []Outcome
	StoppedAt Step
	Complete  bool
}

// Execute runs the exact chain produced by Chain and stops on the first
// non-success outcome. Unknown future non-zero exit codes therefore fail closed
// automatically through Interpret instead of falling through to later apply or
// verification stages.
func Execute(ctx context.Context, req Request, runner Runner) (RunResult, error) {
	if runner == nil {
		return RunResult{}, errors.New("admission runner is nil")
	}

	invocations, err := Chain(req)
	if err != nil {
		return RunResult{}, err
	}

	result := RunResult{Outcomes: make([]Outcome, 0, len(invocations))}
	for _, invocation := range invocations {
		output, exitCode, runErr := runner.Run(ctx, invocation)
		if runErr != nil {
			return result, fmt.Errorf("%s: %w", invocation.Step, runErr)
		}

		outcome := Interpret(invocation.Step, exitCode, output)
		result.Outcomes = append(result.Outcomes, outcome)
		if !outcome.Success {
			result.StoppedAt = invocation.Step
			return result, nil
		}
	}

	result.Complete = true
	return result, nil
}
