//go:build acceptance

// Package acceptance drives one real governed run against live Sensei and live
// providers.
//
// Everything else in this repository is a unit or structural test: fast,
// hermetic, and blind to whether the pieces work together against the actual
// world. That blindness has already cost us once. The start gate looked correct
// under fixtures and would have refused every task in the product, because the
// real server answers an unscoped preflight with PREFLIGHT_STATUS_EMPTY and no
// fixture said so. This exists so that class of mistake is caught before a
// merge rather than after one.
//
// It is behind a build tag because it is slow, costs provider tokens, mutates a
// candidate worktree, and needs a working Sensei. Run it deliberately:
//
//	go test -tags acceptance ./internal/acceptance/ -run TestGovernedRun -v -timeout 20m
//
// It answers the human rendezvous itself, which is the one thing a canary has
// to do carefully: approve the plan, authorize at an architectural boundary,
// and decline the pull request, because publication is human-owned and a test
// that opened one would be doing the exact thing this product exists never to
// do on its own. See answer() for why authorizing is the right default here.
package acceptance

import (
	"context"
	"encoding/json"
	"github.com/globulario/sensei-code/internal/candidate"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// acceptanceTask is deliberately small and genuinely governed work. It has to
// require planning, a candidate, a real file mutation, an audit and a review,
// while keeping the failure surface narrow enough that a jam is diagnosable
// without also debugging a large feature.
const acceptanceTask = "Add a table-driven unit test proving that an unknown TUI slash command cannot start governed execution. Do not change product behaviour."

func TestGovernedRunEndToEnd(t *testing.T) {
	root := repoRoot(t)
	repo := gitx.Repo{Root: root}

	// The governed path refuses a dirty canonical checkout by design, so say so
	// plainly rather than failing later inside the engine with a subtler error.
	clean, err := repo.IsClean(context.Background())
	if err != nil {
		t.Fatalf("cannot read the working tree: %v", err)
	}
	if !clean {
		t.Skip("canonical checkout is dirty; commit or stash before running the governed canary")
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sessionID := session.ID(time.Now())
	store, err := session.New(root, sessionID)
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(256)
	defer unsubscribe()

	engine := workflow.New(repo, cfg, bus, store, sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()

	taskID := engine.SubmitGoverned(ctx, acceptanceTask)
	t.Logf("governed task: %s", taskID)

	seen := map[event.Kind]bool{}
	var modePayload struct {
		Mode       string `json:"mode"`
		Provenance string `json:"provenance"`
	}
	var failure, completion string
	var workers []string
	var auditVerdicts []string

	deadline := time.After(18 * time.Minute)
	for done := false; !done; {
		select {
		case <-deadline:
			t.Fatalf("governed run did not finish; phases reached: %v", kinds(seen))
		case ev := <-events:
			if ev.TaskID != taskID && ev.TaskID != "" {
				continue
			}
			seen[ev.Kind] = true
			t.Logf("[%-22s] %s", ev.Kind, oneLine(ev.Summary))

			switch ev.Kind {
			case event.ModeSelected:
				_ = json.Unmarshal(ev.Payload, &modePayload)
			case event.AgentStarted:
				workers = append(workers, oneLine(ev.Summary))
			case event.CandidateAudited:
				auditVerdicts = append(auditVerdicts, oneLine(ev.Summary))
			case event.AuthorityRequired:
				answer(t, engine, taskID, ev)
			case event.WorkflowFailed:
				failure = ev.Summary
				done = true
			case event.WorkflowCompleted:
				completion = ev.Summary
				done = true
			}
		}
	}

	// The chain, stage by stage. Each assertion names the slice it proves,
	// because a bare "run failed" tells whoever reads this nothing about where.
	if modePayload.Mode != "governed" {
		t.Errorf("P0.3: task ran in mode %q with provenance %q, want governed", modePayload.Mode, modePayload.Provenance)
	}
	if !seen[event.SenseiResult] {
		t.Error("P0.2: no Sensei evidence was read; the start gate did not consult the live graph")
	}
	if !seen[event.PlanProposed] {
		t.Error("P0.1: no plan was proposed; the architect never produced a bounded contract")
	}
	if failure != "" {
		t.Fatalf("governed run failed: %s\nphases reached: %v", failure, kinds(seen))
	}
	if !seen[event.CandidateChanged] {
		t.Error("P0.5: no candidate diff was produced; the worker changed nothing")
	}
	if !seen[event.CandidateAudited] {
		t.Error("P0.2: the candidate was never audited by Sensei")
	}
	if len(workers) == 0 {
		t.Error("no agent ever started")
	}
	if completion == "" {
		t.Error("the run ended without a completion summary")
	}

	t.Logf("agents: %s", strings.Join(workers, " | "))
	t.Logf("audits: %s", strings.Join(auditVerdicts, " | "))
	t.Logf("completed: %s", completion)

	// The candidate must be cut from the recorded base, not merely accompanied
	// by a recorded base. Those are different claims, and only the first means
	// the reviewed change is relative to the state that was certified.
	assertCutFromRecordedBase(t, root, repo.WorktreePath(taskID), taskID)

	// The candidate is left on disk deliberately: it is the artifact a human
	// inspects, and deleting it would make a passing canary unreviewable.
	t.Logf("candidate worktree retained for inspection: %s", repo.WorktreePath(taskID))
}

// answer resolves a human rendezvous the way a careful operator would.
//
// The plan is approved because that is the transition under test. The pull
// request is declined because publication is human-owned; an acceptance test
// that opened one would be performing the single action this whole product is
// built never to take by itself.
//
// A Level-3 authority question authorizes. That is a deliberate choice and it
// is worth being explicit about why, because the safe-looking answer is the
// wrong one here. The default option set leads with "preserve current intent
// and require another design", and answering that sends the architect back to
// replan -- which proves the escalation fired and nothing after it. A canary
// that stops at the first refusal never reaches the candidate, the worker, the
// audit, the reviewer or the handoff, so it cannot tell us those work.
//
// This is a property of the test, not of the product. Authorizing here is safe
// precisely because of what has already been proved by reaching this point: the
// worker is confined to a candidate worktree cut from an exact base, push and
// force-push are refused by git itself, and nothing merges without a human. The
// blast radius of a wrong answer is a worktree we then read.
func answer(t *testing.T, engine *workflow.Engine, taskID string, ev event.Event) {
	t.Helper()
	var decision authority.Decision
	if err := json.Unmarshal(ev.Payload, &decision); err != nil {
		t.Errorf("could not read the authority decision: %v", err)
		return
	}
	subject := strings.ToLower(decision.Subject)

	choice := "1"
	switch {
	case strings.Contains(subject, "pull request"):
		choice = declineOption(decision)
		t.Logf("declining the pull request rendezvous (option %s): publication is human-owned", choice)
	case strings.Contains(subject, "implement this plan"):
		t.Logf("approving the plan (option 1)")
	default:
		choice = authorizeOption(decision)
		t.Logf("LEVEL-3 authority question: %s", oneLine(decision.Subject))
		t.Logf("  reason: %s", oneLine(decision.Reason))
		for _, o := range decision.Options {
			t.Logf("    %s) %s", o.ID, oneLine(o.Label))
		}
		t.Logf("  authorizing with option %s", choice)
	}

	// The engine registers its pending channel before emitting, but a retry
	// keeps a slow rendezvous from turning into a spurious timeout.
	for attempt := 0; attempt < 20; attempt++ {
		if engine.ResolveHuman(taskID, choice) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("could not deliver the human answer %q for %q", choice, decision.Subject)
}

// authorizeOption picks the option that lets the work proceed.
//
// It prefers an explicitly authorizing label, then the architect's own
// recommendation, then the first option that is neither a refusal nor a stop.
// Options are matched by meaning rather than by number because the numbers are
// assigned by the engine in whatever order the architect supplied them, and a
// canary that hard-coded "2" would silently start answering something else the
// first time an architect returned its options the other way round.
func authorizeOption(d authority.Decision) string {
	for _, o := range d.Options {
		if strings.Contains(strings.ToLower(o.Label), "authoriz") {
			return o.ID
		}
	}
	if r := strings.TrimSpace(d.Recommendation); r != "" {
		for _, o := range d.Options {
			if o.ID == r && !refuses(o.Label) {
				return r
			}
		}
	}
	for _, o := range d.Options {
		if !refuses(o.Label) {
			return o.ID
		}
	}
	return "1"
}

// refuses reports whether an option label stops or reverses the work.
func refuses(label string) bool {
	label = strings.ToLower(label)
	for _, marker := range []string{"stop", "preserve current", "require another", "not yet", "do not", "abandon", "cancel"} {
		if strings.Contains(label, marker) {
			return true
		}
	}
	return false
}

// declineOption finds the option that does not open a pull request, rather than
// assuming it is number two.
func declineOption(d authority.Decision) string {
	for _, o := range d.Options {
		label := strings.ToLower(o.Label)
		if strings.Contains(label, "not") || strings.Contains(label, "no") || strings.Contains(label, "skip") {
			return o.ID
		}
	}
	if len(d.Options) > 1 {
		return d.Options[len(d.Options)-1].ID
	}
	return "2"
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/acceptance -> repository root
	return strings.TrimSuffix(wd, "/internal/acceptance")
}

func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " · "))
	// Deliberately generous. The first run truncated a diff audit at 160
	// characters and cut off the limitations, which were the only part that
	// said why the audit could not be performed -- so the log recorded that
	// something went wrong and hid what.
	if len(s) > 1200 {
		return s[:1200] + "…"
	}
	return s
}

func kinds(seen map[event.Kind]bool) []string {
	var out []string
	for k := range seen {
		out = append(out, string(k))
	}
	return out
}

// assertCutFromRecordedBase proves the worktree actually derives from the
// commit the identity records, and that the workflow's own governance writes
// did not leak into the change under review.
//
// Recording a base and cutting from it are separate facts. A run could pin one
// commit and create the worktree from another — from HEAD, say — and every
// receipt would still name the pinned one. The only way to know is to ask git
// what the worktree descends from.
func assertCutFromRecordedBase(t *testing.T, root, worktree, taskID string) {
	t.Helper()
	id, ok, err := candidate.Load(root, taskID)
	if err != nil || !ok {
		t.Fatalf("no candidate identity recorded for %s: ok=%v err=%v", taskID, ok, err)
	}
	if strings.TrimSpace(id.BaseSHA) == "" {
		t.Fatal("candidate identity records no base commit")
	}

	head, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read candidate HEAD: %v", err)
	}
	tip := strings.TrimSpace(string(head))

	// Either the worker committed nothing, in which case the worktree still
	// sits exactly on the base, or it committed and the base must be an
	// ancestor. Anything else means the candidate was cut from somewhere else.
	if tip != id.BaseSHA {
		if err := exec.Command("git", "-C", worktree, "merge-base", "--is-ancestor", id.BaseSHA, tip).Run(); err != nil {
			t.Fatalf("candidate was not cut from the recorded base: recorded %s, worktree tip %s", id.BaseSHA, tip)
		}
	}
	t.Logf("candidate cut from recorded base %s (worktree tip %s)", short(id.BaseSHA), short(tip))

	// The resolution this run wrote into the awareness corpus is a governance
	// side effect, not part of the change being reviewed. If it appears in the
	// candidate diff, the run is proposing its own paperwork as work.
	diff, err := exec.Command("git", "-C", worktree, "diff", id.BaseSHA).Output()
	if err != nil {
		t.Fatalf("read candidate diff: %v", err)
	}
	if strings.Contains(string(diff), "candidates/proposals/") {
		t.Error("the candidate diff contains this run's own governance proposal; a decision record is a side effect, not part of the change")
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
