package continuity

import (
	"strings"
	"testing"
	"time"
)

// A conversation that cannot be resumed must say so. An architect that silently
// lost the thread answers exactly like one that still has it, which is why the
// loss has to be a stated result rather than an absence.
func TestALostThreadIsStatedAndSpecific(t *testing.T) {
	recorded := Thread{Architect: "chatgpt", ThreadID: "thread-1", BaseSHA: "aaa111"}

	switched := recorded.Continues("claude", "aaa111")
	if switched.State != Reconstructed {
		t.Fatalf("switching architect kept the thread: %+v", switched)
	}
	if !strings.Contains(switched.Reason, "chatgpt") || !strings.Contains(switched.Reason, "claude") {
		t.Errorf("the reason does not name what changed: %q", switched.Reason)
	}

	noHandle := Thread{Architect: "chatgpt"}.Continues("chatgpt", "aaa111")
	if noHandle.State != Reconstructed {
		t.Fatalf("a conversation with no resumable handle claimed continuity: %+v", noHandle)
	}
	if strings.TrimSpace(noHandle.Reason) == "" {
		t.Error("continuity was reported lost with no reason, which teaches a person to ignore it")
	}

	fresh := Thread{}.Continues("chatgpt", "aaa111")
	if fresh.State != Started {
		t.Fatalf("an unrecorded conversation was not reported as new: %+v", fresh)
	}
}

// Continuity survives an ordinary turn, and a repository that moved underneath
// the conversation is reported without being called a loss of continuity.
func TestContinuedConversationReportsAMovedBase(t *testing.T) {
	recorded := Thread{Architect: "chatgpt", ThreadID: "thread-1", BaseSHA: "aaa111"}

	same := recorded.Continues("chatgpt", "aaa111")
	if same.State != Continued || same.BaseMoved {
		t.Fatalf("an unchanged conversation was not continued: %+v", same)
	}

	moved := recorded.Continues("chatgpt", "bbb222")
	if moved.State != Continued {
		t.Fatalf("a moved base broke continuity, which it does not: %+v", moved)
	}
	if !moved.BaseMoved || moved.PriorBase != "aaa111" {
		t.Fatalf("the moved base was not reported: %+v", moved)
	}
	if !strings.Contains(moved.Describe(), "advanced") {
		t.Errorf("the description does not tell the architect the ground moved: %q", moved.Describe())
	}
}

// The record carries identity, never architectural content. A local file that
// could hold a decision would be a second, weaker governance store, and the
// first time it disagreed with Sensei the disagreement would be invisible.
func TestTheRecordCannotCarryGovernanceContent(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	saved := Thread{}.Record("chatgpt", "thread-1", "aaa111", now)
	if err := saved.Save(root); err != nil {
		t.Fatal(err)
	}
	back := Load(root)
	if back.Architect != "chatgpt" || back.ThreadID != "thread-1" || back.BaseSHA != "aaa111" {
		t.Fatalf("identity did not survive the record: %+v", back)
	}
	if back.Turns != 1 {
		t.Errorf("turns = %d, want 1", back.Turns)
	}

	// An empty handle must not erase a known one: providers report a session
	// only on some turns, and forgetting it would manufacture a loss that did
	// not happen.
	again := back.Record("chatgpt", "", "aaa111", now)
	if again.ThreadID != "thread-1" {
		t.Errorf("a turn that reported no handle discarded the recorded one: %+v", again)
	}
	if again.Turns != 2 {
		t.Errorf("turns = %d, want 2", again.Turns)
	}
}

// Losing the file costs continuity and nothing else. A missing or corrupt
// record must reconstruct rather than fail a turn, because continuity is not
// correctness.
func TestAnUnreadableRecordReconstructsRatherThanFails(t *testing.T) {
	root := t.TempDir()
	got := Load(root)
	if got.Architect != "" {
		t.Fatalf("a missing record produced an identity: %+v", got)
	}
	if r := got.Continues("chatgpt", "aaa111"); r.State != Started {
		t.Fatalf("a missing record did not start a conversation: %+v", r)
	}
}
