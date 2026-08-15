package sensei

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestRefusalReasonIsPreserved(t *testing.T) {
	var result ToolResult
	result.IsError = true
	result.Content = append(result.Content, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "text", Text: "graph freshness stale for briefing"})

	if got := refusalReason(result); got != "graph freshness stale for briefing" {
		t.Fatalf("refusal reason = %q, want the reason Sensei supplied", got)
	}
	if got := refusalReason(ToolResult{IsError: true}); got != "no reason supplied" {
		t.Fatalf("empty refusal = %q, want an explicit placeholder", got)
	}
}

func TestReadFrameRejectsOversizedContentLength(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("Content-Length: 999999999999\r\n\r\n"))
	if _, err := readFrame(r); err == nil {
		t.Fatal("readFrame accepted a Content-Length beyond the allocation limit")
	}
}

func TestStderrTailKeepsBoundedSuffix(t *testing.T) {
	tail := &stderrTail{}
	if _, err := tail.Write(bytes.Repeat([]byte("a"), stderrTailLimit)); err != nil {
		t.Fatal(err)
	}
	if _, err := tail.Write([]byte("sensei: cannot reach graph")); err != nil {
		t.Fatal(err)
	}
	got := tail.String()
	if len(got) > stderrTailLimit {
		t.Fatalf("stderr tail grew to %d bytes, want at most %d", len(got), stderrTailLimit)
	}
	if !strings.HasSuffix(got, "sensei: cannot reach graph") {
		t.Fatalf("stderr tail dropped the most recent output: %q", got)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := writeFrame(&b, map[string]any{"x": "y"}); err != nil {
		t.Fatal(err)
	}
	body, err := readFrame(bufio.NewReader(&b))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"x":"y"}` {
		t.Fatalf("unexpected body %s", body)
	}
}
