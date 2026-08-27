package gitx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoWithBase(t *testing.T) (Repo, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"a\")\n}\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "base")
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	return Repo{Root: dir}, strings.TrimSpace(string(out))
}

// Regression for #89: two lines of source, plus a >5 MB compiled artifact left
// in the worktree. The artifact cannot silently enter an ordinary candidate,
// and the two lines stay readable.
func TestABuildArtifactCannotSilentlyEnterASourceEditCandidate(t *testing.T) {
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"b\")\n\tprintln(\"c\")\n}\n"), 0o644)
	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0x00, 0xff, 0x13}, 2*1024*1024)...) // ~6 MB
	os.WriteFile(filepath.Join(r.Root, "main"), elf, 0o755)

	cap, err := r.CandidateCapture(context.Background(), base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cap.Excluded) != 1 || cap.Excluded[0].Path != "main" || cap.Excluded[0].Class != "binary" || !cap.Excluded[0].Excluded {
		t.Fatalf("the artifact was not excluded and named: %+v", cap.Excluded)
	}
	if cap.Excluded[0].Size < 5<<20 {
		t.Fatalf("the specimen must exceed the 5 MiB audit limit; got %d", cap.Excluded[0].Size)
	}
	if !strings.Contains(cap.Diff, `+	println("c")`) || strings.Contains(cap.Diff, "ELF") || strings.Contains(cap.Diff, "Binary files") {
		t.Fatalf("the text change is not readable on its own:\n%s", cap.Diff)
	}
	if len(cap.Diff) > 4<<10 {
		t.Fatalf("the transported diff is %d bytes; a two-line change must stay bounded", len(cap.Diff))
	}
	// And the exclusion is real: the artifact is no longer intended for the index.
	out, _ := exec.Command("git", "-C", r.Root, "status", "--porcelain", "--", "main").Output()
	if !strings.HasPrefix(string(out), "??") {
		t.Fatalf("the artifact is still recorded in the index: %q", out)
	}
}

// A binary the plan NAMES is the change, kept as metadata rather than as bytes.
func TestAnIntendedBinaryIsKeptAsMetadataNotTransported(t *testing.T) {
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "asset.bin"), bytes.Repeat([]byte{0, 1, 2, 0xff}, 4096), 0o644)
	cap, err := r.CandidateCapture(context.Background(), base, []string{"asset.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cap.Excluded) != 0 || len(cap.Binaries) != 1 || cap.Binaries[0].Path != "asset.bin" {
		t.Fatalf("an intended binary was mishandled: excluded=%+v binaries=%+v", cap.Excluded, cap.Binaries)
	}
	if !strings.Contains(cap.Diff, "Binary files") || strings.Contains(cap.Diff, "GIT binary patch") {
		t.Fatalf("the intended binary must appear as a marker, never as a patch:\n%s", cap.Diff[:min(len(cap.Diff), 400)])
	}
}

// A new oversized TEXT file is an artifact too: size can blind governance as
// effectively as kind.
func TestANewOversizedTextFileIsAnArtifactUnlessIntended(t *testing.T) {
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "graph.nt"), bytes.Repeat([]byte("<a> <b> <c> .\n"), 300000), 0o644) // ~4 MB
	cap, err := r.CandidateCapture(context.Background(), base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cap.Excluded) != 1 || cap.Excluded[0].Class != "oversized" {
		t.Fatalf("oversized generated text was not excluded: %+v", cap.Excluded)
	}
	cap2, _ := r.CandidateCapture(context.Background(), base, []string{"graph.nt"})
	if len(cap2.Excluded) != 0 {
		t.Fatalf("an intended oversized file was excluded: %+v", cap2.Excluded)
	}
}
