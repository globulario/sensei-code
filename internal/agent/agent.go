package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/processx"
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
	// Name is the load-bearing identifier: output normalization matches on it.
	Name string
	// Label is what humans see. It defaults to Name when unset, and is kept
	// separate so renaming for display can never change parsing behaviour.
	Label     string
	Command   string
	Args      []string
	Source    event.Source
	SessionID string
	// Env are extra environment entries for this agent's process, used to
	// enforce capability boundaries the agent must not be able to talk its way
	// past.
	Env []string
	// UnsetEnv are variables removed from the agent's environment so it
	// authenticates with its own stored session.
	UnsetEnv []string
}

func (c CLI) label() string {
	if strings.TrimSpace(c.Label) != "" {
		return c.Label
	}
	return c.Name
}

func (c CLI) Run(ctx context.Context, req Request, emit func(event.Event)) (Result, error) {
	emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentStarted, c.label()+" started", nil))
	var out strings.Builder
	_, err := processx.RunWithEnv(ctx, req.Workspace, c.Command, c.Args, c.Env, c.UnsetEnv, bytes.NewBufferString(req.Prompt), func(line processx.Line) {
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
	emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentFinished, c.label()+" finished", nil))
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
