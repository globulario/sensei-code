package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/mcpconfig"
	"github.com/globulario/sensei-code/internal/sensei"
)

// InspectRepository gathers the machine and repository state the checks need
// and returns the readiness report.
//
// It observes and nothing else. The checks it returns still carry their repairs,
// because the same report is what `sensei-code setup --apply` acts on, but
// producing the report never runs one: a caller that only wants to know what is
// wrong must be able to ask without a shared graph, a home-directory registry or
// an agent's MCP configuration being rewritten underneath it.
func InspectRepository(ctx context.Context, repoRoot string, cfg config.Config) Report {
	options := Options{
		RepoRoot:        repoRoot,
		SenseiSourceDir: FindSenseiSource(repoRoot),
		GraphAddr:       graphAddr(cfg),
	}
	// The tool list and the domain both come from the MCP server, and a server
	// that will not start is itself a finding rather than a reason to stop.
	if client, err := sensei.Start(ctx, repoRoot, cfg.Sensei.Command, cfg.Sensei.Args); err == nil {
		if raw, err := client.ListTools(); err == nil {
			options.ToolNames = DecodeToolNames(raw)
		}
		if status, err := client.CallTool("sensei_workspace_status", map[string]any{"repo": repoRoot}); err == nil {
			options.Domain = sensei.RepositoryDomain(status)
		}
		client.Close()
	}
	if options.Domain == "" {
		options.Domain = configuredDomain(repoRoot)
	}

	report := Inspect(ctx, options)
	// Agent access is part of readiness: a registered server whose tools an
	// agent cannot call is not access to Sensei.
	for _, access := range mcpconfig.Describe(repoRoot, cfg.Sensei.Args, cfg.SenseiAddressIsStated()) {
		report.Checks = append(report.Checks, mcpCheck(repoRoot, cfg, access))
	}
	return report
}

func mcpCheck(repoRoot string, cfg config.Config, access mcpconfig.Status) Check {
	c := Check{Name: "mcp: " + string(access.Agent), Detail: access.Detail}
	switch access.State {
	case mcpconfig.Configured:
		c.State = OK
	case mcpconfig.Unproven:
		// Degraded, with no Repair. A repair here would rewrite an entry that
		// may well be the correct one, on the authority of a default.
		c.State = Degraded
		c.Detail = access.Detail
		c.Symptom = "nothing establishes which graph is authoritative for this repository, so neither address can be called wrong"
		c.Fix = "state the endpoint in .sensei-code/config.json, then re-run"
	case mcpconfig.Unknown:
		c.State = Degraded
		c.Detail = access.Detail
	default:
		c.State = Degraded
		c.Symptom = "the agent's Sensei tool calls are cancelled, and it reports Sensei as unavailable"
		c.Fix = "sensei-code mcp " + string(access.Agent)
		agent := access.Agent
		c.Repair = func(ctx context.Context) error {
			_, err := mcpconfig.Configure(repoRoot, agent, cfg.Sensei.Command, cfg.Sensei.Args, cfg.SenseiAddressIsStated())
			return err
		}
	}
	return c
}

// configuredDomain reads the domain this checkout binds, for the case where the
// MCP server cannot answer yet.
func configuredDomain(repoRoot string) string {
	body, err := os.ReadFile(filepath.Join(repoRoot, ".sensei", "config.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		if _, value, ok := strings.Cut(strings.TrimSpace(line), "domain:"); ok {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

func graphAddr(cfg config.Config) string {
	for i, arg := range cfg.Sensei.Args {
		if arg == "--awareness-addr" && i+1 < len(cfg.Sensei.Args) {
			return cfg.Sensei.Args[i+1]
		}
	}
	return ""
}

// InspectQuickRepository is the launch-time check for one repository: only what
// can be decided without asking Sensei anything, so starting the tool stays
// instant. It lives beside InspectRepository so both entry points resolve the
// same options the same way.
func InspectQuickRepository(ctx context.Context, repoRoot string, cfg config.Config) Report {
	return InspectQuick(ctx, Options{
		RepoRoot:  repoRoot,
		GraphAddr: graphAddr(cfg),
	})
}
