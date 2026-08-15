package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type CodexAccount struct {
	Authenticated bool
	Type          string
	Email         string
	Plan          string
}

type appServer struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	nextID  int64
	queued  []rpcEnvelope
}

type rpcEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type loginCompleted struct {
	LoginID string          `json:"loginId"`
	Success bool            `json:"success"`
	Error   json.RawMessage `json:"error"`
}

func startAppServer(ctx context.Context) (*appServer, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, "app-server", "--listen", "stdio://")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &appServer{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout)}
	c.scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var initResult json.RawMessage
	if err := c.call("initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "sensei_code",
			"title":   "Sensei Code",
			"version": "0.1.0",
		},
	}, &initResult); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, fmt.Errorf("acknowledge Codex app-server: %w", err)
	}
	return c, nil
}

func (c *appServer) Close() {
	if c == nil {
		return
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}

func (c *appServer) call(method string, params any, out any) error {
	c.nextID++
	id := c.nextID
	payload := map[string]any{"method": method, "id": id}
	if params != nil {
		payload["params"] = params
	}
	if err := c.write(payload); err != nil {
		return err
	}
	for {
		msg, err := c.read()
		if err != nil {
			return err
		}
		if msg.Method != "" && len(msg.ID) == 0 {
			c.queued = append(c.queued, msg)
			continue
		}
		gotID, ok := numericID(msg.ID)
		if !ok || gotID != id {
			continue
		}
		if msg.Error != nil {
			return fmt.Errorf("%s: rpc %d: %s", method, msg.Error.Code, msg.Error.Message)
		}
		if out == nil || len(msg.Result) == 0 {
			return nil
		}
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append((*raw)[:0], msg.Result...)
			return nil
		}
		if err := json.Unmarshal(msg.Result, out); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	}
}

func (c *appServer) notify(method string, params any) error {
	payload := map[string]any{"method": method}
	if params != nil {
		payload["params"] = params
	}
	return c.write(payload)
}

func (c *appServer) write(value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = c.stdin.Write(b)
	return err
}

func (c *appServer) read() (rpcEnvelope, error) {
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return rpcEnvelope{}, err
		}
		return rpcEnvelope{}, io.EOF
	}
	var msg rpcEnvelope
	if err := json.Unmarshal(c.scanner.Bytes(), &msg); err != nil {
		return rpcEnvelope{}, fmt.Errorf("decode Codex app-server message: %w", err)
	}
	return msg, nil
}

func (c *appServer) waitNotification(method string, match func(json.RawMessage) bool) (json.RawMessage, error) {
	for i, msg := range c.queued {
		if msg.Method == method && (match == nil || match(msg.Params)) {
			c.queued = append(c.queued[:i], c.queued[i+1:]...)
			return msg.Params, nil
		}
	}
	for {
		msg, err := c.read()
		if err != nil {
			return nil, err
		}
		if msg.Method == method && len(msg.ID) == 0 && (match == nil || match(msg.Params)) {
			return msg.Params, nil
		}
		if msg.Method != "" && len(msg.ID) == 0 {
			c.queued = append(c.queued, msg)
		}
	}
}

func numericID(raw json.RawMessage) (int64, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, false
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	}
	return 0, false
}

func ReadCodexAccount(ctx context.Context) (CodexAccount, error) {
	c, err := startAppServer(ctx)
	if err != nil {
		return CodexAccount{}, err
	}
	defer c.Close()
	return c.readAccount()
}

func (c *appServer) readAccount() (CodexAccount, error) {
	var result struct {
		Account *struct {
			Type     string  `json:"type"`
			Email    *string `json:"email"`
			PlanType string  `json:"planType"`
		} `json:"account"`
		RequiresOpenAIAuth bool `json:"requiresOpenaiAuth"`
	}
	if err := c.call("account/read", map[string]any{"refreshToken": false}, &result); err != nil {
		return CodexAccount{}, err
	}
	if result.Account == nil {
		return CodexAccount{Authenticated: !result.RequiresOpenAIAuth}, nil
	}
	account := CodexAccount{Authenticated: true, Type: result.Account.Type, Plan: result.Account.PlanType}
	if result.Account.Email != nil {
		account.Email = *result.Account.Email
	}
	return account, nil
}

func LoginChatGPT(ctx context.Context) error {
	loginCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	c, err := startAppServer(loginCtx)
	if err != nil {
		return err
	}
	defer c.Close()

	var result struct {
		Type    string `json:"type"`
		LoginID string `json:"loginId"`
		AuthURL string `json:"authUrl"`
	}
	if err := c.call("account/login/start", map[string]any{
		"type":                      "chatgpt",
		"useHostedLoginSuccessPage": true,
		"appBrand":                  "chatgpt",
	}, &result); err != nil {
		return err
	}
	if result.LoginID == "" || result.AuthURL == "" {
		return errors.New("Codex app-server returned an incomplete ChatGPT login response")
	}
	fmt.Println("Open this URL to sign in with ChatGPT:")
	fmt.Println(result.AuthURL)
	if err := openURL(result.AuthURL); err != nil {
		fmt.Println("Browser was not opened automatically:", err)
	}
	fmt.Println("Waiting for ChatGPT login to complete...")

	raw, err := c.waitNotification("account/login/completed", func(raw json.RawMessage) bool {
		var p loginCompleted
		return json.Unmarshal(raw, &p) == nil && p.LoginID == result.LoginID
	})
	if err != nil {
		if errors.Is(loginCtx.Err(), context.DeadlineExceeded) {
			return errors.New("ChatGPT login timed out")
		}
		return err
	}
	var completed loginCompleted
	if err := json.Unmarshal(raw, &completed); err != nil {
		return fmt.Errorf("decode ChatGPT login completion: %w", err)
	}
	if !completed.Success {
		return fmt.Errorf("ChatGPT login failed: %s", loginErrorMessage(completed.Error))
	}
	account, err := c.readAccount()
	if err != nil {
		return fmt.Errorf("verify ChatGPT login: %w", err)
	}
	if !account.Authenticated || account.Type != "chatgpt" {
		return errors.New("ChatGPT login did not produce an authenticated Codex account")
	}
	fmt.Print("Connected to ChatGPT")
	if account.Plan != "" {
		fmt.Printf(" (%s)", account.Plan)
	}
	if account.Email != "" {
		fmt.Printf(" as %s", account.Email)
	}
	fmt.Println()
	return nil
}

func loginErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "provider rejected login"
	}
	var message string
	if json.Unmarshal(raw, &message) == nil && strings.TrimSpace(message) != "" {
		return message
	}
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &payload) == nil && strings.TrimSpace(payload.Message) != "" {
		return payload.Message
	}
	return strings.TrimSpace(string(raw))
}

func LogoutCodex(ctx context.Context) error {
	c, err := startAppServer(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.call("account/logout", nil, nil)
}

func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func FormatStatus(status Status) string {
	state := "not installed"
	if status.Installed {
		state = "installed"
	}
	if status.AuthKnown {
		if status.Authenticated {
			state = "connected"
		} else {
			state = "logged out"
		}
	}
	parts := []string{state}
	if status.Plan != "" {
		parts = append(parts, status.Plan)
	}
	if status.Account != "" {
		parts = append(parts, status.Account)
	}
	if status.Detail != "" && !strings.EqualFold(status.Detail, "not logged in") {
		parts = append(parts, status.Detail)
	}
	return strings.Join(parts, " · ")
}
