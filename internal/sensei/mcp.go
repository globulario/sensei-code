package sensei

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Client struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	stderr *stderrTail
	mu     sync.Mutex
	next   atomic.Int64
	// ok remembers when Sensei last answered, so a failure can say when the
	// environment was last known good rather than only that it is bad now.
	ok lastOK
}

const (
	// stderrTailLimit bounds how much of the Sensei server's stderr is retained.
	stderrTailLimit = 4096
	// maxFrameBytes caps a declared Content-Length so a corrupt header cannot
	// drive an unbounded allocation.
	maxFrameBytes = 64 << 20
)

// stderrTail keeps the last stderrTailLimit bytes the Sensei server wrote. The
// stream was previously discarded, which erased the one diagnostic explaining
// why a Sensei surface was unavailable.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (s *stderrTail) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	if len(s.buf) > stderrTailLimit {
		s.buf = s.buf[len(s.buf)-stderrTailLimit:]
	}
	return len(p), nil
}

func (s *stderrTail) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.buf))
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

type ToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Structured map[string]any `json:"structuredContent"`
	IsError    bool           `json:"isError"`
}

func Start(ctx context.Context, dir, command string, args []string) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	tail := &stderrTail{}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, in: in, out: bufio.NewReader(out), stderr: tail}
	if err := c.initialize(); err != nil {
		_ = c.Close()
		return nil, c.withStderr(err)
	}
	return c, nil
}

// withStderr attaches what the Sensei server reported on stderr, so an
// unavailable surface names its cause instead of failing opaquely.
func (c *Client) withStderr(err error) error {
	if c.stderr == nil {
		return err
	}
	if tail := c.stderr.String(); tail != "" {
		return fmt.Errorf("%w: sensei stderr: %s", err, tail)
	}
	return err
}

func (c *Client) initialize() error {
	var result map[string]any
	if err := c.call("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "sensei-code", "version": "0.1.0"},
	}, &result); err != nil {
		return fmt.Errorf("sensei MCP initialize: %w", err)
	}
	return c.notify("notifications/initialized", map[string]any{})
}

func (c *Client) CallTool(name string, args map[string]any) (ToolResult, error) {
	var result ToolResult
	err := c.call("tools/call", map[string]any{"name": name, "arguments": args}, &result)
	if err != nil {
		// A transport failure is reported with what this process witnessed
		// about its dependency, because the error text names the socket and
		// the cause is usually a layer below it.
		return ToolResult{}, fmt.Errorf("%s", c.diagnose(name, err))
	}
	if result.IsError {
		// A refusal is Sensei working correctly and saying no. Deliberately
		// NOT diagnosed: attaching dependency state here would dress a
		// fail-closed answer as an outage, which is the opposite of the
		// distinction this file exists to keep.
		c.ok.mark(name)
		return result, fmt.Errorf("sensei tool %s refused: %s", name, refusalReason(result))
	}
	c.ok.mark(name)
	return result, nil
}

// Health is what this client has witnessed about its dependency.
func (c *Client) Health() Health {
	at, tool := c.ok.read()
	h := Health{LastOK: at, LastTool: tool}
	if c.cmd != nil && c.cmd.Process != nil {
		// Signal 0 asks whether the process exists without disturbing it.
		h.SubprocessAlive = c.cmd.Process.Signal(syscall.Signal(0)) == nil
	}
	if c.stderr != nil {
		h.Stderr = c.stderr.String()
	}
	return h
}

// diagnose renders a failed call with its causal chain attached.
func (c *Client) diagnose(tool string, err error) string {
	h := c.Health()
	var elapsed time.Duration
	if !h.LastOK.IsZero() {
		elapsed = time.Since(h.LastOK)
	}
	return Causes{Tool: tool, Err: err, Health: h, Elapsed: elapsed}.Diagnose()
}

// refusalReason recovers the reason Sensei gave for refusing. Dropping it would
// leave a fail-closed refusal indistinguishable from a transport fault.
func refusalReason(result ToolResult) string {
	for _, item := range result.Content {
		if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
			return strings.TrimSpace(item.Text)
		}
	}
	return "no reason supplied"
}

func (c *Client) ListTools() (json.RawMessage, error) {
	var result json.RawMessage
	if err := c.call("tools/list", map[string]any{}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) Close() error {
	if c.in != nil {
		_ = c.in.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	return nil
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return writeFrame(c.in, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Client) call(method string, params any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.next.Add(1)
	if err := writeFrame(c.in, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return err
	}
	for {
		body, err := readFrame(c.out)
		if err != nil {
			return c.withStderr(err)
		}
		// A frame carrying a method is a server-initiated request or
		// notification, not the reply to id. Anything that is not valid JSON at
		// all is a protocol fault and must be reported, not skipped.
		var probe struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			return c.withStderr(fmt.Errorf("sensei MCP sent a malformed frame: %w", err))
		}
		if probe.Method != "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return c.withStderr(fmt.Errorf("sensei MCP sent a malformed response: %w", err))
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return fmt.Errorf("rpc %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	}
}

func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(b))
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	if length > maxFrameBytes {
		return nil, fmt.Errorf("frame of %d bytes exceeds the %d byte limit", length, maxFrameBytes)
	}
	b := make([]byte, length)
	_, err := io.ReadFull(r, b)
	return b, err
}

// RepositoryDomain reads the repository domain out of Sensei's
// sensei.workspace.identity.v1 receipt. Callers pass it to domain-scoped
// surfaces such as awareness_preflight: a graph hosting more than one domain
// cannot scope itself and answers with a domain_scope blind spot. It returns ""
// when Sensei stated no domain, so an unbound workspace leaves the query
// unscoped rather than being given a domain Sensei never asserted.
func RepositoryDomain(result ToolResult) string {
	binding, ok := result.Structured["binding"].(map[string]any)
	if !ok {
		return ""
	}
	domain, ok := binding["repository_domain"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(domain)
}
