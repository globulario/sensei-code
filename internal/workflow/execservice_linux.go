package workflow

// The Linux implementation of the execution service's platform primitives:
// peer credentials, descriptor-relative no-follow file access, the staged
// atomic write and the private-socket check. They are moved here unchanged
// from execservice.go; execservice_other.go refuses them elsewhere, because
// the service and its D2 confinement are Linux-only.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// execPlatformSupported admits the execution profile: Linux is the platform
// it is built for.
func execPlatformSupported() error { return nil }

// peerUID is the uid of the process at the other end of conn.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return cred.Uid, nil
}

// openDirBeneath opens the directory holding parts' last element, walking
// from the worktree one component at a time and refusing a symlink at any of
// them. The descriptor it returns is the identity every later step uses, so
// a path swapped after the check is never the path written.
func openDirBeneath(root string, parts []string) (int, error) {
	dir, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, c := range parts[:len(parts)-1] {
		next, err := unix.Openat(dir, c, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(dir)
		if err != nil {
			return -1, err
		}
		dir = next
	}
	return dir, nil
}

// readAt reads the regular file name in dir without following a symlink.
func readAt(dir int, name string) (unix.Stat_t, []byte, error) {
	var st unix.Stat_t
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return st, nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if err := unix.Fstat(fd, &st); err != nil {
		return st, nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return st, nil, errors.New("not a regular file")
	}
	if st.Size > maxToolFile {
		return st, nil, fmt.Errorf("larger than %d bytes", maxToolFile)
	}
	body, err := io.ReadAll(io.LimitReader(f, maxToolFile+1))
	return st, body, err
}

func pathRefusal(p string, err error) *toolRefusal {
	switch {
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR):
		return refuseTool(refusePath, "%s passes through a symlink or a non-directory; links are never followed", p)
	case errors.Is(err, unix.ENOENT):
		return refuseTool(refusePath, "%s does not exist in the candidate; these tools never create a file", p)
	}
	return refuseTool(refusePath, "%s cannot be read: %v", p, err)
}

func (s *execService) readFile(args toolArgs) (any, *toolRefusal) {
	if r := args.only("path", "start_line", "max_lines"); r != nil {
		return nil, r
	}
	p, r := args.str("path", true)
	if r != nil {
		return nil, r
	}
	start, r := args.integer("start_line")
	if r != nil {
		return nil, r
	}
	max, r := args.integer("max_lines")
	if r != nil {
		return nil, r
	}
	parts, r := toolPath(p)
	if r != nil {
		return nil, r
	}
	gate := s.e.authorityGate(s.b.TaskID)
	gate.RLock()
	defer gate.RUnlock()
	if _, r := s.revalidate(); r != nil {
		return nil, r
	}
	dir, err := openDirBeneath(s.b.Worktree, parts)
	if err != nil {
		return nil, pathRefusal(p, err)
	}
	defer unix.Close(dir)
	_, body, err := readAt(dir, parts[len(parts)-1])
	if err != nil {
		return nil, pathRefusal(p, err)
	}
	lines := strings.SplitAfter(string(body), "\n")
	if n := len(lines); n != 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if start < 1 {
		start = 1
	}
	end := len(lines)
	if max > 0 && start-1+max < end {
		end = start - 1 + max
	}
	content := ""
	if start <= len(lines) {
		content = strings.Join(lines[start-1:end], "")
	} else {
		end = start - 1
	}
	return map[string]any{"path": p, "sha256": sha256Hex(body), "total_lines": len(lines), "total_bytes": len(body),
		"start_line": start, "end_line": end, "content": content}, nil
}

func (s *execService) applyPatch(args toolArgs) (any, *toolRefusal) {
	if r := args.only("path", "expected_sha256", "old_string", "new_string"); r != nil {
		return nil, r
	}
	p, r := args.str("path", true)
	if r != nil {
		return nil, r
	}
	expected, r := args.str("expected_sha256", true)
	if r != nil {
		return nil, r
	}
	old, r := args.str("old_string", true)
	if r != nil {
		return nil, r
	}
	replacement, r := args.str("new_string", true)
	if r != nil {
		return nil, r
	}
	if old == "" {
		return nil, refuseTool(refuseArg, "old_string must not be empty")
	}
	expected = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expected)), "sha256:")
	parts, r := toolPath(p)
	if r != nil {
		return nil, r
	}

	s.effects.Lock()
	defer s.effects.Unlock()
	gate := s.e.authorityGate(s.b.TaskID)
	gate.RLock()
	defer gate.RUnlock()
	op, r := s.revalidate()
	if r != nil {
		return nil, r
	}
	if r := s.admitted(op, p); r != nil {
		return nil, r
	}
	dir, err := openDirBeneath(s.b.Worktree, parts)
	if err != nil {
		return nil, pathRefusal(p, err)
	}
	defer unix.Close(dir)
	name := parts[len(parts)-1]
	st, body, err := readAt(dir, name)
	if err != nil {
		return nil, pathRefusal(p, err)
	}
	before := sha256Hex(body)
	if before != expected {
		return nil, refuseTool(refusePatchStale, "%s is at sha256 %s, not the expected %s; read it again", p, before, expected)
	}
	if n := occurrences(string(body), old); n != 1 {
		return nil, refuseTool(refuseArg, "old_string occurs %d times in %s; exactly one occurrence is required", n, p)
	}
	updated := []byte(strings.Replace(string(body), old, replacement, 1))

	suffix, err := randomHex(6)
	if err != nil {
		return nil, refuseTool(refusePath, "%v", err)
	}
	tmp := "." + name + ".sensei-exec-" + suffix
	tfd, err := unix.Openat(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, refuseTool(refusePath, "the replacement for %s cannot be staged: %v", p, err)
	}
	tf := os.NewFile(uintptr(tfd), tmp)
	_, werr := tf.Write(updated)
	if werr == nil {
		werr = unix.Fchmod(tfd, st.Mode&0o7777)
	}
	if werr == nil {
		werr = tf.Sync()
	}
	tf.Close()
	discard := func() { _ = unix.Unlinkat(dir, tmp, 0) }
	if werr != nil {
		discard()
		return nil, refuseTool(refusePath, "the replacement for %s cannot be written: %v", p, werr)
	}
	// Concurrent modification: the name must still be the inode that was
	// read, holding exactly the bytes the preimage named.
	now, again, err := readAt(dir, name)
	if err != nil || now.Dev != st.Dev || now.Ino != st.Ino || !bytes.Equal(again, body) {
		discard()
		return nil, refuseTool(refusePatchStale, "%s changed while the patch was being applied; nothing was written", p)
	}
	if err := unix.Renameat(dir, tmp, dir, name); err != nil {
		discard()
		return nil, refuseTool(refusePath, "the replacement for %s cannot be installed: %v", p, err)
	}
	_ = unix.Fsync(dir)
	return map[string]any{"path": p, "previous_sha256": before, "sha256": sha256Hex(updated), "replacements": 1}, nil
}

// regularFileSum hashes the regular file at p without following a symlink.
func regularFileSum(p string) (string, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is no longer a regular file", p)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checkPrivateSocket establishes that socket is a socket owned by this user
// in a directory only this user can enter, neither of them a symlink.
func checkPrivateSocket(socket string) error {
	if !filepath.IsAbs(socket) {
		return fmt.Errorf("the execution socket %q is not an absolute path", socket)
	}
	uid := uint32(os.Getuid())
	owner := func(info os.FileInfo) (uint32, bool) {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, false
		}
		return st.Uid, true
	}
	dir, err := os.Lstat(filepath.Dir(socket))
	if err != nil {
		return err
	}
	if u, ok := owner(dir); !dir.IsDir() || dir.Mode().Perm() != 0o700 || !ok || u != uid {
		return fmt.Errorf("the execution socket's directory %s is not a private directory of this user", filepath.Dir(socket))
	}
	info, err := os.Lstat(socket)
	if err != nil {
		return err
	}
	if u, ok := owner(info); info.Mode()&os.ModeSocket == 0 || !ok || u != uid {
		return fmt.Errorf("%s is not a socket owned by this user", socket)
	}
	return nil
}
