//go:build !linux

package validation

import (
	"fmt"
	"os"
	"runtime"
)

// openWrapper refuses: the D2 confinement and the /proc/self/fd execution of
// the pinned wrapper are Linux-only, and nothing unpinned is run instead.
func openWrapper(string) (*os.File, error) {
	return nil, fmt.Errorf("confinement is unavailable on %s: the D2 profile is Linux-only", runtime.GOOS)
}
