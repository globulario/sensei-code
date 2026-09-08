package ghbridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// An object id is compared for identity, so it must be exactly what git wrote
// to stdout. git puts advisories on stderr, and merging the two made a correct
// id read as a wrong one — VerifySnapshot would accuse the projection of not
// being the candidate when only the reading was polluted.
func TestAnObjectIdNeverCarriesAGitAdvisory(t *testing.T) {
	dir, base, _, _ := tempRepo(t)
	ctx := context.Background()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	// A name that is both a branch and a tag makes git warn while still
	// resolving. Set the switch locally so the advisory does not depend on
	// whatever global config the developer happens to have.
	run("config", "--local", "core.warnAmbiguousRefs", "true")
	run("branch", "ambig", base)
	run("tag", "ambig", base)

	// The witness is only a witness if git really does write to stderr here.
	probe := exec.Command("git", "rev-parse", "ambig")
	probe.Dir = dir
	var probeErr strings.Builder
	probe.Stderr = &probeErr
	if err := probe.Run(); err != nil {
		t.Fatalf("probe rev-parse: %v", err)
	}
	if !strings.Contains(probeErr.String(), "ambiguous") {
		t.Fatalf("this git no longer warns on an ambiguous refname, so the test proves nothing: %q", probeErr.String())
	}

	got, err := git(ctx, dir, "rev-parse", "ambig")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	if got != base {
		t.Errorf("an advisory reached the id: read %q, want the bare id %q", got, base)
	}
}

// A real git failure must still say what git said, or the refusal names no cause.
func TestAFailedGitCommandReportsWhatGitWroteToStderr(t *testing.T) {
	dir, _, _, _ := tempRepo(t)

	out, err := git(context.Background(), dir, "rev-parse", "--verify", "no-such-ref")
	if err == nil {
		t.Fatal("resolving a ref that does not exist succeeded")
	}
	if out != "" {
		t.Errorf("a failed command returned a value: %q", out)
	}
	if !strings.Contains(err.Error(), "Needed a single revision") {
		t.Errorf("the error dropped git's own diagnostic: %v", err)
	}
}

// A refusal that names git's failure must read exactly as it always has:
// "<what we were doing>: <exit error>: <what git wrote>". Moving stderr from the
// caller's own %s into the helper's wrapped error must not add a separator, nor
// leave a trailing one when git said nothing. These go through the exported
// entry points, because that is where the wording is read.
func TestAGitFailureKeepsItsMessageShapeOnThePublicPath(t *testing.T) {
	dir, base, tree1, _ := tempRepo(t)
	ctx := context.Background()
	const absent = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	// A commit that is a true projection of tree1 but has no parent: the tree
	// comparison passes and the parent read is the step that fails.
	parentless := gitOut(t, dir, "commit-tree", tree1, "-m", "no parent")

	for _, tc := range []struct {
		name   string
		prefix string
		said   string
		// A rev that git echoes to stdout when it cannot resolve it. git also
		// quotes it in its own fatal, so it must appear exactly once: twice
		// means the stdout value was appended to the message as well.
		echoed string
		run    func() error
	}{
		{
			name:   "commit-tree",
			prefix: "commit-tree",
			said:   "not a valid object",
			run: func() error {
				_, err := PublishSnapshot(ctx, dir, "origin",
					Subject{TaskID: "T", CandidateTree: absent, BaseSHA: base, CandidateDigest: digestC1}, "r-1")
				return err
			},
		},
		{
			name:   "push",
			prefix: "pushing the review snapshot",
			said:   "does not appear to be a git repository",
			run: func() error {
				_, err := PublishSnapshot(ctx, dir, "no-such-remote",
					Subject{TaskID: "T", CandidateTree: tree1, BaseSHA: base, CandidateDigest: digestC1}, "r-1")
				return err
			},
		},
		{
			name:   "snapshot tree",
			prefix: "reading the snapshot tree",
			said:   "unknown revision",
			echoed: absent + "^{tree}",
			run: func() error {
				return VerifySnapshot(ctx, dir, absent, Subject{TaskID: "T", CandidateTree: tree1, BaseSHA: base})
			},
		},
		{
			name:   "snapshot parent",
			prefix: "reading the snapshot parent",
			said:   "unknown revision",
			echoed: parentless + "^",
			run: func() error {
				return VerifySnapshot(ctx, dir, parentless, Subject{TaskID: "T", CandidateTree: tree1, BaseSHA: base})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("the failing git command was reported as success")
			}
			msg := err.Error()
			// One separator between the exit error and git's own words, and
			// git's words start right there rather than after an empty value.
			shape := regexp.MustCompile(`^` + regexp.QuoteMeta(tc.prefix) + `: exit status \d+: \S`)
			if !shape.MatchString(msg) {
				t.Errorf("message shape changed: %q", msg)
			}
			if strings.Contains(msg, ": : ") {
				t.Errorf("an empty value was appended as a diagnostic: %q", msg)
			}
			if strings.HasSuffix(msg, ": ") || strings.HasSuffix(msg, ":") {
				t.Errorf("message ends in a dangling separator: %q", msg)
			}
			if n := strings.Count(msg, tc.said); n != 1 {
				t.Errorf("git's diagnostic %q appears %d times, want once: %q", tc.said, n, msg)
			}
			if tc.echoed != "" {
				if n := strings.Count(msg, tc.echoed); n != 1 {
					t.Errorf("the unresolvable rev %q appears %d times, want once — the stdout echo was quoted as a diagnostic: %q", tc.echoed, n, msg)
				}
			}
		})
	}
}

// gitOut runs git for test setup and returns its stdout, failing the test on error.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
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
