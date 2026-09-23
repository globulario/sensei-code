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

const (
	// The EXACT commit from the observed failure. It is Sensei graph
	// provenance: it exists in globulario/sensei -- "Merge pull request #379
	// from globulario/land/376-on-current-main" -- and does NOT exist in
	// globulario/sensei-code, which answered "422 No commit found for SHA" when
	// a consumer looked for it there on 2026-09-22.
	architectureGraphCommit     = "05feaf64d2694e97ac42b6bb93fbb49b9851a1f1"
	architectureGraphRepository = "globulario/sensei"
	// baseSHA is a WORKSPACE object and lives in the other repository.
	architectureWorkspaceRepository = "globulario/sensei-code"
	architectureMailboxRepository   = "globulario/sensei-code"
)

func architectureBinding() roles.ArchitectureBinding {
	return roles.BindArchitecture("task-arch-1", "repair the bridge exactly", baseSHA,
		architectureGraphRepository, architectureGraphCommit)
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
	other.Binding = roles.BindArchitecture(binding.TaskID, "a different objective", binding.BaseSHA,
		binding.GraphRepository, binding.GraphBuildCommit)
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

// noSuchCommit is what a repository says about a SHA it does not contain, which
// is what GitHub said -- 422, no commit found -- when a Sensei graph commit was
// looked for in the workspace repository.
type noSuchCommit struct{ repository, commit string }

func (e noSuchCommit) Error() string { return "no commit " + e.commit + " in " + e.repository }

// recordingLookup answers only for commits the named repository really holds,
// and remembers every repository it was asked about. Remembering is the point:
// an assertion on the ANSWER alone would pass on a consumer that tried both
// repositories and kept whichever one replied, which is the guess this routing
// exists to delete.
type recordingLookup struct {
	holds     map[string]map[string]bool
	attempted []string
}

func (l *recordingLookup) resolve(repository, commit string) error {
	l.attempted = append(l.attempted, repository+" "+commit)
	if !l.holds[repository][commit] {
		return noSuchCommit{repository: repository, commit: commit}
	}
	return nil
}

func (l *recordingLookup) tried(repository, commit string) bool {
	for _, a := range l.attempted {
		if a == repository+" "+commit {
			return true
		}
	}
	return false
}

// THE EXACT FAILURE, and its mirror image.
//
// graph_build_commit is Sensei graph provenance and base is a workspace object.
// Each is resolved in the ONE repository whose authority owns it, and the other
// repository is never ASKED. Both directions are asserted, and both are
// asserted as the ABSENCE of a lookup: 05feaf64 really does resolve in
// globulario/sensei-code-the-wrong-way only by not existing there, so a
// consumer that tried both and took whichever answered would produce the right
// final answer while still guessing.
func TestEachPinnedCommitIsResolvedOnlyInTheRepositoryThatOwnsIt(t *testing.T) {
	req := ArchitectureRequest{
		Binding:             architectureBinding(),
		RequestID:           "r-abcdef0123456789",
		Prompt:              "p",
		MailboxRepository:   architectureMailboxRepository,
		WorkspaceRepository: architectureWorkspaceRepository,
	}
	lookup := &recordingLookup{holds: map[string]map[string]bool{
		architectureGraphRepository:     {architectureGraphCommit: true},
		architectureWorkspaceRepository: {baseSHA: true},
	}}
	if err := req.ResolvePinnedEvidence(lookup.resolve); err != nil {
		t.Fatalf("a fully routed request did not resolve: %v", err)
	}
	if !lookup.tried(architectureGraphRepository, architectureGraphCommit) {
		t.Errorf("the graph commit was never resolved in %s; attempts=%v",
			architectureGraphRepository, lookup.attempted)
	}
	if !lookup.tried(architectureWorkspaceRepository, baseSHA) {
		t.Errorf("base was never resolved in %s; attempts=%v",
			architectureWorkspaceRepository, lookup.attempted)
	}
	if lookup.tried(architectureWorkspaceRepository, architectureGraphCommit) {
		t.Errorf("the graph commit was looked for in the workspace repository %s, "+
			"which is the exact 422 this routing removes; attempts=%v",
			architectureWorkspaceRepository, lookup.attempted)
	}
	if lookup.tried(architectureGraphRepository, baseSHA) {
		t.Errorf("base was looked for in the graph repository %s; a domain that owns "+
			"one identity must not be searched for another; attempts=%v",
			architectureGraphRepository, lookup.attempted)
	}
	// One attempt per identity. Two lookups for two commits is the whole claim:
	// a third would mean some identity was tried in more than one domain.
	if len(lookup.attempted) != 2 {
		t.Fatalf("pinned evidence was attempted %d times, want exactly one per identity: %v",
			len(lookup.attempted), lookup.attempted)
	}

	// The case a clean fixture cannot see. Above, the owning repository answered
	// yes, so a consumer that ALSO tried the other one on failure would never
	// have run that branch. Here the owning repository says no and the wrong one
	// would say yes: the only correct behaviour is to refuse, because the
	// question "does globulario/sensei contain this graph commit" has been
	// answered and a second repository cannot un-answer it.
	misplaced := &recordingLookup{holds: map[string]map[string]bool{
		architectureGraphRepository:     {},
		architectureWorkspaceRepository: {baseSHA: true, architectureGraphCommit: true},
	}}
	if err := req.ResolvePinnedEvidence(misplaced.resolve); err == nil {
		t.Fatal("a graph commit missing from its own repository resolved anyway; " +
			"some other repository answered for it")
	}
	if misplaced.tried(architectureWorkspaceRepository, architectureGraphCommit) {
		t.Fatalf("after the graph repository said no, the workspace repository was tried "+
			"for the same commit and would have said yes; attempts=%v", misplaced.attempted)
	}
	if n := len(misplaced.attempted); n != 2 {
		t.Fatalf("a failed lookup produced %d attempts, want one per identity: %v",
			n, misplaced.attempted)
	}
}

// CONTROL -- no fallback. A pinned commit whose owning repository the envelope
// did not state is REFUSED, and no other repository is tried on its behalf.
func TestAnUnroutedPinnedCommitIsRefusedRatherThanSearchedFor(t *testing.T) {
	legacyGraph := architectureBinding()
	legacyGraph.GraphRepository = "" // the shape every envelope had before this pair existed

	for name, tc := range map[string]struct {
		request ArchitectureRequest
		unused  string // the repository that must not be substituted
		commit  string
	}{
		"graph commit with no graph repository": {
			request: ArchitectureRequest{
				Binding: legacyGraph, RequestID: "r-abcdef0123456789", Prompt: "p",
				MailboxRepository:   architectureMailboxRepository,
				WorkspaceRepository: architectureWorkspaceRepository,
			},
			unused: architectureWorkspaceRepository,
			commit: architectureGraphCommit,
		},
		"base with no workspace repository": {
			request: ArchitectureRequest{
				Binding: architectureBinding(), RequestID: "r-abcdef0123456789", Prompt: "p",
				MailboxRepository: architectureMailboxRepository,
			},
			unused: architectureGraphRepository,
			commit: baseSHA,
		},
	} {
		t.Run(name, func(t *testing.T) {
			lookup := &recordingLookup{holds: map[string]map[string]bool{
				architectureGraphRepository:     {architectureGraphCommit: true, baseSHA: true},
				architectureWorkspaceRepository: {architectureGraphCommit: true, baseSHA: true},
			}}
			err := tc.request.ResolvePinnedEvidence(lookup.resolve)
			if err == nil {
				t.Fatal("an unrouted commit resolved; some repository was inferred for it")
			}
			if !strings.Contains(err.Error(), ErrEvidenceHasNoOwningRepository.Error()) {
				t.Fatalf("refusal did not name the missing owner: %v", err)
			}
			// Every repository in the envelope would have answered yes. None was
			// asked, which is the difference between routing and guessing.
			if lookup.tried(tc.unused, tc.commit) {
				t.Fatalf("%s was substituted for the missing owner of %s; attempts=%v",
					tc.unused, tc.commit, lookup.attempted)
			}
		})
	}
}

// The pair is inseparable ON THE WIRE. Neither half travels alone, in either
// direction, in a request or in a response.
func TestGraphProvenanceTravelsAsAnInseparablePair(t *testing.T) {
	req := ArchitectureRequest{
		Binding: architectureBinding(), RequestID: "r-abcdef0123456789", Prompt: "p",
		MailboxRepository:   architectureMailboxRepository,
		WorkspaceRepository: architectureWorkspaceRepository,
	}
	requestWire, err := req.Marker()
	if err != nil {
		t.Fatal(err)
	}
	responseWire, err := ArchitectureResponse{
		Binding: architectureBinding(), RequestID: "r-abcdef0123456789", Body: "{}",
	}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	// The fixture must be accepted whole, or "refused" below would mean nothing:
	// an emitter that simply stopped stating the pair would satisfy every
	// subtest by making the unmutated envelope unparseable too.
	if _, ok := ParseArchitectureRequest(requestWire); !ok {
		t.Fatalf("the request fixture does not parse, so removing a half proves nothing:\n%s", requestWire)
	}
	if _, ok := ParseArchitectureResponse(responseWire); !ok {
		t.Fatalf("the response fixture does not parse, so removing a half proves nothing:\n%s", responseWire)
	}
	repoLine := "graph_repository=" + architectureGraphRepository + "\n"
	commitLine := "graph_build_commit=" + architectureGraphCommit + "\n"
	for half, line := range map[string]string{"graph_repository": repoLine, "graph_build_commit": commitLine} {
		if !strings.Contains(requestWire, line) || !strings.Contains(responseWire, line) {
			t.Fatalf("the fixture never stated %s, so stripping it removes nothing", half)
		}
	}
	for half, drop := range map[string]string{
		"graph_repository":   repoLine,
		"graph_build_commit": commitLine,
	} {
		t.Run("request without "+half, func(t *testing.T) {
			if _, ok := ParseArchitectureRequest(strings.Replace(requestWire, drop, "", 1)); ok {
				t.Fatalf("a request carrying only half the graph provenance pair was accepted (missing %s)", half)
			}
		})
		t.Run("response without "+half, func(t *testing.T) {
			if _, ok := ParseArchitectureResponse(strings.Replace(responseWire, drop, "", 1)); ok {
				t.Fatalf("a response carrying only half the graph provenance pair was accepted (missing %s)", half)
			}
		})
	}

	// And the emitter refuses before publishing: a binding with a graph commit
	// and no repository for it never becomes an envelope at all.
	half := req
	half.Binding.GraphRepository = ""
	if _, err := half.Marker(); err == nil {
		t.Fatal("a request with a graph commit and no graph repository was rendered rather than refused")
	}
	half = req
	half.Binding.GraphBuildCommit = ""
	if _, err := half.Marker(); err == nil {
		t.Fatal("a request with a graph repository and no graph commit was rendered rather than refused")
	}
}

// CONTROL -- nothing is inferred for a legacy envelope.
//
// An envelope written before graph_repository existed carries graph_build_commit
// alone. It is refused, ON PURPOSE, and the refusal holds even though
// workspace_repository is sitting right there in the same header: the material
// for the inference is present and is not used. Refusing an old envelope is
// correct; guessing its repository is the defect.
func TestALegacyEnvelopeDoesNotHaveItsGraphRepositoryInferred(t *testing.T) {
	req := ArchitectureRequest{
		Binding: architectureBinding(), RequestID: "r-abcdef0123456789", Prompt: "p",
		MailboxRepository:   architectureMailboxRepository,
		WorkspaceRepository: architectureWorkspaceRepository,
	}
	wire, err := req.Marker()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ParseArchitectureRequest(wire); !ok {
		t.Fatalf("the fixture this control removes a field from is already refused:\n%s", wire)
	}
	legacy := strings.Replace(wire, "graph_repository="+architectureGraphRepository+"\n", "", 1)
	if legacy == wire {
		t.Fatal("the legacy fixture is identical to the well-formed one; nothing was removed")
	}
	if strings.Contains(legacy, "graph_repository=") {
		t.Fatal("the legacy fixture still states a graph repository, so it proves nothing")
	}
	if !strings.Contains(legacy, "workspace_repository="+architectureWorkspaceRepository) ||
		!strings.Contains(legacy, "graph_build_commit="+architectureGraphCommit) {
		t.Fatalf("the legacy fixture lost the fields the inference would have used:\n%s", legacy)
	}
	back, ok := ParseArchitectureRequest(legacy)
	if ok {
		t.Fatalf("a legacy envelope was accepted; graph repository read back as %q",
			back.Binding.GraphRepository)
	}
	if back.Binding.GraphRepository != "" {
		t.Fatalf("a graph repository was manufactured for a refused envelope: %q",
			back.Binding.GraphRepository)
	}
}

// CONTROL -- round trip unchanged.
//
// Three distinct repositories so a route that is copied from a neighbour is
// visible rather than accidentally correct. The envelope parses, re-emits
// byte-identically, re-parses, and every binding field keeps the value and the
// meaning it had before the graph pair existed.
func TestAThreeRouteEnvelopeRoundTripsWithEveryBindingFieldUnchanged(t *testing.T) {
	const (
		mailbox   = "globulario/mailbox-repo"
		workspace = "globulario/workspace-repo"
		graph     = "globulario/graph-repo"
	)
	binding := roles.BindArchitecture("task-arch-1", "repair the bridge exactly", baseSHA,
		graph, architectureGraphCommit)
	req := ArchitectureRequest{
		Binding: binding, RequestID: "r-abcdef0123456789", Prompt: "architect prompt\nwith context",
		MailboxRepository: mailbox, WorkspaceRepository: workspace,
	}
	wire, err := req.Marker()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"task=" + binding.TaskID,
		"objective_digest=" + binding.ObjectiveDigest,
		"base=" + baseSHA,
		"graph_repository=" + graph,
		"graph_build_commit=" + architectureGraphCommit,
		"mailbox_repository=" + mailbox,
		"workspace_repository=" + workspace,
	} {
		if !strings.Contains(wire, want+"\n") {
			t.Fatalf("the envelope does not state %q:\n%s", want, wire)
		}
	}

	back, ok := ParseArchitectureRequest(wire)
	if !ok {
		t.Fatal("a fully routed envelope did not parse")
	}
	again, err := back.Marker()
	if err != nil {
		t.Fatal(err)
	}
	if again != wire {
		t.Fatalf("re-emitting changed the envelope:\n got %q\nwant %q", again, wire)
	}
	if _, ok := ParseArchitectureRequest(again); !ok {
		t.Fatal("the re-emitted envelope did not parse")
	}

	// Field by field, so a failure says which meaning moved.
	for _, f := range []struct{ name, got, want string }{
		{"task", back.Binding.TaskID, binding.TaskID},
		{"objective_digest", back.Binding.ObjectiveDigest, binding.ObjectiveDigest},
		{"base", back.Binding.BaseSHA, baseSHA},
		{"graph_repository", back.Binding.GraphRepository, graph},
		{"graph_build_commit", back.Binding.GraphBuildCommit, architectureGraphCommit},
		{"mailbox_repository", back.MailboxRepository, mailbox},
		{"workspace_repository", back.WorkspaceRepository, workspace},
		{"request", back.RequestID, req.RequestID},
		{"prompt", back.Prompt, req.Prompt},
	} {
		if f.got != f.want {
			t.Errorf("%s round tripped as %q, want %q", f.name, f.got, f.want)
		}
	}
	if !back.Binding.Same(binding) {
		t.Fatalf("the answered identity changed across a round trip: %+v", back.Binding)
	}
}
