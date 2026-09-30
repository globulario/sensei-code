// Command sensei-code-answerer is the unattended process that answers governed
// architecture and advisory review requests on the Sensei Code mailbox.
//
// It is a composition root and nothing more. Configuration is the operator's,
// read from SENSEI_ANSWERER_* variables; the canonical wire contracts are the
// workflow's own validators, handed to the answerer as functions so there is
// one contract and not two. Reviews it posts are advisory: nothing here
// establishes reviewer independence.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/globulario/sensei-code/internal/answerer"
	"github.com/globulario/sensei-code/internal/workflow"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code-answerer:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := answerer.LoadConfig(os.Getenv, func(path string) (string, error) {
		b, err := os.ReadFile(path)
		return string(b), err
	})
	if err != nil {
		return err
	}
	a, err := answerer.New(cfg, answerer.NewGitHubMailbox(cfg), answerer.NewChatModel(cfg),
		workflow.ValidateArchitectureBody, workflow.ValidateReviewPayload)
	if err != nil {
		return err
	}
	return a.Run(ctx, answerer.DefaultInterval, func(o answerer.Outcome) {
		fmt.Fprintln(os.Stderr, o.String())
	})
}
