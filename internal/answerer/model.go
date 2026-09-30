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

// Model answers one request in one fresh, stateless call.
//
// system is the fixed role contract this process owns; request is the exact
// request body, untrusted. The returned text is the model's response exactly as
// it arrived: a Model neither trims, repairs nor re-encodes it, and it owns no
// architecture or review schema -- whether the text satisfies one is decided by
// the canonical validator the caller was given.
//
// Nothing carries between calls. There is no conversation id, no history and no
// session to resume, so a reviewer turn can never inherit a previous turn.
type Model interface {
	Answer(ctx context.Context, system, request string) (string, error)
}

// maxModelResponse bounds what is read from the model endpoint.
const maxModelResponse = 1 << 20

// ChatModel is a Model over an OpenAI-compatible chat-completions endpoint.
type ChatModel struct {
	Endpoint string
	Model    string
	// Key is a secret. It leaves this type only as an Authorization header and
	// never appears in an error.
	Key string
	// HTTP is the client used. Empty means a client with Timeout.
	HTTP    *http.Client
	Timeout time.Duration
}

// NewChatModel builds the model the operator configured.
func NewChatModel(c Config) *ChatModel {
	return &ChatModel{Endpoint: c.ModelEndpoint, Model: c.Model, Key: c.ModelKey, Timeout: 10 * time.Minute}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Answer makes exactly one call. The message list is built fresh from the two
// arguments every time and is the whole conversation the model sees.
func (m *ChatModel) Answer(ctx context.Context, system, request string) (string, error) {
	if m == nil || strings.TrimSpace(m.Endpoint) == "" || strings.TrimSpace(m.Model) == "" || m.Key == "" {
		return "", errors.New("the model is not configured")
	}
	payload, err := json.Marshal(chatRequest{
		Model: m.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: request},
		},
	})
	if err != nil {
		return "", fmt.Errorf("encoding the model request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return "", fmt.Errorf("building the model request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.Key)
	req.Header.Set("Content-Type", "application/json")
	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: m.Timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling the model endpoint: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModelResponse+1))
	if err != nil {
		return "", fmt.Errorf("reading the model response: %w", err)
	}
	if len(raw) > maxModelResponse {
		return "", fmt.Errorf("the model response exceeds %d bytes", maxModelResponse)
	}
	if resp.StatusCode/100 != 2 {
		// The status only: a provider's error body can echo request content.
		return "", fmt.Errorf("the model endpoint answered HTTP %d", resp.StatusCode)
	}
	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("the model endpoint's reply is not a chat completion: %w", err)
	}
	if len(decoded.Choices) != 1 || decoded.Choices[0].Message.Content == nil {
		return "", fmt.Errorf("the model endpoint returned %d choices and no single message content", len(decoded.Choices))
	}
	return *decoded.Choices[0].Message.Content, nil
}
