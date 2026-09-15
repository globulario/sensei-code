package workflow

import (
	"context"
	"errors"
	"testing"
)

var (
	errNotThisWorld = errors.New("wrong world")
	errAbsentHere   = errors.New("does not exist in the given revision")
)

// The W3 case itself, end to end through the repaired predicate: control.go governed by
// its authored invariant grants control_test.go, which shares its package.
func TestW3ControlTestIsGrantedByItsAuthoredNeighbour(t *testing.T) {
	const (
		world = "9d0ec1671c1a000000000000000000000000000"
		S     = "cmd/sensei-code/control.go"
		F     = "cmd/sensei-code/control_test.go"
		inv   = "sensei_code.ghwebhook.ingress_and_control_surfaces_stay_separate"
	)
	files := map[string]string{
		S: "package main\n\nimport \"fmt\"\n\nfunc runControlSurface() { fmt.Println(\"x\") }\n",
		F: "package main\n\nimport \"testing\"\n\nfunc TestTheReviewerDeadlineHasOneOwner(t *testing.T) {}\n",
	}
	authored := authoredEvidence{World: world, ByFile: map[string][]string{S: {inv}}}
	grants, reasons := testEditGrants(context.Background(), world, []string{S, F}, nil, authored, func(_ context.Context, w, f string) ([]byte, error) {
		if w != world {
			return nil, errNotThisWorld
		}
		src, ok := files[f]
		if !ok {
			return nil, errAbsentHere
		}
		return []byte(src), nil
	})
	if len(grants) != 1 {
		t.Fatalf("control_test.go was not granted: %v", reasons)
	}
	g := grants[0]
	if g.Path != F || g.Covering != S {
		t.Fatalf("grant = %+v", g)
	}
	if g.CoveringEvidence != evidenceAuthored {
		t.Errorf("evidence class = %q, want authored", g.CoveringEvidence)
	}
	if len(g.CoveringIdentity) != 1 || g.CoveringIdentity[0] != inv {
		t.Errorf("evidence identity = %v, want the exact invariant id", g.CoveringIdentity)
	}
	if g.Facts.Package != "main" {
		t.Errorf("package relationship not recorded: %+v", g.Facts)
	}
	t.Logf("GRANTED  F=%s  S=%s  evidence=%s  identity=%s  world=%s  package=%s",
		g.Path, g.Covering, g.CoveringEvidence, g.CoveringIdentity[0], g.World[:12], g.Facts.Package)
}
