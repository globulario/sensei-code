package assist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/sensei"
)

const PacketVersion = 1

type Presence string

const (
	Present     Presence = "present"
	EmptyProven Presence = "empty-proven"
	Absent      Presence = "absent"
	Stale       Presence = "stale"
	Unavailable Presence = "unavailable"
)

type Observation struct {
	State      Presence       `json:"state"`
	Source     string         `json:"source"`
	Text       string         `json:"text,omitempty"`
	Structured map[string]any `json:"structured,omitempty"`
	Reason     string         `json:"reason,omitempty"`
}

type Authority struct {
	Mode      string `json:"mode"`
	Admission string `json:"admission"`
}

type ContextPacket struct {
	Version         int         `json:"version"`
	TaskID          string      `json:"task_id"`
	Task            string      `json:"task"`
	Repository      string      `json:"repository"`
	BaseSHA         string      `json:"base_sha"`
	Files           []string    `json:"files,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	WorkspaceStatus Observation `json:"workspace_status"`
	Preflight       Observation `json:"preflight"`
	Authority       Authority   `json:"authority"`
}

type SenseiCaller interface {
	CallTool(name string, args map[string]any) (sensei.ToolResult, error)
}

func Build(ctx context.Context, repo gitx.Repo, caller SenseiCaller, taskID, task string, files []string, now time.Time) (ContextPacket, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return ContextPacket{}, errors.New("task is empty")
	}
	if caller == nil {
		return ContextPacket{}, errors.New("Sensei caller is nil")
	}
	baseSHA, err := repo.Head(ctx)
	if err != nil {
		return ContextPacket{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	workspace, err := caller.CallTool("sensei_workspace_status", map[string]any{"repo": repo.Root})
	if err != nil {
		return ContextPacket{}, fmt.Errorf("Sensei workspace status: %w", err)
	}
	preflightArgs := map[string]any{
		"task":  task,
		"files": cleanFiles(files),
		"mode":  "compact",
	}
	// Sensei already stated the repository domain in the workspace identity
	// receipt. Carry that value through instead of leaving preflight unscoped:
	// a graph hosting more than one domain cannot scope itself, and answers
	// with a domain_scope blind spot. The domain is Sensei's fact, never
	// re-derived here.
	if domain := sensei.RepositoryDomain(workspace); domain != "" {
		preflightArgs["domain"] = domain
	}
	preflight, err := caller.CallTool("awareness_preflight", preflightArgs)
	if err != nil {
		return ContextPacket{}, fmt.Errorf("Sensei preflight: %w", err)
	}
	workspaceStatus := observed("sensei:sensei_workspace_status", workspace)
	if err := requireEvidence("workspace status", workspaceStatus); err != nil {
		return ContextPacket{}, err
	}
	preflightStatus := observed("sensei:awareness_preflight", preflight)
	if err := requireEvidence("preflight", preflightStatus); err != nil {
		return ContextPacket{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	return ContextPacket{
		Version:         PacketVersion,
		TaskID:          strings.TrimSpace(taskID),
		Task:            task,
		Repository:      repo.Root,
		BaseSHA:         strings.TrimSpace(baseSHA),
		Files:           cleanFiles(files),
		CreatedAt:       now,
		WorkspaceStatus: workspaceStatus,
		Preflight:       preflightStatus,
		Authority: Authority{
			Mode:      "assisted",
			Admission: "not-requested",
		},
	}, nil
}

// observed classifies what Sensei actually returned.
//
// A transport success is not itself evidence. The four outcomes below are
// genuinely different pieces of information and were, until this function
// distinguished them, all reported as Present:
//
//   - Absent: Sensei said nothing. No evidence either way.
//   - Stale: Sensei answered, and told us its own graph is not current. The
//     content is readable but must not be relied on. This is the dangerous one,
//     because a stale graph answers fluently and specifically; nothing about
//     the text looks wrong.
//   - EmptyProven: Sensei answered, its graph is sound, and the answer is that
//     there is nothing here. This is a real finding — the region is uncovered —
//     and must not be confused with Absent, which is the absence of a finding.
//   - Present: Sensei answered with content, from a graph that vouches for it.
//
// Staleness is checked before emptiness, because "nothing here" from a graph
// that cannot vouch for itself is not proof of anything.
func observed(source string, result sensei.ToolResult) Observation {
	text := firstText(result)
	if strings.TrimSpace(text) == "" && len(result.Structured) == 0 {
		return Observation{
			State:  Absent,
			Source: source,
			Reason: "Sensei returned neither text nor structured content",
		}
	}
	if authority, ok := sensei.AuthorityOf(result); ok && !authority.Certifiable() {
		return Observation{
			State:      Stale,
			Source:     source,
			Reason:     authority.Diagnostic(),
			Text:       text,
			Structured: result.Structured,
		}
	}
	if reason, ok := sensei.ProvenEmpty(result); ok {
		return Observation{
			State:      EmptyProven,
			Source:     source,
			Reason:     reason,
			Text:       text,
			Structured: result.Structured,
		}
	}
	return Observation{
		State:      Present,
		Source:     source,
		Text:       text,
		Structured: result.Structured,
	}
}

// requireEvidence fails closed when a required Sensei surface produced nothing
// certifiable. Without it an empty answer would be carried forward as though
// Sensei had supplied context.
func requireEvidence(label string, o Observation) error {
	switch o.State {
	case Present:
		return nil
	case EmptyProven:
		// A proven empty is an answer, not a missing one. Sensei's graph
		// vouched for itself and reported that this scope has no coverage,
		// which is a fact the packet should carry rather than a failure that
		// should stop the work.
		return nil
	case Stale:
		// A stale answer is also an answer, and this packet type was built to
		// say so: validateObservation accepts Stale provided it explains why.
		// Refusing here made the packet unable to express the one state it has
		// a name for, and turned an honest degradation into a hard failure --
		// which is what broke advisory CI, where a freshly built graph is
		// routinely not yet authoritative.
		//
		// The refusal belongs in the governed path, and lives there:
		// certifyStart requires Authority.Certifiable() before any worker runs.
		// Assisted context has different authority and therefore a different
		// obligation. It must never claim to be graph-backed when it is not,
		// and it must not withhold what Sensei did say. Carrying the state and
		// the reason satisfies both; refusing satisfies only the first, by
		// saying nothing at all.
		return nil
	}
	return fmt.Errorf("Sensei %s is %s: %s", label, o.State, o.Reason)
}

// GraphBacked reports whether every required observation came from a graph that
// vouched for itself. A packet that is not graph-backed is still useful; it is
// simply not evidence, and a consumer must not present it as such.
func (p ContextPacket) GraphBacked() bool {
	for _, o := range []Observation{p.WorkspaceStatus, p.Preflight} {
		if o.State != Present && o.State != EmptyProven {
			return false
		}
	}
	return true
}

// Caveats names every way this packet falls short of graph-backed evidence, in
// the words a reader needs. Empty when there are none.
//
// This exists because the packet is handed to a reviewer under a heading that
// calls it governed evidence. When it is not, something has to say so in a
// place the reader cannot skip.
func (p ContextPacket) Caveats() []string {
	var out []string
	for _, o := range []struct {
		label string
		obs   Observation
	}{{"workspace status", p.WorkspaceStatus}, {"preflight", p.Preflight}} {
		switch o.obs.State {
		case Present, EmptyProven:
		default:
			reason := o.obs.Reason
			if strings.TrimSpace(reason) == "" {
				reason = "no reason given"
			}
			out = append(out, fmt.Sprintf("%s is %s: %s", o.label, o.obs.State, reason))
		}
	}
	return out
}

func UnavailableObservation(source, reason string) Observation {
	return Observation{State: Unavailable, Source: source, Reason: strings.TrimSpace(reason)}
}

func (p ContextPacket) Validate() error {
	if p.Version != PacketVersion {
		return fmt.Errorf("unsupported context packet version %d", p.Version)
	}
	if strings.TrimSpace(p.Task) == "" || strings.TrimSpace(p.Repository) == "" || strings.TrimSpace(p.BaseSHA) == "" {
		return errors.New("context packet is missing task, repository, or base SHA")
	}
	// Task identity is what makes a packet traceable across agents. An empty id
	// silently collapses every task into the same anonymous one, and a handoff
	// comparing two empty ids would call them a match.
	if strings.TrimSpace(p.TaskID) == "" {
		return errors.New("context packet is missing a task id")
	}
	if p.Authority.Mode != "assisted" || p.Authority.Admission != "not-requested" {
		return errors.New("assisted context packet must not claim governed admission")
	}
	if err := validateObservation(p.WorkspaceStatus); err != nil {
		return fmt.Errorf("workspace status: %w", err)
	}
	if err := validateObservation(p.Preflight); err != nil {
		return fmt.Errorf("preflight: %w", err)
	}
	return nil
}

func (p ContextPacket) Digest() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (p ContextPacket) Write(path string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func ReadContext(path string) (ContextPacket, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ContextPacket{}, err
	}
	var p ContextPacket
	if err := json.Unmarshal(b, &p); err != nil {
		return ContextPacket{}, err
	}
	if err := p.Validate(); err != nil {
		return ContextPacket{}, err
	}
	return p, nil
}

func validateObservation(o Observation) error {
	switch o.State {
	case Present, EmptyProven, Absent, Stale, Unavailable:
	default:
		return fmt.Errorf("unknown presence state %q", o.State)
	}
	if strings.TrimSpace(o.Source) == "" {
		return errors.New("observation source is empty")
	}
	switch o.State {
	case Absent, Stale, Unavailable:
		if strings.TrimSpace(o.Reason) == "" {
			return fmt.Errorf("%s observation must explain why", o.State)
		}
	}
	return nil
}

func firstText(result sensei.ToolResult) string {
	for _, item := range result.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			return item.Text
		}
	}
	return ""
}

func cleanFiles(files []string) []string {
	out := make([]string, 0, len(files))
	seen := map[string]struct{}{}
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		out = append(out, file)
	}
	return out
}
