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

// The caller's environment cannot reach git, and the owner's or the
// environment's configuration cannot either: hostile GIT_* variables that are
// not configuration are ignored and the capture is the ordinary one; hostile
// configuration -- in HOME, in GIT_CONFIG_COUNT/KEY/VALUE or in
// GIT_CONFIG_PARAMETERS -- is refused by membership before git runs
// (RULING-322).
func TestTheCallersEnvironmentDoesNotReachGit(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (Repo, string, string, string) {
		r, base := hsRepo(t)
		wt, err := r.CreateWorktreeAt(ctx, "env", base)
		if err != nil {
			t.Fatal(err)
		}
		hsWrite(t, filepath.Join(wt, "a.txt"), "two\n")
		// diff=evil: a textconv driver hostile configuration would name.
		hsWrite(t, filepath.Join(wt, ".gitattributes"), "*.txt diff=evil\n")
		marker := filepath.Join(t.TempDir(), "ran")
		return r, base, wt, marker
	}
	t.Run("hostile variables that are not configuration", func(t *testing.T) {
		_, base, wt, marker := setup(t)
		evil := hsProgram(t, marker)
		home := t.TempDir()
		t.Setenv("GIT_DIR", filepath.Join(home, "elsewhere"))
		t.Setenv("GIT_EXTERNAL_DIFF", evil)
		t.Setenv("GIT_EXEC_PATH", home)
		cap, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
		if err != nil {
			t.Fatal(err)
		}
		if hsRan(marker) {
			t.Fatal("a program from the caller's environment ran")
		}
		if !strings.Contains(cap.Diff, "+two") || len(cap.Paths) != 2 {
			t.Fatalf("the capture is not the ordinary one: paths %q\n%s", cap.Paths, cap.Diff)
		}
	})
	for name, plant := range map[string]func(t *testing.T, evil string){
		"global configuration naming a program": func(t *testing.T, evil string) {
			hsOwner(t, "[core]\n\tfsmonitor = "+evil+"\n", "")
		},
		"GIT_CONFIG_COUNT naming a program": func(t *testing.T, evil string) {
			hsOwner(t, "", "")
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
			t.Setenv("GIT_CONFIG_VALUE_0", evil)
		},
		"GIT_CONFIG_PARAMETERS naming a program": func(t *testing.T, evil string) {
			hsOwner(t, "", "")
			t.Setenv("GIT_CONFIG_PARAMETERS", "'diff.evil.textconv'='"+evil+"'")
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, base, wt, marker := setup(t)
			evil := hsProgram(t, marker)
			plant(t, evil)
			_, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
			hsRefused(t, "CandidateCapture", err)
			if hsRan(marker) {
				t.Fatal("the hostile configuration's program ran")
			}
			// Control: ordinary git with the same environment runs it.
			if out, _ := hsOwnerGit(wt, "", "status", "--porcelain"); !hsRan(marker) {
				if out2, _ := hsOwnerGit(wt, "", "diff", "HEAD"); !hsRan(marker) {
					t.Fatalf("premise: ordinary git with this environment does not run the program\n%s\n%s", out, out2)
				}
			}
		})
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
	hsOwner(t, "[core]\n\tautocrlf = false\n[user]\n\tname = Owner\n\temail = owner@example.invalid\n[init]\n\tdefaultBranch = main\n", "# no rules\n")
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

// A candidate macro cannot turn an owner attribute rule into a content
// transformation: any active owner attribute rule is refused, whatever the
// attribute is called (independent review G1, RULING-320).
func TestAnOwnerAttributeCannotBeExpandedByACandidateMacro(t *testing.T) {
	ctx := context.Background()
	for name, macro := range map[string]string{
		"into a filter":             "[attr]export-ignore filter=up\n",
		"into a line-ending change": "[attr]export-ignore text eol=crlf\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, base := hsRepo(t)
			wt, err := r.CreateWorktreeAt(ctx, "macro", base)
			if err != nil {
				t.Fatal(err)
			}
			hsWrite(t, filepath.Join(wt, ".gitattributes"), macro)
			hsWrite(t, filepath.Join(wt, "a.txt"), "hello\nworld\n")
			hsOwner(t, "[filter \"up\"]\n\tclean = tr a-z A-Z\n", "*.txt export-ignore\n")
			_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
			hsRefused(t, "CandidateCapture", err)
			// Control: ordinary git, with the owner's attributes, expands the
			// macro and stores different bytes than git without them.
			owner, err1 := hsOwnerGit(wt, "hello\r\nworld\r\n", "hash-object", "--path=a.txt", "--stdin")
			cmd := exec.Command("git", "-C", wt, "hash-object", "--path=a.txt", "--stdin")
			cmd.Stdin = strings.NewReader("hello\r\nworld\r\n")
			cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
			plain, err2 := cmd.Output()
			if err1 != nil || err2 != nil || strings.TrimSpace(owner) == strings.TrimSpace(string(plain)) {
				t.Fatalf("premise: the owner's attribute does not change what ordinary git stores (%v %v)", err1, err2)
			}
		})
	}
}

// System attributes are hidden from the trusted profile, as system
// configuration is: git itself reports no system attributes file under the
// profile's environment (RULING-320 G1, supported-git witness).
func TestTheProfileReadsNoSystemAttributes(t *testing.T) {
	p, err := trustedProfile()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(p.exe, "var", "GIT_ATTR_SYSTEM")
	cmd.Dir, cmd.Env = p.home, p.baseEnv()
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		t.Fatalf("under the profile, git still names a system attributes file: %q", out)
	}
	// Control: without the switch, git names one.
	cmd = exec.Command(p.exe, "var", "GIT_ATTR_SYSTEM")
	cmd.Dir = p.home
	if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("premise: git names no system attributes file even without GIT_ATTR_NOSYSTEM: %q %v", out, err)
	}
}

// The owner's global excludes are honoured exactly as ordinary git honours
// them: an owner-ignored file never enters the capture, local rules keep their
// precedence, an intended path does not override it, and a changed excludes
// file is seen by the next capture (independent review G2, RULING-320).
func TestTheOwnersGlobalExcludesAreHonoured(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "excludes", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"secret.env", "kept.env", "notes.local", "b.txt"} {
		hsWrite(t, filepath.Join(wt, f), "dummy\n")
	}
	hsWrite(t, filepath.Join(wt, ".gitignore"), "!kept.env\n")
	home := hsOwner(t, "", "")
	ignore := filepath.Join(home, "xdg", "git", "ignore")
	hsWrite(t, ignore, "*.env\n")
	paths := func() []string {
		t.Helper()
		cap, err := (Repo{Root: wt}).CandidateCapture(ctx, base, []string{"secret.env"})
		if err != nil {
			t.Fatal(err)
		}
		return cap.Paths
	}
	has := func(ps []string, p string) bool {
		for _, x := range ps {
			if x == p {
				return true
			}
		}
		return false
	}
	got := paths()
	if has(got, "secret.env") {
		t.Fatalf("an owner-ignored file entered the capture (even named as intended): %q", got)
	}
	if !has(got, "kept.env") || !has(got, "b.txt") {
		t.Fatalf("the repository's negation or an ordinary new file was lost: %q", got)
	}
	// The same verdict as ordinary git under the owner's configuration.
	ordinary, _ := hsOwnerGit(wt, "", "status", "--porcelain", "--untracked-files=all")
	if strings.Contains(ordinary, "secret.env") || !strings.Contains(ordinary, "kept.env") {
		t.Fatalf("premise: ordinary git does not ignore secret.env and keep kept.env:\n%s", ordinary)
	}
	// Control: without the owner's excludes, secret.env WOULD be captured.
	cmd := exec.Command("git", "-C", wt, "status", "--porcelain", "--untracked-files=all")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	if out, _ := cmd.Output(); !strings.Contains(string(out), "secret.env") {
		t.Fatalf("premise: without the owner's excludes secret.env is not a new file\n%s", out)
	}
	// A changed excludes file is seen by the next capture -- of a worktree the
	// first capture has not already intent-added paths in (a path git already
	// tracks is not un-tracked by a new ignore rule, in ordinary git either).
	hsWrite(t, ignore, "*.local\n")
	wt2, err := r.CreateWorktreeAt(ctx, "excludes-changed", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"secret.env", "notes.local"} {
		hsWrite(t, filepath.Join(wt2, f), "dummy\n")
	}
	cap2, err := (Repo{Root: wt2}).CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !has(cap2.Paths, "secret.env") || has(cap2.Paths, "notes.local") {
		t.Fatalf("the changed owner excludes were not honoured: %q", cap2.Paths)
	}
}

// An excludes source that is unsafe or ambiguous is refused; a missing one
// ignores nothing, as in ordinary git (RULING-320 G2).
func TestAnUnsafeOwnerExcludesSourceIsRefused(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		config func(home, wt string) string
		refuse bool
	}{
		"inside the candidate worktree": {config: func(home, wt string) string {
			return "[core]\n\texcludesFile = " + filepath.Join(wt, "ignore") + "\n"
		}, refuse: true},
		"a symlink": {config: func(home, wt string) string {
			os.Symlink(filepath.Join(wt, "ignore"), filepath.Join(home, "link"))
			return "[core]\n\texcludesFile = " + filepath.Join(home, "link") + "\n"
		}, refuse: true},
		"two different files": {config: func(home, wt string) string {
			inc := filepath.Join(home, "inc")
			os.WriteFile(inc, []byte("[core]\n\texcludesFile = /x/two\n"), 0o644)
			return "[core]\n\texcludesFile = /x/one\n[include]\n\tpath = " + inc + "\n"
		}, refuse: true},
		// H1: a value behind an unevaluated includeIf may not apply.
		"behind an includeIf": {config: func(home, wt string) string {
			inc := filepath.Join(home, "work.inc")
			os.WriteFile(inc, []byte("[core]\n\texcludesFile = "+filepath.Join(home, "work-ignore")+"\n"), 0o644)
			return "[includeIf \"gitdir:" + filepath.Join(home, "work") + "/\"]\n\tpath = " + inc + "\n"
		}, refuse: true},
		// H2: ordinary git resolves a relative value inside the repository.
		"relative": {config: func(home, wt string) string {
			return "[core]\n\texcludesFile = rel-ignore\n"
		}, refuse: true},
		"missing": {config: func(home, wt string) string {
			return "[core]\n\texcludesFile = " + filepath.Join(home, "absent") + "\n"
		}, refuse: false},
	} {
		t.Run(name, func(t *testing.T) {
			r, base := hsRepo(t)
			wt, err := r.CreateWorktreeAt(ctx, "unsafe", base)
			if err != nil {
				t.Fatal(err)
			}
			hsWrite(t, filepath.Join(wt, "ignore"), "*.txt\n")
			home := t.TempDir()
			hsOwner(t, c.config(home, wt), "")
			_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
			if c.refuse {
				hsRefused(t, "CandidateCapture", err)
			} else if err != nil {
				t.Fatalf("a missing excludes file was refused: %v", err)
			}
		})
	}
	t.Run("unreadable", func(t *testing.T) {
		if os.Getuid() <= 0 {
			t.Skip("root reads any file")
		}
		r, base := hsRepo(t)
		wt, err := r.CreateWorktreeAt(ctx, "unreadable", base)
		if err != nil {
			t.Fatal(err)
		}
		home := hsOwner(t, "", "")
		ignore := filepath.Join(home, "xdg", "git", "ignore")
		hsWrite(t, ignore, "*.env\n")
		if err := os.Chmod(ignore, 0); err != nil {
			t.Fatal(err)
		}
		_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
		hsRefused(t, "CandidateCapture", err)
	})
}

// Owner settings that change pathname identity, rendering or rejection are
// refused unless the repository establishes them itself (RULING-320 G3, G4).
func TestOwnerPathAndRenderingSettingsAreRefused(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		gitconfig string
		control   func(t *testing.T, r Repo) bool // ordinary git behaves differently; nil where the platform cannot show it
	}{
		"core.ignoreCase": {gitconfig: "[core]\n\tignoreCase = true\n", control: func(t *testing.T, r Repo) bool {
			// A.TXT beside the tracked a.txt: ordinary git under the owner's
			// ignoreCase no longer reports it as a new file.
			hsWrite(t, filepath.Join(r.Root, "A.TXT"), "upper\n")
			owner, _ := hsOwnerGit(r.Root, "", "status", "--porcelain", "--untracked-files=all")
			cmd := exec.Command("git", "-C", r.Root, "status", "--porcelain", "--untracked-files=all")
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
			plain, _ := cmd.Output()
			return owner != string(plain)
		}},
		// Linux git does not act on it: conservative refusal (RULING-320 Q3).
		"core.precomposeUnicode": {gitconfig: "[core]\n\tprecomposeUnicode = true\n"},
		// H4: recorded modes, when the repository does not set it.
		"core.fileMode": {gitconfig: "[core]\n\tfileMode = false\n", control: func(t *testing.T, r Repo) bool {
			os.Chmod(filepath.Join(r.Root, "a.txt"), 0o644) // hsWrite commits 0755
			owner, _ := hsOwnerGit(r.Root, "", "status", "--porcelain")
			cmd := exec.Command("git", "-C", r.Root, "status", "--porcelain")
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull)
			plain, _ := cmd.Output()
			return owner != string(plain)
		}},
		"core.bigFileThreshold": {gitconfig: "[core]\n\tbigFileThreshold = 1\n", control: func(t *testing.T, r Repo) bool {
			hsWrite(t, filepath.Join(r.Root, "a.txt"), "changed\n")
			out, _ := hsOwnerGit(r.Root, "", "diff", "--no-ext-diff")
			return strings.Contains(out, "Binary files")
		}},
		"core.safecrlf": {gitconfig: "[core]\n\tsafecrlf = true\n\tautocrlf = false\n", control: func(t *testing.T, r Repo) bool {
			hsWrite(t, filepath.Join(r.Root, ".gitattributes"), "*.txt text\n")
			hsWrite(t, filepath.Join(r.Root, "a.txt"), "one\r\ntwo\n")
			_, err := hsOwnerGit(r.Root, "", "add", "a.txt")
			return err != nil
		}},
		"core.checkRoundtripEncoding": {gitconfig: "[core]\n\tcheckRoundtripEncoding = UTF-16\n"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := hsRepo(t)
			if name == "core.fileMode" {
				// git init records it; only a repository without it diverges.
				hsGit(t, r.Root, "config", "--unset", "core.filemode")
			}
			hsOwner(t, c.gitconfig, "")
			_, err := r.Head(ctx)
			hsRefused(t, "Head", err)
			if c.control != nil && !c.control(t, r) {
				t.Fatal("premise: ordinary git under the owner's setting behaves no differently")
			}
		})
	}
	// The repository's own setting wins in git's precedence, so the owner's no
	// longer diverges.
	r, _ := hsRepo(t)
	hsGit(t, r.Root, "config", "core.ignoreCase", "false")
	hsOwner(t, "[core]\n\tignoreCase = true\n", "")
	if _, err := r.Head(ctx); err != nil {
		t.Fatalf("an owner ignoreCase the repository overrides was refused: %v", err)
	}
}

// Discovery that git cannot answer exactly is a refusal, and the supported
// version boundary is git 2.42 (RULING-320 G6).
func TestOwnerDiscoveryAndVersionBoundary(t *testing.T) {
	r, _ := hsRepo(t)
	hsOwner(t, "", "")
	t.Setenv("GIT_CONFIG_GLOBAL", "relative/config")
	profileMu.Lock()
	profile = nil
	profileMu.Unlock()
	_, err := r.Head(context.Background())
	hsRefused(t, "a relative global configuration path", err)
	for v, ok := range map[string]bool{
		"git version 2.41.0": false, "git version 2.42.0": true, "git version 2.43.0": true,
		"git version 2.42.0.windows.1": true, "git version 3.0.0": true, "git version 2.34.1": false,
		"not git": false,
	} {
		if versionAtLeast(v, 2, 42) != ok {
			t.Errorf("versionAtLeast(%q, 2.42) != %v", v, ok)
		}
	}
}

// A diff between two commits still reports a gitlink: only diffs against the
// worktree, which could run git inside a nested repository, ignore them
// (Codex r4250927580).
func TestACommitDiffReportsAGitlink(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	hsGit(t, r.Root, "update-index", "--add", "--cacheinfo", "160000,"+base+",mod")
	hsGit(t, r.Root, "commit", "-q", "-m", "gitlink only")
	head := hsGit(t, r.Root, "rev-parse", "HEAD")
	out, err := r.CommitDiff(ctx, head)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mod") || !strings.Contains(out, "Subproject commit") {
		t.Fatalf("the commit diff hides its gitlink:\n%s", out)
	}
	if hidden, _ := hsPlain(r.Root, "diff", "--ignore-submodules=all", head+"^!", "--"); strings.Contains(hidden, "mod") {
		t.Fatalf("premise: --ignore-submodules=all does not hide the gitlink:\n%s", hidden)
	}
}

// Every diff in this package either faces the worktree and carries
// ignoreNested, or compares two trees and does not: checked at each diff's own
// argument list, so a function with several diffs cannot hide one unflagged
// (Codex r4250927580, independent review B2).
func TestEveryDiffDeclaresWhetherItFacesTheWorktree(t *testing.T) {
	treeToTree := map[string]bool{"CommitDiff": true, "RenderCandidateDiff": true, "validateBoundaryAgainstTree": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	diffs := 0
	check := func(fn string, pos token.Pos, elems []ast.Expr) {
		hasDiff, hasFlag := false, false
		for _, e := range elems {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Value == `"diff"` {
				hasDiff = true
			}
			if id, ok := e.(*ast.Ident); ok && id.Name == "ignoreNested" {
				hasFlag = true
			}
		}
		if !hasDiff {
			return
		}
		diffs++
		if treeToTree[fn] == hasFlag {
			t.Errorf("%s (%s): a diff that %s, but its arguments %s ignoreNested", fn, fset.Position(pos),
				map[bool]string{true: "compares two trees", false: "faces the worktree"}[treeToTree[fn]],
				map[bool]string{true: "carry", false: "lack"}[hasFlag])
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					check(fn.Name.Name, x.Pos(), x.Args)
				case *ast.CompositeLit:
					check(fn.Name.Name, x.Pos(), x.Elts)
				}
				return true
			})
		}
	}
	if diffs < 7 {
		t.Fatalf("premise: the census found only %d diff argument lists", diffs)
	}
}

// Owner configuration the trusted profile cannot resolve exactly as git does
// is refused: an include supplied through the environment, an include or
// excludes path in "~user/" or "%(prefix)/" form, and an excludes path that a
// symlinked directory leads into the candidate worktree (Codex r4250927589,
// r4250927600, r4250927611).
func TestOwnerIndirectionsGitResolvesAreRefused(t *testing.T) {
	ctx := context.Background()
	t.Run("an include supplied through the environment", func(t *testing.T) {
		r, _ := hsRepo(t)
		home := hsOwner(t, "", "")
		inc := filepath.Join(home, "env.inc")
		hsWrite(t, inc, "[core]\n\tautocrlf = true\n")
		t.Setenv("GIT_CONFIG_COUNT", "1")
		t.Setenv("GIT_CONFIG_KEY_0", "include.path")
		t.Setenv("GIT_CONFIG_VALUE_0", inc)
		_, err := r.Head(ctx)
		hsRefused(t, "Head", err)
		// Control: ordinary git with the environment stores different bytes for
		// a CRLF file than ordinary git without it.
		body := "one\r\ntwo\r\n"
		with, err1 := hsOwnerGit(r.Root, body, "hash-object", "--path=a.dat", "--stdin")
		cmd := exec.Command("git", "-C", r.Root, "hash-object", "--path=a.dat", "--stdin")
		cmd.Stdin = strings.NewReader(body)
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "GIT_CONFIG_") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		without, err2 := cmd.Output()
		if err1 != nil || err2 != nil || strings.TrimSpace(with) == strings.TrimSpace(string(without)) {
			t.Fatalf("premise: ordinary git does not follow the environment's include (%v %v)", err1, err2)
		}
	})
	for name, cfg := range map[string]string{
		"a ~user/ include":     "[include]\n\tpath = ~root/.gitconfig\n",
		"a %(prefix)/ include": "[include]\n\tpath = %(prefix)/etc/gitconfig\n",
		"a ~user/ excludes":    "[core]\n\texcludesFile = ~root/ignore\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := hsRepo(t)
			hsOwner(t, cfg, "")
			_, err := r.Head(ctx)
			hsRefused(t, "Head", err)
		})
	}
	t.Run("an excludes path through a symlinked directory", func(t *testing.T) {
		r, base := hsRepo(t)
		wt, err := r.CreateWorktreeAt(ctx, "linked", base)
		if err != nil {
			t.Fatal(err)
		}
		hsWrite(t, filepath.Join(wt, "ignore"), "hidden.txt\n")
		hsWrite(t, filepath.Join(wt, "hidden.txt"), "x\n")
		home := t.TempDir()
		if err := os.Symlink(wt, filepath.Join(home, "link")); err != nil {
			t.Fatal(err)
		}
		hsOwner(t, "[core]\n\texcludesFile = "+filepath.Join(home, "link", "ignore")+"\n", "")
		_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
		hsRefused(t, "CandidateCapture", err)
		if strings.HasPrefix(filepath.Join(home, "link", "ignore"), wt) {
			t.Fatal("premise: the excludes path is lexically inside the worktree")
		}
	})
}

// A filter attribute is refused before add -N even when no driver is
// configured anywhere: the trusted profile never supplies an empty driver in
// place of a missing one (RULING-319), and the index is left untouched.
func TestAFilterAttributeIsRefusedBeforeStaging(t *testing.T) {
	ctx := context.Background()
	r, base := hsRepo(t)
	wt, err := r.CreateWorktreeAt(ctx, "nodriver", base)
	if err != nil {
		t.Fatal(err)
	}
	hsWrite(t, filepath.Join(wt, ".gitattributes"), "*.dat filter=nowhere\n")
	hsWrite(t, filepath.Join(wt, "new.dat"), "new\n")
	before := hsGit(t, wt, "ls-files", "--stage", "-z")
	_, err = (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	if err == nil || !strings.Contains(err.Error(), "content filter") {
		t.Fatalf("a filter attribute was not refused as one: %v", err)
	}
	if after := hsGit(t, wt, "ls-files", "--stage", "-z"); after != before {
		t.Fatalf("the refused capture changed the index first:\nbefore %q\nafter  %q", before, after)
	}
}

// The owner's configuration is read by membership: an unknown key of any
// shape, a repeated assignment that turns unsafe, an invalid value for an
// admitted key, an escaped file reference, an environment-supplied unknown
// key and a symlink setting the repository does not establish are all
// refused before git runs (RULING-322).
func TestOwnerConfigurationIsReadByMembership(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		gitconfig string
		env       map[string]string
		repo      []string // repository config to set first
	}{
		"an unknown core key":                 {gitconfig: "[core]\n\tpager = less\n"},
		"an unknown non-core key":             {gitconfig: "[merge]\n\tconflictStyle = diff3\n"},
		"an unknown subsection key":           {gitconfig: "[remote \"origin\"]\n\tprune = true\n"},
		"safe then unsafe autocrlf":           {gitconfig: "[core]\n\tautocrlf = false\n\tautocrlf = true\n"},
		"an invalid admitted value":           {gitconfig: "[core]\n\tignoreCase = maybe\n", repo: []string{"core.ignoreCase", "false"}},
		"an escaped excludes path":            {gitconfig: "[core]\n\texcludesFile = /tmp/a\\\\b\n"},
		"an include":                          {gitconfig: "[include]\n\tpath = /tmp/x\n"},
		"an includeIf":                        {gitconfig: "[includeIf \"gitdir:/\"]\n\tpath = /tmp/x\n"},
		"an environment unknown key":          {env: map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "merge.tool", "GIT_CONFIG_VALUE_0": "x"}},
		"a URL-qualified credential helper":   {gitconfig: "[credential \"https://example.invalid\"]\n\thelper = store\n"},
		"credential.username":                 {gitconfig: "[credential]\n\tusername = someone\n"},
		"credential.useHttpPath":              {gitconfig: "[credential]\n\tuseHttpPath = true\n"},
		"an arbitrary safe key":               {gitconfig: "[safe]\n\tbareRepository = all\n"},
		"core.symlinks the repo does not set": {gitconfig: "[core]\n\tsymlinks = false\n"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := hsRepo(t)
			for i := 0; i+1 < len(c.repo); i += 2 {
				hsGit(t, r.Root, "config", c.repo[i], c.repo[i+1])
			}
			hsOwner(t, c.gitconfig, "")
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			_, err := r.Head(ctx)
			hsRefused(t, "Head", err)
		})
	}
	// The escaped reference is one git decodes: ordinary git reads a
	// different path than the raw text names (Codex r4251107439).
	r, _ := hsRepo(t)
	hsOwner(t, "[core]\n\texcludesFile = /tmp/a\\\\b\n", "")
	if out, _ := hsOwnerGit(r.Root, "", "config", "--global", "--get", "core.excludesFile"); strings.TrimSpace(out) != `/tmp/a\b` {
		t.Fatalf("premise: git does not decode the escape: %q", out)
	}
}

// Repository precedence for the path keys: the owner's value is admitted only
// when the repository sets its own, which both sides then apply (J1, Codex
// r4251107452). Without it, a symlink ordinary git would check out as a file
// is refused rather than checked out as a symlink.
func TestOwnerPathKeysFollowRepositoryPrecedence(t *testing.T) {
	ctx := context.Background()
	// Positive: the repository establishes each key; owner values agree or
	// conflict, and the repository's wins on both sides.
	for _, k := range []string{"core.fileMode", "core.symlinks"} {
		for _, owner := range []string{"true", "false"} {
			r, _ := hsRepo(t)
			hsGit(t, r.Root, "config", k, "true")
			hsOwner(t, "[core]\n\t"+strings.TrimPrefix(k, "core.")+" = "+owner+"\n", "")
			if _, err := r.Head(ctx); err != nil {
				t.Errorf("owner %s=%s with the repository's own value was refused: %v", k, owner, err)
			}
		}
	}
	// Negative with control: a symlink blob, owner core.symlinks=false, no
	// repository value.
	r, _ := hsRepo(t)
	if err := os.Symlink("/etc/passwd", filepath.Join(r.Root, "link")); err != nil {
		t.Fatal(err)
	}
	hsGit(t, r.Root, "add", "link")
	hsGit(t, r.Root, "commit", "-q", "-m", "link")
	base := hsGit(t, r.Root, "rev-parse", "HEAD")
	hsOwner(t, "[core]\n\tsymlinks = false\n", "")
	_, err := r.CreateWorktreeAt(ctx, "symlink", base)
	hsRefused(t, "CreateWorktreeAt", err)
	control := filepath.Join(t.TempDir(), "control")
	if out, err := hsOwnerGit(r.Root, "", "worktree", "add", "-q", "--detach", control, base); err != nil {
		t.Fatalf("control: %v\n%s", err, out)
	}
	if fi, err := os.Lstat(filepath.Join(control, "link")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("premise: ordinary git under owner symlinks=false did not check the link out as a regular file (%v)", err)
	}
}

// safe.directory and credential.helper are observed, never used (RULING-323):
// with them present -- several entries, a helper that would leave a marker --
// every supported operation works, nothing runs, the capture is identical to
// one without them, and neither value reaches the executed git; ownership is
// still ownedByUs's.
func TestObservedOwnerKeysNeverReachGit(t *testing.T) {
	ctx := context.Background()
	capture := func(t *testing.T, gitconfig string) (Capture, Repo, string) {
		t.Helper()
		r, base := hsRepo(t)
		hsOwner(t, gitconfig, "")
		wt, err := r.CreateWorktreeAt(ctx, "observed", base)
		if err != nil {
			t.Fatal(err)
		}
		hsWrite(t, filepath.Join(wt, "a.txt"), "two\n")
		hsWrite(t, filepath.Join(wt, "b.txt"), "new\n")
		cap, err := (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
		if err != nil {
			t.Fatalf("capture with observed owner keys: %v", err)
		}
		return cap, r, wt
	}
	marker := filepath.Join(t.TempDir(), "helper-ran")
	helper := hsProgram(t, marker)
	with, r, wt := capture(t, "[safe]\n\tdirectory = *\n\tdirectory = /somewhere\n[credential]\n\thelper = "+helper+"\n\thelper = store\n")
	without, _, _ := capture(t, "")
	if with.Tree != without.Tree || with.Diff != without.Diff || strings.Join(with.Paths, "\x00") != strings.Join(without.Paths, "\x00") {
		t.Fatalf("the observed keys changed the capture:\n%+v\n%+v", with, without)
	}
	hsOwner(t, "[safe]\n\tdirectory = *\n[credential]\n\thelper = "+helper+"\n", "")
	if _, err := r.Head(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.WorktreeIsClean(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if u := r.OriginURL(ctx); u != "" {
		t.Fatalf("unexpected origin %q", u)
	}
	if err := r.RemoveWorktree(ctx, wt); err != nil {
		t.Fatal(err)
	}
	if hsRan(marker) {
		t.Fatal("the owner's credential helper ran")
	}
	// Neither value is handed to git.
	cmd, err := r.gitCommand(ctx, r.Root, nil, true, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(append(append([]string{}, cmd.Args...), cmd.Env...), "\n")
	if strings.Contains(all, helper) || strings.Contains(strings.ToLower(all), "safe.directory") || strings.Contains(strings.ToLower(all), "credential") {
		t.Fatalf("an observed owner key reached the executed git:\n%s", all)
	}
	// safe.directory does not relax ownership.
	if os.Getuid() > 0 {
		hsRefused(t, "ownedByUs(/) under safe.directory=*", ownedByUs("/"))
	}
	// A candidate's own helper never enters the trusted environment: the
	// repository allowlist refuses it.
	hsGit(t, r.Root, "config", "credential.helper", helper)
	_, err = r.Head(ctx)
	hsRefused(t, "a repository credential helper", err)
}

// Locating the owner's files never reads another repository's configuration:
// git var is fenced at the empty home, so a repository above it -- a planted
// /tmp/.git, trusted through the owner's safe.directory -- cannot redirect
// where the owner's attributes are found (independent review K0, RULING-323).
func TestLocatingOwnerFilesReadsNoEnclosingRepository(t *testing.T) {
	ctx := context.Background()
	r, _ := hsRepo(t)
	planted := t.TempDir()
	benign := filepath.Join(planted, "benign-attrs")
	hsWrite(t, benign, "# nothing\n")
	hsGit(t, planted, "init", "-q")
	hsGit(t, planted, "config", "core.attributesFile", benign)
	t.Setenv("TMPDIR", planted) // the profile's home is created inside it
	hsOwner(t, "[safe]\n\tdirectory = *\n", "* text\n")
	_, err := r.Head(ctx)
	hsRefused(t, "Head with an owner attribute rule", err)
	// Control: git var run inside the planted repository is redirected.
	sub := filepath.Join(planted, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "var", "GIT_ATTR_GLOBAL")
	cmd.Dir = sub
	if out, _ := cmd.Output(); strings.TrimSpace(string(out)) != benign {
		t.Fatalf("premise: git var inside the planted repository is not redirected: %q", out)
	}
}

// Environment configuration outranks the repository's, so the repository
// cannot establish a path key the environment sets; a quoted excludes path
// and GIT_ATTR_SOURCE are refused (independent review K1, K2, K3).
func TestEnvironmentPrecedenceAndQuotedPathsAreRefused(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		gitconfig string
		env       map[string]string
	}{
		"an environment core.symlinks over the repository's": {
			env: map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.symlinks", "GIT_CONFIG_VALUE_0": "false"}},
		"a quoted excludes path": {gitconfig: "[core]\n\texcludesFile = \"/tmp/x \"\n"},
		"GIT_ATTR_SOURCE":        {env: map[string]string{"GIT_ATTR_SOURCE": "HEAD"}},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := hsRepo(t)
			hsGit(t, r.Root, "config", "core.symlinks", "true")
			hsOwner(t, c.gitconfig, "")
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			_, err := r.Head(ctx)
			hsRefused(t, "Head", err)
		})
	}
}

// An owner-installed filter driver no path selects -- the git-lfs block a
// GitHub runner's /etc/gitconfig carries -- changes nothing: capture works and
// is identical, and the driver never runs; once a path selects it, the
// capture is refused before staging (Codex r4251382665, RULING-324).
func TestAnInstalledUnselectedFilterDriverIsObservedOnly(t *testing.T) {
	ctx := context.Background()
	marker := filepath.Join(t.TempDir(), "driver-ran")
	evil := hsProgram(t, marker)
	lfs := "[filter \"lfs\"]\n\tclean = " + evil + "\n\tsmudge = " + evil + "\n\tprocess = " + evil + "\n\trequired = true\n"
	capture := func(t *testing.T, gitconfig, attrs string) (Capture, error) {
		t.Helper()
		r, base := hsRepo(t)
		hsOwner(t, gitconfig, "")
		wt, err := r.CreateWorktreeAt(ctx, "lfs", base)
		if err != nil {
			return Capture{}, err
		}
		hsWrite(t, filepath.Join(wt, "a.txt"), "two\n")
		hsWrite(t, filepath.Join(wt, "b.bin"), "data\n")
		if attrs != "" {
			hsWrite(t, filepath.Join(wt, ".gitattributes"), attrs)
		}
		return (Repo{Root: wt}).CandidateCapture(ctx, base, nil)
	}
	with, err := capture(t, lfs, "")
	if err != nil {
		t.Fatalf("an installed, unselected driver refused the capture: %v", err)
	}
	without, err := capture(t, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if with.Tree != without.Tree || with.Diff != without.Diff {
		t.Fatal("an installed, unselected driver changed the capture")
	}
	_, err = capture(t, lfs, "*.bin filter=lfs\n")
	if err == nil || !strings.Contains(err.Error(), "content filter") {
		t.Fatalf("a path selecting the installed driver was not refused as a content filter: %v", err)
	}
	if hsRan(marker) {
		t.Fatal("the installed driver ran")
	}
	// Only the four driver keys: any other filter.* key is still refused.
	r, _ := hsRepo(t)
	hsOwner(t, "[filter \"lfs\"]\n\tsomethingElse = x\n", "")
	_, err = r.Head(ctx)
	hsRefused(t, "an unknown filter key", err)
}

// Environment-supplied configuration may provide only inert keys: command
// scope outranks the repository's own configuration, so an environment
// core.autocrlf over a repository value is refused (Codex r4251382662).
func TestEnvironmentConfigurationProvidesOnlyInertKeys(t *testing.T) {
	ctx := context.Background()
	r, _ := hsRepo(t)
	hsGit(t, r.Root, "config", "core.autocrlf", "true")
	hsOwner(t, "", "")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.autocrlf")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	_, err := r.Head(ctx)
	hsRefused(t, "an environment core.autocrlf over the repository's", err)
	// Control: ordinary git with the environment stores different bytes than
	// git reading only the repository's value.
	body := "one\r\ntwo\r\n"
	with, err1 := hsOwnerGit(r.Root, body, "hash-object", "--path=a.dat", "--stdin")
	cmd := exec.Command("git", "-C", r.Root, "hash-object", "--path=a.dat", "--stdin")
	cmd.Stdin = strings.NewReader(body)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GIT_CONFIG_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	without, err2 := cmd.Output()
	if err1 != nil || err2 != nil || strings.TrimSpace(with) == strings.TrimSpace(string(without)) {
		t.Fatalf("premise: the environment's autocrlf does not change what ordinary git stores (%v %v)", err1, err2)
	}
	// Every other special key, and filter definitions, from the environment
	// are refused too (RULING-324 Q6 E, F).
	for key, value := range map[string]string{"core.excludesFile": "/tmp/ignore", "filter.lfs.clean": "cat", "core.symlinks": "false"} {
		t.Setenv("GIT_CONFIG_KEY_0", key)
		t.Setenv("GIT_CONFIG_VALUE_0", value)
		_, err := r.Head(ctx)
		hsRefused(t, "an environment "+key, err)
	}
	// An inert key from the environment is admitted.
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "Owner")
	if _, err := r.Head(ctx); err != nil {
		t.Fatalf("an inert environment key was refused: %v", err)
	}
}

// Owner configuration paths are taken exactly as git reports them: a path
// whose spelling ends in whitespace is refused, not trimmed into another
// file; /dev/null, git's spelling of an empty configuration, is empty
// (Codex r4251563406, r4251563416).
func TestOwnerConfigurationPathsAreExact(t *testing.T) {
	ctx := context.Background()
	t.Run("a global path ending in whitespace", func(t *testing.T) {
		r, _ := hsRepo(t)
		home := hsOwner(t, "", "")
		spaced := filepath.Join(home, "global ")
		hsWrite(t, spaced, "[core]\n\tautocrlf = true\n")
		t.Setenv("GIT_CONFIG_GLOBAL", spaced)
		profileMu.Lock()
		profile = nil
		profileMu.Unlock()
		_, err := r.Head(ctx)
		hsRefused(t, "Head", err)
		// Control: git itself reads the spaced file.
		if out, _ := hsOwnerGit(r.Root, "", "config", "--global", "--get", "core.autocrlf"); strings.TrimSpace(out) != "true" {
			t.Fatalf("premise: git does not read the whitespace-ended path: %q", out)
		}
	})
	t.Run("a path variable holding a newline", func(t *testing.T) {
		r, _ := hsRepo(t)
		home := hsOwner(t, "", "")
		t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "a")+"\n"+filepath.Join(home, "b"))
		profileMu.Lock()
		profile = nil
		profileMu.Unlock()
		_, err := r.Head(ctx)
		hsRefused(t, "Head", err)
	})
	t.Run("GIT_CONFIG_GLOBAL=/dev/null", func(t *testing.T) {
		r, _ := hsRepo(t)
		hsOwner(t, "", "")
		t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
		profileMu.Lock()
		profile = nil
		profileMu.Unlock()
		if _, err := r.Head(ctx); err != nil {
			t.Fatalf("an explicitly empty global configuration was refused: %v", err)
		}
	})
	t.Run("another non-regular file", func(t *testing.T) {
		r, _ := hsRepo(t)
		home := hsOwner(t, "", "")
		dir := filepath.Join(home, "a-directory")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", dir)
		profileMu.Lock()
		profile = nil
		profileMu.Unlock()
		_, err := r.Head(ctx)
		hsRefused(t, "Head with a directory as global configuration", err)
	})
}
