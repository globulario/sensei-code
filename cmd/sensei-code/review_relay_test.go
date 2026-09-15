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
	assertSoleCallers(t, "AcceptRelayedReview(",
		"../../cmd/sensei-code/review_relay.go", "../../internal/ghbridge/relay.go")
}

// The same for a human override: only the attestation socket handler records
// one. A second caller would be a way for the orchestrator to override the
// obligation it is subject to.
func TestOnlyTheAttestationSocketHandlerRecordsAnOverride(t *testing.T) {
	assertSoleCallers(t, "AcceptAttestation(",
		"../../cmd/sensei-code/review_relay.go", "../../internal/ghbridge/attestation.go")
}

func assertSoleCallers(t *testing.T, symbol string, allowed ...string) {
	t.Helper()
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
		if strings.Contains(string(blob), symbol) {
			callers = append(callers, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, a := range allowed {
		want[a] = true
	}
	if len(callers) != len(want) {
		t.Fatalf("%s appears in %v, want exactly %v", symbol, callers, allowed)
	}
	for _, c := range callers {
		if !want[c] {
			t.Fatalf("%s reaches %s; only its socket handler may", c, symbol)
		}
	}
}
