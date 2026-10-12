package validation

import (
	"os"
	"syscall"
)

// openWrapper opens the confinement wrapper once, without following a
// symlink, for Confinement.open to hash and commandFor to execute through
// /proc/self/fd.
func openWrapper(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
