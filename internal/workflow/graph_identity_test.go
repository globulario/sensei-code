package workflow

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
)

// ONE EVALUATION OWNS BOTH THE START VERDICT AND THE GRAPH IDENTITY.
//
//	one start preflight evaluation
//	  -> certifiedStart.GraphBuildCommit()
//	  -> task GraphBinding.Digest
//	  -> ArchitectureBinding.GraphBuildCommit
//
// bindGraph used to run immediately after the workspace status, before the
// preflight existed, and read the commit from the workspace authority. That
// type HAS the field, so it compiled and read correctly -- but
// sensei.workspace.identity.v1 deliberately projects a bounded authority that
// never populates it, so the decode produced "" on every governed run.
// ArchitectureBinding requires ^[0-9a-f]{40}$ and refused every architect turn,
// reporting a missing graph identity for a graph whose identity was sitting in
// the other surface the gate had already decoded.
//
// Nothing below reads a prompt, re-calls awareness_metadata, substitutes the
// workspace authority, falls back to certified_awareness_graph_commit or
// source_repo_commit, substitutes the live-store digest, or expands a prefix.

const canonicalGraphCommit = "fd350489a19394be847d770f2e8dd7b8f1b75303"

// preflightWithCommit is the exact shape the live producer returns for the
// governed start: PREFLIGHT_STATUS_EMPTY with a certifiable authority.
func preflightWithCommit(t *testing.T, commit string) sensei.ToolResult {
	t.Helper()
	return result(t, "", `{
		"status": "PREFLIGHT_STATUS_EMPTY",
		"risk_class": "UNKNOWN_IMPACT",
		"authority": {
			"authoritative": true,
			"verdict": "authoritative",
			"state": "current",
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
			"seed_state": "SEED_STATE_CURRENT",
			"build_provenance_state": "BUILD_PROVENANCE_STATE_STAMPED",
			"graph_build_commit": "`+commit+`",
			"source_repo_commit": "39a8d2809ef239f203d5365d7f6e170349186cc4",
			"live_store_graph_digest_sha256": "3894607e6c37f203c2c0b4128d821761d1c5d064d745d6858b012248cc32f91f"
		}
	}`)
}

func startWithCommit(t *testing.T, commit string) certifiedStart {
	t.Helper()
	start, err := certifyStart(result(t, "", okWorkspace), preflightWithCommit(t, commit), "")
	if err != nil {
		t.Fatalf("certify a start carrying %q: %v", commit, err)
	}
	return start
}

func engineForBinding(t *testing.T) *Engine {
	t.Helper()
	return New(gitx.Repo{Root: t.TempDir()}, config.Default(), event.NewBus(), nil, "sess-graph")
}

// The canonical value passes through byte-exact, all the way to the binding a
// GitHub architect turn is issued under.
func TestTheCertifiedGraphCommitReachesTheArchitectureBindingByteExact(t *testing.T) {
	e := engineForBinding(t)
	start := startWithCommit(t, canonicalGraphCommit)

	if got := start.GraphBuildCommit(); got != canonicalGraphCommit {
		t.Fatalf("certifiedStart.GraphBuildCommit() = %q", got)
	}
	e.bindGraph("task-1", start)

	bound := e.graphFor("task-1")
	if bound == nil {
		t.Fatal("no graph binding was installed for a certified start")
	}
	if bound.Digest != start.GraphBuildCommit() {
		t.Fatalf("graphFor().Digest = %q, want certifiedStart.GraphBuildCommit() %q", bound.Digest, start.GraphBuildCommit())
	}
	if bound.Domain != start.Domain() {
		t.Errorf("binding domain = %q, want %q", bound.Domain, start.Domain())
	}

	// The architect RunnerSpec receives that exact commit.
	e.recordObjective("task-1", Objective{Text: "an objective", Provenance: SubmittedByLocalOperator})
	b := e.architectureBinding("task-1")
	if b.GraphBuildCommit != canonicalGraphCommit {
		t.Fatalf("ArchitectureBinding.GraphBuildCommit = %q, want %q", b.GraphBuildCommit, canonicalGraphCommit)
	}
	if !canonicalCommit(b.GraphBuildCommit) {
		t.Error("the architecture binding carries a non-canonical commit")
	}
}

// A commit that is missing, abbreviated, or malformed is REFUSED -- not
// accepted, not expanded, not substituted from another field.
func TestANonCanonicalGraphCommitIsRefusedRatherThanRepaired(t *testing.T) {
	cases := map[string]string{
		"missing":             "",
		"twelve characters":   "fd350489a193",
		"forty-one":           canonicalGraphCommit + "a",
		"thirty-nine":         canonicalGraphCommit[:39],
		"uppercase":           strings.ToUpper(canonicalGraphCommit),
		"non-hex":             "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
		"whitespace padded":   "  " + canonicalGraphCommit + "  ",
		"a sha256 by mistake": "3894607e6c37f203c2c0b4128d821761d1c5d064d745d6858b012248cc32f91f",
	}
	for name, commit := range cases {
		t.Run(name, func(t *testing.T) {
			e := engineForBinding(t)
			start := startWithCommit(t, commit)
			e.bindGraph("task-1", start)

			bound := e.graphFor("task-1")
			if bound == nil {
				t.Fatal("no binding installed at all")
			}
			// Padded canonical is the one case that legitimately survives: the
			// bytes are trimmed, never expanded or reshaped.
			if name == "whitespace padded" {
				if bound.Digest != canonicalGraphCommit {
					t.Fatalf("Digest = %q, want the trimmed canonical id", bound.Digest)
				}
				return
			}
			if bound.Digest != "" {
				t.Fatalf("a %s commit was installed as %q; it must be refused, not repaired", name, bound.Digest)
			}

			// And the architect turn cannot be bound.
			e.recordObjective("task-1", Objective{Text: "an objective", Provenance: SubmittedByLocalOperator})
			if b := e.architectureBinding("task-1"); b.Valid() {
				t.Fatalf("a %s commit produced a valid architecture binding: %+v", name, b)
			}
		})
	}
}

// A prefix of the real commit is still not the real commit. This is the exact
// value the producer used to publish.
func TestAPrefixOfTheCertifiedCommitIsNotTheCertifiedCommit(t *testing.T) {
	short := canonicalGraphCommit[:12]
	if !strings.HasPrefix(canonicalGraphCommit, short) {
		t.Fatal("fixture is not a prefix; the test proves nothing")
	}
	if canonicalCommit(short) {
		t.Fatal("a 12-character prefix is being accepted as a canonical identity")
	}

	e := engineForBinding(t)
	e.bindGraph("task-1", startWithCommit(t, short))
	if got := e.graphFor("task-1").Digest; got != "" {
		t.Fatalf("the abbreviated commit was installed as %q -- expanded or accepted", got)
	}
}

// The workspace authority can be present and complete and still supply no
// graph identity. That is the contract, not a degraded state.
func TestAWorkspaceAuthorityCannotSupplyTheArchitectureIdentity(t *testing.T) {
	// A workspace whose authority is present, authoritative, and carries every
	// field workspace-v1 owns -- but not graph_build_commit, because v1 does
	// not project it.
	ws := result(t, "", `{
		"composition_state": "complete",
		"binding": {"repository_domain": "github.com/globulario/sensei-code"},
		"graph_authority": {
			"authoritative": true,
			"seed_state": "SEED_STATE_CURRENT",
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
			"build_provenance_state": "BUILD_PROVENANCE_STATE_STAMPED",
			"certified_awareness_graph_commit": "",
			"certified_services_repo_commit": "",
			"live_store_graph_digest_sha256": "3894607e6c37f203c2c0b4128d821761d1c5d064d745d6858b012248cc32f91f"
		}
	}`)
	start, err := certifyStart(ws, preflightWithCommit(t, canonicalGraphCommit), "")
	if err != nil {
		t.Fatalf("certify: %v", err)
	}

	e := engineForBinding(t)
	e.bindGraph("task-1", start)

	// The identity came from the PREFLIGHT, and the live-store digest -- present
	// on both surfaces -- was not substituted for it.
	got := e.graphFor("task-1").Digest
	if got != canonicalGraphCommit {
		t.Fatalf("Digest = %q, want the preflight's certified commit", got)
	}
	if got == "3894607e6c37f203c2c0b4128d821761d1c5d064d745d6858b012248cc32f91f" {
		t.Fatal("the live-store digest was substituted for the graph build commit")
	}
	if got == start.SourceRepoCommit() {
		t.Fatal("source_repo_commit was substituted for the graph build commit")
	}
}

// PREFLIGHT_STATUS_EMPTY with a certifiable authority is a valid start. No plan
// exists yet, so no file list can be named; the file-scoped verdict is deferred.
func TestAnEmptyPreflightWithCertifiableAuthorityStillStarts(t *testing.T) {
	start, err := certifyStart(result(t, "", okWorkspace), preflightWithCommit(t, canonicalGraphCommit), "")
	if err != nil {
		t.Fatalf("an empty-but-certifiable start was refused: %v", err)
	}
	if start.GraphBuildCommit() != canonicalGraphCommit {
		t.Fatalf("GraphBuildCommit = %q", start.GraphBuildCommit())
	}
}

// A start the gate refuses installs NO binding. A task that never started must
// not leave a graph identity behind for a later turn to read.
func TestARefusedStartInstallsNoGraphBinding(t *testing.T) {
	uncertifiable := result(t, "", `{
		"status": "PREFLIGHT_STATUS_OK",
		"authority": {"authoritative": false, "verdict": "not_authoritative", "graph_build_commit": "`+canonicalGraphCommit+`"}
	}`)
	if _, err := certifyStart(result(t, "", okWorkspace), uncertifiable, ""); err == nil {
		t.Fatal("an uncertifiable start was allowed")
	}

	// The engine never reaches bindGraph on that path, so nothing is bound.
	e := engineForBinding(t)
	if e.graphFor("task-1") != nil {
		t.Fatal("a graph binding exists for a task that never certified a start")
	}
	e.recordObjective("task-1", Objective{Text: "an objective", Provenance: SubmittedByLocalOperator})
	if b := e.architectureBinding("task-1"); b.Valid() {
		t.Fatalf("an architect turn was bindable with no certified start: %+v", b)
	}
}

// Resume takes the graph world THIS start certified, not the one the previous
// run recorded. The graph may have been rebuilt while the task was not running.
func TestResumeBindsTheNewlyCertifiedGraphWorld(t *testing.T) {
	const previous = "9004725eb6c0a77830fea592adb0839ce0d86ec1"
	e := engineForBinding(t)

	e.bindGraph("task-1", startWithCommit(t, previous))
	if got := e.graphFor("task-1").Digest; got != previous {
		t.Fatalf("first run bound %q", got)
	}

	// The graph is rebuilt; the task resumes and re-certifies.
	e.bindGraph("task-1", startWithCommit(t, canonicalGraphCommit))
	got := e.graphFor("task-1").Digest
	if got == previous {
		t.Fatal("resume resurrected the previous run's graph commit")
	}
	if got != canonicalGraphCommit {
		t.Fatalf("resume bound %q, want the newly certified %q", got, canonicalGraphCommit)
	}

	e.recordObjective("task-1", Objective{Text: "an objective", Provenance: SubmittedByLocalOperator})
	if b := e.architectureBinding("task-1"); b.GraphBuildCommit != canonicalGraphCommit {
		t.Fatalf("the architecture binding still names %q", b.GraphBuildCommit)
	}
}

// The abbreviation fossil is gone, and cannot be reintroduced by accident.
func TestPrefixCommitComparisonIsNotAvailable(t *testing.T) {
	if !canonicalCommit(canonicalGraphCommit) {
		t.Fatal("a canonical object id is not recognized")
	}
	for _, notCanonical := range []string{"", "fd350489a193", strings.ToUpper(canonicalGraphCommit), canonicalGraphCommit + "0"} {
		if canonicalCommit(notCanonical) {
			t.Errorf("%q was accepted as a canonical identity", notCanonical)
		}
	}
}

// The architecture binding is minted from engine-owned records, and the graph
// half of it is the one the gate certified.
func TestTheArchitectureBindingHasExactlyOneGraphSource(t *testing.T) {
	e := engineForBinding(t)
	e.bindGraph("task-9", startWithCommit(t, canonicalGraphCommit))
	e.recordObjective("task-9", Objective{Text: "  exact objective bytes  \n", Provenance: SubmittedByLocalOperator})

	b := e.architectureBinding("task-9")
	if b.GraphBuildCommit != e.graphFor("task-9").Digest {
		t.Fatalf("the architecture binding (%q) and the task binding (%q) disagree", b.GraphBuildCommit, e.graphFor("task-9").Digest)
	}
	if b.ObjectiveDigest != roles.BindArchitecture("t", "  exact objective bytes  \n", "", "").ObjectiveDigest {
		t.Error("the objective digest does not name the exact recorded bytes")
	}
}
