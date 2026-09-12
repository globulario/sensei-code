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

// An architecture request states both repositories too.
//
// Architecture turns stay in the mailbox repository today, which is exactly why
// this is worth stating rather than leaving implicit: the base an architecture
// request names is already a WORKSPACE object. On 2026-09-12 base f62e3379
// belonged to globulario/sensei while the mailbox was globulario/sensei-code, so
// the conflation was latent here too and escaped notice only because nothing
// fetched it. The law must not depend on that accident.
func TestAnArchitectureRequestNamesBothRepositories(t *testing.T) {
	r := ArchitectureRequest{
		Binding:             architectureBinding(),
		RequestID:           "r-abcdef0123456789",
		Prompt:              "review the world",
		MailboxRepository:   "globulario/sensei-code",
		WorkspaceRepository: "globulario/sensei",
	}
	m, err := r.Marker()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"mailbox_repository=globulario/sensei-code",
		"workspace_repository=globulario/sensei",
	} {
		if !strings.Contains(m, want) {
			t.Fatalf("architecture request does not state %q:\n%s", want, m)
		}
	}
	back, ok := ParseArchitectureRequest(m)
	if !ok {
		t.Fatal("the architecture request did not survive a round trip")
	}
	if back.WorkspaceRepository != "globulario/sensei" || back.MailboxRepository != "globulario/sensei-code" {
		t.Fatalf("routing lost in parsing: mailbox=%q workspace=%q",
			back.MailboxRepository, back.WorkspaceRepository)
	}
}

// Routing must not enter the answered identity: a response that echoes only the
// binding still answers the request.
func TestArchitectureRoutingIsNotPartOfTheAnsweredBinding(t *testing.T) {
	b := architectureBinding()
	req := ArchitectureRequest{
		Binding: b, RequestID: "r-abcdef0123456789", Prompt: "p",
		MailboxRepository: "globulario/sensei-code", WorkspaceRepository: "globulario/sensei",
	}
	resp := ArchitectureResponse{Binding: b, RequestID: "r-abcdef0123456789", Body: "{}"}
	if !resp.Answers(req) {
		t.Fatal("a response echoing the binding no longer answers the request; " +
			"repository routing leaked into the answered identity")
	}
}

// A request that predates repository binding must still PARSE.
//
// Refusing to read it would break Answers() for requests already standing on
// GitHub when the schema changed. Absence is a missing binding the consumer
// refuses on -- a typed refusal at the point of use, never a silent default to
// whichever repository the mailbox happens to be.
func TestALegacyArchitectureRequestStillParses(t *testing.T) {
	legacy := ArchitectureRequest{
		Binding: architectureBinding(), RequestID: "r-abcdef0123456789", Prompt: "p",
	}
	m, err := legacy.Marker()
	if err != nil {
		t.Fatal(err)
	}
	back, ok := ParseArchitectureRequest(m)
	if !ok {
		t.Fatal("a request without repository binding no longer parses; every exchange " +
			"already standing on GitHub would stop matching its answer")
	}
	if back.WorkspaceRepository != "" {
		t.Fatalf("absence was filled in with %q instead of left missing", back.WorkspaceRepository)
	}
}
