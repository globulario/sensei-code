package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// sandboxReadOnly is the codex app-server's spelling for a read-only sandbox.
//
// It is hyphenated. The app-server rejects "readOnly" outright -- "unknown
// variant `readOnly`, expected one of `read-only`, `workspace-write`,
// `danger-full-access`" -- and it rejected it on every architect turn, which
// meant the ChatGPT architect could not start a thread at all. Unit tests did
// not catch it because none of them speak to a real app-server; the governed
// acceptance run found it on its first execution.
//
// Both call sites use this constant so the two cannot drift apart again.
const sandboxReadOnly = "read-only"

const (
	// ChatGPTArchitectModel is deliberately pinned for the first Sensei Code
	// architect. Codex app-server is the transport; ChatGPT is the authority.
	ChatGPTArchitectModel  = "gpt-5.6-sol"
	ChatGPTArchitectEffort = "high"
)

type ChatGPTSession struct {
	mu sync.Mutex

	cwd      string
	model    string
	effort   string
	server   *appServer
	threadID string
	// hasHistory records whether the base thread has taken a turn. A thread
	// with no rollout cannot be forked.
	hasHistory  bool
	personality bool
}

var (
	chatGPTSessionsMu sync.Mutex
	chatGPTSessions   = map[string]*ChatGPTSession{}
)

// ChatGPTForWorkspace returns the process-local architectural conversation for
// a repository. Direct human conversation and governed architectural forks use
// the same root thread, so an execution request can inherit the design dialogue
// without making the worker or reviewer the architectural authority.
func ChatGPTForWorkspace(workspace string) *ChatGPTSession {
	key := filepath.Clean(workspace)
	chatGPTSessionsMu.Lock()
	defer chatGPTSessionsMu.Unlock()
	if s := chatGPTSessions[key]; s != nil {
		return s
	}
	s := &ChatGPTSession{
		cwd:    key,
		model:  ChatGPTArchitectModel,
		effort: ChatGPTArchitectEffort,
	}
	chatGPTSessions[key] = s
	return s
}

// Ask adds a turn to the human-facing architectural conversation.
func (s *ChatGPTSession) Ask(ctx context.Context, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("ChatGPT architect prompt is empty")
	}
	if err := s.ensureStarted(ctx); err != nil {
		return "", err
	}
	text, err := s.runTurn(s.threadID, prompt)
	if err == nil {
		// The base thread now has a rollout, so later machine turns can fork it
		// and inherit the human conversation.
		s.hasHistory = true
	}
	return text, err
}

// AskFork asks the same architect with all conversation history, but on an
// ephemeral branch. Machine-only JSON contracts therefore do not pollute the
// human-facing conversation while still inheriting its architectural context.
func (s *ChatGPTSession) AskFork(ctx context.Context, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("ChatGPT architect prompt is empty")
	}
	if err := s.ensureStarted(ctx); err != nil {
		return "", err
	}
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	// A thread that has never taken a turn has no rollout on disk, and the
	// app-server refuses to fork one: "no rollout found for thread id ...".
	// That is every first machine turn, because machine turns always fork and
	// therefore never write to the base thread themselves — so without this the
	// architect could never answer at all.
	//
	// When there is no history there is also nothing to inherit, so a fresh
	// ephemeral thread is the same answer with none of the failure. The base
	// thread stays clean either way, which is the point of forking: a JSON
	// contract must not land in the human's conversation.
	if !s.hasHistory {
		id, err := startThread(s.server, s.model, s.cwd, s.personality)
		if err != nil {
			return "", fmt.Errorf("start an ephemeral ChatGPT architect thread: %w", err)
		}
		return s.runTurn(id, prompt)
	}
	if err := s.server.call("thread/fork", map[string]any{
		"threadId":  s.threadID,
		"ephemeral": true,
	}, &result); err != nil {
		return "", fmt.Errorf("fork ChatGPT architect conversation: %w", err)
	}
	if strings.TrimSpace(result.Thread.ID) == "" {
		return "", errors.New("ChatGPT app-server returned an empty fork thread id")
	}
	return s.runTurn(result.Thread.ID, prompt)
}

func (s *ChatGPTSession) ensureStarted(ctx context.Context) error {
	if s.server != nil && s.threadID != "" {
		return nil
	}

	c, err := startAppServer(ctx)
	if err != nil {
		return fmt.Errorf("start ChatGPT app-server: %w", err)
	}
	fail := func(err error) error {
		c.Close()
		return err
	}

	account, err := c.readAccount()
	if err != nil {
		return fail(fmt.Errorf("read ChatGPT account: %w", err))
	}
	if !account.Authenticated || account.Type != "chatgpt" {
		return fail(errors.New("ChatGPT architect requires an authenticated ChatGPT subscription; run `sensei-code login chatgpt`"))
	}

	model, effort, personality, err := chooseArchitectModel(c, s.model, s.effort)
	if err != nil {
		return fail(err)
	}

	id, err := startThread(c, model, s.cwd, personality)
	if err != nil {
		return fail(err)
	}

	s.server = c
	s.threadID = id
	s.model = model
	s.effort = effort
	s.personality = personality
	return nil
}

// startThread opens one app-server thread and returns its id.
func startThread(c *appServer, model, cwd string, personality bool) (string, error) {
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	params := map[string]any{
		"model":          model,
		"cwd":            cwd,
		"approvalPolicy": "never",
		"sandbox":        sandboxReadOnly,
		"serviceName":    "sensei_code",
	}
	if personality {
		params["personality"] = "friendly"
	}
	if err := c.call("thread/start", params, &started); err != nil {
		return "", fmt.Errorf("start ChatGPT architect thread: %w", err)
	}
	if strings.TrimSpace(started.Thread.ID) == "" {
		return "", errors.New("ChatGPT app-server returned an empty thread id")
	}
	return started.Thread.ID, nil
}

type appServerModel struct {
	ID                     string `json:"id"`
	Model                  string `json:"model"`
	DefaultReasoningEffort string `json:"defaultReasoningEffort"`
	SupportsPersonality    bool   `json:"supportsPersonality"`
	SupportedEfforts       []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

func chooseArchitectModel(c *appServer, wantedModel, wantedEffort string) (string, string, bool, error) {
	var result struct {
		Data []appServerModel `json:"data"`
	}
	if err := c.call("model/list", map[string]any{"limit": 100, "includeHidden": true}, &result); err != nil {
		return "", "", false, fmt.Errorf("list ChatGPT models: %w", err)
	}
	for _, candidate := range result.Data {
		model := candidate.Model
		if model == "" {
			model = candidate.ID
		}
		if model != wantedModel && candidate.ID != wantedModel {
			continue
		}
		effort := selectEffort(candidate, wantedEffort)
		return model, effort, candidate.SupportsPersonality, nil
	}
	available := make([]string, 0, len(result.Data))
	for _, candidate := range result.Data {
		name := candidate.Model
		if name == "" {
			name = candidate.ID
		}
		if name != "" {
			available = append(available, name)
		}
	}
	sort.Strings(available)
	return "", "", false, fmt.Errorf("required ChatGPT architect model %q is unavailable (available: %s)", wantedModel, strings.Join(available, ", "))
}

func selectEffort(model appServerModel, wanted string) string {
	if len(model.SupportedEfforts) == 0 {
		if wanted != "" {
			return wanted
		}
		return model.DefaultReasoningEffort
	}
	for _, option := range model.SupportedEfforts {
		if option.ReasoningEffort == wanted {
			return wanted
		}
	}
	if model.DefaultReasoningEffort != "" {
		return model.DefaultReasoningEffort
	}
	return model.SupportedEfforts[0].ReasoningEffort
}

func (s *ChatGPTSession) runTurn(threadID, prompt string) (string, error) {
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	params := map[string]any{
		"threadId":       threadID,
		"input":          []map[string]string{{"type": "text", "text": prompt}},
		"cwd":            s.cwd,
		"approvalPolicy": "never",
		"sandboxPolicy":  map[string]any{"type": sandboxReadOnly},
		"model":          s.model,
		"effort":         s.effort,
		"personality":    "friendly",
	}
	if err := s.server.call("turn/start", params, &started); err != nil {
		return "", fmt.Errorf("start ChatGPT architect turn: %w", err)
	}
	turnID := strings.TrimSpace(started.Turn.ID)
	if turnID == "" {
		return "", errors.New("ChatGPT app-server returned an empty turn id")
	}

	var finalText, lastText, streamText, eventError string
	for {
		msg, err := s.server.nextNotification()
		if err != nil {
			return "", fmt.Errorf("read ChatGPT architect turn: %w", err)
		}
		switch msg.Method {
		case "item/agentMessage/delta":
			var p struct {
				TurnID string `json:"turnId"`
				Delta  string `json:"delta"`
			}
			if json.Unmarshal(msg.Params, &p) == nil && (p.TurnID == "" || p.TurnID == turnID) {
				streamText += p.Delta
			}
		case "item/completed":
			var p struct {
				TurnID string `json:"turnId"`
				Item   struct {
					Type  string `json:"type"`
					Text  string `json:"text"`
					Phase string `json:"phase"`
				} `json:"item"`
			}
			if json.Unmarshal(msg.Params, &p) != nil || (p.TurnID != "" && p.TurnID != turnID) || p.Item.Type != "agentMessage" {
				continue
			}
			if strings.TrimSpace(p.Item.Text) != "" {
				lastText = p.Item.Text
				if p.Item.Phase == "final_answer" {
					finalText = p.Item.Text
				}
			}
		case "error":
			var p struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(msg.Params, &p) == nil {
				eventError = p.Error.Message
			}
		case "turn/completed":
			var p struct {
				Turn struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
			}
			if json.Unmarshal(msg.Params, &p) != nil || p.Turn.ID != turnID {
				continue
			}
			if p.Turn.Status != "completed" {
				message := eventError
				if p.Turn.Error != nil && p.Turn.Error.Message != "" {
					message = p.Turn.Error.Message
				}
				if message == "" {
					message = "turn finished with status " + p.Turn.Status
				}
				return "", errors.New(message)
			}
			answer := strings.TrimSpace(finalText)
			if answer == "" {
				answer = strings.TrimSpace(lastText)
			}
			if answer == "" {
				answer = strings.TrimSpace(streamText)
			}
			if answer == "" {
				return "", errors.New("ChatGPT architect completed without a text answer")
			}
			return answer, nil
		}
	}
}

// nextNotification returns the next server notification in wire order. A
// server-initiated request is not silently accepted: the first-version
// architect runs read-only with approvalPolicy=never, so an unexpected request
// is a control-boundary failure rather than an invitation to fabricate input.
func (c *appServer) nextNotification() (rpcEnvelope, error) {
	for len(c.queued) > 0 {
		msg := c.queued[0]
		c.queued = c.queued[1:]
		if msg.Method != "" && len(msg.ID) == 0 {
			return msg, nil
		}
	}
	for {
		msg, err := c.read()
		if err != nil {
			return rpcEnvelope{}, err
		}
		if msg.Method == "" {
			continue
		}
		if len(msg.ID) != 0 {
			return rpcEnvelope{}, fmt.Errorf("unsupported ChatGPT app-server request %q", msg.Method)
		}
		return msg, nil
	}
}
