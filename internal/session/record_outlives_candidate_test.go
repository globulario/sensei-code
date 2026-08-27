package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The execution record outlives the disposable workspace (issue #82).
//
// A governed run does its work in a candidate worktree that is removed when
// the run ends. If the session store lived there, the run's own event stream
// would go with it -- which is what #82 found: the first end-to-end
// self-repair was historically evidenced and not replayable. The store is
// keyed to the CANONICAL checkout, and every entry point constructs it there.
func TestTheExecutionRecordIsKeyedToTheCanonicalCheckout(t *testing.T) {
	root := t.TempDir()
	s, err := New(root, "session-x")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".sensei-code", "sessions", "session-x", "events.jsonl")
	if s.path != want {
		t.Fatalf("store at %s, want %s", s.path, want)
	}
	for _, rel := range []string{
		"internal/workflow/engine.go", "cmd/sensei-code/run.go",
		"cmd/sensei-code/main.go", "cmd/sensei-code/auditrepair.go",
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		i := strings.Index(src, "session.New(")
		if i < 0 {
			t.Fatalf("%s no longer constructs a session store", rel)
		}
		call := src[i : i+60]
		if !strings.Contains(call, ".Root") {
			t.Fatalf("%s constructs the store somewhere other than the repository root: %s", rel, call)
		}
		if strings.Contains(call, "workspace") || strings.Contains(call, "Worktree") {
			t.Fatalf("%s keys the store to a workspace: %s", rel, call)
		}
	}
}
