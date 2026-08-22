package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/globulario/sensei-code/internal/sensei"
)

// The active task binding pins architectural context to one revision and one
// graph. When either moves, Sensei refuses the briefing:
//
//	task briefing refused: repair task binding before using architectural context
//
// That refusal is correct. The problem is what follows it. The architect
// proceeds without the briefing on every governed run, says so in one line
// inside a plan of several thousand characters, and the run looks healthy. On
// 2026-08-22 a binding pinned to an Aug 20 revision had been degrading every
// architect turn, and nothing reported it: doctor checked executables,
// providers, the MCP surface and the graph's own authority, but never whether
// the binding still matched the repository it binds.
//
// This reports it. It deliberately does NOT repair or clear anything. The
// binding is task custody, the bound task may be mid-flight, and discarding
// governed state to make a check go green is the move this repository exists to
// refuse.

// binding is the part of .sensei/tasks/active.yaml this check reads.
type binding struct {
	TaskID   string
	Revision string
	Graph    string
}

// readBinding extracts the active binding, or reports that there is none.
//
// Fields are matched by exact key prefix rather than parsed as YAML: this
// repository carries no YAML dependency, and a check that added one would cost
// more than it reports. A file that exists but carries none of the keys is
// reported as unreadable rather than treated as absent -- "no task is bound" and
// "the binding could not be read" are different facts, and only one of them is
// fine.
func readBinding(repo string) (b binding, present bool, err error) {
	data, err := os.ReadFile(filepath.Join(repo, ".sensei", "tasks", "active.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return binding{}, false, nil
		}
		return binding{}, false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "task_id:"):
			b.TaskID = strings.TrimSpace(strings.TrimPrefix(line, "task_id:"))
		case strings.HasPrefix(line, "revision:"):
			b.Revision = strings.TrimSpace(strings.TrimPrefix(line, "revision:"))
		case strings.HasPrefix(line, "graph_digest_sha256:"):
			b.Graph = strings.TrimSpace(strings.TrimPrefix(line, "graph_digest_sha256:"))
		}
	}
	if b.TaskID == "" {
		return binding{}, false, errUnreadableBinding
	}
	return b, true, nil
}

type bindingError string

func (e bindingError) Error() string { return string(e) }

const errUnreadableBinding bindingError = ".sensei/tasks/active.yaml exists but names no task"

// checkTaskBinding compares what the binding pins against what is live.
//
// head and graph are supplied rather than read here so the check states what it
// compared; a check that silently resolved its own comparands can report a
// mismatch nobody can reproduce.
func checkTaskBinding(repo, head, graph string) Check {
	const name = "sensei:task_binding"

	b, present, err := readBinding(repo)
	if err != nil {
		return Check{Name: name, Status: Warn, Detail: err.Error() +
			" — architectural briefings may be refused until it is repaired"}
	}
	if !present {
		// No bound task is the ordinary state between tasks, and briefings work.
		return Check{Name: name, Status: Pass, Detail: "no active task is bound"}
	}

	var drifted []string
	if head != "" && b.Revision != "" && b.Revision != head {
		drifted = append(drifted, "revision "+short(b.Revision)+" ≠ HEAD "+short(head))
	}
	if graph != "" && b.Graph != "" && b.Graph != graph {
		drifted = append(drifted, "graph "+short(b.Graph)+" ≠ live "+short(graph))
	}
	if len(drifted) == 0 {
		return Check{Name: name, Status: Pass, Detail: b.TaskID + " matches this checkout"}
	}
	// Fail rather than Warn. The consequence is not cosmetic: every architect
	// turn loses its briefing, and it loses it quietly.
	return Check{Name: name, Status: Fail, Detail: b.TaskID + " is stale (" +
		strings.Join(drifted, "; ") + ") — Sensei will refuse task briefings, so the architect plans without them. " +
		"Repair or complete the task; this check will not discard it for you"}
}

func short(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

// repositoryHead is this checkout's commit, or "" when it cannot be read.
//
// An unreadable head is not a mismatch: the comparison is simply skipped, so a
// checkout without git never produces a drift claim nobody can check.
func repositoryHead(ctx context.Context, repo string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// liveGraphDigest asks Sensei what it is serving. Same rule: unknown means the
// comparison is skipped, not that it failed.
func liveGraphDigest(client *sensei.Client) string {
	if client == nil {
		return ""
	}
	res, err := client.CallTool("awareness_metadata", map[string]any{})
	if err != nil || res.Structured == nil {
		return ""
	}
	for _, key := range []string{"live_digest_sha256", "live_digest", "graph_digest_sha256"} {
		if v, ok := res.Structured[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
