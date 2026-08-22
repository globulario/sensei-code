package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBinding(t *testing.T, repo, body string) {
	t.Helper()
	dir := filepath.Join(repo, ".sensei", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "active.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const boundTask = `architecture_active_task:
    task_id: task.defect.b165d8e2088a
    revision: b571bac2218379a64bc344b2bf05f6851867e1c6
    graph_digest_sha256: eb2a202fe17b8641140772bf78aea177c997fb1e9fd7de95a0a86487c74c9f7f
`

// The defect this exists for: a binding pinned to an old revision made Sensei
// refuse every task briefing, the architect planned without one on every
// governed run, and no check reported it.
func TestAStaleBindingIsReported(t *testing.T) {
	repo := t.TempDir()
	writeBinding(t, repo, boundTask)

	got := checkTaskBinding(repo, "5ae2f27612e91797b039bc978cb5cbc7370da4a7", "eb2a202fe17b8641140772bf78aea177c997fb1e9fd7de95a0a86487c74c9f7f")
	if got.Status != Fail {
		t.Fatalf("a stale revision is %s, not FAIL: %s", got.Status, got.Detail)
	}
	for _, want := range []string{"task.defect.b165d8e2088a", "refuse task briefings", "will not discard it for you"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("the report is missing %q: %s", want, got.Detail)
		}
	}
}

// The graph moves independently of the repository -- republishing the corpus is
// enough -- so it is compared separately.
func TestAMovedGraphIsAlsoStale(t *testing.T) {
	repo := t.TempDir()
	writeBinding(t, repo, boundTask)
	got := checkTaskBinding(repo, "b571bac2218379a64bc344b2bf05f6851867e1c6", "def94857a06a997412c56c682c39481b226f1834f")
	if got.Status != Fail {
		t.Fatalf("a moved graph is %s, not FAIL: %s", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "graph") {
		t.Errorf("the graph drift is not named: %s", got.Detail)
	}
}

func TestAMatchingBindingPasses(t *testing.T) {
	repo := t.TempDir()
	writeBinding(t, repo, boundTask)
	got := checkTaskBinding(repo,
		"b571bac2218379a64bc344b2bf05f6851867e1c6",
		"eb2a202fe17b8641140772bf78aea177c997fb1e9fd7de95a0a86487c74c9f7f")
	if got.Status != Pass {
		t.Fatalf("a matching binding is %s: %s", got.Status, got.Detail)
	}
}

// No bound task is the ordinary state between tasks, and briefings work.
func TestNoBindingIsNotAFailure(t *testing.T) {
	got := checkTaskBinding(t.TempDir(), "abc", "def")
	if got.Status != Pass {
		t.Fatalf("an unbound repository is %s: %s", got.Status, got.Detail)
	}
}

// A binding that exists but cannot be read is not the same fact as no binding.
func TestAnUnreadableBindingIsNotSilentlyAbsent(t *testing.T) {
	repo := t.TempDir()
	writeBinding(t, repo, "architecture_active_task:\n    something_else: 1\n")
	got := checkTaskBinding(repo, "abc", "def")
	if got.Status == Pass {
		t.Fatalf("an unreadable binding passed as if absent: %s", got.Detail)
	}
}

// An unknown comparand skips its comparison rather than claiming drift. A
// checkout without git must not produce a mismatch nobody can reproduce.
func TestUnknownComparandsDoNotClaimDrift(t *testing.T) {
	repo := t.TempDir()
	writeBinding(t, repo, boundTask)
	if got := checkTaskBinding(repo, "", ""); got.Status != Pass {
		t.Fatalf("unknown head and graph produced %s: %s", got.Status, got.Detail)
	}
}
