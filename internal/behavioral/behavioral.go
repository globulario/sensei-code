// Package behavioral reports what actually happened during a task to the
// behavioral service, so principles are learned from real runs instead of being
// authored by hand.
//
// It only records facts. The service's own tools state that recording cannot
// create or promote a principle, and Sensei Code does not try to: an outcome is
// evidence, and promoting evidence into a rule stays a governed step elsewhere.
package behavioral

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Config points Sensei Code at a behavioral service. Reporting is off until a
// project and domain are named: guessing a scope would file this repository's
// outcomes against somebody else's principles.
type Config struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Project string `json:"project"`
	Domain  string `json:"domain"`
	// InsecureSkipVerify disables TLS verification. It defaults to false and
	// must be set deliberately; a behavioral record is evidence, and evidence
	// sent to an unverified endpoint is not evidence.
	InsecureSkipVerify bool `json:"insecure_skip_verify"`
}

func (c Config) ready() bool {
	return c.Enabled &&
		strings.TrimSpace(c.URL) != "" &&
		strings.TrimSpace(c.Project) != "" &&
		strings.TrimSpace(c.Domain) != ""
}

// Outcome is what became of one task.
type Outcome struct {
	// Status is one of success, failure, blocked, reverted.
	Status string
	// Theme groups repeated patterns so the service can detect them.
	Theme string
	Note  string
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	transport := http.DefaultTransport
	if cfg.InsecureSkipVerify {
		transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: 20 * time.Second, Transport: transport}}
}

// ErrNotConfigured reports that no behavioral service is set up. It is not a
// failure: reporting is opt-in.
var ErrNotConfigured = errors.New("no behavioral service is configured")

// Record sends one outcome. It never blocks the workflow's own result: a task
// that succeeded did succeed, whether or not the fact could be filed.
func (c *Client) Record(ctx context.Context, o Outcome) error {
	if c == nil || !c.cfg.ready() {
		return ErrNotConfigured
	}
	session, err := c.initialize(ctx)
	if err != nil {
		return err
	}
	args := map[string]any{
		"project": c.cfg.Project,
		"domain":  c.cfg.Domain,
		"status":  o.Status,
	}
	if o.Theme != "" {
		args["theme"] = o.Theme
	}
	if o.Note != "" {
		args["note"] = o.Note
	}
	args["agent_id"] = "sensei-code"
	_, err = c.call(ctx, session, "tools/call", map[string]any{
		"name":      "behavioral_record_outcome",
		"arguments": args,
	})
	return err
}

func (c *Client) initialize(ctx context.Context) (string, error) {
	body, session, err := c.post(ctx, "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "sensei-code", "version": "0.1.0"},
		},
	})
	if err != nil {
		return "", err
	}
	if err := rpcError(body); err != nil {
		return "", err
	}
	if session == "" {
		return "", errors.New("behavioral service returned no session id")
	}
	_, _, err = c.post(ctx, session, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	})
	return session, err
}

func (c *Client) call(ctx context.Context, session, method string, params any) ([]byte, error) {
	body, _, err := c.post(ctx, session, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	return body, rpcError(body)
}

func (c *Client) post(ctx context.Context, session string, payload any) ([]byte, string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(b))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	out := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		out = append(out, buf[:n]...)
		if readErr != nil || len(out) > 1<<20 {
			break
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("behavioral service returned %s", resp.Status)
	}
	return out, resp.Header.Get("Mcp-Session-Id"), nil
}

// rpcError surfaces a JSON-RPC error rather than letting a refused call look
// like a recorded one.
func rpcError(body []byte) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var envelope struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	if envelope.Error != nil {
		return fmt.Errorf("behavioral rpc %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return nil
}
