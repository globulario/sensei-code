package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const proposal = `proposal:
    kind: contract_unknown
    description: |-
        Level-3 authority resolution.
        Certifiability condition: Sensei reported blind spots in the planned region
        Chosen: Preserve current human-owned intent and require another design (option 1)
        Task: task-1787235334698173142
        Decided at: 2026-08-20T14:21:48Z
`

func TestARecordedDecisionReachesTheBrief(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/awareness/candidates/proposals/a.yaml", proposal)

	got := Gather(root, 6)
	if len(got.Decisions) != 1 {
		t.Fatalf("expected one decision, got %d (%v)", len(got.Decisions), got.Unavailable)
	}
	d := got.Decisions[0]
	if d.Task != "task-1787235334698173142" || !strings.Contains(d.Chosen, "Preserve current human-owned intent") {
		t.Fatalf("decision fields not extracted: %+v", d)
	}
	if !strings.Contains(got.Render(), "a condition answered once has been answered") {
		t.Error("the brief does not say that a recorded decision binds")
	}
}

// Half a decision is worse than none: an architect told the condition but not
// the choice, or the choice but not which task it was about, would plan against
// a decision nobody made.
func TestAnIncompleteDecisionIsReportedNotGuessed(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/awareness/candidates/proposals/broken.yaml",
		"proposal:\n    description: |-\n        Chosen: something\n        Task: task-1\n")

	got := Gather(root, 6)
	if len(got.Decisions) != 0 {
		t.Fatalf("a decision missing its condition was accepted: %+v", got.Decisions)
	}
	if len(got.Unavailable) == 0 {
		t.Fatal("an unreadable decision file is silently skipped")
	}
	if !strings.Contains(got.Render(), "not read —") {
		t.Error("the brief does not say a record could not be read")
	}
}

// An audit is a point-in-time finding. Rendering it as a live defect list would
// have an architect plan around bugs that are already fixed -- which is the
// state of most findings in this repository's own audit.
func TestAnAuditIsLabelledAsPointInTime(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/audit/2026-08-21-architecture-audit.md",
		"# Audit\n\n## Findings\n\n### 1. [High · Confirmed] A deferred publication decision is recorded as standing\nbody text\n### 2. [Low] Something else\n")

	got := Gather(root, 6)
	if len(got.Audits) != 1 || len(got.Audits[0].Findings) != 2 {
		t.Fatalf("findings not extracted: %+v", got.Audits)
	}
	if strings.Contains(strings.Join(got.Audits[0].Findings, " "), "body text") {
		t.Error("the finding body was pulled in; only headings belong in a brief")
	}
	out := got.Render()
	for _, want := range []string{"AS RECORDED THEN", "not a live defect list", "may since have been fixed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the audit section is missing %q, so it reads as current state", want)
		}
	}
}

// A bounded brief must never read as a complete one.
func TestTruncationSaysSo(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		write(t, root, "docs/awareness/candidates/proposals/"+n+".yaml", proposal)
	}
	got := Gather(root, 2)
	if len(got.Decisions) != 2 {
		t.Fatalf("limit not applied: %d", len(got.Decisions))
	}
	if !strings.Contains(got.Render(), "more exist than are shown here") {
		t.Error("a truncated corpus does not say it was truncated")
	}
}

// Nothing recorded and nothing readable are different facts.
func TestAbsentCorporaAreNotAFailure(t *testing.T) {
	got := Gather(t.TempDir(), 6)
	if !got.Empty() {
		t.Fatalf("an empty repository reported content: %+v", got)
	}
	if len(got.Unavailable) != 0 {
		t.Fatalf("missing directories were reported as failures: %v", got.Unavailable)
	}
}

// Against this repository's own corpora, which is what the architect will read.
func TestThisRepositorysOwnRecordsParse(t *testing.T) {
	got := Gather("../..", 6)
	if len(got.Unavailable) != 0 {
		t.Fatalf("this repository's own records did not parse: %v", got.Unavailable)
	}
	if len(got.Decisions) == 0 {
		t.Fatal("no authority decision was read from this repository")
	}
	if len(got.Audits) == 0 {
		t.Fatal("no audit was read from this repository")
	}
	t.Logf("read %d decision(s) and %d audit(s)", len(got.Decisions), len(got.Audits))
}
