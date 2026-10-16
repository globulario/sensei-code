//go:build !unix

package candidate

// crossProcessLock reports that this platform has no cross-process
// serialization of a candidate record. Ordinary record writes keep their prior
// in-process contract here; the required confined execution, whose safety
// depends on the cross-process guarantee, refuses (BeginExecution) rather than
// substitute the process-local mutex for it (RULING-360 §14).
const crossProcessLock = false

// lockRecord takes no cross-process lock on this platform.
func lockRecord(string) (func(), error) { return func() {}, nil }
