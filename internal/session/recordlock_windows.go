package session

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockRecordFile makes ONE attempt at the exclusive OS lock on the lock
// file at path: LockFileEx with LOCKFILE_FAIL_IMMEDIATELY on one byte far
// beyond any offset the file reaches. The record lock is taken on a lock file
// of its own beside the record (recordLockPath), never on the record, so it
// exists -- and an absent record is examined under it -- without creating or
// touching any task history. held=false means another handle -- in
// this process or any other -- holds it now; lockRecordFile decides how long
// the caller may wait. The system releases the lock when release runs or when
// the holding process dies, so no crash leaves the record permanently
// non-appendable.
func tryLockRecordFile(path string) (release func(), held bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	h := windows.Handle(f.Fd())
	ol := &windows.Overlapped{Offset: 0xFFFFFFFE, OffsetHigh: 0x7FFFFFFF}
	err = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	switch {
	case err == windows.ERROR_LOCK_VIOLATION:
		f.Close()
		return nil, false, nil
	case err != nil:
		f.Close()
		return nil, false, fmt.Errorf("the record lock %s could not be taken: %w", f.Name(), err)
	}
	return func() {
		_ = windows.UnlockFileEx(h, 0, 1, 0, ol)
		f.Close()
	}, true, nil
}
