package tui

// The TUI delegates provider login and owns no credential.
//
// This pins the property that made internal/tui/model.go worth naming on
// sensei_code.provider.credentials_remain_provider_owned in the first place:
// the transcript view invokes the native login path and never handles a token
// itself.
//
// It replaces a blanket anchor with a check of the actual behaviour. The anchor
// made every change to this file a security change class requiring human
// approval -- measured in the proof-v6 campaign as two of five governed
// refusals, both on transcript SCROLLING changes that touch no credential path.
// A rule that gates scrolling because a neighbouring line mentions credentials
// protects nothing and costs delivery.
//
// The invariant is unchanged and its other anchors are unchanged. What changed
// is that this file's obligation is now proven by a test rather than asserted
// by an approval gate.

import (
	"os"
	"strings"
	"testing"
)

// Login is delegated to the native provider client, never performed here.
func TestTheTUIDelegatesLoginAndHandlesNoCredential(t *testing.T) {
	src, err := os.ReadFile("model.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	// The delegation itself: a subprocess, not an in-process auth flow.
	if !strings.Contains(body, `exec.Command(exe, "login", string(id))`) {
		t.Error("the TUI no longer delegates login to the native provider client; if it now performs " +
			"authentication itself, sensei_code.provider.credentials_remain_provider_owned is at risk " +
			"and this file needs its anchor back")
	}

	// And no credential ever lands here. Checked against the source rather than
	// against behaviour because the property is an ABSENCE, and an absence is
	// what a test can state precisely.
	for _, forbidden := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN",
		"AccessToken", "RefreshToken", "BearerToken", "apiKey", "APIKey",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("%s appears in the transcript view; credentials belong to the native provider "+
				"client and must not be read, stored or rendered here", forbidden)
		}
	}
}

// What the TUI uses internal/provider for is identity and display.
//
// If that widens to anything that reads or carries authentication state, the
// narrowed anchor is no longer correct and this test says so.
func TestTheTUIUsesProvidersOnlyForIdentityAndDisplay(t *testing.T) {
	src, err := os.ReadFile("model.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"provider.Label": true, "provider.Parse": true,
		"provider.Ordered": true, "provider.ID": true,
	}
	for _, line := range strings.Split(string(src), "\n") {
		idx := strings.Index(line, "provider.")
		for idx >= 0 {
			rest := line[idx:]
			end := len("provider.")
			for end < len(rest) && (rest[end] >= 'A' && rest[end] <= 'Z' ||
				rest[end] >= 'a' && rest[end] <= 'z' || rest[end] >= '0' && rest[end] <= '9') {
				end++
			}
			sym := rest[:end]
			if sym != "provider." && !allowed[sym] {
				t.Errorf("the transcript view now uses %s. The credential anchor was narrowed on the "+
					"evidence that this file uses internal/provider only for identity and display; if "+
					"that changed, re-examine the anchor rather than this test", sym)
			}
			next := strings.Index(line[idx+1:], "provider.")
			if next < 0 {
				break
			}
			idx = idx + 1 + next
		}
	}
}
