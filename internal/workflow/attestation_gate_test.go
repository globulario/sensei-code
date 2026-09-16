package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

// relayedAcceptRunner answers as the GitHub bridge does for a RELAYED review:
// an accepting verdict, a session nobody could verify, and the digest of the
// exact artifact the transport carried.
type relayedAcceptRunner struct {
	digest string
	seen   *[]roles.Binding
}

func (r relayedAcceptRunner) Run(_ context.Context, req agent.Request, _ func(event.Event)) (agent.Result, error) {
	if r.seen != nil {
		*r.seen = append(*r.seen, req.Binding)
	}
	verdict, _ := json.Marshal(map[string]any{"decision": "accept", "summary": "the candidate stands"})
	return agent.Result{Text: string(verdict), Session: roles.Unverified, ReviewDigest: r.digest}, nil
}

// recordedOverride is the store answering for the candidate it is asked about,
// which is what the real one does: AttestationFor matches on the binding, and
// the digest it carries is whatever the operator attested to.
//
// The digest is therefore the variable here. Whether the override covers the
// review actually consumed is the ENGINE's check, and giving the store a digest
// of its own is how that check gets something real to refuse.
type recordedOverride struct {
	digest string
	found  bool
}

func (o recordedOverride) AttestationFor(b roles.Binding, _ string) (roles.Attestation, bool, error) {
	if !o.found {
		return roles.Attestation{}, false, nil
	}
	return roles.Attestation{
		RequestID: "r-0123456789abcdef", ReviewDigest: o.digest, Reviewer: "chatgpt",
		Decision: roles.Accept, Binding: b, Principal: "uid:1000,user:dave,pid:4242,terminal:34816",
		Statement: roles.AttestationStatement,
	}, true, nil
}

const relayedDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// The whole point of the relay, end to end, on a task whose measured risk
// REQUIRES an independent review.
//
// A relayed ACCEPT is advisory however it travelled, so on its own the candidate
// waits: that is the gate working. What may change the transition is a human
// deciding to advance it anyway -- and only when this workspace granted that
// authority, and only for the exact review and candidate the override names.
func TestARelayedAcceptAdvancesOnlyOnACoveringHumanOverride(t *testing.T) {
	for _, c := range []struct {
		name   string
		grant  bool
		digest string
		found  bool
		want   candidateOutcome
	}{
		{"no override recorded", true, relayedDigest, false, candidateAwaitingIndependentReview},
		{"an override of a different review", true, "sha256:" + strings.Repeat("0", 64), true,
			candidateAwaitingIndependentReview},
		{"a covering override, authority not granted here", false, relayedDigest, true,
			candidateAwaitingIndependentReview},
		{"a covering override", true, relayedDigest, true, candidateAccepted},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newGateHarness(t, requiresIndependentReview(), roles.Unverified, "accept")
			h.engine.Runners = roleResolver{reviewer: relayedAcceptRunner{digest: relayedDigest},
				name: "chatgpt", session: "session-1"}
			h.engine.Config.Workflow.OwnerAttestation = c.grant
			h.engine.Attestations = recordedOverride{digest: c.digest, found: c.found}

			outcome := h.run(t)
			if outcome != c.want {
				t.Fatalf("the loop concluded %q, want %q", outcome, c.want)
			}

			events := drainEvents(h.events)
			overrode := false
			for _, e := range events {
				var p struct {
					Kind string `json:"review_kind"`
				}
				if len(e.Payload) != 0 && json.Unmarshal(e.Payload, &p) == nil && p.Kind == "human_override" {
					overrode = true
				}
			}
			if overrode != (c.want == candidateAccepted) {
				t.Fatalf("a human override was recorded=%v for outcome %q", overrode, outcome)
			}
			if c.want != candidateAccepted {
				return
			}
			// Advanced, and the record says WHY: a human decided, and the
			// obligation it overrode is still unmet and still listed.
			obligations := h.engine.AdvisoryObligations("task-1")
			if len(obligations) == 0 || !strings.Contains(strings.Join(obligations, "\n"), "overridden by a human") {
				t.Fatalf("the overridden obligation left the record: %v", obligations)
			}
			if !strings.Contains(strings.Join(obligations, "\n"), "remains unmet") {
				t.Fatalf("the obligation is not recorded as unmet: %v", obligations)
			}
		})
	}
}

// An override changes what may PROCEED. It never becomes the independent review
// nobody had, and it never raises the session mode.
func TestAnAttestedResultStillSatisfiesNoAdversarialObligation(t *testing.T) {
	binding := roles.Binding{TaskID: "task-1", BaseSHA: "b", CandidateDigest: "d", CandidateTree: "t"}
	advisory := roles.NewAdvisory(roles.ReviewVerdict{
		Provenance: roles.Provenance{TaskID: "task-1", Role: roles.Reviewer, Provider: "chatgpt",
			SessionMode: roles.Unverified, BaseSHA: "b", CandidateDigest: "d", CandidateTree: "t"},
		Decision: roles.Accept, Summary: "the candidate stands",
	})
	att := roles.Attestation{RequestID: "r-1", ReviewDigest: relayedDigest, Reviewer: "chatgpt",
		Decision: roles.Accept, Binding: binding, Principal: "uid:1000,pid:1,terminal:2",
		Statement: roles.AttestationStatement}

	result, err := attestedReview(advisory, att, relayedDigest)
	if err != nil {
		t.Fatalf("a covering override was refused: %v", err)
	}
	if result.SatisfiesAdversarialObligation() {
		t.Fatal("a human override claimed to satisfy the adversarial-review obligation")
	}
	if !result.Advisory() {
		t.Fatal("an attested result stopped reporting that its review was advisory")
	}
	if !result.Unlocks(requiresIndependentReview()) {
		t.Fatal("a covering override did not unlock the transition it exists to unlock")
	}
	if result.Verdict().Provenance.SessionMode != roles.Unverified {
		t.Fatalf("the override raised the session mode to %q", result.Verdict().Provenance.SessionMode)
	}
	if got, ok := result.Attestation(); !ok || got.Principal != att.Principal {
		t.Fatalf("the result does not carry who overrode: %+v %v", got, ok)
	}
	if !strings.Contains(result.Describe(), "human override") {
		t.Fatalf("the record does not name the override: %s", result.Describe())
	}

	// The same override does not cover a different review or a moved candidate.
	if _, err := attestedReview(advisory, att, "sha256:"+strings.Repeat("9", 64)); err == nil {
		t.Fatal("an override covered a review it does not name")
	}
	moved := advisory
	moved.Provenance.CandidateTree = "moved"
	if _, err := attestedReview(moved, att, relayedDigest); err == nil {
		t.Fatal("an override covered a candidate that moved")
	}
}
