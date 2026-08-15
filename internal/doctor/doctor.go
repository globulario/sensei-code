package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/sensei"
)

type Status string

const (
	Pass Status = "PASS"
	Fail Status = "FAIL"
)

type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type Report struct {
	Checks []Check `json:"checks"`
}

func (r Report) OK() bool {
	for _, check := range r.Checks {
		if check.Status != Pass {
			return false
		}
	}
	return true
}

// Run verifies the local execution surface Sensei Code depends on. It does not
// authenticate on the user's behalf and it does not mutate the repository.
func Run(ctx context.Context, repo string, cfg config.Config) Report {
	report := Report{}
	commands := configuredCommands(cfg)
	for _, command := range commands {
		path, err := exec.LookPath(command)
		if err != nil {
			report.Checks = append(report.Checks, Check{Name: "executable:" + command, Status: Fail, Detail: err.Error()})
			continue
		}
		report.Checks = append(report.Checks, Check{Name: "executable:" + command, Status: Pass, Detail: path})
	}

	if _, err := exec.LookPath(cfg.Sensei.Command); err != nil {
		return report
	}
	client, err := sensei.Start(ctx, repo, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "sensei:mcp", Status: Fail, Detail: err.Error()})
		return report
	}
	defer client.Close()

	raw, err := client.ListTools()
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "sensei:mcp", Status: Fail, Detail: err.Error()})
		return report
	}
	tools, err := toolNames(raw)
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "sensei:mcp", Status: Fail, Detail: err.Error()})
		return report
	}
	report.Checks = append(report.Checks, Check{Name: "sensei:mcp", Status: Pass, Detail: fmt.Sprintf("%d tools", len(tools))})

	have := make(map[string]bool, len(tools))
	for _, name := range tools {
		have[name] = true
	}
	for _, name := range requiredSenseiTools {
		status := Pass
		detail := "available"
		if !have[name] {
			status = Fail
			detail = "required tool missing"
		}
		report.Checks = append(report.Checks, Check{Name: "sensei:" + name, Status: status, Detail: detail})
	}
	return report
}

var requiredSenseiTools = []string{
	"sensei_workspace_status",
	"awareness_preflight",
	"awareness_audit_diff",
	"sensei_workspace_admit_change",
	"sensei_workspace_verify_admission",
	"complete_task",
	"inspect_terminal",
}

func configuredCommands(cfg config.Config) []string {
	seen := map[string]bool{"git": true}
	commands := []string{"git"}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			commands = append(commands, s)
		}
	}
	add(cfg.Sensei.Command)
	add(cfg.Architect.Command)
	for _, a := range cfg.Implementors {
		add(a.Command)
	}
	add(cfg.Reviewer.Command)
	sort.Strings(commands)
	return commands
}

func toolNames(raw json.RawMessage) ([]string, error) {
	var payload struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}
	out := make([]string, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		if tool.Name != "" {
			out = append(out, tool.Name)
		}
	}
	return out, nil
}
