package provider

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseProviderChoices(t *testing.T) {
	tests := map[string]ID{
		"1":           ChatGPT,
		"chatgpt":     ChatGPT,
		"openai":      ChatGPT,
		"2":           Codex,
		"codex":       Codex,
		"3":           Claude,
		"claude":      Claude,
		"anthropic":   Claude,
		"4":           Antigravity,
		"antigravity": Antigravity,
		"agy":         Antigravity,
		"google":      Antigravity,
	}
	for input, want := range tests {
		got, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("Parse(%q)=%q want %q", input, got, want)
		}
	}
	if _, err := Parse("mystery"); err == nil {
		t.Fatal("Parse(mystery) unexpectedly succeeded")
	}
}

func TestProviderStatusHasNoCredentialFields(t *testing.T) {
	typeOf := reflect.TypeOf(Status{})
	for i := 0; i < typeOf.NumField(); i++ {
		field := typeOf.Field(i)
		name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
		for _, forbidden := range []string{"token", "secret", "credential", "password", "api_key", "apikey"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("provider Status must not carry credentials: field %s", field.Name)
			}
		}
	}
}

func TestFormatStatusPreservesUnknownAuth(t *testing.T) {
	status := Status{
		ID:        Antigravity,
		Label:     Label(Antigravity),
		Installed: true,
		AuthKnown: false,
		Detail:    "authentication is provider-owned",
	}
	got := FormatStatus(status)
	want := "installed · authentication is provider-owned"
	if got != want {
		t.Fatalf("FormatStatus()=%q want %q", got, want)
	}
}

func TestFormatStatusConnected(t *testing.T) {
	status := Status{
		ID:            ChatGPT,
		Label:         Label(ChatGPT),
		Installed:     true,
		AuthKnown:     true,
		Authenticated: true,
		AuthMode:      "chatgpt",
		Plan:          "plus",
		Account:       "dev@example.com",
	}
	got := FormatStatus(status)
	want := "connected · plus · dev@example.com"
	if got != want {
		t.Fatalf("FormatStatus()=%q want %q", got, want)
	}
}

func TestNumericID(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
		ok   bool
	}{
		{raw: "12", want: 12, ok: true},
		{raw: `"13"`, want: 13, ok: true},
		{raw: `"abc"`, ok: false},
		{raw: `null`, ok: false},
	} {
		got, ok := numericID(json.RawMessage(tc.raw))
		if ok != tc.ok || got != tc.want {
			t.Fatalf("numericID(%s)=(%d,%v) want (%d,%v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLoginErrorMessage(t *testing.T) {
	cases := map[string]string{
		"":                        "provider rejected login",
		"null":                    "provider rejected login",
		`"access denied"`:         "access denied",
		`{"message":"cancelled"}`: "cancelled",
	}
	for raw, want := range cases {
		if got := loginErrorMessage(json.RawMessage(raw)); got != want {
			t.Fatalf("loginErrorMessage(%q)=%q want %q", raw, got, want)
		}
	}
}

func TestAwaitAccountStateWaitsForCodexToSettle(t *testing.T) {
	// Codex acknowledges a login before the credential has settled, so the
	// first reads still report signed out. A single read would report the
	// opposite of the truth.
	calls := 0
	read := func() (CodexAccount, error) {
		calls++
		if calls < 3 {
			return CodexAccount{}, nil
		}
		return CodexAccount{Authenticated: true, Type: "chatgpt", Plan: "plus"}, nil
	}
	got, err := awaitAccountState(read, true, "chatgpt", time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Authenticated || got.Type != "chatgpt" {
		t.Fatalf("awaitAccountState returned %+v, want the settled authenticated account", got)
	}
}

func TestAwaitAccountStateFailsClosedWhenStateNeverSettles(t *testing.T) {
	read := func() (CodexAccount, error) {
		return CodexAccount{Authenticated: true, Type: "chatgpt"}, nil
	}
	// A logout that never takes effect must surface the state actually
	// observed, not an assumed success.
	got, err := awaitAccountState(read, false, "", 20*time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Authenticated {
		t.Fatal("awaitAccountState hid a logout that never took effect")
	}
}

func TestEnvironmentKeyOverrideIsReported(t *testing.T) {
	// doctor reported "connected" from the stored login while the worker
	// authenticated with an environment key and failed 401. The override has to
	// be visible, or the check describes a session the work never uses.
	if got := envKeySource("ANTHROPIC_API_KEY"); got != "ANTHROPIC_API_KEY" {
		t.Fatalf("envKeySource = %q, want the overriding variable", got)
	}
	for _, benign := range []string{"", "none", "login", "claude.ai"} {
		if got := envKeySource(benign); got != "" {
			t.Fatalf("envKeySource(%q) = %q, want no override", benign, got)
		}
	}
}
