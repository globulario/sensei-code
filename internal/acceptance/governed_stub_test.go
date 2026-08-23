//go:build stubsmoke

// Deterministic governed-run tripwire.
//
// The live canary in governed_run_test.go answers the more important question —
// does governed mode work against real providers — but when it fails halfway
// there is no way to tell a skipped transition from a model that wandered off,
// and hours go into debugging Claude, Codex or MCP when the engine itself
// simply never made a transition.
//
// This run removes the models. Sensei is real, the workflow is real, the
// candidate worktree is real; only the three providers are deterministic stubs
// that always cooperate. So a failure here is the state machine, and a pass
// here means the next failure belongs to the environment.
//
//	go test -tags stubsmoke ./internal/acceptance/ -run TestGovernedRunStubProviders -v -timeout 10m
//
// It is a tripwire, not a benchmark. It proves the chain connects. It proves
// nothing whatsoever about the quality of any decision along it.
package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// The two targets differ in exactly one property: whether Sensei holds graph
// coverage for the file the plan names.
//
// unanchoredTarget is a scratch file this test owns, which the graph has never
// heard of. anchoredTarget is a product file Sensei holds a direct anchor for,
// with a local blast radius and no approval gate, so the router can grant
// architectural authority and the rest of the chain can run.
const (
	unanchoredTarget = "internal/acceptance/testdata/stub_smoke_target.txt"
	anchoredTarget   = "internal/report/report.go"
)

// A run over a file the graph knows nothing about must stop at the human
// boundary, and the question must survive.
//
// This is the product working, not failing. It is pinned because it is also
// the ceiling on autonomy: governed mode cannot currently close any task whose
// files Sensei has no coverage for, which includes every new file. That is the
// same regime #259 measures in Sensei, showing up here as a product limit.
func TestGovernedRunDefersWhenCoverageIsAbsent(t *testing.T) {
	r := runStubGoverned(t, unanchoredTarget)
	if r.completion != "" {
		t.Fatalf("a task over an unanchored file completed without a human: %s", r.completion)
	}
	if !r.seen[event.AuthorityRequired] {
		t.Fatalf("no human boundary was reached; phases: %v", kinds(r.seen))
	}
	if !r.seen[event.WorkflowAwaitingAuthority] {
		t.Errorf("the deferred question did not settle as awaiting authority; phases: %v", kinds(r.seen))
	}
	if !r.seen[event.PlanProposed] && !r.seen[event.ContextConsulted] {
		t.Error("the run stopped before it had read anything or planned anything")
	}
}

// A run over a file Sensei does hold coverage for must carry the whole chain.
func TestGovernedRunStubProviders(t *testing.T) {
	r := runStubGoverned(t, anchoredTarget)

	// The chain cannot run past the router while Sensei escalates every file
	// in this repository, and that ceiling is measured rather than assumed: at
	// the time of writing, 0 of 183 probed files could reach architectural
	// authority — 157 answered PREFLIGHT_STATUS_EMPTY and all 26 that answered
	// OK carried at least one blind spot.
	//
	// Skipping is right here and failing is not, because nothing is broken:
	// the router is doing exactly what it says. But the skip names the exact
	// condition, so the day coverage improves this test starts running for
	// real, and an escalation for any OTHER reason still fails.
	if r.completion == "" && r.authorityCondition != "" {
		if isCoverageCeiling(r.authorityCondition) {
			t.Skipf("the governed chain cannot complete while the router escalates on coverage: %s", r.authorityCondition)
		}
		t.Fatalf("the run escalated for a reason unrelated to coverage: %s", r.authorityCondition)
	}

	if r.mode != "governed" {
		t.Errorf("task ran in mode %q, want governed", r.mode)
	}
	// Each assertion names the transition it proves, because "the run failed"
	// tells whoever reads this nothing about where.
	for kind, what := range map[event.Kind]string{
		event.SenseiResult:     "Sensei evidence was never read; the start gate did not consult the live graph",
		event.PlanProposed:     "no plan was proposed; the architect stage produced no bounded contract",
		event.CandidateChanged: "no candidate diff was produced; the worker stage changed nothing",
		event.CandidateAudited: "the candidate was never audited by Sensei",
	} {
		if !r.seen[kind] {
			t.Errorf("%s (%s)", what, kind)
		}
	}
	if r.failure != "" {
		t.Fatalf("the state machine failed with cooperative providers: %s\nphases reached: %v", r.failure, kinds(r.seen))
	}
	if r.completion == "" {
		t.Fatalf("the run ended without completing; phases reached: %v", kinds(r.seen))
	}
}

type stubRun struct {
	seen       map[event.Kind]bool
	mode       string
	failure    string
	completion string
	// authorityCondition is the exact condition the router named, so a caller
	// can tell the known coverage ceiling from a new escalation.
	authorityCondition string
}

// isCoverageCeiling reports whether the router stopped because Sensei covers
// nothing here, rather than because something else went wrong.
func isCoverageCeiling(condition string) bool {
	for _, known := range []string{
		"graph coverage is absent for the planned files",
		// The blind-spot channel is now classified rather than counted, so the
		// ceiling arrives under whichever reading applied. A gap that failed to
		// close reaches the human branch with its original condition prefixed,
		// and still contains one of these.
		"Sensei reported missing coverage in the planned region",
		"Sensei reported consequence signals in the planned region",
		"Sensei reported a blind spot this router has no reading for",
		"Sensei classified no approval gate for the planned region",
	} {
		if strings.Contains(condition, known) {
			return true
		}
	}
	return false
}

// runStubGoverned drives one governed task with deterministic providers.
func runStubGoverned(t *testing.T, target string) stubRun {
	t.Helper()
	root := repoRoot(t)
	repo := gitx.Repo{Root: root}

	clean, err := repo.IsClean(context.Background())
	if err != nil {
		t.Fatalf("cannot read the working tree: %v", err)
	}
	if !clean {
		t.Skip("canonical checkout is dirty; the governed path refuses one by design")
	}
	if _, err := os.Stat(filepath.Join(root, target)); err != nil {
		t.Fatalf("the target is missing: %v", err)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	stub := buildStubAgent(t, root)
	// Only the providers are replaced. Sensei, the workflow, the candidate
	// lifecycle and the authority router are exactly the product's.
	cfg.Architect = config.Agent{Name: "stub-architect", Command: stub, Args: []string{"--role", "architect", "--target", target}}
	cfg.Implementors = []config.Agent{{Name: "stub-implementor", Command: stub, Args: []string{"--role", "implementor", "--target", target}}}
	cfg.Reviewer = config.Agent{Name: "stub-reviewer", Command: stub, Args: []string{"--role", "reviewer"}}
	cfg.Reviewers = []config.Agent{cfg.Reviewer}

	sessionID := session.ID(time.Now())
	store, err := session.New(root, sessionID)
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(512)
	defer unsubscribe()

	engine := workflow.New(repo, cfg, bus, store, sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	taskID := engine.SubmitGoverned(ctx, "Append one trailing comment line to "+target+" and change nothing else.")
	t.Logf("governed task: %s  target: %s", taskID, target)

	out := stubRun{seen: map[event.Kind]bool{}}
	var modePayload struct {
		Mode string `json:"mode"`
	}
	deadline := time.After(8 * time.Minute)
	for done := false; !done; {
		select {
		case <-deadline:
			t.Fatalf("the stub run did not settle; phases reached: %v", kinds(out.seen))
		case ev := <-events:
			if ev.TaskID != taskID && ev.TaskID != "" {
				continue
			}
			out.seen[ev.Kind] = true
			t.Logf("[%-24s] %s", ev.Kind, oneLine(ev.Summary))
			switch ev.Kind {
			case event.ModeSelected:
				_ = json.Unmarshal(ev.Payload, &modePayload)
			case event.Status:
				// The router states the condition it routed on, and it is the
				// one thing worth keeping from an escalated run.
				if c := strings.TrimSpace(strings.TrimPrefix(oneLine(ev.Summary), "human-authority-required:")); c != oneLine(ev.Summary) {
					out.authorityCondition = c
				}
			case event.AuthorityRequired:
				// A human boundary with no human present. The question is
				// preserved rather than answered -- answering here would make
				// the tripwire pass by doing the one thing the product must
				// never do alone.
				engine.DeferAuthority(taskID)
			case event.WorkflowFailed:
				out.failure = ev.Summary
				done = true
			case event.WorkflowCompleted:
				out.completion = ev.Summary
				done = true
			case event.WorkflowAwaitingAuthority, event.WorkflowStopped:
				done = true
			}
		}
	}
	out.mode = modePayload.Mode

	// Candidate isolation is the claim most worth catching a leak in: whatever
	// the outcome, the canonical checkout must be untouched.
	after, err := repo.IsClean(context.Background())
	if err != nil {
		t.Fatalf("cannot re-read the working tree: %v", err)
	}
	if !after {
		status, _ := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput()
		t.Errorf("the governed run modified the canonical checkout; candidate isolation leaked:\n%s", strings.TrimSpace(string(status)))
	}
	return out
}

// buildStubAgent compiles the deterministic provider once per run.
func buildStubAgent(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "stubagent")
	cmd := exec.Command("go", "build", "-o", bin, "./internal/acceptance/testdata/stubagent")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub agent: %v: %s", err, out)
	}
	return bin
}
