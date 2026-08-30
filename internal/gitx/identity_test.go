package gitx

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTheExactPathSetSurvivesAPathnameGitQuotes is the seam this slice pulled
// forward: the canonical tree stages exactly these paths, so a lossy set would
// build an identity that is exact about the wrong tree.
//
// `--numstat` without -z is trimmed and split on tabs, and a pathname holding a
// tab or a newline is quoted by Git -- so the old derivation lost it or split
// it in half. Nothing here trims, splits or unquotes.
func TestTheExactPathSetSurvivesAPathnameGitQuotes(t *testing.T) {
	r, base := repoWithBase(t)
	awkward := "a file\twith a tab.txt"
	if err := os.WriteFile(filepath.Join(r.Root, awkward), []byte("x\n"), 0o644); err != nil {
		t.Skipf("this filesystem will not hold %q: %v", awkward, err)
	}
	os.WriteFile(filepath.Join(r.Root, "plain.txt"), []byte("y\n"), 0o644)

	cap, err := r.CandidateCapture(context.Background(), base, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawAwkward, sawPlain bool
	for _, p := range cap.Paths {
		switch p {
		case awkward:
			sawAwkward = true
		case "plain.txt":
			sawPlain = true
		}
		if strings.Contains(p, `\t`) || strings.HasPrefix(p, `"`) {
			t.Errorf("path %q arrived quoted or escaped; the set must be Git's own bytes", p)
		}
	}
	if !sawPlain {
		t.Fatalf("the ordinary path is missing from %q", cap.Paths)
	}
	if !sawAwkward {
		t.Fatalf("the path containing a tab did not survive: %q", cap.Paths)
	}
}

func TestARenameArrivesAsADeletionAndAnAddition(t *testing.T) {
	r, base := repoWithBase(t)
	if err := os.Rename(filepath.Join(r.Root, "main.go"), filepath.Join(r.Root, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	cap, err := r.CandidateCapture(context.Background(), base, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(cap.Paths, " ")
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "renamed.go") {
		t.Fatalf("a rename must appear as both paths, got %q", cap.Paths)
	}
}

// The canonical tree is the base tree plus exactly the reviewed paths: a
// worker's scratch file cannot ride along.
func TestTheCanonicalTreeHoldsExactlyTheReviewedPaths(t *testing.T) {
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(r.Root, "scratch.txt"), []byte("a worker's note\n"), 0o644)

	tree, err := r.CanonicalTree(context.Background(), base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", r.Root, "ls-tree", "-r", "--name-only", tree).Output()
	if err != nil {
		t.Fatal(err)
	}
	listed := strings.Fields(string(out))
	for _, p := range listed {
		if p == "scratch.txt" {
			t.Fatal("a path the reviewer never saw entered the canonical tree")
		}
	}
	if len(listed) != 1 || listed[0] != "main.go" {
		t.Fatalf("tree holds %q, want exactly main.go", listed)
	}
	// And the live index is untouched: the tree was built elsewhere.
	if st, _ := exec.Command("git", "-C", r.Root, "diff", "--cached", "--name-only").Output(); len(strings.TrimSpace(string(st))) != 0 {
		t.Errorf("the canonical tree staged into the live index: %q", st)
	}
}

func TestADeletedPathIsRecordedAsADeletionNotRetainedFromTheBase(t *testing.T) {
	r, base := repoWithBase(t)
	if err := os.Remove(filepath.Join(r.Root, "main.go")); err != nil {
		t.Fatal(err)
	}
	tree, err := r.CanonicalTree(context.Background(), base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "-C", r.Root, "ls-tree", "-r", "--name-only", tree).Output()
	if strings.Contains(string(out), "main.go") {
		t.Fatal("a deleted path was retained from the base tree")
	}
}

// TestTheCanonicalCommitIsAFunctionOfItsInputs is the proof the whole design
// rests on: an author string can be forged, a reconstruction cannot.
func TestTheCanonicalCommitIsAFunctionOfItsInputs(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.MintCanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}

	// Hostile local differences must not reach the object.
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", r.Root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("config", "user.name", "Somebody Else")
	run("config", "user.email", "somebody@example.com")
	run("config", "commit.gpgsign", "false")
	t.Setenv("TZ", "Pacific/Kiritimati")
	t.Setenv("GIT_AUTHOR_NAME", "Injected")
	t.Setenv("GIT_AUTHOR_EMAIL", "injected@example.com")
	t.Setenv("GIT_AUTHOR_DATE", "2001-02-03T04:05:06 +0900")
	t.Setenv("GIT_COMMITTER_DATE", "2001-02-03T04:05:06 +0900")

	second, err := r.MintCanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("the identity moved with the environment: %s then %s", short12(first), short12(second))
	}
	if err := r.VerifyCanonicalCommit(ctx, base, tree, first); err != nil {
		t.Fatalf("reconstruction rejected the object it produced: %v", err)
	}
}

func TestTheCanonicalCommitsFirstParentIsExactlyTheBase(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.MintCanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := r.FirstParentOf(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if parent != base {
		t.Fatalf("first parent = %s, want exactly the base %s", short12(parent), short12(base))
	}
	got, err := r.CommitTreeOf(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got != tree {
		t.Fatalf("tree = %s, want %s", short12(got), short12(tree))
	}
}

// A worker's own commits must not determine the identity: the canonical object
// is parented on the base, whatever history the worker left behind.
func TestWorkerHistoryDoesNotDetermineTheIdentity(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", r.Root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "w1")
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "w2")

	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.MintCanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := r.FirstParentOf(ctx, c)
	if parent != base {
		t.Fatalf("first parent = %s: worker history determined the identity", short12(parent))
	}
	head, _ := r.Head(ctx)
	if c == head {
		t.Fatal("the canonical identity is the worker's tip; it must be its own object")
	}
}

// A forged author string survives inspection. It does not survive
// reconstruction.
func TestAForgedIdentityDoesNotSurviveReconstruction(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", r.Root, "commit-tree", tree, "-p", base, "-m", "sensei-code candidate identity")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+CanonicalName, "GIT_AUTHOR_EMAIL="+CanonicalEmail,
		"GIT_COMMITTER_NAME="+CanonicalName, "GIT_COMMITTER_EMAIL="+CanonicalEmail,
		"GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_DATE=1700000000 +0000")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.TrimSpace(string(out))
	if err := r.VerifyCanonicalCommit(ctx, base, tree, forged); err == nil {
		t.Fatal("an object wearing the canonical authorship passed verification; authorship is not the proof")
	}
}

func TestACanonicalTreeRefusesAnEmptyCandidate(t *testing.T) {
	r, base := repoWithBase(t)
	if _, err := r.CanonicalTree(context.Background(), base, nil); err == nil {
		t.Fatal("an empty path set produced a tree; a run that changed nothing is not a candidate")
	}
}

// TestTheCanonicalSerializationIsPinned is the fixed test vector.
//
// The other tests prove this implementation agrees with itself. This one pins
// the exact bytes and the exact object id, so a stray newline, a reordered
// header or a changed message makes a visible failure rather than a silently
// different identity for every candidate ever minted afterwards.
func TestTheCanonicalSerializationIsPinned(t *testing.T) {
	const (
		base  = "1111111111111111111111111111111111111111"
		tree  = "2222222222222222222222222222222222222222"
		stamp = "1700000001 +0000"
		want  = "tree 2222222222222222222222222222222222222222\n" +
			"parent 1111111111111111111111111111111111111111\n" +
			"author Sensei Code Candidate Identity <candidate-identity@sensei-code.invalid> 1700000001 +0000\n" +
			"committer Sensei Code Candidate Identity <candidate-identity@sensei-code.invalid> 1700000001 +0000\n" +
			"\n" +
			"sensei-code candidate identity\n\nbase: 1111111111111111111111111111111111111111\ntree: 2222222222222222222222222222222222222222\n"
		wantSHA1 = "44a62d723545fa84101419632ec6cb93d3b62717"
	)
	got := string(CanonicalCommitBytes(base, tree, stamp))
	if got != want {
		t.Fatalf("the canonical serialization moved.\n got: %q\nwant: %q", got, want)
	}
	sum := sha1.Sum([]byte("commit " + strconv.Itoa(len(want)) + "\x00" + want))
	if hex.EncodeToString(sum[:]) != wantSHA1 {
		t.Fatalf("object id = %s, pinned %s", hex.EncodeToString(sum[:]), wantSHA1)
	}
}

// A verifier must be able to run against a repository it does not modify.
func TestVerificationWritesNothing(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := r.ExpectedCanonicalSHA(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}
	// The object must NOT be in the store yet: computing an identity is not
	// creating one.
	if err := exec.Command("git", "-C", r.Root, "cat-file", "-e", expected+"^{object}").Run(); err == nil {
		t.Fatal("computing the expected identity wrote the object; verification must not mint what it verifies")
	}
	// Verification against a not-yet-minted identity is a mismatch, not a write.
	if err := r.VerifyCanonicalCommit(ctx, base, tree, strings.Repeat("0", 40)); err == nil {
		t.Fatal("verification accepted a wrong object")
	}
	if err := exec.Command("git", "-C", r.Root, "cat-file", "-e", expected+"^{object}").Run(); err == nil {
		t.Fatal("verification wrote the reconstructed object into the store")
	}
	// Minting is the separate, deliberate act.
	minted, err := r.MintCanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}
	if minted != expected {
		t.Fatalf("minted %s, expected %s", short12(minted), short12(expected))
	}
	if err := exec.Command("git", "-C", r.Root, "cat-file", "-e", minted+"^{object}").Run(); err != nil {
		t.Fatalf("the minted object is not in the store: %v", err)
	}
}

// The artifact boundary decides which paths are excluded, so IT must read
// pathnames exactly too: an exact path set downstream cannot repair a boundary
// decision already made against a mangled name.
func TestTheArtifactBoundaryExcludesAnAwkwardlyNamedArtifactByItsExactName(t *testing.T) {
	r, base := repoWithBase(t)
	awkward := "build\toutput.bin"
	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0x00, 0xff, 0x13}, 1024)...)
	if err := os.WriteFile(filepath.Join(r.Root, awkward), elf, 0o644); err != nil {
		t.Skipf("this filesystem will not hold %q: %v", awkward, err)
	}
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)

	cap, err := r.CandidateCapture(context.Background(), base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cap.Excluded) != 1 {
		t.Fatalf("excluded = %+v, want exactly the awkward artifact", cap.Excluded)
	}
	if cap.Excluded[0].Path != awkward {
		t.Fatalf("excluded path = %q, want the exact pathname %q", cap.Excluded[0].Path, awkward)
	}
	for _, p := range cap.Paths {
		if p == awkward {
			t.Fatal("the excluded artifact remained in the reviewed path set")
		}
	}
}

// TestAPathspecMagicFilenameIsALiteralPath is the last hop of the pathname
// repair: exact bytes are not enough if the receiver may read them as a
// pattern.
//
// A file genuinely named ":(glob)*" survives capture intact, and `git add`
// would then read it as pathspec magic and stage everything -- including a
// scratch file the reviewer never named. GIT_LITERAL_PATHSPECS closes it.
func TestAPathspecMagicFilenameIsALiteralPath(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	magic := ":(glob)*"
	if err := os.WriteFile(filepath.Join(r.Root, magic), []byte("reviewed\n"), 0o644); err != nil {
		t.Skipf("this filesystem will not hold %q: %v", magic, err)
	}
	os.WriteFile(filepath.Join(r.Root, "secret.txt"), []byte("never reviewed\n"), 0o644)

	tree, err := r.CanonicalTree(ctx, base, []string{magic})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", r.Root, "ls-tree", "-r", "-z", "--name-only", tree).Output()
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			listed = append(listed, p)
		}
	}
	var sawMagic bool
	for _, p := range listed {
		if p == "secret.txt" {
			t.Fatal("a pathspec-magic filename globbed an unreviewed file into the canonical tree")
		}
		if p == magic {
			sawMagic = true
		}
	}
	if !sawMagic {
		t.Fatalf("the literal path %q is missing from the tree: %q", magic, listed)
	}
}

// The artifact boundary hands measured pathnames back to `git reset`, so a
// magic-looking artifact must not reset paths it never classified.
func TestAMagicNamedArtifactCannotResetUnrelatedPaths(t *testing.T) {
	r, base := repoWithBase(t)
	// The pattern must be one that WOULD capture the unrelated file, or the
	// test passes with or without the guard and proves nothing. ":(glob)*.bin"
	// was that mistake: it globs only .bin files, so kept.txt survived either
	// way. ":(glob)*" matches everything.
	magic := ":(glob)*"
	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0x00, 0xff, 0x13}, 1024)...)
	if err := os.WriteFile(filepath.Join(r.Root, magic), elf, 0o644); err != nil {
		t.Skipf("this filesystem will not hold %q: %v", magic, err)
	}
	os.WriteFile(filepath.Join(r.Root, "kept.txt"), []byte("real candidate work\n"), 0o644)

	cap, err := r.CandidateCapture(context.Background(), base, nil)
	if err != nil {
		t.Fatal(err)
	}
	var keptPresent, magicPresent bool
	for _, p := range cap.Paths {
		switch p {
		case "kept.txt":
			keptPresent = true
		case magic:
			magicPresent = true
		}
	}
	if !keptPresent {
		t.Fatalf("resetting the magic-named artifact removed unrelated work: %q", cap.Paths)
	}
	if magicPresent {
		t.Fatalf("the excluded artifact stayed in the reviewed set: %q", cap.Paths)
	}
	if len(cap.Excluded) != 1 || cap.Excluded[0].Path != magic {
		t.Fatalf("excluded = %+v, want exactly %q", cap.Excluded, magic)
	}
}

// The parsers are identity infrastructure: a record they cannot model is a
// pathname they cannot vouch for.
func TestTheNameStatusParserFailsClosed(t *testing.T) {
	if _, err := parseNameStatusZ("A\x00one\x00M\x00"); err == nil {
		t.Fatal("an odd field count parsed as if it were pairs")
	}
	if _, err := parseNameStatusZ("A\x00\x00"); err == nil {
		t.Fatal("an empty pathname parsed as a path")
	}
	got, err := parseNameStatusZ("A\x00one\x00M\x00two\x00")
	if err != nil {
		t.Fatalf("a well-formed stream was rejected: %v", err)
	}
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("paths = %q", got)
	}
	if got, err := parseNameStatusZ(""); err != nil || len(got) != 0 {
		t.Fatalf("an empty diff = %q, %v", got, err)
	}
}

// TestTheDiffIsRenderedFromTheTreeNotASecondLookAtTheWorktree is the Step 2
// ordering: one measurement, and a representation OF it.
//
// Two sequential reads of a mutable worktree leave a seam a later equality
// check cannot always close -- the review text renders a kept binary as
// "Binary files differ", so different binary content can produce an identical
// diff while the tree moves underneath it.
func TestTheDiffIsRenderedFromTheTreeNotASecondLookAtTheWorktree(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() { println(2) }\n"), 0o644)

	cap, err := r.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cap.Tree == "" {
		t.Fatal("a capture with a base must carry a content identity")
	}
	// The diff must be exactly what the bound tree says, not what the worktree
	// says now: mutating the worktree after the capture cannot change it.
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() { println(999) }\n"), 0o644)
	rendered, err := r.raw(ctx, "diff", "--no-ext-diff", base, cap.Tree, "--")
	if err != nil {
		t.Fatal(err)
	}
	if rendered != cap.Diff {
		t.Fatal("the captured diff is not a rendering of the captured tree")
	}
	if strings.Contains(cap.Diff, "999") {
		t.Fatal("the captured diff followed the worktree after the capture")
	}
}

// A candidate that changed nothing has the base's own tree. "Produced no work"
// is then a MEASUREMENT, not an inference from an empty diff.
func TestACandidateThatChangedNothingHasTheBaseTree(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	cap, err := r.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseTree, err := r.CommitTreeOf(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if cap.Tree != baseTree {
		t.Fatalf("tree = %s, want the base tree %s", short12(cap.Tree), short12(baseTree))
	}
	if len(cap.Paths) != 0 || cap.Diff != "" {
		t.Fatalf("paths = %q diff = %q", cap.Paths, cap.Diff)
	}
}

// TestDifferentBinaryContentAlwaysMovesTheTree states the property T actually
// guarantees, unconditionally.
//
// A note on the argument for it. The binary corner is weaker than it first
// appears: `git diff` renders a changed binary as an `index <old>..<new>` line
// followed by "Binary files ... differ", so the abbreviated blob hashes DO
// distinguish two different binaries in the review text. Measured, not assumed.
//
// The ordering change does not rest on that corner. It rests on removing the
// seam between two sequential reads of a mutable worktree: with the diff
// rendered FROM the tree there is one measurement and one representation of it,
// and no instant in between for the content to move. The binary case remains a
// reason to prefer a full tree hash over abbreviated ones in a rendering, which
// is a weaker claim than the one that motivated the change.
func TestDifferentBinaryContentAlwaysMovesTheTree(t *testing.T) {
	ctx := context.Background()
	r, _ := repoWithBase(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", r.Root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	blob := append([]byte{0x00, 0x01, 0x02}, bytes.Repeat([]byte{0xff}, 64)...)
	os.WriteFile(filepath.Join(r.Root, "asset.bin"), blob, 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "with an asset")
	newBase, err := r.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}

	os.WriteFile(filepath.Join(r.Root, "asset.bin"), append([]byte{0x00, 0x01, 0x02}, bytes.Repeat([]byte{0xaa}, 64)...), 0o644)
	first, err := r.CandidateCapture(ctx, newBase, []string{"asset.bin"})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(r.Root, "asset.bin"), append([]byte{0x00, 0x01, 0x02}, bytes.Repeat([]byte{0xbb}, 64)...), 0o644)
	second, err := r.CandidateCapture(ctx, newBase, []string{"asset.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Tree == second.Tree {
		t.Fatal("two different binary contents produced the same content identity")
	}
	if first.Tree == "" || second.Tree == "" {
		t.Fatal("a capture with a base must carry a content identity")
	}
}

// An intended path is compared against a pathname Git reported exactly, so the
// intended set must not normalise whitespace either. Trimming on one side undid
// the exactness the other side had just gained.
func TestAnIntendedPathKeepsItsWhitespace(t *testing.T) {
	r, base := repoWithBase(t)
	name := " asset.bin"
	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0x00, 0xff, 0x13}, 1024)...)
	if err := os.WriteFile(filepath.Join(r.Root, name), elf, 0o644); err != nil {
		t.Skipf("this filesystem will not hold %q: %v", name, err)
	}
	cap, err := r.CandidateCapture(context.Background(), base, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range cap.Excluded {
		if a.Path == name {
			t.Fatalf("an intended output was excluded because its name was trimmed before comparison: %+v", a)
		}
	}
	var kept bool
	for _, a := range cap.Binaries {
		if a.Path == name {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("the intended binary is not recorded as kept: %+v", cap.Binaries)
	}
}

// The artifact boundary decides against a MUTABLE worktree, before T exists.
// Once the content is frozen the same question is re-asked of it, so a capture
// cannot ship exclusion metadata that describes bytes the reviewer never sees.
func TestTheBoundaryDecisionIsRevalidatedAgainstTheFrozenTree(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)

	// A tree that holds a new, unplanned binary -- the state the boundary would
	// have refused had it seen these bytes.
	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0x00, 0xff, 0x13}, 1024)...)
	os.WriteFile(filepath.Join(r.Root, "sneaked.bin"), elf, 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"sneaked.bin"})
	if err != nil {
		t.Fatal(err)
	}
	err = r.validateBoundaryAgainstTree(ctx, base, tree, map[string]bool{})
	if err == nil {
		t.Fatal("a frozen tree holding an unplanned new binary passed the boundary re-check")
	}
	if !strings.Contains(err.Error(), "sneaked.bin") {
		t.Errorf("the refusal must name the path, got %v", err)
	}
	// Named by the plan, the same tree is fine.
	if err := r.validateBoundaryAgainstTree(ctx, base, tree, map[string]bool{"sneaked.bin": true}); err != nil {
		t.Fatalf("an intended output was refused: %v", err)
	}
}

func TestAnOversizedUnplannedFileInTheFrozenTreeIsRefused(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	big := bytes.Repeat([]byte("text\n"), (oversizedBytes/5)+16)
	os.WriteFile(filepath.Join(r.Root, "generated.txt"), big, 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"generated.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.validateBoundaryAgainstTree(ctx, base, tree, map[string]bool{}); err == nil {
		t.Fatal("a frozen tree holding an unplanned oversized file passed the boundary re-check")
	}
}
