//go:build !linux

package workflow

// The execution service's platform primitives, refused off Linux. The
// service relies on Linux peer credentials, descriptor-relative no-follow
// file access and the Linux-only D2 confinement, and no weaker portable
// substitute is provided: an enabled profile is refused before the
// implementer starts (execPlatformSupported), and every primitive below
// refuses if reached anyway.

import (
	"fmt"
	"net"
	"runtime"

	"github.com/globulario/sensei-code/internal/validation"
)

func errExecUnsupported() error {
	return fmt.Errorf("%s: the execution service and its D2 confinement are Linux-only; this is %s", validation.SandboxUnavailable, runtime.GOOS)
}

// execPlatformSupported refuses the execution profile on this platform.
func execPlatformSupported() error { return errExecUnsupported() }

func peerUID(*net.UnixConn) (uint32, error) { return 0, errExecUnsupported() }

func (s *execService) readFile(toolArgs) (any, *toolRefusal) {
	return nil, refuseTool(refuseSandbox, "%v", errExecUnsupported())
}

func (s *execService) applyPatch(toolArgs) (any, *toolRefusal) {
	return nil, refuseTool(refuseSandbox, "%v", errExecUnsupported())
}

func regularFileSum(string) (string, error) { return "", errExecUnsupported() }

func checkPrivateSocket(string) error { return errExecUnsupported() }
