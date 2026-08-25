package proofbench

// Executing an arm against a real provider.
//
// The two governed arms drive the ordinary `sensei-code run` headless path --
// the same engine /run uses -- so the campaign measures the product rather than
// a benchmark-shaped imitation of it. RAW drives the author model directly in
// the same isolated worktree with no Sensei plumbing at all.
//
// Nothing here interprets a result generously. An arm that could not run
// records NO_RESULT with the reason; an arm that ran records what the oracle
// said, whatever that was.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ArmOutcome is what executing one arm produced, before the oracle runs.
type ArmOutcome struct {
	Terminal string
	// Infrastructure is set only for an externally attributable failure --
	// provider outage, quota, auth. It is the one thing that licenses a retry,
	// so it is set from recognised provider signals rather than from any
	// non-zero exit.
	Infrastructure string
	ReviewCycles   int
	Observations   int
	AuthorityAsks  int
	Interventions  []Intervention
	Objections     []Objection
	Events         int
	Raw            string
}

// governedExit maps the headless run's documented exit codes.
//
// Taken from `sensei-code run`'s own contract rather than guessed: 0 complete,
// 1 failed, 3 awaiting human authority, 4 stopped, 5 timed out, 6 observed.
func governedExit(code int) string {
	switch code {
	case 0:
		return "workflow.completed"
	case 1:
		return "workflow.failed"
	case 3:
		return "workflow.awaiting_authority"
	case 4:
		return "workflow.stopped"
	case 5:
		return "workflow.timed_out"
	case 6:
		return "workflow.observed"
	}
	return fmt.Sprintf("workflow.exit_%d", code)
}

// infrastructureSignals are provider failures that are not the candidate's
// fault. Deliberately a short recognised list: treating every failure as
// infrastructure would make the retry rule a licence to re-roll.
//
// Phrases only. The first draft also matched bare status codes -- "403", "429",
// "503" -- and the FIRST real campaign run misclassified a governed failure as
// infrastructure because the event stream carried
// `certified_awareness_graph_commit: a4034c78de600ad14f388343224492a5d722459c`,
// whose hash contains "403". A three-digit substring against a stream full of
// hashes, counts and timestamps matches constantly, and every false match would
// have licensed a retry the rule exists to forbid.
//
// So a status code counts only where something says it is a status code.
var infrastructureSignals = []string{
	"usage limit", "quota exceeded", "rate limit", "too many requests",
	"unauthorized", "authentication failed", "not authenticated", "invalid api key",
	"connection refused", "connection reset", "no rollout found",
	"service unavailable", "bad gateway", "gateway timeout",
}

// statusCodeContext are the shapes a real HTTP failure is reported in.
var statusCodeContext = []string{
	"status 401", "status 403", "status 429", "status 502", "status 503",
	"http 401", "http 403", "http 429", "http 502", "http 503",
	"401 unauthorized", "403 forbidden", "429 too many", "502 bad gateway",
	"503 service unavailable", "statuscode=401", "statuscode=403", "statuscode=429",
}

func infrastructureReason(output string) string {
	low := strings.ToLower(output)
	for _, s := range infrastructureSignals {
		if strings.Contains(low, s) {
			return s
		}
	}
	for _, s := range statusCodeContext {
		if strings.Contains(low, s) {
			return s
		}
	}
	return ""
}

// ExecuteGoverned drives one COLD or WARM arm through the headless engine.
func (r Runner) ExecuteGoverned(ctx context.Context, dir, statement string, timeout time.Duration) ArmOutcome {
	ctx, cancel := context.WithTimeout(ctx, timeout+2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.Binary, "run",
		"--task", statement, "--json", "--timeout", timeout.String())
	cmd.Dir = dir
	// A fresh provider session with no conversation carry-over: the worktree is
	// new, so the session store under it is new too.
	cmd.Env = append(os.Environ(), "SENSEI_CODE_BENCHMARK=1")
	out, err := cmd.CombinedOutput()

	o := ArmOutcome{Raw: tail(string(out), 20000)}
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			o.Terminal = "workflow.not_started"
			o.Infrastructure = "could not start " + r.Binary + ": " + err.Error()
			return o
		}
	}
	o.Terminal = governedExit(code)
	o.readEvents(string(out))
	if code != 0 {
		if why := infrastructureReason(string(out)); why != "" {
			o.Infrastructure = why
		}
	}
	return o
}

// readEvents mines the JSONL stream for the metrics the campaign scores.
func (o *ArmOutcome) readEvents(stream string) {
	for _, line := range strings.Split(stream, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e SessionEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		o.Events++
		switch e.Kind {
		case "review.started":
			o.ReviewCycles++
		case "workflow.observed":
			o.Observations++
		case "authority.required":
			o.AuthorityAsks++
			// An authority question is a human DECISION point, not a human
			// supplying the technical answer. Recorded as such: conflating the
			// two would let every approval prompt read as lost autonomy, and
			// would let a supplied fix hide among them.
			o.Interventions = append(o.Interventions, Intervention{
				Kind: "authority_decision", Detail: firstLine(e.Summary), At: e.Time})
		case "review.finding":
			o.Objections = append(o.Objections, Objection{
				ID: stableObjectionID(e.Summary), Cycle: o.ReviewCycles,
				Status: "new", Text: firstLine(e.Summary)})
		}
	}
	markRepeats(o.Objections)
}

// stableObjectionID lets "the same objection reworded" be distinguished from
// progress, which is the #76 endpoint.
//
// Keyed on the first six words of the finding rather than on its full prose,
// because a reviewer restating an objection changes the sentence and not the
// requirement. Eight words was too many: the same objection reworded diverged
// at word seven and read as progress.
//
// Crude, and over-merges rather than over-splits on purpose. Two genuinely
// different findings that open identically read as one repeat, which
// UNDERSTATES progress -- the safe direction, since the metric exists to catch
// a loop that is not converging and must not be able to invent convergence.
func stableObjectionID(summary string) string {
	// Punctuation is stripped: a reviewer's trailing comma is not a new
	// requirement, and "conclusions" vs "conclusions," read as two objections
	// until it was.
	f := strings.FieldsFunc(strings.ToLower(firstLine(summary)), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	if len(f) > 6 {
		f = f[:6]
	}
	return strings.Join(f, " ")
}

func markRepeats(os_ []Objection) {
	seen := map[string]bool{}
	for i := range os_ {
		if seen[os_[i].ID] {
			os_[i].Status = "repeated"
		}
		seen[os_[i].ID] = true
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return tail(s, 240)
}

// ExecuteRaw drives the author model directly, with no Sensei plumbing.
//
// The baseline for the only question RAW exists to answer: what did the control
// plane buy over the coding model itself?
func (r Runner) ExecuteRaw(ctx context.Context, dir, statement string, timeout time.Duration) ArmOutcome {
	if len(r.RawCommand) == 0 {
		return ArmOutcome{Terminal: "raw.not_configured",
			Infrastructure: "no RAW provider command configured; the baseline cannot be measured " +
				"and must not be reported as anything else"}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := make([]string, len(r.RawCommand))
	for i, a := range r.RawCommand {
		argv[i] = strings.ReplaceAll(a, "{{TASK}}", statement)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	o := ArmOutcome{Raw: tail(string(out), 20000)}
	switch {
	case err == nil:
		o.Terminal = "raw.completed"
	case ctx.Err() != nil:
		o.Terminal = "raw.timed_out"
	default:
		o.Terminal = "raw.failed"
		if why := infrastructureReason(string(out)); why != "" {
			o.Infrastructure = why
		}
	}
	return o
}
