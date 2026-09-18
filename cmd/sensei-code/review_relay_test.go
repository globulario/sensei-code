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

// The attestation handler resolves the override's review from the COMMON store.
//
// An override binds to a canonical review, so the one production caller of
// AcceptAttestation must hand it the review store. Passing the relay store here
// would restore the transport/authority split R3 removed, in the one place that
// holds local override authority.
func TestTheAttestationHandlerPassesTheCommonReviewStore(t *testing.T) {
	blob, err := os.ReadFile("review_relay.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(blob)
	start := strings.Index(src, "func attestHandler(")
	if start < 0 {
		t.Fatal("attestHandler was not found; this check proves nothing")
	}
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "Reviews:   runners.Reviews") && !strings.Contains(body, "Reviews: runners.Reviews") {
		t.Errorf("attestHandler does not pass the common review store:\n%s", body)
	}
	if strings.Contains(body, "runners.Relays") {
		t.Errorf("attestHandler still reaches for the relay store:\n%s", body)
	}
}
