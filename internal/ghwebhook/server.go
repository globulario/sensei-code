package ghwebhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Endpoint is the single path this surface answers on.
//
// Different from control.Endpoint, and that is load-bearing rather than
// incidental: two authentication regimes on one path is how a request
// authenticated under one rule gets served by the other.
const Endpoint = "/github/webhook"

// MaxBodyBytes bounds a delivery before any of it is read.
//
// GitHub caps a comment body at 65536 characters; the surrounding issue_comment
// payload — repository, installation, sender, issue, reactions — is a few
// kilobytes more, and escaping can inflate multibyte text. One mebibyte is
// comfortably above a real delivery and far below what an unbounded read would
// let an unauthenticated caller allocate.
//
// Bounded BEFORE the read, not checked after: Content-Length is the sender's
// claim, and a body is only actually bounded by refusing to read past a limit.
const MaxBodyBytes int64 = 1 << 20

// Server is the GitHub webhook ingress.
//
// It authenticates deliveries and hands the facts to a sink. It holds no
// workflow state, no engine, no task and no lease, because it is entitled to
// cause none of those.
type Server struct {
	addr   string
	secret []byte

	installationID int64
	repositoryID   int64
	repository     string

	sink Sink

	ln  net.Listener
	srv *http.Server
}

// Listen binds the configured address, refusing anything that is not loopback.
func (s *Server) Listen() error {
	if err := requireLoopback(s.addr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	// Bound, and then checked again against what was actually bound. The
	// address asked for and the address obtained are different facts.
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && !tcp.IP.IsLoopback() {
		_ = ln.Close()
		return fmt.Errorf("refusing to serve the github webhook on %s, which is not loopback", ln.Addr())
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return nil
}

// Addr is what was actually bound, empty before Listen.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Serve runs until Close.
func (s *Server) Serve() error {
	if s.ln == nil {
		return errors.New("the github webhook was asked to serve before it bound an address")
	}
	err := s.srv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops serving.
func (s *Server) Close() error {
	if s.srv == nil {
		if s.ln != nil {
			return s.ln.Close()
		}
		return nil
	}
	return s.srv.Close()
}

// Handler is the HTTP surface, exposed so a test can exercise it without a
// socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(Endpoint, s.handle)
	return mux
}

// handle authenticates one delivery and dispatches it.
//
// THE ORDER BELOW IS THE SECURITY PROPERTY. Read it as a sequence, because
// every step earns the right to perform the next one:
//
//	1  bound the body            before anything is read
//	2  read the exact raw bytes  once, and keep them
//	3  require the signature header
//	4  require the sha256= form
//	5  HMAC-SHA256 over those exact bytes
//	6  constant-time compare
//	-- everything above this line runs on UNAUTHENTICATED input --
//	7  parse JSON
//	8  require a delivery id
//	9  read the event name
//	10 event-specific structural and binding checks
//
// Parsing sits at step 7 and not one step earlier. A JSON parser is a large
// piece of machinery to expose to an unauthenticated sender, and — more
// importantly for what this surface claims — a request refused because its JSON
// was malformed has been REFUSED FOR THE WRONG REASON: the answer to "was this
// GitHub?" is still unknown, and the response says nothing about it.
//
// Nothing above step 6 may be substituted by anything below it. User-Agent,
// sender.login, sender.id, X-GitHub-Event and X-GitHub-Delivery are facts
// carried BY an authenticated delivery. Each is trivially forged by anyone who
// can reach this socket, and treating one as evidence of origin would replace a
// MAC with a string comparison against attacker-controlled input.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// A webhook is a delivery, not a query. GET/HEAD are refused before
	// anything else so no other path has to consider them.
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		refuse(w, http.StatusMethodNotAllowed, "the github webhook accepts POST only")
		return
	}

	// 1 + 2. Bounded, then read once. The bytes are kept exactly as received:
	// the MAC is over these, and any normalization would authenticate a
	// document GitHub did not send.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			refuse(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("the delivery exceeds the %d byte limit", MaxBodyBytes))
			return
		}
		refuse(w, http.StatusBadRequest, "the delivery body could not be read")
		return
	}

	// 3 + 4 + 5 + 6. One error for every way this fails, and it names nothing
	// about the secret, the expected MAC or the received one.
	if err := verify(s.secret, body, r.Header.Get(SignatureHeader)); err != nil {
		refuse(w, http.StatusUnauthorized, ErrUnauthenticated.Error())
		return
	}

	// ---- authenticated: GitHub sent exactly these bytes ----
	//
	// And nothing more than that. Not that anyone authorized anything.

	// 8. A delivery with no id cannot be reconciled with GitHub's own record of
	// what it sent, and is the one field the later replay boundary is keyed on.
	deliveryID := strings.TrimSpace(r.Header.Get(DeliveryHeader))
	if deliveryID == "" {
		refuse(w, http.StatusBadRequest, "the delivery carried no "+DeliveryHeader)
		return
	}

	// 9.
	switch strings.TrimSpace(r.Header.Get(EventHeader)) {
	case "ping":
		// GitHub's configuration test. It proves the route and the secret
		// agree, and it must cause nothing else — the sink is not called and
		// the payload is not even parsed, because there is nothing here to
		// bind and no reason to run a parser for a receipt.
		accept(w, deliveryID, "ping", "route confirmed; no effect")
	case "issue_comment":
		s.issueComment(w, r, deliveryID, body)
	default:
		// Authenticated and ignored. A 2xx, because refusing would make GitHub
		// retry a delivery this slice is simply not interested in.
		accept(w, deliveryID, "", "event not handled by this ingress")
	}
}

// issueCommentPayload is the subset of the payload this ingress reads.
//
// A subset on purpose. Fields nobody checks are fields nobody has reasoned
// about, and unmarshalling the whole document would carry attacker-influenced
// text into a struct that later grows a consumer.
type issueCommentPayload struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Issue struct {
		Number int64 `json:"number"`
	} `json:"issue"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	} `json:"comment"`
	Sender struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"sender"`
}

// issueComment applies step 7 and step 10 for an issue_comment delivery.
func (s *Server) issueComment(w http.ResponseWriter, r *http.Request, deliveryID string, body []byte) {
	// 7. Only now, and only because the signature verified.
	var payload issueCommentPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		// The error text is ours. json's own message can quote the offending
		// input, and the input is attacker-influenced.
		refuse(w, http.StatusBadRequest, "the issue_comment payload is not valid JSON")
		return
	}

	// 10. Binding. Three separate facts, all checked, none inferred from
	// another: an installation can be moved, a repository can be renamed, and
	// a name can be transferred to a different repository while the id cannot.
	// Checking one and trusting the rest is how a delivery about somebody
	// else's repository gets accepted as this one's.
	if payload.Installation.ID != s.installationID {
		refuse(w, http.StatusForbidden, "the delivery is not from the configured App installation")
		return
	}
	if payload.Repository.ID != s.repositoryID {
		refuse(w, http.StatusForbidden, "the delivery is not for the configured repository id")
		return
	}
	if payload.Repository.FullName != s.repository {
		refuse(w, http.StatusForbidden, "the delivery is not for the configured repository")
		return
	}

	// Structural. A payload missing these is not one this ingress can normalize
	// honestly, and a zero id passed downstream reads as a real id.
	if payload.Issue.Number == 0 {
		refuse(w, http.StatusBadRequest, "the issue_comment payload names no issue")
		return
	}
	if payload.Comment.ID == 0 {
		refuse(w, http.StatusBadRequest, "the issue_comment payload names no comment")
		return
	}
	if payload.Sender.ID == 0 || strings.TrimSpace(payload.Sender.Login) == "" {
		refuse(w, http.StatusBadRequest, "the issue_comment payload names no sender")
		return
	}

	// The issue NUMBER is deliberately not checked. Which issue is the review
	// mailbox is a protocol question, decided by a later layer that can see
	// what it means; a transport that hardcoded one number would be holding
	// policy it cannot reason about, and would have to be edited to add a
	// second mailbox.

	if payload.Action != "created" {
		// Authenticated and ignored: edits and deletions carry no new text this
		// slice is arranged to receive.
		accept(w, deliveryID, "issue_comment", "action not handled by this ingress")
		return
	}

	delivery := IssueCommentDelivery{
		DeliveryID:         deliveryID,
		Action:             payload.Action,
		InstallationID:     payload.Installation.ID,
		RepositoryID:       payload.Repository.ID,
		RepositoryFullName: payload.Repository.FullName,
		IssueNumber:        payload.Issue.Number,
		CommentID:          payload.Comment.ID,
		CommentBody:        payload.Comment.Body,
		SenderID:           payload.Sender.ID,
		SenderLogin:        payload.Sender.Login,
		SenderType:         payload.Sender.Type,
	}

	if err := s.sink.IssueComment(r.Context(), delivery); err != nil {
		// The delivery WAS authentic; receiving it failed. A 500 is honest and
		// lets GitHub retry, which is safe precisely because this slice has no
		// side-effecting consumer. The sink's error text is not echoed: it is
		// this process's internals, not GitHub's business.
		refuse(w, http.StatusInternalServerError, "the delivery could not be received")
		return
	}
	accept(w, deliveryID, "issue_comment", "delivered")
}

// accept writes a safe receipt.
//
// It carries back only what GitHub already sent: the delivery id it minted and
// the event it named. Nothing about the repository, the sender, the comment, or
// this process's configuration — a receipt is not a place to confirm what an
// unauthenticated prober guessed right.
func accept(w http.ResponseWriter, deliveryID, event, note string) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "accepted",
		"delivery": deliveryID,
		"event":    event,
		"note":     note,
	})
}

// refuse writes a refusal.
//
// The reason is always this package's own text. Nothing derived from the
// secret, from either MAC, or from the request body reaches it.
func refuse(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, map[string]string{"status": "refused", "reason": reason})
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
