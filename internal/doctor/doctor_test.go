package doctor

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/provider"
)

func TestConfiguredCommandsAreUnique(t *testing.T) {
	cfg := config.Default()
	got := configuredCommands(cfg)
	seen := map[string]bool{}
	for _, command := range got {
		if seen[command] {
			t.Fatalf("duplicate command %q", command)
		}
		seen[command] = true
	}
	for _, want := range []string{"git", "awareness-mcp", "codex", "claude"} {
		if !seen[want] {
			t.Fatalf("missing command %q in %v", want, got)
		}
	}
}

func TestConfiguredProvidersAreUnique(t *testing.T) {
	cfg := config.Default()
	got := configuredProviders(cfg)
	if len(got) != 3 {
		t.Fatalf("configuredProviders(default)=%v want three providers", got)
	}
	seen := map[provider.ID]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("configuredProviders(default)=%v repeats %q", got, id)
		}
		seen[id] = true
	}
	// The architect authenticates as ChatGPT through the codex app-server, and
	// the two bounded workers are Claude and Codex, so all three are configured
	// login surfaces that doctor must report on.
	for _, want := range []provider.ID{provider.ChatGPT, provider.Codex, provider.Claude} {
		if !seen[want] {
			t.Fatalf("configuredProviders(default)=%v is missing %q", got, want)
		}
	}
}

func TestWarningsDoNotMasqueradeAsPassButDoNotBlockReadiness(t *testing.T) {
	report := Report{Checks: []Check{{Name: "provider:antigravity", Status: Warn, Detail: "auth state unknown"}}}
	if !report.OK() {
		t.Fatal("warning-only doctor report should remain usable")
	}
	if report.Checks[0].Status == Pass {
		t.Fatal("unknown provider auth must not be represented as PASS")
	}
	report.Checks = append(report.Checks, Check{Name: "provider:codex", Status: Fail})
	if report.OK() {
		t.Fatal("FAIL should block doctor readiness")
	}
}

func TestToolNames(t *testing.T) {
	names, err := toolNames(json.RawMessage(`{"tools":[{"name":"awareness_preflight"},{"name":"sensei_workspace_status"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "awareness_preflight" {
		t.Fatalf("unexpected names %v", names)
	}
}

func TestMissingWorkspaceToolsNameTheirRemedy(t *testing.T) {
	// Released sensei packages lag these tools. A check that only says
	// "missing" makes every reader rediscover that on their own.
	got := remediation("sensei_workspace_status")
	if !strings.Contains(got, "build awareness-mcp") {
		t.Fatalf("remediation = %q, want the action that fixes it", got)
	}
	if remediation("awareness_preflight") == "" {
		t.Fatal("every missing tool should say what to check")
	}
}

// A provider that is authenticated is not thereby a provider that can serve a
// turn, and doctor reported the two as one thing.
//
// The identical line — "provider:codex · connected · plus" — appeared on a day
// Codex was out of quota and on a day it was not. A readiness check whose output
// does not change when readiness changes carries no information about it.
//
// This is the architect's accepted intent for question.20de26bd75eab654:
// providers own whether they are configured, authenticated and able to serve
// turns; doctor only projects that confirmed state; unknown capability stays
// explicitly non-PASS; and the diagnostic stays quota-safe.
func TestAuthenticationAloneIsNotReadiness(t *testing.T) {
	if Unproven == Pass {
		t.Fatal("unproven collapses into pass, which is the defect this exists to remove")
	}
	// Unproven does not fail the report: nothing is known to be broken. It is
	// simply not green, because nothing is known to work either.
	report := Report{Checks: []Check{{Name: "provider:codex", Status: Unproven}}}
	if !report.OK() {
		t.Fatal("an unproven check fails the report; absence of proof is not a failure")
	}
	failing := Report{Checks: []Check{{Name: "provider:codex", Status: Fail}}}
	if failing.OK() {
		t.Fatal("a failing check no longer fails the report")
	}
}

// Quota-safety is a property of the code path, not a promise in a comment: the
// diagnostic must not reach a provider's execution surface at all.
func TestTheDiagnosticSpendsNoProviderQuota(t *testing.T) {
	body := sourceOf(t, "doctor.go")
	for _, forbidden := range []string{
		"exec.Command(", "processx.", "agent.CLI", "AskFork", "AskIndependent", "AskOnce",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("doctor reaches %s; establishing health must not spend a provider turn", forbidden)
		}
	}
	// LookPath is the one exec-adjacent call it may make: it resolves a binary
	// on disk and starts nothing.
	if !strings.Contains(body, "exec.LookPath") {
		t.Error("doctor no longer checks that its executables resolve")
	}
}

func sourceOf(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// doctor projects the provider's account of its own capability and concludes
// nothing of its own. An unknown capability stays unknown: the alternative is a
// diagnostic inventing a readiness the provider never claimed.
func TestDoctorProjectsCapabilityRatherThanConcludingIt(t *testing.T) {
	for _, tc := range []struct {
		capability provider.TurnCapability
		want       Status
	}{
		{provider.CapabilityDemonstrated, Pass},
		{provider.CapabilityRefused, Fail},
		{provider.CapabilityUnknown, Unproven},
		{provider.TurnCapability("something new"), Unproven},
		{"", Unproven},
	} {
		got, detail := projectCapability(tc.capability, "base")
		if got != tc.want {
			t.Errorf("capability %q projected as %s, want %s", tc.capability, got, tc.want)
		}
		if !strings.HasPrefix(detail, "base") {
			t.Errorf("capability %q lost the existing detail: %q", tc.capability, detail)
		}
	}
}

// A provider that has not been asked never claims capability. StatusFor
// inspects installation and stored credentials and starts nothing.
func TestAProviderNeverClaimsCapabilityItHasNotDemonstrated(t *testing.T) {
	for _, id := range provider.Ordered {
		status := provider.StatusFor(context.Background(), id)
		if status.Capability != provider.CapabilityUnknown {
			t.Errorf("%s claims capability %q without having served a turn", id, status.Capability)
		}
	}
}
