package assist

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
