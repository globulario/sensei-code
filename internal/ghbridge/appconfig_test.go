package ghbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const keyPathFixture = "/tmp/does-not-need-to-exist.pem"

func fullAppConfig() AppConfig {
	return AppConfig{AppID: 4850747, InstallationID: 159521273,
		PrivateKeyPath: keyPathFixture, Owner: "globulario", Repo: "sensei-code"}
}

// FINDING 1. Zero App configuration keeps the legacy gh path exactly as it was.
func TestNoAppConfigurationKeepsTheLegacyPath(t *testing.T) {
	var none AppConfig
	if none.Selected() {
		t.Fatal("an empty config selected App transport")
	}
	client, err := none.Client()
	if err != nil {
		t.Fatalf("an empty config should not be an error: %v", err)
	}
	if client != nil {
		t.Fatal("an empty config produced an App client")
	}
}

// FINDING 1. ANY App field selects App transport; incomplete then REFUSES and
// never falls through to the operator's gh credentials.
func TestEveryPartialAppConfigurationRefusesAndNeverSelectsGH(t *testing.T) {
	full := fullAppConfig()

	// Each case drops exactly one required field from a complete config.
	drops := map[string]func(*AppConfig){
		"app id absent":          func(c *AppConfig) { c.AppID = 0 },
		"installation id absent": func(c *AppConfig) { c.InstallationID = 0 },
		"key path absent":        func(c *AppConfig) { c.PrivateKeyPath = "" },
		"owner absent":           func(c *AppConfig) { c.Owner = "" },
		"repo absent":            func(c *AppConfig) { c.Repo = "" },
		"key path blank":         func(c *AppConfig) { c.PrivateKeyPath = "   " },
		"owner blank":            func(c *AppConfig) { c.Owner = "  " },
	}
	for name, drop := range drops {
		t.Run(name, func(t *testing.T) {
			c := full
			drop(&c)
			if !c.Selected() {
				t.Fatal("a partially configured App was not treated as selected — it would take the gh path")
			}
			client, err := c.Client()
			if err == nil {
				t.Fatal("a partially configured App did not refuse")
			}
			if client != nil {
				t.Fatal("a refused config still produced a client")
			}
			if !strings.Contains(err.Error(), "refusing rather than falling back") {
				t.Errorf("the refusal must say it did not fall back: %v", err)
			}
		})
	}

	// The case the finding names explicitly: everything but the app id.
	t.Run("installation+key+owner+repo, app id forgotten", func(t *testing.T) {
		c := AppConfig{InstallationID: 159521273, PrivateKeyPath: keyPathFixture,
			Owner: "globulario", Repo: "sensei-code"}
		if !c.Selected() {
			t.Fatal("this is plainly an operator configuring App transport; it must be selected")
		}
		if _, err := c.Client(); err == nil {
			t.Fatal("it silently took the personal-gh path")
		} else if !strings.Contains(err.Error(), "app id") {
			t.Errorf("the refusal should name the missing app id: %v", err)
		}
	})
}

// Each single field, alone, is enough to mean "App transport was intended".
func TestAnySingleAppFieldSelectsAppTransport(t *testing.T) {
	for name, c := range map[string]AppConfig{
		"app id only":          {AppID: 4850747},
		"installation id only": {InstallationID: 159521273},
		"key path only":        {PrivateKeyPath: keyPathFixture},
		"owner only":           {Owner: "globulario"},
		"repo only":            {Repo: "sensei-code"},
	} {
		t.Run(name, func(t *testing.T) {
			if !c.Selected() {
				t.Fatal("not selected — this configuration would take the gh path")
			}
			if _, err := c.Client(); err == nil {
				t.Fatal("an incomplete configuration was accepted")
			}
		})
	}
}

func TestCompleteAppConfigurationBuildsTheClient(t *testing.T) {
	client, err := fullAppConfig().Client()
	if err != nil {
		t.Fatalf("a complete configuration was refused: %v", err)
	}
	if client == nil || !client.Configured() {
		t.Fatal("a complete configuration produced an unusable client")
	}
	if client.Owner != "globulario" || client.Repo != "sensei-code" {
		t.Errorf("repository not carried: %s/%s", client.Owner, client.Repo)
	}
}

// FINDING 2. No exported production API may hand out a credential.
func TestNoExportedMethodReturnsCredentialMaterial(t *testing.T) {
	forbidden := map[string]bool{"Token": true, "JWT": true, "Assertion": true,
		"PrivateKey": true, "Key": true, "Secret": true, "Credential": true}

	for _, typ := range []reflect.Type{
		reflect.TypeOf(&InstallationAuth{}),
		reflect.TypeOf(&AppClient{}),
		reflect.TypeOf(AppConfig{}),
		reflect.TypeOf(Issue{}),
	} {
		for i := 0; i < typ.NumMethod(); i++ {
			m := typ.Method(i)
			if forbidden[m.Name] {
				t.Errorf("%s has an exported %s method — the installation credential must not leave this package",
					typ, m.Name)
			}
		}
	}

	// The unexported accessor still exists and is what AppClient uses.
	if _, ok := reflect.TypeOf(&InstallationAuth{}).MethodByName("Token"); ok {
		t.Fatal("InstallationAuth.Token is exported again; any importing package could take the installation token")
	}
}

// FINDING 3. Pagination follows Link rel="next" and never truncates silently.
func TestListCommentsFollowsEveryPage(t *testing.T) {
	keyPath, _ := writeTestKey(t)

	// 51 pages: the first 50 full, the 51st short. A fixed 50-page loop would
	// return a result that looked complete and omitted the last page.
	const fullPages = 50
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_x","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/156/comments", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" {
			page = "1"
		}
		var n int
		fmt.Sscanf(page, "%d", &n)

		size := 100
		if n > fullPages {
			size = 3
		}
		batch := make([]map[string]any, 0, size)
		for i := 0; i < size; i++ {
			batch = append(batch, map[string]any{
				"body": fmt.Sprintf("page%d-item%d", n, i),
				"user": map[string]any{"login": "davecourtois", "id": 1697116},
			})
		}
		if n <= fullPages {
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/globulario/sensei-code/issues/156/comments?per_page=100&page=%d>; rel="next"`, base, n+1))
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(batch)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	c := &AppClient{
		Auth:  &InstallationAuth{AppID: 4850747, InstallationID: 159521273, PrivateKeyPath: keyPath, APIBase: srv.URL},
		Owner: "globulario", Repo: "sensei-code"}

	got, err := c.ListComments(context.Background(), "156")
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	want := fullPages*100 + 3
	if len(got) != want {
		t.Fatalf("read %d comments, want %d — page %d was silently dropped", len(got), want, fullPages+1)
	}
	// Order must be preserved across pages.
	if got[0].Body != "page1-item0" {
		t.Errorf("first comment = %q", got[0].Body)
	}
	if got[len(got)-1].Body != fmt.Sprintf("page%d-item2", fullPages+1) {
		t.Errorf("last comment = %q, want the final page's last item", got[len(got)-1].Body)
	}
}

// A single page terminates without a Link header.
func TestListCommentsSinglePage(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_x","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/156/comments", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"body": "only", "user": map[string]any{"login": "davecourtois", "id": 1697116}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &AppClient{
		Auth:  &InstallationAuth{AppID: 4850747, InstallationID: 159521273, PrivateKeyPath: keyPath, APIBase: srv.URL},
		Owner: "globulario", Repo: "sensei-code"}
	got, err := c.ListComments(context.Background(), "156")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Body != "only" {
		t.Fatalf("single page read wrong: %+v", got)
	}
}

// A server that never stops offering a next page must produce an explicit
// error, never a partial slice presented as complete.
func TestUnboundedPaginationRefusesRatherThanTruncating(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_x","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/156/comments", func(w http.ResponseWriter, r *http.Request) {
		// Always another page.
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/globulario/sensei-code/issues/156/comments?page=999>; rel="next"`, base))
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"body": "x", "user": map[string]any{"login": "davecourtois", "id": 1697116}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base = srv.URL

	c := &AppClient{
		Auth:  &InstallationAuth{AppID: 4850747, InstallationID: 159521273, PrivateKeyPath: keyPath, APIBase: srv.URL},
		Owner: "globulario", Repo: "sensei-code"}
	got, err := c.ListComments(context.Background(), "156")
	if err == nil {
		t.Fatalf("an unbounded mailbox returned %d comments as complete", len(got))
	}
	if got != nil {
		t.Fatal("a refused read returned a partial slice")
	}
	if !strings.Contains(err.Error(), "result incomplete") {
		t.Errorf("the error must say the result is incomplete: %v", err)
	}
}

func TestNextPagePathReadsOnlyRelNext(t *testing.T) {
	base := "https://api.github.com"
	link := `<https://api.github.com/x?page=2>; rel="next", <https://api.github.com/x?page=9>; rel="last"`
	if got := nextPagePath(link, base); got != "/x?page=2" {
		t.Errorf("next = %q, want /x?page=2", got)
	}
	// No next: a read terminates.
	if got := nextPagePath(`<https://api.github.com/x?page=1>; rel="prev"`, base); got != "" {
		t.Errorf("a header with no rel=next returned %q", got)
	}
	if got := nextPagePath("", base); got != "" {
		t.Errorf("an empty header returned %q", got)
	}
}
