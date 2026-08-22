package provider

import (
	"slices"

	"encoding/json"
	"github.com/globulario/sensei-code/internal/roles"
	"os"
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

// The credential a status reports must be the credential the work uses.
//
// SessionOnlyEnv strips ANTHROPIC_API_KEY and its siblings from every agent
// process, so an ambient key does not override the subscription login for any
// work this tool drives. The status previously said the opposite -- that the
// key overrode the login and the reported account was not the one a worker
// would authenticate with -- which contradicted the stripping declared in the
// same file.
func TestAnAmbientKeyIsReportedAsStrippedNotAsOverriding(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		var stripped bool
		for _, v := range SessionOnlyEnv {
			if v == name {
				stripped = true
			}
		}
		if !stripped {
			t.Errorf("%s is not stripped from agent processes, so it would override the subscription", name)
		}
	}
	// And the wording must not tell a reader the opposite of what the code does.
	body, err := os.ReadFile("provider.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "which overrides this login") {
		t.Error("the status claims an ambient key overrides the login it reports; agent processes strip it")
	}
	if !strings.Contains(string(body), "stripped from agent processes") {
		t.Error("the status no longer explains why the ambient key does not apply")
	}
}

// Finding 7 of the 2026-08-21 audit. SessionOnlyEnv named Anthropic's variables
// only, while the ChatGPT/Codex branch of StatusFor reports a stored Codex
// account under the same implicit claim -- that this is the account a worker
// authenticates with. Nothing removed an ambient OPENAI_API_KEY, so for one
// provider the claim rested on a mechanism and for the other on nothing.
func TestEveryReportedProviderHasItsCredentialsStripped(t *testing.T) {
	stripped := map[string]bool{}
	for _, v := range SessionOnlyEnv {
		stripped[v] = true
	}
	for _, want := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"OPENAI_API_KEY", "CODEX_API_KEY",
	} {
		if !stripped[want] {
			t.Errorf("%s is not stripped, so an ambient value can override the login doctor reports", want)
		}
	}
}

// The guarantee is only worth as much as its uniformity: a provider this tool
// reports an account for, but whose credentials it does not strip, is a claim
// with no mechanism behind it.
func TestTheStrippedListCoversTheProvidersReportedOn(t *testing.T) {
	byPrefix := map[string]bool{}
	for _, v := range SessionOnlyEnv {
		switch {
		case strings.HasPrefix(v, "ANTHROPIC_"), strings.HasPrefix(v, "CLAUDE_"):
			byPrefix["anthropic"] = true
		case strings.HasPrefix(v, "OPENAI_"), strings.HasPrefix(v, "CODEX_"):
			byPrefix["openai"] = true
		}
	}
	for _, family := range []string{"anthropic", "openai"} {
		if !byPrefix[family] {
			t.Errorf("no %s credential is stripped, but StatusFor reports an account for it", family)
		}
	}
}

// Independence had nowhere to go. ChatGPT and Codex authenticate against one
// account, so exhausting it removes both; with Claude as the worker,
// roles.Assign correctly refuses to let it review its own change. A governed
// run then ends with "no independent reviewer produced a bounded decision"
// while a verified candidate sits unreviewed — observed twice on 2026-08-22.
func TestAThirdProviderCanReview(t *testing.T) {
	if !slices.Contains(Roles(Antigravity), roles.Reviewer) {
		t.Fatal("Antigravity cannot hold the reviewer role, so it cannot break a single-account deadlock")
	}
	// Reviewer only. Implementing needs a write-capable sandbox this adapter's
	// permission model does not expose the way the others do, and the architect
	// is a continuity relationship rather than a capability.
	for _, unwanted := range []roles.Role{roles.Implementer, roles.Architect} {
		if slices.Contains(Roles(Antigravity), unwanted) {
			t.Errorf("Antigravity declares %s, which it has not been shown to hold", unwanted)
		}
	}
}

// The point of the third provider is that it does not share an account with the
// first two. If it ever did, declaring the role would buy nothing.
func TestTheReviewerPoolIsNotOneAccount(t *testing.T) {
	reviewers := map[ID]bool{}
	for _, id := range Ordered {
		if slices.Contains(Roles(id), roles.Reviewer) {
			reviewers[id] = true
		}
	}
	if len(reviewers) < 3 {
		t.Fatalf("only %d provider(s) can review; ChatGPT and Codex share one account, so two is one outage", len(reviewers))
	}
	if !reviewers[Claude] || !reviewers[Antigravity] {
		t.Error("the pool does not span more than the ChatGPT account")
	}
}
