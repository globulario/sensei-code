package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/workflow"
)

// routine-scan is the Stage 2A instrument: it answers, for changes that never
// entered the governed loop, whether routine classification would have
// qualified them and on which conditions.
//
// It grants nothing, changes nothing, and is not part of any governed path. It
// exists because the Stage 1 instrument only ran inside the candidate loop,
// which made it incapable of measuring the population it was meant to describe.
func runRoutineScan(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("routine-scan", flag.ContinueOnError)
	rangeSpec := fs.String("range", "HEAD~20..HEAD", "git revision range to replay, one classification per commit")
	domain := fs.String("domain", "", "Sensei domain to scope the preflight to (required when the graph hosts more than one)")
	corpus := fs.String("corpus", "", "label for this population in the report (default: the range)")
	asJSON := fs.Bool("json", false, "emit the distribution as JSON")
	limit := fs.Int("limit", 200, "maximum commits to replay")
	if err := fs.Parse(args); err != nil {
		return err
	}

	commits, err := repo.RevList(ctx, *rangeSpec, *limit)
	if err != nil {
		return fmt.Errorf("list %s: %w", *rangeSpec, err)
	}
	if len(commits) == 0 {
		return fmt.Errorf("no commits in %s", *rangeSpec)
	}

	sc, err := sensei.Start(ctx, repo.Root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		return fmt.Errorf("start Sensei: %w", err)
	}
	defer sc.Close()

	label := strings.TrimSpace(*corpus)
	if label == "" {
		label = *rangeSpec
	}

	var results []workflow.Counterfactual
	for _, commit := range commits {
		diff, err := repo.CommitDiff(ctx, commit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", commit, err)
			continue
		}
		if strings.TrimSpace(diff) == "" {
			continue
		}
		result, err := classifyOneCommit(sc, *domain, commit, diff)
		if err != nil {
			// Reported, never counted. A commit whose evidence could not be
			// gathered is not a commit that failed to qualify, and folding the
			// two together would put environment failures into the distribution
			// as if they were verdicts.
			fmt.Fprintf(os.Stderr, "no evidence for %s: %v\n", commit, err)
			continue
		}
		results = append(results, result)
	}

	distribution := workflow.Measure(label, results)
	if *asJSON {
		out, err := json.MarshalIndent(struct {
			workflow.Distribution
			Verdict string                    `json:"verdict"`
			Results []workflow.Counterfactual `json:"results"`
		}{distribution, distribution.Verdict(), results}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	fmt.Println(distribution.Render())
	fmt.Println()
	fmt.Println("verdict:", distribution.Verdict())
	return nil
}

// classifyOneCommit gathers the evidence for one commit and answers the
// counterfactual. The preflight is scoped to the files the commit touched,
// which is the same question the governed loop asks about a candidate.
func classifyOneCommit(sc *sensei.Client, domain, commit, diff string) (workflow.Counterfactual, error) {
	paths := workflow.ChangedPathsIn(diff)
	if len(paths) == 0 {
		return workflow.Counterfactual{}, errors.New("the commit touched no files")
	}
	args := map[string]any{
		"task":  "counterfactual routine classification for a historical change",
		"files": paths,
		"mode":  "compact",
	}
	if domain != "" {
		args["domain"] = domain
	}
	result, err := sc.CallTool("awareness_preflight", args)
	if err != nil {
		return workflow.Counterfactual{}, fmt.Errorf("preflight: %w", err)
	}
	scoped, err := sensei.DecodePreflight(result)
	if err != nil {
		return workflow.Counterfactual{}, fmt.Errorf("decode preflight: %w", err)
	}

	edit := sensei.EditCheckResult{}
	editArgs := map[string]any{"file": paths[0], "proposed_content": diff}
	if domain != "" {
		editArgs["domain"] = domain
	}
	if checked, err := sc.CallTool("awareness_edit_check", editArgs); err == nil {
		edit, _ = sensei.DecodeEditCheck(checked)
	}

	// planned is nil and claims are unknown: a historical commit had neither,
	// and ClassifyCounterfactual records both as assumed rather than silently
	// crediting them.
	return workflow.ClassifyCounterfactual(commit, scoped, edit, diff, nil, nil, false), nil
}
