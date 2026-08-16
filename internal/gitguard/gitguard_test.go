package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuardActuallyRefusesAPush drives a real push against a real remote. The
// point is not that the hook file exists but that git declines.
func TestGuardActuallyRefusesAPush(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")

	run := func(env []string, dir string, args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if len(env) != 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run(nil, root, "init", "--bare", "-q", remote); err != nil {
		t.Fatalf("init remote: %v %s", err, out)
	}
	if out, err := run(nil, root, "clone", "-q", remote, work); err != nil {
		t.Skipf("git clone unavailable in this environment: %v %s", err, out)
	}
	for _, args := range [][]string{
		{"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "T"},
	} {
		if out, err := run(nil, work, args...); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(nil, work, "add", "f.txt"); err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if out, err := run(nil, work, "commit", "-q", "-m", "c"); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}

	// Without the guard the push succeeds, which proves the test can push at all.
	if out, err := run(nil, work, "push", "-q", "origin", "HEAD"); err != nil {
		t.Fatalf("baseline push failed, so the guard test would prove nothing: %v %s", err, out)
	}

	env, err := Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "g.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(nil, work, "add", "g.txt"); err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if out, err := run(nil, work, "commit", "-q", "-m", "d"); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	out, err := run(env, work, "push", "origin", "HEAD")
	if err == nil {
		t.Fatal("the push guard did not refuse a push")
	}
	if !strings.Contains(out, "publication is human-owned") {
		t.Fatalf("push failed for the wrong reason: %s", out)
	}
}

func TestGuardDoesNotLiveInTheDirectoryItProtects(t *testing.T) {
	// Installed inside a candidate worktree the hook appears in the worker's
	// own status and can be committed into the change it protects.
	candidate := t.TempDir()
	guard := filepath.Join(filepath.Dir(candidate), ".guard")
	if _, err := Install(guard); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the guard wrote %d entries into the protected directory", len(entries))
	}
}
