//go:build !linux

package control

import (
	"errors"
	"net"
)

// On a platform whose peer credentials this project has not established how to
// read, the objective channel refuses rather than falling back to the file
// mode.
//
// Fail closed, and not as caution: the whole finding that produced this file is
// that the mode establishes the user and not the role. A platform where the
// role cannot be established is one where the channel grants an authority it
// cannot justify, and the honest answer is that it does not open.
type peer struct {
	PID        int
	UID        uint32
	GID        uint32
	Terminal   uint64
	Descendant bool
}

func inspectPeer(net.Conn) (peer, error) {
	return peer{}, errors.New("this platform cannot establish who is on the other end of the objective channel")
}

func peerInspectionSupported() bool { return false }
