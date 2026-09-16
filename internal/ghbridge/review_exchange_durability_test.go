package ghbridge

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/roles"
)

// A review request is the durable form of a review OWED on an exact candidate.
// Startup reconciliation used to withdraw it like any abandoned turn, so a
// restart destroyed the only record that a validated candidate was waiting for
// review. Turns are still withdrawn; review obligations are kept.
func TestStartupKeepsAReviewObligationAndWithdrawsAbandonedTurns(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath)
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}

	at := time.Now().Add(-2 * time.Hour).UTC()
	review := ExchangeRecord{
		TaskID: "task-1", RequestID: "r-review", RequestComment: 11, Conversation: "156", PublishedAt: at,
		Kind: ExchangeReview, BaseSHA: "91b475a172bba0257fd2ffd8a55d3edce582e883",
		CandidateDigest: digestC1, CandidateTree: "1b713c41d4d6ed058313ce940b0cc481e4b22b18",
		ReviewCommit: "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
	}
	turn := ExchangeRecord{TaskID: "task-1", RequestID: "r-turn", RequestComment: 12, Conversation: "156",
		PublishedAt: at.Add(time.Minute), Kind: ExchangeArchitecture}
	// A record written before Kind existed came from the architecture path and
	// keeps that treatment.
	legacy := ExchangeRecord{TaskID: "task-2", RequestID: "r-legacy", RequestComment: 13, Conversation: "156",
		PublishedAt: at.Add(2 * time.Minute)}
	for _, rec := range []ExchangeRecord{review, turn, legacy} {
		if err := log.Open(rec); err != nil {
			t.Fatal(err)
		}
	}

	n, err := ReconcileAbandonedExchanges(context.Background(), log, box, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("withdrew %d exchanges, want the 2 abandoned turns", n)
	}
	for _, id := range withdrawalsIn(m) {
		if id == "r-review" {
			t.Fatal("startup withdrew a review request: the waiting candidate's obligation was destroyed")
		}
	}

	owed, err := log.PendingReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(owed) != 1 || owed[0].RequestID != "r-review" {
		t.Fatalf("the review obligation did not survive restart: %+v", owed)
	}
	if owed[0].Subject() != review.Subject() {
		t.Fatalf("the kept obligation lost its candidate identity: %+v", owed[0].Subject())
	}
	if pending, _ := log.Pending(); len(pending) != 1 {
		t.Fatalf("an abandoned turn survived reconciliation: %+v", pending)
	}
}

func reviewRunnerWithLog(t *testing.T, wait time.Duration) (*prMailbox, *Runner, roles.Binding, ExchangeLog) {
	t.Helper()
	dir, base, tree1, _ := tempRepo(t)
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	bare := t.TempDir()
	for _, args := range [][]string{
		{"init", "--bare", "-q", bare},
		{"-C", dir, "remote", "add", "origin", bare},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}
	runner := &Runner{
		Issue: box, RepoDir: dir, Remote: "origin", NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: wait, Exchanges: log,
	}
	return m, runner, roles.Binding{TaskID: "T", BaseSHA: base, CandidateTree: tree1, CandidateDigest: digestC1}, log
}

// An unanswered review keeps its record, and the record names exactly the
// candidate the request carried.
func TestAnUnansweredReviewKeepsItsRecordWithTheCandidateIdentity(t *testing.T) {
	_, runner, binding, log := reviewRunnerWithLog(t, 120*time.Millisecond)
	_, err := runner.Run(context.Background(), agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}, nil)
	var owed *roles.ReviewUnanswered
	if !errors.As(err, &owed) {
		t.Fatalf("the turn did not end as an owed review, so the kept record proves nothing: %v", err)
	}

	pending, err := log.PendingReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("an unanswered review left %d review records, want 1", len(pending))
	}
	rec := pending[0]
	if rec.RequestID != owed.RequestID {
		t.Errorf("record request %q is not the owed request %q", rec.RequestID, owed.RequestID)
	}
	want := Subject{TaskID: "T", BaseSHA: binding.BaseSHA, CandidateDigest: binding.CandidateDigest,
		CandidateTree: binding.CandidateTree, ReviewCommit: owed.ReviewCommit}
	if rec.Subject() != want {
		t.Errorf("record subject %+v is not the request's %+v", rec.Subject(), want)
	}
}

// The negative control: an ANSWERED review is no longer owed. Its record closes
// and nothing is withdrawn -- a withdrawal after a valid answer would tell a
// reader the exchange failed when it succeeded.
func TestAnAnsweredReviewClosesItsRecordWithoutWithdrawing(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 5*time.Second)
	m.onPost = func(body string) []map[string]any {
		req, ok := ParseRequest(body)
		if !ok {
			return nil
		}
		answer, err := Review{Subject: req.Subject, RequestID: req.RequestID}.Marker()
		if err != nil {
			return nil
		}
		return []map[string]any{{
			"body": answer + "\n" + `{"decision":"accept","summary":"the candidate stands","instructions":"","findings":[]}`,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		}}
	}

	res, err := runner.Run(context.Background(), agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}, nil)
	if err != nil {
		t.Fatalf("an answered review failed: %v", err)
	}
	if res.Session != roles.Unverified {
		t.Errorf("an answer over the transport claimed session %q", res.Session)
	}
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("an answered review kept its record open: %+v", pending)
	}
	for _, c := range m.comments {
		if body, _ := c["body"].(string); len(body) > 0 {
			if _, werr := ParseWithdrawal(body); werr == nil {
				t.Fatalf("an answered review posted a withdrawal: %q", body)
			}
		}
	}
}
