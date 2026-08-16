package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/processx"
	"github.com/globulario/sensei-code/internal/provider"
)

type Role string

const (
	Architect   Role = "architect"
	Implementor Role = "implementor"
	Reviewer    Role = "reviewer"
)

type Request struct {
	Role      Role
	TaskID    string
	Workspace string
	Prompt    string
}

type Result struct {
	Text      string
	SessionID string
}

type Runner interface {
	Run(context.Context, Request, func(event.Event)) (Result, error)
}

type CLI struct {
	Name      string
	Command   string
	Args      []string
	Source    event.Source
	SessionID string
}

func (c CLI) Run(ctx context.Context, req Request, emit func(event.Event)) (Result, error) {
	emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentStarted, c.Name+" started", nil))

	// ChatGPT is a first-class architectural provider, not a synonym for a
	// one-shot `codex exec`. Codex app-server is only the transport to the
	// authenticated ChatGPT subscription. Architectural/review machine turns
	// use an ephemeral fork of the human conversation so they inherit context
	// without filling the visible chat with JSON contracts.
	if strings.EqualFold(strings.TrimSpace(c.Name), string(provider.ChatGPT)) {
		if req.Role != Architect && req.Role != Reviewer {
			return Result{}, fmt.Errorf("ChatGPT provider is read-only architectural authority, not an implementation worker")
		}
		text, err := provider.ChatGPTForWorkspace(req.Workspace).AskFork(ctx, req.Prompt)
		if err != nil {
			return Result{}, err
		}
		for _, line := range strings.Split(text, "\n") {
			emit(event.New(c.SessionID, req.TaskID, c.Source, event.Output, line, map[string]string{"stream": "assistant"}))
		}
		emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentFinished, c.Name+" finished", nil))
		return Result{Text: text}, nil
	}

	var out strings.Builder
	_, err := processx.Run(ctx, req.Workspace, c.Command, c.Args, bytes.NewBufferString(req.Prompt), func(line processx.Line) {
		if line.Stream == "stdout" {
			out.WriteString(line.Text)
			out.WriteByte('\n')
		}
		emit(event.New(c.SessionID, req.TaskID, c.Source, event.Output, line.Text, map[string]string{"stream": line.Stream}))
	})
	if err != nil {
		return Result{}, err
	}
	text, sid := normalizeOutput(c.Name, out.String())
	emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentFinished, c.Name+" finished", nil))
	return Result{Text: text, SessionID: sid}, nil
}

func normalizeOutput(name, raw string) (string, string) {
	if name != "claude" {
		return strings.TrimSpace(raw), ""
	}
	var result, sid string
	for _, line := range strings.Split(raw, "\n") {
		var v map[string]any
		if json.Unmarshal([]byte(line), &v) != nil {
			continue
		}
		if s, _ := v["session_id"].(string); s != "" {
			sid = s
		}
		if v["type"] == "result" {
			if s, _ := v["result"].(string); s != "" {
				result = s
			}
		}
	}
	if result == "" {
		result = strings.TrimSpace(raw)
	}
	if result == "" {
		result = fmt.Sprintf("%s completed without text output", name)
	}
	return result, sid
}
