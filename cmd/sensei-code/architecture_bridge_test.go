package main

import (
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// This is the composition proof the architect mailbox needs. The protocol and
// resolver are tested in ghbridge, but this pins the product wire: the resolver
// produced by composeEngineResolver with the real configured App transport can
// actually carry a bound architect turn, and it carries the same App-backed
// mailbox rather than constructing a second transport that merely agrees today.
func TestConfiguredBridgeCarriesABoundArchitectTurn(t *testing.T) {
	server := newControlServer(t)
	bridge, _ := installedBridge(t, server, configuredBridge())
	binding := roles.BindArchitecture(
		"task-arch-compose",
		"repair exactly the approved objective",
		"1111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222",
	)

	resolved, err := bridge.Resolve(workflow.RunnerSpec{
		Role:         roles.Architect,
		Agent:        config.Agent{Name: "chatgpt"},
		TaskID:       binding.TaskID,
		Architecture: binding,
	})
	if err != nil {
		t.Fatalf("resolve bound architect: %v", err)
	}
	architect, ok := resolved.Runner.(*ghbridge.ArchitectureRunner)
	if !ok {
		t.Fatalf("bound architect resolved to %T, want *ghbridge.ArchitectureRunner", resolved.Runner)
	}
	if resolved.Name != "chatgpt" || resolved.Label != ghbridge.ResolverLabel {
		t.Fatalf("resolved identity = %q / %q, want chatgpt / %q", resolved.Name, resolved.Label, ghbridge.ResolverLabel)
	}
	if !architect.Binding.Same(binding) {
		t.Fatalf("architecture binding changed at composition: got %+v want %+v", architect.Binding, binding)
	}
	if architect.Issue.Number != bridge.Reviewer.Issue.Number {
		t.Fatalf("architect issue = %q, reviewer issue = %q", architect.Issue.Number, bridge.Reviewer.Issue.Number)
	}
	if architect.Issue.API != bridge.Reviewer.Issue.API {
		t.Fatal("architect did not inherit the configured App mailbox client")
	}
	if architect.Issue.API == nil || !architect.Issue.API.Configured() {
		t.Fatal("architect mailbox is not backed by the configured GitHub App client")
	}
	if architect.NewRequestID == nil || architect.SessionID != bridge.Reviewer.SessionID || architect.Wait != bridge.Reviewer.Wait {
		t.Fatalf("architect transport did not inherit request/session/wait configuration: %+v", architect)
	}
}
