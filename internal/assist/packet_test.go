package assist

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/sensei"
)

type fakeSensei struct {
	calls  []string
	fail   string
	empty  string
	domain string
	args   map[string]map[string]any
}

func (f *fakeSensei) CallTool(name string, args map[string]any) (sensei.ToolResult, error) {
	f.calls = append(f.calls, name)
	if f.args == nil {
		f.args = map[string]map[string]any{}
	}
	f.args[name] = args
	if name == f.fail {
		return sensei.ToolResult{}, errors.New("unavailable")
	}
	if name == f.empty {
		// A well-formed transport success that carries no evidence at all.
		return sensei.ToolResult{}, nil
	}
	result := sensei.ToolResult{Structured: map[string]any{"tool": name}}
	if name == "sensei_workspace_status" && f.domain != "" {
		result.Structured["binding"] = map[string]any{"repository_domain": f.domain}
	}
	result.Content = append(result.Content, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "text", Text: name + " result"})
	return result, nil
}

func TestBuildUsesSenseiAndCarriesProvenance(t *testing.T) {
	repo := initTestRepo(t)
	caller := &fakeSensei{}
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	packet, err := Build(context.Background(), repo, caller, "task-1", "fix bootstrap", []string{"a.go", "a.go", " b.go "}, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"sensei_workspace_status", "awareness_preflight"}; !reflect.DeepEqual(caller.calls, want) {
		t.Fatalf("Sensei calls = %#v, want %#v", caller.calls, want)
	}
	if packet.WorkspaceStatus.Source != "sensei:sensei_workspace_status" || packet.Preflight.Source != "sensei:awareness_preflight" {
		t.Fatalf("packet lost Sensei provenance: %#v", packet)
	}
	if packet.WorkspaceStatus.State != Present || packet.Preflight.State != Present {
		t.Fatalf("successful Sensei observations must be present: %#v", packet)
	}
	if packet.Authority.Mode != "assisted" || packet.Authority.Admission != "not-requested" {
		t.Fatalf("assisted packet made an authority claim: %#v", packet.Authority)
	}
	if want := []string{"a.go", "b.go"}; !reflect.DeepEqual(packet.Files, want) {
		t.Fatalf("files = %#v, want %#v", packet.Files, want)
	}
}

func TestBuildFailsClosedWhenRequiredSenseiEvidenceIsUnavailable(t *testing.T) {
	repo := initTestRepo(t)
	caller := &fakeSensei{fail: "awareness_preflight"}
	_, err := Build(context.Background(), repo, caller, "task-1", "fix bootstrap", nil, time.Now())
	if err == nil {
		t.Fatal("Build succeeded without required Sensei preflight")
	}
}

func TestBuildScopesPreflightToTheDomainSenseiStated(t *testing.T) {
	repo := initTestRepo(t)
	caller := &fakeSensei{domain: "github.com/globulario/sensei-code"}
	if _, err := Build(context.Background(), repo, caller, "task-1", "fix bootstrap", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := caller.args["awareness_preflight"]["domain"]; got != "github.com/globulario/sensei-code" {
		t.Fatalf("preflight domain = %v, want the domain Sensei stated in the identity receipt", got)
	}
}

func TestBuildLeavesPreflightUnscopedWhenSenseiStatesNoDomain(t *testing.T) {
	repo := initTestRepo(t)
	caller := &fakeSensei{}
	if _, err := Build(context.Background(), repo, caller, "task-1", "fix bootstrap", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := caller.args["awareness_preflight"]["domain"]; ok {
		t.Fatal("Build invented a domain Sensei never asserted")
	}
}

func TestBuildFailsClosedWhenSenseiAnswersWithoutEvidence(t *testing.T) {
	repo := initTestRepo(t)
	caller := &fakeSensei{empty: "awareness_preflight"}
	_, err := Build(context.Background(), repo, caller, "task-1", "fix bootstrap", nil, time.Now())
	if err == nil {
		t.Fatal("Build accepted an empty Sensei response as present evidence")
	}
}

func TestHandoffBindsToExactContextAndCannotClaimAdmission(t *testing.T) {
	contextPacket := ContextPacket{
		Version:         PacketVersion,
		TaskID:          "task-1",
		Task:            "fix bootstrap",
		Repository:      "/repo",
		BaseSHA:         "0123456789abcdef",
		CreatedAt:       time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
		WorkspaceStatus: Observation{State: Present, Source: "sensei:sensei_workspace_status"},
		Preflight:       Observation{State: Present, Source: "sensei:awareness_preflight"},
		Authority:       Authority{Mode: "assisted", Admission: "not-requested"},
	}
	handoff, err := NewHandoff(contextPacket, "claude", "codex", "implementation is half complete", []string{"preserve API"}, []string{"a.go"}, []string{"go test ./... PASS"}, []string{"verify timeout"}, time.Date(2026, 8, 15, 12, 5, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if handoff.ContextDigest == "" {
		t.Fatal("handoff has no context digest")
	}
	mutated := contextPacket
	mutated.Task = "different task"
	if err := handoff.ValidateAgainst(mutated); err == nil {
		t.Fatal("handoff accepted a different context packet")
	}
	handoff.Authority.Admission = "admitted"
	if err := handoff.ValidateAgainst(contextPacket); err == nil {
		t.Fatal("assisted handoff was allowed to claim admission")
	}
}

func TestUnavailableObservationRequiresReason(t *testing.T) {
	o := UnavailableObservation("sensei:runtime-coverage", "no crossing source")
	if err := validateObservation(o); err != nil {
		t.Fatal(err)
	}
	o.Reason = ""
	if err := validateObservation(o); err == nil {
		t.Fatal("unavailable observation without reason was accepted")
	}
}

func initTestRepo(t *testing.T) gitx.Repo {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "sensei-code@example.invalid")
	runGit(t, dir, "config", "user.name", "Sensei Code Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-q", "-m", "initial")
	return gitx.Repo{Root: dir}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func scopedContext(files []string) ContextPacket {
	return ContextPacket{
		Version:         PacketVersion,
		TaskID:          "task-1",
		Task:            "fix bootstrap",
		Repository:      "/repo",
		BaseSHA:         "0123456789abcdef",
		Files:           files,
		CreatedAt:       time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
		WorkspaceStatus: Observation{State: Present, Source: "sensei:sensei_workspace_status"},
		Preflight:       Observation{State: Present, Source: "sensei:awareness_preflight"},
		Authority:       Authority{Mode: "assisted", Admission: "not-requested"},
	}
}

func TestHandoffRefusesWorkOutsideTheContextFileScope(t *testing.T) {
	ctxPacket := scopedContext([]string{"a.go"})
	_, err := NewHandoff(ctxPacket, "claude", "codex", "half done", nil,
		[]string{"a.go", "b.go"}, nil, nil, time.Now())
	if err == nil {
		t.Fatal("handoff accepted work on a file the context packet never preflighted")
	}
	if !strings.Contains(err.Error(), "b.go") {
		t.Fatalf("error should name the out-of-scope file, got: %v", err)
	}
}

func TestHandoffAllowsWorkInsideTheContextFileScope(t *testing.T) {
	ctxPacket := scopedContext([]string{"a.go", "b.go"})
	if _, err := NewHandoff(ctxPacket, "claude", "codex", "half done", nil,
		[]string{"a.go"}, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestHandoffWithNoDeclaredScopeIsUnconstrained(t *testing.T) {
	// A task-only packet asserted no file scope, so there is nothing to escape.
	if _, err := NewHandoff(scopedContext(nil), "claude", "codex", "half done", nil,
		[]string{"anything.go"}, nil, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestContextPacketRequiresATaskID(t *testing.T) {
	p := scopedContext([]string{"a.go"})
	p.TaskID = ""
	if err := p.Validate(); err == nil {
		t.Fatal("a context packet with no task id was accepted, so two tasks would be indistinguishable")
	}
}

// TestObservedDistinguishesStaleFromPresent covers the state that was declared
// in the Presence enum from the start and never once constructed.
//
// A stale graph is the worst case for an assisted packet: it answers, and its
// answer is fluent, specific and wrong. Recording it as Present hands an agent
// confident architectural context derived from a graph that no longer describes
// the repository.
func TestObservedDistinguishesStaleFromPresent(t *testing.T) {
	stale := sensei.ToolResult{
		Structured: map[string]any{
			"status": "PREFLIGHT_STATUS_OK",
			"authority": map[string]any{
				"authoritative":          true,
				"graph_freshness_state":  "GRAPH_FRESHNESS_STATE_STALE",
				"graph_freshness_detail": "live store does not match the validated artifact",
				"seed_state":             "SEED_STATE_CURRENT",
			},
		},
	}
	got := observed("awareness_preflight", stale)
	if got.State != Stale {
		t.Fatalf("a stale graph was classified %q, not stale", got.State)
	}
	if !strings.Contains(got.Reason, "stale") {
		t.Fatalf("stale observation does not explain itself: %q", got.Reason)
	}
	if err := requireEvidence("preflight", got); err == nil {
		t.Fatal("stale evidence satisfied a required surface")
	}
}

// TestObservedDistinguishesProvenEmptyFromAbsent covers the other never-built
// state. "Sensei has no coverage here" is a finding; "Sensei did not answer" is
// not, and collapsing them lets an unanswered question read as a clean report.
func TestObservedDistinguishesProvenEmptyFromAbsent(t *testing.T) {
	empty := sensei.ToolResult{
		Structured: map[string]any{
			"status": "PREFLIGHT_STATUS_EMPTY",
			"authority": map[string]any{
				"authoritative":         true,
				"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
				"seed_state":            "SEED_STATE_CURRENT",
			},
		},
	}
	got := observed("awareness_preflight", empty)
	if got.State != EmptyProven {
		t.Fatalf("a certified empty answer was classified %q", got.State)
	}
	// It is an answer, so it does not fail a required surface.
	if err := requireEvidence("preflight", got); err != nil {
		t.Fatalf("a proven empty was treated as missing evidence: %v", err)
	}

	// And it remains distinct from silence.
	silent := observed("awareness_preflight", sensei.ToolResult{})
	if silent.State != Absent {
		t.Fatalf("silence was classified %q, not absent", silent.State)
	}
	if err := requireEvidence("preflight", silent); err == nil {
		t.Fatal("silence satisfied a required surface")
	}
}

// TestStalenessBeatsEmptiness pins the ordering. "Nothing here" from a graph
// that cannot vouch for itself proves nothing, so it must not be recorded as a
// proven empty.
func TestStalenessBeatsEmptiness(t *testing.T) {
	both := sensei.ToolResult{
		Structured: map[string]any{
			"status": "PREFLIGHT_STATUS_EMPTY",
			"authority": map[string]any{
				"authoritative":         true,
				"graph_freshness_state": "GRAPH_FRESHNESS_STATE_STALE",
				"seed_state":            "SEED_STATE_CURRENT",
			},
		},
	}
	if got := observed("awareness_preflight", both); got.State != Stale {
		t.Fatalf("an empty answer from a stale graph was recorded as %q", got.State)
	}
}

// TestEverySurfaceStateIsReachable is the regression guard for the whole class
// of bug: a typed state that exists only in the enum is a state the system
// cannot express, and the type checker will never say so.
func TestEverySurfaceStateIsReachable(t *testing.T) {
	healthy := map[string]any{
		"authoritative":         true,
		"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
		"seed_state":            "SEED_STATE_CURRENT",
	}
	seen := map[Presence]bool{
		observed("s", sensei.ToolResult{}).State: true,
		observed("s", sensei.ToolResult{Structured: map[string]any{"status": "PREFLIGHT_STATUS_OK", "authority": healthy}}).State:    true,
		observed("s", sensei.ToolResult{Structured: map[string]any{"status": "PREFLIGHT_STATUS_EMPTY", "authority": healthy}}).State: true,
		observed("s", sensei.ToolResult{Structured: map[string]any{"authority": map[string]any{
			"authoritative": true, "graph_freshness_state": "GRAPH_FRESHNESS_STATE_STALE", "seed_state": "SEED_STATE_CURRENT",
		}}}).State: true,
		UnavailableObservation("s", "no sensei").State: true,
	}
	for _, want := range []Presence{Present, EmptyProven, Absent, Stale, Unavailable} {
		if !seen[want] {
			t.Errorf("no input produces the %q state, so it is declared but unreachable", want)
		}
	}
}
