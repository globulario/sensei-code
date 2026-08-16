package behavioral

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecordIsSkippedUntilScopedDeliberately(t *testing.T) {
	for name, cfg := range map[string]Config{
		"disabled":   {Enabled: false, URL: "http://x", Project: "p", Domain: "d"},
		"no project": {Enabled: true, URL: "http://x", Domain: "d"},
		"no domain":  {Enabled: true, URL: "http://x", Project: "p"},
		"no url":     {Enabled: true, Project: "p", Domain: "d"},
	} {
		if err := New(cfg).Record(context.Background(), Outcome{Status: "success"}); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("%s: err = %v, want ErrNotConfigured", name, err)
		}
	}
}

func TestRecordSendsTheScopedOutcome(t *testing.T) {
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		calls = append(calls, payload)
		w.Header().Set("Mcp-Session-Id", "sess-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	cfg := Config{Enabled: true, URL: srv.URL, Project: "globular-services", Domain: "sensei_code"}
	if err := New(cfg).Record(context.Background(), Outcome{Status: "success", Theme: "t", Note: "n"}); err != nil {
		t.Fatal(err)
	}
	var toolCall map[string]any
	for _, c := range calls {
		if c["method"] == "tools/call" {
			toolCall = c
		}
	}
	if toolCall == nil {
		t.Fatalf("no tools/call was sent; got %d requests", len(calls))
	}
	params := toolCall["params"].(map[string]any)
	if params["name"] != "behavioral_record_outcome" {
		t.Fatalf("called %v, want behavioral_record_outcome", params["name"])
	}
	args := params["arguments"].(map[string]any)
	for key, want := range map[string]any{"project": "globular-services", "domain": "sensei_code", "status": "success"} {
		if args[key] != want {
			t.Fatalf("argument %s = %v, want %v", key, args[key], want)
		}
	}
}

func TestRecordSurfacesARefusalInsteadOfReportingSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Mcp-Session-Id", "sess-1")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"unknown project"}}`))
	}))
	defer srv.Close()
	cfg := Config{Enabled: true, URL: srv.URL, Project: "nope", Domain: "d"}
	err := New(cfg).Record(context.Background(), Outcome{Status: "success"})
	if err == nil {
		t.Fatal("a refused record was reported as recorded")
	}
}
