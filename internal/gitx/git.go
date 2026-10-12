package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type Repo struct{ Root string }

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// noOptionalLocks is git's flag for "answer this query without taking the
// locks a query would otherwise take". Named once so a test can pin that the
// read-only inspections still carry it.
const noOptionalLocks = "--no-optional-locks"

// Discover finds the repository containing start WITHOUT running git: running
// git to learn which repository it is in would already have loaded that
// repository's configuration. The top level is the nearest directory holding
// .git, and the repository must satisfy the trusted profile before it is
// returned.
func Discover(ctx context.Context, start string) (Repo, error) {
	dir, err := filepath.Abs(start)
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		return Repo{}, fmt.Errorf("not a git repository: %w", err)
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Repo{}, fmt.Errorf("not a git repository: no .git above %s", start)
		}
		dir = parent
	}
	id, err := resolveIdentity(dir)
	if err != nil {
		return Repo{}, err
	}
	if err := checkRepoConfig(id); err != nil {
		return Repo{}, err
	}
	return Repo{Root: dir}, nil
}

func (r Repo) Head(ctx context.Context) (string, error) {
	return r.readOnlyOutput(ctx, "rev-parse", "HEAD")
}
func (r Repo) Branch(ctx context.Context) (string, error) {
	return r.readOnlyOutput(ctx, "branch", "--show-current")
}

// Diff returns the working-tree diff verbatim.
//
// It uses raw() rather than output() because a diff must not be trimmed. Every
// other git call here is a short single value where stripping whitespace is
// convenient; a unified diff is a format, and its trailing newline is part of
// it. Trimming the end of a patch whose final added lines are blank leaves a
// hunk header promising more lines than the body contains, and Sensei rejects
// the result as malformed_diff -- which is what made every audit in a real run
// come back cannot_verify, for weeks of apparent graph trouble that was
// actually two missing newlines.
func (r Repo) Diff(ctx context.Context, base string) (string, error) {
	args := []string{"diff", "--no-ext-diff", "--binary"}
	if b := strings.TrimSpace(base); b != "" {
		args = append(args, b)
	}
	return r.raw(ctx, args...)
}

// CandidateDiff is the whole of a candidate's work, including files it created.
//
// Plain `git diff` reports tracked modifications only, so a worker that added a
// file produced a diff that did not contain it. The reviewer then judged a
// candidate with its new files invisible -- rejecting real work for omitting a
// test that was sitting untracked on disk -- and no candidate could ever
// converge. Intent-to-add records the new paths so they appear in the diff,
// without staging their content; .gitignore is still honoured.
// CandidateDiff is everything the candidate changed relative to the base it was
// cut from, whether the worker committed it or not.
//
// Diffing the working tree alone was wrong in a way that only appeared once a
// worker used a capability it had been granted: local_commit is permitted, and
// a worker that commits its work leaves a clean tree, so the candidate diff
// came back empty and the run reported that the implementor had produced
// nothing. It had produced everything and then tidied up.
//
// Diffing against the recorded base makes committed and uncommitted work look
// the same to the reviewer, which is the only reading that matches what the
// candidate actually contains.
func (r Repo) CandidateDiff(ctx context.Context, base string) (string, error) {
	// Through the boundary, with no intended outputs: every new binary or
	// oversized file is an artifact here. Callers that know the plan use
	// CandidateCapture directly and receive what was excluded.
	cap, err := r.CandidateCapture(ctx, base, nil)
	if err != nil {
		return "", err
	}
	return cap.Diff, nil
}

// RevList returns commit ids in a range, newest first, bounded by limit.
//
// Used only by the counterfactual scanner, which replays history through the
// routine classifier. It reads history and never rewrites it.
func (r Repo) RevList(ctx context.Context, spec string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	out, err := r.output(ctx, "rev-list", fmt.Sprintf("--max-count=%d", limit), spec)
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			commits = append(commits, line)
		}
	}
	return commits, nil
}

// CommitDiff is the patch one commit introduced, against its first parent.
func (r Repo) CommitDiff(ctx context.Context, commit string) (string, error) {
	return r.output(ctx, "diff", commit+"^!", "--")
}

func (r Repo) IsClean(ctx context.Context) (bool, error) {
	s, err := r.readOnlyOutput(ctx, "status", "--porcelain")
	return s == "", err
}

// Worktrees deliberately live next to the repository, not inside it. A Git
// worktree is an execution boundary and must never be confused with local
// Sensei Code session state under <repo>/.sensei-code.
//
// A candidate belongs to the task, not to the worker that happens to be
// carrying it. One worktree per worker meant a worker that ran out of review
// cycles took its work with it and the next one started from an empty
// checkout, discarding every fix the reviewer had already extracted.
func (r Repo) WorktreePath(taskID string) string {
	// "<repo>" not "<repo>.sensei-code": the suffix already names the tool, so
	// repeating the repository name produced ".sensei-code.sensei-code-worktrees".
	base := "." + filepath.Base(r.Root) + "-worktrees"
	return filepath.Join(filepath.Dir(r.Root), base, clean(taskID))
}

// WorktreeBranch names the branch a candidate is built on. Publication needs
// the same name the worktree was created with, so it is derived once here
// rather than reconstructed by each caller.
func (r Repo) WorktreeBranch(taskID string) string {
	return "sensei-code/" + clean(taskID)
}

// CreateWorktree returns the task's candidate worktree, creating it on first
// use and reusing it afterwards so a second worker continues the same work.
func (r Repo) CreateWorktree(ctx context.Context, taskID string) (string, error) {
	path := r.WorktreePath(taskID)
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	branch := r.WorktreeBranch(taskID)
	return path, r.addWorktree(ctx, path, branch, "HEAD")
}

// CreateWorktreeAt cuts a candidate from an exact commit rather than from
// whatever HEAD happens to mean at this instant.
//
// The distinction matters on re-entry: a task interrupted and resumed later
// must continue from the base its plan was approved against, and "HEAD" is not
// that base once anything else has been committed.
func (r Repo) CreateWorktreeAt(ctx context.Context, taskID, baseSHA string) (string, error) {
	base := strings.TrimSpace(baseSHA)
	if base == "" {
		return "", fmt.Errorf("refusing to create a candidate worktree without an explicit base commit")
	}
	path := r.WorktreePath(taskID)
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, r.addWorktree(ctx, path, r.WorktreeBranch(taskID), base)
}

// CreateObservationWorktree checks out a DETACHED copy of one commit for a
// read-only run.
//
// Detached, and deliberately so: an observation has nothing to commit, so a
// branch would be state to clean up rather than state to use.
//
// What this buys, precisely: the observer's working directory is somewhere the
// governed checkout is not, so an ordinary edit lands here instead of there. It
// is NOT filesystem confinement. The subprocess keeps the ambient permissions
// of the user running Sensei Code and can address any path it likes, including
// the governed root.
//
// The first draft of this comment said a separate worktree "stops a
// write-capable provider from touching" the governed checkout. The observation
// lane audited this file and reported that sentence as false, which it was --
// the same overclaim the reviewer had just rejected in the post-hoc
// cleanliness check, rewritten one layer down.
//
// Removing it is the caller's job, and RemoveObservationWorktree does not need
// a branch deleted afterwards.
func (r Repo) CreateObservationWorktree(ctx context.Context, taskID, baseSHA string) (string, error) {
	base := strings.TrimSpace(baseSHA)
	if base == "" {
		return "", fmt.Errorf("refusing to create an observation worktree without an explicit base commit")
	}
	path := r.WorktreePath(taskID) + "-observe"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := r.refuseFilterAttributesAt(ctx, base); err != nil {
		return "", err
	}
	cmd, err := r.gitCommand(ctx, r.Root, nil, false, "worktree", "add", "--detach", path, base)
	if err != nil {
		return "", err
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add --detach: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return path, nil
}

// WorktreeIsClean reports whether a worktree has no tracked or untracked
// changes, and names them when it does.
//
// Used on the observation workspace, where a write is harmless because the
// workspace is discarded, as a SIGNAL that a configured provider is not as
// read-only as the lane assumes.
//
// It carries exactly the weakness that disqualified this check as a safety
// mechanism on the governed repository: a commit, a write to an ignored path,
// or a write followed by a restore all leave status clean. That is tolerable
// here and was not there, because nothing rests on the answer -- a positive
// result is informative and a negative one proves nothing. Do not promote this
// to a boundary.
func (r Repo) WorktreeIsClean(ctx context.Context, path string) (bool, string, error) {
	b, err := r.readOnly(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, "", err
	}
	out := strings.TrimSpace(string(b))
	return out == "", out, nil
}

func (r Repo) addWorktree(ctx context.Context, path, branch, base string) error {
	if err := r.refuseFilterAttributesAt(ctx, base); err != nil {
		return err
	}
	cmd, err := r.gitCommand(ctx, r.Root, nil, false, "worktree", "add", "-b", branch, path, base)
	if err != nil {
		return err
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// DeleteBranch removes a candidate branch. It is deliberately the forceful
// form: a candidate branch is never merged anywhere by this program, so git's
// "not fully merged" refusal would block every legitimate disposal. What makes
// that safe is not the flag but the caller — a branch is only ever deleted
// after its evidence has been recorded.
func (r Repo) DeleteBranch(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("no branch named to delete")
	}
	cmd, err := r.gitCommand(ctx, r.Root, nil, false, "branch", "-D", name)
	if err != nil {
		return err
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch -D %s: %w: %s", name, err, strings.TrimSpace(string(b)))
	}
	return nil
}

// WorktreeIsCleanDetail returns a worktree's porcelain status verbatim.
//
// Separate from WorktreeIsClean because the observation lane needs to COMPARE
// two moments rather than test one: "clean" cannot distinguish a change this
// run made from one that was already present, and treating an already-dirty
// repository as evidence of mutation is a false accusation that fires exactly
// when someone audits a work-in-progress.
func (r Repo) WorktreeIsCleanDetail(ctx context.Context, path string) (string, error) {
	b, err := r.readOnly(ctx, path, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// RemoveObservationWorktree discards a detached observation workspace. No
// branch is deleted, because none was created.
func (r Repo) RemoveObservationWorktree(ctx context.Context, path string) error {
	return r.RemoveWorktree(ctx, path)
}

func (r Repo) RemoveWorktree(ctx context.Context, path string) error {
	cmd, err := r.gitCommand(ctx, r.Root, nil, false, "worktree", "remove", "--force", path)
	if err != nil {
		return err
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// readOnly runs a git command that INSPECTS a checkout, taking no optional
// lock while it does.
//
// `--no-optional-locks` is git's own switch for exactly this. Without it a
// plain `git status` may refresh and rewrite the index while answering, which
// is a write to the repository being measured -- so the read-only lane's own
// cleanliness check was capable of modifying the thing it exists to prove
// unmodified. A checker for a read-only lane must not become the write it is
// trying to detect.
//
// What this narrows is precise and small: THIS PROGRAM'S measurement of a
// checkout does not mutate it. It is not filesystem confinement, not a
// sandbox, and says nothing about what a subprocess may reach. Do not describe
// it as a boundary.
//
// Reserved for genuine reads. `worktree add`, `worktree remove` and
// `branch -D` are writes and are left alone: suppressing the locks a write
// needs would be a correctness bug, not a narrowing.
func (r Repo) readOnly(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd, err := r.gitCommand(ctx, dir, nil, true, args...)
	if err != nil {
		return nil, err
	}
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(b)))
	}
	return b, nil
}

// readOnlyOutput is readOnly for a short single value, trimmed like output().
func (r Repo) readOnlyOutput(ctx context.Context, args ...string) (string, error) {
	b, err := r.readOnly(ctx, r.Root, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// raw runs git and returns stdout exactly as produced. Use it for anything
// whose format matters; output() is for short values where trimming helps.
func (r Repo) raw(ctx context.Context, args ...string) (string, error) {
	cmd, err := r.gitCommand(ctx, r.Root, nil, false, args...)
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (r Repo) output(ctx context.Context, args ...string) (string, error) {
	cmd, err := r.gitCommand(ctx, r.Root, nil, false, args...)
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func clean(s string) string {
	s = safeName.ReplaceAllString(strings.TrimSpace(s), "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "unnamed"
	}
	return s
}

// OriginURL is the repository's own statement of what it is.
//
// Read-only and independent of any service. It exists for the graph identity
// handshake: the domain a governed run uses came from the awareness service's own
// answer, so nothing compared that answer to the repository on disk, and a healthy
// service for another repository was governed by without complaint. Deriving the
// identity here gives the comparison a second source that no graph can influence.
//
// An unset or unreadable remote yields "" rather than an error: the caller's rule
// is that an underivable identity refuses, and it says so better than a wrapped
// git error would.
func (r Repo) OriginURL(ctx context.Context) string {
	out, err := r.readOnly(ctx, r.Root, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// # Trusted host git
//
// Every git this package runs is a host process with this user's authority,
// and git will run programs the repository names: hooks, fsmonitor, clean,
// smudge and process filters, textconv, an external diff, a helper pulled in
// by an include. Measured on git 2.43 (RULING-312), a candidate worktree's
// configuration or .git pointer was enough to make Sensei's own capture,
// status and worktree calls execute a candidate's program on the host.
//
// gitCommand is the one way this package runs git, and it establishes the
// profile BEFORE git starts, refusing rather than running a weaker git:
//
//   - the executable is resolved once, by absolute path, and must honour
//     GIT_CONFIG_GLOBAL;
//   - the environment is built from an allowlist, never inherited, so neither
//     the caller's GIT_* variables nor a global or system configuration reach
//     it;
//   - the repository and worktree are resolved by reading files, not by
//     running git, a candidate worktree must be the one gitx created, and the
//     verified directories are passed as GIT_DIR and GIT_WORK_TREE so git never
//     re-reads a pointer the candidate can rewrite;
//   - the repository configuration is read AS DATA and every key must belong to
//     a closed allowlist, so a driver, helper or include is refused by name;
//   - hooks are pointed at an empty directory this process owns, and fsmonitor
//     is off.
//
// What stays outside: a process that can write the main .git between the
// inspection and the run (a candidate test on the host, until N4 confines
// them), and remote publication, which needs credentials this profile drops.

// ProfileRefused prefixes every refusal to run git outside the trusted
// profile.
const ProfileRefused = "REFUSED_UNSAFE_GIT_PROFILE"

func refuseProfile(format string, a ...any) error {
	return fmt.Errorf("%s: %s", ProfileRefused, fmt.Sprintf(format, a...))
}

// hostProfile is the process-wide part of the trusted profile.
type hostProfile struct {
	exe   string // absolute path of the git executable
	home  string // empty directory standing in for HOME and XDG_CONFIG_HOME
	hooks string // empty directory every invocation points core.hooksPath at
	// The owner's own configuration and attribute files, which ordinary git
	// would read and this profile does not. They are read as DATA, never
	// through git, so a content transformation they select can be refused
	// rather than silently dropped (RULING-319).
	ownerConfig []string
	ownerAttrs  []string
	ownerHome   string
}

var (
	profileMu  sync.Mutex
	profile    *hostProfile
	gitProgram = "git" // the name resolved on PATH; tests replace it
)

// trustedProfile resolves the executable and creates the empty directories
// once. A failure is not cached, so a later call can succeed.
func trustedProfile() (hostProfile, error) {
	profileMu.Lock()
	defer profileMu.Unlock()
	if profile != nil {
		return *profile, nil
	}
	exe, err := exec.LookPath(gitProgram)
	if err != nil || !filepath.IsAbs(exe) {
		return hostProfile{}, refuseProfile("no git executable resolves to an absolute path: %v", err)
	}
	home, err := os.MkdirTemp("", "sensei-code-git-home-")
	if err != nil {
		return hostProfile{}, refuseProfile("the trusted git home cannot be created: %v", err)
	}
	hooks := filepath.Join(home, "hooks")
	if err := os.Mkdir(hooks, 0o700); err != nil {
		return hostProfile{}, refuseProfile("the trusted hooks directory cannot be created: %v", err)
	}
	p := hostProfile{exe: exe, home: home, hooks: hooks}
	cmd := exec.Command(p.exe, "--version")
	cmd.Dir, cmd.Env = home, p.baseEnv()
	out, err := cmd.Output()
	if err != nil {
		return hostProfile{}, refuseProfile("%s --version failed: %v", exe, err)
	}
	if !versionAtLeast(string(out), 2, 42) {
		return hostProfile{}, refuseProfile("%s is %q; the trusted profile needs git 2.42 or later (GIT_CONFIG_GLOBAL, git var config paths)", exe, strings.TrimSpace(string(out)))
	}
	if err := p.locateOwnerFiles(); err != nil {
		return hostProfile{}, err
	}
	profile = &p
	return p, nil
}

// locateOwnerFiles asks git WHERE ordinary git would read the owner's global
// and system configuration and attributes, honouring the caller's
// path-selection variables. `git var` prints paths and reads nothing else;
// it runs in the empty home, outside any repository.
func (p *hostProfile) locateOwnerFiles() error {
	env := []string{"PATH=" + os.Getenv("PATH")}
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM",
		"GIT_ATTR_NOSYSTEM", "SYSTEMROOT", "SystemRoot", "USERPROFILE"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	p.ownerHome = os.Getenv("HOME")
	for _, v := range []string{"GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL", "GIT_ATTR_SYSTEM", "GIT_ATTR_GLOBAL"} {
		cmd := exec.Command(p.exe, "var", v)
		cmd.Dir, cmd.Env = p.home, env
		out, err := cmd.Output()
		if err != nil {
			// A system source switched off by the caller has no path.
			if strings.HasSuffix(v, "_SYSTEM") && (os.Getenv("GIT_CONFIG_NOSYSTEM") != "" || os.Getenv("GIT_ATTR_NOSYSTEM") != "") {
				continue
			}
			return refuseProfile("the owner's %s cannot be located: %v", v, err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line == "" {
				continue
			}
			if strings.HasPrefix(v, "GIT_CONFIG") {
				p.ownerConfig = append(p.ownerConfig, line)
			} else {
				p.ownerAttrs = append(p.ownerAttrs, line)
			}
		}
	}
	return nil
}

// checkOwnerContent refuses when the owner's ordinary git would transform
// content in a way this profile does not reproduce: core.autocrlf other than
// false, core.eol, core.attributesFile, or an attribute rule that converts
// content. It reads the files as data -- including the files they include --
// and reports only the source, key and reason, never a value (RULING-319).
func (p hostProfile) checkOwnerContent() error {
	for _, e := range os.Environ() {
		k, v, _ := strings.Cut(e, "=")
		if k == "GIT_CONFIG_PARAMETERS" || strings.HasPrefix(k, "GIT_CONFIG_KEY_") {
			low := strings.ToLower(v)
			for _, key := range []string{"core.autocrlf", "core.eol", "core.attributesfile"} {
				if strings.Contains(low, key) {
					return refuseProfile("the environment (%s) sets %s for ordinary git, which the trusted profile does not reproduce", k, key)
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, f := range p.ownerConfig {
		if err := p.checkOwnerConfigFile(f, seen, 0); err != nil {
			return err
		}
	}
	for _, f := range p.ownerAttrs {
		if err := checkOwnerAttributes(f); err != nil {
			return err
		}
	}
	return nil
}

func (p hostProfile) checkOwnerConfigFile(f string, seen map[string]bool, depth int) error {
	if depth > 10 {
		return refuseProfile("the owner's git configuration includes files more than 10 deep at %s", f)
	}
	if seen[f] {
		return nil
	}
	seen[f] = true
	keys, err := configKeys(f, true)
	if err != nil {
		return err
	}
	for k, vs := range keys {
		switch {
		case k == "core.autocrlf":
			for _, v := range vs {
				if !v.isFalse() {
					return refuseProfile("%s sets core.autocrlf to a value other than false; ordinary git converts line endings the trusted profile does not", f)
				}
			}
		case k == "core.eol", k == "core.attributesfile":
			return refuseProfile("%s sets %s; ordinary git transforms content the trusted profile does not", f, k)
		case k == "include.path" || strings.HasPrefix(k, "includeif.") && strings.HasSuffix(k, ".path"):
			// Followed as data only, whatever its condition: a file that might
			// apply is a file whose settings might matter.
			for _, v := range vs {
				inc := v.raw
				if strings.HasPrefix(inc, "~/") {
					inc = filepath.Join(p.ownerHome, inc[2:])
				} else if !filepath.IsAbs(inc) {
					inc = filepath.Join(filepath.Dir(f), inc)
				}
				if err := p.checkOwnerConfigFile(inc, seen, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// contentAttributes are the attributes that change what content git stores
// or checks out.
var contentAttributes = setOf("text", "eol", "crlf", "filter", "working-tree-encoding", "ident", "binary")

// checkOwnerAttributes refuses an owner attributes file that sets any
// content-converting attribute.
func checkOwnerAttributes(f string) error {
	body, err := readSmallFile(f)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return refuseProfile("the owner's attributes file %s cannot be read: %v", f, err)
	}
	for n, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		var attrs string
		switch {
		case strings.HasPrefix(line, "[attr]"):
			_, attrs, _ = strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "[attr]")), " ")
		case line[0] == '"':
			end := strings.LastIndex(line, "\"")
			if end <= 0 {
				return refuseProfile("%s:%d has an unterminated quoted pattern", f, n+1)
			}
			attrs = line[end+1:]
		default:
			_, attrs, _ = strings.Cut(strings.ReplaceAll(line, "\t", " "), " ")
		}
		for _, a := range strings.Fields(attrs) {
			name, _, _ := strings.Cut(strings.TrimLeft(a, "-!"), "=")
			if contentAttributes[strings.ToLower(name)] {
				return refuseProfile("%s:%d sets the content attribute %s; ordinary git transforms content the trusted profile does not", f, n+1, name)
			}
		}
	}
	return nil
}

// versionAtLeast reads "git version X.Y..." and compares X.Y.
func versionAtLeast(out string, major, minor int) bool {
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return false
	}
	parts := strings.SplitN(fields[2], ".", 3)
	if len(parts) < 2 {
		return false
	}
	x, err1 := strconv.Atoi(parts[0])
	y, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return x > major || (x == major && y >= minor)
}

// baseEnv is the allowlisted environment: nothing git reads for
// configuration, hooks, helpers or repository location is inherited.
func (p hostProfile) baseEnv() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + p.home,
		"XDG_CONFIG_HOME=" + p.home,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		// Replace refs and grafts substitute objects: a worker that wrote
		// refs/replace in the shared repository could make "the base" mean
		// another commit to every read below (independent review, N2).
		"GIT_NO_REPLACE_OBJECTS=1",
		// A path that never exists: no grafts, and no deprecation notice in
		// the output (os.DevNull "exists" and makes git print one).
		"GIT_GRAFT_FILE=" + filepath.Join(p.home, "no-grafts"),
		"LC_ALL=C",
	}
	// Not git configuration: the system's own location of temporary files, and
	// the variables a Windows process needs to start at all.
	for _, k := range []string{"TMPDIR", "TEMP", "TMP", "SYSTEMROOT", "SystemRoot", "PATHEXT"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// gitCommand builds the one git process this package runs, about the
// repository or worktree at dir. extra may only carry the two variables this
// package uses as mechanism; readOnly adds --no-optional-locks. Nothing runs
// when the profile cannot be established.
func (r Repo) gitCommand(ctx context.Context, dir string, extra []string, readOnly bool, args ...string) (*exec.Cmd, error) {
	p, err := trustedProfile()
	if err != nil {
		return nil, err
	}
	id, err := resolveIdentity(dir)
	if err != nil {
		return nil, err
	}
	if err := checkRepoConfig(id); err != nil {
		return nil, err
	}
	if err := p.checkOwnerContent(); err != nil {
		return nil, err
	}
	if entries, err := os.ReadDir(p.hooks); err != nil || len(entries) != 0 {
		return nil, refuseProfile("the trusted hooks directory %s is not an empty directory", p.hooks)
	}
	// GIT_COMMON_DIR too: git would otherwise re-read <gitDir>/commondir at
	// run time, a file in a directory the worktree's own git writes to.
	env := append(p.baseEnv(), "GIT_DIR="+id.gitDir, "GIT_COMMON_DIR="+id.commonDir, "GIT_WORK_TREE="+id.workTree)
	for _, e := range extra {
		if !strings.HasPrefix(e, "GIT_INDEX_FILE=") && e != "GIT_LITERAL_PATHSPECS=1" {
			return nil, refuseProfile("environment %q is not part of the trusted profile", e)
		}
		env = append(env, e)
	}
	full := []string{"-c", "core.hooksPath=" + p.hooks, "-c", "core.fsmonitor=false"}
	if readOnly {
		full = append(full, noOptionalLocks)
	}
	// status and diff look inside a nested repository (a gitlink in the
	// worktree) by running git THERE, with that repository's configuration and
	// attributes, which this profile never inspected (independent review, B2).
	// The flag form wins over any .gitmodules "ignore" setting.
	if len(args) > 0 && (args[0] == "status" || args[0] == "diff") {
		args = append([]string{args[0], "--ignore-submodules=all"}, args[1:]...)
	}
	cmd := exec.CommandContext(ctx, p.exe, append(full, args...)...)
	cmd.Dir, cmd.Env = id.workTree, env
	return cmd, nil
}

// gitIdentity is a repository location established without running git.
type gitIdentity struct {
	workTree  string // the checkout git operates on
	gitDir    string // its git directory (a worktree's own, under commonDir/worktrees)
	commonDir string // the repository's shared git directory, holding config
}

// resolveIdentity establishes which repository dir is.
//
// A directory inside the gitx candidate convention (WorktreePath) is a
// candidate or observation worktree, and its .git pointer was written into a
// directory the candidate controls, so it is only believed when the main
// repository's records agree with it. Any other directory is the owner's own
// checkout.
func resolveIdentity(dir string) (gitIdentity, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return gitIdentity{}, refuseProfile("%s cannot be resolved: %v", dir, err)
	}
	root = filepath.Clean(root)
	if owner, ok := conventionOwner(root); ok {
		main, err := resolveIdentity(owner)
		if err != nil {
			return gitIdentity{}, refuseProfile("%s is a candidate worktree, but its repository %s is not trusted: %v", root, owner, err)
		}
		return linkedWorktree(root, main.commonDir)
	}
	dotgit := filepath.Join(root, ".git")
	fi, err := os.Lstat(dotgit)
	switch {
	case err != nil:
		return gitIdentity{}, refuseProfile("%s is not a git repository: %v", root, err)
	case fi.IsDir():
		// A commondir file would make git read ANOTHER directory's
		// configuration, one this check never saw (independent review, S2).
		if _, err := os.Lstat(filepath.Join(dotgit, "commondir")); err == nil {
			return gitIdentity{}, refuseProfile("%s carries a commondir file, which would redirect git to another repository", dotgit)
		}
		if err := ownedByUs(dotgit); err != nil {
			return gitIdentity{}, err
		}
		return gitIdentity{workTree: root, gitDir: dotgit, commonDir: dotgit}, nil
	case fi.Mode().IsRegular():
		gitDir, err := readGitdirPointer(root)
		if err != nil {
			return gitIdentity{}, err
		}
		common, err := readCommondir(gitDir)
		if err != nil {
			return gitIdentity{}, err
		}
		return linkedWorktree(root, common)
	}
	return gitIdentity{}, refuseProfile("%s is neither a directory nor a gitdir file", dotgit)
}

// conventionOwner reports the repository a path inside its worktree
// directory belongs to: <parent>/.<name>-worktrees/<x> belongs to
// <parent>/<name> (WorktreePath).
func conventionOwner(root string) (string, bool) {
	holder := filepath.Base(filepath.Dir(root))
	if !strings.HasPrefix(holder, ".") || !strings.HasSuffix(holder, "-worktrees") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(holder, "."), "-worktrees")
	if name == "" {
		return "", false
	}
	return filepath.Join(filepath.Dir(filepath.Dir(root)), name), true
}

// linkedWorktree accepts root as a linked worktree of the repository whose
// shared git directory is common only when both sides agree: root/.git is a
// regular file naming common/worktrees/<n>, that directory names root/.git
// back, and its commondir leads to common.
func linkedWorktree(root, common string) (gitIdentity, error) {
	fi, err := os.Lstat(filepath.Join(root, ".git"))
	if err != nil || !fi.Mode().IsRegular() {
		return gitIdentity{}, refuseProfile("%s/.git is not the gitdir file a linked worktree has", root)
	}
	gitDir, err := readGitdirPointer(root)
	if err != nil {
		return gitIdentity{}, err
	}
	realGitDir, err1 := filepath.EvalSymlinks(gitDir)
	realHolder, err2 := filepath.EvalSymlinks(filepath.Join(common, "worktrees"))
	if err1 != nil || err2 != nil || filepath.Dir(realGitDir) != realHolder {
		return gitIdentity{}, refuseProfile("%s/.git points at %s, which is not a worktree of %s", root, gitDir, common)
	}
	if gi, err := os.Lstat(gitDir); err != nil || !gi.IsDir() {
		return gitIdentity{}, refuseProfile("the worktree git directory %s is not a directory", gitDir)
	}
	back, err := readSmallFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return gitIdentity{}, refuseProfile("%s records no worktree: %v", gitDir, err)
	}
	backPath := strings.TrimSpace(back)
	if !filepath.IsAbs(backPath) {
		backPath = filepath.Join(gitDir, backPath)
	}
	a, err1 := os.Stat(backPath)
	b, err2 := os.Stat(filepath.Join(root, ".git"))
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		return gitIdentity{}, refuseProfile("%s records the worktree %s, not %s", gitDir, backPath, root)
	}
	recorded, err := readCommondir(gitDir)
	if err != nil {
		return gitIdentity{}, err
	}
	c1, err1 := os.Stat(recorded)
	c2, err2 := os.Stat(common)
	if err1 != nil || err2 != nil || !os.SameFile(c1, c2) {
		return gitIdentity{}, refuseProfile("%s belongs to %s, not %s", gitDir, recorded, common)
	}
	for _, d := range []string{gitDir, common} {
		if err := ownedByUs(d); err != nil {
			return gitIdentity{}, err
		}
	}
	return gitIdentity{workTree: root, gitDir: gitDir, commonDir: common}, nil
}

// ownedByUs refuses a git directory owned by another user: git's own
// ownership check (safe.directory) is skipped when GIT_DIR is explicit, and a
// repository another user can rewrite between this inspection and the run is
// not one this inspection describes (independent review, S3). Where the
// platform reports no owner (Windows), no owner can be compared and the check
// does not apply.
func ownedByUs(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return refuseProfile("%s cannot be inspected: %v", dir, err)
	}
	uid := os.Getuid()
	if uid < 0 || fi.Sys() == nil {
		return nil
	}
	v := reflect.Indirect(reflect.ValueOf(fi.Sys()))
	if v.Kind() != reflect.Struct {
		return nil
	}
	f := v.FieldByName("Uid")
	if !f.IsValid() || !f.CanUint() {
		return nil
	}
	if f.Uint() != uint64(uid) {
		return refuseProfile("%s is owned by uid %d, not this user (uid %d)", dir, f.Uint(), uid)
	}
	return nil
}

// readGitdirPointer reads root/.git as a "gitdir: <path>" file.
func readGitdirPointer(root string) (string, error) {
	body, err := readSmallFile(filepath.Join(root, ".git"))
	if err != nil {
		return "", refuseProfile("%s/.git cannot be read: %v", root, err)
	}
	line := strings.TrimSpace(body)
	if !strings.HasPrefix(line, "gitdir: ") || strings.Contains(line, "\n") {
		return "", refuseProfile("%s/.git is not a gitdir pointer", root)
	}
	p := strings.TrimSpace(strings.TrimPrefix(line, "gitdir: "))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p), nil
}

// readCommondir is the shared git directory a worktree's git directory names.
func readCommondir(gitDir string) (string, error) {
	body, err := readSmallFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return "", refuseProfile("%s names no common directory: %v", gitDir, err)
	}
	p := strings.TrimSpace(body)
	if !filepath.IsAbs(p) {
		p = filepath.Join(gitDir, p)
	}
	p = filepath.Clean(p)
	if fi, err := os.Lstat(p); err != nil || !fi.IsDir() {
		return "", refuseProfile("the common directory %s of %s is not a directory", p, gitDir)
	}
	return p, nil
}

// readSmallFile reads a regular file that is not a symlink, bounded.
func readSmallFile(p string) (string, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", p)
	}
	if fi.Size() > 1<<20 {
		return "", fmt.Errorf("%s is larger than 1 MiB", p)
	}
	b, err := os.ReadFile(p)
	return string(b), err
}

// checkRepoConfig reads the repository's configuration as data and refuses any
// key outside the closed allowlist. Membership, not exclusion: a key this
// list does not know is refused, because an unknown key is one nobody has
// shown cannot name a program.
func checkRepoConfig(id gitIdentity) error {
	keys, err := configKeys(filepath.Join(id.commonDir, "config"), false)
	if err != nil {
		return err
	}
	if _, ok := keys["extensions.worktreeconfig"]; ok {
		wt, err := configKeys(filepath.Join(id.gitDir, "config.worktree"), true)
		if err != nil {
			return err
		}
		for k, vs := range wt {
			keys[k] = append(keys[k], vs...)
		}
	}
	for k, vs := range keys {
		if !allowedConfigKey(k) {
			return refuseProfile("the repository configuration sets %s, which the trusted git profile does not admit", k)
		}
		if falseOnlyKeys[k] {
			for _, v := range vs {
				if !v.isFalse() {
					return refuseProfile("the repository configuration sets %s to a value other than false, which the trusted git profile does not admit", k)
				}
			}
		}
	}
	return nil
}

// falseOnlyKeys are admitted only when every value they hold is false: they
// are compatibility settings that turn signing off, and true would ask for a
// signing program (RULING-317).
var falseOnlyKeys = setOf("commit.gpgsign", "tag.gpgsign")

// configValue is one assignment of a key: its value as written, after the
// comment and surrounding quotes are removed, and whether it had an "=" at
// all (a bare key is git's true).
type configValue struct {
	raw      string
	assigned bool
}

// isFalse is git's boolean false, strictly: an explicit empty value, false,
// no, off or 0. A bare key or anything else is not.
func (v configValue) isFalse() bool {
	if !v.assigned {
		return false
	}
	switch strings.ToLower(v.raw) {
	case "", "false", "no", "off", "0":
		return true
	}
	return false
}

// valueOf reads the value of a key line: everything after "=", up to an
// unquoted comment, trimmed, with surrounding double quotes removed.
func valueOf(line string) configValue {
	_, rest, ok := strings.Cut(line, "=")
	if !ok {
		return configValue{}
	}
	var b strings.Builder
	quoted := false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '"' {
			quoted = !quoted
			continue
		}
		if (c == '#' || c == ';') && !quoted {
			break
		}
		b.WriteByte(c)
	}
	return configValue{raw: strings.TrimSpace(b.String()), assigned: true}
}

// Config keys the trusted profile admits, by section. None of them names a
// program, a hook, a helper, another configuration file or another worktree.
var (
	allowedCore = setOf("repositoryformatversion", "filemode", "bare", "logallrefupdates", "ignorecase",
		"precomposeunicode", "symlinks", "autocrlf", "safecrlf", "eol", "quotepath", "checkstat", "trustctime",
		"abbrev", "compression", "loosecompression", "bigfilethreshold", "preloadindex", "untrackedcache",
		"splitindex", "multipackindex", "commitgraph", "sharedrepository", "warnambiguousrefs")
	allowedExtensions = setOf("objectformat", "worktreeconfig", "refstorage", "preciousobjects")
	allowedRemote     = setOf("url", "pushurl", "fetch", "push", "tagopt", "prune", "mirror", "skipdefaultupdate", "skipfetchall")
	allowedUser       = setOf("name", "email")
	// Sections whose every key is data.
	// gc and push are not here: gc.recentObjectsHook names a program, and
	// push.gpgSign runs gpg (independent review, N1).
	openSections = setOf("branch", "pack", "index", "feature", "color", "advice", "fetch", "pull")
)

func setOf(ks ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range ks {
		m[k] = true
	}
	return m
}

// allowedConfigKey decides one lower-cased "section[.subsection].name" key.
func allowedConfigKey(key string) bool {
	section, rest, _ := strings.Cut(key, ".")
	name := rest
	if i := strings.LastIndex(rest, "."); i >= 0 {
		name = rest[i+1:]
	}
	hasSub := strings.Contains(rest, ".")
	switch {
	case openSections[section]:
		return true
	case section == "core" && !hasSub:
		return allowedCore[name]
	case section == "extensions" && !hasSub:
		return allowedExtensions[name]
	case section == "remote" && hasSub:
		return allowedRemote[name]
	case section == "user" && !hasSub:
		return allowedUser[name]
	case key == "init.defaultbranch", key == "maintenance.auto":
		return true
	// Admitted only as false (falseOnlyKeys): signing off. The program that
	// would sign is gpg.*, which is refused, and this package never commits or
	// tags.
	case falseOnlyKeys[key]:
		return true
	}
	return false
}

// configKeys parses a git configuration file into its keys, as "section.name"
// or "section.subsection.name" with section and name lower-cased (subsections
// keep their case), each with the values assigned to it. A line it cannot read
// is a refusal: a key it cannot see is one it cannot vouch for.
func configKeys(path string, optional bool) (map[string][]configValue, error) {
	keys := map[string][]configValue{}
	body, err := readSmallFile(path)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return keys, nil
		}
		return nil, refuseProfile("the repository configuration %s cannot be read: %v", path, err)
	}
	section := ""
	for n, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				return nil, refuseProfile("%s:%d is not a section header", path, n+1)
			}
			header := strings.TrimSpace(line[1:end])
			name, sub, quoted := strings.Cut(header, " ")
			name = strings.ToLower(name)
			if quoted {
				sub = strings.TrimSpace(sub)
				if len(sub) < 2 || sub[0] != '"' || sub[len(sub)-1] != '"' {
					return nil, refuseProfile("%s:%d has a malformed subsection", path, n+1)
				}
				section = name + "." + sub[1:len(sub)-1]
			} else if strings.Contains(name, ".") {
				section = name // the deprecated [section.subsection] form
			} else {
				section = name
			}
			line = strings.TrimSpace(line[end+1:])
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
		}
		if section == "" {
			return nil, refuseProfile("%s:%d sets a key outside any section", path, n+1)
		}
		key := line
		if i := strings.IndexAny(line, "= \t"); i >= 0 {
			key = line[:i]
		}
		if key == "" || !isConfigName(key) {
			return nil, refuseProfile("%s:%d is not a configuration key", path, n+1)
		}
		// A value continued onto the next line is refused outright. Git carries
		// quote state across a continuation, so whether the next physical line
		// is more value or a new key depends on reading the whole value as git
		// does; a reader that guesses can be made to skip a key git then
		// applies (independent review, B1). No continued value is needed for
		// anything the profile admits.
		if continuesLine(line) {
			return nil, refuseProfile("%s:%d continues a value onto the next line, which the trusted git profile does not admit", path, n+1)
		}
		k := sectionKey(section) + "." + strings.ToLower(key)
		keys[k] = append(keys[k], valueOf(line))
	}
	return keys, nil
}

// continuesLine reports whether a value line ends in an unescaped backslash
// outside a comment, which is git's line continuation. An escaped backslash
// or a backslash inside a comment is not one. Within one line the quote state
// is git's own, so the answer agrees with git; configKeys refuses every line
// for which it is true.
func continuesLine(line string) bool {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\':
			if i == len(line)-1 {
				return true
			}
			i++
		case c == '"':
			quoted = !quoted
		case (c == '#' || c == ';') && !quoted:
			return false
		}
	}
	return false
}

// sectionKey lower-cases the section name and keeps a subsection as given.
func sectionKey(section string) string {
	name, sub, ok := strings.Cut(section, ".")
	if !ok {
		return strings.ToLower(section)
	}
	return strings.ToLower(name) + "." + sub
}

func isConfigName(s string) bool {
	for i, c := range s {
		alpha := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !alpha && (i == 0 || !(c >= '0' && c <= '9' || c == '-')) {
			return false
		}
	}
	return true
}
