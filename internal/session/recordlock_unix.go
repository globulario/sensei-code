//go:build unix

package session

import (
	"os"

	"golang.org/x/sys/unix"
)

// tryLockRecordFile makes ONE attempt at the exclusive OS lock on the lock
// file at path: flock(2), non-blocking, through a descriptor of its own. The
// record lock is taken on a lock file of its own beside the record
// (recordLockPath), never on the record, so it exists -- and an absent record
// is examined under it -- without creating or touching any task history.
// held=false means another holder -- in this process through another
// descriptor, or in any other process -- has it now; lockRecordFile decides
// how long the caller may wait. The lock is the kernel's, not the file's: no
// durable state is ever a locked state, and the kernel releases the lock
// when release runs or when the holding process dies, however it dies, so no
// crash leaves the record permanently non-appendable.
func tryLockRecordFile(path string) (release func(), held bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err != unix.EINTR {
			break
		}
	}
	switch {
	case err == unix.EWOULDBLOCK:
		f.Close()
		return nil, false, nil
	case err != nil:
		f.Close()
		return nil, false, &os.PathError{Op: "flock", Path: f.Name(), Err: err}
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, true, nil
}
