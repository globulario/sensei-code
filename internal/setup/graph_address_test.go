package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSensei puts a stand-in `sensei` on PATH and returns the file its
// invocations are recorded in.
//
// The body decides what the stand-in answers; every call appends its argv to
// the log first, so a test can assert what was asked as well as what came back.
func fakeSensei(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n" + body
	if err := os.WriteFile(filepath.Join(dir, "sensei"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return log
}

func invocations(t *testing.T, log string) []string {
	t.Helper()
	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the stand-in was never invoked: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// A healthy answer in the shape the checks actually parse.
const healthyMetadata = "echo 'Server version:        0.0.6'\n" +
	"echo '  Freshness state:     current'\n" +
	"echo 'Live counts:'\n"

// TestGraphChecksQueryTheConfiguredAddress is the whole point of GraphAddr
// being on Options.
//
// Both graph checks shell out to `sensei metadata`. Neither passed the address
// this repository is configured for, so both asked the CLI's compiled-in
// default while labelling the answer with the configured one.
func TestGraphChecksQueryTheConfiguredAddress(t *testing.T) {
	log := fakeSensei(t, healthyMetadata)
	o := Options{RepoRoot: t.TempDir(), GraphAddr: "localhost:10122"}

	if c := checkGraphServer(context.Background(), o); c.State != OK {
		t.Fatalf("graph server = %q (%s), want ok against a healthy stand-in", c.State, c.Detail)
	}
	if c := checkGraphFreshness(context.Background(), o); c.State != OK {
		t.Fatalf("graph freshness = %q (%s), want ok against a healthy stand-in", c.State, c.Detail)
	}

	calls := invocations(t, log)
	if len(calls) != 2 {
		t.Fatalf("invocations = %d, want one per graph check: %v", len(calls), calls)
	}
	for _, call := range calls {
		if !strings.Contains(call, "-addr localhost:10122") {
			t.Errorf("a graph check asked %q; it must name the configured address, "+
				"or it is reporting on a graph this repository never selected", call)
		}
	}
}

// TestAnAnswerFromAnotherAddressIsNotReportedAsTheConfiguredOne is the failing
// direction that matters, and it is not the one that looks obvious.
//
// A missing -addr shows up as a false RED on a machine where the default port
// serves nothing. On a machine where it serves ANOTHER repository's graph it
// shows up as a false GREEN: the check reports "answering on <configured>"
// about an answer the configured address never gave. The stand-in below is that
// machine — it answers only when the address is left to the default.
func TestAnAnswerFromAnotherAddressIsNotReportedAsTheConfiguredOne(t *testing.T) {
	refuseWhenAddressed := "for a in \"$@\"; do\n" +
		"  if [ \"$a\" = \"-addr\" ]; then\n" +
		"    echo 'metadata unavailable: awareness-graph backend is unreachable' >&2\n" +
		"    exit 1\n" +
		"  fi\n" +
		"done\n" + healthyMetadata
	fakeSensei(t, refuseWhenAddressed)
	o := Options{RepoRoot: t.TempDir(), GraphAddr: "localhost:10122"}

	c := checkGraphServer(context.Background(), o)
	if c.State == OK {
		t.Fatalf("the configured address was never reached, yet the check reports %q: %q — "+
			"a different graph answered and was labelled as this one", c.State, c.Detail)
	}
	if strings.Contains(c.Detail, "answering on") {
		t.Fatalf("detail claims an address answered when it was not asked: %q", c.Detail)
	}

	f := checkGraphFreshness(context.Background(), o)
	if f.State == OK {
		t.Fatalf("freshness = ok from a graph the configured address never served: %q", f.Detail)
	}
}

// TestAnUnconfiguredAddressIsLeftToTheCLI keeps the fix from inventing one.
//
// A checkout that states no address has not chosen the default — it has chosen
// nothing, and the CLI's own default is the honest thing to fall through to. A
// check that passed -addr "" would turn "unstated" into a malformed request.
func TestAnUnconfiguredAddressIsLeftToTheCLI(t *testing.T) {
	log := fakeSensei(t, healthyMetadata)
	o := Options{RepoRoot: t.TempDir()}

	if c := checkGraphServer(context.Background(), o); c.State != OK {
		t.Fatalf("graph server = %q (%s), want ok", c.State, c.Detail)
	}
	for _, call := range invocations(t, log) {
		if strings.Contains(call, "-addr") {
			t.Errorf("an unstated address produced %q; nothing here chose an address to send", call)
		}
	}
}
