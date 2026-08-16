package setup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrokenChecksBlockReadiness(t *testing.T) {
	r := Report{Checks: []Check{{Name: "a", State: OK}, {Name: "b", State: Degraded}}}
	if !r.Ready() {
		t.Fatal("a degraded session is still usable and must not block")
	}
	r.Checks = append(r.Checks, Check{Name: "c", State: Broken})
	if r.Ready() {
		t.Fatal("a broken check did not block readiness")
	}
}

func TestUnknownIsNeverReportedAsReady(t *testing.T) {
	// An unanswered check is not a passing one; it must be visible as unknown.
	c := checkWorkspaceTools(Options{ToolNames: nil})
	if c.State != Unknown {
		t.Fatalf("state = %q, want unknown when the server did not answer", c.State)
	}
}

func TestMissingWorkspaceToolsNameTheSymptomTheUserSees(t *testing.T) {
	c := checkWorkspaceTools(Options{ToolNames: []string{"awareness_preflight"}})
	if c.State != Broken {
		t.Fatalf("state = %q, want broken", c.State)
	}
	// The user meets this as an "unknown tool" error, not as a version mismatch.
	if !strings.Contains(c.Symptom, "unknown tool") {
		t.Fatalf("symptom does not match what the user sees: %q", c.Symptom)
	}
	if !strings.Contains(c.Fix, "upgrade it") && !strings.Contains(c.Fix, "build") {
		t.Fatalf("no actionable fix was offered: %q", c.Fix)
	}
}

func TestPresentToolsPass(t *testing.T) {
	c := checkWorkspaceTools(Options{ToolNames: RequiredWorkspaceTools})
	if c.State != OK {
		t.Fatalf("state = %q with every required tool present", c.State)
	}
}

func TestRegisterDomainAddsWithoutRewritingOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "domains.yaml")
	original := "domains:\n  github.com/other/repo:\n    repository_identity: other/repo\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := registerDomain(path, "github.com/globulario/sensei-code"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "github.com/other/repo:") {
		t.Fatal("registering a domain removed somebody else's entry")
	}
	if !strings.Contains(string(body), "repository_identity: globulario/sensei-code") {
		t.Fatalf("the identity was not derived from the domain:\n%s", body)
	}
	// The registry vouches for repositories, so a dirty publish must stay refused.
	if strings.Contains(string(body), "allow_dirty_worktree: true") {
		t.Fatal("registration enabled dirty publishing")
	}
}

func TestRegisterDomainIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "domains.yaml")
	for i := 0; i < 2; i++ {
		if err := registerDomain(path, "github.com/a/b"); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := os.ReadFile(path)
	if strings.Count(string(body), "github.com/a/b:") != 1 {
		t.Fatalf("the domain was registered twice:\n%s", body)
	}
}

func TestMissingCorpusOffersInit(t *testing.T) {
	c := checkCorpus(context.Background(), Options{RepoRoot: t.TempDir()})
	if c.State != Broken || c.Repair == nil {
		t.Fatalf("a repository with no corpus should be broken and repairable: %+v", c)
	}
}

func TestRenderShowsSymptomAndFixForFailures(t *testing.T) {
	out := Report{Checks: []Check{{
		Name: "x", State: Broken, Detail: "d", Symptom: "you see this", Fix: "run that",
	}}}.Render()
	for _, want := range []string{"BROKEN", "what you would see: you see this", "run that"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render is missing %q:\n%s", want, out)
		}
	}
}

func TestDomainDerivedFromEitherRemoteForm(t *testing.T) {
	// Sensei's domain validator requires host/path, so a derived value must
	// never be a bare alias.
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	for url, want := range map[string]string{
		"https://github.com/globulario/sensei-code.git": "github.com/globulario/sensei-code",
		"git@github.com:globulario/sensei-code.git":     "github.com/globulario/sensei-code",
	} {
		exec.Command("git", "-C", dir, "remote", "remove", "origin").Run()
		git("remote", "add", "origin", url)
		if got := domainFromOrigin(context.Background(), dir); got != want {
			t.Fatalf("domainFromOrigin(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestNoRemoteSaysSoRatherThanRepeatingInit(t *testing.T) {
	// "run sensei init" is useless advice to someone who just ran it.
	dir := t.TempDir()
	c := checkDomainRegistered(context.Background(), Options{RepoRoot: dir})
	if c.Repair != nil {
		t.Fatal("a repository with no remote offered a repair that cannot work")
	}
	if !strings.Contains(c.Detail, "no git remote") {
		t.Fatalf("the actual cause was not named: %q", c.Detail)
	}
}
