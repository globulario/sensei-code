package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The only production caller of ghbridge.AcceptRelayedReview is the control
// process's relay handler, which receives its principal from the relay socket's
// authority decision. A second caller -- the engine, a runner, a resume path --
// would be a way for the orchestrator to accept a review nobody relayed.
func TestOnlyTheRelaySocketHandlerAcceptsARelayedReview(t *testing.T) {
	root := filepath.Join("..", "..")
	var callers []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".sensei-code", ".sensei-worktrees", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		blob, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(blob), "AcceptRelayedReview(") {
			callers = append(callers, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"../../cmd/sensei-code/review_relay.go": true, "../../internal/ghbridge/relay.go": true}
	if len(callers) != len(want) {
		t.Fatalf("AcceptRelayedReview appears in %v, want exactly the relay handler and its definition", callers)
	}
	for _, c := range callers {
		if !want[c] {
			t.Fatalf("%s reaches AcceptRelayedReview; only the relay socket handler may", c)
		}
	}
}
