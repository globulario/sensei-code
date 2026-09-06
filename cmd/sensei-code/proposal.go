package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/control"
	"github.com/globulario/sensei-code/internal/ghwebhook"
	"github.com/globulario/sensei-code/internal/gitx"
)

type proposalSubmitter func(repoRoot, task string) (control.LocalAccepted, error)

func runProposal(repo gitx.Repo, args []string) error {
	if len(args) == 0 {
		return errors.New("proposal requires one of: list, show <comment-id>, approve <comment-id>")
	}
	store := ghwebhook.NewProposalStore(repo.Root)
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("proposal list takes no arguments")
		}
		items, err := store.List()
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No GitHub objective proposals are pending or recorded.")
			return nil
		}
		for _, p := range items {
			state := "pending"
			if a, ok, err := store.Approval(p.CommentID); err != nil {
				return err
			} else if ok {
				state = a.State
			}
			fmt.Printf("%d  %-10s  %.12s  %s\n", p.CommentID, state, p.ObjectiveDigest, firstProposalLine(p.Objective))
		}
		return nil
	case "show":
		if len(args) != 2 {
			return errors.New("proposal show requires one GitHub comment id")
		}
		id, err := proposalCommentID(args[1])
		if err != nil {
			return err
		}
		p, err := store.Load(id)
		if err != nil {
			return err
		}
		printProposal(os.Stdout, p)
		if a, ok, err := store.Approval(id); err != nil {
			return err
		} else if ok {
			fmt.Printf("approval    %s\nnonce       %s\n", a.State, a.Nonce)
			if a.TaskID != "" {
				fmt.Printf("task         %s\nprovenance   %s\n", a.TaskID, a.Provenance)
			}
		} else {
			fmt.Println("approval    pending")
		}
		return nil
	case "approve":
		if len(args) != 2 {
			return errors.New("proposal approve requires one GitHub comment id")
		}
		id, err := proposalCommentID(args[1])
		if err != nil {
			return err
		}
		return approveObjectiveProposal(repo.Root, id, os.Stdout, control.SubmitLocalObjective)
	default:
		return fmt.Errorf("unknown proposal command %q; expected list, show, or approve", args[0])
	}
}

func proposalCommentID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("proposal id %q is not a positive GitHub comment id", raw)
	}
	return id, nil
}

func approveObjectiveProposal(repoRoot string, commentID int64, out io.Writer, submit proposalSubmitter) error {
	store := ghwebhook.NewProposalStore(repoRoot)
	p, err := store.Load(commentID)
	if err != nil {
		return err
	}
	printProposal(out, p)

	nonce, err := approvalNonce()
	if err != nil {
		return err
	}
	p, attempt, err := store.BeginApproval(commentID, time.Now(), nonce)
	if err != nil {
		return err
	}

	// The exact immutable stored string, not text from argv and not text fetched
	// from GitHub again, crosses the existing local authority boundary here.
	// That server independently requires same UID, not a governed descendant,
	// and a controlling terminal before it will create a task.
	accepted, err := submit(repoRoot, p.Objective)
	if err != nil {
		return fmt.Errorf("proposal %d entered fail-closed approval state with nonce %s before the local objective channel returned success; do not retry automatically because whether a task was created is not safely inferable: %w",
			commentID, attempt.Nonce, err)
	}
	receipt, err := store.CompleteApproval(commentID, attempt.Nonce, accepted.TaskID, accepted.Provenance, time.Now())
	if err != nil {
		return fmt.Errorf("task %s was accepted for proposal %d but the durable approval receipt could not be completed; do not retry the proposal: %w",
			accepted.TaskID, commentID, err)
	}
	fmt.Fprintf(out, "\nApproved through the existing local objective authority boundary.\n")
	fmt.Fprintf(out, "  comment      %d\n", commentID)
	fmt.Fprintf(out, "  digest       %s\n", receipt.ObjectiveDigest)
	fmt.Fprintf(out, "  nonce        %s\n", receipt.Nonce)
	fmt.Fprintf(out, "  task         %s\n", receipt.TaskID)
	fmt.Fprintf(out, "  provenance   %s\n", receipt.Provenance)
	return nil
}

func printProposal(w io.Writer, p ghwebhook.ObjectiveProposal) {
	fmt.Fprintf(w, "GitHub objective proposal %d\n", p.CommentID)
	fmt.Fprintf(w, "  repo         %s\n", p.RepositoryFullName)
	fmt.Fprintf(w, "  issue        %d\n", p.IssueNumber)
	fmt.Fprintf(w, "  sender       %s / %d\n", p.SenderLogin, p.SenderID)
	fmt.Fprintf(w, "  digest       %s\n", p.ObjectiveDigest)
	fmt.Fprintln(w, "  objective:")
	for _, line := range strings.Split(p.Objective, "\n") {
		fmt.Fprintf(w, "    %s\n", line)
	}
}

func firstProposalLine(s string) string {
	line := strings.TrimSpace(s)
	if at := strings.IndexByte(line, '\n'); at >= 0 {
		line = line[:at]
	}
	if len(line) > 72 {
		line = line[:72] + "..."
	}
	return line
}

func approvalNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
