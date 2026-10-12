//go:build !linux

package workflow

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/validation"
)

// Off Linux an enabled execution profile is refused before the implementer
// starts, so no unconfined implementation can follow; disabled is unchanged.
func TestN3AnEnabledProfileIsRefusedOffLinux(t *testing.T) {
	runner := agent.CLI{Name: "claude", Command: "claude"}
	off := &Engine{Config: config.Default()}
	if svc, err := off.startImplementerExec(context.Background(), "task-1", t.TempDir(), runner); svc != nil || err != nil {
		t.Fatalf("the disabled profile changed: %v %v", svc, err)
	}
	on := &Engine{Config: config.Default()}
	on.Config.ExecBinding.Enabled = true
	svc, err := on.startImplementerExec(context.Background(), "task-1", t.TempDir(), runner)
	if svc != nil || err == nil || !strings.HasPrefix(err.Error(), validation.SandboxUnavailable) {
		t.Fatalf("an enabled profile was not refused off Linux: %v %v", svc, err)
	}
	if err := ExecShim(context.Background(), "/nonexistent/exec.sock", "svc", strings.NewReader(""), io.Discard); err == nil ||
		!strings.Contains(err.Error(), validation.SandboxUnavailable) {
		t.Fatalf("the exec shim did not refuse off Linux: %v", err)
	}
	var s execService
	for name, call := range map[string]func(toolArgs) (any, *toolRefusal){"read_file": s.readFile, "apply_patch": s.applyPatch} {
		if _, r := call(nil); r == nil || r.Code != refuseSandbox {
			t.Errorf("%s was not refused off Linux: %v", name, r)
		}
	}
}
