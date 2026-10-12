package gitx

// Host-safety witnesses for the trusted git profile (GITX objective,
// RULING-313/316). Every case drives the real gitx functions against a
// disposable repository and plants a program that leaves a marker if git runs
// it. A negative passes only when the marker is absent; each attack has a
// control showing that plain git, given the same repository, does run it --
// so an absent marker means the profile stopped it, not that the attack was
// inert.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hsRepo is a committed repository at <tmp>/repo with one tracked file.
func hsRepo(t *testing.T) (Repo, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	hsGit(t, root, "init", "-q", "-b", "main")
	hsGit(t, root, "config", "user.email", "t@example.invalid")
	hsGit(t, root, "config", "user.name", "T")
	hsGit(t, root, "config", "commit.gpgsign", "false")
	hsWrite(t, filepath.Join(root, "a.txt"), "one\n")
	hsGit(t, root, "add", "a.txt")
	hsGit(t, root, "commit", "-q", "-m", "base")
	return Repo{Root: root}, hsGit(t, root, "rev-parse", "HEAD")
}

// hsGit is plain, unrestricted git: the test's own tool and the controls.
// Its hooks are pointed at nothing so setup never fires a planted program.
func hsGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := hsPlain(dir, append([]string{"-c", "core.hooksPath=" + os.DevNull}, args...)...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func hsPlain(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func hsWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// hsProgram writes an executable that touches marker, and returns its path.
func hsProgram(t *testing.T, marker string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "evil.sh")
	hsWrite(t, p, "#!/bin/sh\ntouch '"+marker+"'\ncat\n")
	return p
}

func hsRan(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

func hsRefused(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), ProfileRefused) {
		t.Fatalf("%s: want a %s refusal, got %v", what, ProfileRefused, err)
	}
}

func hsAppendConfig(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

// Hooks git would run for worktree creation, index changes and ref updates
// are never run: the profile points core.hooksPath at an empty directory.
func TestHostGitRunsNoRepositoryHook(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	evil := hsProgram(t, marker)
	for _, h := range []string{"post-checkout", "post-index-change", "reference-transaction"} {
		hsWrite(t, filepath.Join(r.Root, ".git", "hooks", h), "#!/bin/sh\nexec '"+evil+"'\n")
	}
	wt, err := r.CreateWorktreeAt(ctx, "hooks", base)
	if err != nil {
		t.Fatal(err)
	}
	hsWrite(t, filepath.Join(wt, "b.txt"), "new\n")
	cap, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := r.MintCanonicalCommit(ctx, base, cap.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.PointBranchAt(ctx, r.WorktreeBranch("hooks"), commit); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteBranch(ctx, r.WorktreeBranch("hooks")); err != nil {
		t.Fatal(err)
	}
	if hsRan(marker) {
		t.Fatal("a repository hook ran during trusted git operations")
	}
	// Control: the same hooks fire under plain git.
	if out, err := hsPlain(r.Root, "worktree", "add", "-q", "--detach", filepath.Join(t.TempDir(), "control"), base); err != nil {
		t.Fatalf("control: %v\n%s", err, out)
	}
	if !hsRan(marker) {
		t.Fatal("premise: the planted hook does not run under plain git, so its absence proves nothing")
	}
}

// Executable configuration is refused BEFORE git runs, by name, for every
// gitx entry point; plain git given the same repository runs the program.
func TestExecutableRepositoryConfigurationIsRefusedBeforeGitRuns(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		config     func(evil string) string
		attributes string
		control    []string // plain git command that runs the program
	}{
		"fsmonitor": {
			config:  func(e string) string { return "[core]\n\tfsmonitor = " + e + "\n" },
			control: []string{"status", "--porcelain"},
		},
		"clean filter": {
			config:     func(e string) string { return "[filter \"evil\"]\n\tclean = " + e + "\n" },
			attributes: "*.txt filter=evil\n",
			control:    []string{"diff", "--no-ext-diff"},
		},
		"process filter": {
			config:     func(e string) string { return "[filter \"evil\"]\n\tprocess = " + e + "\n\trequired = true\n" },
			attributes: "*.txt filter=evil\n",
			control:    []string{"diff", "--no-ext-diff"},
		},
		"textconv": {
			config:     func(e string) string { return "[diff \"evil\"]\n\ttextconv = " + e + "\n" },
			attributes: "*.txt diff=evil\n",
			control:    []string{"diff", "HEAD"},
		},
		"external diff": {
			config:  func(e string) string { return "[diff]\n\texternal = " + e + "\n" },
			control: []string{"diff", "HEAD"},
		},
		"include": {
			config: func(e string) string {
				inc := filepath.Join(filepath.Dir(e), "included.conf")
				os.WriteFile(inc, []byte("[core]\n\tfsmonitor = "+e+"\n"), 0o644)
				return "[include]\n\tpath = " + inc + "\n"
			},
			control: []string{"status", "--porcelain"},
		},
		"includeIf": {
			config: func(e string) string {
				inc := filepath.Join(filepath.Dir(e), "included.conf")
				os.WriteFile(inc, []byte("[core]\n\tfsmonitor = "+e+"\n"), 0o644)
				return "[includeIf \"gitdir:/\"]\n\tpath = " + inc + "\n"
			},
			control: []string{"status", "--porcelain"},
		},
		// The reviewer's B1 config: git closes the quote on the next line, so
		// ";" starts a comment and there is no continuation; a reader that kept
		// the quote open would skip the filter line git then applies.
		"key behind a quoted continuation": {
			config: func(e string) string {
				return "[remote \"origin\"]\n\turl = \"x \\\n\"; \\\n[filter \"evil\"] clean = " + e + "\n"
			},
			attributes: "*.txt filter=evil\n",
			control:    []string{"diff", "--no-ext-diff"},
		},
		"key after a comment ending in a backslash": {
			config:  func(e string) string { return "[core]\n\tbare = false ; note \\\n\tfsmonitor = " + e + "\n" },
			control: []string{"status", "--porcelain"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, base := hsRepo(t)
			wt, err := r.CreateWorktreeAt(ctx, "cfg", base)
			if err != nil {
				t.Fatal(err)
			}
			hsWrite(t, filepath.Join(wt, "a.txt"), "two\n")
			hsWrite(t, filepath.Join(r.Root, "a.txt"), "two\n")
			if c.attributes != "" {
				hsWrite(t, filepath.Join(wt, ".gitattributes"), c.attributes)
				hsWrite(t, filepath.Join(r.Root, ".gitattributes"), c.attributes)
			}
			marker := filepath.Join(t.TempDir(), "ran")
			hsAppendConfig(t, filepath.Join(r.Root, ".git", "config"), c.config(hsProgram(t, marker)))

			cand := Repo{Root: wt}
			_, err = cand.CandidateCapture(ctx, base, nil)
			hsRefused(t, "CandidateCapture", err)
			_, _, err = r.WorktreeIsClean(ctx, wt)
			hsRefused(t, "WorktreeIsClean", err)
			_, err = r.IsClean(ctx)
			hsRefused(t, "IsClean", err)
			_, err = r.Head(ctx)
			hsRefused(t, "Head", err)
			_, err = r.CreateObservationWorktree(ctx, "cfg", base)
			hsRefused(t, "CreateObservationWorktree", err)
			_, err = Discover(ctx, r.Root)
			hsRefused(t, "Discover", err)
			if hsRan(marker) {
				t.Fatal("the repository's program ran during a refused operation")
			}
			if out, _ := hsPlain(r.Root, c.control...); !hsRan(marker) {
				t.Fatalf("premise: plain git %v did not run the program, so the refusal proves nothing\n%s", c.control, out)
			}
		})
	}
}

// The caller's environment and global configuration cannot reach git: hostile
// GIT_* variables and a HOME whose .gitconfig names a program are ignored, and
// the capture is the ordinary one.
func TestTheCallersEnvironmentDoesNotReachGit(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "env", base)
	if err != nil {
		t.Fatal(err)
	}
	hsWrite(t, filepath.Join(wt, "a.txt"), "two\n")
	// diff=evil: a textconv driver named by the hostile environment. (A
	// filter attribute is refused outright now -- RULING-319 -- and has its
	// own witnesses.)
	hsWrite(t, filepath.Join(wt, ".gitattributes"), "*.txt diff=evil\n")
	marker := filepath.Join(t.TempDir(), "ran")
	evil := hsProgram(t, marker)
	home := t.TempDir()
	hsWrite(t, filepath.Join(home, ".gitconfig"), "[core]\n\tfsmonitor = "+evil+"\n[filter \"evil\"]\n\tclean = "+evil+"\n")
	hsWrite(t, filepath.Join(home, "xdg", "git", "config"), "[core]\n\tfsmonitor = "+evil+"\n")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_PARAMETERS", "'diff.evil.textconv'='"+evil+"'")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", evil)
	t.Setenv("GIT_DIR", filepath.Join(home, "elsewhere"))
	t.Setenv("GIT_EXTERNAL_DIFF", evil)
	t.Setenv("GIT_EXEC_PATH", home)

	cap, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if hsRan(marker) {
		t.Fatal("a program from the caller's environment or global configuration ran")
	}
	if !strings.Contains(cap.Diff, "+two") || len(cap.Paths) != 2 {
		t.Fatalf("the capture is not the ordinary one: paths %q\n%s", cap.Paths, cap.Diff)
	}
	// Control: plain git with the same HOME (GIT_DIR and GIT_EXEC_PATH dropped,
	// or it would fail before reading any configuration) runs the program.
	control := exec.Command("git", "-C", wt, "status", "--porcelain")
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_DIR=") && !strings.HasPrefix(e, "GIT_EXEC_PATH=") {
			control.Env = append(control.Env, e)
		}
	}
	if out, _ := control.CombinedOutput(); !hsRan(marker) {
		t.Fatalf("premise: plain git with this environment does not run the program\n%s", out)
	}
}

// A candidate cannot redirect trusted git by rewriting its worktree's .git:
// neither to a git directory of its own nor by replacing the pointer with a
// repository.
func TestACandidateCannotRedirectTrustedGit(t *testing.T) {
	ctx := context.Background()
	// Each case's control: plain git in the planted worktree is redirected --
	// it reads a git directory other than the one gitx created, or runs the
	// planted program.
	for name, plant := range map[string]func(t *testing.T, wt, evil string){
		"pointer to an attacker git directory": func(t *testing.T, wt, evil string) {
			attacker := filepath.Join(t.TempDir(), "attacker")
			hsGit(t, filepath.Dir(attacker), "init", "-q", attacker)
			hsAppendConfig(t, filepath.Join(attacker, ".git", "config"), "[core]\n\tfsmonitor = "+evil+"\n")
			hsWrite(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(attacker, ".git")+"\n")
		},
		"a repository in place of the pointer": func(t *testing.T, wt, evil string) {
			if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
				t.Fatal(err)
			}
			hsGit(t, wt, "init", "-q")
			hsAppendConfig(t, filepath.Join(wt, ".git", "config"), "[core]\n\tfsmonitor = "+evil+"\n")
		},
		// Identity alone, with no program anywhere: a clean repository in place
		// of the pointer would let git capture content the candidate chose.
		"a clean repository in place of the pointer": func(t *testing.T, wt, evil string) {
			if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
				t.Fatal(err)
			}
			hsGit(t, wt, "init", "-q")
		},
		// A fabricated repository whose worktree records point back at this
		// worktree: every link is self-consistent, and only the anchor in the
		// owner repository tells it apart.
		"a fabricated repository that points back": func(t *testing.T, wt, evil string) {
			fake := filepath.Join(t.TempDir(), "fake")
			hsGit(t, filepath.Dir(fake), "init", "-q", fake)
			g := filepath.Join(fake, ".git", "worktrees", "n")
			hsWrite(t, filepath.Join(g, "gitdir"), filepath.Join(wt, ".git")+"\n")
			hsWrite(t, filepath.Join(g, "commondir"), "../..\n")
			hsWrite(t, filepath.Join(g, "HEAD"), "ref: refs/heads/main\n")
			hsWrite(t, filepath.Join(wt, ".git"), "gitdir: "+g+"\n")
		},
		"pointer to another worktree of the same repository": func(t *testing.T, wt, evil string) {
			hsWrite(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(filepath.Dir(filepath.Dir(wt)), "repo", ".git", "worktrees", "other")+"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, base := hsRepo(t)
			if _, err := r.CreateWorktreeAt(ctx, "other", base); err != nil {
				t.Fatal(err)
			}
			wt, err := r.CreateWorktreeAt(ctx, "redirect", base)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "ran")
			plant(t, wt, hsProgram(t, marker))
			_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
			hsRefused(t, "CandidateCapture", err)
			_, _, err = r.WorktreeIsClean(ctx, wt)
			hsRefused(t, "WorktreeIsClean", err)
			if hsRan(marker) {
				t.Fatal("the candidate's program ran through a redirected worktree")
			}
			gitDir, _ := hsPlain(wt, "rev-parse", "--absolute-git-dir")
			gitDir = strings.TrimSpace(gitDir)
			want := filepath.Join(r.Root, ".git", "worktrees", "redirect")
			if real, err := filepath.EvalSymlinks(want); err == nil {
				want = real
			}
			if real, err := filepath.EvalSymlinks(gitDir); err == nil {
				gitDir = real
			}
			if gitDir == want {
				t.Fatalf("premise: plain git in the planted worktree still reads %s, so nothing was redirected", gitDir)
			}
		})
	}
}

// With extensions.worktreeConfig, a worktree's own config.worktree is
// configuration too, and is held to the same allowlist.
func TestAWorktreeConfigurationIsInspectedToo(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "wtcfg", base)
	if err != nil {
		t.Fatal(err)
	}
	hsGit(t, r.Root, "config", "core.repositoryformatversion", "1")
	hsGit(t, r.Root, "config", "extensions.worktreeConfig", "true")
	marker := filepath.Join(t.TempDir(), "ran")
	gitDir, err := readGitdirPointer(wt)
	if err != nil {
		t.Fatal(err)
	}
	hsAppendConfig(t, filepath.Join(gitDir, "config.worktree"), "[core]\n\tfsmonitor = "+hsProgram(t, marker)+"\n")
	_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	hsRefused(t, "CandidateCapture", err)
	if hsRan(marker) {
		t.Fatal("a program named in config.worktree ran")
	}
	if out, _ := hsPlain(wt, "status", "--porcelain"); !hsRan(marker) {
		t.Fatalf("premise: plain git does not run the program in config.worktree\n%s", out)
	}
}

// No trusted executable, an extra variable outside the profile, or a polluted
// hooks directory is a refusal -- never unrestricted git.
func TestAnUnestablishedProfileNeverFallsBack(t *testing.T) {
	ctx := context.Background()
	r, _ := hsRepo(t)

	profileMu.Lock()
	saved, savedProgram := profile, gitProgram
	profile, gitProgram = nil, "sensei-code-no-such-git"
	profileMu.Unlock()
	_, err := r.Head(ctx)
	profileMu.Lock()
	profile, gitProgram = saved, savedProgram
	profileMu.Unlock()
	hsRefused(t, "missing executable", err)

	_, err = r.envOutput(ctx, []string{"GIT_EXTERNAL_DIFF=/bin/true"}, "rev-parse", "HEAD")
	hsRefused(t, "extra environment", err)

	p, err := trustedProfile()
	if err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(p.hooks, "post-checkout")
	hsWrite(t, stray, "#!/bin/sh\n")
	_, err = r.Head(ctx)
	os.Remove(stray)
	hsRefused(t, "polluted hooks directory", err)
	if _, err := r.Head(ctx); err != nil {
		t.Fatalf("the profile did not recover once the hooks directory was empty again: %v", err)
	}
}

// On a safe repository the profile changes nothing a reviewer sees: the
// capture's tree and diff are exactly what unrestricted git produces, built-in
// attribute conversions included.
func TestASafeRepositoryCapturesTheSameIdentity(t *testing.T) {
	ctx := context.Background()
	r, _ := hsRepo(t)
	hsWrite(t, filepath.Join(r.Root, ".gitattributes"), "*.txt text eol=lf\n")
	hsGit(t, r.Root, "add", ".gitattributes")
	hsGit(t, r.Root, "commit", "-q", "-m", "attributes")
	base := hsGit(t, r.Root, "rev-parse", "HEAD")
	wt, err := r.CreateWorktreeAt(ctx, "same", base)
	if err != nil {
		t.Fatal(err)
	}
	hsWrite(t, filepath.Join(wt, "a.txt"), "one\r\ntwo\r\n")
	hsWrite(t, filepath.Join(wt, "dir", "new.txt"), "new\n")
	cap, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The same tree, built by unrestricted git from the same paths.
	index := filepath.Join(t.TempDir(), "index")
	plain := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", wt, "-c", "core.hooksPath=" + os.DevNull}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index, "GIT_LITERAL_PATHSPECS=1")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return string(out)
	}
	plain("read-tree", base)
	plain(append([]string{"add", "--all", "--"}, cap.Paths...)...)
	if want := strings.TrimSpace(plain("write-tree")); cap.Tree != want {
		t.Fatalf("the capture's tree %s is not unrestricted git's %s", cap.Tree, want)
	}
	if want, err := hsPlain(wt, "diff", "--no-ext-diff", base, cap.Tree, "--"); err != nil || cap.Diff != want {
		t.Fatalf("the capture's diff differs from unrestricted git's:\n%s\n---\n%s", cap.Diff, want)
	}
	if !strings.Contains(cap.Diff, "+two") || strings.Contains(cap.Diff, "\r") {
		t.Fatalf("the eol conversion did not apply:\n%q", cap.Diff)
	}
	// The ordinary lifecycle still works end to end.
	obs, err := r.CreateObservationWorktree(ctx, "same", base)
	if err != nil {
		t.Fatal(err)
	}
	if clean, _, err := r.WorktreeIsClean(ctx, obs); err != nil || !clean {
		t.Fatalf("a fresh observation worktree is not clean: %v %v", clean, err)
	}
	if err := r.RemoveObservationWorktree(ctx, obs); err != nil {
		t.Fatal(err)
	}
	commit, err := r.MintCanonicalCommit(ctx, base, cap.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.VerifyCanonicalCommit(ctx, base, cap.Tree, commit); err != nil {
		t.Fatal(err)
	}
}

// Every git this package runs goes through gitCommand: the only process
// launches in the package's production files are gitCommand's and the
// profile's own version probe.
func TestEveryGitInvocationUsesTheTrustedProfile(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"gitCommand": true, "trustedProfile": true, "locateOwnerFiles": true}
	fset := token.NewFileSet()
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == `"os/exec"` && imp.Name != nil {
				t.Errorf("%s imports os/exec as %s, which this census would not see", f, imp.Name.Name)
			}
			if imp.Path.Value == `"syscall"` || imp.Path.Value == `"golang.org/x/sys/unix"` {
				t.Errorf("%s imports %s, through which a process could be started unseen", f, imp.Path.Value)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "StartProcess" {
				t.Errorf("%s: os.StartProcess outside the trusted profile", fset.Position(sel.Pos()))
			}
			return true
		})
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "exec" && strings.HasPrefix(sel.Sel.Name, "Command") {
					seen++
					if !allowed[fn.Name.Name] {
						t.Errorf("%s: %s calls exec.%s outside the trusted profile", fset.Position(sel.Pos()), fn.Name.Name, sel.Sel.Name)
					}
				}
				return true
			})
		}
	}
	if seen == 0 {
		t.Fatal("premise: the census found no process launch at all, so it inspected nothing")
	}
}

// The configuration reader sees exactly the keys git sees, so no key can hide
// from the allowlist behind a continuation, a comment, a quoted value or a
// header written on the same line as a key. Git reads the same files as data
// (config --list runs nothing) and is the oracle.
func TestTheConfigReaderSeesEveryKeyGitSees(t *testing.T) {
	fixtures := map[string]string{
		"escaped backslash ends the value": "[remote \"origin\"]\n\turl = https://example.invalid/r\\\\\n\treceivepack = evil\n",
		"backslash in a comment":           "[core]\n\tbare = false ; note \\\n\tfsmonitor = evil\n",
		"quoted semicolon and hash":        "[remote \"origin\"]\n\turl = \"a;b#c\"\n\tproxy = evil\n",
		"key on the header line":           "[core] fsmonitor = evil\n[filter \"x\"] clean = evil\n",
		"deprecated subsection form":       "[Diff.Evil]\n\tTextConv = evil\n",
		"boolean key without a value":      "[core]\n\tbare\n\tfsmonitor\n",
		"mixed case subsection":            "[Branch \"Feature/X\"]\n\tRemote = origin\n[INCLUDE]\n\tPath = /x\n",
	}
	// A value continued onto the next line is refused whatever git makes of
	// it, so no key can hide behind one (independent review, B1).
	for name, body := range map[string]string{
		"continued value":             "[remote \"origin\"]\n\turl = https://example.invalid/\\\nr\n\tuploadpack = evil\n",
		"quoted continuation":         "[remote \"origin\"]\n\turl = \"x \\\n\"; \\\n[filter \"evil\"] clean = evil\n",
		"continuation after a header": "[core] bare = false \\\n\tfsmonitor = evil\n",
	} {
		p := filepath.Join(t.TempDir(), "config")
		hsWrite(t, p, body)
		if _, err := configKeys(p, false); err == nil || !strings.Contains(err.Error(), "continues a value") {
			t.Errorf("%s: a continued value was not refused: %v", name, err)
		}
	}
	for name, body := range fixtures {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config")
			hsWrite(t, p, body)
			got, err := configKeys(p, false)
			if err != nil {
				t.Fatal(err)
			}
			oracle := exec.Command("git", "config", "--file", p, "--list", "--name-only")
			oracle.Dir = filepath.Dir(p) // outside any repository
			out, err := oracle.Output()
			if err != nil {
				t.Fatalf("git cannot read the fixture, so it is not a configuration git would act on: %v", err)
			}
			want := map[string]bool{}
			for _, k := range strings.Fields(string(out)) {
				want[k] = true
			}
			for k := range want {
				if _, ok := got[k]; !ok {
					t.Errorf("git sees %s; the reader does not", k)
				}
			}
			for k := range got {
				if !want[k] {
					t.Errorf("the reader invents %s, which git does not see", k)
				}
			}
		})
	}
}

// Signing settings are admitted only as false: false in any of git's spellings
// passes, and true -- including a bare key, which git reads as true -- is
// refused before git runs (RULING-317 Q3).
func TestSigningSettingsAreAdmittedOnlyAsFalse(t *testing.T) {
	ctx := context.Background()
	for value, ok := range map[string]bool{
		"\tgpgsign = false\n": true, "\tgpgsign = No\n": true, "\tgpgsign = off ; disabled\n": true,
		"\tgpgsign = 0\n": true, "\tgpgsign =\n": true, "\tgpgsign = \"false\"\n": true,
		"\tgpgsign = true\n": false, "\tgpgsign\n": false, "\tgpgsign = yes\n": false,
		"\tgpgsign = 1\n": false, "\tgpgsign = maybe\n": false, "\tgpgsign = false\n\tgpgsign = true\n": false,
	} {
		for _, section := range []string{"commit", "tag"} {
			r, _ := hsRepo(t)
			cfg := filepath.Join(r.Root, ".git", "config")
			// hsRepo set commit.gpgsign=false; replace it with the case under test.
			body, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			cleaned := strings.Replace(string(body), "[commit]\n\tgpgsign = false\n", "", 1)
			if err := os.WriteFile(cfg, []byte(cleaned+"["+section+"]\n"+value), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err = r.Head(ctx)
			if ok && err != nil {
				t.Errorf("%s %q: refused: %v", section, value, err)
			}
			if !ok {
				hsRefused(t, section+" "+value, err)
			}
		}
	}
}

// A nested repository in the candidate is refused, and trusted git never runs
// git inside it: status and diff of a gitlink would run the nested
// repository's own configured program (independent review, B2).
func TestANestedRepositoryNeverRunsItsProgram(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "nested", base)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	evil := hsProgram(t, marker)
	nested := filepath.Join(wt, "nested")
	hsWrite(t, filepath.Join(nested, "f.txt"), "x\n")
	hsWrite(t, filepath.Join(nested, ".gitattributes"), "* filter=evil\n")
	hsGit(t, nested, "init", "-q")
	hsGit(t, nested, "add", ".")
	hsGit(t, nested, "-c", "user.name=T", "-c", "user.email=t@t", "commit", "-q", "-m", "n")
	hsAppendConfig(t, filepath.Join(nested, ".git", "config"), "[filter \"evil\"]\n\tclean = "+evil+"\n")
	// The worker records the nested repository as a gitlink and commits it,
	// as local_commit permits. (An intent-to-add gitlink alone is not looked
	// into; a committed one is -- measured on git 2.43.)
	hsGit(t, wt, "add", "nested")
	hsGit(t, wt, "commit", "-q", "-m", "gitlink")
	// Stat-dirty with the same content, so git must re-read it through the
	// filter to decide.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(nested, "f.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	// Setup ran plain git, which may itself have looked inside; only what
	// trusted git does from here on is under test.
	os.Remove(marker)

	_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	hsRefused(t, "CandidateCapture of a nested repository", err)
	if _, _, err := r.WorktreeIsClean(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WorktreeIsCleanDetail(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if hsRan(marker) {
		t.Fatal("the nested repository's program ran during trusted git operations")
	}
	// Control: plain git looks inside the committed gitlink and runs it.
	if out, _ := hsPlain(wt, "status", "--porcelain"); !hsRan(marker) {
		t.Fatalf("premise: plain git does not run the nested repository's program\n%s", out)
	}
}

// Replace refs do not substitute the base: a worker that wrote refs/replace in
// the shared repository cannot make the capture build on another commit
// (independent review, N2).
func TestAReplaceRefDoesNotSubstituteTheBase(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	realTree := hsGit(t, r.Root, "rev-parse", base+"^{tree}")
	hsWrite(t, filepath.Join(r.Root, "a.txt"), "forged\n")
	hsGit(t, r.Root, "commit", "-q", "-am", "forged")
	forged := hsGit(t, r.Root, "rev-parse", "HEAD")
	hsGit(t, r.Root, "replace", base, forged)
	wt, err := r.CreateWorktreeAt(ctx, "replace", base)
	if err != nil {
		t.Fatal(err)
	}
	hsWrite(t, filepath.Join(wt, "b.txt"), "b\n")
	cap, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cap.BaseTree != realTree {
		t.Fatalf("the capture built on %s, not the base's own tree %s", cap.BaseTree, realTree)
	}
	if strings.Contains(cap.Diff, "forged") {
		t.Fatalf("the replaced base leaked into the diff:\n%s", cap.Diff)
	}
	if got := hsGit(t, r.Root, "rev-parse", base+"^{tree}"); got == realTree {
		t.Fatal("premise: plain git does not honour the replace ref")
	}
}

// A commondir file inside a .git directory would make git read another
// repository's configuration; it is refused (independent review, S2).
func TestACommondirInAGitDirectoryIsRefused(t *testing.T) {
	ctx := context.Background()
	r, _ := hsRepo(t)
	other, _ := hsRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	hsAppendConfig(t, filepath.Join(other.Root, ".git", "config"), "[core]\n\tfsmonitor = "+hsProgram(t, marker)+"\n")
	hsWrite(t, filepath.Join(r.Root, ".git", "commondir"), filepath.Join(other.Root, ".git")+"\n")
	_, err := r.IsClean(ctx)
	hsRefused(t, "IsClean", err)
	if hsRan(marker) {
		t.Fatal("the other repository's program ran")
	}
	if out, _ := hsPlain(r.Root, "status", "--porcelain"); !hsRan(marker) {
		t.Fatalf("premise: plain git does not follow the commondir file\n%s", out)
	}
}

// A git directory owned by another user is refused (independent review, S3).
// Unprivileged tests cannot create one, so the witness uses "/", which no
// unprivileged user owns.
func TestAGitDirectoryOwnedByAnotherUserIsRefused(t *testing.T) {
	if os.Getuid() <= 0 {
		t.Skip("running as root or on a platform without uids: no directory here is owned by another user")
	}
	hsRefused(t, "ownedByUs(/)", ownedByUs("/"))
	if err := ownedByUs(t.TempDir()); err != nil {
		t.Fatalf("this user's own directory was refused: %v", err)
	}
}

// The base's own gitlink is no exception: a repository the candidate plants
// at that path would be looked inside by `add` itself, so it is refused before
// add runs.
func TestARepositoryPlantedAtTheBasesGitlinkIsRefused(t *testing.T) {
	ctx := context.Background()
	r, _ := hsRepo(t)
	sub := filepath.Join(t.TempDir(), "sub")
	hsWrite(t, filepath.Join(sub, "f.txt"), "x\n")
	hsWrite(t, filepath.Join(sub, ".gitattributes"), "* filter=evil\n")
	hsGit(t, sub, "init", "-q")
	hsGit(t, sub, "add", ".")
	hsGit(t, sub, "-c", "user.name=T", "-c", "user.email=t@t", "commit", "-q", "-m", "s")
	hsGit(t, r.Root, "update-index", "--add", "--cacheinfo", "160000,"+hsGit(t, sub, "rev-parse", "HEAD")+",mod")
	hsGit(t, r.Root, "commit", "-q", "-m", "submodule")
	base := hsGit(t, r.Root, "rev-parse", "HEAD")
	wt, err := r.CreateWorktreeAt(ctx, "basegitlink", base)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	// The submodule's own commit, checked out where the base records it, with
	// a filter configured: exactly what git checks for dirt.
	planted := filepath.Join(wt, "mod")
	if err := os.Remove(planted); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	hsGit(t, wt, "clone", "-q", sub, "mod")
	hsAppendConfig(t, filepath.Join(planted, ".git", "config"), "[filter \"evil\"]\n\tclean = "+hsProgram(t, marker)+"\n")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(planted, "f.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	os.Remove(marker)
	_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	hsRefused(t, "CandidateCapture with a repository at the base's gitlink", err)
	if hsRan(marker) {
		t.Fatal("the planted repository's program ran during trusted git operations")
	}
	if out, _ := hsPlain(wt, "add", "--intent-to-add", "--", "."); !hsRan(marker) {
		t.Fatalf("premise: plain git add does not look inside the planted repository\n%s", out)
	}
}

// A gitlink the candidate records with no repository behind it is still a
// change, and status/diff with --ignore-submodules=all would silently drop it
// from the captured paths: it is refused, never omitted from review.
func TestANewGitlinkIsRefusedNotOmitted(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "gitlink", base)
	if err != nil {
		t.Fatal(err)
	}
	hsGit(t, wt, "update-index", "--add", "--cacheinfo", "160000,"+base+",linked")
	hsGit(t, wt, "commit", "-q", "-m", "gitlink")
	_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	hsRefused(t, "CandidateCapture of a new gitlink", err)
	// Premise: the change is real, and ignoring submodules hides it.
	all, _ := hsPlain(wt, "diff", "--name-only", base, "HEAD", "--")
	ignored, _ := hsPlain(wt, "diff", "--ignore-submodules=all", "--name-only", base, "HEAD", "--")
	if !strings.Contains(all, "linked") || strings.Contains(ignored, "linked") {
		t.Fatalf("premise: the gitlink is not a change that --ignore-submodules=all hides: %q / %q", all, ignored)
	}
}

// hsBaseWithGitlink is a repository whose base records a gitlink at "mod" for
// a submodule repository carrying a filter attribute, and a candidate
// worktree cut from that base.
func hsBaseWithGitlink(t *testing.T, task string) (Repo, string, string, string) {
	t.Helper()
	r, _ := hsRepo(t)
	sub := filepath.Join(t.TempDir(), "sub")
	hsWrite(t, filepath.Join(sub, "f.txt"), "x\n")
	hsWrite(t, filepath.Join(sub, ".gitattributes"), "* filter=evil\n")
	hsGit(t, sub, "init", "-q")
	hsGit(t, sub, "add", ".")
	hsGit(t, sub, "-c", "user.name=T", "-c", "user.email=t@t", "commit", "-q", "-m", "s")
	hsGit(t, r.Root, "update-index", "--add", "--cacheinfo", "160000,"+hsGit(t, sub, "rev-parse", "HEAD")+",mod")
	hsGit(t, r.Root, "commit", "-q", "-m", "submodule")
	base := hsGit(t, r.Root, "rev-parse", "HEAD")
	wt, err := r.CreateWorktreeAt(context.Background(), task, base)
	if err != nil {
		t.Fatal(err)
	}
	return r, base, wt, sub
}

// CanonicalTree never looks inside a gitlink path it is handed, even though
// today no caller hands it one (independent review cycle 2, N-a).
func TestCanonicalTreeNeverEntersAGitlinkPath(t *testing.T) {
	ctx := context.Background()
	_, base, wt, sub := hsBaseWithGitlink(t, "canon")
	marker := filepath.Join(t.TempDir(), "ran")
	if err := os.Remove(filepath.Join(wt, "mod")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	hsGit(t, wt, "clone", "-q", sub, "mod")
	hsAppendConfig(t, filepath.Join(wt, "mod", ".git", "config"), "[filter \"evil\"]\n\tclean = "+hsProgram(t, marker)+"\n")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(wt, "mod", "f.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	os.Remove(marker)
	_, err := (Repo{Root: wt}).CanonicalTree(ctx, base, []string{"mod"})
	hsRefused(t, "CanonicalTree of a gitlink path", err)
	if hsRan(marker) {
		t.Fatal("the planted repository's program ran while building the canonical tree")
	}
	// Control: the same temporary-index add under plain git runs it.
	index := filepath.Join(t.TempDir(), "index")
	for _, args := range [][]string{{"read-tree", base}, {"add", "--all", "--", "mod"}} {
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
		cmd.CombinedOutput()
	}
	if !hsRan(marker) {
		t.Fatal("premise: plain git's temporary-index add does not enter the planted repository")
	}
}

// A candidate that removes the base's gitlink is refused, not reviewed
// without the removal: ignoring submodules hides it from every diff
// (independent review cycle 2, N-b).
func TestARemovedBaseGitlinkIsRefusedNotOmitted(t *testing.T) {
	ctx := context.Background()
	_, base, wt, _ := hsBaseWithGitlink(t, "removed")
	hsGit(t, wt, "rm", "-q", "--cached", "mod")
	hsGit(t, wt, "commit", "-q", "-m", "drop the submodule")
	_, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	hsRefused(t, "CandidateCapture after removing the base's gitlink", err)
	all, _ := hsPlain(wt, "diff", "--name-status", base, "HEAD", "--")
	ignored, _ := hsPlain(wt, "diff", "--ignore-submodules=all", "--name-status", base, "HEAD", "--")
	if !strings.Contains(all, "mod") || strings.Contains(ignored, "mod") {
		t.Fatalf("premise: the removal is not a change that --ignore-submodules=all hides: %q / %q", all, ignored)
	}
}

// Every way a base gitlink can stop being the base's own is refused, decided
// on exact index entries -- including a crafted file whose NAME embeds the
// gitlink's index record (independent final-delta review, F1).
func TestEveryChangeToABaseGitlinkIsRefused(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(t *testing.T, wt, sha string){
		"a different commit": func(t *testing.T, wt, sha string) {
			hsGit(t, wt, "update-index", "--cacheinfo", "160000,"+hsGit(t, wt, "rev-parse", "HEAD")+",mod")
		},
		"a regular file at the path": func(t *testing.T, wt, sha string) {
			hsGit(t, wt, "rm", "-q", "--cached", "mod")
			if err := os.Remove(filepath.Join(wt, "mod")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			hsWrite(t, filepath.Join(wt, "mod"), "file\n")
			hsGit(t, wt, "add", "mod")
		},
		"a tree at the path": func(t *testing.T, wt, sha string) {
			hsGit(t, wt, "rm", "-q", "--cached", "mod")
			hsWrite(t, filepath.Join(wt, "mod", "f"), "file\n")
			hsGit(t, wt, "add", "mod/f")
		},
		"a removal masked by a crafted name": func(t *testing.T, wt, sha string) {
			hsGit(t, wt, "rm", "-q", "--cached", "mod")
			hsWrite(t, filepath.Join(wt, "x160000 "+sha+" 0\tmod"), "crafted\n")
			hsGit(t, wt, "add", "--", "x160000 "+sha+" 0\tmod")
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, base, wt, sub := hsBaseWithGitlink(t, "changed")
			change(t, wt, hsGit(t, sub, "rev-parse", "HEAD"))
			hsGit(t, wt, "commit", "-q", "-m", "change the gitlink")
			_, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
			hsRefused(t, "CandidateCapture after "+name, err)
		})
	}
}

// CanonicalTree refuses a named path that holds a repository even when the
// base has no gitlink there: the .git test, not the index listing, decides.
func TestCanonicalTreeRefusesAPathHoldingARepository(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "plainrepo", base)
	if err != nil {
		t.Fatal(err)
	}
	hsWrite(t, filepath.Join(wt, "plain", "f.txt"), "x\n")
	hsGit(t, filepath.Join(wt, "plain"), "init", "-q")
	_, err = (Repo{Root: wt}).CanonicalTree(ctx, base, []string{"plain"})
	hsRefused(t, "CanonicalTree of a path holding a repository", err)
}

// hsOwner gives this test an owner whose global git configuration and
// global attributes are the given text, and makes the trusted profile locate
// them afresh; the profile is located again from the real environment after.
func hsOwner(t *testing.T, gitconfig, attributes string) string {
	t.Helper()
	home := t.TempDir()
	if gitconfig != "" {
		hsWrite(t, filepath.Join(home, ".gitconfig"), gitconfig)
	}
	if attributes != "" {
		hsWrite(t, filepath.Join(home, "xdg", "git", "attributes"), attributes)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_ATTR_NOSYSTEM", "1")
	resetProfile := func() {
		profileMu.Lock()
		profile = nil
		profileMu.Unlock()
	}
	resetProfile()
	t.Cleanup(resetProfile)
	return home
}

// hsOwnerGit is ordinary git under the owner's configuration: the control.
func hsOwnerGit(dir string, stdin string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A content filter selected by the repository's attributes -- whose driver
// lives only in the owner's global configuration -- is refused before any
// content passes through git, wherever the attribute comes from; ordinary
// git would run the driver (Codex P1 on #297; RULING-319 witnesses 1-5).
func TestASelectedContentFilterIsRefused(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		plant   func(t *testing.T, r Repo, wt string)
		control []string // ordinary git in the worktree that runs the driver
	}{
		"global clean filter selected by a tracked .gitattributes": {
			plant: func(t *testing.T, r Repo, wt string) {
				hsWrite(t, filepath.Join(wt, ".gitattributes"), "*.txt filter=g\n")
				hsGit(t, wt, "-c", "filter.g.clean=cat", "add", ".gitattributes")
				hsGit(t, wt, "-c", "filter.g.clean=cat", "commit", "-q", "-m", "attributes")
				hsWrite(t, filepath.Join(wt, "a.txt"), "changed\n")
			},
			control: []string{"diff", "--no-ext-diff"},
		},
		"a git-lfs attribute whose driver is only global": {
			plant: func(t *testing.T, r Repo, wt string) {
				hsWrite(t, filepath.Join(wt, ".gitattributes"), "*.bin filter=lfs diff=lfs merge=lfs -text\n")
				hsWrite(t, filepath.Join(wt, "a.bin"), "large\n")
			},
			control: []string{"add", "a.bin"},
		},
		"repository info/attributes selecting a filter": {
			plant: func(t *testing.T, r Repo, wt string) {
				hsWrite(t, filepath.Join(r.Root, ".git", "info", "attributes"), "*.txt filter=g\n")
				hsWrite(t, filepath.Join(wt, "a.txt"), "changed\n")
			},
			control: []string{"diff", "--no-ext-diff"},
		},
		"a new candidate file whose attributes select a filter": {
			plant: func(t *testing.T, r Repo, wt string) {
				hsWrite(t, filepath.Join(wt, ".gitattributes"), "*.dat filter=g\n")
				hsWrite(t, filepath.Join(wt, "new.dat"), "new\n")
			},
			control: []string{"add", "new.dat"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, base := hsRepo(t)
			wt, err := r.CreateWorktreeAt(ctx, "filter", base)
			if err != nil {
				t.Fatal(err)
			}
			c.plant(t, r, wt)
			marker := filepath.Join(t.TempDir(), "ran")
			evil := hsProgram(t, marker)
			before := hsGit(t, wt, "ls-files", "--stage", "-z")
			hsOwner(t, "[filter \"g\"]\n\tclean = "+evil+"\n\tsmudge = "+evil+"\n[filter \"lfs\"]\n\tclean = "+evil+"\n\tsmudge = "+evil+"\n", "")
			_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
			hsRefused(t, "CandidateCapture", err)
			// Refused BEFORE the first staging operation: the index is exactly as
			// the worker left it (RULING-319 Q2).
			if after, _ := hsOwnerGit(wt, "", "ls-files", "--stage", "-z"); strings.TrimSpace(after) != before {
				t.Fatalf("the refused capture changed the index first:\nbefore %q\nafter  %q", before, after)
			}
			if !strings.Contains(err.Error(), "content filter") {
				t.Fatalf("refused, but not for the selected filter: %v", err)
			}
			if hsRan(marker) {
				t.Fatal("the filter driver ran through the trusted profile")
			}
			if out, _ := hsOwnerGit(wt, "", c.control...); !hsRan(marker) {
				t.Fatalf("premise: ordinary git under the owner's configuration does not run the driver\n%s", out)
			}
		})
	}
}

// A checkout that would smudge through a filter is refused before the
// worktree is created (RULING-319 witness 2).
func TestASmudgingCheckoutIsRefused(t *testing.T) {
	ctx := context.Background()
	r, _ := hsRepo(t)
	hsWrite(t, filepath.Join(r.Root, ".gitattributes"), "*.txt filter=g\n")
	hsGit(t, r.Root, "-c", "filter.g.clean=cat", "add", ".gitattributes")
	hsGit(t, r.Root, "-c", "filter.g.clean=cat", "commit", "-q", "-m", "attributes")
	base := hsGit(t, r.Root, "rev-parse", "HEAD")
	marker := filepath.Join(t.TempDir(), "ran")
	hsOwner(t, "[filter \"g\"]\n\tsmudge = "+hsProgram(t, marker)+"\n", "")
	_, err := r.CreateWorktreeAt(ctx, "smudge", base)
	hsRefused(t, "CreateWorktreeAt", err)
	_, err = r.CreateObservationWorktree(ctx, "smudge", base)
	hsRefused(t, "CreateObservationWorktree", err)
	if hsRan(marker) {
		t.Fatal("the smudge driver ran through the trusted profile")
	}
	if out, _ := hsOwnerGit(r.Root, "", "worktree", "add", "-q", "--detach", filepath.Join(t.TempDir(), "control"), base); !hsRan(marker) {
		t.Fatalf("premise: an ordinary checkout does not smudge\n%s", out)
	}
}

// The owner's content-transforming configuration and attributes refuse every
// trusted git operation; each case's control shows ordinary git converting
// content differently from the trusted profile (RULING-319 witnesses 6-11).
func TestTheOwnersContentTransformationsAreRefused(t *testing.T) {
	ctx := context.Background()
	crlf := "one\r\ntwo\r\n"
	for name, c := range map[string]struct {
		gitconfig, attributes string
		control               func(t *testing.T, r Repo) bool // true when ordinary git transforms
	}{
		"core.autocrlf true": {gitconfig: "[core]\n\tautocrlf = true\n", control: func(t *testing.T, r Repo) bool {
			return hsHashDiffers(t, r, crlf)
		}},
		"core.autocrlf input": {gitconfig: "[core]\n\tautocrlf = input\n", control: func(t *testing.T, r Repo) bool {
			return hsHashDiffers(t, r, crlf)
		}},
		"core.eol":                 {gitconfig: "[core]\n\teol = crlf\n", control: hsCheckoutConverts},
		"a global attributes file": {attributes: "*.txt text eol=crlf\n", control: hsCheckoutConverts},
		"core.attributesFile":      {gitconfig: "[core]\n\tattributesFile = ATTRS\n", control: hsCheckoutConverts},
		"an included file setting core.autocrlf": {gitconfig: "[include]\n\tpath = INCLUDED\n", control: func(t *testing.T, r Repo) bool {
			return hsHashDiffers(t, r, crlf)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := hsRepo(t)
			hsWrite(t, filepath.Join(r.Root, ".gitattributes"), "*.txt text\n")
			hsGit(t, r.Root, "add", ".gitattributes")
			hsGit(t, r.Root, "commit", "-q", "-m", "text")
			home := t.TempDir()
			attrs := filepath.Join(home, "attrs")
			hsWrite(t, attrs, "*.txt text eol=crlf\n")
			included := filepath.Join(home, "included")
			hsWrite(t, included, "[core]\n\tautocrlf = true\n")
			cfg := strings.ReplaceAll(strings.ReplaceAll(c.gitconfig, "ATTRS", attrs), "INCLUDED", included)
			hsOwner(t, cfg, c.attributes)
			_, err := r.Head(ctx)
			hsRefused(t, "Head", err)
			_, err = r.CreateWorktreeAt(ctx, "owner", hsGit(t, r.Root, "rev-parse", "HEAD"))
			hsRefused(t, "CreateWorktreeAt", err)
			if !c.control(t, r) {
				t.Fatal("premise: ordinary git under the owner's configuration does not transform content")
			}
		})
	}
	// Malformed or unreadable owner configuration is not a configuration the
	// profile can vouch for.
	t.Run("malformed owner configuration", func(t *testing.T) {
		r, _ := hsRepo(t)
		hsOwner(t, "[core\n\tautocrlf = true\n", "")
		_, err := r.Head(ctx)
		hsRefused(t, "Head", err)
	})
	t.Run("unreadable owner configuration", func(t *testing.T) {
		if os.Getuid() <= 0 {
			t.Skip("root reads any file")
		}
		r, _ := hsRepo(t)
		home := hsOwner(t, "[core]\n\tbare = false\n", "")
		if err := os.Chmod(filepath.Join(home, ".gitconfig"), 0); err != nil {
			t.Fatal(err)
		}
		_, err := r.Head(ctx)
		hsRefused(t, "Head", err)
	})
}

// hsHashDiffers reports whether ordinary git, under the owner's
// configuration, stores different bytes for a CRLF file than git with no
// owner configuration does.
func hsHashDiffers(t *testing.T, r Repo, body string) bool {
	t.Helper()
	owner, err1 := hsOwnerGit(r.Root, body, "hash-object", "--path=a.dat", "--stdin")
	cmd := exec.Command("git", "-C", r.Root, "hash-object", "--path=a.dat", "--stdin")
	cmd.Stdin = strings.NewReader(body)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
	plain, err2 := cmd.Output()
	return err1 == nil && err2 == nil && strings.TrimSpace(owner) != strings.TrimSpace(string(plain))
}

// hsCheckoutConverts reports whether ordinary git, under the owner's
// configuration, checks a.txt out with CRLF line endings.
func hsCheckoutConverts(t *testing.T, r Repo) bool {
	t.Helper()
	hsWrite(t, filepath.Join(r.Root, "a.txt"), "one\n")
	out, err := hsOwnerGit(r.Root, "", "cat-file", "--filters", "HEAD:a.txt")
	return err == nil && strings.Contains(out, "\r\n")
}

// The positive: the owner's autocrlf=false and a globally installed driver no
// path selects change nothing -- capture proceeds and is exactly ordinary
// git's (RULING-319 positives).
func TestNeutralOwnerConfigurationCapturesNormally(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "neutral", base)
	if err != nil {
		t.Fatal(err)
	}
	hsWrite(t, filepath.Join(wt, "a.txt"), "two\n")
	marker := filepath.Join(t.TempDir(), "ran")
	hsOwner(t, "[core]\n\tautocrlf = false\n[filter \"unused\"]\n\tclean = "+hsProgram(t, marker)+"\n", "# no rules\n")
	cap, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatalf("a neutral owner configuration was refused: %v", err)
	}
	if hsRan(marker) || !strings.Contains(cap.Diff, "+two") {
		t.Fatalf("unexpected capture (marker %v):\n%s", hsRan(marker), cap.Diff)
	}
	want, err := hsOwnerGit(wt, "", "diff", "--no-ext-diff", base, cap.Tree, "--")
	if err != nil || want != cap.Diff {
		t.Fatalf("the capture differs from ordinary git's rendering of its tree: %v\n%s\n---\n%s", err, cap.Diff, want)
	}
}

// The attribute query's answer is matched to its question exactly: a short,
// reordered or foreign answer is a refusal, never a pass (RULING-319
// witness 12).
func TestAnUnmatchedAttributeAnswerIsRefused(t *testing.T) {
	paths := []string{"a.txt", "b\tc.txt"}
	ok := "a.txt\x00filter\x00unspecified\x00b\tc.txt\x00filter\x00unset\x00"
	if err := filterAttributesRefusal(ok, paths); err != nil {
		t.Fatalf("a complete, matching answer was refused: %v", err)
	}
	for name, out := range map[string]string{
		"short":           "a.txt\x00filter\x00unspecified\x00",
		"reordered":       "b\tc.txt\x00filter\x00unset\x00a.txt\x00filter\x00unspecified\x00",
		"wrong attribute": "a.txt\x00diff\x00unspecified\x00b\tc.txt\x00filter\x00unset\x00",
		"unterminated":    "a.txt\x00filter\x00unspecified\x00b\tc.txt\x00filter\x00unset",
		"selected":        "a.txt\x00filter\x00lfs\x00b\tc.txt\x00filter\x00unset\x00",
		"set":             "a.txt\x00filter\x00set\x00b\tc.txt\x00filter\x00unset\x00",
	} {
		if err := filterAttributesRefusal(out, paths); err == nil || !strings.Contains(err.Error(), ProfileRefused) {
			t.Errorf("%s: not refused: %v", name, err)
		}
	}
}

// CanonicalTree checks again before it stages: a named path whose attributes
// select a filter is refused there too, not only by the capture before it
// (RULING-319 Q2 coverage).
func TestCanonicalTreeRefusesASelectedFilter(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "canonfilter", base)
	if err != nil {
		t.Fatal(err)
	}
	hsWrite(t, filepath.Join(wt, ".gitattributes"), "*.txt filter=g\n")
	hsWrite(t, filepath.Join(wt, "a.txt"), "two\n")
	_, err = (Repo{Root: wt}).CanonicalTree(ctx, base, []string{"a.txt", ".gitattributes"})
	hsRefused(t, "CanonicalTree", err)
}
