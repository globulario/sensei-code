package main

import (
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/control"
	"github.com/globulario/sensei-code/internal/ghwebhook"
)

// The MCP control surface and the GitHub webhook ingress are two listeners with
// two authentication regimes:
//
//	MCP              Bearer credential   control.Endpoint
//	GitHub webhook   HMAC-SHA256         ghwebhook.Endpoint
//
// They must not converge. A single surface holding both would eventually
// authenticate one regime's request with the other's rule, and that failure is
// a silent authorization bypass rather than an error anybody sees.

func TestTheMCPAndWebhookSurfacesAreSeparate(t *testing.T) {
	if control.Endpoint == ghwebhook.Endpoint {
		t.Fatalf("both surfaces answer on %q; one path cannot carry two authentication regimes", control.Endpoint)
	}
	if strings.HasPrefix(ghwebhook.Endpoint, control.Endpoint) || strings.HasPrefix(control.Endpoint, ghwebhook.Endpoint) {
		t.Errorf("the endpoints %q and %q are prefixes of one another; a mux change could merge them",
			control.Endpoint, ghwebhook.Endpoint)
	}
}

// Two listeners, bound separately, from separate configuration. The source pin
// exists because the failure it catches -- someone serving the webhook path on
// the MCP mux "to avoid a second port" -- produces no test failure anywhere
// else, and would hand the webhook path the Bearer regime.
func TestTheControlCommandBindsTheWebhookOnItsOwnListener(t *testing.T) {
	src, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatalf("read control.go: %v", err)
	}
	text := string(src)

	for _, want := range []string{
		// Its own configuration, its own construction.
		"ghwebhook.Config{",
		// Its own socket, not the control server's.
		"webhook.Listen()",
		"webhook.Serve()",
		// The control surface still binds its own.
		"server.Listen(*addr)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("control.go no longer contains %q; the two listeners may have been merged", want)
		}
	}

	// The webhook must never be mounted onto the control server's handler.
	for _, forbidden := range []string{
		"server.Handler().(*http.ServeMux)",
		"mux.Handle(ghwebhook.Endpoint",
		"server.Listen(*whAddr)",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("control.go contains %q: the webhook is being served by the MCP surface", forbidden)
		}
	}
}

// The ingress is wired to an observation sink and to nothing else. If a later
// change hands it the engine, this fails.
func TestTheWebhookIngressIsWiredToAnObservationSinkOnly(t *testing.T) {
	src, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatalf("read control.go: %v", err)
	}
	text := string(src)

	if !strings.Contains(text, "ghwebhook.NewObservationSink(") {
		t.Fatal("the webhook ingress no longer uses the observation sink")
	}
	// The sink construction must not be handed anything governed.
	at := strings.Index(text, "}.Server(")
	end := strings.Index(text[at:], "\n")
	wiring := text[at : at+end]
	for _, forbidden := range []string{"engine", "server", "runners", "SubmitGoverned"} {
		if strings.Contains(wiring, forbidden) {
			t.Errorf("the webhook sink is wired to %q: a delivery would gain governed effect (%s)", forbidden, wiring)
		}
	}
}

// The secret reaches the process as a PATH. Never as a value on the command
// line, where it would be visible in ps output to every user on the machine,
// and never printed back.
func TestTheWebhookSecretIsConfiguredByPathAndNeverPrinted(t *testing.T) {
	src, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatalf("read control.go: %v", err)
	}
	text := string(src)

	if !strings.Contains(text, `fs.String("github-webhook-secret-file"`) {
		t.Error("the webhook secret is not configured by file path")
	}
	for _, forbidden := range []string{
		`fs.String("github-webhook-secret"`,
		`os.Getenv("SENSEI_CODE_WEBHOOK_SECRET")`,
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("control.go contains %q: the secret VALUE is configuration, not just its path", forbidden)
		}
	}

	// The banner prints the path and says so.
	at := strings.Index(text, "func printWebhookBanner(")
	if at < 0 {
		t.Fatal("printWebhookBanner is gone")
	}
	banner := text[at:]
	if !strings.Contains(banner, "secretPath") {
		t.Error("the webhook banner does not report the secret path")
	}
	if !strings.Contains(banner, "never printed") {
		t.Error("the webhook banner does not state that the value is not printed")
	}
}

// All five flags exist, so an operator cannot half-configure the ingress by
// reaching for a name that was never added.
func TestTheWebhookFlagsAreAllPresent(t *testing.T) {
	src, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatalf("read control.go: %v", err)
	}
	for _, flag := range []string{
		"github-webhook-addr",
		"github-webhook-secret-file",
		"github-webhook-installation-id",
		"github-webhook-repository-id",
		"github-webhook-repository",
	} {
		if !strings.Contains(string(src), `"`+flag+`"`) {
			t.Errorf("the -%s flag is missing", flag)
		}
	}
}
