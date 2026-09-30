// sensei-code-answerer supplies architecture and advisory-review payloads only.
// It does not establish independent-review standing or operational readiness.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/globulario/sensei-code/internal/answerer"
	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/workflow"
)

func main() {
	once := flag.Bool("once", false, "poll once; all review output is advisory")
	flag.Parse()
	if err := run(context.Background(), *once); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, once bool) error {
	cfg, err := answerer.ConfigFromEnv()
	if err != nil {
		return err
	}
	box := &answerer.GitHubMailbox{Config: cfg, Decode: mailboxDecoder(cfg)}
	// Hold the single-writer lock even while checking network configuration.
	worker, err := answerer.New(cfg, box, &answerer.OpenAIModel{Model: cfg.Model, APIKey: cfg.APIKey}, workflow.StrictValidateArchitectureBody, workflow.StrictValidateReviewPayload)
	if err != nil {
		return err
	}
	defer worker.Close()
	if err = box.Verify(ctx); err != nil {
		return err
	}
	if once {
		return worker.Poll(ctx)
	}
	return worker.Run(ctx, func(err error) { fmt.Fprintln(os.Stderr, err) })
}

// mailboxDecoder adapts existing bridge parsers. Only the fixed publisher can
// supply requests or withdrawals; only the fixed responder can supply answers.
func mailboxDecoder(cfg answerer.Config) answerer.DecodeMailbox {
	return func(comments []answerer.Comment) (answerer.Snapshot, error) {
		s := answerer.Snapshot{Withdrawn: map[string]bool{}, Answered: map[string]bool{}}
		for _, c := range comments {
			if c.AuthorID != cfg.PublisherID {
				continue
			}
			if strings.HasPrefix(c.Body, ghbridge.WithdrawnMarker) {
				id, err := ghbridge.ParseWithdrawal(c.Body)
				if err != nil {
					return s, err
				}
				s.Withdrawn[id] = true
				continue
			}
			if strings.HasPrefix(c.Body, "[sensei-code:architecture-request]") {
				r, ok := ghbridge.ParseArchitectureRequest(c.Body)
				if !ok {
					return s, errors.New("malformed architecture request")
				}
				if r.MailboxRepository != "" && r.MailboxRepository != cfg.Repository {
					return s, errors.New("architecture mailbox binding differs from operator destination")
				}
				envelope, err := (ghbridge.ArchitectureResponse{Binding: r.Binding, RequestID: r.RequestID, Body: "\x00"}).Marker()
				if err != nil {
					return s, err
				}
				s.Requests = append(s.Requests, answerer.Request{Kind: answerer.Architecture, Task: r.Binding.TaskID, ID: r.RequestID, Body: c.Body, Envelope: strings.TrimSuffix(envelope, "\x00"), CommentID: c.ID})
			} else if strings.HasPrefix(c.Body, "[sensei-code:review-request]") {
				if _, _, err := answerer.EnvelopePrefix(c.Body); err != nil {
					return s, err
				}
				r, ok := ghbridge.ParseRequest(c.Body)
				// Unsupported authority kinds are invisible to the answerer.
				if !ok {
					_, fields, err := answerer.EnvelopePrefix(c.Body)
					if err == nil && fields["kind"] != "review" {
						continue
					}
					return s, errors.New("malformed review request")
				}
				if r.MailboxRepository != "" && r.MailboxRepository != cfg.Repository {
					return s, errors.New("review mailbox binding differs from operator destination")
				}
				envelope, err := embeddedReviewEnvelope(c.Body, r)
				if err != nil {
					return s, err
				}
				s.Requests = append(s.Requests, answerer.Request{Kind: answerer.Review, Task: r.TaskID, ID: r.RequestID, Body: c.Body, Envelope: envelope, CommentID: c.ID})
			}
		}
		for _, c := range comments {
			if c.AuthorID != cfg.ResponderID {
				continue
			}
			if r, ok := ghbridge.ParseArchitectureResponse(c.Body); ok {
				for _, q := range s.Requests {
					if q.Kind != answerer.Architecture {
						continue
					}
					request, ok := ghbridge.ParseArchitectureRequest(q.Body)
					if ok && r.Answers(request) {
						s.Answered[q.ID] = true
					}
				}
			}
			if ghbridge.ArchitectureRefusalShaped(c.Body) {
				r, err := ghbridge.ParseArchitectureRefusal(c.Body)
				if err != nil {
					return s, err
				}
				for _, q := range s.Requests {
					if q.Kind != answerer.Architecture {
						continue
					}
					request, ok := ghbridge.ParseArchitectureRequest(q.Body)
					if ok && r.Refuses(request) {
						s.Answered[q.ID] = true
					}
				}
			}
			if strings.HasPrefix(c.Body, "[sensei-code:review]") {
				for _, q := range s.Requests {
					if q.Kind != answerer.Review {
						continue
					}
					request, ok := ghbridge.ParseRequest(q.Body)
					if !ok {
						continue
					}
					// Reading identity through ParseRequest reuses the bridge's canonical
					// field validation. Payload validity is irrelevant to at-most-once.
					_, fields, err := answerer.EnvelopePrefix(c.Body)
					if err != nil {
						return s, err
					}
					response, ok := reviewIdentity(fields)
					if ok && response.RequestID == request.RequestID && response.Subject.Same(request.Subject) && response.ReviewerProvider == request.ReviewerProvider {
						s.Answered[q.ID] = true
					}
				}
			}
		}
		return s, nil
	}
}

// The request teaches one envelope at position zero on a line. Extract its
// bytes verbatim, then verify its binding against the request through the
// existing exported request parser. Neither request prose nor the model selects
// identity. Multiple candidate envelopes are refused as ambiguous.
func embeddedReviewEnvelope(body string, r ghbridge.Request) (string, error) {
	const marker = "\n[sensei-code:review]\n"
	if strings.Count(body, marker) != 1 {
		return "", errors.New("review request must embed one exact reply envelope")
	}
	start := strings.Index(body, marker) + 1
	prefix, fields, err := answerer.EnvelopePrefix(body[start:])
	if err != nil {
		return "", err
	}
	response, ok := reviewIdentity(fields)
	if !ok || response.RequestID != r.RequestID || !response.Subject.Same(r.Subject) || response.ReviewerProvider != r.ReviewerProvider {
		return "", errors.New("embedded review envelope does not match request")
	}
	// Strip nothing from the envelope and add nothing to its identity.
	return prefix, nil
}

func reviewIdentity(fields map[string]string) (ghbridge.Request, bool) {
	var b strings.Builder
	b.WriteString("[sensei-code:review-request]\nkind=review\n")
	for _, key := range []string{"task", "request", "base", "candidate_digest", "candidate_tree", "review_commit", "reviewer"} {
		value := fields[key]
		if value == "" {
			return ghbridge.Request{}, false
		}
		fmt.Fprintf(&b, "%s=%s\n", key, value)
	}
	if len(fields) != 7 {
		return ghbridge.Request{}, false
	}
	return ghbridge.ParseRequest(b.String())
}
