package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

type relayRecorder struct {
	mu        sync.Mutex
	artifacts []string
	who       []LocalRelayPrincipal
}

func (r *relayRecorder) handler(ctx context.Context, artifact string, p LocalRelayPrincipal) (LocalRelayResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.artifacts = append(r.artifacts, artifact)
	r.who = append(r.who, p)
	return LocalRelayResult{State: "published", RequestID: "r-1"}, nil
}

func (r *relayRecorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.artifacts)
}

func newRelayHarness(t *testing.T, observe func(net.Conn) (peer, error)) (*harness, *relayRecorder) {
	t.Helper()
	h := newHarness(t)
	h.server.peerFor = observe
	ch, err := h.server.ListenRelay(h.root)
	if err != nil {
		t.Fatalf("bind the relay channel: %v", err)
	}
	t.Cleanup(func() { ch.Close() })
	rec := &relayRecorder{}
	go ch.Serve(rec.handler)
	return h, rec
}

// A review is relayed only by an interactive process this orchestrator did not
// launch -- the same judgement an objective gets. A governed worker cannot relay
// a verdict on its own candidate, and a refusal never reaches the handler.
func TestOnlyAnOperatorMayRelayAReview(t *testing.T) {
	uid := uint32(os.Getuid())
	for name, observe := range map[string]func(net.Conn) (peer, error){
		// Short names: each subtest's temp dir is part of a Unix socket path.
		"worker": func(net.Conn) (peer, error) {
			return peer{PID: 77, UID: uid, Terminal: 34816, Descendant: true}, nil
		},
		"notty": func(net.Conn) (peer, error) { return peer{PID: 78, UID: uid}, nil },
		"otheruid": func(net.Conn) (peer, error) {
			return peer{PID: 79, UID: uid + 1, Terminal: 34816}, nil
		},
		"unobserved": func(net.Conn) (peer, error) { return peer{}, errors.New("no credentials") },
	} {
		t.Run(name, func(t *testing.T) {
			h, rec := newRelayHarness(t, observe)
			if _, err := SubmitLocalRelay(h.root, "[sensei-code:review]\n..."); err == nil {
				t.Fatal("the relay was accepted")
			}
			if rec.calls() != 0 {
				t.Fatal("a refused relay reached the handler")
			}
		})
	}

	h, rec := newRelayHarness(t, operatorPeer)
	artifact := "[sensei-code:review]\ntask=t\n  exact bytes, kept \n"
	res, err := SubmitLocalRelay(h.root, artifact)
	if err != nil || res.State != "published" {
		t.Fatalf("an operator's relay: %+v %v", res, err)
	}
	if rec.calls() != 1 || rec.artifacts[0] != artifact {
		t.Fatalf("the handler did not receive the exact artifact: %q", rec.artifacts)
	}
	if got := rec.who[0]; got.PID != 4242 || got.Terminal != 34816 || got.UID != uid {
		t.Fatalf("the handler was not told who relayed: %+v", got)
	}
}

// The relay channel reads one artifact and nothing else: no decision, no
// request, no candidate, nothing trailing.
func TestTheRelayChannelCarriesOneArtifactAndNothingElse(t *testing.T) {
	h, rec := newRelayHarness(t, operatorPeer)
	for name, body := range map[string]string{
		"a decision beside it": `{"artifact":"x","decision":"accept"}`,
		"a request beside it":  `{"artifact":"x","request_id":"r-1"}`,
		"an empty artifact":    `{"artifact":"   "}`,
		"nothing":              `{}`,
		"trailing content":     `{"artifact":"x"} {"artifact":"y"}`,
		"not json":             `{"artifact":`,
	} {
		if err := rawRelay(t, h.root, body); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if rec.calls() != 0 {
		t.Fatalf("a refused relay reached the handler: %q", rec.artifacts)
	}
}

func rawRelay(t *testing.T, root, body string) error {
	t.Helper()
	conn, err := net.Dial("unix", RelaySocketPath(root))
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(body)); err != nil {
		return err
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	var reply map[string]any
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		return err
	}
	if msg, ok := reply["error"].(string); ok && msg != "" {
		return errors.New(msg)
	}
	return nil
}
