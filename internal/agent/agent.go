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

// Activity renders one line of an agent's output as something a human can
// follow. Claude speaks stream-json, so its raw output is a wall of envelopes:
// one task emitted over a thousand of them. Showing those verbatim is not
// visibility, it is noise, and the architect cannot see a worker going wrong in
// it. Returning "" drops a line that carries nothing worth reading.
//
// This decodes rather than interprets. It reports the tool the worker invoked
// and the file it touched, which are facts. It does not ask a model what the
// worker was doing, because a narrated summary of an agent's work is exactly
// the kind of claim this project refuses to trust elsewhere.
func Activity(name, line string) string {
	if name != "claude" {
		return strings.TrimSpace(line)
	}
	var envelope struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &envelope) != nil {
		return ""
	}
	if envelope.Type != "assistant" {
		return ""
	}
	var out []string
	for _, part := range envelope.Message.Content {
		switch part.Type {
		case "text":
			if text := firstSentence(part.Text); text != "" {
				out = append(out, text)
			}
		case "tool_use":
			if target := toolTarget(part.Input); target != "" {
				out = append(out, part.Name+"("+target+")")
			} else {
				out = append(out, part.Name)
			}
		}
	}
	return strings.Join(out, " ")
}

// toolTarget picks the argument a reader cares about: which file, or which
// command. Everything else is detail the transcript does not need.
func toolTarget(input json.RawMessage) string {
	var args map[string]any
	if json.Unmarshal(input, &args) != nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "notebook_path"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			// Paths are trimmed from the left. A worktree prefix is the same on
			// every line and the filename is the only part worth reading, so
			// cutting the tail would hide exactly what the architect is looking
			// for.
			return truncateLeft(value, 60)
		}
	}
	for _, key := range []string{"command", "pattern", "query", "prompt", "description"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return truncate(strings.TrimSpace(value), 70)
		}
	}
	return ""
}

func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return truncate(text, 100)
}

// truncateLeft keeps the end of a string, which for a path is the part that
// identifies it.
func truncateLeft(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return "…" + s[len(s)-limit+1:]
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit-1] + "…"
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
