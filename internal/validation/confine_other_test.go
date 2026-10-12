//go:build !linux

package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Off Linux a correctly pinned wrapper is still refused before anything
// runs: the D2 profile is Linux-only and no unpinned host runner is
// substituted.
func TestConfinementIsRefusedOffLinuxBeforeTheCheckRuns(t *testing.T) {
	body := []byte("wrapper")
	p := filepath.Join(t.TempDir(), "d2-confine")
	if err := os.WriteFile(p, body, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	c := &Confinement{Profile: "d2-test", Command: p, SHA256: hex.EncodeToString(sum[:])}
	e := Runner{Workspace: t.TempDir(), Confine: c}.Run(context.Background(), "task-1", "sha256:abc",
		[]Check{{Kind: Test, Command: "sensei-code-check-that-must-not-run"}}).Checks[0]
	if e.Outcome != Errored || !strings.HasPrefix(e.Detail, SandboxUnavailable) || !strings.Contains(e.Detail, "unavailable on "+runtime.GOOS) {
		t.Fatalf("confinement off Linux was not refused before the check: %+v", e)
	}
}
