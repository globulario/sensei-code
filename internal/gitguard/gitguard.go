// Package gitguard turns the declared publication capabilities into something
// a worker process actually cannot do.
//
// The capability flags in .sensei-code/config.json read like guarantees. Until
// now push, force-push and deploy were only sentences in a worker's prompt, and
// a prompt is not an enforcement boundary: workers run with permissive provider
// sandboxes, so a mistaken `git push` would have succeeded. This installs a
// pre-push hook and points the worker's git at it through the environment, so
// the refusal happens in git rather than in a model's good intentions.
//
// It stops accidents, not a determined process: anything that can edit its own
// environment can route around it. That limit is stated rather than papered
// over, and the worktree branch remains the real blast-radius boundary.
package gitguard

import (
	"fmt"
	"os"
	"path/filepath"
)

// hook refuses every push and names why, so the worker reports a governance
// boundary rather than an unexplained git failure.
const hook = `#!/bin/sh
echo "sensei-code: push is refused; publication is human-owned and this capability is not granted" >&2
exit 1
`

// Install writes a hooks directory that refuses pushes and returns the
// environment entries that make a worker's git use it.
func Install(dir string) ([]string, error) {
	hooksDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(hooksDir, "pre-push")
	if err := os.WriteFile(path, []byte(hook), 0o755); err != nil {
		return nil, err
	}
	// GIT_CONFIG_* is process scoped. Setting core.hooksPath in the repository
	// instead would leak the refusal into the human's own checkout, which owns
	// the capability and must keep it.
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		fmt.Sprintf("GIT_CONFIG_VALUE_0=%s", hooksDir),
	}, nil
}
