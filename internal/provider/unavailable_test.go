package provider

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

type discardCloser struct{ io.Writer }

func (discardCloser) Close() error { return nil }

// replayedSession is a ChatGPT session whose app-server is a recorded wire
// transcript. runTurn is the production code under test; only the pipe is fake.
func replayedSession(lines ...string) *ChatGPTSession {
	srv := &appServer{
		stdin:   discardCloser{io.Discard},
		scanner: bufio.NewScanner(strings.NewReader(strings.Join(lines, "\n") + "\n")),
	}
	srv.scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return &ChatGPTSession{server: srv, cwd: "/tmp", model: "m", effort: "e"}
}

// The exact sequence codex-cli 0.150.1 sent on 2026-09-18 when the account was
// out of quota (turn and thread ids shortened). Note the rate-limit update
// during the turn names a DIFFERENT limit with every field null: nothing in the
// failure itself says when the provider will serve again.
const usageLimitMessage = "You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again at Sep 19th, 2026 7:10 AM."

func usageLimitTranscript() []string {
	return []string{
		`{"id":1,"result":{"turn":{"id":"T1","items":[],"status":"inProgress","error":null}}}`,
		`{"method":"turn/started","params":{"threadId":"TH","turn":{"id":"T1","status":"inProgress","error":null}}}`,
		`{"method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"premium","limitName":null,"primary":null,"secondary":null,"credits":{"hasCredits":false,"unlimited":false,"balance":"0"},"individualLimit":null,"spendControlReached":null,"planType":null,"rateLimitReachedType":null}}}`,
		`{"method":"thread/status/changed","params":{"threadId":"TH","status":{"type":"systemError"}}}`,
		`{"method":"error","params":{"error":{"message":"` + usageLimitMessage + `","codexErrorInfo":"usageLimitExceeded","additionalDetails":null},"willRetry":false,"threadId":"TH","turnId":"T1"}}`,
		`{"method":"turn/completed","params":{"threadId":"TH","turn":{"id":"T1","items":[],"status":"failed","error":{"message":"` + usageLimitMessage + `","codexErrorInfo":"usageLimitExceeded","additionalDetails":null}}}}`,
	}
}

// The provider's structured code, and only that, establishes unavailability.
func TestAUsageLimitTurnIsProvenProviderUnavailability(t *testing.T) {
	_, err := replayedSession(usageLimitTranscript()...).runTurn("TH", "prompt")
	u, ok := AsUnavailable(err)
	if !ok {
		t.Fatalf("the recorded usage-limit turn was not classified as provider unavailability: %v", err)
	}
	if u.Provider != string(ChatGPT) || u.Reason != "usageLimitExceeded" {
		t.Fatalf("unavailability must name the provider and its own code, got provider=%q reason=%q", u.Provider, u.Reason)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("the typed unavailability must still match ErrUnavailable")
	}
	// The message SAYS "try again at Sep 19th, 2026 7:10 AM". Reading it would be
	// inferring a structured fact from presentation.
	if !u.RetryAt.IsZero() {
		t.Fatalf("a retry time was invented from text: %v", u.RetryAt)
	}
	if !strings.Contains(err.Error(), "retry time UNKNOWN") {
		t.Fatalf("an absent retry time must be stated as UNKNOWN: %v", err)
	}
}

// The same failed turn WITHOUT the structured code is an ordinary failure, even
// though its message is word for word the usage-limit sentence. This is the
// guard against laundering: text alone proves nothing.
func TestTheUsageLimitSentenceAloneIsNotUnavailability(t *testing.T) {
	lines := usageLimitTranscript()
	for i := range lines {
		lines[i] = strings.ReplaceAll(lines[i], `"codexErrorInfo":"usageLimitExceeded",`, "")
	}
	_, err := replayedSession(lines...).runTurn("TH", "prompt")
	if err == nil {
		t.Fatal("a failed turn returned no error")
	}
	if _, ok := AsUnavailable(err); ok {
		t.Fatalf("a failure with no structured code was classified as unavailability: %v", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("an unclassified failure matched ErrUnavailable: %v", err)
	}
}

// Codes outside the closed set stay ordinary failures, including an object
// variant that carries an HTTP status and a code a later Codex might add.
func TestOnlyListedCodexCodesProveUnavailability(t *testing.T) {
	for _, info := range []string{
		`"other"`, `"internalServerError"`, `"badRequest"`, `"unauthorized"`, `"contextWindowExceeded"`,
		`"someFutureCode"`, `{"httpConnectionFailed":{"httpStatusCode":429}}`, `null`, `""`,
	} {
		if u := codexTurnUnavailable("chatgpt", []byte(info), usageLimitMessage); u != nil {
			t.Errorf("codexErrorInfo %s was classified as unavailability", info)
		}
	}
	if u := codexTurnUnavailable("chatgpt", []byte(`"usageLimitExceeded"`), "x"); u == nil {
		t.Error("the listed code was not classified")
	}
}

// A completed turn is untouched by the new branch.
func TestACompletedTurnStillAnswers(t *testing.T) {
	text, err := replayedSession(
		`{"id":1,"result":{"turn":{"id":"T1"}}}`,
		`{"method":"item/completed","params":{"turnId":"T1","item":{"type":"agentMessage","text":"ok","phase":"final_answer"}}}`,
		`{"method":"turn/completed","params":{"threadId":"TH","turn":{"id":"T1","status":"completed","error":null}}}`,
	).runTurn("TH", "prompt")
	if err != nil || text != "ok" {
		t.Fatalf("a completed turn: text=%q err=%v", text, err)
	}
}
