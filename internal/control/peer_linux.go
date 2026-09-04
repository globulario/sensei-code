//go:build linux

package control

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// What the operating system will tell us about who is on the other end.
//
// Read from the SOCKET, never from the request. A field in the payload saying
// "human: true" is the forgeable claim this whole separation exists to refuse,
// and a second file readable by the same user would be the same claim with an
// extra step.
type peer struct {
	// PID, UID and GID as the kernel recorded them at connect time.
	PID int
	UID uint32
	GID uint32
	// Terminal is the peer's controlling terminal, 0 when it has none. A
	// governed worker is launched with pipes and no terminal; a person running
	// a command in a shell has one.
	Terminal uint64
	// Descendant reports that the peer's parent chain reaches this process. The
	// workers this orchestrator launches are its children.
	Descendant bool
}

// inspectPeer establishes what can be established about the other end.
//
// Every failure is an error rather than a permissive default. A peer whose
// properties cannot be read is not a peer that passes; establishing nothing and
// proceeding is how "we could not check" becomes "it was fine".
func inspectPeer(conn net.Conn) (peer, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return peer{}, fmt.Errorf("the objective channel carried a %T rather than a unix connection", conn)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return peer{}, err
	}
	var creds *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		creds, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return peer{}, err
	}
	if credErr != nil {
		return peer{}, fmt.Errorf("the peer's credentials could not be read: %w", credErr)
	}

	p := peer{PID: int(creds.Pid), UID: creds.Uid, GID: creds.Gid}
	terminal, parents, err := processFacts(p.PID)
	if err != nil {
		return peer{}, err
	}
	p.Terminal = terminal
	self := os.Getpid()
	for _, ancestor := range parents {
		if ancestor == self {
			p.Descendant = true
			break
		}
	}
	return p, nil
}

// processFacts reads a process's controlling terminal and its chain of parents.
//
// The pid comes from SO_PEERCRED, which the kernel recorded when the connection
// was made. A process that exited between then and now could in principle have
// had its pid reused, and these facts would then describe a different process.
// The window is the length of one accept, and closing it would mean comparing
// process start times through a second /proc read that has the same race one
// level down. It is recorded here rather than left for a reader to notice.
func processFacts(pid int) (terminal uint64, parents []int, err error) {
	for depth := 0; pid > 1 && depth < 64; depth++ {
		raw, readErr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if readErr != nil {
			if depth == 0 {
				return 0, nil, fmt.Errorf("the peer process could not be read: %w", readErr)
			}
			// An ancestor that exited mid-walk. The chain so far still stands.
			return terminal, parents, nil
		}
		// The second field is the executable name in parentheses and may
		// contain spaces and parentheses of its own, so the split starts after
		// the LAST closing one.
		close := strings.LastIndex(string(raw), ")")
		if close < 0 {
			return 0, nil, fmt.Errorf("the peer process's stat has no comm field")
		}
		fields := strings.Fields(string(raw)[close+1:])
		// state, ppid, pgrp, session, tty_nr
		if len(fields) < 5 {
			return 0, nil, fmt.Errorf("the peer process's stat is shorter than expected")
		}
		ppid, convErr := strconv.Atoi(fields[1])
		if convErr != nil {
			return 0, nil, convErr
		}
		if depth == 0 {
			tty, convErr := strconv.ParseUint(fields[4], 10, 64)
			if convErr != nil {
				return 0, nil, convErr
			}
			terminal = tty
		}
		parents = append(parents, ppid)
		pid = ppid
	}
	return terminal, parents, nil
}

// peerInspectionSupported reports whether this platform can establish the
// peer's properties at all.
func peerInspectionSupported() bool { return true }
