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

// The attestation channel carries ONE thing: a local operator overriding the
// review obligation on one exact relayed review.
//
// Its own socket, for the same reason the relay has its own: each channel reads
// one kind of message and nothing else. What the three share is the authority
// decision -- authorizedPeer -- so an override, like an objective and like a
// relay, comes only from an interactive process this orchestrator did not
// launch. A governed worker cannot override the review of its own candidate.
//
// The operator names the request AND the review digest. Neither is inferred: an
// override typed against "whatever review is there" would cover an artifact its
// author never read.

// AttestSocketName is the attestation socket, beside the objective and relay
// sockets. Short, because a Unix socket path is bounded.
const AttestSocketName = "attest.sock"

// AttestSocketPath is where the attestation socket lives for a repository.
func AttestSocketPath(repoRoot string) string {
	return filepath.Join(filepath.Dir(LocalSocketPath(repoRoot)), AttestSocketName)
}

// LocalAttestation is the only message the attestation channel reads.
type LocalAttestation struct {
	RequestID    string `json:"request_id"`
	ReviewDigest string `json:"review_digest"`
}

// LocalAttestationResult is what the handler recorded.
type LocalAttestationResult struct {
	State              string `json:"state,omitempty"`
	TaskID             string `json:"task_id,omitempty"`
	RequestID          string `json:"request_id,omitempty"`
	ReviewDigest       string `json:"review_digest,omitempty"`
	Reviewer           string `json:"reviewer_provider,omitempty"`
	Principal          string `json:"principal,omitempty"`
	Publication        string `json:"publication,omitempty"`
	PublicationComment int64  `json:"publication_comment,omitempty"`
	Error              string `json:"error,omitempty"`
}

// AttestHandler validates, records and publishes one override. It may return a
// result AND an error: an accepted attestation whose publication failed.
type AttestHandler func(ctx context.Context, in LocalAttestation, principal LocalRelayPrincipal) (LocalAttestationResult, error)

// AttestChannel is a bound attestation socket.
type AttestChannel struct {
	server *Server
	ln     net.Listener
	path   string
}

// ListenAttest binds the attestation socket, refusing one a live process owns.
func (s *Server) ListenAttest(repoRoot string) (*AttestChannel, error) {
	path := AttestSocketPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if live, err := net.Dial("unix", path); err == nil {
		_ = live.Close()
		return nil, fmt.Errorf("another control process is already accepting attestations on %s", path)
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
	return &AttestChannel{server: s, ln: ln, path: path}, nil
}

// Serve accepts attestations until the channel is closed.
func (c *AttestChannel) Serve(h AttestHandler) error {
	if h == nil {
		return errors.New("the attestation channel was served without a handler")
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

// Close stops accepting attestations and removes the socket.
func (c *AttestChannel) Close() error {
	if c == nil || c.ln == nil {
		return nil
	}
	err := c.ln.Close()
	_ = os.Remove(c.path)
	return err
}

func (c *AttestChannel) serveConn(conn net.Conn, h AttestHandler) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(localDeadline))

	p, err := c.server.authorizedPeer(conn)
	if err != nil {
		writeLocalError(conn, "the attestation was refused because this caller may not make one: "+err.Error())
		return
	}

	dec := json.NewDecoder(io.LimitReader(conn, maxLocalSubmissionBytes))
	dec.DisallowUnknownFields()
	var in LocalAttestation
	if err := dec.Decode(&in); err != nil {
		writeLocalError(conn, "the attestation is not a request id and a review digest: "+err.Error())
		return
	}
	if dec.More() {
		writeLocalError(conn, "the attestation carries trailing content; this channel takes one override")
		return
	}
	if strings.TrimSpace(in.RequestID) == "" || strings.TrimSpace(in.ReviewDigest) == "" {
		writeLocalError(conn, "an attestation must name both the request it covers and that review's digest")
		return
	}

	_ = conn.SetDeadline(time.Now().Add(relayDeadline))
	ctx, cancel := context.WithTimeout(context.Background(), relayDeadline)
	defer cancel()
	res, herr := h(ctx, in, LocalRelayPrincipal{UID: p.UID, PID: p.PID, Terminal: p.Terminal})
	if herr != nil {
		res.Error = herr.Error()
	}
	_ = json.NewEncoder(conn).Encode(res)
}

// SubmitLocalAttestation carries one override to the control process and reads
// what it recorded.
func SubmitLocalAttestation(repoRoot, requestID, reviewDigest string) (LocalAttestationResult, error) {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(reviewDigest) == "" {
		return LocalAttestationResult{}, errors.New("an attestation must name both the request and the review digest")
	}
	path := AttestSocketPath(repoRoot)
	conn, err := net.Dial("unix", path)
	if err != nil {
		return LocalAttestationResult{}, fmt.Errorf("no control process is accepting attestations at %s; start one with "+
			"`sensei-code control` and the GitHub bridge configured: %w", path, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(relayDeadline + 10*time.Second))
	if err := json.NewEncoder(conn).Encode(LocalAttestation{RequestID: requestID, ReviewDigest: reviewDigest}); err != nil {
		return LocalAttestationResult{}, err
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	var res LocalAttestationResult
	if err := json.NewDecoder(io.LimitReader(conn, maxRelaySubmissionBytes)).Decode(&res); err != nil {
		return LocalAttestationResult{}, fmt.Errorf("the control process answered something this client cannot read: %w", err)
	}
	if msg := strings.TrimSpace(res.Error); msg != "" {
		return res, errors.New(msg)
	}
	return res, nil
}
