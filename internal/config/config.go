package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/globulario/sensei-code/internal/behavioral"
)

type Permissions struct {
	ReadRepository  bool `json:"read_repository"`
	WriteCandidates bool `json:"write_candidates"`
	CreateWorktrees bool `json:"create_worktrees"`
	// RunFormatters is separate from builds and tests because a formatter can
	// rewrite the candidate. A check that mutates invalidates every piece of
	// evidence gathered before it, so it cannot be lumped in with the checks
	// that only certify.
	RunFormatters    bool `json:"run_formatters"`
	RunBuilds        bool `json:"run_builds"`
	RunTests         bool `json:"run_tests"`
	LocalCommit      bool `json:"local_commit"`
	Push             bool `json:"push"`
	ForcePush        bool `json:"force_push"`
	ProductionDeploy bool `json:"production_deploy"`
}

type Agent struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// Graph declares how this provider reaches the awareness graph. Empty or
	// "mcp": the engine binds it to the graph it verified, per provider, and
	// refuses to run a provider it cannot bind -- "unbound" must never look
	// like "bound". "none": the provider consumes no graph at all (a
	// deterministic stub, a tool with no MCP), so there is nothing to bind and
	// nothing to diverge. A declaration, so the exception is visible in config
	// rather than inferred from a command name.
	Graph string `json:"graph,omitempty"`
}

// ConsumesGraph reports whether the engine must bind this provider to the
// verified graph before running it.
func (a Agent) ConsumesGraph() bool {
	return !strings.EqualFold(strings.TrimSpace(a.Graph), "none")
}

type Workflow struct {
	ReviewCycles int `json:"review_cycles"`
	// PublishBase is the branch a pull request targets. Empty lets the host
	// choose its own default rather than Sensei Code guessing one.
	PublishBase string `json:"publish_base,omitempty"`
}

type Config struct {
	Sensei struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"sensei"`
	Architect    Agent   `json:"architect"`
	Implementors []Agent `json:"implementors"`
	// Reviewer is the first independent reviewer, and Reviewers is the bounded
	// set to fall back through when it cannot do the job.
	//
	// They are a separate roster from Implementors rather than the same list
	// reused, and the difference is not cosmetic: an implementor is configured
	// with write capability, so reviewing with an implementor's argv would hand
	// the reviewer a sandbox it can edit in. A read-only reviewer that is asked
	// to attack a candidate must not be able to fix it and report it clean.
	Reviewer    Agent             `json:"reviewer"`
	Reviewers   []Agent           `json:"reviewers,omitempty"`
	Workflow    Workflow          `json:"workflow"`
	Behavioral  behavioral.Config `json:"behavioral"`
	Permissions Permissions       `json:"permissions"`
	Validation  Validation        `json:"validation"`

	// Source records where this configuration came from. It is not persisted:
	// it is a fact about THIS load, not about the file.
	//
	// It exists because a diagnostic that cannot tell a repository's stated
	// choice from a built-in fallback will report the fallback as though the
	// repository had asserted it. That is how `doctor` came to announce that
	// this repository "certifies against" an address nothing here had ever
	// named, and prescribe a repair that pointed agents at another
	// repository's graph.
	Source Source `json:"-"`
}

// Source is the provenance of a loaded configuration.
type Source string

const (
	// FromRepository means .sensei-code/config.json supplied the values.
	FromRepository Source = "repository"
	// FromDefault means no configuration exists and the built-in values are in
	// use. A default is a starting point, never evidence about the repository.
	FromDefault Source = "default"
)

// SenseiAddressIsStated reports whether the Sensei endpoint in use was chosen
// for this repository, rather than inherited from the built-in default.
//
// Callers use it to decide whether a divergent agent entry is drift or merely
// unproven: without a stated address there is nothing to be drifting FROM.
func (c Config) SenseiAddressIsStated() bool { return c.Source == FromRepository }

func Default() Config {
	var c Config
	c.Source = FromDefault
	c.Sensei.Command = "awareness-mcp"
	c.Sensei.Args = []string{"--awareness-addr", "localhost:10120"}
	// ChatGPT is the first-version architectural authority, and it is the agent
	// the human talks to. The codex binary is retained only because its
	// app-server is the authenticated ChatGPT transport; agent.CLI does not
	// invoke a one-shot codex exec for this role.
	c.Architect = Agent{Name: "chatgpt", Command: "codex"}
	c.Implementors = []Agent{
		// --verbose is required: claude refuses --output-format=stream-json
		// without it under --print, so the worker exited 1 before doing any work.
		{Name: "claude", Command: "claude", Args: []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions"}},
		{Name: "codex", Command: "codex", Args: []string{"exec", "--sandbox", "workspace-write", "-"}},
	}
	c.Reviewer = Agent{Name: "codex", Command: "codex", Args: []string{"exec", "--sandbox", "read-only", "-"}}
	// The alternates exist so a reviewer that is out of quota, unreachable or
	// returning unparseable output costs a fallback rather than a person's
	// attention. Both are read-only: claude reviews in plan mode, which refuses
	// edits, for the same reason codex reviews in a read-only sandbox.
	c.Reviewers = []Agent{
		c.Reviewer,
		{Name: "claude", Command: "claude", Args: []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "plan"}},
	}
	c.Workflow = Workflow{ReviewCycles: 3}
	// Reporting is opt-in and unscoped by default: filing this repository's
	// outcomes against a guessed project or domain would corrupt somebody
	// else's principles.
	c.Behavioral = behavioral.Config{Enabled: false, Domain: "sensei_code"}
	c.Permissions = Permissions{
		ReadRepository:  true,
		WriteCandidates: true,
		CreateWorktrees: true,
		RunFormatters:   true,
		RunBuilds:       true,
		RunTests:        true,
		LocalCommit:     true,
	}
	c.Validation = defaultValidation()
	return c
}

// Validation is the set of checks a candidate must pass before a reviewer can
// accept it. They are configuration rather than hard-coded, because what proves
// a change in one repository proves nothing in another -- but they default to
// this project's own, which are the ones AGENTS.md already requires.
type Validation struct {
	// Format may rewrite the candidate and therefore runs first. Its own
	// evidence is discarded, because it describes the bytes before the rewrite.
	Format []Command `json:"format"`
	// FormatVerify proves formatting holds for the bytes that will actually be
	// reviewed. It must not mutate.
	//
	// This exists because discarding the formatter's evidence left the reviewer
	// unable to see that a mandated format check had run at all, and it refused
	// on those grounds -- correctly, and with no way for any worker to satisfy
	// it. A passing non-mutating verification bound to the final digest is also
	// simply better evidence than a record of a rewrite: it states that the
	// reviewed candidate is formatted, rather than that something once
	// reformatted something.
	FormatVerify []Command `json:"format_verify"`
	// Vet, Build and Test certify without mutating.
	Vet   []Command `json:"vet"`
	Build []Command `json:"build"`
	Test  []Command `json:"test"`
}

// ReviewRoster is the bounded set of independent reviewers, in fallback order.
//
// A configuration that names only the single Reviewer still gets a roster of
// one rather than an empty one: a deployment with no alternate is a real and
// supportable state, and it should fail by exhausting a bounded set rather than
// by finding no set at all.
func (c Config) ReviewRoster() []Agent {
	if len(c.Reviewers) != 0 {
		return c.Reviewers
	}
	if strings.TrimSpace(c.Reviewer.Name) == "" {
		return nil
	}
	return []Agent{c.Reviewer}
}

// Command is one executable check.
type Command struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	// FailIfOutput treats any output as failure even on a zero exit, for
	// verifiers that report by printing rather than by exit status.
	FailIfOutput bool `json:"fail_if_output,omitempty"`
}

func defaultValidation() Validation {
	return Validation{
		Format:       []Command{{Command: "gofmt", Args: []string{"-w", "cmd", "internal"}}},
		FormatVerify: []Command{{Command: "gofmt", Args: []string{"-l", "cmd", "internal"}, FailIfOutput: true}},
		Vet:          []Command{{Command: "go", Args: []string{"vet", "./..."}}},
		// -buildvcs=false because a candidate is a git worktree, and `go build`
		// tries to stamp VCS metadata into the binary from it. That fails with
		// "error obtaining VCS status: exit status 128" for reasons that have
		// nothing to do with whether the code compiles, so without this the
		// build check reports a failure no worker can fix and the review loop
		// never converges. Stamping is irrelevant to the question the check is
		// asking, so disabling it weakens nothing.
		Build: []Command{{Command: "go", Args: []string{"build", "-buildvcs=false", "./..."}}},
		Test:  []Command{{Command: "go", Args: []string{"test", "./..."}}},
	}
}

func Path(repo string) string { return filepath.Join(repo, ".sensei-code", "config.json") }

func Load(repo string) (Config, error) {
	p := Path(repo)
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		d := Default()
		d.Source = FromDefault
		return d, nil
	}
	if err != nil {
		return Config{}, err
	}
	c := Default()
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	// Migrate only the exact old built-in architect. A deliberately customized
	// architect remains user-owned configuration and is never rewritten.
	if isLegacyDefaultArchitect(c.Architect) {
		c.Architect = Default().Architect
	}
	if c.Workflow.ReviewCycles < 1 {
		c.Workflow.ReviewCycles = 1
	}
	c.Source = FromRepository
	return c, nil
}

func isLegacyDefaultArchitect(a Agent) bool {
	return a.Name == "codex" &&
		a.Command == "codex" &&
		reflect.DeepEqual(a.Args, []string{"exec", "--sandbox", "read-only", "-"})
}

func Save(repo string, c Config) error {
	p := Path(repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(p, b, 0o600)
}

// DisplayName renders an agent identifier for humans. The identifier itself is
// load-bearing -- output normalization and event routing match on it -- so it is
// never replaced by this label.
func DisplayName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "chatgpt":
		return "ChatGPT"
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	case "antigravity", "agy":
		return "Antigravity"
	case "grok":
		return "Grok"
	case "":
		return "agent"
	default:
		return name
	}
}
