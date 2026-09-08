package control

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// The authorized submission handshake.
//
// A caller that holds an at-most-once token of its own — the GitHub objective
// proposal's approval receipt — must be able to learn the authority verdict
// BEFORE it spends that token. Before this handshake the only way to learn the
// verdict was to submit, so a caller who was never going to be authorized had
// already spent the token by the time it was refused.
//
// What must NOT change while making that possible: the judgement itself, where
// it happens, and how many times. These pin that.

// refusedPeer is what the kernel reports for a same-UID process with no
// controlling terminal — the exact shape that motivated this repair.
func noTerminalPeer(net.Conn) (peer, error) {
	return peer{PID: 5150, UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Terminal: 0}, nil
}

// governedDescendantPeer is a worker this orchestrator launched.
func governedDescendantPeer(net.Conn) (peer, error) {
	return peer{PID: 5151, UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Terminal: 34816, Descendant: true}, nil
}

// THE ORDERING PROPERTY. A refused caller is refused at DIAL, before it has
// been given any opportunity to act, and the engine never sees an objective.
// Short name deliberately: t.TempDir() embeds the full subtest name in the
// Unix socket path, and sun_path is ~108 bytes. A descriptive name here binds
// nothing and reports a bind error instead of an authority result.
func TestUnauthorizedIsRefusedAtDial(t *testing.T) {
	for name, observe := range map[string]func(net.Conn) (peer, error){
		"no terminal": noTerminalPeer,
		"descendant":  governedDescendantPeer,
	} {
		t.Run(name, func(t *testing.T) {
			h := newLocalHarnessWithPeer(t, observe)

			auth, err := DialLocalObjective(h.root)
			if err == nil {
				_ = auth.Close()
				t.Fatal("an unauthorized caller was handed an authorized connection")
			}
			if auth != nil {
				t.Error("a refused dial still returned a connection")
			}
			if len(h.submitted) != 0 {
				t.Errorf("the engine received %d objectives from a refused caller", len(h.submitted))
			}
		})
	}
}

// The judgement happens ONCE for the connection. Readiness reports it; it does
// not defer, repeat, or re-open it. A second evaluation would mean the verdict
// a caller acted on and the verdict that admitted its objective could differ.
func TestTheAuthorityJudgementHappensOncePerConnection(t *testing.T) {
	var judged atomic.Int64
	h := newLocalHarnessWithPeer(t, func(c net.Conn) (peer, error) {
		judged.Add(1)
		return operatorPeer(c)
	})

	auth, err := DialLocalObjective(h.root)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer auth.Close()
	if got := judged.Load(); got != 1 {
		t.Fatalf("authority evaluated %d times at readiness, want 1", got)
	}

	if _, err := auth.Submit("an objective placed after readiness"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := judged.Load(); got != 1 {
		t.Fatalf("authority evaluated %d times across the whole exchange, want exactly 1", got)
	}
	if len(h.submitted) != 1 || h.submitted[0] != "an objective placed after readiness" {
		t.Fatalf("engine saw %q", h.submitted)
	}
}

// Readiness genuinely precedes the objective: a caller can do arbitrary work in
// between and still submit on the same connection.
func TestAnAuthorizedCallerMaySubmitAfterActingOnReadiness(t *testing.T) {
	h := newLocalHarness(t)

	auth, err := DialLocalObjective(h.root)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer auth.Close()
	if auth.Workspace() != testWorkspace {
		t.Errorf("readiness named workspace %q, want %q", auth.Workspace(), testWorkspace)
	}

	// The durable step a real caller takes here.
	scratch := t.TempDir() + "/receipt"
	if err := os.WriteFile(scratch, []byte("attempting"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	accepted, err := auth.Submit("format three files with gofmt")
	if err != nil {
		t.Fatalf("submit after acting on readiness: %v", err)
	}
	if accepted.TaskID == "" {
		t.Error("acceptance named no task")
	}
}

// One objective per authorized connection. A second would block on a reply
// nobody is going to send; refusing is the honest version of that.
func TestAnAuthorizedConnectionCarriesOneObjective(t *testing.T) {
	h := newLocalHarness(t)
	auth, err := DialLocalObjective(h.root)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer auth.Close()

	if _, err := auth.Submit("the first objective"); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, err := auth.Submit("the second objective"); err == nil {
		t.Fatal("a second objective was accepted on one authorized connection")
	}
	if len(h.submitted) != 1 {
		t.Fatalf("engine saw %d objectives, want 1", len(h.submitted))
	}
}

// The ordinary one-shot path is unchanged for callers with nothing to do in
// between, and is written in terms of the handshake so the two cannot drift.
func TestOrdinaryLocalObjectiveSubmissionStillWorks(t *testing.T) {
	h := newLocalHarness(t)

	const exact = "  an ordinary objective  "
	accepted, err := SubmitLocalObjective(h.root, exact)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if accepted.TaskID == "" || accepted.Workspace != testWorkspace {
		t.Fatalf("acceptance = %+v", accepted)
	}
	if len(h.submitted) != 1 || h.submitted[0] != exact {
		t.Fatalf("engine saw %q; the objective was not forwarded unchanged", h.submitted)
	}

	// And it still refuses through the same judgement.
	refused := newLocalHarnessWithPeer(t, noTerminalPeer)
	if _, err := SubmitLocalObjective(refused.root, "an objective"); err == nil {
		t.Fatal("the one-shot path accepted an unauthorized caller")
	} else if !errorMentions(err, "controlling terminal") {
		t.Errorf("the refusal does not name the reason: %v", err)
	}
	if len(refused.submitted) != 0 {
		t.Error("a refused one-shot submission reached the engine")
	}
}

func errorMentions(err error, want string) bool {
	return err != nil && strings.Contains(err.Error(), want)
}

// With no owner running, the caller is told how to start one — the refusal
// still arrives at dial rather than after a durable step.
func TestDialingWithNoOwnerRunningRefusesAtDial(t *testing.T) {
	if _, err := DialLocalObjective(t.TempDir()); err == nil {
		t.Fatal("dialling a socket nobody serves succeeded")
	} else if !errorMentions(err, "sensei-code control") {
		t.Errorf("the refusal does not say how to start an owner: %v", err)
	}
}

// mayOriginateObjective is still the judgement, and it is not substituted by
// any of the above.
func TestTheJudgementItselfIsUnchanged(t *testing.T) {
	self := uint32(os.Getuid())
	if err := mayOriginateObjective(peer{PID: 1, UID: self, Terminal: 34816}, self); err != nil {
		t.Errorf("an operator was refused: %v", err)
	}
	if err := mayOriginateObjective(peer{PID: 2, UID: self, Terminal: 0}, self); err == nil {
		t.Error("a caller with no controlling terminal was allowed")
	}
	if err := mayOriginateObjective(peer{PID: 3, UID: self, Terminal: 34816, Descendant: true}, self); err == nil {
		t.Error("a governed descendant was allowed")
	}
	if err := mayOriginateObjective(peer{PID: 4, UID: self + 1, Terminal: 34816}, self); err == nil {
		t.Error("another uid was allowed")
	}
}

// ---------------------------------------------------- exact objective bytes

// The bytes an operator places are the bytes the engine records.
//
// A GitHub objective proposal hashes the bytes it stored. If this channel
// delivered a trimmed version, the recorded objective and the digest that
// claims to name it would be two different strings — and every architecture
// envelope built on that digest would be perfectly valid and perfectly wrong.
// The channel therefore VALIDATES without NORMALIZING: an objective that is
// only whitespace says nothing and is refused; an objective that merely has
// whitespace around it is these bytes and no others.
func TestTheChannelCarriesExactObjectiveBytes(t *testing.T) {
	exact := []string{
		"  exact objective bytes  \n",
		"\n\nleading and trailing newlines\n\n",
		"\ttab indented objective\t",
		"trailing space ",
		" leading space",
		"interior  double  spaces kept",
		"unicode  café ✅  padded  ",
	}
	for _, want := range exact {
		h := newLocalHarness(t)
		if _, err := SubmitLocalObjective(h.root, want); err != nil {
			t.Fatalf("submit %q: %v", want, err)
		}
		if len(h.submitted) != 1 {
			t.Fatalf("%q: engine saw %d objectives", want, len(h.submitted))
		}
		if got := h.submitted[0]; got != want {
			t.Errorf("the channel altered the objective:\n got  %q\n want %q", got, want)
		}
	}
}

// Only-whitespace still says nothing, and is refused at both ends.
func TestAnAllWhitespaceObjectiveIsStillRefused(t *testing.T) {
	for _, blank := range []string{"", "   ", "\n", "\t\n  \r\n"} {
		h := newLocalHarness(t)

		// The client refuses without troubling the server.
		if _, err := SubmitLocalObjective(h.root, blank); err == nil {
			t.Fatalf("the client accepted %q as an objective", blank)
		}
		// And the server refuses it on the wire, for a caller that skips the
		// client. json marshalling of the raw value keeps the bytes exact.
		raw, err := json.Marshal(LocalSubmission{Task: blank})
		if err != nil {
			t.Fatal(err)
		}
		if err := rawLocal(t, h.root, string(raw)); err == nil {
			t.Fatalf("the server accepted %q as an objective", blank)
		}
		if len(h.submitted) != 0 {
			t.Fatalf("%q reached the engine: %q", blank, h.submitted)
		}
	}
}
