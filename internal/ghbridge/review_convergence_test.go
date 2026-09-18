package ghbridge

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
)

const acceptJSON = `{"decision":"accept","summary":"the candidate stands","instructions":"","findings":[]}`

// nextAnswerID numbers fixture answers the way GitHub numbers comments.
var nextAnswerID int64 = 9000

// answerWith makes the mailbox reply to the published request with one comment.
func answerWith(m *prMailbox, body func(Request) string, login string, id float64) {
	m.onPost = func(posted string) []map[string]any {
		req, ok := ParseRequest(posted)
		if !ok {
			return nil
		}
		out := body(req)
		if out == "" {
			return nil
		}
		// Real GitHub numbers every comment, and the comment id is the transport
		// evidence a stored review is later checked against. A fixture without
		// one would let this path pass while recording evidence nobody can
		// re-verify.
		nextAnswerID++
		return []map[string]any{{
			"id":   float64(nextAnswerID),
			"body": out,
			"user": map[string]any{"login": login, "id": id},
		}}
	}
}

func reviewTurn(binding roles.Binding) agent.Request {
	return agent.Request{Role: roles.Reviewer, TaskID: binding.TaskID, Binding: binding}
}

// A review answered on the authenticated mailbox becomes the same durable
// record a relayed review does: the reviewer's exact bytes, their digest, and
// GitHub's transport facts kept separately beside them.
func TestADirectMailboxReviewBecomesTheCommonRecord(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 5*time.Second)
	var posted string
	answerWith(m, func(req Request) string {
		posted = canonicalAnswer(t, req.Subject, req.RequestID, req.ReviewerProvider, acceptJSON)
		return posted
	}, "davecourtois", 1697116)

	res, err := runner.Run(context.Background(), reviewTurn(binding), nil)
	if err != nil {
		t.Fatalf("an answered review failed: %v", err)
	}
	if res.Session != roles.Unverified {
		t.Fatalf("a mailbox answer claimed session %q", res.Session)
	}
	// The digest names the EXACT comment bytes, not a re-rendering of them.
	if want := reviewartifact.Digest(posted); res.ReviewDigest != want {
		t.Fatalf("ReviewDigest is %q, want the digest of the exact comment bytes %s", res.ReviewDigest, want)
	}

	req := requestFor(t, log, m)
	rec, found, err := runner.Reviews.Load(req)
	if err != nil || !found {
		t.Fatalf("the answered review was not recorded: found=%v err=%v", found, err)
	}
	if rec.ArtifactRaw != posted {
		t.Fatalf("the store holds %q, want the exact comment bytes", rec.ArtifactRaw)
	}
	if rec.Standing != reviewstore.Advisory {
		t.Fatalf("a mailbox answer was stored with standing %q", rec.Standing)
	}
	if len(rec.Evidence) != 1 || rec.Evidence[0].Transport != reviewstore.GitHubMailbox {
		t.Fatalf("transport evidence is %+v, want one github_mailbox row", rec.Evidence)
	}
	ev := rec.Evidence[0]
	if ev.GitHubAuthor != "davecourtois" || ev.GitHubAuthorID != 1697116 || ev.GitHubComment <= 0 {
		t.Fatalf("github transport evidence is incomplete: %+v", ev)
	}
	// The reviewer is the artifact's, never the account GitHub authenticated.
	art, err := rec.Artifact()
	if err != nil {
		t.Fatal(err)
	}
	if art.ReviewerProvider != "chatgpt" {
		t.Fatalf("reviewer provider is %q; the GitHub login must not become the reviewer", art.ReviewerProvider)
	}
}

// requestFor recovers the request id the runner published, off the mailbox it
// was published to.
func requestFor(t *testing.T, _ ExchangeLog, m *prMailbox) string {
	t.Helper()
	for _, c := range m.comments {
		body, _ := c["body"].(string)
		if req, ok := ParseRequest(body); ok {
			return req.RequestID
		}
	}
	t.Fatal("no review request was published")
	return ""
}

// Whitespace the canonical parser admits is part of the artifact. The mailbox
// stores what was posted, so nothing re-renders the bytes on the way in and the
// digest keeps naming what the reviewer actually wrote.
func TestTheMailboxStoresTheExactCommentBytes(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 5*time.Second)
	var posted string
	answerWith(m, func(req Request) string {
		posted = "\n  " + canonicalAnswer(t, req.Subject, req.RequestID, req.ReviewerProvider, acceptJSON) + "\n \n"
		return posted
	}, "davecourtois", 1697116)

	res, err := runner.Run(context.Background(), reviewTurn(binding), nil)
	if err != nil {
		t.Fatalf("an answered review failed: %v", err)
	}
	rec, found, err := runner.Reviews.Load(requestFor(t, log, m))
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if rec.ArtifactRaw != posted {
		t.Fatalf("the store normalized the comment:\n got %q\nwant %q", rec.ArtifactRaw, posted)
	}
	if res.ReviewDigest != reviewartifact.Digest(posted) {
		t.Fatal("the returned digest is not the digest of the exact comment bytes")
	}
	if res.ReviewDigest == reviewartifact.Digest(strings.TrimSpace(posted)) {
		t.Fatal("the digest was taken over a trimmed copy of the comment")
	}
}

// Everything that must not answer. Each case is otherwise a perfect canonical
// review from the authenticated account, so a pass means exactly one rule
// stopped applying -- and nothing may be recorded either.
func TestTheMailboxRefusesAnAnswerThatIsNotTheOneAsked(t *testing.T) {
	other := Subject{TaskID: "T", BaseSHA: strings.Repeat("b", 40),
		CandidateDigest: "sha256:" + strings.Repeat("0", 64), CandidateTree: strings.Repeat("a", 40),
		ReviewCommit: strings.Repeat("c", 40)}

	for name, answer := range map[string]func(t *testing.T, req Request) string{
		// The historical grammar scar: an envelope with no reviewer= line. It
		// was acceptable before R2 and is deliberately not acceptable now.
		"no reviewer line": func(t *testing.T, req Request) string {
			raw, err := Review{Subject: req.Subject, RequestID: req.RequestID}.Marker()
			if err != nil {
				t.Fatal(err)
			}
			return raw + acceptJSON
		},
		"a provider that was not assigned": func(t *testing.T, req Request) string {
			return canonicalAnswer(t, req.Subject, req.RequestID, "claude", acceptJSON)
		},
		"another request id": func(t *testing.T, req Request) string {
			return canonicalAnswer(t, req.Subject, "r-ffffffffffffffff", req.ReviewerProvider, acceptJSON)
		},
		"another candidate": func(t *testing.T, req Request) string {
			return canonicalAnswer(t, other, req.RequestID, req.ReviewerProvider, acceptJSON)
		},
		"another candidate tree": func(t *testing.T, req Request) string {
			s := req.Subject
			s.CandidateTree = strings.Repeat("a", 40)
			return canonicalAnswer(t, s, req.RequestID, req.ReviewerProvider, acceptJSON)
		},
		"another candidate digest": func(t *testing.T, req Request) string {
			s := req.Subject
			s.CandidateDigest = "sha256:" + strings.Repeat("0", 64)
			return canonicalAnswer(t, s, req.RequestID, req.ReviewerProvider, acceptJSON)
		},
		"another base": func(t *testing.T, req Request) string {
			s := req.Subject
			s.BaseSHA = strings.Repeat("b", 40)
			return canonicalAnswer(t, s, req.RequestID, req.ReviewerProvider, acceptJSON)
		},
		"another review commit": func(t *testing.T, req Request) string {
			s := req.Subject
			s.ReviewCommit = strings.Repeat("c", 40)
			return canonicalAnswer(t, s, req.RequestID, req.ReviewerProvider, acceptJSON)
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, runner, binding, log := reviewRunnerWithLog(t, 150*time.Millisecond)
			answerWith(m, func(req Request) string { return answer(t, req) }, "davecourtois", 1697116)

			res, err := runner.Run(context.Background(), reviewTurn(binding), nil)
			if err == nil {
				t.Fatalf("an answer that was not the one asked for was accepted: %q", res.Text)
			}
			var owed *roles.ReviewUnanswered
			if !errors.As(err, &owed) {
				t.Fatalf("err = %v, want the review still owed", err)
			}
			if _, found, _ := runner.Reviews.Load(requestFor(t, log, m)); found {
				t.Fatal("a refused answer was recorded in the review store")
			}
		})
	}
}

// A perfect canonical review from the wrong GitHub account is an ordinary
// comment. Authentication comes first, and no property of the artifact can
// substitute for it.
func TestAPerfectArtifactFromTheWrongPrincipalAnswersNothing(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 150*time.Millisecond)
	answerWith(m, func(req Request) string {
		return canonicalAnswer(t, req.Subject, req.RequestID, req.ReviewerProvider, acceptJSON)
	}, "someone-else", 424242)

	if res, err := runner.Run(context.Background(), reviewTurn(binding), nil); err == nil {
		t.Fatalf("a comment from the wrong account answered the turn: %q", res.Text)
	}
	if _, found, _ := runner.Reviews.Load(requestFor(t, log, m)); found {
		t.Fatal("a comment from the wrong account was recorded")
	}
}

// The reviewer provider is never inferred from the GitHub login. The account
// here is literally named after another provider and the assignment still wins.
func TestTheProviderIsNeverInferredFromTheGitHubLogin(t *testing.T) {
	m, runner, binding, _ := reviewRunnerWithLog(t, 150*time.Millisecond)
	// The authenticated reviewer account, answering as a provider nobody asked.
	answerWith(m, func(req Request) string {
		return canonicalAnswer(t, req.Subject, req.RequestID, "davecourtois", acceptJSON)
	}, "davecourtois", 1697116)

	if res, err := runner.Run(context.Background(), reviewTurn(binding), nil); err == nil {
		t.Fatalf("an artifact naming the GitHub login as the provider answered the turn: %q", res.Text)
	}
}

// A well-formed envelope around prose is not a review. The workflow's own
// parser decides that, before anything durable happens.
func TestAMalformedVerdictNeverBecomesAStoredReview(t *testing.T) {
	for name, body := range map[string]string{
		"prose instead of a verdict": "LGTM, ship it",
		"a decision nobody defines":  `{"decision":"ship-it","summary":"fine","instructions":"","findings":[]}`,
		"json that is not a verdict": `{"hello":"world"}`,
	} {
		t.Run(name, func(t *testing.T) {
			m, runner, binding, log := reviewRunnerWithLog(t, 150*time.Millisecond)
			answerWith(m, func(req Request) string {
				return canonicalAnswer(t, req.Subject, req.RequestID, req.ReviewerProvider, body)
			}, "davecourtois", 1697116)

			res, err := runner.Run(context.Background(), reviewTurn(binding), nil)
			if err == nil {
				t.Fatalf("a payload the reviewer parser rejects answered the turn: %q", res.Text)
			}
			if _, found, _ := runner.Reviews.Load(requestFor(t, log, m)); found {
				t.Fatal("a payload the reviewer parser rejects was recorded")
			}
		})
	}
}

// The compatibility scar, both directions in one test: the historical artifact
// with no reviewer= line is refused, and a newly authored artifact carrying the
// SAME verdict body with reviewer=chatgpt from the beginning is accepted.
//
// Two byte identities, deliberately. Nothing edits the old artifact into
// compliance and calls it the same review.
func TestTheOldGrammarIsRefusedAndTheCanonicalOneIsAccepted(t *testing.T) {
	legacy := func(t *testing.T, req Request) string {
		t.Helper()
		raw, err := Review{Subject: req.Subject, RequestID: req.RequestID}.Marker()
		if err != nil {
			t.Fatal(err)
		}
		return raw + acceptJSON
	}

	m, runner, binding, _ := reviewRunnerWithLog(t, 150*time.Millisecond)
	answerWith(m, func(req Request) string { return legacy(t, req) }, "davecourtois", 1697116)
	if res, err := runner.Run(context.Background(), reviewTurn(binding), nil); err == nil {
		t.Fatalf("the pre-R2 grammar still answers a live review: %q", res.Text)
	}

	m2, runner2, binding2, log2 := reviewRunnerWithLog(t, 5*time.Second)
	var canonical string
	answerWith(m2, func(req Request) string {
		canonical = canonicalAnswer(t, req.Subject, req.RequestID, req.ReviewerProvider, acceptJSON)
		if strings.Contains(legacy(t, req), "reviewer=") {
			t.Fatal("the legacy fixture already names a reviewer; the two grammars are not distinguished")
		}
		return canonical
	}, "davecourtois", 1697116)
	if _, err := runner2.Run(context.Background(), reviewTurn(binding2), nil); err != nil {
		t.Fatalf("the canonical artifact was refused: %v", err)
	}
	rec, found, err := runner2.Reviews.Load(requestFor(t, log2, m2))
	if err != nil || !found {
		t.Fatalf("the canonical artifact was not recorded: found=%v err=%v", found, err)
	}
	if rec.ArtifactRaw != canonical {
		t.Fatal("the recorded bytes are not the canonical artifact that was posted")
	}
}

// A published relay writes the reviewer's ORIGINAL artifact to the common
// store, with the terminal principal and the App publication as evidence -- and
// the reviewer stays the artifact's.
func TestAPublishedRelayConvergesOnTheCommonRecord(t *testing.T) {
	f := newRelayFixture(t)
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	published, err := f.submit(art)
	if err != nil {
		t.Fatalf("relaying: %v", err)
	}

	rec, found, err := f.reviews.Load(relayRequest)
	if err != nil || !found {
		t.Fatalf("a published relay did not reach the common store: found=%v err=%v", found, err)
	}
	if rec.ArtifactRaw != art {
		t.Fatal("the common store holds bytes other than the reviewer's original artifact")
	}
	if rec.ReviewDigest != ReviewDigest(art) {
		t.Fatalf("digest is %s, want %s", rec.ReviewDigest, ReviewDigest(art))
	}
	if len(rec.Evidence) != 1 || rec.Evidence[0].Transport != reviewstore.LocalRelay {
		t.Fatalf("transport evidence is %+v, want one local_relay row", rec.Evidence)
	}
	ev := rec.Evidence[0]
	if ev.RelayPrincipal != operator.token() {
		t.Fatalf("relay principal evidence is %q, want %q", ev.RelayPrincipal, operator.token())
	}
	if ev.PublicationComment != published.PublicationComment || ev.Publication == "" {
		t.Fatalf("publication evidence is incomplete: %+v", ev)
	}
	art2, err := rec.Artifact()
	if err != nil {
		t.Fatal(err)
	}
	if art2.ReviewerProvider != "chatgpt" {
		t.Fatalf("reviewer provider is %q; the relaying operator must not become the reviewer", art2.ReviewerProvider)
	}
	if strings.Contains(art2.ReviewerProvider, "dave") {
		t.Fatal("the terminal principal became the reviewer provider")
	}
}

// Accepted is not published, and unpublished is not an answer. The common store
// stays empty until the App has actually posted the relay.
func TestAnAcceptedButUnpublishedRelayIsNotInTheCommonStore(t *testing.T) {
	f := newRelayFixture(t)
	f.mailbox.failPosts = true
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	if _, err := f.submit(art); !errors.Is(err, ErrRelayPublication) {
		t.Fatalf("err = %v, want ErrRelayPublication", err)
	}
	if _, found, _ := f.reviews.Load(relayRequest); found {
		t.Fatal("an unpublished relay became a consumable common review")
	}
	// The relay receipt is preserved for retry, which is RelayStore's remaining job.
	if rec, found, _ := f.store.Load(relayRequest); !found || rec.State != RelayAccepted {
		t.Fatalf("the accepted relay was not preserved for retry: found=%v %+v", found, rec)
	}
	// And the turn is not answered.
	if res, err := f.runner().Run(context.Background(), f.turn(), nil); err == nil {
		t.Fatalf("an unpublished relay answered the turn: %q", res.Text)
	}
}

// Publication succeeded and the common-store write did not. The published relay
// is preserved, the failure is explicit, and resubmitting the SAME artifact
// retries only the store write -- it must not publish a second receipt.
func TestAFailedConvergenceRetriesTheStoreWriteWithoutPublishingTwice(t *testing.T) {
	f := newRelayFixture(t)
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	// A file where the store's directory must go: the publication succeeds and
	// the record cannot be written.
	blocked := filepath.Join(t.TempDir(), "reviews")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := f
	broken.reviews = reviewstore.Store{Dir: blocked}

	published, err := broken.submit(art)
	if !errors.Is(err, ErrRelayConvergence) {
		t.Fatalf("err = %v, want ErrRelayConvergence", err)
	}
	if published.State != RelayPublished || published.PublicationComment <= 0 {
		t.Fatalf("the published relay was not preserved: %+v", published)
	}
	postsAfterFirst := len(f.mailbox.posted())
	if postsAfterFirst != 1 {
		t.Fatalf("the relay published %d comments, want 1", postsAfterFirst)
	}

	// Resubmit the identical artifact against a working store.
	again, err := f.submit(art)
	if err != nil {
		t.Fatalf("retrying convergence: %v", err)
	}
	if again.PublicationComment != published.PublicationComment {
		t.Fatalf("the retry republished: comment %d then %d", published.PublicationComment, again.PublicationComment)
	}
	if n := len(f.mailbox.posted()); n != postsAfterFirst {
		t.Fatalf("the retry posted again: %d comments, want %d", n, postsAfterFirst)
	}
	rec, found, err := f.reviews.Load(relayRequest)
	if err != nil || !found {
		t.Fatalf("the retry did not record the review: found=%v err=%v", found, err)
	}
	if rec.ArtifactRaw != art {
		t.Fatal("the retry recorded bytes other than the reviewer's artifact")
	}
}

// The same reviewer bytes seen on BOTH transports are one review. The second
// arrival adds its evidence and changes nothing about what the review means.
func TestTheSameArtifactOnBothTransportsIsOneRecord(t *testing.T) {
	f := newRelayFixture(t)
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(art); err != nil {
		t.Fatal(err)
	}
	first, _, err := f.reviews.Load(relayRequest)
	if err != nil {
		t.Fatal(err)
	}

	// The same bytes, now observed directly on the mailbox.
	comment := f.mailbox.add(art, "davecourtois", 1697116)
	second, err := f.reviews.Accept(reviewstore.Acceptance{
		RequestID: relayRequest, Artifact: art,
		Evidence: reviewstore.Evidence{Transport: reviewstore.GitHubMailbox,
			GitHubAuthor: "davecourtois", GitHubAuthorID: 1697116, GitHubComment: comment},
		Validate: func(reviewartifact.Artifact) error { return nil },
	})
	if err != nil {
		t.Fatalf("the same bytes on a second transport were refused: %v", err)
	}
	if second.ReviewDigest != first.ReviewDigest || second.ArtifactRaw != first.ArtifactRaw {
		t.Fatal("a second transport changed the review")
	}
	if !second.AcceptedAt.Equal(first.AcceptedAt) {
		t.Fatal("a second transport moved the acceptance time")
	}
	if len(second.Evidence) != 2 {
		t.Fatalf("evidence rows = %d, want 2", len(second.Evidence))
	}
}

// A second, different artifact for an answered request is a conflict wherever
// it arrives from, and the first review is untouched.
func TestADifferentArtifactForAnAnsweredRequestConflicts(t *testing.T) {
	f := newRelayFixture(t)
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(art); err != nil {
		t.Fatal(err)
	}
	rival := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"revise","summary":"actually no","instructions":"redo it","findings":[]}`)

	_, err := f.reviews.Accept(reviewstore.Acceptance{
		RequestID: relayRequest, Artifact: rival,
		Evidence: reviewstore.Evidence{Transport: reviewstore.GitHubMailbox, GitHubComment: 9001},
		Validate: func(reviewartifact.Artifact) error { return nil },
	})
	if !errors.Is(err, reviewstore.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	rec, _, err := f.reviews.Load(relayRequest)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ArtifactRaw != art {
		t.Fatal("a conflicting artifact replaced the accepted review")
	}
}

// The runner consumes a converged review from the common store, closes the
// obligation, and neither publishes nor mints another request.
func TestTheRunnerConsumesAConvergedReviewWithoutMintingAnother(t *testing.T) {
	f := newRelayFixture(t)
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(art); err != nil {
		t.Fatal(err)
	}
	postsBefore := len(f.mailbox.posted())

	res, err := f.runner().Run(context.Background(), f.turn(), nil)
	if err != nil {
		t.Fatalf("the converged review was not consumed: %v", err)
	}
	if res.Session != roles.Unverified {
		t.Fatalf("a consumed review claimed session %q", res.Session)
	}
	if res.ReviewDigest != ReviewDigest(art) {
		t.Fatalf("consumed digest %s, want %s", res.ReviewDigest, ReviewDigest(art))
	}
	if n := len(f.mailbox.posted()); n != postsBefore {
		t.Fatalf("consuming posted %d new comments, want none", n-postsBefore)
	}
	if pending, _ := f.exchanges.PendingReviews(); len(pending) != 0 {
		t.Fatalf("the answered obligation is still owed: %+v", pending)
	}
}

// A request published before the assignment was recorded cannot authenticate
// anybody. The assignment is not recoverable from today's configuration, and
// guessing it would invent the fact the check exists to verify.
func TestALegacyObligationWithNoRecordedAssignmentAuthenticatesNobody(t *testing.T) {
	f := newRelayFixture(t)
	// Rewrite the obligation the way a pre-R2 process wrote it.
	if err := f.exchanges.Close(relaySubject.TaskID, relayRequest); err != nil {
		t.Fatal(err)
	}
	if err := f.exchanges.Open(ExchangeRecord{
		TaskID: relaySubject.TaskID, RequestID: relayRequest, RequestComment: 5686428018, Conversation: "157",
		PublishedAt: time.Now().Add(-time.Hour).UTC(), Kind: ExchangeReview,
		BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
	}); err != nil {
		t.Fatal(err)
	}
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(art); !errors.Is(err, ErrRelayRefused) {
		t.Fatalf("err = %v, want a refusal against a legacy obligation", err)
	}
	if _, found, _ := f.reviews.Load(relayRequest); found {
		t.Fatal("an artifact was recorded against an obligation that never named its reviewer")
	}
}

// Structural: the production mailbox acceptance path reads answers through the
// canonical parser. The legacy grammar may still exist for fixtures and tooling;
// it may not be how a live review is accepted.
func TestTheProductionMailboxPathUsesTheCanonicalParser(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "transport.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var reads, legacy int
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Reviews" {
			return true
		}
		ast.Inspect(fn, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch f := call.Fun.(type) {
			case *ast.SelectorExpr:
				if id, ok := f.X.(*ast.Ident); ok && id.Name == "reviewartifact" && f.Sel.Name == "Parse" {
					reads++
				}
			case *ast.Ident:
				if f.Name == "ParseReview" {
					legacy++
				}
			}
			return true
		})
		return false
	})
	if reads == 0 {
		t.Error("Reviews does not parse comments through reviewartifact.Parse")
	}
	if legacy != 0 {
		t.Errorf("Reviews still reads answers through the legacy ParseReview (%d call(s))", legacy)
	}
}

// Structural: one component interprets what a review SAYS.
//
// Both acceptance paths -- the mailbox and the relay -- hand the reviewer body
// to workflow's parser, and the bridge keeps exactly one durable copy of the
// decision (the relay receipt, holding what that parser already decided). A
// second `json:"decision"` here would be a second representation of the verdict,
// agreeing with the reviewer's bytes only until one of them is edited.
func TestTheBridgeNeverInterpretsAVerdictItself(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	decisionFields := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		blob, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(blob)
		if n := strings.Count(src, "workflow.ValidateReviewBody("); n > 0 {
			calls[name] = n
		}
		decisionFields += strings.Count(src, `json:"decision"`)
	}
	for _, path := range []string{"runner.go", "relay.go"} {
		if calls[path] == 0 {
			t.Errorf("%s accepts a review without asking workflow's parser what the body says", path)
		}
	}
	// One: RelayRecord's durable receipt of a verdict workflow already read.
	if decisionFields != 1 {
		t.Errorf("the bridge holds %d json:\"decision\" fields, want exactly 1 (the relay receipt); "+
			"a second is a second representation of the verdict", decisionFields)
	}
}

// The published request STATES who was asked, and the obligation records it.
//
// Both halves matter. The request is what gives the reviewer an exact value to
// echo; the record is what lets a restarted process, or a relay arriving after
// the configuration changed, check an answer against the assignment that was
// actually made rather than against whatever is configured by then.
func TestTheRequestAndTheObligationBothNameTheAssignedReviewer(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 120*time.Millisecond)
	_, _ = runner.Run(context.Background(), reviewTurn(binding), nil)

	var published *Request
	for _, c := range m.comments {
		body, _ := c["body"].(string)
		if req, ok := ParseRequest(body); ok {
			published = &req
			break
		}
	}
	if published == nil {
		t.Fatal("no review request was published")
	}
	if published.ReviewerProvider != "chatgpt" {
		t.Fatalf("the request states reviewer %q, want the assigned chatgpt", published.ReviewerProvider)
	}

	owed, err := log.PendingReviews()
	if err != nil || len(owed) != 1 {
		t.Fatalf("pending reviews: %+v err=%v", owed, err)
	}
	if owed[0].ReviewerProvider != "chatgpt" {
		t.Fatalf("the obligation recorded reviewer %q, want chatgpt", owed[0].ReviewerProvider)
	}
	if owed[0].RequestID != published.RequestID {
		t.Fatalf("the obligation names request %s and the published one is %s",
			owed[0].RequestID, published.RequestID)
	}
}

// An obligation that never recorded its assignment authenticates nobody, and
// the gap is never filled from the running configuration.
//
// The runner here IS configured for chatgpt and the stored review IS from
// chatgpt. It still refuses, because the question is not "does this match what
// we would assign today" but "does this match what we actually asked".
func TestAStoredReviewCannotAnswerAnObligationThatNeverNamedItsReviewer(t *testing.T) {
	f := newRelayFixture(t)
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(art); err != nil {
		t.Fatal(err)
	}
	// Reopen the obligation the way a pre-R2 process wrote it: no assignment.
	if err := f.exchanges.Close(relaySubject.TaskID, relayRequest); err != nil {
		t.Fatal(err)
	}
	if err := f.exchanges.Open(ExchangeRecord{
		TaskID: relaySubject.TaskID, RequestID: relayRequest, RequestComment: 5686428018, Conversation: "157",
		PublishedAt: time.Now().Add(-time.Hour).UTC(), Kind: ExchangeReview,
		BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
	}); err != nil {
		t.Fatal(err)
	}

	runner := f.runner()
	if runner.ReviewerProvider != "chatgpt" {
		t.Fatalf("this runner is assigned %q; the test needs it to match the artifact", runner.ReviewerProvider)
	}
	res, err := runner.Run(context.Background(), f.turn(), nil)
	var owed *roles.ReviewUnanswered
	if !errors.As(err, &owed) {
		t.Fatalf("a legacy obligation was answered from the running configuration: text=%q err=%v", res.Text, err)
	}
	if pending, _ := f.exchanges.PendingReviews(); len(pending) != 1 {
		t.Fatalf("the owed request did not survive: %+v", pending)
	}
}
