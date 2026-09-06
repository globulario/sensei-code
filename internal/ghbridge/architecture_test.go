package ghbridge

import (
	"context"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

const architectureGraphCommit = "3333333333333333333333333333333333333333"

func architectureBinding() roles.ArchitectureBinding {
	return roles.BindArchitecture("task-arch-1", "repair the bridge exactly", baseSHA, architectureGraphCommit)
}

func TestArchitectureEnvelopeRoundTripsExactSubject(t *testing.T) {
	binding := architectureBinding()
	req := ArchitectureRequest{Binding: binding, RequestID: "a-1", Prompt: "architect prompt\nwith context"}
	wire, err := req.Marker()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ParseArchitectureRequest(wire)
	if !ok {
		t.Fatal("request did not parse")
	}
	if !got.Binding.Same(binding) || got.RequestID != req.RequestID || got.Prompt != req.Prompt {
		t.Fatalf("round trip changed request: %#v", got)
	}

	response := ArchitectureResponse{Binding: binding, RequestID: req.RequestID,
		Body: `{"decision":"proceed","summary":"bounded","plan":"do it"}`}
	answerWire, err := response.Marker()
	if err != nil {
		t.Fatal(err)
	}
	answer, ok := ParseArchitectureResponse(answerWire)
	if !ok || !answer.Answers(req) {
		t.Fatalf("matching answer did not answer request: %#v", answer)
	}

	other := req
	other.Binding = roles.BindArchitecture(binding.TaskID, "a different objective", binding.BaseSHA, binding.GraphBuildCommit)
	if answer.Answers(other) {
		t.Fatal("an architecture answer for one objective answered another")
	}
}

func TestArchitectureEnvelopeRefusesAmbiguousIdentityHeaders(t *testing.T) {
	binding := architectureBinding()
	req := ArchitectureRequest{Binding: binding, RequestID: "a-1", Prompt: "prompt"}
	wire, err := req.Marker()
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"duplicate": strings.Replace(wire, "base="+binding.BaseSHA+"\n", "base="+binding.BaseSHA+"\nbase="+binding.BaseSHA+"\n", 1),
		"unknown":   strings.Replace(wire, "kind=architecture\n", "kind=architecture\nverdict=proceed\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := ParseArchitectureRequest(bad); ok {
				t.Fatal("ambiguous architecture header was accepted")
			}
		})
	}
}

func TestResolverCarriesOnlyBoundArchitectForItsProvider(t *testing.T) {
	fb := &recordingResolver{}
	reviewRunner := &Runner{Issue: Issue{Number: "156", ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"}}}
	res := Resolver{Provider: "chatgpt", Reviewer: reviewRunner, Fallback: fb}

	got, err := res.Resolve(workflow.RunnerSpec{
		Role: roles.Architect, Agent: config.Agent{Name: "chatgpt"}, TaskID: architectureBinding().TaskID,
		Architecture: architectureBinding(),
	})
	if err != nil {
		t.Fatalf("bound architect: %v", err)
	}
	architect, ok := got.Runner.(*ArchitectureRunner)
	if !ok {
		t.Fatalf("architect resolved to %T", got.Runner)
	}
	if !architect.Binding.Same(architectureBinding()) || architect.Issue.Number != "156" {
		t.Fatalf("architect transport changed binding/mailbox: %+v", architect)
	}
	if got.Name != "chatgpt" || len(fb.saw) != 0 {
		t.Fatalf("architect identity/fallback changed: name=%q fallback=%v", got.Name, fb.saw)
	}

	_, err = res.Resolve(workflow.RunnerSpec{Role: roles.Architect, Agent: config.Agent{Name: "chatgpt"}, TaskID: architectureBinding().TaskID})
	if err == nil || !strings.Contains(err.Error(), ErrUnboundArchitecture.Error()) {
		t.Fatalf("unbound carried architect did not fail closed: %v", err)
	}
	if len(fb.saw) != 0 {
		t.Fatal("unbound architect silently fell back")
	}
}

func TestArchitectureRunnerRefusesWrongRoleBeforeTransport(t *testing.T) {
	r := &ArchitectureRunner{Binding: architectureBinding()}
	_, err := r.Run(context.Background(), agent.Request{Role: roles.Implementer, TaskID: architectureBinding().TaskID}, nil)
	if err == nil || !strings.Contains(err.Error(), ErrNotArchitect.Error()) {
		t.Fatalf("wrong role was not refused: %v", err)
	}
}
