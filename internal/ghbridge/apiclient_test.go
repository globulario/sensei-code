package ghbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// appMailbox stands up a fake GitHub that mints a token and serves one issue's
// comments. It records every Authorization header it saw so a test can prove
// which credential the call actually used.
type appMailbox struct {
	srv        *httptest.Server
	comments   []map[string]any
	authSeen   []string
	tokenCalls int32
	issuePath  string
}

func newAppMailbox(t *testing.T, keyPath string) (*appMailbox, Issue) {
	t.Helper()
	m := &appMailbox{issuePath: "/repos/globulario/sensei-code/issues/156/comments"}

	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&m.tokenCalls, 1)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_installation","expires_at":%q}`,
			time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc(m.issuePath, func(w http.ResponseWriter, r *http.Request) {
		m.authSeen = append(m.authSeen, r.Header.Get("Authorization"))
		switch r.Method {
		case http.MethodPost:
			var in struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			m.comments = append(m.comments, map[string]any{
				"body": in.Body,
				"user": map[string]any{"login": "globulario-sensei-code[bot]", "id": 99887766},
			})
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":1}`)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(m.comments)
		}
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)

	box := Issue{
		Number: "156",
		API: &AppClient{
			Auth: &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
				PrivateKeyPath: keyPath, APIBase: m.srv.URL},
			Owner: "globulario", Repo: "sensei-code",
		},
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"},
	}
	return m, box
}

// 7. PostRequest uses installation authentication.
func TestPostRequestUsesInstallationAuthentication(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath)

	if err := PostRequest(context.Background(), box, reqC1(), "please review"); err != nil {
		t.Fatalf("PostRequest: %v", err)
	}
	if atomic.LoadInt32(&m.tokenCalls) != 1 {
		t.Errorf("installation token minted %d times, want 1", m.tokenCalls)
	}
	if len(m.authSeen) != 1 || m.authSeen[0] != "Bearer ghs_installation" {
		t.Fatalf("the comment was not posted with the installation token: %v", m.authSeen)
	}
	if len(m.comments) != 1 {
		t.Fatalf("comment not recorded")
	}
	if !strings.Contains(m.comments[0]["body"].(string), requestMarker) {
		t.Error("the posted body is not a review request")
	}
}

// 8. Reviews uses installation authentication.
func TestReviewsUsesInstallationAuthentication(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath)

	if _, err := Reviews(context.Background(), box); err != nil {
		t.Fatalf("Reviews: %v", err)
	}
	if len(m.authSeen) == 0 {
		t.Fatal("no authenticated read happened")
	}
	for _, a := range m.authSeen {
		if a != "Bearer ghs_installation" {
			t.Errorf("a mailbox read used %q rather than the installation token", a)
		}
	}
}

// 9 and 10. Reviewer authentication is unchanged by the App transport: the App
// says who Sensei Code is, not who reviewed.
func TestReviewerAuthenticationIsUnchangedUnderTheAppTransport(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath)

	envelope, err := Review{Subject: subjC1(), RequestID: "r-1", Body: reviewerJSON}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	body := envelope + "\n" + reviewerJSON

	// From the wrong account: ignored, however well formed.
	m.comments = append(m.comments, map[string]any{
		"body": body,
		"user": map[string]any{"login": "someone-else", "id": 424242},
	})
	got, err := Reviews(context.Background(), box)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a comment from the wrong GitHub account was read as a review: %+v", got)
	}

	// From the configured reviewer: an answer.
	m.comments = append(m.comments, map[string]any{
		"body": body,
		"user": map[string]any{"login": "davecourtois", "id": 1697116},
	})
	got, err = Reviews(context.Background(), box)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the configured reviewer's answer was not read: %d found", len(got))
	}
	if got[0].AuthorID != 1697116 {
		t.Errorf("authenticated id = %d, want 1697116", got[0].AuthorID)
	}
	if !got[0].Answers(reqC1()) {
		t.Error("the answer did not match its request")
	}
}

// 12. App selected but unusable: refuse. Never fall back to the operator's gh.
func TestSelectedAppTransportRefusesRatherThanFallingBackToPersonalGH(t *testing.T) {
	box := Issue{
		Dir:              t.TempDir(), // a gh fallback would try to use this
		Number:           "156",
		API:              &AppClient{}, // selected, but not configured
		ExpectedReviewer: Principal{UserID: 1697116},
	}
	err := PostRequest(context.Background(), box, reqC1(), "")
	if err == nil {
		t.Fatal("an unconfigured app transport posted anyway")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("the refusal should say it refused rather than fell back: %v", err)
	}
	if _, err = Reviews(context.Background(), box); err == nil {
		t.Fatal("an unconfigured app transport read anyway")
	}
}

// 11. With no App configured, behaviour is exactly what it was.
func TestWithoutAppConfigurationTheGHPathIsUnchanged(t *testing.T) {
	box := Issue{Dir: t.TempDir(), Number: "156",
		ExpectedReviewer: Principal{UserID: 1697116}}
	if box.API != nil {
		t.Fatal("a mailbox with no App configuration should carry no API client")
	}
	// Reaches the gh path and fails there because the temp dir is not a repo —
	// which is the pre-existing behaviour, not an App refusal.
	_, err := Reviews(context.Background(), box)
	if err == nil {
		t.Fatal("expected the gh path to fail in a non-repository")
	}
	if strings.Contains(err.Error(), "app transport") {
		t.Errorf("a mailbox with no App configuration took the App path: %v", err)
	}
}

// 13 (transport half). The installation token must not reach an error a caller
// could log.
func TestInstallationTokenNeverAppearsInTransportErrors(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_SUPER_SECRET_VALUE","expires_at":%q}`,
			time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/156/comments",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"boom"}`)
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	box := Issue{Number: "156",
		API: &AppClient{
			Auth:  &InstallationAuth{AppID: 4850747, InstallationID: 159521273, PrivateKeyPath: keyPath, APIBase: srv.URL},
			Owner: "globulario", Repo: "sensei-code"},
		ExpectedReviewer: Principal{UserID: 1697116}}

	err := PostRequest(context.Background(), box, reqC1(), "")
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Contains(err.Error(), "ghs_SUPER_SECRET_VALUE") {
		t.Fatalf("the installation token appeared in an error: %v", err)
	}
	if _, rerr := Reviews(context.Background(), box); rerr == nil {
		t.Fatal("expected a read failure")
	} else if strings.Contains(rerr.Error(), "ghs_SUPER_SECRET_VALUE") {
		t.Fatalf("the installation token appeared in a read error: %v", rerr)
	}
}

// The repository is configuration. It must never be inferred from the working
// directory, a remote, or an issue URL.
func TestRepositoryIsConfigurationNotAmbientState(t *testing.T) {
	c := &AppClient{Auth: &InstallationAuth{AppID: 1, InstallationID: 2, PrivateKeyPath: "/x"}}
	if c.Configured() {
		t.Fatal("a client with no owner/repo reported itself configured")
	}
	c.Owner = "globulario"
	if c.Configured() {
		t.Fatal("a client with an owner but no repo reported itself configured")
	}
	c.Repo = "sensei-code"
	if !c.Configured() {
		t.Fatal("a fully configured client was refused")
	}
}

// The app probe is not a review and must never parse as one, whoever posted it.
func TestTheAppProbeIsNotAReview(t *testing.T) {
	probe := "[sensei-code:app-probe]\napp_id=4850747\ninstallation_id=159521273\n"
	if _, ok := ParseReview(probe, "globulario-sensei-code[bot]"); ok {
		t.Fatal("the app probe parsed as a review")
	}
	if _, ok := ParseRequest(probe); ok {
		t.Fatal("the app probe parsed as a review request")
	}
}

// The App bot is not the expected reviewer. Sensei Code's own machine identity
// must never be able to answer Sensei Code's own review request.
func TestTheAppBotCannotAnswerAsTheReviewer(t *testing.T) {
	box := Issue{Number: "156", ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"}}
	const botID = 325661868
	if box.ExpectedReviewer.Matches(botID, "globulario-sensei-code[bot]") {
		t.Fatal("the App bot authenticated as the reviewer — the machine could review its own work")
	}
}
