// Package continuity keeps the architect conversation identifiable across
// restarts without letting memory become authority.
//
// The architect process is started fresh for every turn, so continuity has to
// come from somewhere. The tempting somewhere is the transcript: keep the
// dialogue, replay it, and the conversation appears to persist. That fails in
// the direction that matters — a remembered exchange is not evidence, and a
// model that recalls being told something behaves exactly like one that
// verified it.
//
// So this records identity, not content. Which conversation this is, which
// architect it was held with, what repository state it last saw, and whether
// the thread can be picked up at all. Everything durable and architectural
// lives in Sensei and in the session record; this only answers "is this the
// same conversation, and if not, say so out loud".
//
//	A stored thread id is a continuity hint and never an authority claim. When
//	it cannot be resumed, the turn states the loss and reconstructs context from
//	governed sources, rather than pretending the conversation survived.
package continuity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is what happened to the conversation at the start of this turn.
type State string

const (
	// Started means there was no prior conversation for this workspace.
	Started State = "started"
	// Continued means the same architect is picking up the same thread.
	Continued State = "continued"
	// Reconstructed means the prior thread cannot be resumed, so the
	// architectural context is rebuilt from durable sources. It is a real
	// answer and must be visible: an answer built from reconstruction should
	// never be presented as an answer built from an unbroken conversation.
	Reconstructed State = "reconstructed"
)

// Thread is the durable identity of one architect conversation.
//
// It deliberately holds no architectural content. There is no field here for a
// decision, an invariant or a conclusion, because a local file that could carry
// one would become a second, weaker governance store — and the first time it
// disagreed with Sensei, the disagreement would be invisible.
type Thread struct {
	Workspace string `json:"workspace"`
	// Architect identifies who the conversation was held with. A thread is not
	// portable across providers: resuming another provider's handle is not
	// continuity, it is a wrong answer with a plausible shape.
	//
	// A model change underneath the same provider is deliberately not modelled
	// here. Providers do not report the model they served a turn with, and a
	// field nothing can populate would look like a check that is happening. In
	// practice the handle carries it: a provider that switched models issues a
	// new session, and a new session is a reconstruction.
	Architect string `json:"architect"`
	// ThreadID is the provider's own conversation handle when it has one. It is
	// a hint. Its absence costs continuity, never correctness.
	ThreadID string `json:"thread_id,omitempty"`
	// BaseSHA is the repository state the conversation last saw, so a turn can
	// notice that the ground moved underneath it.
	BaseSHA   string    `json:"base_sha,omitempty"`
	Turns     int       `json:"turns"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Resumption is the verdict for one turn: what state the conversation is in and
// why, in words a human can act on.
type Resumption struct {
	State  State  `json:"state"`
	Reason string `json:"reason,omitempty"`
	// BaseMoved reports that the repository advanced since the last turn. It is
	// not a loss of continuity — it is a fact the architect must know, because
	// a conversation about a file is about that file as it stood.
	BaseMoved bool   `json:"base_moved,omitempty"`
	PriorBase string `json:"prior_base,omitempty"`
}

// Continues reports whether this turn continues the recorded conversation.
//
// Every negative answer is specific. "Continuity was lost" that cannot say what
// changed teaches a person to ignore it.
func (t Thread) Continues(architect, baseSHA string) Resumption {
	switch {
	case strings.TrimSpace(t.Architect) == "":
		return Resumption{State: Started, Reason: "no architect conversation has been recorded for this workspace"}
	case !strings.EqualFold(strings.TrimSpace(t.Architect), strings.TrimSpace(architect)):
		return Resumption{State: Reconstructed,
			Reason: fmt.Sprintf("the recorded conversation was held with %s and this turn is with %s; a thread is not portable between them", t.Architect, architect)}
	case strings.TrimSpace(t.ThreadID) == "":
		return Resumption{State: Reconstructed,
			Reason: "the provider gave no resumable thread for the earlier conversation"}
	}
	r := Resumption{State: Continued}
	if prior := strings.TrimSpace(t.BaseSHA); prior != "" && strings.TrimSpace(baseSHA) != "" && prior != baseSHA {
		r.BaseMoved, r.PriorBase = true, prior
	}
	return r
}

// Describe renders the resumption for the architect and for the human, saying
// plainly what is being relied on.
func (r Resumption) Describe() string {
	switch r.State {
	case Continued:
		if r.BaseMoved {
			return "continuing the same architect conversation; the repository has advanced since the last turn (was " + short(r.PriorBase) + "), so re-read anything you are relying on"
		}
		return "continuing the same architect conversation"
	case Reconstructed:
		return "the earlier architect conversation cannot be resumed (" + r.Reason +
			"), so this turn is reconstructed from the session record and Sensei rather than from remembered dialogue"
	default:
		return "this is the first architect conversation recorded for this workspace"
	}
}

// Record advances the conversation record for a completed turn.
func (t Thread) Record(architect, threadID, baseSHA string, now time.Time) Thread {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.Architect, t.BaseSHA, t.UpdatedAt = architect, baseSHA, now
	// An empty handle does not erase a known one: providers report a thread
	// only on some turns, and forgetting it would manufacture a loss of
	// continuity that did not happen.
	if strings.TrimSpace(threadID) != "" {
		t.ThreadID = threadID
	}
	t.Turns++
	return t
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func path(repoRoot string) string {
	return filepath.Join(repoRoot, ".sensei-code", "architect-thread.json")
}

// Load reads the recorded conversation. A missing or unreadable record is a
// conversation that has to be reconstructed, not an error worth failing a turn
// over: the cost of losing this file is continuity, and continuity is not
// correctness.
func Load(repoRoot string) Thread {
	body, err := os.ReadFile(path(repoRoot))
	if err != nil {
		return Thread{Workspace: repoRoot}
	}
	var t Thread
	if err := json.Unmarshal(body, &t); err != nil {
		return Thread{Workspace: repoRoot}
	}
	t.Workspace = repoRoot
	return t
}

// Save writes the conversation record.
func (t Thread) Save(repoRoot string) error {
	target := path(repoRoot)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(body, '\n'), 0o644)
}
