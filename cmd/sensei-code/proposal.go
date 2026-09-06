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

// authorizedSubmitter is an already-authorized objective connection.
//
// An interface rather than the concrete type so a test can observe WHEN the
// authority decision happened relative to the approval receipt, which is the
// property this file exists to get right.
type authorizedSubmitter interface {
	Submit(task string) (control.LocalAccepted, error)
	Close() error
}

// localAuthorizer establishes local authority and hands back the connection it
// was established on. Refusal is an error, returned before the caller has done
// anything durable.
type localAuthorizer func(repoRoot string) (authorizedSubmitter, error)

// dialLocalObjective is the production authorizer.
func dialLocalObjective(repoRoot string) (authorizedSubmitter, error) {
	return control.DialLocalObjective(repoRoot)
}

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
		return approveObjectiveProposal(repo.Root, id, os.Stdout, dialLocalObjective)
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

// approveObjectiveProposal turns a pending proposal into governed work, in the
// one order that is safe.
//
//	establish local authority on the connection
//	  -> durable BeginApproval
//	  -> send the exact stored objective on THAT SAME authorized connection
//	  -> task acceptance
//	  -> CompleteApproval
//
// Authority first, and the reason is that the approval receipt is an
// at-most-once token. It used to be written before the objective was sent, and
// the authority decision happens inside the server AFTER the connection is
// made — so a same-UID caller with no controlling terminal, or a governed
// descendant, could run this, spend the token, and only then be refused. No
// task was created and the proposal was permanently unapprovable: an authority
// refusal was consuming the very thing authority was supposed to protect. Any
// local process able to run the CLI could destroy a pending proposal without
// holding any authority at all.
//
// The fail-closed semantics after BeginApproval are UNCHANGED and deliberately
// so. Once the objective is on the wire, whether a task was created is not
// safely inferable from a failure, and the attempt stays durable rather than
// being retried into a second Claude. What moved is only the line between "was
// never allowed to try" and "tried and lost the answer" — the first is not
// ambiguous and must not be charged as though it were.
//
// authorizeObjective remains the single authority decision. This does not
// duplicate it, re-implement it, or pre-check it locally; it asks the server
// once and then keeps that connection.
func approveObjectiveProposal(repoRoot string, commentID int64, out io.Writer, authorize localAuthorizer) error {
	store := ghwebhook.NewProposalStore(repoRoot)
	p, err := store.Load(commentID)
	if err != nil {
		return err
	}
	printProposal(out, p)

	// Authority BEFORE anything durable. A refusal here leaves the proposal
	// exactly as it was found, still approvable by someone who does hold it.
	conn, err := authorize(repoRoot)
	if err != nil {
		return fmt.Errorf("proposal %d was not approved because this caller may not originate governed work; "+
			"the proposal is untouched and remains pending: %w", commentID, err)
	}
	defer conn.Close()

	nonce, err := approvalNonce()
	if err != nil {
		return err
	}
	p, attempt, err := store.BeginApproval(commentID, time.Now(), nonce)
	if err != nil {
		return err
	}

	// The exact immutable stored string, not text from argv and not text fetched
	// from GitHub again, crosses the local authority boundary here — on the
	// connection whose peer that boundary already judged.
	accepted, err := conn.Submit(p.Objective)
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
