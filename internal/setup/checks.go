package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// RequiredWorkspaceTools are the MCP tools Sensei Code cannot work without.
// They are absent from released Sensei packages that predate them, which is the
// single most confusing failure a new user hits: everything installs cleanly and
// the first task dies on "unknown tool".
var RequiredWorkspaceTools = []string{
	"sensei_workspace_status",
	"awareness_preflight",
	"awareness_briefing",
	"awareness_audit_diff",
}

// Options are what the checks need to know about this machine and repository.
type Options struct {
	RepoRoot string
	// Domain is the repository's graph domain, empty when not yet bound.
	Domain string
	// SenseiSourceDir is a Sensei checkout that can supply a current
	// awareness-mcp when the installed one is too old. Empty when unknown.
	SenseiSourceDir string
	// ToolNames are the tools the configured MCP server actually exposes.
	// Nil when the server could not be reached.
	ToolNames []string
	// GraphAddr is the awareness-graph address, for the reachability check.
	GraphAddr string
}

// InspectQuick runs only the checks that can be decided from the filesystem and
// PATH.
//
// The full inspection starts an MCP server and asks Sensei several questions,
// which costs seconds. That is right for `setup` and wrong on every launch of an
// interactive tool, so this reports the failures that make a session impossible
// and omits the rest rather than guessing at them. Omitting a check is honest;
// reporting it as passing would not be.
func InspectQuick(ctx context.Context, o Options) Report {
	var r Report
	r.Checks = append(r.Checks,
		checkSensei(ctx),
		checkMCPBinary(ctx, o),
		checkCorpus(ctx, o),
	)
	return r
}

// Inspect runs every readiness check.
func Inspect(ctx context.Context, o Options) Report {
	var r Report
	r.Checks = append(r.Checks,
		checkSensei(ctx),
		checkMCPBinary(ctx, o),
		checkWorkspaceTools(o),
		checkGraphServer(ctx, o),
		checkGraphFreshness(ctx, o),
		checkDomainRegistered(ctx, o),
		checkCorpus(ctx, o),
		checkAbandonedWorktrees(ctx, o),
	)
	return r
}

// checkSensei looks for the Sensei CLI, which everything else depends on.
func checkSensei(ctx context.Context) Check {
	c := Check{Name: "sensei installed"}
	path, err := exec.LookPath("sensei")
	if err != nil {
		c.State = Broken
		c.Detail = "not on PATH"
		c.Symptom = "every command reports that the sensei CLI is not installed"
		c.Fix = installCommand()
		return c
	}
	c.State = OK
	c.Detail = path
	if version := strings.TrimSpace(run(ctx, "", path, "version")); version != "" {
		c.Detail += " · " + firstLine(version)
	}
	return c
}

// installCommand names the packaged install for this platform rather than
// telling a user to build from source.
func installCommand() string {
	switch runtime.GOOS {
	case "darwin", "linux":
		return "brew install globulario/tap/sensei"
	case "windows":
		return "winget install Globulario.Sensei"
	default:
		return "install Sensei from https://github.com/globulario/sensei/releases"
	}
}

// checkMCPBinary reports which awareness-mcp answers, because two copies on one
// machine is normal once anyone has built from source, and PATH order silently
// decides which capability set the agents get.
func checkMCPBinary(ctx context.Context, o Options) Check {
	c := Check{Name: "awareness-mcp"}
	path, err := exec.LookPath("awareness-mcp")
	if err != nil {
		c.State = Broken
		c.Detail = "not on PATH"
		c.Symptom = "doctor cannot start the Sensei MCP server"
		c.Fix = installCommand()
		return c
	}
	c.State = OK
	c.Detail = path
	if others := otherCopies(path); len(others) != 0 {
		c.Detail += fmt.Sprintf(" (%d other cop%s on PATH; this one wins)", len(others), plural(len(others)))
	}
	return c
}

// otherCopies finds shadowed copies so a stale binary earlier on PATH is
// visible rather than being discovered hours later.
func otherCopies(winner string) []string {
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		candidate := filepath.Join(dir, "awareness-mcp")
		if candidate == winner {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			out = append(out, candidate)
		}
	}
	return out
}

// checkWorkspaceTools is the check that would have saved the most time. A
// released Sensei can be installed correctly and still lack the tools this needs.
func checkWorkspaceTools(o Options) Check {
	c := Check{Name: "required Sensei tools"}
	if o.ToolNames == nil {
		c.State = Unknown
		c.Detail = "the MCP server did not answer, so its tools are unknown"
		c.Symptom = "tasks fail with an MCP error before any work starts"
		return c
	}
	have := map[string]bool{}
	for _, name := range o.ToolNames {
		have[name] = true
	}
	var missing []string
	for _, name := range RequiredWorkspaceTools {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		c.State = OK
		c.Detail = fmt.Sprintf("%d tools, all required ones present", len(o.ToolNames))
		return c
	}
	c.State = Broken
	c.Detail = "missing " + strings.Join(missing, ", ")
	c.Symptom = `a task fails immediately with: unknown tool "` + missing[0] + `"`
	if o.SenseiSourceDir != "" {
		c.Fix = "build a current awareness-mcp from " + o.SenseiSourceDir + " and put it first on PATH"
		c.Repair = func(ctx context.Context) error { return buildAwarenessMCP(ctx, o.SenseiSourceDir) }
		return c
	}
	// Without a checkout there is nothing this tool can build, and guessing a
	// download would install an unverified binary.
	c.Fix = "the installed Sensei predates these tools: upgrade it (" + installCommand() +
		"), or clone github.com/globulario/sensei and build cmd/awareness-mcp"
	return c
}

// buildAwarenessMCP compiles a current server from a Sensei checkout and
// installs it ahead of the packaged one.
func buildAwarenessMCP(ctx context.Context, sourceDir string) error {
	target, err := userBinDir()
	if err != nil {
		return err
	}
	out := run(ctx, sourceDir, "go", "build", "-o", filepath.Join(target, "awareness-mcp"), "./cmd/awareness-mcp")
	if strings.Contains(strings.ToLower(out), "error") || strings.Contains(out, "cannot find") {
		return fmt.Errorf("build awareness-mcp: %s", firstLine(out))
	}
	if _, err := os.Stat(filepath.Join(target, "awareness-mcp")); err != nil {
		return fmt.Errorf("build produced no binary in %s", target)
	}
	return nil
}

func userBinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "bin")
	return dir, os.MkdirAll(dir, 0o755)
}

// checkGraphServer reports whether the awareness graph is answering.
func checkGraphServer(ctx context.Context, o Options) Check {
	c := Check{Name: "graph server"}
	out := run(ctx, o.RepoRoot, "sensei", "metadata")
	switch {
	case strings.Contains(out, "Live counts") || strings.Contains(out, "Server version"):
		c.State = OK
		c.Detail = "answering on " + fallback(o.GraphAddr, "localhost:10120")
	case strings.TrimSpace(out) == "":
		c.State = Unknown
		c.Detail = "no answer from the sensei CLI"
	default:
		c.State = Broken
		c.Detail = firstLine(out)
		c.Symptom = "every command fails with a connection error to the awareness graph"
		c.Fix = "sensei serve -no-seed &"
	}
	return c
}

// checkGraphFreshness catches the failure that blocks every task and explains
// nothing useful on its own.
//
// Any `sensei build --repo X` recomputes the whole-graph marker and writes it
// into X's marker file. Every other repository sharing the store then expects a
// marker the store no longer has, and every command fails closed with a digest
// nobody can place. It is the same repository-scoped rebuild that fixes it.
func checkGraphFreshness(ctx context.Context, o Options) Check {
	c := Check{Name: "graph freshness"}
	out := run(ctx, o.RepoRoot, "sensei", "metadata")
	switch {
	case strings.Contains(out, "Freshness state:     current"):
		c.State = OK
		c.Detail = "current"
	case strings.Contains(out, "stale"):
		c.State = Broken
		c.Detail = "stale: the live store does not carry the marker this server expects"
		c.Symptom = "every task fails with \"graph freshness stale\" and a digest that appears nowhere"
		if o.Domain != "" {
			c.Fix = "sensei build --repo " + o.Domain
			c.Repair = func(ctx context.Context) error {
				out := run(ctx, o.RepoRoot, "sensei", "build", "--repo", o.Domain)
				if !strings.Contains(out, "Build complete") {
					return fmt.Errorf("%s", firstLine(out))
				}
				return nil
			}
		} else {
			c.Fix = "republish a domain's slice with `sensei build --repo <domain>` to re-stamp the marker"
		}
	case strings.TrimSpace(out) == "":
		c.State = Unknown
		c.Detail = "the graph did not answer, so freshness is unknown"
	default:
		c.State = Unknown
		c.Detail = "freshness not reported"
	}
	return c
}

// domainRegistry is the file that binds a graph domain to the repository
// allowed to publish it. It lives outside every repository on purpose, which
// also means nothing in a fresh checkout hints that it exists.
func domainRegistry() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sensei", "domains.yaml"), nil
}

func checkDomainRegistered(ctx context.Context, o Options) Check {
	c := Check{Name: "domain registered"}
	if o.Domain == "" {
		c.State = Degraded
		c.Detail = "this checkout binds no repository domain"
		c.Symptom = "queries answer for the whole graph instead of only this repository"
		// The cause decides the fix, and "run init" is useless advice to someone
		// who already ran it. A domain is derived from the git origin, so a
		// checkout without one has nothing to derive from.
		if derived := domainFromOrigin(ctx, o.RepoRoot); derived != "" {
			c.Detail += " (git origin suggests " + derived + ")"
			c.Fix = "bind " + derived + " in .sensei/config.yaml"
			c.Repair = func(ctx context.Context) error { return bindDomain(o.RepoRoot, derived) }
			return c
		}
		c.Detail += " and it has no git remote to derive one from"
		c.Fix = "add a git remote, or set repository.domain in .sensei/config.yaml by hand"
		return c
	}
	path, err := domainRegistry()
	if err != nil {
		c.State = Unknown
		c.Detail = err.Error()
		return c
	}
	body, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(body), o.Domain+":") {
		c.State = OK
		c.Detail = o.Domain
		return c
	}
	c.State = Broken
	c.Detail = o.Domain + " is not in " + path
	c.Symptom = "publishing fails with PUBLICATION_REFUSED: domain is not registered"
	c.Fix = "register " + o.Domain + " in " + path
	c.Repair = func(ctx context.Context) error { return registerDomain(path, o.Domain) }
	return c
}

// registerDomain appends the domain binding. It only ever adds: the registry is
// deliberately outside the repository because it vouches for it, so rewriting
// somebody else's entry would undo exactly the separation it exists for.
func registerDomain(path, domain string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	body := string(existing)
	if strings.Contains(body, domain+":") {
		return nil
	}
	identity := domain
	if parts := strings.Split(domain, "/"); len(parts) >= 3 {
		identity = strings.Join(parts[len(parts)-2:], "/")
	}
	if strings.TrimSpace(body) == "" {
		body = "# Domain registry: binds each graph domain to the repository allowed to\n" +
			"# publish it. Deliberately outside any published repository.\ndomains:\n"
	}
	body = strings.TrimRight(body, "\n") + fmt.Sprintf(`

  %s:
    repository_identity: %s
    allowed_corpus_roots:
      - docs/awareness
    # allow_dirty_worktree is deliberately absent: commit the corpus, publish
    # the committed revision.
`, domain, identity)
	return os.WriteFile(path, []byte(body), 0o644)
}

// domainFromOrigin derives the graph domain from the git remote, in the
// host/path form Sensei requires. Sensei's own domain validator rejects a bare
// alias, so a derived value must not be a shortened one.
func domainFromOrigin(ctx context.Context, repoRoot string) string {
	url := strings.TrimSpace(run(ctx, repoRoot, "git", "remote", "get-url", "origin"))
	if url == "" {
		return ""
	}
	url = strings.TrimSuffix(strings.TrimSpace(firstLine(url)), ".git")
	switch {
	case strings.HasPrefix(url, "git@"):
		// git@github.com:owner/repo -> github.com/owner/repo
		url = strings.TrimPrefix(url, "git@")
		return strings.Replace(url, ":", "/", 1)
	case strings.Contains(url, "://"):
		_, rest, _ := strings.Cut(url, "://")
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		return rest
	}
	return ""
}

// bindDomain records the repository domain in this checkout's Sensei config.
func bindDomain(repoRoot, domain string) error {
	path := filepath.Join(repoRoot, ".sensei", "config.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	text := string(body)
	if strings.Contains(text, "domain: "+domain) {
		return nil
	}
	if strings.Contains(text, "repository:") {
		return fmt.Errorf("%s already has a repository section; set its domain by hand", path)
	}
	text = strings.TrimRight(text, "\n") + "\nrepository:\n    domain: " + domain + "\n"
	return os.WriteFile(path, []byte(text), 0o644)
}

// checkCorpus reports whether the repository has an awareness corpus at all.
func checkCorpus(ctx context.Context, o Options) Check {
	c := Check{Name: "awareness corpus"}
	dir := filepath.Join(o.RepoRoot, "docs", "awareness")
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.State = Broken
		c.Detail = "no docs/awareness in this repository"
		c.Symptom = "Sensei has nothing to say about any file, so every briefing is empty"
		c.Fix = "sensei init -mcp"
		c.Repair = func(ctx context.Context) error {
			out := run(ctx, o.RepoRoot, "sensei", "init", "-mcp")
			if strings.Contains(out, "Next steps") || strings.Contains(out, "Protection coverage") {
				return nil
			}
			return fmt.Errorf("%s", firstLine(out))
		}
		return c
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".yaml") {
			count++
		}
	}
	if count == 0 {
		c.State = Broken
		c.Detail = "docs/awareness holds no YAML sources"
		c.Fix = "sensei init -mcp"
		return c
	}
	c.State = OK
	c.Detail = fmt.Sprintf("%d source files", count)
	return c
}

// checkAbandonedWorktrees finds candidate checkouts git no longer tracks.
//
// They sit beside the repository, which is deliberate, but it also means a user
// finds unexplained directories next to their work and cannot tell which are
// live. Empty ones are removed. A non-empty one is named and left alone: an
// abandoned candidate can still hold unreviewed work, and deleting that to tidy
// up would throw away the very thing worth keeping.
func checkAbandonedWorktrees(ctx context.Context, o Options) Check {
	c := Check{Name: "candidate worktrees"}
	root := worktreeRoot(o.RepoRoot)
	entries, err := os.ReadDir(root)
	if err != nil {
		c.State = OK
		c.Detail = "none"
		return c
	}
	live := liveWorktrees(ctx, o.RepoRoot)
	var empty, holding []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			// Dot directories here are Sensei Code's own, not candidates.
			continue
		}
		path := filepath.Join(root, entry.Name())
		if live[path] {
			continue
		}
		if holdsOnlyToolArtefacts(path) {
			empty = append(empty, path)
			continue
		}
		holding = append(holding, path)
	}
	switch {
	case len(empty) == 0 && len(holding) == 0:
		c.State = OK
		c.Detail = fmt.Sprintf("%d live", len(live))
	case len(holding) != 0:
		c.State = Degraded
		c.Detail = fmt.Sprintf("%d abandoned candidate(s) still holding files, %d empty", len(holding), len(empty))
		c.Symptom = "unexplained directories beside your repository"
		c.Fix = "inspect and remove by hand: " + strings.Join(holding, ", ")
		if len(empty) != 0 {
			c.Repair = func(ctx context.Context) error { return removeAll(empty) }
			c.Fix = "remove the empty ones; the rest hold files: " + strings.Join(holding, ", ")
		}
	default:
		c.State = Degraded
		c.Detail = fmt.Sprintf("%d empty leftover director%s", len(empty), plural(len(empty)))
		c.Symptom = "unexplained empty directories beside your repository"
		c.Fix = "remove them"
		c.Repair = func(ctx context.Context) error { return removeAll(empty) }
	}
	return c
}

func worktreeRoot(repoRoot string) string {
	return filepath.Join(filepath.Dir(repoRoot), "."+filepath.Base(repoRoot)+"-worktrees")
}

// liveWorktrees are the checkouts git still tracks, which must never be removed.
func liveWorktrees(ctx context.Context, repoRoot string) map[string]bool {
	live := map[string]bool{}
	for _, line := range strings.Split(run(ctx, repoRoot, "git", "worktree", "list", "--porcelain"), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree "); ok {
			live[strings.TrimSpace(path)] = true
		}
	}
	return live
}

// toolArtefacts are directories Sensei Code puts beside a candidate for its own
// purposes. They are not the user's work, so a candidate holding nothing else is
// removable rather than being preserved as if it contained something.
var toolArtefacts = map[string]bool{".guard": true}

func holdsOnlyToolArtefacts(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !toolArtefacts[entry.Name()] {
			return false
		}
	}
	return true
}

func removeAll(paths []string) error {
	for _, p := range paths {
		if !holdsOnlyToolArtefacts(p) {
			// Refuse anything that gained real content between the check and the
			// repair: this deletes directories, and the check is the only thing
			// that established they held nothing worth keeping.
			return fmt.Errorf("%s now holds files; left alone", p)
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

func run(ctx context.Context, dir, name string, args ...string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	cmd := exec.CommandContext(ctx, path, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 140 {
		s = s[:139] + "…"
	}
	return s
}

func fallback(value, alternative string) string {
	if strings.TrimSpace(value) == "" {
		return alternative
	}
	return value
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// FindSenseiSource looks for a Sensei checkout beside this repository, which is
// the usual layout when someone works on both.
func FindSenseiSource(repoRoot string) string {
	candidates := []string{
		filepath.Join(filepath.Dir(repoRoot), "sensei"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "sensei"))
	}
	sort.Strings(candidates)
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "awareness-mcp")); err == nil {
			return dir
		}
	}
	return ""
}

// DecodeToolNames pulls the tool names out of an MCP tools/list payload.
func DecodeToolNames(raw json.RawMessage) []string {
	var payload struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	out := make([]string, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		if tool.Name != "" {
			out = append(out, tool.Name)
		}
	}
	return out
}
