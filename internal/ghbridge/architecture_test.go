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

// THE REFUSAL ENVELOPE -- W2, W3, W4, W5 and the grammar half of W8.
//
// MEASURED 2026-09-24. A governed run published two architecture requests, rang
// the doorbell, and ended on its deadline as "no architecture answer answering
// that request was posted". The consumer had not been silent: it had refused,
// precisely, and the grammar had no prefix that could carry a refusal. The hour
// that cost, and the two separate misattributions to quota, are what these
// witnesses hold in place.

// architectureRefusalMeasuredReason is the consumer's OWN text from that
// failure, with the two identifiers redacted exactly as the objective redacted
// them: they name a commit and a tag that exist on one disk and no remote, so
// quoting them verbatim would make anything that resolves this text fail the
// same pinned-evidence check it describes. The shape is what matters here -- a
// consumer's diagnostic, carried through unparaphrased.
const architectureRefusalMeasuredReason = "workspace repository globulario/sensei-code could not " +
	"resolve prior candidate commit REDACTED-LOCAL-ONLY-SHA or archive tag REDACTED-LOCAL-ONLY-TAG"

// architectureRefusalFixture is the measured request and the refusal of it.
func architectureRefusalFixture() (ArchitectureRequest, ArchitectureRefusal) {
	req := ArchitectureRequest{
		Binding:             architectureBinding(),
		RequestID:           "r-e25830e22588e77d",
		Prompt:              "architect this objective",
		MailboxRepository:   architectureMailboxRepository,
		WorkspaceRepository: architectureWorkspaceRepository,
	}
	return req, ArchitectureRefusal{
		Binding:   req.Binding,
		RequestID: req.RequestID,
		Stage:     RefusalStagePinnedEvidence,
		Reason:    architectureRefusalMeasuredReason,
	}
}

// observeRefusals posts bodies as the AUTHENTICATED consumer and returns what the
// bridge saw for one open request.
//
// The observation is the deciding layer: it is what says whether a wait is
// settled. Proving "does not discharge" one level below it -- on the parser alone
// -- would leave the decision itself unwitnessed.
func observeRefusals(t *testing.T, req ArchitectureRequest, bodies ...string) ArchitectureObservation {
	t.Helper()
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	for i, body := range bodies {
		m.append(map[string]any{
			"id": float64(9000 + i), "body": body,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		})
	}
	obs, err := ObserveArchitecture(context.Background(), box, req)
	if err != nil {
		t.Fatalf("reading the architecture mailbox: %v", err)
	}
	return obs
}

// W2 BINDING ECHO. The refusal carries the COMPLETE binding, in the canonical
// order the request itself used, and that is what lets it settle the wait.
//
// The order is asserted against the REQUEST's own header rather than only
// against a list written here, so "the same canonical order" stays a claim about
// the two envelopes and not about this test's memory of one of them.
func TestARefusalEchoesTheCompleteBindingInTheRequestsCanonicalOrder(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	wire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	header, reason, ok := strings.Cut(strings.TrimPrefix(wire, architectureRefusalMarker+"\n"), "\n\n")
	if !ok {
		t.Fatalf("the refusal has no payload boundary:\n%s", wire)
	}
	want := []string{
		"task=" + req.Binding.TaskID,
		"request=" + req.RequestID,
		"objective_digest=" + req.Binding.ObjectiveDigest,
		"base=" + req.Binding.BaseSHA,
		"graph_repository=" + req.Binding.GraphRepository,
		"graph_build_commit=" + req.Binding.GraphBuildCommit,
		"stage_vocabulary=" + RefusalStageVocabulary,
		"stage=" + string(RefusalStagePinnedEvidence),
	}
	got := strings.Split(header, "\n")
	if len(got) != len(want) {
		t.Fatalf("the refusal header is\n%q\nwant exactly\n%q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("header line %d is %q, want %q", i, got[i], want[i])
		}
	}
	if reason != architectureRefusalMeasuredReason {
		t.Errorf("the reason was altered in transit:\n got %q\nwant %q", reason, architectureRefusalMeasuredReason)
	}

	// The same order the REQUEST states its binding in, field for field.
	requestWire, err := req.Marker()
	if err != nil {
		t.Fatal(err)
	}
	bindingKeys := map[string]bool{
		"task": true, "request": true, "objective_digest": true,
		"base": true, "graph_repository": true, "graph_build_commit": true,
	}
	var fromRequest []string
	requestHeader, _, _ := strings.Cut(strings.TrimPrefix(requestWire, architectureRequestMarker+"\n"), "\n\n")
	for _, line := range strings.Split(requestHeader, "\n") {
		key, _, _ := strings.Cut(line, "=")
		if bindingKeys[key] {
			fromRequest = append(fromRequest, line)
		}
	}
	if len(fromRequest) != len(bindingKeys) {
		t.Fatalf("the request states %d binding fields, want %d:\n%s",
			len(fromRequest), len(bindingKeys), requestWire)
	}
	for i := range fromRequest {
		if got[i] != fromRequest[i] {
			t.Errorf("the echo is not the request's order: refusal line %d is %q, the request's is %q",
				i, got[i], fromRequest[i])
		}
	}

	// And it binds: parsed back, every field intact, and it refuses THIS request.
	back, perr := ParseArchitectureRefusal(wire)
	if perr != nil {
		t.Fatalf("a canonical refusal did not parse: %v", perr)
	}
	for _, f := range []struct{ name, got, want string }{
		{"task", back.Binding.TaskID, req.Binding.TaskID},
		{"request", back.RequestID, req.RequestID},
		{"objective_digest", back.Binding.ObjectiveDigest, req.Binding.ObjectiveDigest},
		{"base", back.Binding.BaseSHA, req.Binding.BaseSHA},
		{"graph_repository", back.Binding.GraphRepository, req.Binding.GraphRepository},
		{"graph_build_commit", back.Binding.GraphBuildCommit, req.Binding.GraphBuildCommit},
		{"stage", string(back.Stage), string(RefusalStagePinnedEvidence)},
		{"reason", back.Reason, architectureRefusalMeasuredReason},
	} {
		if f.got != f.want {
			t.Errorf("%s round tripped as %q, want %q", f.name, f.got, f.want)
		}
	}
	if !back.Refuses(req) {
		t.Fatal("a refusal echoing the complete binding does not refuse the request it echoes")
	}
	obs := observeRefusals(t, req, wire)
	settled, ok := obs.Settlement()
	if !ok {
		t.Fatalf("the complete refusal did not settle the request: %+v", obs)
	}
	if !settled.Refused() {
		t.Fatalf("a refusal settled the request as an ANSWER: %+v", settled)
	}
	if settled.Refusal.Reason != architectureRefusalMeasuredReason ||
		settled.Refusal.Stage != RefusalStagePinnedEvidence {
		t.Errorf("the settlement lost the consumer's diagnostic: %+v", settled.Refusal)
	}
	if settled.Answer != nil {
		t.Errorf("a refusal was also read as an architecture answer: %+v", settled.Answer)
	}
}

// W3 HALF PAIR REFUSED. graph_repository and graph_build_commit are one fact in
// two fields; neither half travels alone, in either direction.
//
// Refusing a half pair is CORRECT rather than unfortunate: base is a workspace
// object and graph_build_commit is Sensei graph provenance, so a commit arriving
// without the repository that owns it can only be resolved by inferring one --
// the guess that had 05feaf64 looked for in globulario/sensei-code, which
// answered 422, while the commit sat in globulario/sensei the whole time.
func TestARefusalCarryingHalfTheGraphPairCannotSettleAnything(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	wire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	// The whole fixture must be accepted, or removing a half proves nothing: an
	// emitter that simply stopped stating the pair would satisfy every subtest.
	if _, perr := ParseArchitectureRefusal(wire); perr != nil {
		t.Fatalf("the fixture does not parse, so removing a half proves nothing: %v", perr)
	}
	if _, ok := observeRefusals(t, req, wire).Settlement(); !ok {
		t.Fatal("the whole fixture does not settle the request, so a mutation proving it cannot means nothing")
	}
	for half, line := range map[string]string{
		"graph_repository":   "graph_repository=" + req.Binding.GraphRepository + "\n",
		"graph_build_commit": "graph_build_commit=" + req.Binding.GraphBuildCommit + "\n",
	} {
		t.Run("without "+half, func(t *testing.T) {
			bad := strings.Replace(wire, line, "", 1)
			if bad == wire {
				t.Fatalf("the fixture never stated %s, so stripping it removed nothing", half)
			}
			if _, perr := ParseArchitectureRefusal(bad); perr == nil {
				t.Fatalf("a refusal carrying only half the graph pair parsed (missing %s)", half)
			}
			obs := observeRefusals(t, req, bad)
			if _, ok := obs.Settlement(); ok {
				t.Fatalf("a half-pair refusal settled the request (missing %s)", half)
			}
			if len(obs.Rejected) != 1 {
				t.Fatalf("the half pair was rejected silently: %+v", obs)
			}
			if !strings.Contains(obs.Rejected[0].Diagnostic, "half stated") {
				t.Errorf("the rejection does not name the half pair: %q", obs.Rejected[0].Diagnostic)
			}
		})
	}
	// And the emitter never renders one, in either direction.
	for half, mutate := range map[string]func(ArchitectureRefusal) ArchitectureRefusal{
		"graph_repository":   func(f ArchitectureRefusal) ArchitectureRefusal { f.Binding.GraphRepository = ""; return f },
		"graph_build_commit": func(f ArchitectureRefusal) ArchitectureRefusal { f.Binding.GraphBuildCommit = ""; return f },
	} {
		if _, merr := mutate(refusal).Marker(); merr == nil {
			t.Errorf("a refusal with no %s was rendered rather than refused", half)
		}
	}
}

// W4 FOREIGN, STALE, DIFFERENTLY BOUND. Each direction separately, because one
// of them passing does not imply the others.
//
// Every case differs from the open request in EXACTLY ONE identity, so a match
// that had quietly degraded to a subset of the binding fails here on the field
// it stopped reading rather than passing on the fields it still reads.
func TestARefusalBoundElsewhereCannotSettleThisRequest(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	const otherBase = "9f8e7d6c5b4a39281706f5e4d3c2b1a098765432"
	const otherGraphCommit = "1111111111111111111111111111111111111111"

	otherTask := refusal
	otherTask.Binding.TaskID = "task-arch-2"

	otherRequest := refusal
	otherRequest.RequestID = "r-0000000000000000"

	otherObjective := refusal
	otherObjective.Binding = roles.BindArchitecture(req.Binding.TaskID, "a different objective entirely",
		req.Binding.BaseSHA, req.Binding.GraphRepository, req.Binding.GraphBuildCommit)

	otherBaseRefusal := refusal
	otherBaseRefusal.Binding.BaseSHA = otherBase

	otherGraphRepo := refusal
	otherGraphRepo.Binding.GraphRepository = "globulario/some-other-repo"

	otherGraphBuild := refusal
	otherGraphBuild.Binding.GraphBuildCommit = otherGraphCommit

	for name, tc := range map[string]struct {
		refusal ArchitectureRefusal
		names   string
	}{
		"another task":               {otherTask, "task"},
		"another request":            {otherRequest, "request"},
		"another objective":          {otherObjective, "objective_digest"},
		"another base":               {otherBaseRefusal, "base"},
		"another graph repository":   {otherGraphRepo, "graph_repository"},
		"another graph build commit": {otherGraphBuild, "graph_build_commit"},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.refusal.Refuses(req) {
				t.Fatalf("a refusal bound to %s elsewhere refused this request", tc.names)
			}
			wire, err := tc.refusal.Marker()
			if err != nil {
				t.Fatalf("the differently bound fixture is not renderable, so it proves nothing: %v", err)
			}
			// It must be a WELL FORMED refusal, or the subtest would be proving
			// malformedness rather than binding.
			if _, perr := ParseArchitectureRefusal(wire); perr != nil {
				t.Fatalf("the fixture is malformed rather than differently bound: %v", perr)
			}
			obs := observeRefusals(t, req, wire)
			if _, ok := obs.Settlement(); ok {
				t.Fatalf("a refusal bound to %s elsewhere settled this request", tc.names)
			}
			if len(obs.Rejected) != 1 {
				t.Fatalf("a differently bound refusal was ignored silently: %+v", obs)
			}
			if !strings.Contains(obs.Rejected[0].Diagnostic, tc.names) {
				t.Errorf("the rejection does not name the field that differs (%s): %q",
					tc.names, obs.Rejected[0].Diagnostic)
			}
		})
	}
}

// W5 MALFORMED AND PARTIAL -- the witness that stops this repair from recreating
// the defect one layer up.
//
// A refusal-shaped comment that cannot be read must not discharge the wait, and
// its rejection must be OBSERVABLE. Dropping it silently would leave exactly the
// observation the refusal envelope exists to remove: a consumer that spoke, and
// a machine that reports nothing was said.
func TestAnUnreadableRefusalIsRejectedVisiblyRatherThanIgnored(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	wire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"unparseable header":  architectureRefusalMarker + "\nthis is not a header at all\n\n" + architectureRefusalMeasuredReason,
		"no payload boundary": architectureRefusalMarker + "\ntask=" + req.Binding.TaskID + "\n",
		"missing base":        strings.Replace(wire, "base="+req.Binding.BaseSHA+"\n", "", 1),
		"missing stage":       strings.Replace(wire, "stage="+string(RefusalStagePinnedEvidence)+"\n", "", 1),
		"missing reason":      strings.TrimSuffix(wire, architectureRefusalMeasuredReason),
		"unknown stage":       strings.Replace(wire, "stage="+string(RefusalStagePinnedEvidence), "stage=some-new-stage", 1),
		"unknown vocabulary":  strings.Replace(wire, "stage_vocabulary="+RefusalStageVocabulary, "stage_vocabulary=v99", 1),
		"unknown header":      strings.Replace(wire, "stage=", "decision=proceed\nstage=", 1),
		"duplicate header":    strings.Replace(wire, "base="+req.Binding.BaseSHA+"\n", "base="+req.Binding.BaseSHA+"\nbase="+req.Binding.BaseSHA+"\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if bad == wire {
				t.Fatal("the mutation changed nothing, so it proves nothing")
			}
			// Still ATTRIBUTABLE as an attempted refusal. This is the property
			// that makes the rejection reportable at all: a body that claims the
			// prefix is a refusal somebody tried to make.
			if !ArchitectureRefusalShaped(bad) {
				t.Fatal("the mutation stopped the body claiming to be a refusal, so this proves nothing")
			}
			if parsed, err := ParseArchitectureRefusal(bad); err == nil {
				t.Fatalf("an unusable refusal parsed: %+v", parsed)
			}
			obs := observeRefusals(t, req, bad)
			if _, ok := obs.Settlement(); ok {
				t.Fatal("an unusable refusal settled the request")
			}
			if len(obs.Rejected) != 1 {
				t.Fatalf("an unusable refusal was discarded in silence: %+v", obs)
			}
			if strings.TrimSpace(obs.Rejected[0].Diagnostic) == "" {
				t.Fatal("the rejection carries no reason, which is silence with a record")
			}
			if obs.Rejected[0].Comment == 0 {
				t.Error("the rejection does not locate the comment an operator must go and read")
			}
			if len(obs.Terminals) != 0 {
				t.Errorf("an unusable refusal settled the request anyway: %+v", obs.Terminals)
			}
		})
	}
}

// W8, the grammar half -- THE CONTROL THAT MATTERS MOST.
//
// This project has a recorded scar in which a grammar changed, the responder was
// never told, and messages went unrecognised for days. So the addition must be
// invisible: the seven existing prefixes are not captured by it, and a refusal is
// not captured by any of them.
//
// Bounded honestly: the attestation and relayed-review envelopes are round
// tripped by their own tests, not here, because rendering one needs types this
// file does not import. What is asserted here for all seven is that the new
// grammar claims none of them.
func TestTheRefusalGrammarIsInvisibleToTheSevenExistingPrefixes(t *testing.T) {
	existing := map[string]string{
		"review-request":       requestMarker,
		"relayed-review":       relayedReviewMarker,
		"attestation":          attestationMarker,
		"withdrawn":            WithdrawnMarker,
		"wake":                 WakeMarker,
		"architecture-request": architectureRequestMarker,
		"architecture":         architectureResponseMarker,
	}
	for name, marker := range existing {
		if marker == architectureRefusalMarker {
			t.Fatalf("the addition reuses the existing %s prefix %q", name, marker)
		}
		body := marker + "\ntask=" + architectureBinding().TaskID + "\n\npayload"
		if ArchitectureRefusalShaped(body) {
			t.Errorf("a %s envelope reads as refusal-shaped", name)
		}
		if _, err := ParseArchitectureRefusal(body); err == nil ||
			!strings.Contains(err.Error(), ErrNotAnArchitectureRefusal.Error()) {
			t.Errorf("a %s envelope was read by the refusal parser: %v", name, err)
		}
	}

	// The prefixes whose emitters live in reach still parse EXACTLY as before.
	req, refusal := architectureRefusalFixture()
	requestWire, err := req.Marker()
	if err != nil {
		t.Fatal(err)
	}
	back, ok := ParseArchitectureRequest(requestWire)
	if !ok || !back.Binding.Same(req.Binding) || back.RequestID != req.RequestID || back.Prompt != req.Prompt {
		t.Errorf("the architecture request no longer parses to itself: %+v", back)
	}
	answerWire, err := ArchitectureResponse{Binding: req.Binding, RequestID: req.RequestID,
		Body: `{"decision":"proceed","summary":"bounded","plan":"do it"}`}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	answer, ok := ParseArchitectureResponse(answerWire)
	if !ok || !answer.Answers(req) {
		t.Errorf("the architecture response no longer answers its request: %+v", answer)
	}
	wakeWire, err := RenderWake(4242)
	if err != nil {
		t.Fatal(err)
	}
	if id, werr := ParseWake(wakeWire); werr != nil || id != 4242 {
		t.Errorf("the wake no longer parses to its locator: %d %v", id, werr)
	}
	withdrawnWire, err := RenderWithdrawal(req.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if id, werr := ParseWithdrawal(withdrawnWire); werr != nil || id != req.RequestID {
		t.Errorf("the withdrawal no longer parses to its request: %q %v", id, werr)
	}
	reviewRequestWire, err := reqC1().Marker()
	if err != nil {
		t.Fatal(err)
	}
	if parsed, rok := ParseRequest(reviewRequestWire); !rok || parsed.RequestID != reqC1().RequestID {
		t.Errorf("the review request no longer parses to itself: %+v", parsed)
	}

	// And the other direction: a refusal is refused by every one of those parsers.
	refusalWire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ParseArchitectureRequest(refusalWire); ok {
		t.Error("a refusal was read as an architecture request")
	}
	if _, ok := ParseArchitectureResponse(refusalWire); ok {
		t.Error("a refusal was read as an architecture answer, which would make a refusal into a decision")
	}
	if _, ok := ParseRequest(refusalWire); ok {
		t.Error("a refusal was read as a review request")
	}
	if _, err := ParseWake(refusalWire); err == nil {
		t.Error("a refusal was read as a wake")
	}
	if _, err := ParseWithdrawal(refusalWire); err == nil {
		t.Error("a refusal was read as a withdrawal")
	}
}

// The stage vocabulary is CLOSED and read by membership.
//
// Membership rather than exclusion, because an exclusion test admits every
// spelling nobody thought to exclude -- and a stage decides what a refusal
// means, so failing open there is failing open about meaning.
func TestTheRefusalStageVocabularyIsClosedAndReadByMembership(t *testing.T) {
	for _, stage := range []RefusalStage{
		RefusalStagePinnedEvidence, RefusalStageWorkspace, RefusalStageAnswerContract,
	} {
		if !stage.Known() {
			t.Errorf("declared stage %q is not a member of its own vocabulary", stage)
		}
	}
	for _, stage := range []RefusalStage{"", "PINNED-EVIDENCE", "pinned_evidence", "quota", "anything"} {
		if stage.Known() {
			t.Errorf("stage %q was admitted to a closed vocabulary", stage)
		}
		_, refusal := architectureRefusalFixture()
		refusal.Stage = stage
		if _, err := refusal.Marker(); err == nil {
			t.Errorf("a refusal at unknown stage %q was rendered rather than refused", stage)
		}
	}
}

// A refusal with no bound request cannot be spoken at all.
//
// The consumer-side rule stated in the protocol itself: only a refusal reached
// AFTER an exact request has been authenticated and bound may be posted. A wake
// that could not be attributed, or an event with no trustworthy request behind
// it, has no binding to echo -- and the envelope offers no way to invent one, no
// default, and no partial fill. Those failures stay consumer-side diagnostics,
// which is a limit rather than a gap: a refusal whose subject was guessed would
// terminate whichever exchange the guess happened to name.
func TestARefusalWithNoBoundRequestCannotBeRendered(t *testing.T) {
	_, whole := architectureRefusalFixture()
	if _, err := whole.Marker(); err != nil {
		t.Fatalf("the whole fixture is not renderable, so these subtractions prove nothing: %v", err)
	}
	noBinding := whole
	noBinding.Binding = roles.ArchitectureBinding{}
	noRequest := whole
	noRequest.RequestID = ""
	noTask := whole
	noTask.Binding.TaskID = ""
	noObjective := whole
	noObjective.Binding.ObjectiveDigest = ""
	noBase := whole
	noBase.Binding.BaseSHA = ""
	noReason := whole
	noReason.Reason = "   "
	for name, unbound := range map[string]ArchitectureRefusal{
		"nothing bound at all": noBinding,
		"no request":           noRequest,
		"no task":              noTask,
		"no objective digest":  noObjective,
		"no base":              noBase,
		"no reason":            noReason,
	} {
		t.Run(name, func(t *testing.T) {
			wire, err := unbound.Marker()
			if err == nil {
				t.Fatalf("a refusal with %s was rendered rather than refused:\n%s", name, wire)
			}
			if wire != "" {
				t.Errorf("a refused render still produced bytes: %q", wire)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// POSITIONAL FRAMING: identity comes from the envelope at position zero.
//
// LAW. An envelope is what a message BEGINS with, never what it MENTIONS. A
// classifier that decides what an artifact IS by searching its whole body for
// marker-shaped substrings cannot tell a message from a message ABOUT messages
// -- and a protocol that cannot discuss its own vocabulary has made itself
// undiscussable.
// ---------------------------------------------------------------------------

// quotedMarkers is the sentence that made a correct verdict undeliverable on
// 2026-09-24, plus every other envelope this package knows, so a repair cannot
// satisfy the witnesses by special-casing one marker.
const quotedMarkers = "a bare " + architectureRefusalMarker + " comment is classified as ordinary " +
	"content; the same is true of " + architectureRequestMarker + ", " + architectureResponseMarker +
	", " + requestMarker + ", " + relayedReviewMarker + ", " + attestationMarker + ", " +
	WakeMarker + " and " + WithdrawnMarker + "."

// POSITIONAL FRAMING W2 -- QUOTED MARKERS IN ARCHITECTURE AND REFUSAL TEXT.
//
// The same property W1 proves for a review, proved here for the two envelopes on
// the architecture side. It exists so a later repair cannot special-case reviews
// and leave every other participant unable to describe the protocol it is taking
// part in: an architect's plan that names a marker, and a consumer's refusal
// reason that quotes the marker it could not read, are both ordinary payload.
//
// POSITIONAL FRAMING W8 is asserted in the same place: the payload holds EIGHT
// marker-shaped strings and yields exactly ONE artifact. The remainder after the
// envelope is never rescanned as further mailbox artifacts, so none of those
// eight becomes a second observation of any kind.
func TestArchitectureTextMayQuoteProtocolMarkers(t *testing.T) {
	req, _ := architectureRefusalFixture()

	t.Run("an answer whose plan quotes markers", func(t *testing.T) {
		body := `{"decision":"proceed","summary":"` + quotedMarkers + `","plan":"repair the classifier"}`
		wire, err := ArchitectureResponse{
			Binding: req.Binding, RequestID: req.RequestID, Body: body,
		}.Marker()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(wire, "[sensei-code:") < 9 {
			t.Fatalf("the fixture quotes too few markers to prove anything:\n%s", wire)
		}
		got, ok := ParseArchitectureResponse(wire)
		if !ok {
			t.Fatal("an architecture answer quoting protocol markers was not read as an answer")
		}
		if got.Body != body {
			t.Errorf("the payload was altered:\n got %q\nwant %q", got.Body, body)
		}
		if !got.Answers(req) {
			t.Error("the quoted markers rewrote the answer's binding")
		}
		// NO RESCAN. The payload quotes a refusal envelope; the comment is an
		// answer and nothing else, and it settles as one.
		if ArchitectureRefusalShaped(wire) {
			t.Error("an answer quoting the refusal marker was classified refusal-shaped")
		}
		obs := observeRefusals(t, req, wire)
		if len(obs.Terminals) != 1 {
			t.Fatalf("one comment yielded %d terminals; its payload was rescanned: %+v",
				len(obs.Terminals), obs.Terminals)
		}
		if len(obs.Rejected) != 0 {
			t.Errorf("quoted markers were reported as unusable artifacts: %+v", obs.Rejected)
		}
		settled, ok := obs.Settlement()
		if !ok || settled.Refused() || settled.Answer == nil {
			t.Fatalf("an answer quoting markers did not settle as an answer: %+v", settled)
		}
	})

	t.Run("a refusal whose reason quotes markers", func(t *testing.T) {
		reason := "I could not read the reply. " + quotedMarkers
		wire, err := ArchitectureRefusal{
			Binding: req.Binding, RequestID: req.RequestID,
			Stage: RefusalStageAnswerContract, Reason: reason,
		}.Marker()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(wire, "[sensei-code:") < 9 {
			t.Fatalf("the fixture quotes too few markers to prove anything:\n%s", wire)
		}
		back, perr := ParseArchitectureRefusal(wire)
		if perr != nil {
			t.Fatalf("a refusal whose reason quotes protocol markers was refused: %v", perr)
		}
		if back.Reason != reason {
			t.Errorf("the consumer's diagnostic was altered:\n got %q\nwant %q", back.Reason, reason)
		}
		if !back.Refuses(req) {
			t.Error("the quoted markers rewrote the refusal's binding")
		}
		obs := observeRefusals(t, req, wire)
		if len(obs.Terminals) != 1 || len(obs.Rejected) != 0 {
			t.Fatalf("one refusal yielded %d terminals and %d rejections; its reason was rescanned",
				len(obs.Terminals), len(obs.Rejected))
		}
		settled, ok := obs.Settlement()
		if !ok || !settled.Refused() || settled.Refusal.Reason != reason {
			t.Fatalf("a refusal quoting markers did not settle with its own reason: %+v", settled)
		}
	})
}

// POSITIONAL FRAMING W3 CONTROL, at the architecture envelopes. Ordinary content
// that merely CONTAINS a marker is ordinary content: it is not identified as an
// artifact of any kind and it settles nothing.
//
// The other direction of W2, and the one that keeps the repair from becoming
// permissive. Without it, "stop searching the body" could be satisfied by a
// reader that accepted anything containing a marker.
func TestOrdinaryArchitectureContentContainingAMarkerIsNotAnArtifact(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	refusalWire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	answerWire, err := ArchitectureResponse{Binding: req.Binding, RequestID: req.RequestID,
		Body: `{"decision":"proceed","summary":"bounded","plan":"do it"}`}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"prose naming the markers":      quotedMarkers,
		"a refusal behind a preamble":   "for the record:\n" + refusalWire,
		"an answer behind a preamble":   "for the record:\n" + answerWire,
		"a refusal behind one word":     "quote " + refusalWire,
		"a marker in a bulleted list":   "- the " + architectureResponseMarker + " envelope\n- and its payload\n",
		"an answer quoted in a refusal": "here is what I was sent:\n" + answerWire + "\n" + refusalWire,
	} {
		t.Run(name, func(t *testing.T) {
			if ArchitectureRefusalShaped(body) {
				t.Errorf("ordinary content was classified refusal-shaped: %q", body)
			}
			if _, ok := ParseArchitectureResponse(body); ok {
				t.Errorf("ordinary content was read as an architecture answer: %q", body)
			}
			if _, ok := ParseArchitectureRequest(body); ok {
				t.Errorf("ordinary content was read as an architecture request: %q", body)
			}
			obs := observeRefusals(t, req, body)
			if _, ok := obs.Settlement(); ok {
				t.Errorf("ordinary content settled the request: %+v", obs.Terminals)
			}
			if len(obs.Rejected) != 0 {
				t.Errorf("ordinary content was reported as an unusable artifact: %+v", obs.Rejected)
			}
		})
	}
}

// POSITIONAL FRAMING W4 AND W5 -- AN ATTRIBUTABLE MALFORMED ENVELOPE AT ZERO.
//
// A body whose FIRST bytes claim a known envelope remains a malformed artifact
// OF THAT KIND. It must not fall back to ordinary content, it must be visibly
// rejected with a stated reason, and it must settle nothing.
//
// W5 is the first case: a BARE refusal marker. Shaping used to require the
// marker followed by a newline, so a bare marker -- or one followed by any other
// delimiter -- began with the additive prefix and was still classified ordinary
// content. It never reached the parser and never reached the rejected list, so
// silence returned at exactly the boundary the refusal envelope exists to
// remove.
//
// A7 IS WHAT KEEPS THIS TRUE. Identification precedes parsing: the first token
// establishes provenance, and a failure later in the body stays attributable to
// that kind. A reader that identified an artifact only once it had parsed would
// answer "not a refusal" for both a broken refusal and a shopping list, and the
// whole point of this envelope -- telling a consumer that failed from one that
// never spoke -- would be gone while the framing witnesses still passed.
func TestAMalformedRefusalEnvelopeAtZeroIsAttributableNotOrdinary(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	good, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ body, names string }{
		"a bare marker":             {architectureRefusalMarker, "newline"},
		"a marker and nothing else": {architectureRefusalMarker + "\n", "payload boundary"},
		"a space after the marker":  {architectureRefusalMarker + " stage=workspace\n\nwhy", "newline"},
		"a colon after the marker":  {architectureRefusalMarker + ": workspace\n\nwhy", "newline"},
		"a marker on a shared line": {architectureRefusalMarker + " " + good, "newline"},
		"no stage":                  {strings.Replace(good, "stage="+string(refusal.Stage)+"\n", "", 1), "stage"},
		"an unknown stage":          {strings.Replace(good, "stage="+string(refusal.Stage), "stage=whenever", 1), "stage"},
		"an unknown vocabulary":     {strings.Replace(good, "stage_vocabulary=v1", "stage_vocabulary=v9", 1), "vocabulary"},
		"no reason":                 {strings.TrimSuffix(good, refusal.Reason), "payload"},
	} {
		t.Run(name, func(t *testing.T) {
			// IDENTIFIED: these bytes claim the refusal envelope at position zero.
			if !ArchitectureRefusalShaped(tc.body) {
				t.Fatalf("a body opening with the refusal marker was not classified "+
					"refusal-shaped, so it fell back to ordinary content: %q", tc.body)
			}
			// AND REFUSED, with a reason.
			got, perr := ParseArchitectureRefusal(tc.body)
			if perr == nil {
				t.Fatalf("a malformed refusal parsed: %+v", got)
			}
			if got != (ArchitectureRefusal{}) {
				t.Fatalf("a refused refusal was returned partly populated: %+v", got)
			}
			if !strings.Contains(perr.Error(), tc.names) {
				t.Errorf("the diagnostic does not name %q, so the failure is not attributable "+
					"to this envelope's grammar: %v", tc.names, perr)
			}
			// AND VISIBLE, AND SETTLING NOTHING.
			obs := observeRefusals(t, req, tc.body)
			if _, ok := obs.Settlement(); ok {
				t.Fatal("a malformed refusal settled the request")
			}
			if len(obs.Rejected) != 1 {
				t.Fatalf("a malformed refusal produced %d rejections, want exactly 1: %+v",
					len(obs.Rejected), obs.Rejected)
			}
			rej := obs.Rejected[0]
			if rej.Comment == 0 || rej.Author == "" || strings.TrimSpace(rej.Diagnostic) == "" {
				t.Fatalf("the rejection does not say where, from whom, or why: %+v", rej)
			}
		})
	}
}
