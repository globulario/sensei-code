package ghbridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// ---------- snapshot: a projection, proven, not a second measurement ----------

// tempRepo builds a real repository with one base commit and a second tree, so
// the snapshot proofs run against git rather than against a mock of it.
func tempRepo(t *testing.T) (dir, base, tree1, tree2 string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := writeFile(filepath.Join(dir, "a.txt"), "one"); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "base")
	base = run("rev-parse", "HEAD")
	tree1 = run("rev-parse", "HEAD^{tree}")

	if err := writeFile(filepath.Join(dir, "a.txt"), "two"); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "second")
	tree2 = run("rev-parse", "HEAD^{tree}")
	return dir, base, tree1, tree2
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestSnapshotVerificationRefusesAWrongTree(t *testing.T) {
	dir, base, tree1, tree2 := tempRepo(t)
	ctx := context.Background()

	// Build a commit over tree2 but claim it is tree1.
	cmd := exec.Command("git", "commit-tree", tree2, "-p", base, "-m", "x")
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("commit-tree: %v: %s", err, out)
	}
	commit := strings.TrimSpace(string(out))

	err = VerifySnapshot(ctx, dir, commit, Subject{
		TaskID: "T-1", BaseSHA: base, CandidateDigest: digestC1, CandidateTree: tree1,
	})
	if err == nil {
		t.Fatal("a snapshot whose tree is not the candidate tree was accepted")
	}
	if !strings.Contains(err.Error(), "tree") {
		t.Errorf("refusal should name the tree mismatch: %v", err)
	}
}

func TestSnapshotVerificationRefusesAWrongParent(t *testing.T) {
	dir, base, tree1, tree2 := tempRepo(t)
	ctx := context.Background()

	// Parent it on the wrong commit.
	wrongParent := func() string {
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = dir
		o, _ := cmd.CombinedOutput()
		return strings.TrimSpace(string(o))
	}()

	cmd := exec.Command("git", "commit-tree", tree1, "-p", wrongParent, "-m", "x")
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("commit-tree: %v: %s", err, out)
	}
	commit := strings.TrimSpace(string(out))
	_ = tree2

	err = VerifySnapshot(ctx, dir, commit, Subject{
		TaskID: "T-1", BaseSHA: base, CandidateDigest: digestC1, CandidateTree: tree1,
	})
	if err == nil {
		t.Fatal("a snapshot parented off the wrong base was accepted")
	}
	if !strings.Contains(err.Error(), "parent") && !strings.Contains(err.Error(), "base") {
		t.Errorf("refusal should name the parent mismatch: %v", err)
	}
}

func TestSnapshotVerificationAcceptsATrueProjection(t *testing.T) {
	dir, base, tree1, _ := tempRepo(t)
	cmd := exec.Command("git", "commit-tree", tree1, "-p", base, "-m", "x")
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("commit-tree: %v: %s", err, out)
	}
	commit := strings.TrimSpace(string(out))

	if err := VerifySnapshot(context.Background(), dir, commit, Subject{
		TaskID: "T-1", BaseSHA: base, CandidateDigest: digestC1, CandidateTree: tree1,
	}); err != nil {
		t.Fatalf("a true projection was refused: %v", err)
	}
}

func TestPublishRefusesAMalformedSubject(t *testing.T) {
	dir, base, tree1, _ := tempRepo(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		s    Subject
	}{
		{"no tree", Subject{TaskID: "T", BaseSHA: base, CandidateDigest: digestC1}},
		{"no base", Subject{TaskID: "T", CandidateTree: tree1, CandidateDigest: digestC1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PublishSnapshot(ctx, dir, "origin", tc.s, "r-1"); err == nil {
				t.Fatal("expected refusal before anything was pushed")
			}
		})
	}
}

// ---------- runner ----------

func TestRunnerServesReviewerOnly(t *testing.T) {
	r := &Runner{Issue: Issue{Dir: t.TempDir(), Number: "1"}, NewRequestID: func() string { return "r-1" }}
	for _, role := range []roles.Role{roles.Architect, roles.Implementer} {
		_, err := r.Run(context.Background(), agent.Request{TaskID: "T-1", Role: role}, nil)
		if !errors.Is(err, ErrNotReviewer) {
			t.Errorf("role %s: expected ErrNotReviewer, got %v", role, err)
		}
	}
}

// A reviewer turn with no exact binding must refuse before publishing anything.
func TestRunnerRefusesAnUnboundSubject(t *testing.T) {
	r := &Runner{Issue: Issue{Dir: t.TempDir(), Number: "1"}, NewRequestID: func() string { return "r-1" }}
	for _, tc := range []struct {
		name string
		b    roles.Binding
	}{
		{"nothing", roles.Binding{TaskID: "T-1"}},
		{"no tree", roles.Binding{TaskID: "T-1", BaseSHA: baseSHA, CandidateDigest: digestC1}},
		{"no digest", roles.Binding{TaskID: "T-1", BaseSHA: baseSHA, CandidateTree: treeC1}},
		{"no base", roles.Binding{TaskID: "T-1", CandidateDigest: digestC1, CandidateTree: treeC1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Run(context.Background(),
				agent.Request{TaskID: "T-1", Role: roles.Reviewer, Binding: tc.b}, nil)
			if !errors.Is(err, ErrUnboundSubject) {
				t.Fatalf("expected ErrUnboundSubject, got %v", err)
			}
		})
	}
}

// ---------- resolver composition ----------

type recordingResolver struct{ saw []roles.Role }

func (r *recordingResolver) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	r.saw = append(r.saw, spec.Role)
	return workflow.Resolved{Name: "fallback", Label: "Fallback"}, nil
}

// The bridge carries the ASSIGNED reviewer. It must not rename the assignment:
// RoleAssigned.Provider and ReviewVerdict.Provider have to agree.
func TestTheBridgePreservesTheAssignedProviderIdentity(t *testing.T) {
	fb := &recordingResolver{}
	res := Resolver{Provider: "chatgpt", Reviewer: &Runner{}, Fallback: fb}

	got, err := res.Resolve(workflow.RunnerSpec{
		Role: roles.Reviewer, Agent: config.Agent{Name: "chatgpt"}})
	if err != nil {
		t.Fatalf("assigned chatgpt reviewer: %v", err)
	}
	if got.Name != "chatgpt" {
		t.Errorf("resolved provider = %q, want the assignment's own name \"chatgpt\"", got.Name)
	}
	if got.Label != ResolverLabel {
		t.Errorf("label = %q, want %q", got.Label, ResolverLabel)
	}
	if len(fb.saw) != 0 {
		t.Errorf("the carried reviewer reached the fallback: %v", fb.saw)
	}
}

// A reviewer assigned to a DIFFERENT provider is not this bridge's turn.
func TestAnotherAssignedReviewerReachesTheExistingResolver(t *testing.T) {
	fb := &recordingResolver{}
	res := Resolver{Provider: "chatgpt", Reviewer: &Runner{}, Fallback: fb}

	got, err := res.Resolve(workflow.RunnerSpec{
		Role: roles.Reviewer, Agent: config.Agent{Name: "codex"}})
	if err != nil {
		t.Fatalf("codex reviewer: %v", err)
	}
	if got.Name != "fallback" {
		t.Errorf("an assigned codex reviewer was captured by the github bridge: %s", got.Name)
	}
	if len(fb.saw) != 1 || fb.saw[0] != roles.Reviewer {
		t.Errorf("fallback saw %v, want one reviewer turn", fb.saw)
	}
}

func TestNonReviewerRolesAlwaysReachTheExistingResolver(t *testing.T) {
	fb := &recordingResolver{}
	res := Resolver{Provider: "chatgpt", Reviewer: &Runner{}, Fallback: fb}
	for _, role := range []roles.Role{roles.Architect, roles.Implementer} {
		got, err := res.Resolve(workflow.RunnerSpec{Role: role, Agent: config.Agent{Name: "chatgpt"}})
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if got.Name != "fallback" {
			t.Errorf("%s did not reach the existing resolver: %s", role, got.Name)
		}
	}
	if len(fb.saw) != 2 {
		t.Errorf("fallback saw %v, want architect and implementer", fb.saw)
	}
}

// An unconfigured Resolver captures nothing rather than everything.
func TestAnUnconfiguredBridgeCarriesNothing(t *testing.T) {
	fb := &recordingResolver{}
	res := Resolver{Reviewer: &Runner{}, Fallback: fb}
	got, err := res.Resolve(workflow.RunnerSpec{Role: roles.Reviewer, Agent: config.Agent{Name: "chatgpt"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "fallback" {
		t.Errorf("a bridge with no configured provider captured a reviewer turn: %s", got.Name)
	}
}

// If the carried reviewer's transport cannot serve, REFUSE. workflow never
// recovers from a resolver refusal, so this is what stops a remote reviewer
// quietly becoming the local one. The engine may still pick an explicitly
// recorded fallback reviewer through its own assignment ladder.
func TestUnavailableTransportRefusesRatherThanSubstituting(t *testing.T) {
	fb := &recordingResolver{}
	res := Resolver{Provider: "chatgpt", Reviewer: nil, Fallback: fb}
	_, err := res.Resolve(workflow.RunnerSpec{Role: roles.Reviewer, Agent: config.Agent{Name: "chatgpt"}})
	if !errors.Is(err, ErrBridgeUnavailable) {
		t.Fatalf("expected ErrBridgeUnavailable, got %v", err)
	}
	if len(fb.saw) != 0 {
		t.Fatal("an unavailable transport handed the carried reviewer to the fallback")
	}
}

// ---------- attribution ----------

// A turn answered over a transport is Unverified, and an Unverified answer can
// never satisfy an independent-review obligation however it decided.
func TestTransportAnswersAreUnverifiedAndNeverIndependent(t *testing.T) {
	if roles.Unverified == roles.Fresh {
		t.Fatal("Unverified and Fresh collapsed — a transport answer would claim an observed session")
	}
	p := roles.Provenance{TaskID: "T-1", Role: roles.Reviewer, Provider: "chatgpt",
		SessionMode: roles.Unverified}
	if p.Independent() {
		t.Fatal("an unverified transport answer reported itself independent")
	}
}
