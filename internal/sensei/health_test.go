package sensei

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// A failure says when the environment was last known good.
//
// The point of the whole file: "dial tcp 127.0.0.1:10199: connect: connection
// refused" is accurate about the socket and useless about the cause. Someone
// reading this log hours later needs to know the run WAS working and when it
// stopped.
func TestAFailureSaysWhenTheEnvironmentWasLastGood(t *testing.T) {
	last := time.Now().Add(-7 * time.Minute)
	got := Causes{
		Tool: "awareness_preflight",
		Err:  errors.New("preflight unavailable: awareness-graph backend is unreachable; dial tcp 127.0.0.1:10199: connect: connection refused"),
		Health: Health{
			LastOK: last, LastTool: "sensei_workspace_status", SubprocessAlive: true,
			Stderr: "sensei serve: oxigraph did not become ready in 10s",
		},
		Elapsed: time.Since(last),
	}.Diagnose()

	for _, required := range []string{
		"awareness_preflight",           // which call
		"Sensei last answered",          // when it was good
		"sensei_workspace_status",       // what worked last
		"still running",                 // the client did not crash
		"oxigraph did not become ready", // the actual cause, from stderr
	} {
		if !strings.Contains(got, required) {
			t.Errorf("the diagnosis omits %q:\n%s", required, got)
		}
	}
}

// Never having worked is a different finding from having stopped.
func TestNeverHavingWorkedIsItsOwnFinding(t *testing.T) {
	got := Causes{Tool: "sensei_workspace_status", Err: errors.New("connection refused"),
		Health: Health{SubprocessAlive: false}}.Diagnose()
	if !strings.Contains(got, "no Sensei call has ever succeeded") {
		t.Fatalf("a never-working environment is reported as one that stopped:\n%s", got)
	}
	if !strings.Contains(got, "NOT running") {
		t.Fatalf("a dead subprocess is not distinguished from a live one:\n%s", got)
	}
}

// The store is quoted, not observed. This process never speaks to oxigraph, and
// a diagnosis that implied otherwise would be the same overclaim this codebase
// keeps making about its own mechanisms.
func TestTheStoreIsQuotedRatherThanObserved(t *testing.T) {
	got := Causes{Tool: "awareness_impact",
		Err:    errors.New("rpc: backend unhealthy at http://127.0.0.1:7899: oxigraph health: 404; other stuff"),
		Health: Health{LastOK: time.Now().Add(-time.Minute), SubprocessAlive: true},
	}.Diagnose()
	if !strings.Contains(got, "quoted from Sensei") {
		t.Fatalf("the diagnosis presents Sensei's report as its own observation:\n%s", got)
	}
	if !strings.Contains(got, "does not observe the store directly") {
		t.Fatalf("the diagnosis does not disclaim what it cannot see:\n%s", got)
	}
}

// The witness observes and does nothing else. A restart or retry hidden here
// would conceal the failures it exists to surface.
func TestTheWitnessOnlyObserves(t *testing.T) {
	src := readSource(t, "health.go")
	for _, forbidden := range []string{"exec.Command", "Restart", "retry", "time.Sleep", "for {"} {
		if strings.Contains(src, forbidden) {
			t.Errorf("the health witness contains %q; it reports and recovers nothing", forbidden)
		}
	}
}

// A refusal is Sensei working correctly and saying no. Dressing it as an outage
// would destroy the distinction the client already keeps between a fail-closed
// answer and a transport fault.
func TestARefusalIsNotDiagnosedAsAnOutage(t *testing.T) {
	src := readSource(t, "mcp.go")
	i := strings.Index(src, "if result.IsError {")
	if i < 0 {
		t.Fatal("the refusal branch is gone")
	}
	branch := src[i : i+400]
	if strings.Contains(branch, "c.diagnose(") {
		t.Fatal("a refusal is reported with dependency state, which reads as an outage")
	}
	if !strings.Contains(branch, "refused") {
		t.Fatal("the refusal no longer says it was refused")
	}
}

var osReadFile = os.ReadFile

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := osReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
