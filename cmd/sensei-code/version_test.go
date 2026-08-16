package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunVersionPrintsVersion(t *testing.T) {
	var out bytes.Buffer
	if !runVersion([]string{"--version"}, &out) {
		t.Fatal("runVersion did not handle --version")
	}
	if got, want := out.String(), "sensei-code "+Version+"\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestRunVersionIgnoresOtherArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"doctor"}, {"init", "--version"}} {
		var out bytes.Buffer
		if runVersion(args, &out) {
			t.Fatalf("runVersion claimed %v", args)
		}
		if out.Len() != 0 {
			t.Fatalf("runVersion wrote %q for %v", out.String(), args)
		}
	}
}

// The version must be printable without a repository: it is answered before
// discovery and configuration loading, so it exits 0 anywhere.
func TestVersionFlagExitsZeroOutsideRepository(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sensei-code")
	// -buildvcs=false: the version is a constant, not a stamp, so the test must
	// not fail merely because the checkout's VCS metadata is unreadable.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build sensei-code: %v\n%s", err, out)
	}

	// t.TempDir() is outside any checkout, so gitx.Discover would fail here.
	cmd := exec.Command(bin, "--version")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("sensei-code --version: %v\nstderr: %s", err, stderr.String())
	}
	if got, want := stdout.String(), "sensei-code "+Version+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
