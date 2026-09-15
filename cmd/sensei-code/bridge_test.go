package main

import (
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// An installation that never heard of the bridge must behave exactly as before:
// Runners stays nil, which the engine reads as the provider command line.
func TestNoConfiguredBridgeLeavesTheEngineUntouched(t *testing.T) {
	e := &workflow.Engine{SessionID: "s1"}
	banner, err := installGitHubBridge(e, t.TempDir(), config.GitHubBridge{})
	if err != nil {
		t.Fatalf("an absent bridge was treated as an error: %v", err)
	}
	if banner != "" {
		t.Fatalf("announced a bridge that was not configured: %q", banner)
	}
	if e.Runners != nil {
		t.Fatal("an absent bridge installed a resolver; the provider command line was replaced by nothing anyone asked for")
	}
}

// Configured is keyed on the mailbox alone. Any other field set without one is
// still "off" -- completeness is then required rather than assumed.
func TestOnlyAMailboxTurnsTheBridgeOn(t *testing.T) {
	if (config.GitHubBridge{Owner: "globulario", Repo: "sensei-code"}).Configured() {
		t.Fatal("a bridge with no mailbox reported itself configured")
	}
	if !(config.GitHubBridge{MailboxPR: "157"}).Configured() {
		t.Fatal("a bridge with a mailbox reported itself off")
	}
}

// The fallback must serve every role the bridge does not carry. ghbridge.Resolver
// refuses on a nil Fallback, so handing it one is what keeps the implementor
// working while architect and reviewer travel over GitHub.
func TestTheFallbackStillServesRolesTheBridgeDoesNotCarry(t *testing.T) {
	f := cliFallback{sessionID: "s1"}
	got, err := f.Resolve(workflow.RunnerSpec{Role: roles.Implementer})
	if err != nil {
		t.Fatalf("the fallback refused the implementer role: %v", err)
	}
	if got.Runner == nil {
		t.Fatal("the fallback returned no adapter; a bridged run would lose its implementer")
	}
}

// An unreadable role spec must carry NOTHING rather than everything. Carrying a
// role by accident sends a governed turn to a remote nobody is attending, and
// the run then waits out its full deadline looking like a refusal.
func TestAnUnreadableRoleSpecCarriesNothing(t *testing.T) {
	if got := mustRoles("architect,nonsense"); len(got) != 0 {
		t.Fatalf("an unreadable role spec carried roles anyway: %v", got)
	}
	if got := mustRoles(""); !got[roles.Architect] || !got[roles.Reviewer] {
		t.Fatalf("the default did not carry architect+reviewer: %v", got)
	}
	if got := mustRoles("none"); len(got) != 0 {
		t.Fatalf("\"none\" carried roles: %v", got)
	}
}
