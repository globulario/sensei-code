//go:build acceptance

package workflow

// The specimen, against the real deployment.
//
//	go test -tags acceptance ./internal/workflow/ -run TestUnexaminedFilesAgainstTheLiveGraph -v
//
// The unit test pins the router on a recorded answer. This asks the graph the
// region question and the per-file question for real, so the two facts the
// repair rests on -- the region is proven by one file; the other file is
// unexamined on its own -- are observed rather than assumed.

import (
	"context"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/sensei"
)

func TestUnexaminedFilesAgainstTheLiveGraph(t *testing.T) {
	root := liveRepoRoot(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sc, err := sensei.Start(context.Background(), root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		t.Fatalf("start Sensei: %v", err)
	}
	defer sc.Close()

	const domain = "github.com/globulario/sensei-code"
	anchored, ghost := "internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"
	preflight := func(files ...string) sensei.PreflightDecision {
		t.Helper()
		result, err := sc.CallTool("awareness_preflight", map[string]any{
			"task": "edit these files", "files": files, "domain": domain, "mode": "compact"})
		if err != nil {
			t.Fatalf("preflight %v: %v", files, err)
		}
		d, err := sensei.DecodePreflight(result)
		if err != nil {
			t.Fatalf("decode preflight %v: %v", files, err)
		}
		return d
	}

	region := preflight(anchored, ghost)
	t.Logf("region: status=%s sufficient=%v anchors=%d files=%d indexed=%d",
		region.Status, region.Coverage.Sufficient, region.Coverage.DirectAnchorCount, region.Coverage.FileCount, region.Coverage.IndexedFileCount)
	if !region.Coverage.Proven() {
		t.Skip("the live graph no longer proves the region by one file; the specimen's premise is gone and the router's per-file fact is not exercised here")
	}
	if region.Coverage.IndexedFileCount >= region.Coverage.FileCount {
		t.Fatalf("the region claims every planned file examined, including one that does not exist: %+v", region.Coverage)
	}

	var start certifiedStart
	start.workspace.Binding.RepositoryDomain = domain
	got := (&Engine{}).unexaminedFiles(sc, start, "edit these files", []string{anchored, ghost}, region)
	if len(got) != 1 || got[0] != ghost {
		t.Fatalf("unexamined = %v, want only %s", got, ghost)
	}

	// And the router, on the live region answer plus the live per-file fact.
	routed := routeAuthorityForAction(region, nil, Action{
		Stage: StageCandidateEdit, Files: []string{anchored, ghost}, Unexamined: got})
	if routed.Granted() || !routed.ClosesGap() {
		t.Fatalf("the live region granted a file the graph never examined: %+v", routed)
	}
}
