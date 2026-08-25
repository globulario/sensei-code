package main

// proofbench preflight-candidate — prove the repaired evaluator without
// spending a provider token.
//
// It points the frozen oracle at a candidate worktree that already exists on
// disk and reports what it finds. The proof-v5 COLD wave recorded
// internal-gitx-a4fa351 as INCORRECT while its real candidate holds the
// required method; a repaired evaluator must reproduce CORRECT from that same
// tree, and if it cannot, no wave should be started.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/proofbench"
)

func preflightCandidate(args []string) int {
	fs := flag.NewFlagSet("preflight-candidate", flag.ExitOnError)
	manifest := fs.String("manifest", "", "manifest carrying the task's frozen oracle (required)")
	task := fs.String("task", "", "task id (required)")
	dir := fs.String("candidate", "", "an existing candidate worktree to judge (required)")
	want := fs.String("want", "CORRECT", "the verdict this candidate is known to deserve")
	if err := fs.Parse(args); err != nil ||
		strings.TrimSpace(*manifest) == "" || strings.TrimSpace(*task) == "" || strings.TrimSpace(*dir) == "" {
		fs.Usage()
		return exitUsage
	}
	m, _, err := proofbench.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench preflight-candidate:", err)
		return exitFailed
	}
	t, ok := m.Task(*task)
	if !ok {
		fmt.Fprintf(os.Stderr, "no task %q in this manifest\n", *task)
		return exitFailed
	}
	if t.Contract == nil {
		fmt.Fprintf(os.Stderr, "task %q has no contract oracle\n", *task)
		return exitFailed
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailed
	}
	if _, err := os.Stat(*dir); err != nil {
		fmt.Fprintf(os.Stderr, "candidate %s: %v\n", *dir, err)
		return exitFailed
	}

	r := proofbench.Runner{RepoRoot: root, CorpusRoot: dirOf(*manifest), Now: time.Now}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cand := proofbench.Candidate{Dir: *dir, Method: "supplied"}
	hash := proofbench.CandidateDiffHash(ctx, cand, t.BaseSHA)
	res := r.Judge(ctx, *dir, t)

	fmt.Printf("task       %s\ncandidate  %s\ndiff       %s\nverdict    %s\nwant       %s\n",
		*task, *dir, hash, res.Verdict, *want)
	if hash == proofbench.EmptyTreeHash {
		fmt.Fprintln(os.Stderr, "\nthe supplied candidate hashes to the EMPTY TREE, which is the shape "+
			"of the defect this preflight exists to rule out")
		return exitNotGood
	}
	if string(res.Verdict) != *want {
		fmt.Fprintf(os.Stderr, "\nthe repaired evaluator did not reproduce the known result. "+
			"Do not start a wave.\n%s\n", tailLines(res.Detail, 12))
		return exitNotGood
	}
	fmt.Println("\nreproduced. The evaluator finds the work where a governed run actually leaves it.")
	return exitOK
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
