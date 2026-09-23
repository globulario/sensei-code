package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// A wake carries one locator and nothing else. These pin that the doorbell
// cannot become a second channel for protocol content.

func TestAWakeCarriesExactlyOneLocator(t *testing.T) {
	body, err := RenderWake(5573752546)
	if err != nil {
		t.Fatal(err)
	}
	if body != "[sensei-code:wake]\nrequest_comment=5573752546\n" {
		t.Fatalf("wake body = %q", body)
	}
	// Nothing a turn is bound to may appear in a wake.
	for _, forbidden := range []string{"task", "objective_digest", "base", "graph_build_commit",
		"candidate", "decision", "kind="} {
		if strings.Contains(body, forbidden) {
			t.Errorf("a wake carried %q; it must say only which comment to look at", forbidden)
		}
	}
	got, err := ParseWake(body)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if got != 5573752546 {
		t.Fatalf("locator = %d", got)
	}
}

func TestAWakeMustPointAtAPublishedComment(t *testing.T) {
	for _, bad := range []int64{0, -1} {
		if _, err := RenderWake(bad); err == nil {
			t.Errorf("RenderWake(%d) produced a wake pointing at nothing", bad)
		}
	}
}

// Strict by construction: a body that is a wake PLUS anything else is refused,
// because the one use of this value is fetching a comment by it.
//
// MALFORMED, NOT ABSENT. Every body here OPENS with the wake envelope at
// position zero, so every one of them CLAIMS to be a wake and is reported as a
// broken one. They used to come back as ErrNotAWake -- the same answer a
// shopping list gets -- which told a caller "nobody rang" while a corrupt
// doorbell sat in the conversation.
func TestAMalformedWakeIsReportedAsABrokenWakeNotAnAbsentOne(t *testing.T) {
	for name, body := range map[string]string{
		"extra field":                     "[sensei-code:wake]\nrequest_comment=1\ntask=t-1\n",
		"extra prose":                     "[sensei-code:wake]\nrequest_comment=1\nplease hurry\n",
		"no locator":                      "[sensei-code:wake]\n",
		"non numeric":                     "[sensei-code:wake]\nrequest_comment=abc\n",
		"wrong field":                     "[sensei-code:wake]\ncomment=1\n",
		"zero locator":                    "[sensei-code:wake]\nrequest_comment=0\n",
		"no delimiter after the envelope": "[sensei-code:wake] request_comment=1\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWake(body)
			if err == nil {
				t.Fatalf("ParseWake accepted %q as locator %d", body, got)
			}
			if got != 0 {
				t.Errorf("a refused wake returned locator %d", got)
			}
			if !errors.Is(err, ErrMalformedWake) {
				t.Fatalf("a body that opens with the wake envelope was not attributed to it: %v", err)
			}
			if errors.Is(err, ErrNotAWake) {
				t.Error("a broken wake was reported as no wake at all")
			}
		})
	}
}

// The other half of the same distinction: these do not CLAIM to be wakes, so
// there is nothing to attribute and nothing broken to report.
func TestABodyThatDoesNotOpenWithTheWakeEnvelopeIsNotAWakeAtAll(t *testing.T) {
	for name, body := range map[string]string{
		"prose before":      "look at this\n[sensei-code:wake]\nrequest_comment=1\n",
		"architecture turn": "[sensei-code:architecture]\ntask=t\n\n{}",
		"a withdrawal":      "[sensei-code:withdrawn]\nrequest=r-1\n",
		"empty":             "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseWake(body); !errors.Is(err, ErrNotAWake) {
				t.Errorf("ParseWake(%q) did not report an absent wake: %v", body, err)
			}
		})
	}
}

// LEADING-WHITESPACE CONTROL FOR THE WAKE GRAMMAR. Line endings are normalized;
// position zero is not negotiated.
//
// ParseWake used to run strings.TrimSpace across the whole body before matching,
// and the test that stood here REQUIRED "\n  " in front of the marker to parse.
// That is the marker-at-a-nonzero-offset defect wearing the doorbell's clothes:
// a comment whose first characters are prose-indentation acquired wake identity
// from bytes that were never at position zero.
//
// CRLF is a different thing and is still accepted. Rewriting line endings cannot
// move the envelope, because the envelope contains no newline -- a body that
// opens with the marker opens with it under either encoding. Trimming moves
// position zero; normalizing does not. Trailing whitespace is likewise still
// tolerated: it sits after the artifact, where identity is already settled.
func TestAWakeSurvivesCRLFButNotLeadingWhitespace(t *testing.T) {
	for name, body := range map[string]string{
		"CRLF throughout":     "[sensei-code:wake]\r\nrequest_comment=42\r\n",
		"trailing whitespace": "[sensei-code:wake]\nrequest_comment=42\n  \n",
		"both":                "[sensei-code:wake]\r\nrequest_comment=42\r\n \t\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWake(body)
			if err != nil || got != 42 {
				t.Errorf("ParseWake(%q) = %d, %v", body, got, err)
			}
		})
	}
	for name, body := range map[string]string{
		"one leading space":          " [sensei-code:wake]\nrequest_comment=42\n",
		"one leading tab":            "\t[sensei-code:wake]\nrequest_comment=42\n",
		"a blank line and an indent": "\n  [sensei-code:wake]\nrequest_comment=42\n",
		"a leading CRLF":             "\r\n[sensei-code:wake]\nrequest_comment=42\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWake(body)
			if err == nil {
				t.Fatalf("a wake envelope at a nonzero offset was read as a wake: %q -> %d", body, got)
			}
			// Not merely refused: it never claimed to be a wake, so it is not a
			// broken wake either. Padding does not buy partial protocol identity.
			if !errors.Is(err, ErrNotAWake) {
				t.Errorf("a padded body was attributed to the wake grammar: %v", err)
			}
		})
	}
}

// THE WITHDRAWAL ENVELOPE, WITNESSED HERE BESIDE THE WAKE, and the placement is
// deliberate rather than convenient.
//
// Wake and withdrawal are this grammar's two LOCATOR envelopes: each is an
// envelope plus exactly one field, each is read from a mailbox comment nobody
// authenticated as a role, and both reached identity through the same
// TrimSpace-then-anchored-match path that the positional rule replaces. They
// are one repair with two spellings, so they are witnessed as one.
//
// A withdrawal's claim is NEGATIVE -- "no process is waiting for this any more"
// -- so the difference between "no withdrawal was posted" and "a withdrawal was
// posted and cannot be read" licenses opposite actions. One error value for both
// answers a caller the first while the second is true.
func TestAMalformedWithdrawalIsReportedAsABrokenOneNotAnAbsentOne(t *testing.T) {
	for name, body := range map[string]string{
		"no request":                      "[sensei-code:withdrawn]\n",
		"empty request":                   "[sensei-code:withdrawn]\nrequest=\n",
		"a second field":                  "[sensei-code:withdrawn]\nrequest=r-1\ntask=task-1\n",
		"a decision smuggled in":          "[sensei-code:withdrawn]\nrequest=r-1\ndecision=proceed\n",
		"no delimiter after the envelope": "[sensei-code:withdrawn] request=r-1\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWithdrawal(body)
			if err == nil {
				t.Fatalf("accepted %q as a withdrawal of %q", body, got)
			}
			if got != "" {
				t.Errorf("a refused withdrawal returned request %q", got)
			}
			if !errors.Is(err, ErrMalformedWithdrawal) {
				t.Fatalf("a body opening with the withdrawal envelope was not attributed to it: %v", err)
			}
			if errors.Is(err, ErrNotAWithdrawal) {
				t.Error("a broken withdrawal was reported as no withdrawal at all")
			}
		})
	}
}

// The other half: these never claimed to be withdrawals.
func TestABodyThatDoesNotOpenWithTheWithdrawalEnvelopeIsNotAWithdrawalAtAll(t *testing.T) {
	for name, body := range map[string]string{
		"a wake":            "[sensei-code:wake]\nrequest_comment=1\n",
		"prose before":      "look at this\n[sensei-code:withdrawn]\nrequest=r-1\n",
		"architecture turn": "[sensei-code:architecture]\ntask=t\n\n{}",
		"empty":             "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseWithdrawal(body); !errors.Is(err, ErrNotAWithdrawal) {
				t.Errorf("ParseWithdrawal(%q) did not report an absent withdrawal: %v", body, err)
			}
		})
	}
}

// LEADING-WHITESPACE CONTROL FOR THE WITHDRAWAL GRAMMAR, and the payload-offset
// control beside it.
//
// ParseWithdrawal used to TrimSpace the whole body before an anchored match, so
// an indented marker was promoted to position zero and retracted a live request.
// A withdrawal is the one envelope whose whole effect is to declare something
// dead; granting that power to bytes that were not at position zero is the worst
// place in this grammar to be generous.
func TestAWithdrawalSurvivesCRLFButNotLeadingWhitespace(t *testing.T) {
	for name, body := range map[string]string{
		"CRLF throughout":     "[sensei-code:withdrawn]\r\nrequest=r-1\r\n",
		"trailing whitespace": "[sensei-code:withdrawn]\nrequest=r-1\n  \n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWithdrawal(body)
			if err != nil || got != "r-1" {
				t.Errorf("ParseWithdrawal(%q) = %q, %v", body, got, err)
			}
		})
	}
	for name, body := range map[string]string{
		"one leading space":          " [sensei-code:withdrawn]\nrequest=r-1\n",
		"one leading tab":            "\t[sensei-code:withdrawn]\nrequest=r-1\n",
		"a blank line and an indent": "\n  [sensei-code:withdrawn]\nrequest=r-1\n",
		"quoted inside prose":        "I am not withdrawing it:\n[sensei-code:withdrawn]\nrequest=r-1\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWithdrawal(body)
			if err == nil {
				t.Fatalf("a withdrawal envelope at a nonzero offset retracted %q: %q", got, body)
			}
			if !errors.Is(err, ErrNotAWithdrawal) {
				t.Errorf("a padded body was attributed to the withdrawal grammar: %v", err)
			}
		})
	}
}

// recordingDoorbell records rings and can be made to fail.
type recordingDoorbell struct {
	rung []int64
	err  error
}

func (d *recordingDoorbell) Ring(_ context.Context, id int64) error {
	d.rung = append(d.rung, id)
	return d.err
}

// The load-bearing property: a doorbell that fails does NOT cost a second
// request. The published request is durable, so failing the turn here would
// discard it and the next attempt would mint another for the same objective —
// a transport problem becoming a duplicate governed turn.
func TestAFailedRingDoesNotPublishASecondRequest(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)

	bell := &recordingDoorbell{err: errors.New("doorbell unreachable")}
	runner := &ArchitectureRunner{
		Issue:        box,
		Binding:      architectureBinding(),
		NewRequestID: NewRequestID,
		Poll:         10 * time.Millisecond,
		Wait:         120 * time.Millisecond,
		Doorbell:     bell,
	}

	var events []event.Event
	_, err := runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"},
		func(e event.Event) { events = append(events, e) })
	if err == nil {
		t.Fatal("an unanswered turn returned success")
	}

	posted := 0
	for _, c := range m.comments {
		if body, ok := c["body"].(string); ok && strings.HasPrefix(body, "[sensei-code:architecture-request]") {
			posted++
		}
	}
	if posted != 1 {
		t.Fatalf("%d architecture requests published; a doorbell failure must not mint another", posted)
	}
	if len(bell.rung) != 1 {
		t.Fatalf("doorbell rung %d time(s), want exactly one attempt", len(bell.rung))
	}

	// The locator must be reported, so a retry can ring the same comment.
	var reported bool
	for _, e := range events {
		if strings.Contains(string(e.Payload), `"request_comment"`) {
			reported = true
		}
	}
	if !reported {
		t.Error("a failed ring did not report the locator a retry must target")
	}
}

// The happy path rings exactly the comment that was just published.
func TestTheDoorbellRingsThePublishedRequest(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "157", true)

	bell := &recordingDoorbell{}
	runner := &ArchitectureRunner{
		Issue:        box,
		Binding:      architectureBinding(),
		NewRequestID: NewRequestID,
		Poll:         10 * time.Millisecond,
		Wait:         120 * time.Millisecond,
		Doorbell:     bell,
	}
	_, _ = runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"}, nil)

	if len(bell.rung) != 1 {
		t.Fatalf("doorbell rung %d time(s), want one", len(bell.rung))
	}
	if bell.rung[0] <= 0 {
		t.Fatalf("doorbell rang locator %d; it must point at the published comment", bell.rung[0])
	}
}

// No doorbell configured is the arrangement wherever the wake path admits the
// App. It must not become an error, and nothing may be posted in its place.
func TestNoDoorbellIsNotAFailure(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)

	runner := &ArchitectureRunner{
		Issue: box, Binding: architectureBinding(), NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: 120 * time.Millisecond,
	}
	_, _ = runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"}, nil)

	for _, c := range m.comments {
		if body, ok := c["body"].(string); ok && strings.HasPrefix(body, WakeMarker) {
			t.Error("a wake was posted with no doorbell configured")
		}
	}
}

// The doorbell is not a fallback inside the App transport. GHDoorbell is its
// own type, and AppClient exposes no way to ring one.
func TestTheDoorbellIsSeparateFromTheAppTransport(t *testing.T) {
	var _ Doorbell = GHDoorbell{}
	if _, ok := any(&AppClient{}).(Doorbell); ok {
		t.Error("AppClient satisfies Doorbell; the operator-credential wake path must not " +
			"share a type with the App transport that refuses to fall back to it")
	}
}

func TestADoorbellWithNoConversationRefuses(t *testing.T) {
	if err := (GHDoorbell{Dir: t.TempDir()}).Ring(context.Background(), 42); err == nil {
		t.Error("a doorbell with no conversation rang anyway")
	}
}

// A role the bridge does not carry reaches the engine's own ladder untouched.
// This is the difference from the forbidden fallback: the bridge never accepts
// the turn, so nothing is attributed to a party that did not answer it.
func TestARoleTheBridgeDoesNotCarryReachesTheFallback(t *testing.T) {
	fb := &recordingResolver{}
	reviewer := &Runner{Issue: Issue{Number: "157",
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"}}}
	r := Resolver{
		Provider: "chatgpt",
		Roles:    map[roles.Role]bool{roles.Reviewer: true}, // architect NOT carried
		Reviewer: reviewer,
		Fallback: fb,
	}

	_, err := r.Resolve(workflow.RunnerSpec{
		Role:         roles.Architect,
		Agent:        config.Agent{Name: "chatgpt"},
		TaskID:       architectureBinding().TaskID,
		Architecture: architectureBinding(),
	})
	if err != nil {
		t.Fatalf("an uncarried architect turn errored instead of reaching the ladder: %v", err)
	}
	if len(fb.saw) != 1 {
		t.Fatalf("fallback saw %d turns, want the architect turn", len(fb.saw))
	}

	// The carried role still goes over the bridge.
	got, err := r.Resolve(workflow.RunnerSpec{Role: roles.Reviewer, Agent: config.Agent{Name: "chatgpt"}})
	if err != nil {
		t.Fatal(err)
	}
	carried, ok := got.Runner.(*Runner)
	if !ok || carried.Issue.Number != reviewer.Issue.Number {
		t.Errorf("a carried role stopped being carried: %T", got.Runner)
	}
	// The resolver hands back a per-turn copy carrying the WORKFLOW's
	// assignment, so the request it publishes can state who was asked.
	if ok && carried.ReviewerProvider != "chatgpt" {
		t.Errorf("the carried runner was assigned %q, want the workflow's chatgpt", carried.ReviewerProvider)
	}
}

// none means none: every role reaches the ladder, and the bridge accepts nothing.
func TestABridgeCarryingNoRolesAcceptsNothing(t *testing.T) {
	fb := &recordingResolver{}
	r := Resolver{
		Provider: "chatgpt",
		Roles:    map[roles.Role]bool{},
		Reviewer: &Runner{Issue: Issue{Number: "157",
			ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"}}},
		Fallback: fb,
	}
	for _, role := range []roles.Role{roles.Architect, roles.Reviewer} {
		if _, err := r.Resolve(workflow.RunnerSpec{
			Role: role, Agent: config.Agent{Name: "chatgpt"},
			TaskID: architectureBinding().TaskID, Architecture: architectureBinding(),
		}); err != nil {
			t.Fatalf("%v: %v", role, err)
		}
	}
	if len(fb.saw) != 2 {
		t.Fatalf("fallback saw %d turns, want both", len(fb.saw))
	}
}

// The gh helper must not merge stderr into the value, because the mailbox reads
// unmarshal that value as JSON. A gh advisory would turn a successful read into
// a parse failure and report malformed content when the content was fine.
func TestAGhAdvisoryDoesNotCorruptAMailboxRead(t *testing.T) {
	dir := t.TempDir()
	// A stand-in for gh that writes a valid answer to stdout and an advisory to
	// stderr, which is exactly what a real gh version notice looks like.
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(
		"#!/bin/sh\necho 'A new release of gh is available' >&2\nprintf '%s' '[{\"body\":\"x\"}]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := run(context.Background(), dir, []string{"api", "whatever"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out, "new release") {
		t.Fatalf("stderr leaked into the value a mailbox read unmarshals: %q", out)
	}
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("the value did not survive as JSON: %v (got %q)", err, out)
	}
}

// A failure still reports what gh said, so a real error is not silently blank.
func TestAGhFailureStillCarriesWhatGhSaid(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(
		"#!/bin/sh\necho 'gh: could not resolve repository' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := run(context.Background(), dir, []string{"api", "whatever"})
	if err == nil {
		t.Fatal("a failing gh returned no error")
	}
	if !strings.Contains(err.Error(), "could not resolve repository") {
		t.Errorf("the error dropped what gh said: %v", err)
	}
}

// every_remote_exchange_is_bounded has two clauses, and only the first was
// tested. The bound was enforced and proven; "reports why it ended" lived only
// in the returned error, so from outside the process an exchange that had given
// up looked exactly like one still waiting — and the engine's re-ask looked
// like nothing at all. That ambiguity is what made a silent transport expensive
// to diagnose: distinguishing "no answer yet" from "no answer, I stopped"
// required reading GitHub by hand.
func TestAnUnansweredArchitectTurnReportsThatItEnded(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "157", true)

	runner := &ArchitectureRunner{
		Issue: box, Binding: architectureBinding(), NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: 120 * time.Millisecond,
	}
	var events []event.Event
	_, err := runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"},
		func(e event.Event) { events = append(events, e) })
	if err == nil {
		t.Fatal("an unanswered turn returned success")
	}

	var ended *event.Event
	for i := range events {
		if strings.Contains(string(events[i].Payload), `"outcome":"unanswered"`) {
			ended = &events[i]
		}
	}
	if ended == nil {
		t.Fatal("the turn ended and emitted nothing saying so; an operator cannot " +
			"tell a finished exchange from one still waiting")
	}
	// The fields an operator needs to act: which request, how long, and why.
	for _, want := range []string{`"request_id"`, `"waited"`, `"reason"`, `"objective_digest"`} {
		if !strings.Contains(string(ended.Payload), want) {
			t.Errorf("the end report omits %s: %s", want, ended.Payload)
		}
	}
	if !strings.Contains(ended.Summary, "stands") {
		t.Errorf("the report does not say the request stands, so a retry may publish "+
			"a second one: %q", ended.Summary)
	}
}

func TestAnUnansweredReviewTurnReportsThatItEnded(t *testing.T) {
	dir, base, tree1, _ := tempRepo(t)
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "157", true)

	// A real pushable remote, so the turn reaches the WAIT rather than dying at
	// publication. Without it this test would skip, and a check that skips is
	// not a check — the path under test is the end of an exchange that waited.
	bare := t.TempDir()
	for _, args := range [][]string{
		{"init", "--bare", "-q", bare},
		{"-C", dir, "remote", "add", "origin", bare},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	runner := &Runner{
		Issue: box, RepoDir: dir, Remote: "origin", NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: 120 * time.Millisecond, ReviewerProvider: "chatgpt",
		Exchanges: ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")},
	}
	var events []event.Event
	_, err := runner.Run(context.Background(), agent.Request{
		Role: roles.Reviewer, TaskID: "T",
		Binding: roles.Binding{TaskID: "T", BaseSHA: base, CandidateTree: tree1, CandidateDigest: digestC1},
	}, func(e event.Event) { events = append(events, e) })
	if err == nil {
		t.Fatal("an unanswered turn returned success")
	}
	// Publishing the snapshot needs a pushable remote, which a bare temp repo
	// has not got. That failure is reported by its own error and is a different
	// outcome; this test is about the end of an exchange that actually WAITED.
	if strings.Contains(err.Error(), "publishing the review snapshot") {
		t.Fatalf("the turn never reached the wait, so the end-report path was not "+
			"exercised: %v", err)
	}

	for _, e := range events {
		if strings.Contains(string(e.Payload), `"outcome":"unanswered"`) {
			return // reported
		}
	}
	t.Fatal("a review turn ended and emitted nothing saying so")
}

// The wake must land in the repository the REQUEST was published to.
//
// The App pins the mailbox to its own owner/repo; gh resolves a repository from
// the directory it runs in. While a control surface serves the repository its
// mailbox lives in, the two agree and nothing distinguishes them. Serve a
// different workspace and they diverge silently: the request sits in one
// repository's conversation while the wake is posted to another repository's
// issue of the same number. Nobody is told to look, and the exchange expires
// looking exactly like a remote that chose not to reply.
func TestTheWakeIsAddressedToTheMailboxRepositoryNotTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(
		"#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argv+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Dir is a workspace that is NOT the mailbox repository.
	d := GHDoorbell{Dir: dir, Conversation: "157", Repo: "globulario/sensei-code"}
	if err := d.Ring(context.Background(), 12345); err != nil {
		t.Fatalf("Ring: %v", err)
	}

	recorded, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(recorded)), "\n")
	var target string
	for i, a := range args {
		if a == "-R" && i+1 < len(args) {
			target = args[i+1]
		}
	}
	if target != "globulario/sensei-code" {
		t.Fatalf("the wake was not addressed to the mailbox repository; gh argv = %v\n"+
			"without an explicit -R the wake lands wherever %s resolves, which is a "+
			"different conversation than the request", args, dir)
	}
}

// An unset Repo keeps the original behaviour: gh resolves from Dir. Installations
// whose workspace IS the mailbox repository are unaffected by the field existing.
func TestAnUnsetDoorbellRepoDoesNotAddressAnyRepository(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	script := filepath.Join(dir, "gh")
	if err := os.WriteFile(script, []byte(
		"#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argv+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	d := GHDoorbell{Dir: dir, Conversation: "157"}
	if err := d.Ring(context.Background(), 12345); err != nil {
		t.Fatalf("Ring: %v", err)
	}
	recorded, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recorded), "-R") {
		t.Fatalf("an unset Repo still aimed gh at a repository: %q", recorded)
	}
}

// blockingDoorbell rings, and on its way through makes the exchange record at
// path impossible to remove.
//
// THE DOORBELL IS THE SEAM, not the subject. The runner opens the exchange
// record, then rings, then waits: ringing is the one point at which a test can
// reach a record that has genuinely been written and is about to be closed. What
// it simulates is a durable store whose close FAILS -- a read-only mount, a
// revoked permission, an I/O error -- and it does so by replacing the record
// with a non-empty directory, which os.Remove refuses for every user including
// root. A chmod would have made this test pass or fail depending on who ran it.
type blockingDoorbell struct {
	path string
	rung int
}

func (d *blockingDoorbell) Ring(_ context.Context, _ int64) error {
	d.rung++
	if err := os.RemoveAll(d.path); err != nil {
		return err
	}
	if err := os.MkdirAll(d.path, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.path, "occupied"), []byte("x"), 0o600)
}

// POSITIONAL FRAMING W7 -- A FAILED EXCHANGE CLOSE IS NOT SETTLEMENT.
//
// THE DEFECT. The refused branch discarded the error from closing its durable
// exchange record, reported outcome=refused and returned the typed refusal. A
// close that fails leaves the exchange PENDING, and a pending exchange is
// withdrawn by the next startup -- so this process would say the consumer
// refused the request while the next one retracted it as though nobody had ever
// replied. Two durable records, disagreeing, about one exchange.
//
// TWO CLAIMS, and both are needed. The failure must be SURFACED, and the typed
// refusal details must SURVIVE it: a repair that reported the close failure by
// replacing the refusal with a storage error would lose the consumer's
// diagnostic, which is the whole thing the refusal envelope exists to carry.
func TestARefusalWhoseExchangeCannotBeClosedIsNotReportedAsSettled(t *testing.T) {
	const requestID = "r-00000000feedface"
	_, refusal := architectureRefusalFixture()
	refusal.RequestID = requestID

	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	m.onPost = func(body string) []map[string]any {
		posted, ok := ParseArchitectureRequest(body)
		if !ok {
			return nil
		}
		wire, rerr := ArchitectureRefusal{
			Binding: posted.Binding, RequestID: posted.RequestID,
			Stage: refusal.Stage, Reason: refusal.Reason,
		}.Marker()
		if rerr != nil {
			return nil
		}
		return []map[string]any{{
			"id": float64(7401), "body": wire,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		}}
	}

	dir := filepath.Join(t.TempDir(), "exchanges")
	log := ExchangeLog{Dir: dir}
	bell := &blockingDoorbell{path: filepath.Join(dir, architectureBinding().TaskID+"."+requestID+".json")}
	runner := &ArchitectureRunner{
		Issue: box, Binding: architectureBinding(),
		NewRequestID: func() string { return requestID },
		Poll:         10 * time.Millisecond, Wait: 20 * time.Second,
		Exchanges: log, Doorbell: bell,
	}

	var summaries []string
	var closedField, closeErrField any
	res, err := runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "architect this"},
		func(e event.Event) {
			summaries = append(summaries, e.Summary)
			var fields map[string]any
			if jerr := json.Unmarshal(e.Payload, &fields); jerr != nil {
				return
			}
			if fields["outcome"] == "refused" {
				closedField = fields["exchange_closed"]
				closeErrField = fields["exchange_close_error"]
			}
		})

	if bell.rung != 1 {
		t.Fatalf("the doorbell rang %d times, so the record was never blocked", bell.rung)
	}
	// PREMISE: the close really does fail. Without this the test would pass on a
	// store that closed cleanly and prove nothing about the ignored error.
	if cerr := log.Close(architectureBinding().TaskID, requestID); cerr == nil {
		t.Fatal("the exchange record closes cleanly, so this fixture proves nothing")
	}

	if err == nil {
		t.Fatalf("a refused request returned success: %+v", res)
	}
	if res != (agent.Result{}) {
		t.Fatalf("a refusal produced an architecture result: %+v", res)
	}
	// THE TYPED REFUSAL SURVIVES, with the consumer's own diagnostic.
	refused, ok := err.(*ArchitectureRefused)
	if !ok {
		t.Fatalf("a close failure replaced the typed refusal: %v", err)
	}
	if refused.Stage != refusal.Stage || refused.Reason != refusal.Reason ||
		refused.RequestID != requestID || !refused.Binding.Same(architectureBinding()) {
		t.Fatalf("the refusal details did not survive the close failure: %+v", refused)
	}
	// AND THE CLOSE FAILURE IS SURFACED, on the error and to an operator.
	if refused.ExchangeCloseErr == nil {
		t.Fatal("the exchange close error was discarded")
	}
	if !strings.Contains(err.Error(), "could not be closed") {
		t.Errorf("the returned error does not say the record is still open: %v", err)
	}
	if closedField != false {
		t.Errorf("the refused event reported exchange_closed=%v on a failed close", closedField)
	}
	if s, _ := closeErrField.(string); strings.TrimSpace(s) == "" {
		t.Errorf("the refused event carries no exchange_close_error: %v", closeErrField)
	}
	var said bool
	for _, s := range summaries {
		if strings.Contains(s, "exchange record is still open") {
			said = true
		}
	}
	if !said {
		t.Fatalf("no operator-visible event says the exchange is still open: %v", summaries)
	}
	// AND THE RECORD REALLY IS STILL THERE, which is what the next startup sees.
	if _, serr := os.Stat(bell.path); serr != nil {
		t.Fatalf("the blocked record is gone, so nothing was left open: %v", serr)
	}
}
