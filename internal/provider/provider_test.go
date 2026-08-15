package provider

import (
	"encoding/json"
	"testing"
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
		"":                         "provider rejected login",
		"null":                     "provider rejected login",
		`"access denied"`:          "access denied",
		`{"message":"cancelled"}`: "cancelled",
	}
	for raw, want := range cases {
		if got := loginErrorMessage(json.RawMessage(raw)); got != want {
			t.Fatalf("loginErrorMessage(%q)=%q want %q", raw, got, want)
		}
	}
}
