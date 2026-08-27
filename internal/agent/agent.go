package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/processx"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/roles"
)

// Role names the job this request is for. The vocabulary lives in
// internal/roles because it is semantic: a role is a job with its own context
// and its own session rule, not a synonym for whichever provider is configured
// to do it today.
type Role = roles.Role

type Request struct {
	Role      Role
	TaskID    string
	Workspace string
	Prompt    string
	// Session says whether this turn may inherit the architectural
	// conversation. It defaults to the role's own rule rather than to
	// "continue", so an adversarial role that nobody remembered to configure
	// still starts clean.
	Session roles.Session
	// Graph is the awareness graph the engine verified for this run, handed to
	// the agent as an execution-scoped MCP binding.
	//
	// The first governed run against a foreign repository had the ENGINE bound
	// to that repository's graph and the ARCHITECT bound, through its own
	// global ~/.codex/config.toml, to a different one. The investigator was
	// reasoning over a substrate the engine had never admitted, and nothing
	// noticed. So the binding is now part of the request, and each provider is
	// launched so that it can reach this graph and no other. Nil means the
	// caller made no claim, and the provider's own configuration applies.
	Graph *GraphBinding
}

// GraphBinding is one verified graph, as an MCP server the agent may reach.
type GraphBinding struct {
	// Command and Args launch the same MCP the engine itself used.
	Command string
	Args    []string
	// Domain and Digest identify what the engine verified. Recorded so an
	// agent's output can be checked against the graph it was meant to see.
	Domain string
	Digest string
}

// CodexOverrides renders the binding as `codex -c` overrides, which apply to
// every codex subcommand including app-server and take precedence over the
// user's global config.
func (g GraphBinding) CodexOverrides() []string {
	args := make([]string, 0, len(g.Args))
	for _, a := range g.Args {
		args = append(args, strconv.Quote(a))
	}
	return []string{
		"-c", "mcp_servers.sensei.command=" + strconv.Quote(g.Command),
		"-c", "mcp_servers.sensei.args=[" + strings.Join(args, ",") + "]",
	}
}

// ClaudeMCPConfig renders the binding as the JSON `claude --mcp-config` reads.
func (g GraphBinding) ClaudeMCPConfig() []byte {
	b, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"sensei": map[string]any{"command": g.Command, "args": g.Args}}})
	return b
}

// DivergenceIn reports evidence, in an agent's own output, that it reached a
// graph other than the one bound.
//
// The bound graph holds Domain; a graph that answers "unknown domain scope"
// for it is a different graph. This is the after-the-fact check; the strict
// per-run configuration is the structural one.
func (g GraphBinding) DivergenceIn(output string) string {
	if g.Domain == "" {
		return ""
	}
	if strings.Contains(output, "unknown domain scope \\\""+g.Domain) ||
		strings.Contains(output, "unknown domain scope \""+g.Domain) {
		return "the agent reached a graph that does not hold " + g.Domain
	}
	return ""
}

// session resolves the mode actually used, so the caller records what happened
// rather than what it asked for.
//
// An adversarial role is Fresh whatever the caller asked for. Independence is
// the property that makes its verdict worth anything, and a field a caller can
// set to Continue is a field that will eventually be set to Continue -- by a
// refactor, by a copied struct literal, by somebody threading a session id
// through for an unrelated reason. Making it unsettable is cheaper than
// noticing later that reviews stopped being independent.
func (r Request) session() roles.Session {
	if r.Role.Adversarial() {
		return roles.Fresh
	}
	if r.Session == roles.Continue || r.Session == roles.Fresh {
		return r.Session
	}
	return r.Role.SessionMode()
}

type Result struct {
	Text      string
	SessionID string
	// Session is the mode this turn actually ran in. It is returned rather than
	// assumed because independence is a claim, and a claim about independence
	// has to be checkable after the fact -- deriving it from the fact that the
	// call succeeded would only prove that the call succeeded.
	Session roles.Session
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

	// ChatGPT is a first-class architectural provider, not a synonym for a
	// one-shot `codex exec`. Codex app-server is only the transport to the
	// authenticated ChatGPT subscription. Machine turns are ephemeral either
	// way, so a JSON contract never lands in the human's conversation; what
	// differs by role is whether the turn inherits that conversation at all.
	if strings.EqualFold(strings.TrimSpace(c.Name), string(provider.ChatGPT)) {
		if req.Role.Mutates() {
			return Result{}, fmt.Errorf("ChatGPT provider is read-only architectural authority, not an implementation worker")
		}
		if !req.Role.Valid() {
			return Result{}, fmt.Errorf("unknown role %q", req.Role)
		}
		session := provider.ChatGPTForWorkspace(req.Workspace)
		if req.Graph != nil {
			if err := session.BindGraph(req.Graph.CodexOverrides()); err != nil {
				return Result{}, err
			}
		}
		var text string
		var err error
		if mode := req.session(); mode == roles.Fresh {
			// An adversarial role must not read the case for the work before
			// judging it. A fork would hand it exactly that.
			text, err = session.AskIndependent(ctx, req.Prompt)
		} else {
			text, err = session.AskFork(ctx, req.Prompt)
		}
		if err != nil {
			return Result{}, err
		}
		if req.Graph != nil {
			if why := req.Graph.DivergenceIn(text); why != "" {
				return Result{}, fmt.Errorf("graph binding violated: %s", why)
			}
		}
		for _, line := range strings.Split(text, "\n") {
			emit(event.New(c.SessionID, req.TaskID, c.Source, event.Output, line, map[string]string{"stream": "assistant"}))
		}
		emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentFinished, c.label()+" finished", nil))
		return Result{Text: text, Session: req.session()}, nil
	}

	args := append([]string(nil), c.Args...)
	if req.Graph != nil {
		bound, err := c.bindGraphArgs(req, args)
		if err != nil {
			return Result{}, err
		}
		args = bound
	}
	var out strings.Builder
	_, err := processx.RunWithEnv(ctx, req.Workspace, c.Command, args, c.Env, c.UnsetEnv, bytes.NewBufferString(req.Prompt), func(line processx.Line) {
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
	if req.Graph != nil {
		if why := req.Graph.DivergenceIn(out.String()); why != "" {
			return Result{}, fmt.Errorf("graph binding violated: %s", why)
		}
	}
	emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentFinished, c.label()+" finished", nil))
	// A one-shot CLI turn inherits nothing by construction: the process is new,
	// and no resume handle is passed to it. That is reported as Fresh rather
	// than as whatever was requested, because what the caller wanted and what
	// the transport did are different facts and only the second one is evidence.
	return Result{Text: text, SessionID: sid, Session: roles.Fresh}, nil
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

// bindGraphArgs launches a CLI provider so it can reach the bound graph and no
// other.
//
// claude: an execution-scoped --mcp-config with --strict-mcp-config, so every
// other MCP source -- user, project, plugin -- is ignored for this turn.
// codex exec: -c overrides, which precede the user's config.toml.
// Anything else: refused, because a provider that cannot be bound would run
// against whatever it finds, and "unbound" must not look like "bound".
func (c CLI) bindGraphArgs(req Request, args []string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(c.Name)) {
	case "claude":
		dir := filepath.Join(req.Workspace, ".sensei-code")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "mcp-"+sanitize(req.TaskID)+".json")
		if err := os.WriteFile(path, req.Graph.ClaudeMCPConfig(), 0o644); err != nil {
			return nil, err
		}
		return append(args, "--mcp-config", path, "--strict-mcp-config"), nil
	case "codex":
		return append(req.Graph.CodexOverrides(), args...), nil
	}
	return nil, fmt.Errorf("provider %q cannot be bound to the verified graph; refusing to run it unbound", c.Name)
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}
