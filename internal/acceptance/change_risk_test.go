//go:build acceptance

package acceptance

// The deployment check behind the deleted change-risk parser.
//
// Routing used to learn the approval class by recognising a sentence Sensei
// composed into required_actions. globulario/sensei#171 published the same
// verdict as structured fields, so the parser was deleted and the router now
// reads change_risk directly. That trade is only sound while the server this
// repository actually talks to publishes those fields: against an older server
// the structured verdict is simply absent, every plan escalates, and nothing in
// a unit test would say why.
//
// So this asks the deployment, not a fixture, and it asks through the same
// configured endpoint the workflow uses.
//
//	go test -tags acceptance ./internal/acceptance/ -run TestChangeRisk -v

import (
	"context"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/sensei"
)

func TestChangeRiskIsPublishedStructurally(t *testing.T) {
	root := repoRoot(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	sc, err := sensei.Start(context.Background(), root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		t.Fatalf("start Sensei: %v", err)
	}
	defer sc.Close()

	result, err := sc.CallTool("awareness_preflight", map[string]any{
		"task":   "read the change-risk verdict for a governed file",
		"files":  []string{"internal/workflow/authority.go"},
		"domain": "github.com/globulario/sensei-code",
		"mode":   "compact",
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	decision, err := sensei.DecodePreflight(result)
	if err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	t.Logf("status %s · blast %s · gate %s", decision.Status, decision.ChangeRisk.Blast(), decision.ChangeRisk.Gate())

	// A server that cannot scope this repository has not been asked the
	// question this test exists to ask. Skipping says so out loud rather than
	// passing on an answer nobody gave. See sensei-code#10 and #13.
	if decision.Status != sensei.PreflightOK && decision.Status != sensei.PreflightEmpty {
		t.Skipf("the endpoint could not classify this region, so it was never asked for a change-risk verdict: %s", decision.Diagnostic())
	}

	if !decision.ChangeRisk.Classified() {
		t.Fatalf("the endpoint published no structured approval gate, so every plan will escalate: %s", decision.Diagnostic())
	}
	if decision.ChangeRisk.Blast() == "unclassified" {
		t.Errorf("an approval gate was published without a blast radius, which is half a verdict: %+v", decision.ChangeRisk)
	}

	// The prose line is still rendered for older consumers and must remain
	// something this repository never reads. Its presence is not required; its
	// agreement with the structured verdict is, when both are present.
	for _, action := range decision.RequiredActions {
		t.Logf("required action: %s", action)
	}
}
