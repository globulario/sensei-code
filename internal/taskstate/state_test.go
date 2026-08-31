package taskstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() State {
	return State{
		TaskID: "task-9", SessionID: "sess-1", Task: "add a --json flag to the report command",
		Domain:  "github.com/globulario/sensei-code",
		BaseSHA: "abcdef0123456789", Branch: "sensei-code/task-9", Worktree: "/wt/task-9",
		Contract: Contract{
			Rationale:    "the report is only readable by a human today",
			Steps:        []string{"add the flag", "render JSON", "cover it with a test"},
			Files:        []string{"internal/architect/report.go"},
			Invariants:   []string{"invariant:sensei_code.report.counts_carry_provenance"},
			Consequences: "scripts can consume the report",
		},
		Authority: []AuthorityDecision{{
			Question: "May the JSON shape omit lower-bound counts?", Chosen: "No, keep provenance on every count",
			Condition: "graph coverage is absent for the planned files", Durable: true,
			DecidedAt: time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC),
		}},
		Evidence: Evidence{
			DiffBytes: 2048, ChangedPaths: []string{"internal/architect/report.go"},
			AuditVerdict: "review", AuditDetail: "touches a governed report surface",
			RequiredTests: []string{"internal/architect/report_test.go:TestCountsCarryProvenance"},
		},
		Open:  []Finding{{Source: "reviewer", Detail: "the JSON omits the unpublished-domain caveat"}},
		Phase: Reviewing, Workers: []string{"claude"},
		GraphBuildCommit: "9723c9b177f1", ObservedAt: time.Date(2026, 8, 16, 11, 0, 0, 0, time.UTC),
	}
}

// TestHandoverCarriesSemanticStateNotConversation is the core requirement: the
// next worker receives what is true about the task, not what was said about it.
func TestHandoverCarriesSemanticStateNotConversation(t *testing.T) {
	out := sample().Handover("claude", "9723c9b177f1")

	required := map[string]string{
		"task-9":                         "task identity",
		"abcdef0123456789":               "base SHA",
		"sensei-code/task-9":             "candidate identity",
		"the report is only readable":    "architectural contract",
		"render JSON":                    "contract steps",
		"invariant:sensei_code.report":   "governing invariants",
		"keep provenance on every count": "prior authority decision",
		"internal/architect/report.go":   "current evidence",
		"review":                         "audit verdict",
		"TestCountsCarryProvenance":      "required tests",
		"omits the unpublished-domain":   "unresolved finding",
		"reviewing":                      "workflow phase",
	}
	for fragment, what := range required {
		if !strings.Contains(out, fragment) {
			t.Errorf("handover does not carry %s (missing %q)", what, fragment)
		}
	}
}

// TestHandoverIsNotAColdPromptRestart covers "no cold-prompt restart": the next
// worker is told plainly that work exists and must be continued.
func TestHandoverIsNotAColdPromptRestart(t *testing.T) {
	out := sample().Handover("claude", "9723c9b177f1")
	if !strings.Contains(out, "not a fresh start") {
		t.Error("handover does not say the task is already under way")
	}
	if !strings.Contains(out, "Do not start over") {
		t.Error("handover does not tell the next worker to continue rather than restart")
	}
	if !strings.Contains(out, "claude") {
		t.Error("handover does not name who worked on it before")
	}
}

// TestStaleHandoverIsRefreshedOrRejectedNotHidden covers "stale handoff context
// is refreshed or explicitly rejected". Silence is the failure: a worker given
// stale architectural facts with no warning will use them confidently.
func TestStaleHandoverIsRefreshedOrRejectedNotHidden(t *testing.T) {
	s := sample()
	if s.Stale("9723c9b177f1") {
		t.Fatal("state read at the current generation reported itself stale")
	}
	if !s.Stale("ffff00001111") {
		t.Fatal("state read at an older generation did not report itself stale")
	}
	// An unknown current generation is stale: "I could not check" is not "fine".
	if !s.Stale("") {
		t.Fatal("an unknown current graph generation was treated as fresh")
	}
	blank := sample()
	blank.GraphBuildCommit = ""
	if !blank.Stale("9723c9b177f1") {
		t.Fatal("state that never recorded a generation was treated as fresh")
	}

	out := s.Handover("claude", "ffff00001111")
	if !strings.Contains(out, "different graph generation") {
		t.Fatalf("a stale handover did not warn the next worker:\n%s", out)
	}
	if !strings.Contains(out, "Re-read Sensei") {
		t.Error("a stale handover did not say what to do about it")
	}

	// Refreshing rebinds it, and the warning goes away.
	refreshed := s.Refresh("ffff00001111", time.Now())
	if refreshed.Stale("ffff00001111") {
		t.Fatal("refresh did not rebind the generation")
	}
	if strings.Contains(refreshed.Handover("claude", "ffff00001111"), "different graph generation") {
		t.Error("a refreshed handover still warns about staleness")
	}
}

// TestHandoverCarriesNoAuthority covers "local session loss cannot manufacture
// missing Sensei authority". Nothing in the state is shaped like permission,
// and the handover says so to the worker reading it.
func TestHandoverCarriesNoAuthority(t *testing.T) {
	out := sample().Handover("claude", "9723c9b177f1")
	if !strings.Contains(out, "carries no authority") {
		t.Fatal("handover does not disclaim authority")
	}
	for _, forbidden := range []string{"certified", "approved to proceed", "authority granted"} {
		if strings.Contains(strings.ToLower(out), forbidden) {
			t.Errorf("handover contains %q, which a worker could read as permission", forbidden)
		}
	}
	// A non-durable human decision is marked as such, so it is not mistaken for
	// settled project knowledge.
	s := sample()
	s.Authority[0].Durable = false
	if !strings.Contains(s.Handover("claude", "9723c9b177f1"), "not yet project knowledge") {
		t.Error("an unpersisted human decision was presented as settled knowledge")
	}
}

// TestStateSurvivesAProcessRestart covers continuity across worker changes that
// span a restart, which is when a transcript is least likely to still exist.
func TestStateSurvivesAProcessRestart(t *testing.T) {
	root := t.TempDir()
	original := sample()
	if err := original.Save(root); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := Load(root, "task-9")
	if err != nil || !ok {
		t.Fatalf("state did not survive: ok=%v err=%v", ok, err)
	}
	if loaded.BaseSHA != original.BaseSHA || loaded.Phase != original.Phase {
		t.Fatalf("identity or phase lost: %+v", loaded)
	}
	if len(loaded.Authority) != 1 || loaded.Authority[0].Chosen != original.Authority[0].Chosen {
		t.Fatalf("authority decisions lost: %+v", loaded.Authority)
	}
	if len(loaded.Open) != 1 {
		t.Fatalf("open findings lost: %+v", loaded.Open)
	}
	if loaded.Evidence.AuditVerdict != "review" {
		t.Fatalf("evidence lost: %+v", loaded.Evidence)
	}
}

// TestUnreadableOrForeignStateFailsClosed keeps a corrupt or future-versioned
// file from being read as "no state", which would silently cold-start a task
// that already had decisions attached to it.
func TestUnreadableOrForeignStateFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root+"/.sensei-code/tasks", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(root, "bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(root, "bad"); err == nil {
		t.Fatal("a corrupt task state read as absent")
	}
	if err := os.WriteFile(path(root, "future"), []byte(`{"version":99,"task_id":"future"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Load(root, "future"); err == nil || ok {
		t.Fatal("a state written by a newer build was read as usable")
	}
}

// TestWorkerChainIsRecordedWithoutRepetition keeps the provenance readable when
// one worker runs several cycles.
func TestWorkerChainIsRecordedWithoutRepetition(t *testing.T) {
	s := State{}
	s.RecordWorker("claude")
	s.RecordWorker("claude")
	s.RecordWorker("codex")
	s.RecordWorker("claude")
	got := strings.Join(s.Workers, ",")
	if got != "claude,codex,claude" {
		t.Fatalf("worker chain is %q; consecutive repeats should collapse but a genuine switch back should not", got)
	}
}

// TestEmptyOpenListSaysSoRatherThanImplyingDone stops an absent list from
// reading as a clean bill of health.
func TestEmptyOpenListSaysSoRatherThanImplyingDone(t *testing.T) {
	s := sample()
	s.Open = nil
	out := s.Handover("claude", "9723c9b177f1")
	if !strings.Contains(out, "nothing recorded as open") {
		t.Fatal("an empty findings list rendered as silence")
	}
	if !strings.Contains(out, "before assuming it is done") {
		t.Fatal("an empty findings list implied the work was finished")
	}
}

// ---------------------------------------------------------------------------
// Run dimensions.
// ---------------------------------------------------------------------------

func candidateA() CandidateIdentity {
	return CandidateIdentity{BaseSHA: "76dcea91d867", DiffDigest: "aebb428c68ab"}
}

func candidateB() CandidateIdentity {
	return CandidateIdentity{BaseSHA: "76dcea91d867", DiffDigest: "db93955b9e3e"}
}

func sourced(dim Dimension, value string, c CandidateIdentity, at time.Time) Observation {
	return Observation{
		Dimension: dim, Value: value, Candidate: c,
		Producer: "reviewer:codex", Source: "event:review.completed/1", ObservedAt: at,
	}
}

// An observation without a producer or a source is an assertion, not a
// measurement. It must not enter the record as knowledge, and the omission must
// be visible rather than silently dropped.
func TestUnsourcedObservationBecomesUnobserved(t *testing.T) {
	var s State
	s.Record(Observation{Dimension: DimReview, Value: string(ReviewAccept), Candidate: candidateA()})
	got := s.Observations[0]
	if got.Value != unobserved {
		t.Fatalf("unsourced observation was recorded as %q", got.Value)
	}
	if !strings.Contains(got.Detail, "unsourced") || !strings.Contains(got.Detail, "ACCEPT") {
		t.Fatalf("detail does not name the omission or keep the value: %q", got.Detail)
	}
}

// A value this build does not know is not a value it may act on. It becomes
// unobserved and keeps the original, so a future member cannot read as a
// present one and cannot vanish either.
func TestUnknownVocabularyValueBecomesUnobservedAndKeepsTheOriginal(t *testing.T) {
	var s State
	s.Record(Observation{
		Dimension: DimAudit, Value: "AUDIT_PROBABLY_FINE", Candidate: candidateA(),
		Producer: "sensei:audit", Source: "event:candidate.audited/9",
	})
	got := s.Observations[0]
	if got.Value != unobserved {
		t.Fatalf("unknown value was accepted as %q", got.Value)
	}
	if !strings.Contains(got.Detail, "AUDIT_PROBABLY_FINE") {
		t.Fatalf("original value was lost: %q", got.Detail)
	}
}

// An observation that names no candidate may be recorded -- some facts are
// about the run -- but it can never become a current claim ABOUT a candidate.
func TestCandidatelessObservationIsNeverCurrent(t *testing.T) {
	var s State
	s.Record(Observation{
		Dimension: DimReview, Value: string(ReviewAccept),
		Producer: "reviewer:claude", Source: "event:review.completed/2",
	})
	if o, ok := s.Current(candidateA())[DimReview]; ok {
		t.Fatalf("an observation with no candidate identity became current: %+v", o)
	}
}

// Identity is content, not custody: the same diff at the same base is the same
// candidate whether it is committed or not, and a preservation commit that
// changes only the commit SHA must not change selection.
func TestIdentityIsContentNotCustody(t *testing.T) {
	var s State
	uncommitted := sourced(DimReview, string(ReviewAccept), candidateA(), time.Unix(10, 0))
	s.Record(uncommitted)
	preserved := sourced(DimScope, string(ScopeCompliant), candidateA(), time.Unix(20, 0))
	preserved.CommitSHA = "b26f46c81ce9" // custody changed, content did not
	s.Record(preserved)

	current := s.Current(candidateA())
	if current[DimReview].Value != string(ReviewAccept) {
		t.Fatalf("the uncommitted observation was not selected: %+v", current[DimReview])
	}
	if current[DimScope].Value != string(ScopeCompliant) {
		t.Fatalf("a commit SHA changed selection: %+v", current[DimScope])
	}
}

// A later candidate makes an earlier candidate's observations historical, not
// false. Selection must not carry candidate A's failure onto candidate B, and
// must not delete it either.
func TestEarlierCandidateStaysHistorical(t *testing.T) {
	var s State
	s.Record(sourced(DimImplementation, string(ImplementationRefused), candidateA(), time.Unix(10, 0)))
	s.Record(sourced(DimReview, string(ReviewAccept), candidateB(), time.Unix(20, 0)))

	current := s.Current(candidateB())
	if _, ok := current[DimImplementation]; ok {
		t.Fatal("candidate A's implementation state described candidate B")
	}
	if current[DimReview].Value != string(ReviewAccept) {
		t.Fatalf("candidate B's own verdict was not selected: %+v", current[DimReview])
	}
	hist := s.Historical(candidateB())
	if len(hist) != 1 || hist[0].Value != string(ImplementationRefused) {
		t.Fatalf("candidate A's record was not retained as history: %+v", hist)
	}
}

// Evaluator availability is a fact about the run, so it is selected whatever
// the candidate -- and it is never derived from the audit state, which is a
// separate dimension answering a different question.
func TestEvaluatorIsRunScopedAndSeparateFromAudit(t *testing.T) {
	var s State
	s.Record(Observation{
		Dimension: DimEvaluator, Value: string(EvaluatorUnreachable),
		Producer: "system:client", Source: "cmd:awareness_preflight", ObservedAt: time.Unix(10, 0),
	})
	s.Record(sourced(DimAudit, string(AuditCannotVerify), candidateA(), time.Unix(20, 0)))

	current := s.Current(candidateA())
	if current[DimEvaluator].Value != string(EvaluatorUnreachable) {
		t.Fatalf("a run-scoped observation was not selected: %+v", current[DimEvaluator])
	}
	if current[DimAudit].Value != string(AuditCannotVerify) {
		t.Fatalf("audit state changed: %+v", current[DimAudit])
	}
	if current[DimImplementation].Value != "" {
		t.Fatalf("an unreachable evaluator wrote an implementation state: %+v", current[DimImplementation])
	}
}

// The disagreement is the interesting event: a reviewer accepted, the audit
// could not certify, and admission was therefore deferred rather than refused.
// All three, and the override, must survive a save and reload.
func TestOverrodeAndDisagreementSurviveReload(t *testing.T) {
	dir := t.TempDir()
	s := State{TaskID: "t-1", SessionID: "s-1", Phase: Reviewing}
	s.Record(sourced(DimReview, string(ReviewAccept), candidateA(), time.Unix(10, 0)))
	s.Record(sourced(DimAudit, string(AuditCannotVerify), candidateA(), time.Unix(11, 0)))
	deferred := sourced(DimAdmission, string(AdmissionDeferred), candidateA(), time.Unix(12, 0))
	deferred.Overrode = true
	deferred.Detail = "Sensei audit could not verify this candidate"
	s.Record(deferred)
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := Load(dir, "t-1")
	if err != nil || !ok {
		t.Fatalf("load: %v ok=%v", err, ok)
	}
	current := loaded.Current(candidateA())
	if current[DimReview].Value != string(ReviewAccept) {
		t.Fatalf("reviewer verdict lost: %+v", current[DimReview])
	}
	if current[DimAudit].Value != string(AuditCannotVerify) {
		t.Fatalf("audit state lost: %+v", current[DimAudit])
	}
	if current[DimAdmission].Value != string(AdmissionDeferred) {
		t.Fatalf("admission was not deferred: %+v", current[DimAdmission])
	}
	if !current[DimAdmission].Overrode {
		t.Fatal("Overrode did not survive serialization: the disagreement was silently resolved")
	}
}

// A version 1 session predates every dimension. Every one of them must read as
// unobserved, and none may default to success.
func TestV1SessionReadsAsUnobserved(t *testing.T) {
	dir := t.TempDir()
	writeV1(t, dir, "t-v1", `{"version":1,"task_id":"t-v1","session_id":"s","phase":"reviewing","evidence":{"diff_bytes":10}}`)

	loaded, ok, err := Load(dir, "t-v1")
	if err != nil || !ok {
		t.Fatalf("a v1 session did not load: %v ok=%v", err, ok)
	}
	if loaded.Version != Version {
		t.Fatalf("v1 was not projected to %d: %d", Version, loaded.Version)
	}
	for _, d := range []Dimension{DimImplementation, DimReview, DimAudit, DimEvaluator, DimScope, DimAdmission} {
		if o, present := loaded.Current(candidateA())[d]; present && o.Value != unobserved {
			t.Fatalf("%s defaulted to %q on a session that never observed it", d, o.Value)
		}
	}
}

// Reading is not writing. Inspecting an old session must leave it exactly as it
// was found, or inspection becomes a mutation nobody authorized.
func TestLoadingV1WritesNothing(t *testing.T) {
	dir := t.TempDir()
	writeV1(t, dir, "t-v1", `{"version":1,"task_id":"t-v1","session_id":"s","phase":"reviewing","evidence":{"audit_verdict":"pass"}}`)
	file := filepath.Join(dir, ".sensei-code", "tasks", "t-v1.json")
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	beforeBody, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := Load(dir, "t-v1"); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	afterBody, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || string(afterBody) != string(beforeBody) {
		t.Fatal("loading a v1 session rewrote it: reading must not be a write")
	}
}

// A version this build does not know is not an old version. Guessing at it is
// how a future shape gets read as a present one, so it is refused.
func TestUnknownFutureVersionIsRefusedNotUpgraded(t *testing.T) {
	dir := t.TempDir()
	writeV1(t, dir, "t-v3", `{"version":3,"task_id":"t-v3","session_id":"s"}`)
	_, ok, err := Load(dir, "t-v3")
	if err == nil {
		t.Fatal("a future version was accepted")
	}
	if ok {
		t.Fatal("a future version reported a usable state")
	}
	if !strings.Contains(err.Error(), "version 3") {
		t.Fatalf("refusal does not name the version it refused: %v", err)
	}
}

// A legacy audit verdict is translated only where the mapping is exact, and the
// result is stamped as migrated so it is never mistaken for something this
// version observed. An unrecognized one keeps its original string.
func TestV1AuditVerdictProjectionCarriesItsProvenance(t *testing.T) {
	dir := t.TempDir()
	writeV1(t, dir, "t-map", `{"version":1,"task_id":"t-map","session_id":"s","evidence":{"audit_verdict":"cannot_verify"}}`)
	mapped, _, err := Load(dir, "t-map")
	if err != nil {
		t.Fatal(err)
	}
	if len(mapped.Observations) != 1 {
		t.Fatalf("expected one projected observation, got %+v", mapped.Observations)
	}
	if got := mapped.Observations[0]; got.Value != string(AuditCannotVerify) ||
		got.Producer != "migration:v1" || got.Source != "taskstate.v1 Evidence.AuditVerdict" {
		t.Fatalf("projection lost its provenance or its value: %+v", got)
	}

	writeV1(t, dir, "t-odd", `{"version":1,"task_id":"t-odd","session_id":"s","evidence":{"audit_verdict":"probably ok"}}`)
	odd, _, err := Load(dir, "t-odd")
	if err != nil {
		t.Fatal(err)
	}
	got := odd.Observations[0]
	if got.Value != unobserved {
		t.Fatalf("an unrecognized legacy verdict was translated to %q", got.Value)
	}
	if !strings.Contains(got.Detail, "probably ok") {
		t.Fatalf("the original legacy string was discarded: %q", got.Detail)
	}
}

func writeV1(t *testing.T, repoRoot, taskID, body string) {
	t.Helper()
	dir := filepath.Join(repoRoot, ".sensei-code", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, taskID+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Identity is the diff AND the base it was derived from. The same change
// applied to a different base is a different candidate, and observations about
// one must never be selected for the other.
func TestSameDiffOnADifferentBaseIsADifferentCandidate(t *testing.T) {
	rebased := CandidateIdentity{BaseSHA: "0000000rebase", DiffDigest: candidateA().DiffDigest}
	if candidateA().Equal(rebased) {
		t.Fatal("the same diff at a different base compared equal: the base is not part of the identity")
	}

	var s State
	s.Record(sourced(DimReview, string(ReviewAccept), candidateA(), time.Unix(10, 0)))
	if o, ok := s.Current(rebased)[DimReview]; ok {
		t.Fatalf("a verdict about another base was selected for this candidate: %+v", o)
	}
}

// The set of dimensions is closed. A name this build does not define is kept in
// the append-only record -- something was observed and dropping it would hide
// that -- but it must never appear in a governed projection, where a renderer
// or a resume path could read its presence as meaningful.
func TestUnknownDimensionIsRecordedButNeverProjected(t *testing.T) {
	var s State
	s.Record(Observation{
		Dimension: "correctness", Value: "CERTIFIED", Candidate: candidateA(),
		Producer: "reviewer:codex", Source: "event:review.completed/7",
	})

	if len(s.Observations) != 1 {
		t.Fatalf("the observation was dropped from the record: %+v", s.Observations)
	}
	kept := s.Observations[0]
	if kept.Value != unobserved {
		t.Fatalf("an undefined dimension carried a value: %q", kept.Value)
	}
	if !strings.Contains(kept.Detail, "correctness") || !strings.Contains(kept.Detail, "unrecognized dimension") {
		t.Fatalf("the original dimension name was not preserved: %q", kept.Detail)
	}
	if _, ok := s.Current(candidateA())["correctness"]; ok {
		t.Fatal("an undefined dimension became a current claim")
	}
	if len(s.Current(candidateA())) != 0 {
		t.Fatalf("an undefined dimension entered the current projection: %+v", s.Current(candidateA()))
	}
	if h := s.Historical(candidateB()); len(h) != 0 {
		t.Fatalf("an undefined dimension was classified as candidate history: %+v", h)
	}
}

// Overrode names one thing: a reviewer accepted while the audit refused, so
// admission could not be established. Anywhere else it would assert a
// disagreement the observation does not represent.
func TestOverrodeIsLegalOnlyOnAdmission(t *testing.T) {
	var s State
	scope := sourced(DimScope, string(ScopeCompliant), candidateA(), time.Unix(10, 0))
	scope.Overrode = true
	s.Record(scope)
	if s.Observations[0].Overrode {
		t.Fatal("a scope observation asserted a reviewer/audit disagreement")
	}
	if !strings.Contains(s.Observations[0].Detail, "overrode is meaningful only on admission") {
		t.Fatalf("dropping it was not explained: %q", s.Observations[0].Detail)
	}

	admission := sourced(DimAdmission, string(AdmissionDeferred), candidateA(), time.Unix(11, 0))
	admission.Overrode = true
	s.Record(admission)
	if !s.Observations[1].Overrode {
		t.Fatal("the disagreement was dropped from the observation that represents it")
	}
}
