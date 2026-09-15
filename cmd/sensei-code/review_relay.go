package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"

	"github.com/globulario/sensei-code/internal/control"
	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/gitx"
)

// `sensei-code review submit --file <artifact>` relays a reviewer's complete
// review artifact to the control process.
//
// The command carries the artifact and nothing else. It has no flag for a
// decision, a finding, a request or a candidate: every one of those is the
// artifact's, and a command that could set one would let the relay edit the
// review it is carrying. The control process accepts the bytes exactly or
// refuses them.

// maxReviewArtifactFileBytes mirrors the relay bound, read one byte past so an
// oversized file is refused rather than truncated into a different artifact.
const maxReviewArtifactFileBytes = 64 << 10

func runReview(repo gitx.Repo, args []string) error {
	if len(args) == 0 || args[0] != "submit" {
		return errors.New("review requires: submit --file <artifact>")
	}
	fs := flag.NewFlagSet("review submit", flag.ContinueOnError)
	file := fs.String("file", "", "the complete review artifact, exactly as the reviewer produced it")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return errors.New("review submit takes exactly --file <artifact>")
	}
	raw, err := readReviewArtifact(*file)
	if err != nil {
		return err
	}
	res, err := control.SubmitLocalRelay(repo.Root, raw)
	printRelayResult(os.Stdout, res)
	return err
}

func readReviewArtifact(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	blob, err := io.ReadAll(io.LimitReader(f, maxReviewArtifactFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(blob) > maxReviewArtifactFileBytes {
		return "", fmt.Errorf("%s is larger than a review artifact may be (%d bytes)", path, maxReviewArtifactFileBytes)
	}
	return string(blob), nil
}

func printRelayResult(w io.Writer, res control.LocalRelayResult) {
	if res.State == "" {
		return
	}
	fmt.Fprintf(w, "relayed review %s\n", res.State)
	fmt.Fprintf(w, "  task          %s\n", res.TaskID)
	fmt.Fprintf(w, "  request       %s\n", res.RequestID)
	fmt.Fprintf(w, "  reviewer      %s (the verdict's author; you relayed it)\n", res.Reviewer)
	fmt.Fprintf(w, "  review digest %s\n", res.ReviewDigest)
	if res.PublicationComment > 0 {
		fmt.Fprintf(w, "  published     comment %d by %s\n", res.PublicationComment, res.Publication)
	}
}

// relayHandler is the control process's relay handler: the only caller of
// ghbridge.AcceptRelayedReview, and only with the principal the relay socket
// observed.
func relayHandler(runners engineResolver) control.RelayHandler {
	return func(ctx context.Context, artifact string, p control.LocalRelayPrincipal) (control.LocalRelayResult, error) {
		if runners.Relays.Dir == "" || runners.Exchanges.Dir == "" {
			return control.LocalRelayResult{}, errors.New("this control process has no GitHub review bridge, so it cannot accept a relayed review")
		}
		principal := ghbridge.RelayPrincipal{UID: p.UID, PID: p.PID, Terminal: p.Terminal}
		if u, err := user.LookupId(strconv.FormatUint(uint64(p.UID), 10)); err == nil {
			principal.User = u.Username
		}
		rec, err := ghbridge.AcceptRelayedReview(ctx, ghbridge.RelaySubmission{
			Artifact: artifact, Principal: principal,
			Exchanges: runners.Exchanges, Store: runners.Relays, Mailbox: runners.Mailbox,
		})
		return control.LocalRelayResult{
			State: rec.State, TaskID: rec.TaskID, RequestID: rec.RequestID, Reviewer: rec.Reviewer,
			ReviewDigest: rec.ReviewDigest, Publication: rec.Publication, PublicationComment: rec.PublicationComment,
		}, err
	}
}
