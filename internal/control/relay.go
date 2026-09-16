package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The relay channel carries ONE thing: a complete review artifact a local
// operator is relaying on a reviewer's behalf.
//
// It is its own socket rather than a second message on the objective channel.
// That channel's message types are pinned by name to an objective and nothing
// else, and widening them is how a local command protocol grows. What the two
// share is the authority decision: authorizedPeer is the same judgement an
// objective gets, so a relay is accepted only from an interactive process this
// orchestrator did not launch. A governed worker cannot relay a review of its
// own candidate any more than it can originate an objective.
//
// Relay authority is not reviewer authority. The observed principal travels
// with the artifact so the record can say who CARRIED it; who produced the
// verdict is the artifact's own claim.

// RelaySocketName is the relay socket, beside the objective socket.
// Short on purpose: a Unix socket path is bounded (108 bytes on Linux), and a
// repository can live deep.
const RelaySocketName = "relay.sock"

// RelaySocketPath is where the relay socket lives for a repository.
func RelaySocketPath(repoRoot string) string {
	return filepath.Join(filepath.Dir(LocalSocketPath(repoRoot)), RelaySocketName)
}

// LocalRelaySubmission is the only message the relay channel reads.
type LocalRelaySubmission struct {
	Artifact string `json:"artifact"`
}

// LocalRelayPrincipal is who the kernel says is relaying.
type LocalRelayPrincipal struct {
	UID      uint32
	PID      int
	Terminal uint64
}

// LocalRelayResult is what the relay handler recorded.
type LocalRelayResult struct {
	State              string `json:"state,omitempty"`
	TaskID             string `json:"task_id,omitempty"`
	RequestID          string `json:"request_id,omitempty"`
	Reviewer           string `json:"reviewer_provider,omitempty"`
	ReviewDigest       string `json:"review_digest,omitempty"`
	Publication        string `json:"publication,omitempty"`
	PublicationComment int64  `json:"publication_comment,omitempty"`
	Error              string `json:"error,omitempty"`
}

// RelayHandler validates, records and publishes one relayed artifact. It may
// return a result AND an error: an accepted relay whose publication failed.
type RelayHandler func(ctx context.Context, artifact string, principal LocalRelayPrincipal) (LocalRelayResult, error)

// maxRelaySubmissionBytes bounds one relay message: an artifact of at most
// 64KiB, JSON-escaped.
const maxRelaySubmissionBytes = 256 << 10

// relayDeadline bounds one relay, which includes publishing to GitHub.
const relayDeadline = 90 * time.Second

// RelayChannel is a bound relay socket.
type RelayChannel struct {
	server *Server
	ln     net.Listener
	path   string
}

// ListenRelay binds the relay socket, refusing one a live process owns.
func (s *Server) ListenRelay(repoRoot string) (*RelayChannel, error) {
	path := RelaySocketPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if live, err := net.Dial("unix", path); err == nil {
		_ = live.Close()
		return nil, fmt.Errorf("another control process is already accepting relayed reviews on %s", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("could not restrict %s to this user: %w", path, err)
	}
	return &RelayChannel{server: s, ln: ln, path: path}, nil
}

// Serve accepts relays until the channel is closed.
func (c *RelayChannel) Serve(h RelayHandler) error {
	if h == nil {
		return errors.New("the relay channel was served without a handler")
	}
	for {
		conn, err := c.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go c.serveConn(conn, h)
	}
}

// Close stops accepting relays and removes the socket.
func (c *RelayChannel) Close() error {
	if c == nil || c.ln == nil {
		return nil
	}
	err := c.ln.Close()
	_ = os.Remove(c.path)
	return err
}

func (c *RelayChannel) serveConn(conn net.Conn, h RelayHandler) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(localDeadline))

	// Judged from the socket before a byte of the artifact is read, by the same
	// decision an objective gets. A refusal never reaches the handler.
	p, err := c.server.authorizedPeer(conn)
	if err != nil {
		writeLocalError(conn, "the relayed review was refused because this caller may not relay one: "+err.Error())
		return
	}

	dec := json.NewDecoder(io.LimitReader(conn, maxRelaySubmissionBytes))
	dec.DisallowUnknownFields()
	var in LocalRelaySubmission
	if err := dec.Decode(&in); err != nil {
		writeLocalError(conn, "the relay is not one review artifact: "+err.Error())
		return
	}
	if dec.More() {
		writeLocalError(conn, "the relay carries trailing content; this channel takes one review artifact")
		return
	}
	if strings.TrimSpace(in.Artifact) == "" {
		writeLocalError(conn, "a relay must carry a review artifact")
		return
	}

	_ = conn.SetDeadline(time.Now().Add(relayDeadline))
	ctx, cancel := context.WithTimeout(context.Background(), relayDeadline)
	defer cancel()
	res, herr := h(ctx, in.Artifact, LocalRelayPrincipal{UID: p.UID, PID: p.PID, Terminal: p.Terminal})
	if herr != nil {
		res.Error = herr.Error()
	}
	_ = json.NewEncoder(conn).Encode(res)
}

// SubmitLocalRelay relays one artifact to the control process and reads what it
// recorded. An accepted relay whose publication failed returns its result AND
// an error.
func SubmitLocalRelay(repoRoot, artifact string) (LocalRelayResult, error) {
	if strings.TrimSpace(artifact) == "" {
		return LocalRelayResult{}, errors.New("a relay must carry a review artifact")
	}
	path := RelaySocketPath(repoRoot)
	conn, err := net.Dial("unix", path)
	if err != nil {
		return LocalRelayResult{}, fmt.Errorf("no control process is accepting relayed reviews at %s; start one with "+
			"`sensei-code control` and the GitHub bridge configured: %w", path, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(relayDeadline + 10*time.Second))
	if err := json.NewEncoder(conn).Encode(LocalRelaySubmission{Artifact: artifact}); err != nil {
		return LocalRelayResult{}, err
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	var res LocalRelayResult
	if err := json.NewDecoder(io.LimitReader(conn, maxRelaySubmissionBytes)).Decode(&res); err != nil {
		return LocalRelayResult{}, fmt.Errorf("the control process answered something this client cannot read: %w", err)
	}
	if msg := strings.TrimSpace(res.Error); msg != "" {
		return res, errors.New(msg)
	}
	return res, nil
}
