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

	"github.com/globulario/sensei-code/internal/provider"
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
	// Raw is the COMPLETE captured stream. It used to be the last 20KB, which
	// meant a classification could rest on text nobody could go back and read.
	Raw string
	// RawBytes and RawSHA256 describe that complete stream.
	RawBytes  int
	RawSHA256 string
	// TerminalSource says whether the engine named this outcome specifically,
	// and so whether the text classifier was permitted to decide.
	TerminalSource TerminalSource
	// InfrastructureHint is a recognised phrase that was found and overruled.
	InfrastructureHint string
	// Classifier is the span that produced the classification.
	Classifier *ClassifierEvidence
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
	"unauthorized", "authentication failed", "failed to authenticate", "not authenticated",
	"invalid api key", "api key is invalid", "api error: 401", "api error: 403",
	"api error: 429", "api error: 500", "api error: 529",
	"connection refused", "connection reset", "no rollout found",
	"service unavailable", "bad gateway", "gateway timeout",
	// The graph backend, added after a real COLD arm died on it and was
	// recorded as a semantic INCORRECT. A governed run whose awareness graph is
	// unreachable is not evidence about governance -- it is evidence that the
	// service was down, and it must be retry-eligible rather than scored.
	"backend is unreachable", "preflight unavailable",
	"transport failed on all configured addresses",
	"rst_stream", "context deadline exceeded",
	"awareness-graph backend", "sensei mcp handshake",
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
	cmd.Env = append(strippedEnv(), "SENSEI_CODE_BENCHMARK=1")
	out, err := cmd.CombinedOutput()

	full := string(out)
	o := ArmOutcome{Raw: full, RawBytes: len(out), RawSHA256: HashBytes(out)}
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			o.Terminal = "workflow.not_started"
			o.TerminalSource = TerminalProcessFailure
			o.Infrastructure = "could not start " + r.Binary + ": " + err.Error()
			return o
		}
	}
	o.Terminal = governedExit(code)
	o.TerminalSource = terminalSource(code)
	o.readEvents(full)
	o.classifyInfrastructure(full, code)
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

// strippedEnv is the environment an arm runs in, without ambient provider
// credentials.
//
// Sensei Code already strips these from every agent process it drives, so an
// ambient key cannot override the subscription login. The harness has to do the
// same, and the FIRST RAW arm proved why: it died in 174 seconds with
//
//	claude.ai connectors are disabled because ANTHROPIC_API_KEY … takes
//	precedence over your claude.ai login
//	Failed to authenticate. API Error: 401 API key is invalid.
//
// A RAW arm authenticating differently from the governed arms is not a baseline
// for them. It is a different provider identity, measured against the same
// tasks, reported in the same column.
func strippedEnv() []string {
	strip := map[string]bool{}
	for _, k := range provider.SessionOnlyEnv {
		strip[k] = true
	}
	var out []string
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 && strip[kv[:i]] {
			continue
		}
		out = append(out, kv)
	}
	return out
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
	cmd.Env = strippedEnv()
	out, err := cmd.CombinedOutput()

	full := string(out)
	o := ArmOutcome{Raw: full, RawBytes: len(out), RawSHA256: HashBytes(out)}
	switch {
	case err == nil:
		o.Terminal = "raw.completed"
		o.TerminalSource = TerminalStructuredSpecific
	case ctx.Err() != nil:
		// The harness itself watched the deadline pass. That is an observation,
		// not an inference from prose, so it is authoritative on the same terms
		// as the engine naming its own timeout.
		o.Terminal = "raw.timed_out"
		o.TerminalSource = TerminalStructuredSpecific
		o.classifyInfrastructure(full, 1)
	default:
		o.Terminal = "raw.failed"
		o.TerminalSource = TerminalStructuredGeneric
		o.classifyInfrastructure(full, 1)
	}
	return o
}
