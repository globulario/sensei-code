// Command sensei-code-answerer answers governed architecture and advisory review
// requests standing on the sensei-code mailbox, unattended.
//
// It is the composition root and nothing more: configuration comes from the
// operator's environment (see internal/answerer), and the contract validators
// are the workflow's own strict ones, injected so the answerer carries no
// second copy of either contract. Reviews it posts are ADVISORY and satisfy no
// independent-review requirement.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/globulario/sensei-code/internal/answerer"
	"github.com/globulario/sensei-code/internal/workflow"
)

func main() {
	a, err := answerer.New(answerer.ConfigFromEnv(os.Getenv), answerer.Validators{
		Architecture: workflow.StrictValidateArchitectureBody,
		Review:       workflow.StrictValidateReviewPayload,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code-answerer:", err)
		os.Exit(2)
	}
	if err := a.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code-answerer:", err)
		os.Exit(1)
	}
}
