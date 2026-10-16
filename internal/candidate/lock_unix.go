//go:build unix

package candidate

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// crossProcessLock reports that this platform serializes a candidate record's
// read-check-write across processes (RULING-360 §9-§10).
const crossProcessLock = true

// lockRecord takes the exclusive advisory lock of one candidate record. The
// lock object is a dedicated file beside the record (<record>.lock): it is
// never renamed or replaced by a record update (the record is replaced by
// rename; the lock file is not), it is opened without following a symlink,
// and it must be a regular file. Acquisition is bounded by lockWait and fails
// closed. The returned function releases it; the kernel releases it too if
// the process dies holding it.
func lockRecord(lockPath string) (func(), error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the candidate record lock: %w", err)
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("the candidate record lock is not a regular file")
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, fmt.Errorf("lock the candidate record: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errLockTimeout
		}
		time.Sleep(5 * time.Millisecond)
	}
}
