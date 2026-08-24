package finding

import "testing"

// An inference cannot become repair work.
//
// The first attack #73 names, and the reason this package classifies rather
// than compares: an observer reasoning its way to a conclusion is not a reason
// to edit the repository, and everything downstream treats a task as a task.
func TestAnInferenceCannotBecomeRepairWork(t *testing.T) {
	f := New("task-1", "abc123", "audit x", "these packages could be merged safely", "internal/", []string{"internal/a.go"}, "inference")
	if f.Eligible() {
		t.Fatal("an inference-only finding was eligible to become a change objective")
	}
	// It is still retained and still reportable. Ineligible is not discarded.
	if f.Statement == "" || f.ID == "" {
		t.Fatal("an ineligible finding was dropped rather than retained")
	}
}

// Unknown provenance fails closed, and this is the property the package exists
// for: a neighbouring classifier read only one bad value and let every other
// unrecognised one through.
func TestUnknownProvenanceFailsClosed(t *testing.T) {
	for _, src := range []string{"", "assumed", "reasoning", "model_certified", "REPOSITORY_ISH", "inferred", "  "} {
		f := New("task-1", "abc", "audit", "a statement", "somewhere", []string{"a.go"}, src)
		if f.Source.EvidenceBearing() {
			t.Errorf("source %q was treated as evidence-bearing", src)
		}
		if f.Eligible() {
			t.Errorf("source %q produced repair work", src)
		}
	}
	// And the two that are evidence do proceed, or the gate is just "refuse".
	for _, src := range []string{"repository", "Repository", "  graph  "} {
		f := New("task-1", "abc", "audit", "a statement", "somewhere", []string{"a.go"}, src)
		if !f.Eligible() {
			t.Errorf("source %q could not become repair work", src)
		}
	}
}

// Unrecognised is the zero value, so an unset source is never evidence.
func TestTheZeroProvenanceIsUnrecognised(t *testing.T) {
	var p Provenance
	if p != Unrecognised || p.EvidenceBearing() {
		t.Fatalf("the zero provenance is %v and evidence-bearing=%v", p, p.EvidenceBearing())
	}
	if (Finding{}).Eligible() {
		t.Fatal("a zero finding was eligible")
	}
}

// A finding that names nowhere cannot be repaired, because nothing can be
// re-checked.
func TestAFindingWithNoFilesIsNotWork(t *testing.T) {
	if New("t", "w", "o", "something is wrong", "", nil, "repository").Eligible() {
		t.Fatal("a finding naming no files became repair work")
	}
}

// Identity is stable for the same claim at the same revision, so re-running an
// audit does not manufacture new work, and moves when the world moves.
func TestFindingIdentityIsStablePerWorld(t *testing.T) {
	a := New("task-1", "world-1", "obj", "s", "a", []string{"x.go"}, "repository")
	b := New("task-2", "world-1", "different objective", "s", "a", []string{"x.go"}, "repository")
	if a.ID != b.ID {
		t.Fatal("the same claim at the same revision produced two identities")
	}
	c := New("task-1", "world-2", "obj", "s", "a", []string{"x.go"}, "repository")
	if a.ID == c.ID {
		t.Fatal("a finding kept its identity across revisions; a repair must re-check the current world")
	}
}

// The repair objective states the defect and NOT the fix.
//
// If the objective carried the answer, the experiment would prove only that an
// agent can apply a patch it was handed.
func TestTheRepairObjectiveDoesNotSupplyTheFix(t *testing.T) {
	f := New("task-1", "abc123", "audit the router",
		"only the literal source inference is treated as unverified", "authority.go",
		[]string{"internal/workflow/authority.go"}, "repository")
	obj := f.RepairObjective()
	for _, required := range []string{
		"re-check the CURRENT repository", // the world may have moved
		"change nothing",                  // refusal is allowed
		"No fix has been supplied to you", // the answer is not in the wording
		"EVIDENCE, not authority",         // the finding grants nothing
	} {
		if !contains(obj, required) {
			t.Errorf("the repair objective omits %q:\n%s", required, obj)
		}
	}
}

func contains(h, n string) bool {
	return len(h) >= len(n) && (func() bool {
		for i := 0; i+len(n) <= len(h); i++ {
			if h[i:i+len(n)] == n {
				return true
			}
		}
		return false
	}())
}
