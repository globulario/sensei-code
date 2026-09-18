// Package governedfile is how a governed local record reaches disk.
//
// It owns ONE mechanism and no semantic fact: the posture and the durability of
// a write, not what the bytes mean. Nothing here decides authority, and nothing
// gains authority by being written through it.
//
// It exists because four stores that carry governance -- the review record, the
// owner attestation, the review obligation and the task state -- had four
// different answers to the same question, and two of them had no answer at all:
//
//	reviewstore    0700/0600, temp file then rename
//	attestations   0700/0600, temp file then rename
//	exchanges      0700/0600, WriteFile straight onto the target
//	task state     0755/0644, WriteFile straight onto the target
//
// Two ad-hoc copies of the same four lines, and the two stores WITHOUT them
// include the obligation owner -- the component #182 R4 made the single
// authority on what review is owed. A crash between truncate and write leaves a
// truncated obligation, which R4 reads as unreadable and fails closed on: safe,
// and avoidable.
//
// Consolidated rather than duplicated twice more. Adding a third and fourth
// copy of a mechanism is how a repository accumulates parallel guards that
// agree until one of them is edited (sensei-code#184).
package governedfile

import (
	"errors"
	"os"
	"path/filepath"
)

const (
	// DirMode is the posture of a directory holding governed records.
	DirMode os.FileMode = 0o700
	// FileMode is the posture of one governed record.
	//
	// Owner-only, and that is the INTEGRITY boundary this repository already
	// relies on: every governed store is writable by exactly one local user,
	// the one running the process. Confidentiality is the same decision made
	// once rather than four times, so a reader cannot conclude from the modes
	// that one store is protected more carefully than the authority inputs it
	// depends on.
	FileMode os.FileMode = 0o600
)

// Replace writes bytes to path so a reader sees either the previous content or
// the new content, never a half-written record.
//
// Temp file in the SAME directory then rename: rename is atomic within a
// filesystem, and a temp file elsewhere would cross a device boundary and
// degrade to a copy, which is the thing being avoided.
//
// The temp file is removed on any failure, so a failed write leaves no debris a
// later reader could mistake for a record.
func Replace(path string, body []byte) error {
	if path == "" {
		return errors.New("a governed record needs a path")
	}
	if err := os.MkdirAll(filepath.Dir(path), DirMode); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, FileMode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Create writes a record that must NOT already exist, and reports whether it
// won the creation.
//
// O_EXCL, because for a record whose identity is decided at creation -- one
// canonical review per request -- "who got there first" is the semantic
// question, and a caller that overwrote instead would silently replace a
// review. Losing the race is not an error: the caller converges on what is
// already there.
func Create(path string, body []byte) (bool, error) {
	if path == "" {
		return false, errors.New("a governed record needs a path")
	}
	if err := os.MkdirAll(filepath.Dir(path), DirMode); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, FileMode)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return false, err
	}
	return true, f.Close()
}
