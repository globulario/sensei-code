package roles

import "testing"

const (
	architectureBase  = "1111111111111111111111111111111111111111"
	architectureGraph = "2222222222222222222222222222222222222222"
)

func TestArchitectureBindingNamesExactObjectiveAndWorld(t *testing.T) {
	b := BindArchitecture("task-7", "fix exactly this\n", architectureBase, architectureGraph)
	if !b.Valid() {
		t.Fatalf("binding is not valid: %+v", b)
	}
	if err := b.CheckObjective("fix exactly this\n"); err != nil {
		t.Fatalf("exact objective refused: %v", err)
	}
	if err := b.CheckObjective("fix exactly this"); err == nil {
		t.Fatal("normalizing the objective changed its bytes without invalidating the binding")
	}
}

func TestArchitectureBindingRequiresEveryReferent(t *testing.T) {
	good := BindArchitecture("task-7", "objective", architectureBase, architectureGraph)
	cases := []ArchitectureBinding{
		{ObjectiveDigest: good.ObjectiveDigest, BaseSHA: architectureBase, GraphBuildCommit: architectureGraph},
		{TaskID: good.TaskID, BaseSHA: architectureBase, GraphBuildCommit: architectureGraph},
		{TaskID: good.TaskID, ObjectiveDigest: good.ObjectiveDigest, GraphBuildCommit: architectureGraph},
		{TaskID: good.TaskID, ObjectiveDigest: good.ObjectiveDigest, BaseSHA: architectureBase},
	}
	for i, b := range cases {
		if b.Valid() {
			t.Errorf("case %d accepted an incomplete binding: %+v", i, b)
		}
	}
}
