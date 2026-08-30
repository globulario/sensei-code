package roles

import (
	"fmt"
	"strings"
	"time"
)

// Provenance is who produced a cross-agent artifact, about what, under which
// rules.
//
// The identity that matters most here is CandidateDigest. A review is an
// opinion about an exact sequence of bytes, and the worker revises those bytes
// between cycles — so a verdict outlives the thing it judged by design, and
// nothing about the verdict itself says which revision it read. Carrying the
// digest turns "this review is stale" from a judgement somebody has to make
// into a comparison that either matches or does not.
type Provenance struct {
	TaskID string `json:"task_id"`
	Role   Role   `json:"role"`
	// Provider is the adapter that produced this, and Model is what it used
	// where the adapter reports one. Provider is the load-bearing half: a
	// review is independent of an implementation because a different provider
	// produced it, not because a different model name was printed.
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	// SessionID is the orchestration session, and SessionMode says whether the
	// provider conversation inherited anything. Both are recorded because
	// "started fresh" is a claim, and a claim about independence is exactly the
	// kind that must be checkable afterwards.
	SessionID   string  `json:"session_id,omitempty"`
	SessionMode Session `json:"session_mode,omitempty"`
	// BaseSHA and CandidateDigest bind the artifact to a repository state and to
	// an exact candidate revision.
	BaseSHA         string `json:"base_sha,omitempty"`
	CandidateDigest string `json:"candidate_digest,omitempty"`
	// CandidateTree is the exact content the verdict was reached about.
	//
	// It is ENGINE-ASSERTED, not echoed by the producer, and Mismatch therefore
	// does not check it. That is deliberate. CandidateDigest is a deterministic
	// function of (BaseSHA, CandidateTree) -- the capture builds the tree and
	// renders the digest from it in one measurement -- so the digest the
	// producer echoes already binds its verdict to this tree. Demanding a
	// second forty-character transcription would add a failure mode without
	// adding assurance.
	//
	// It is recorded because a later reader must be able to say WHAT the
	// verdict was about without re-deriving it from an event stream.
	CandidateTree string `json:"candidate_tree,omitempty"`
	// GraphBuildCommit pins the rule generation. A verdict reached under one
	// graph and applied under another is being applied by rules it never read.
	GraphBuildCommit string `json:"graph_build_commit,omitempty"`
	// ProofPlan identifies the obligation set in play, where one exists.
	ProofPlan string    `json:"proof_plan,omitempty"`
	At        time.Time `json:"at"`
}

// Binding is the identity a cross-agent artifact must be about.
//
// It is deliberately smaller than Provenance: the producer's identity varies by
// design — that is the point of asking a different agent — while the subject
// must not vary at all.
type Binding struct {
	TaskID          string `json:"task_id"`
	BaseSHA         string `json:"base_sha,omitempty"`
	CandidateDigest string `json:"candidate_digest,omitempty"`
	// CandidateTree is the content identity this artifact must be about. See
	// Provenance.CandidateTree: it is asserted by the engine and carried, not
	// demanded back from the producer.
	CandidateTree string `json:"candidate_tree,omitempty"`
}

// Binding extracts the subject this artifact claims to be about.
func (p Provenance) Binding() Binding {
	return Binding{TaskID: p.TaskID, BaseSHA: p.BaseSHA, CandidateDigest: p.CandidateDigest, CandidateTree: p.CandidateTree}
}

// Verify refuses an artifact whose subject is not the one asked about.
//
// An empty field on the binding means "not asserted here" and is not checked:
// the base is unknown before a candidate exists, and the digest is unknown
// before the first diff. An empty field on the artifact when the binding does
// assert one is a refusal rather than a pass — an unbound artifact is not a
// matching one, and treating missing identity as agreement is how a review of
// candidate A gets attached to candidate B.
func (b Binding) Verify(p Provenance) error {
	if mismatch := b.Mismatch(p); mismatch != "" {
		return fmt.Errorf("provenance does not match this candidate: %s", mismatch)
	}
	return nil
}

// Mismatch names the first identity that disagrees, or "" when none does.
func (b Binding) Mismatch(p Provenance) string {
	for _, check := range []struct {
		what string
		want string
		got  string
	}{
		{"task", b.TaskID, p.TaskID},
		{"base commit", b.BaseSHA, p.BaseSHA},
		{"candidate revision", b.CandidateDigest, p.CandidateDigest},
	} {
		want := strings.TrimSpace(check.want)
		if want == "" {
			continue
		}
		got := strings.TrimSpace(check.got)
		if got == "" {
			return check.what + " is not stated on the artifact, and this candidate is " + short(want)
		}
		if got != want {
			return check.what + " is " + short(got) + ", this candidate is " + short(want)
		}
	}
	return ""
}

// Stale reports an artifact that was true about an earlier revision of the same
// task. It is separated from Mismatch because it means something different: not
// a foreign artifact, but this task's own verdict, superseded by the worker's
// next edit. Both are refused; only one is worth telling the worker about.
func (b Binding) Stale(p Provenance) bool {
	if strings.TrimSpace(b.CandidateDigest) == "" || strings.TrimSpace(p.CandidateDigest) == "" {
		return false
	}
	return p.TaskID == b.TaskID && p.CandidateDigest != b.CandidateDigest
}

// Independent reports whether this artifact was produced without inheriting the
// session it is judging. It reads the recorded mode rather than inferring it,
// because the only alternative is to infer independence from the fact that a
// call succeeded, which proves that a call succeeded.
func (p Provenance) Independent() bool {
	return p.SessionMode == Fresh
}

// Describe is one line for the transcript.
func (p Provenance) Describe() string {
	parts := []string{string(p.Role)}
	if p.Provider != "" {
		parts = append(parts, "via "+p.Provider)
	}
	if p.SessionMode != "" {
		parts = append(parts, "session "+string(p.SessionMode))
	}
	if p.CandidateDigest != "" {
		parts = append(parts, "candidate "+short(p.CandidateDigest))
	}
	return strings.Join(parts, " · ")
}

func short(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
