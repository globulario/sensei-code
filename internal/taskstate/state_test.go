package taskstate

import (
	"os"
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
