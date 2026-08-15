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
	Present      Presence = "present"
	EmptyProven  Presence = "empty-proven"
	Absent       Presence = "absent"
	Stale        Presence = "stale"
	Unavailable  Presence = "unavailable"
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
	preflight, err := caller.CallTool("awareness_preflight", map[string]any{
		"task":  task,
		"files": cleanFiles(files),
		"mode":  "compact",
	})
	if err != nil {
		return ContextPacket{}, fmt.Errorf("Sensei preflight: %w", err)
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
		WorkspaceStatus: observed("sensei:sensei_workspace_status", workspace),
		Preflight:       observed("sensei:awareness_preflight", preflight),
		Authority: Authority{
			Mode:      "assisted",
			Admission: "not-requested",
		},
	}, nil
}

func observed(source string, result sensei.ToolResult) Observation {
	return Observation{
		State:      Present,
		Source:     source,
		Text:       firstText(result),
		Structured: result.Structured,
	}
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
	if o.State == Unavailable && strings.TrimSpace(o.Reason) == "" {
		return errors.New("unavailable observation must explain why")
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
