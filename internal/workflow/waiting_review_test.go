package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
)

// recordingUnansweredRunner is an unanswered review transport that remembers the
// binding it was asked about, so a test can compare two requests' subjects.
type recordingUnansweredRunner struct {
	seen *[]roles.Binding
}

func (r recordingUnansweredRunner) Run(ctx context.Context, req agent.Request, emit func(event.Event)) (agent.Result, error) {
	*r.seen = append(*r.seen, req.Binding)
	return unansweredRunner{calls: new(atomic.Int32)}.Run(ctx, req, emit)
}

// Only a complete unanswered record yields an identity. Anything less claims
// nothing, rather than an identity a continuation would then trust.
func TestOnlyACompleteUnansweredRecordNamesTheCandidateItWasAbout(t *testing.T) {
	complete := map[string]any{"review_kind": "unanswered", "request_id": "r-1", "base": "b", "candidate_digest": "d", "candidate_tree": "t"}
	raw := func(m map[string]any) json.RawMessage { b, _ := json.Marshal(m); return b }
	without := func(key string) map[string]any {
		out := map[string]any{}
		for k, v := range complete {
			if k != key {
				out[k] = v
			}
		}
		return out
	}

	if w := waitingReviewFrom(session.Interrupted{AwaitingReview: true, AwaitingReviewRecord: raw(complete)}); w == nil || w.RequestID != "r-1" || w.CandidateTree != "t" {
		t.Fatalf("a complete unanswered record yielded %+v", w)
	}
	advisory := without("review_kind")
	advisory["review_kind"] = "advisory"
	for name, task := range map[string]session.Interrupted{
		"not awaiting":  {AwaitingReview: false, AwaitingReviewRecord: raw(complete)},
		"no record":     {AwaitingReview: true},
		"unreadable":    {AwaitingReview: true, AwaitingReviewRecord: json.RawMessage(`{`)},
		"advisory":      {AwaitingReview: true, AwaitingReviewRecord: raw(advisory)},
		"no request id": {AwaitingReview: true, AwaitingReviewRecord: raw(without("request_id"))},
		"no base":       {AwaitingReview: true, AwaitingReviewRecord: raw(without("base"))},
		"no digest":     {AwaitingReview: true, AwaitingReviewRecord: raw(without("candidate_digest"))},
		"no tree":       {AwaitingReview: true, AwaitingReviewRecord: raw(without("candidate_tree"))},
	} {
		if w := waitingReviewFrom(task); w != nil {
			t.Fatalf("%s: claimed identity %+v", name, w)
		}
	}
}

// A resumed WAITING_REVIEW candidate is reviewed again without a worker, under a
// new binding captured now, and the engine states whether that candidate is the
// one the unanswered request was about. A candidate that moved is named as
// needing a fresh review; it never inherits the old request.
func TestAResumedWaitingReviewNamesWhetherItsCandidateIsTheOneItsRequestWasAbout(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*waitingReview)
		same   bool
	}{
		{"same candidate", func(*waitingReview) {}, true},
		{"changed candidate", func(w *waitingReview) { w.CandidateDigest = "sha256:" + strings.Repeat("0", 64) }, false},
		{"changed tree", func(w *waitingReview) { w.CandidateTree = strings.Repeat("0", 40) }, false},
		{"changed base", func(w *waitingReview) { w.BaseSHA = strings.Repeat("0", 40) }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Unverified, "accept")
			var seen []roles.Binding
			h.engine.Runners = roleResolver{reviewer: recordingUnansweredRunner{seen: &seen}, name: "chatgpt", session: "session-1"}
			plan := "Rewrite main.go so it prints a number."

			h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc, plan, "", func(error) {})
			if !contains(drainEvents(h.events), event.WorkflowAwaitingReview) || len(seen) != 1 || seen[0].CandidateDigest == "" {
				t.Fatalf("the first run did not leave a bound candidate waiting for review: %+v", seen)
			}

			recorded := &waitingReview{RequestID: "r-old", BaseSHA: seen[0].BaseSHA,
				CandidateDigest: seen[0].CandidateDigest, CandidateTree: seen[0].CandidateTree, ReviewKind: reviewKindUnanswered}
			c.mutate(recorded)
			h.tc.AwaitingReview = true
			h.tc.WaitingReview = recorded

			var failed error
			h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc, plan, "", func(err error) { failed = err })
			if failed != nil {
				t.Fatalf("the resumed review failed the run: %v", failed)
			}
			if len(seen) != 2 {
				t.Fatalf("the resumed candidate was reviewed %d more times, want exactly once", len(seen)-1)
			}
			if seen[1] != seen[0] {
				t.Fatalf("resuming without a worker changed the candidate: %+v -> %+v", seen[0], seen[1])
			}
			if h.tc.WaitingReview != nil {
				t.Fatalf("the recorded obligation was not consumed by the first resumed cycle")
			}

			var statement *event.Event
			for _, e := range drainEvents(h.events) {
				var p struct {
					// The projection names the request it COMPARED against.
					// It is not called "superseded" any more: a byte-identical
					// candidate is reattached to, not superseded (#182 R4).
					Recorded string `json:"recorded_request"`
				}
				if e.Kind == event.Status && len(e.Payload) != 0 && json.Unmarshal(e.Payload, &p) == nil && p.Recorded == "r-old" {
					e := e
					statement = &e
				}
			}
			if statement == nil {
				t.Fatalf("no statement compared the resumed candidate against request r-old")
			}
			var p struct {
				Same bool `json:"same_candidate"`
			}
			if err := json.Unmarshal(statement.Payload, &p); err != nil || p.Same != c.same {
				t.Fatalf("same_candidate = %v (err %v), want %v: %s", p.Same, err, c.same, statement.Summary)
			}
			if !c.same && !strings.Contains(statement.Summary, "fresh review") {
				t.Fatalf("a changed candidate was not named as needing a fresh review: %s", statement.Summary)
			}
			// A byte-identical candidate must NOT be described as producing a
			// new request; that wording was the pre-R4 behaviour.
			if c.same {
				for _, banned := range []string{"new request id", "superseded"} {
					if strings.Contains(statement.Summary, banned) {
						t.Fatalf("an unchanged candidate is still described with %q: %s", banned, statement.Summary)
					}
				}
			}
		})
	}
}
