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

	"github.com/globulario/sensei-code/internal/config"
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
	if len(args) != 0 && args[0] == "attest" {
		return runReviewAttest(repo, args[1:])
	}
	if len(args) == 0 || args[0] != "submit" {
		return errors.New("review requires: submit --file <artifact>, or attest --request <id> --review-digest <sha256>")
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

// `sensei-code review attest` overrides the review obligation on one exact
// relayed review, on the operator's own authority.
//
// Both the request AND the digest are required, and neither is inferred: an
// override that let this command pick the review would cover an artifact its
// author never read. It states plainly what it is recording, because the whole
// value of the act is that nobody later mistakes it for a review.
func runReviewAttest(repo gitx.Repo, args []string) error {
	fs := flag.NewFlagSet("review attest", flag.ContinueOnError)
	request := fs.String("request", "", "the review request this override covers")
	digest := fs.String("review-digest", "", "the sha256 of the relayed review being overridden")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *request == "" || *digest == "" || fs.NArg() != 0 {
		return errors.New("review attest takes exactly --request <id> --review-digest <sha256>")
	}
	res, err := control.SubmitLocalAttestation(repo.Root, *request, *digest)
	if res.State != "" {
		fmt.Printf("attestation %s\n", res.State)
		fmt.Printf("  task          %s\n", res.TaskID)
		fmt.Printf("  request       %s\n", res.RequestID)
		fmt.Printf("  review        %s by %s\n", res.ReviewDigest, res.Reviewer)
		fmt.Printf("  attested by   %s\n", res.Principal)
		if res.PublicationComment > 0 {
			fmt.Printf("  published     comment %d by %s\n", res.PublicationComment, res.Publication)
		}
		fmt.Println("  recorded as a human override; the independent-review obligation remains unmet")
	}
	return err
}

// attestHandler is the control process's attestation handler: the only caller
// of ghbridge.AcceptAttestation, and only with the principal the attestation
// socket observed and the authority this workspace's config grants.
func attestHandler(runners engineResolver, cfg config.Config) control.AttestHandler {
	return func(ctx context.Context, in control.LocalAttestation, p control.LocalRelayPrincipal) (control.LocalAttestationResult, error) {
		if runners.Attestations.Dir == "" || runners.Relays.Dir == "" {
			return control.LocalAttestationResult{}, errors.New("this control process has no GitHub review bridge, so it cannot record an attestation")
		}
		principal := ghbridge.RelayPrincipal{UID: p.UID, PID: p.PID, Terminal: p.Terminal}
		if u, err := user.LookupId(strconv.FormatUint(uint64(p.UID), 10)); err == nil {
			principal.User = u.Username
		}
		rec, err := ghbridge.AcceptAttestation(ctx, ghbridge.AttestationSubmission{
			RequestID: in.RequestID, ReviewDigest: in.ReviewDigest, Principal: principal,
			Permitted: cfg.Workflow.OwnerAttestation,
			Relays:    runners.Relays, Store: runners.Attestations, Mailbox: runners.Mailbox,
		})
		return control.LocalAttestationResult{
			State: rec.State, TaskID: rec.Attestation.Binding.TaskID, RequestID: rec.Attestation.RequestID,
			ReviewDigest: rec.Attestation.ReviewDigest, Reviewer: rec.Attestation.Reviewer,
			Principal:   rec.Attestation.Principal,
			Publication: rec.Publication, PublicationComment: rec.PublicationComment,
		}, err
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
			Exchanges: runners.Exchanges, Store: runners.Relays, Reviews: runners.Reviews, Mailbox: runners.Mailbox,
		})
		return control.LocalRelayResult{
			State: rec.State, TaskID: rec.TaskID, RequestID: rec.RequestID, Reviewer: rec.Reviewer,
			ReviewDigest: rec.ReviewDigest, Publication: rec.Publication, PublicationComment: rec.PublicationComment,
		}, err
	}
}
