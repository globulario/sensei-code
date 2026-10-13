//go:build linux

package workflow

// N4 witnesses: confined validation, through the engine's one policy, with
// fixture launchers. No real D2 executor runs here.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/broker"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/validation"
)

// n4Launcher speaks the launcher status protocol; n3Wrapper (today's pinned
// wrapper's shape) does not.
const n4Launcher = "#!/bin/sh\nread nonce <&5\nexec 5<&-\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&-\nrc=$?\necho \"" + validation.StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"

func n4Engine(t *testing.T) (*Engine, string) {
	t.Helper()
	repo, work := t.TempDir(), t.TempDir()
	store, err := session.New(repo, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Repo: gitx.Repo{Root: repo}, Config: config.Default(), Bus: event.NewBus(), Store: store, SessionID: "session-1"}
	e.Config.Validation = config.Validation{
		Vet:   []config.Command{{Command: "sh", Args: []string{"-c", "touch vet-ran"}}},
		Build: []config.Command{{Command: "sh", Args: []string{"-c", "touch build-ran"}}},
		Test:  []config.Command{{Command: "sh", Args: []string{"-c", "touch test-ran"}}},
	}
	return e, work
}

func (e *Engine) n4Validate(t *testing.T, work string) (validation.Bundle, error) {
	t.Helper()
	b, _, err := e.validate(context.Background(), "task-1", "", broker.New(e.Config.Permissions), gitx.Repo{Root: work}, "diff", nil)
	return b, err
}

func anyRan(work string) bool {
	for _, m := range []string{"vet-ran", "build-ran", "test-ran", "format-ran"} {
		if _, err := os.Stat(filepath.Join(work, m)); err == nil {
			return true
		}
	}
	return false
}

// A6. The policy, from configuration alone.
func TestValidationConfinementPolicy(t *testing.T) {
	profile := pinnedWrapper(t, n4Launcher)
	cases := []struct {
		name     string
		set      func(*config.Config)
		confined bool
		refusal  bool
	}{
		{"default off", func(*config.Config) {}, false, false},
		{"N3 profile present but N3 off", func(c *config.Config) { c.ExecBinding.Confinement = profile }, false, false},
		{"validation confined", func(c *config.Config) { c.Validation.Confined, c.Validation.Confinement = true, profile }, true, false},
		{"validation confined, N3 profile", func(c *config.Config) { c.Validation.Confined, c.ExecBinding.Confinement = true, profile }, true, false},
		{"validation confined, no profile", func(c *config.Config) { c.Validation.Confined = true }, false, true},
		{"N3 enabled", func(c *config.Config) { c.ExecBinding.Enabled, c.ExecBinding.Confinement = true, profile }, true, false},
		{"N3 enabled, no profile", func(c *config.Config) { c.ExecBinding.Enabled = true }, false, true},
		// A profile given for validation is the one used, even incomplete:
		// it is refused at the pin, never replaced or skipped.
		{"incomplete validation profile", func(c *config.Config) {
			c.Validation.Confined, c.Validation.Confinement, c.ExecBinding.Confinement = true, config.Confinement{Profile: "p"}, profile
		}, false, true},
	}
	for _, tc := range cases {
		c := config.Default()
		tc.set(&c)
		conf, refusal := validationConfinementOf(c)
		if (conf != nil) != tc.confined || (refusal != "") != tc.refusal || (conf != nil && !conf.RequireStatus) || (conf != nil && refusal != "") {
			t.Errorf("%s: confinement %+v, refusal %q", tc.name, conf, refusal)
		}
	}
}

// Every check of a confined validation runs through the pinned launcher and
// says so; the evidence certifies the bytes it ran against.
func TestConfinedValidationRunsEveryCheckThroughTheLauncher(t *testing.T) {
	e, work := n4Engine(t)
	e.Config.Validation.Confined, e.Config.Validation.Confinement = true, pinnedWrapper(t, n4Launcher)
	b, err := e.n4Validate(t, work)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Checks) != 3 || !b.Passed() || !b.Certifies("task-1", validation.Digest("diff")) {
		t.Fatalf("confined validation did not pass and certify: %+v", b)
	}
	for _, c := range b.Checks {
		if !strings.Contains(c.ExecutedBy, "confinement profile d2-test") || !strings.Contains(c.ExecutedBy, e.Config.Validation.Confinement.SHA256) ||
			!strings.Contains(c.ExecutedBy, validation.StatusProtocol) {
			t.Fatalf("the evidence does not name the profile, verified wrapper and protocol: %+v", c)
		}
	}
	for _, m := range []string{"vet-ran", "build-ran", "test-ran"} {
		if _, err := os.Stat(filepath.Join(work, m)); err != nil {
			t.Fatalf("%s did not run in the candidate workspace", m)
		}
	}
}

// Today's pinned wrapper, a wrong digest, a missing wrapper, or no profile at
// all under mandatory confinement: nothing certifies, and with no runnable
// launcher nothing runs on the host.
func TestConfinedValidationHasNoUnconfinedFallback(t *testing.T) {
	good := pinnedWrapper(t, n4Launcher)
	cases := map[string]func(*config.Config){
		"status-less wrapper": func(c *config.Config) {
			c.Validation.Confined, c.Validation.Confinement = true, pinnedWrapper(t, n3Wrapper)
		},
		"wrong digest": func(c *config.Config) {
			c.Validation.Confined, c.Validation.Confinement = true, good
			c.Validation.Confinement.SHA256 = strings.Repeat("0", 64)
		},
		"missing wrapper": func(c *config.Config) {
			c.Validation.Confined, c.Validation.Confinement = true, good
			c.Validation.Confinement.Command = filepath.Join(t.TempDir(), "gone")
		},
		"N3 enabled, no profile": func(c *config.Config) { c.ExecBinding.Enabled = true },
		"N3 enabled, N3's wrapper": func(c *config.Config) {
			c.ExecBinding.Enabled, c.ExecBinding.Confinement = true, pinnedWrapper(t, n3Wrapper)
		},
	}
	for name, set := range cases {
		e, work := n4Engine(t)
		set(&e.Config)
		b, err := e.n4Validate(t, work)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if b.Passed() || len(b.Checks) != 3 || len(b.CandidateFailures()) != 0 {
			t.Errorf("%s: certified or blamed the candidate: %+v", name, b)
		}
		for _, c := range b.Checks {
			if c.Outcome != validation.Errored || !strings.HasPrefix(c.Detail, validation.SandboxUnavailable) {
				t.Errorf("%s: not a refusal: %+v", name, c)
			}
		}
		// A status-less wrapper does run what it is given; the others must not.
		if name != "status-less wrapper" && name != "N3 enabled, N3's wrapper" && anyRan(work) {
			t.Errorf("%s: a check ran without its launcher", name)
		}
	}
}

// A3 through the engine: the candidate exiting 125 is its own failure.
func TestConfinedValidationCandidateExit125IsItsOwnFailure(t *testing.T) {
	e, work := n4Engine(t)
	e.Config.Validation.Confined, e.Config.Validation.Confinement = true, pinnedWrapper(t, n4Launcher)
	e.Config.Validation.Test = []config.Command{{Command: "sh", Args: []string{"-c", "exit 125"}}}
	b, err := e.n4Validate(t, work)
	if err != nil {
		t.Fatal(err)
	}
	last := b.Checks[len(b.Checks)-1]
	if last.Outcome != validation.Failed || last.ExitStatus != 125 || strings.HasPrefix(last.Detail, validation.SandboxUnavailable) {
		t.Fatalf("exit 125 impersonated a refusal: %+v", last)
	}
}

// A2 (RULING-338 B3). The formatter runs through the same policy as every
// other check, against a real repository, so the capture after it is real.
//
// confinedFormattingRig is postvalidation_test's formatting engine (gofmt -w,
// then gofmt -l certifying) under a confined validation profile, over a
// repository whose main.go gofmt WILL rewrite.
func confinedFormattingRig(t *testing.T, launcher string) (*Engine, gitx.Repo, string) {
	t.Helper()
	repo, base := mintRepo(t)
	if err := os.WriteFile(filepath.Join(repo.Root, "main.go"), []byte(unformatted), 0o644); err != nil {
		t.Fatal(err)
	}
	e := formattingEngine(t, repo)
	e.Config.Validation.Confined, e.Config.Validation.Confinement = true, pinnedWrapper(t, launcher)
	e.beginReceipt("task-1")
	return e, repo, base
}

// confinedValidate captures the candidate, validates it, and returns the
// bundle with the pre- and post-validation diffs. Setup and capture errors
// fail the test: a witness whose load-bearing assertions can be skipped by an
// error is no witness.
func confinedValidate(t *testing.T, e *Engine, repo gitx.Repo, base string) (validation.Bundle, string, string) {
	t.Helper()
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatalf("capture before validation: %v", err)
	}
	b, after, err := e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	return b, before.Diff, after
}

// n4RefusingLauncher refuses every execution whose argv or workspace mode
// matches `when` (a shell pattern), and runs everything else as n4Launcher.
func n4RefusingLauncher(when string) string {
	return "#!/bin/sh\nread nonce <&5\nexec 5<&-\ncase \"$SENSEI_CONFINE_WORKSPACE_MODE $*\" in " + when + ") echo \"" + validation.StatusProtocol + " $nonce refused no D2 executor for this check\" >&4; exit 0;; esac\n" +
		"while [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&-\nrc=$?\necho \"" + validation.StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"
}

// (i) The formatter is refused, the capture after it succeeds, and the
// refusal is present in the certifying bundle, bound to the bytes it blocks,
// without claiming the formatter was attempted on them: the original refusal,
// bound to the pre-format bytes, is preserved inside the record.
func TestARefusedFormatterBlocksTheCaptureItWasRefusedBefore(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4RefusingLauncher("rw*"))
	b, before, after := confinedValidate(t, e, repo, base)
	if after != before || !strings.Contains(after, unformattedMark) {
		t.Fatalf("a refused formatter changed the candidate")
	}
	if len(b.Checks) < 2 || b.Checks[0].Kind != validation.Format || b.Checks[0].Outcome != validation.Errored ||
		!strings.HasPrefix(b.Checks[0].Detail, validation.SandboxUnavailable) {
		t.Fatalf("the formatter's refusal was discarded with its evidence: %+v", b)
	}
	refusal := b.Checks[0]
	if refusal.DiffDigest != b.DiffDigest || b.DiffDigest != validation.Digest(after) || !b.Certifies("task-1", validation.Digest(after)) {
		t.Fatalf("the refusal does not bind the capture it blocks: %+v", refusal)
	}
	if !strings.Contains(refusal.Detail, validation.Digest(before)) || !strings.Contains(refusal.Detail, "not attempted") {
		t.Fatalf("the record rebinds the refused execution instead of preserving it: %+v", refusal)
	}
	if b.Passed() {
		t.Fatalf("a refused formatter left a certifying bundle: %+v", b)
	}
}

// (ii) The formatter genuinely rewrites the candidate, the capture is taken
// again, and the certifying checks bind to what the formatter produced.
func TestAConfinedFormatterRewriteIsRecapturedAndCertified(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4Launcher)
	b, before, after := confinedValidate(t, e, repo, base)
	if after == before || !strings.Contains(after, formattedMark) || strings.Contains(after, unformattedMark) {
		t.Fatalf("the confined formatter did not rewrite the candidate: %q", after)
	}
	if !b.Passed() || b.DiffDigest != validation.Digest(after) || !b.Certifies("task-1", validation.Digest(after)) {
		t.Fatalf("the certifying checks are not bound to the formatted candidate: %+v", b)
	}
	for _, c := range b.Checks {
		if !strings.Contains(c.ExecutedBy, validation.StatusProtocol) {
			t.Fatalf("a certifying check did not run under the launcher: %+v", c)
		}
	}
}

// (iii) One formatter rewrites the bytes and a later one is refused: the
// rewrite is observed (the capture moved and the certifying check passes on
// it) and the refusal blocks that new capture, preserving its own pre-format
// binding rather than taking the new one.
func TestARewriteFollowedByARefusedFormatterKeepsBothObservations(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4RefusingLauncher("*second-formatter*"))
	e.Config.Validation.Format = append(e.Config.Validation.Format, config.Command{Command: "sh", Args: []string{"-c", ": second-formatter"}})
	b, before, after := confinedValidate(t, e, repo, base)
	if after == before || !strings.Contains(after, formattedMark) {
		t.Fatalf("the first formatter's rewrite was lost: %q", after)
	}
	if len(b.Checks) != 2 || b.Checks[0].Kind != validation.Format || b.Checks[0].Outcome != validation.Errored ||
		b.Checks[1].Outcome != validation.Passed {
		t.Fatalf("expected the refusal then the passing verifier: %+v", b)
	}
	refusal := b.Checks[0]
	if refusal.DiffDigest != validation.Digest(after) || !strings.Contains(refusal.Detail, validation.Digest(before)) ||
		!strings.Contains(refusal.Detail, "not attempted") || !strings.Contains(refusal.Args[len(refusal.Args)-1], "second-formatter") {
		t.Fatalf("the refusal lost its original binding or names the wrong formatter: %+v", refusal)
	}
	if b.Passed() || !b.Certifies("task-1", validation.Digest(after)) {
		t.Fatalf("the refusal does not block the rewritten capture: %+v", b)
	}
}

// A6. Default-off parity: no confinement selected, the unconfined broker,
// claiming nothing about confinement.
func TestDefaultOffValidationIsUnchanged(t *testing.T) {
	e, work := n4Engine(t)
	b, err := e.n4Validate(t, work)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Passed() || len(b.Checks) != 3 {
		t.Fatalf("default-off validation changed: %+v", b)
	}
	for _, c := range b.Checks {
		if c.ExecutedBy != "sensei-code execution broker" {
			t.Fatalf("default-off evidence claims an executor it did not use: %+v", c)
		}
	}
}

// A5 census. Every validation.Runner in this package is built in exactly the
// places that apply a confinement policy: the engine's one validation policy
// and N3's always-confined tool-time check. A new construction anywhere else
// -- a retry, a resume, a shortcut -- is an unconfined path, and fails here.
// The census supplements the policy; it does not replace it. Its scope is
// exactly this: the non-test files of this package, and constructions written
// through the import identifier "validation". An aliased import or a Runner
// built in another package is outside what it sees; the runtime policy
// (validationRunner) is the enforcement, this is the tripwire beside it.
func TestEveryValidationRunnerIsBuiltUnderThePolicy(t *testing.T) {
	allowed := map[string]bool{"engine.go:validationRunner": true, "execservice.go:runCheck": true}
	confinement := map[string]bool{"engine.go:validationConfinementOf": true, "execservice.go:runCheck": true}
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var found, stray []string
	calls := map[string]bool{}
	isValidation := func(x ast.Expr, name string) bool {
		s, ok := x.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := s.X.(*ast.Ident)
		return ok && id.Name == "validation" && s.Sel.Name == name
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			where := path + ":" + fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CompositeLit:
					if isValidation(n.Type, "Runner") {
						found = append(found, where)
						if !allowed[where] {
							stray = append(stray, where+" builds a validation.Runner")
						}
					}
					if isValidation(n.Type, "Confinement") && !confinement[where] {
						stray = append(stray, where+" builds a validation.Confinement")
					}
				case *ast.ValueSpec:
					if n.Type != nil && isValidation(n.Type, "Runner") {
						stray = append(stray, where+" declares a validation.Runner")
					}
				case *ast.CallExpr:
					if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "new" && len(n.Args) == 1 && isValidation(n.Args[0], "Runner") {
						stray = append(stray, where+" allocates a validation.Runner")
					}
					if s, ok := n.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "validationRunner" {
						calls[where] = true
					}
				case *ast.AssignStmt:
					for _, l := range n.Lhs {
						if s, ok := l.(*ast.SelectorExpr); ok && (s.Sel.Name == "Confine" || s.Sel.Name == "Refusal" || s.Sel.Name == "RequireStatus") &&
							where != "engine.go:validationRunner" {
							stray = append(stray, where+" sets a runner's "+s.Sel.Name)
						}
					}
				}
				return true
			})
		}
	}
	sort.Strings(stray)
	if len(stray) != 0 {
		t.Fatalf("validation runners outside the confinement policy:\n%s", strings.Join(stray, "\n"))
	}
	sort.Strings(found)
	if strings.Join(found, ",") != "engine.go:validationRunner,execservice.go:runCheck" {
		t.Fatalf("the runner census changed: %v", found)
	}
	for _, caller := range []string{"engine.go:validate", "engine.go:requiredTestEvidence"} {
		if !calls[caller] {
			t.Errorf("%s does not take its runner from the policy", caller)
		}
	}
}
