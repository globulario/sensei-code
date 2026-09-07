package ghwebhook

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ObjectiveProposalMarker distinguishes untrusted objective proposals from
// every other issue comment. A proposal is data. Recording one grants no
// authority and starts no workflow.
const ObjectiveProposalMarker = "[sensei-code:objective-proposal]"

const maxObjectiveProposalBytes = 64 << 10

var ErrMalformedObjectiveProposal = errors.New("malformed objective proposal")

// ObjectiveProposal is the immutable record created from one authenticated
// GitHub issue_comment.created delivery.
//
// The GitHub signature establishes that GitHub delivered these facts. It does
// not authorize Objective. The proposal stays inert until a local operator
// explicitly approves this exact CommentID through the existing local objective
// channel.
type ObjectiveProposal struct {
	Version            int    `json:"version"`
	CommentID          int64  `json:"comment_id"`
	FirstDeliveryID    string `json:"first_delivery_id"`
	RepositoryFullName string `json:"repository_full_name"`
	IssueNumber        int64  `json:"issue_number"`
	SenderID           int64  `json:"sender_id"`
	SenderLogin        string `json:"sender_login"`
	Objective          string `json:"objective"`
	ObjectiveDigest    string `json:"objective_digest"`
}

// ProposalApproval is a durable, at-most-once approval attempt.
//
// The receipt is created BEFORE the existing local objective channel is called.
// That ordering is deliberate: if the process dies after the task was accepted
// but before the receipt can be completed, the receipt remains "attempting" and
// a second approval is refused rather than possibly creating the task twice.
type ProposalApproval struct {
	Version         int    `json:"version"`
	CommentID       int64  `json:"comment_id"`
	ObjectiveDigest string `json:"objective_digest"`
	Nonce           string `json:"nonce"`
	State           string `json:"state"` // attempting | submitted
	AttemptedAt     string `json:"attempted_at"`
	CompletedAt     string `json:"completed_at,omitempty"`
	TaskID          string `json:"task_id,omitempty"`
	Provenance      string `json:"provenance,omitempty"`
}

type deliveryReceipt struct {
	DeliveryID      string `json:"delivery_id"`
	CommentID       int64  `json:"comment_id"`
	ObjectiveDigest string `json:"objective_digest"`
}

// ProposalStore is the durable mailbox for inert objective proposals.
type ProposalStore struct {
	root string
	mu   sync.Mutex
}

func NewProposalStore(repoRoot string) *ProposalStore {
	return &ProposalStore{root: filepath.Join(repoRoot, ".sensei-code", "github-objective-proposals")}
}

// discoverProposalRepoRoot mirrors the one fact main already established: the
// command is running somewhere inside one Git worktree. It does not infer a
// repository from the webhook payload, which is remote data; it walks the local
// filesystem upward and takes the nearest .git boundary, the same worktree the
// command was launched from.
func discoverProposalRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ParseObjectiveProposal recognizes one deliberately tiny protocol:
//
//	[sensei-code:objective-proposal]
//	{"objective":"the exact objective text"}
//
// JSON is used for the payload so the objective has one unambiguous string
// identity. Unknown fields are refused rather than silently becoming future
// authority knobs.
func ParseObjectiveProposal(body string) (objective string, handled bool, err error) {
	line, rest := body, ""
	if at := strings.IndexByte(body, '\n'); at >= 0 {
		line, rest = body[:at], body[at+1:]
	}
	line = strings.TrimSuffix(line, "\r")
	if line != ObjectiveProposalMarker {
		return "", false, nil
	}
	if strings.TrimSpace(rest) == "" {
		return "", true, fmt.Errorf("%w: the marker carries no JSON envelope", ErrMalformedObjectiveProposal)
	}
	var envelope struct {
		Objective string `json:"objective"`
	}
	dec := json.NewDecoder(strings.NewReader(rest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&envelope); err != nil {
		return "", true, fmt.Errorf("%w: the envelope is not valid JSON", ErrMalformedObjectiveProposal)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", true, fmt.Errorf("%w: the envelope carries trailing content", ErrMalformedObjectiveProposal)
	}
	if strings.TrimSpace(envelope.Objective) == "" {
		return "", true, fmt.Errorf("%w: objective is empty", ErrMalformedObjectiveProposal)
	}
	if len([]byte(envelope.Objective)) > maxObjectiveProposalBytes {
		return "", true, fmt.Errorf("%w: objective exceeds %d bytes", ErrMalformedObjectiveProposal, maxObjectiveProposalBytes)
	}
	return envelope.Objective, true, nil
}

func DigestObjective(objective string) string {
	sum := sha256.Sum256([]byte(objective))
	return fmt.Sprintf("%x", sum[:])
}

func (s *ProposalStore) proposalPath(commentID int64) string {
	return filepath.Join(s.root, strconv.FormatInt(commentID, 10)+".json")
}

func (s *ProposalStore) approvalPath(commentID int64) string {
	return filepath.Join(s.root, "approvals", strconv.FormatInt(commentID, 10)+".json")
}

func (s *ProposalStore) deliveryPath(deliveryID string) string {
	sum := sha256.Sum256([]byte(deliveryID))
	return filepath.Join(s.root, "deliveries", fmt.Sprintf("%x.json", sum[:]))
}

func (s *ProposalStore) ensureDirs() error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return errors.New("objective proposal store has no repository root")
	}
	for _, dir := range []string{s.root, filepath.Join(s.root, "approvals"), filepath.Join(s.root, "deliveries")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Record receives one authenticated delivery and, only if it carries the
// proposal marker, records it as inert data.
//
// Idempotency is durable on BOTH GitHub identities. DeliveryID answers whether
// this delivery was processed before; CommentID answers whether this comment
// already has a proposal. A new delivery for the same comment is a redelivery
// and creates no second proposal. Reusing one delivery id for another comment,
// or one comment id for different objective bytes, is an integrity conflict and
// is never overwritten.
func (s *ProposalStore) Record(d IssueCommentDelivery) (p ObjectiveProposal, created, handled bool, err error) {
	objective, handled, err := ParseObjectiveProposal(d.CommentBody)
	if err != nil || !handled {
		return ObjectiveProposal{}, false, handled, err
	}
	if strings.TrimSpace(d.DeliveryID) == "" || d.CommentID == 0 {
		return ObjectiveProposal{}, false, true, errors.New("authenticated proposal delivery lacks durable GitHub identity")
	}
	digest := DigestObjective(objective)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDirs(); err != nil {
		return ObjectiveProposal{}, false, true, err
	}

	if r, ok, err := s.loadDeliveryUnlocked(d.DeliveryID); err != nil {
		return ObjectiveProposal{}, false, true, err
	} else if ok {
		if r.CommentID != d.CommentID || r.ObjectiveDigest != digest {
			return ObjectiveProposal{}, false, true, fmt.Errorf("delivery %s is already bound to a different objective proposal", d.DeliveryID)
		}
		p, err := s.loadProposalUnlocked(d.CommentID)
		return p, false, true, err
	}

	existing, loadErr := s.loadProposalUnlocked(d.CommentID)
	if loadErr == nil {
		if existing.RepositoryFullName != d.RepositoryFullName || existing.IssueNumber != d.IssueNumber ||
			existing.SenderID != d.SenderID || existing.ObjectiveDigest != digest {
			return ObjectiveProposal{}, false, true, fmt.Errorf("comment %d is already bound to different proposal facts", d.CommentID)
		}
		if err := s.writeDeliveryUnlocked(deliveryReceipt{DeliveryID: d.DeliveryID, CommentID: d.CommentID, ObjectiveDigest: digest}); err != nil {
			return ObjectiveProposal{}, false, true, err
		}
		return existing, false, true, nil
	}
	if !errors.Is(loadErr, os.ErrNotExist) {
		return ObjectiveProposal{}, false, true, loadErr
	}

	// Close the crash window where a proposal file was durable but its delivery
	// receipt was not yet written. The first delivery id is also in the proposal
	// itself, so a reused id cannot become a second comment after restart.
	if other, ok, err := s.findFirstDeliveryUnlocked(d.DeliveryID); err != nil {
		return ObjectiveProposal{}, false, true, err
	} else if ok && other.CommentID != d.CommentID {
		return ObjectiveProposal{}, false, true, fmt.Errorf("delivery %s is already the first delivery for comment %d", d.DeliveryID, other.CommentID)
	}

	p = ObjectiveProposal{
		Version:            1,
		CommentID:          d.CommentID,
		FirstDeliveryID:    d.DeliveryID,
		RepositoryFullName: d.RepositoryFullName,
		IssueNumber:        d.IssueNumber,
		SenderID:           d.SenderID,
		SenderLogin:        d.SenderLogin,
		Objective:          objective,
		ObjectiveDigest:    digest,
	}
	if err := writeExclusiveJSON(s.proposalPath(d.CommentID), p); err != nil {
		return ObjectiveProposal{}, false, true, err
	}
	if err := s.writeDeliveryUnlocked(deliveryReceipt{DeliveryID: d.DeliveryID, CommentID: d.CommentID, ObjectiveDigest: digest}); err != nil {
		// The proposal is already durable. Returning an error makes GitHub retry;
		// the retry will find this proposal and finish the missing delivery
		// receipt without creating another proposal.
		return ObjectiveProposal{}, false, true, err
	}
	return p, true, true, nil
}

func (s *ProposalStore) Load(commentID int64) (ObjectiveProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadProposalUnlocked(commentID)
}

func (s *ProposalStore) loadProposalUnlocked(commentID int64) (ObjectiveProposal, error) {
	var p ObjectiveProposal
	if commentID <= 0 {
		return p, errors.New("proposal comment id must be positive")
	}
	if err := readJSON(s.proposalPath(commentID), &p); err != nil {
		return ObjectiveProposal{}, err
	}
	if p.CommentID != commentID || p.Version != 1 || p.ObjectiveDigest != DigestObjective(p.Objective) {
		return ObjectiveProposal{}, fmt.Errorf("proposal %d failed its immutable identity check", commentID)
	}
	return p, nil
}

func (s *ProposalStore) List() ([]ObjectiveProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDirs(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var out []ObjectiveProposal
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		p, err := s.loadProposalUnlocked(id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CommentID < out[j].CommentID })
	return out, nil
}

// BeginApproval durably spends the one approval attempt for this proposal.
// The caller must do this before it performs the side effect.
func (s *ProposalStore) BeginApproval(commentID int64, at time.Time, nonce string) (ObjectiveProposal, ProposalApproval, error) {
	nonce = strings.TrimSpace(nonce)
	if nonce == "" {
		return ObjectiveProposal{}, ProposalApproval{}, errors.New("approval nonce is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDirs(); err != nil {
		return ObjectiveProposal{}, ProposalApproval{}, err
	}
	p, err := s.loadProposalUnlocked(commentID)
	if err != nil {
		return ObjectiveProposal{}, ProposalApproval{}, err
	}
	a := ProposalApproval{
		Version:         1,
		CommentID:       commentID,
		ObjectiveDigest: p.ObjectiveDigest,
		Nonce:           nonce,
		State:           "attempting",
		AttemptedAt:     at.UTC().Format(time.RFC3339Nano),
	}
	if err := writeExclusiveJSON(s.approvalPath(commentID), a); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ObjectiveProposal{}, ProposalApproval{}, fmt.Errorf("proposal %d already has a durable approval attempt; it will not be submitted twice", commentID)
		}
		return ObjectiveProposal{}, ProposalApproval{}, err
	}
	return p, a, nil
}

// CompleteApproval binds the durable attempt to the task the existing local
// objective channel actually accepted.
func (s *ProposalStore) CompleteApproval(commentID int64, nonce, taskID, provenance string, at time.Time) (ProposalApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var a ProposalApproval
	if err := readJSON(s.approvalPath(commentID), &a); err != nil {
		return ProposalApproval{}, err
	}
	p, err := s.loadProposalUnlocked(commentID)
	if err != nil {
		return ProposalApproval{}, err
	}
	if a.Version != 1 || a.CommentID != commentID || a.ObjectiveDigest != p.ObjectiveDigest || a.Nonce != strings.TrimSpace(nonce) {
		return ProposalApproval{}, fmt.Errorf("proposal %d approval receipt does not match the approval attempt", commentID)
	}
	if a.State != "attempting" {
		return ProposalApproval{}, fmt.Errorf("proposal %d approval is already %s", commentID, a.State)
	}
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(provenance) == "" {
		return ProposalApproval{}, errors.New("the local objective channel returned an incomplete acceptance")
	}
	a.State = "submitted"
	a.CompletedAt = at.UTC().Format(time.RFC3339Nano)
	a.TaskID = strings.TrimSpace(taskID)
	a.Provenance = strings.TrimSpace(provenance)
	if err := replaceJSON(s.approvalPath(commentID), a); err != nil {
		return ProposalApproval{}, err
	}
	return a, nil
}

func (s *ProposalStore) Approval(commentID int64) (ProposalApproval, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var a ProposalApproval
	if err := readJSON(s.approvalPath(commentID), &a); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ProposalApproval{}, false, nil
		}
		return ProposalApproval{}, false, err
	}
	p, err := s.loadProposalUnlocked(commentID)
	if err != nil {
		return ProposalApproval{}, false, err
	}
	if a.CommentID != commentID || a.ObjectiveDigest != p.ObjectiveDigest {
		return ProposalApproval{}, false, fmt.Errorf("proposal %d approval receipt is bound to different objective bytes", commentID)
	}
	return a, true, nil
}

func (s *ProposalStore) loadDeliveryUnlocked(deliveryID string) (deliveryReceipt, bool, error) {
	var r deliveryReceipt
	if err := readJSON(s.deliveryPath(deliveryID), &r); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return deliveryReceipt{}, false, nil
		}
		return deliveryReceipt{}, false, err
	}
	if r.DeliveryID != deliveryID {
		return deliveryReceipt{}, false, errors.New("delivery receipt hash collision or corruption")
	}
	return r, true, nil
}

func (s *ProposalStore) writeDeliveryUnlocked(r deliveryReceipt) error {
	path := s.deliveryPath(r.DeliveryID)
	if err := writeExclusiveJSON(path, r); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		var existing deliveryReceipt
		if err := readJSON(path, &existing); err != nil {
			return err
		}
		if existing != r {
			return fmt.Errorf("delivery %s is already bound to a different proposal", r.DeliveryID)
		}
	}
	return nil
}

func (s *ProposalStore) findFirstDeliveryUnlocked(deliveryID string) (ObjectiveProposal, bool, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return ObjectiveProposal{}, false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		p, err := s.loadProposalUnlocked(id)
		if err != nil {
			return ObjectiveProposal{}, false, err
		}
		if p.FirstDeliveryID == deliveryID {
			return p, true, nil
		}
	}
	return ObjectiveProposal{}, false, nil
}

func writeExclusiveJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	ok = true
	return syncDirectory(filepath.Dir(path))
}

func replaceJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".proposal-receipt-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func readJSON(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
