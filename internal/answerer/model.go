package answerer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Turn is everything one model call receives: the fixed role contract for the
// request's kind, and the request's exact bytes. There is no history field,
// because a turn has none.
type Turn struct {
	Kind    Kind
	System  string
	Request string
}

// Model answers one turn. Every call is fresh and stateless; an implementation
// must not carry anything from one call into the next.
type Model interface {
	Answer(ctx context.Context, t Turn) (string, error)
}

// ChatModel is a chat-completions endpoint. It owns no contract rule: it
// returns the model's content exactly as received, and whether that content is
// an answer is for the injected validator to say.
type ChatModel struct {
	Endpoint string
	Name     string
	APIKey   string
	Client   *http.Client
}

// NewChatModel builds the model from configuration.
func NewChatModel(c Config) *ChatModel {
	return &ChatModel{
		Endpoint: c.ModelEndpoint, Name: c.ModelName, APIKey: c.ModelAPIKey,
		Client: &http.Client{Timeout: 10 * time.Minute},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	// Store is false so the provider keeps nothing a later turn could lean on.
	Store bool `json:"store"`
}

type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content *string `json:"content"`
			Refusal *string `json:"refusal"`
		} `json:"message"`
	} `json:"choices"`
}

// Answer makes exactly one request: the system contract and the request bytes,
// nothing else. A truncated, refused or empty completion is a transport
// failure, never an answer.
func (m *ChatModel) Answer(ctx context.Context, t Turn) (string, error) {
	encoded, err := json.Marshal(chatRequest{
		Model: m.Name,
		Messages: []chatMessage{
			{Role: "system", Content: t.System},
			{Role: "user", Content: t.Request},
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("model call: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("model response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("model call returned %s: %s", resp.Status, bounded(string(raw)))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("model response is not a chat completion: %w", err)
	}
	if len(out.Choices) != 1 {
		return "", fmt.Errorf("model returned %d choices, want exactly 1", len(out.Choices))
	}
	choice := out.Choices[0]
	if choice.Message.Refusal != nil && *choice.Message.Refusal != "" {
		return "", fmt.Errorf("model refused at the provider: %s", bounded(*choice.Message.Refusal))
	}
	if choice.FinishReason != "stop" {
		return "", fmt.Errorf("model completion ended with %q, not stop", choice.FinishReason)
	}
	if choice.Message.Content == nil || strings.TrimSpace(*choice.Message.Content) == "" {
		return "", errors.New("model returned no content")
	}
	return *choice.Message.Content, nil
}

// bounded keeps a diagnostic a diagnostic rather than a copy of a payload.
func bounded(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
