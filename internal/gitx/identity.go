package gitx

// The canonical candidate identity commit.
//
// A candidate that lives only as a working tree has no name. Its content can be
// digested, but nothing durable refers to it, and the digest of a diff is not
// an object anyone can fetch, verify, or admit. This file gives the candidate a
// name -- one exact Git object -- and nothing else.
//
//	IDENTITY             the object C
//	LOCAL CANDIDATE      the candidate branch points at C
//	PUBLICATION CUSTODY  origin's candidate branch points at C: durable, and
//	                     explicitly not an admission
//	ADMISSION CUSTODY    a policy-owned ref points at C
//
// Creating C confers only the first two. Durability does not imply admission
// either: authority comes from the class and creator of the surviving ref, not
// from reachability.
//
// # The proof is reconstruction, not authorship
//
// The author string is not evidence. Anyone can write "Sensei Code" into a
// commit. What cannot be forged is that C is exactly the commit that (base,
// tree) produces: every field is an input or a constant, nothing is read from
// the environment, and a second implementation given the same two inputs
// arrives at the same SHA. A verifier therefore needs no signature and no trust
// in anyone's Git config.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// CanonicalIdentity is the fixed authorship of every candidate identity commit.
//
// It is a tool identity and deliberately not a person: a candidate commit must
// never inherit the identity of whoever happened to be at the keyboard, because
// a later reader would be unable to tell a minted object from authored work.
const (
	CanonicalName  = "Sensei Code Candidate Identity"
	CanonicalEmail = "candidate-identity@sensei-code.invalid"
)

// canonicalMessage is derived only from the two inputs.
//
// The task id, the reviewer, the plan digest and the verdict are deliberately
// absent: they belong to the receipt. Putting them here would give identical
// candidate content different Git identities merely because a different task
// produced it, and identity would stop being a function of content.
func canonicalMessage(base, tree string) string {
	return "sensei-code candidate identity\n\nbase: " + base + "\ntree: " + tree + "\n"
}

// CanonicalTree builds the reviewed tree: the base tree, plus exactly the
// paths the capture named.
//
// It runs in a TEMPORARY INDEX. CandidateCapture mutates the live index with
// `add --intent-to-add`, and a tree built through that index would depend on
// whatever the capture happened to leave behind.
//
// `add --all` is scoped to the given paths, so a worker's scratch file or an
// excluded artifact cannot enter the tree -- the property publish.CommitArgs
// already protects, kept here. Scoping it also means a deleted path is recorded
// as a deletion rather than silently retained from the base.
func (r Repo) CanonicalTree(ctx context.Context, base string, paths []string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("a canonical tree needs the base it is built from")
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("a canonical tree needs the exact paths the candidate changed; an empty set is not a candidate")
	}
	index, err := os.CreateTemp("", "sensei-code-canonical-index-*")
	if err != nil {
		return "", fmt.Errorf("temporary index: %w", err)
	}
	indexPath := index.Name()
	index.Close()
	// git refuses to read-tree into a file it did not create.
	_ = os.Remove(indexPath)
	defer os.Remove(indexPath)

	env := append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	if _, err := r.envOutput(ctx, env, "read-tree", base); err != nil {
		return "", fmt.Errorf("read the base tree: %w", err)
	}
	args := append([]string{"add", "--all", "--"}, paths...)
	if _, err := r.envOutput(ctx, env, args...); err != nil {
		return "", fmt.Errorf("stage the reviewed paths: %w", err)
	}
	tree, err := r.envOutput(ctx, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write the canonical tree: %w", err)
	}
	return strings.TrimSpace(tree), nil
}

// CanonicalCommitBytes is the exact serialized commit object for these inputs.
//
// The serialization is OURS, byte for byte, rather than whatever a git
// porcelain happens to emit: that is what makes a fixed test vector possible,
// and what makes a future stray newline or reordered header visible instead of
// silent. Every field is an input or a constant; nothing is read from the
// environment or a clock.
func CanonicalCommitBytes(base, tree, stamp string) []byte {
	var b strings.Builder
	b.WriteString("tree " + tree + "\n")
	b.WriteString("parent " + base + "\n")
	b.WriteString("author " + CanonicalName + " <" + CanonicalEmail + "> " + stamp + "\n")
	b.WriteString("committer " + CanonicalName + " <" + CanonicalEmail + "> " + stamp + "\n")
	b.WriteString("\n")
	b.WriteString(canonicalMessage(base, tree))
	return []byte(b.String())
}

// canonicalStamp is the base's committer time plus one second, at +0000.
//
// Derived, not observed: two runs of the same candidate against the same base
// must produce the same commit, and a wall clock guarantees they never would.
func (r Repo) canonicalStamp(ctx context.Context, base string) (string, error) {
	out, err := r.readOnlyOutput(ctx, "show", "-s", "--format=%ct", base)
	if err != nil {
		return "", fmt.Errorf("read the base commit time: %w", err)
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return "", fmt.Errorf("the base commit time is not a timestamp: %q", strings.TrimSpace(out))
	}
	return strconv.FormatInt(secs+1, 10) + " +0000", nil
}

// ExpectedCanonicalSHA is the identity (base, tree) MUST produce, computed
// without writing anything.
//
// Verification that mints the object it verifies is not verification: an
// earlier draft called CanonicalCommit from VerifyCanonicalCommit, so every
// check wrote a reconstructed commit into .git/objects. A verifier must be able
// to run against a repository it does not modify. `hash-object` without -w
// computes and discards, and it uses the repository's own object format rather
// than assuming SHA-1.
func (r Repo) ExpectedCanonicalSHA(ctx context.Context, base, tree string) (string, error) {
	stamp, err := r.canonicalStamp(ctx, base)
	if err != nil {
		return "", err
	}
	out, err := r.stdinOutput(ctx, nil, string(CanonicalCommitBytes(base, tree, stamp)),
		"hash-object", "-t", "commit", "--stdin")
	if err != nil {
		return "", fmt.Errorf("compute the canonical identity: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// MintCanonicalCommit writes the exact canonical bytes and returns the object.
//
// It writes OUR serialization rather than asking commit-tree to build one, so
// the object stored is the object CanonicalCommitBytes specifies -- the thing
// the test vector pins and the verifier recomputes.
func (r Repo) MintCanonicalCommit(ctx context.Context, base, tree string) (string, error) {
	stamp, err := r.canonicalStamp(ctx, base)
	if err != nil {
		return "", err
	}
	out, err := r.stdinOutput(ctx, nil, string(CanonicalCommitBytes(base, tree, stamp)),
		"hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("mint the candidate identity commit: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// VerifyCanonicalCommit reports whether the recorded object is exactly the
// identity (base, tree) produces. It computes and compares; it WRITES NOTHING.
//
// This is the verification that needs no signature: a forged author string
// survives inspection, a forged identity does not survive reconstruction. And a
// verifier that minted the object it verifies would leave the repository
// holding a reconstruction of every claim it ever checked.
func (r Repo) VerifyCanonicalCommit(ctx context.Context, base, tree, recorded string) error {
	expected, err := r.ExpectedCanonicalSHA(ctx, base, tree)
	if err != nil {
		return err
	}
	if expected != strings.TrimSpace(recorded) {
		return fmt.Errorf("the recorded candidate identity %s is not the commit (base %s, tree %s) produces, which is %s",
			short12(recorded), short12(base), short12(tree), short12(expected))
	}
	return nil
}

// CommitTreeOf reports a commit's tree, and FirstParentOf its first parent, so
// a caller can measure what it recorded rather than assume it.
func (r Repo) CommitTreeOf(ctx context.Context, commit string) (string, error) {
	return r.readOnlyOutput(ctx, "rev-parse", commit+"^{tree}")
}

// FirstParentOf returns the first parent, or "" when the commit is a root.
func (r Repo) FirstParentOf(ctx context.Context, commit string) (string, error) {
	out, err := r.readOnlyOutput(ctx, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return "", nil
	}
	return fields[1], nil
}

// PointBranchAt moves a candidate branch onto an exact object.
func (r Repo) PointBranchAt(ctx context.Context, branch, commit string) error {
	_, err := r.output(ctx, "update-ref", "refs/heads/"+branch, commit)
	return err
}

func short12(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// envOutput runs git with an explicit environment. The environment is the
// mechanism here rather than a convenience: GIT_INDEX_FILE keeps the canonical
// tree out of the live index, and the GIT_AUTHOR_/GIT_COMMITTER_ variables are
// what make the commit a function of its inputs instead of of this machine.
func (r Repo) envOutput(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Root}, args...)...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// stdinOutput runs git with an optional environment and a stdin payload, so
// that not one byte of the commit object passes through argv quoting.
func (r Repo) stdinOutput(ctx context.Context, env []string, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Root}, args...)...)
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
