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
	// Binding is the exact artifact this turn is about: task, base, candidate
	// digest and tree.
	//
	// Carried by the request rather than reconstructed by whoever answers,
	// because the producer must not author its own subject. A local CLI turn
	// does not need it -- the engine stamps provenance itself when the process
	// returns -- but a turn answered over a transport does: the far side has to
	// be told which object it is being asked about, and a late answer has to be
	// checkable against it. Zero means the caller asserted nothing.
	Binding roles.Binding
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
	// ReviewDigest names the exact artifact a transport carried, when it carried
	// one it can name. It is IDENTITY, never standing: it says which bytes this
	// text came from, so a later record about that review -- a human attestation,
	// say -- can be checked against the review it claims to be about instead of
	// against its own say-so. Empty means the turn asserted nothing.
	ReviewDigest string
	// Invocation is what the concrete adapter observed about this provider
	// invocation. It is returned on error-bearing returns too, because an
	// error does not unsay what the process already wrote. Nil means the
	// adapter observed nothing, never that the invocation returned. A pointer
	// keeps Result comparable.
	Invocation *Invocation
}

// Invocation is one provider invocation as the concrete adapter observed it.
// It holds observations only: whether completion was reached is decided by
// whoever settles it, and never from the assistant's prose.
type Invocation struct {
	// Transport is the adapter actually used, with the capabilities that
	// adapter has. It is a property of the invocation, not of the provider.
	Transport Transport
	// TransportFailed means the invocation failed before it produced any
	// result: the process never started, or the adapter refused to run it.
	TransportFailed bool
	// Exited and ExitCode are the direct process outcome, when there was a
	// process and it was seen to exit.
	Exited   bool
	ExitCode int
	// Returned is true only when an invocation result was actually observed:
	// the return envelope of a structured transport, or the completed output
	// of a plain-text one. A structured process that exited without its
	// envelope did not return one.
	Returned bool
	// Report is the report text the invocation actually returned.
	Report string
	// Lifecycle is the structured operation observations, in the order the
	// transport emitted them. Empty for a transport without that capability.
	Lifecycle []LifecycleObservation
}

// Transport names the concrete adapter of one invocation.
type Transport struct {
	Adapter string
	// Lifecycle says the adapter emits structured lifecycle events. Only an
	// invocation whose transport has it may yield lifecycle observations.
	Lifecycle bool
}

// The concrete adapters this package can observe.
const (
	AdapterStreamJSON     = "cli-stream-json"
	AdapterPlainText      = "cli-plain-text"
	AdapterCodexAppServer = "codex-app-server"
)

// LifecycleKind is the kind of one structured operation observation.
type LifecycleKind string

const (
	LifecycleStarted  LifecycleKind = "started"
	LifecycleUpdate   LifecycleKind = "update"
	LifecycleTerminal LifecycleKind = "terminal"
)

// LifecycleObservation is one structured operation event. Seq is its position
// in the invocation's stream; Operation is the id the transport gave it, which
// is not assumed unique.
type LifecycleObservation struct {
	Seq       int
	Kind      LifecycleKind
	Operation string
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
	// ConsumesGraph is false only for a provider whose config declares
	// graph: none. Such a provider runs unbound because it has no graph to
	// diverge from; every other provider must be bound or refused.
	NoGraph bool
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
		// Refused before any turn was asked: no result exists to observe.
		refused := Result{Invocation: &Invocation{Transport: Transport{Adapter: AdapterCodexAppServer}, TransportFailed: true}}
		if req.Role.Mutates() {
			return refused, fmt.Errorf("ChatGPT provider is read-only architectural authority, not an implementation worker")
		}
		if !req.Role.Valid() {
			return refused, fmt.Errorf("unknown role %q", req.Role)
		}
		session := provider.ChatGPTForWorkspace(req.Workspace)
		if req.Graph != nil {
			if err := session.BindGraph(req.Graph.CodexOverrides()); err != nil {
				return refused, err
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
			// The session reports the error without saying whether a turn
			// result existed, so neither a return nor a pre-result failure is
			// claimed.
			return Result{Invocation: &Invocation{Transport: Transport{Adapter: AdapterCodexAppServer}}}, err
		}
		returned := &Invocation{Transport: Transport{Adapter: AdapterCodexAppServer}, Returned: true, Report: text}
		if req.Graph != nil {
			if why := req.Graph.DivergenceIn(text); why != "" {
				return Result{Invocation: returned}, fmt.Errorf("graph binding violated: %s", why)
			}
		}
		for _, line := range strings.Split(text, "\n") {
			emit(event.New(c.SessionID, req.TaskID, c.Source, event.Output, line, map[string]string{"stream": "assistant"}))
		}
		emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentFinished, c.label()+" finished", nil))
		return Result{Text: text, Session: req.session(), Invocation: returned}, nil
	}

	args := append([]string(nil), c.Args...)
	if req.Graph != nil {
		bound, err := c.bindGraphArgs(req, args)
		if err != nil {
			return Result{Invocation: &Invocation{Transport: cliTransport(args), TransportFailed: true}}, err
		}
		args = bound
	}
	inv := &Invocation{Transport: cliTransport(args)}
	var out strings.Builder
	proc, err := processx.RunWithEnv(ctx, req.Workspace, c.Command, args, c.Env, c.UnsetEnv, bytes.NewBufferString(req.Prompt), func(line processx.Line) {
		if line.Stream == "stdout" {
			out.WriteString(line.Text)
			out.WriteByte('\n')
		}
		emit(event.New(c.SessionID, req.TaskID, c.Source, event.Output, line.Text, map[string]string{"stream": line.Stream}))
	})
	if err != nil && proc.ExitCode == 0 {
		// processx reports an exit only with its code; an error without one
		// is a process that never started or was never waited for.
		inv.TransportFailed = true
		return Result{Invocation: inv}, err
	}
	inv.Exited, inv.ExitCode = true, proc.ExitCode
	text, sid := readOutput(c.Name, inv, out.String())
	if err != nil {
		return Result{Invocation: inv}, err
	}
	if req.Graph != nil {
		if why := req.Graph.DivergenceIn(out.String()); why != "" {
			return Result{Invocation: inv}, fmt.Errorf("graph binding violated: %s", why)
		}
	}
	emit(event.New(c.SessionID, req.TaskID, c.Source, event.AgentFinished, c.label()+" finished", nil))
	// A one-shot CLI turn inherits nothing by construction: the process is new,
	// and no resume handle is passed to it. That is reported as Fresh rather
	// than as whatever was requested, because what the caller wanted and what
	// the transport did are different facts and only the second one is evidence.
	return Result{Text: text, SessionID: sid, Session: roles.Fresh, Invocation: inv}, nil
}

// cliTransport derives the transport from the arguments the process is
// actually launched with. The provider's name plays no part: a "claude" run
// without --output-format stream-json speaks plain text, and its output is
// read as plain text.
func cliTransport(args []string) Transport {
	for i, a := range args {
		if a == "--output-format=stream-json" || a == "--output-format" && i+1 < len(args) && args[i+1] == "stream-json" {
			return Transport{Adapter: AdapterStreamJSON, Lifecycle: true}
		}
	}
	return Transport{Adapter: AdapterPlainText}
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

// readOutput records what the process wrote into inv and returns the text
// callers have always received: the return envelope's result for a structured
// transport, falling back to the raw output, and the trimmed output otherwise.
func readOutput(name string, inv *Invocation, raw string) (string, string) {
	if !inv.Transport.Lifecycle {
		inv.Returned = true
		inv.Report = strings.TrimSpace(raw)
		return inv.Report, ""
	}
	var sid string
	for _, line := range strings.Split(raw, "\n") {
		var envelope struct {
			Type      string  `json:"type"`
			SessionID string  `json:"session_id"`
			Result    *string `json:"result"`
			ToolUseID string  `json:"tool_use_id"`
			// ParentToolUseID links a progress record to the operation it
			// reports on; the record's own tool_use_id is a heartbeat id.
			ParentToolUseID string `json:"parent_tool_use_id"`
			Message         struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &envelope) != nil {
			continue
		}
		if envelope.SessionID != "" {
			sid = envelope.SessionID
		}
		switch envelope.Type {
		case "result":
			inv.Returned = true
			inv.Report = ""
			if envelope.Result != nil {
				inv.Report = *envelope.Result
			}
		case "tool_progress":
			operation := envelope.ParentToolUseID
			if operation == "" {
				operation = envelope.ToolUseID
			}
			inv.observe(LifecycleUpdate, operation)
		case "assistant", "user":
			// content is a string for a plain user turn, and carries no
			// operations then.
			var parts []struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				ToolUseID string `json:"tool_use_id"`
			}
			if json.Unmarshal(envelope.Message.Content, &parts) != nil {
				continue
			}
			for _, part := range parts {
				switch {
				case envelope.Type == "assistant" && part.Type == "tool_use":
					inv.observe(LifecycleStarted, part.ID)
				case envelope.Type == "user" && part.Type == "tool_result":
					inv.observe(LifecycleTerminal, part.ToolUseID)
				}
			}
		}
	}
	text := inv.Report
	if text == "" {
		text = strings.TrimSpace(raw)
	}
	if text == "" {
		text = fmt.Sprintf("%s completed without text output", name)
	}
	return text, sid
}

func (inv *Invocation) observe(kind LifecycleKind, operation string) {
	if strings.TrimSpace(operation) == "" {
		return
	}
	inv.Lifecycle = append(inv.Lifecycle, LifecycleObservation{Seq: len(inv.Lifecycle), Kind: kind, Operation: operation})
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
	if c.NoGraph {
		return args, nil
	}
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
