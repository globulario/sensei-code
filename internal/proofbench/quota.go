package proofbench

// Can this campaign FINISH?
//
// REPAIR_VERIFICATION was launched behind a gate that asked whether one arm
// could start. It could: the provider's five-hour window had just reset. The
// seven-day window was at 96%, and eleven arms of up to twenty-two minutes
// cannot fit in the 4% that was left.
//
// Had it run, the tail of the wave would have failed on quota and recorded as
// INFRA_FAILURE -- a wave measuring its own environment, which is the proof-v5
// disaster arriving by a different road.
//
// The gate was asking the wrong question. "Can I start?" is not the admission
// criterion for a campaign; "can I complete every scheduled arm, with margin"
// is. And the binding window is whichever one runs out first, which is not
// necessarily the one that just reset.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// QuotaReading is the provider's own account of what is left.
//
// Read from the rate_limit_event the provider emits into the session stream, so
// it is the provider's number rather than the harness's estimate of it.
type QuotaReading struct {
	// Windows maps a window name ("five_hour", "seven_day") to its utilisation
	// in [0,1]. Every window is kept: the binding constraint is whichever is
	// tightest, and a gate that inspects only one is how this defect happened.
	Windows map[string]float64 `json:"windows"`
	// ResetsAt maps a window name to its unix reset time.
	ResetsAt map[string]int64 `json:"resets_at,omitempty"`
	// Observed is when the reading was taken.
	Observed string `json:"observed,omitempty"`
	// Status is the provider's own label, e.g. "allowed_warning".
	Status string `json:"status,omitempty"`
}

// Tightest returns the window with the least headroom, which is the one that
// decides whether a campaign can finish.
func (q QuotaReading) Tightest() (name string, available float64) {
	available = 1.0
	for w, used := range q.Windows {
		if free := 1 - used; free < available {
			name, available = w, free
		}
	}
	if name == "" {
		return "", 1.0
	}
	if available < 0 {
		available = 0
	}
	return name, available
}

func (q QuotaReading) String() string {
	var parts []string
	for _, w := range []string{"five_hour", "seven_day"} {
		if u, ok := q.Windows[w]; ok {
			parts = append(parts, fmt.Sprintf("%s %.0f%% used", w, u*100))
		}
	}
	for w, u := range q.Windows {
		if w != "five_hour" && w != "seven_day" {
			parts = append(parts, fmt.Sprintf("%s %.0f%% used", w, u*100))
		}
	}
	if len(parts) == 0 {
		return "no window reported"
	}
	return strings.Join(parts, ", ")
}

// ReadQuota asks the provider what is left.
//
// The probe must actually REACH the provider. The first version reused the
// environment check's start-gate-only probe, which never invokes a model and so
// never elicits a rate-limit event: the gate then refused every campaign for
// lack of a reading. Fail-closed was the right direction and a useless gate.
//
// So this runs a real, deliberately trivial task. It spends a small amount of
// the budget to find out how much budget is left, which is the honest price of
// the question.
func ReadQuota(ctx context.Context, binary, repoRoot string) (QuotaReading, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "run",
		"--task", "Report the current time. Change no files.", "--json", "--timeout", "3m")
	cmd.Dir = repoRoot
	cmd.Env = strippedEnv()
	out, _ := cmd.CombinedOutput()
	return parseQuota(string(out))
}

// LatestQuotaFromTranscripts recovers the newest reading already on disk.
//
// Free, and the fallback when a live probe cannot be afforded or does not
// reach the provider. Its age is returned with it: a stale reading is evidence
// about the past and must be labelled as such rather than used as if current.
func LatestQuotaFromTranscripts(corpusRoot string) (QuotaReading, string, bool) {
	var best QuotaReading
	var bestPath string
	var bestAt time.Time
	_ = filepath.Walk(corpusRoot, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || filepath.Ext(p) != ".log" {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		q, perr := parseQuota(string(b))
		if perr != nil {
			return nil
		}
		if fi.ModTime().After(bestAt) {
			best, bestPath, bestAt = q, p, fi.ModTime()
		}
		return nil
	})
	if bestPath == "" {
		return QuotaReading{}, "", false
	}
	return best, fmt.Sprintf("%s (recorded %s)", bestPath, bestAt.UTC().Format(time.RFC3339)), true
}

// parseQuota mines rate_limit_event out of a session stream.
func parseQuota(stream string) (QuotaReading, error) {
	q := QuotaReading{Windows: map[string]float64{}, ResetsAt: map[string]int64{}}
	found := false
	for _, line := range strings.Split(stream, "\n") {
		// Unquoted on purpose. The event arrives nested inside a session
		// event's summary string, where every quote is backslash-escaped, so
		// searching for `"rate_limit_info"` finds nothing at all -- which is
		// how the first version of this parser silently reported no reading on
		// a transcript that plainly contained one.
		if !strings.Contains(line, "rate_limit_info") {
			continue
		}
		// The event is embedded in an escaped JSON string inside the session
		// event, so the payload is unquoted before it is read.
		var probe struct {
			Info struct {
				Status         string  `json:"status"`
				RateLimitType  string  `json:"rateLimitType"`
				Utilization    float64 `json:"utilization"`
				ResetsAt       int64   `json:"resetsAt"`
				UnifiedWindows map[string]struct {
					Utilization float64 `json:"utilization"`
					ResetsAt    int64   `json:"resetsAt"`
				} `json:"unifiedWindows"`
			} `json:"rate_limit_info"`
		}
		for _, candidate := range unescapedCandidates(line) {
			if json.Unmarshal([]byte(candidate), &probe) != nil {
				continue
			}
			if probe.Info.RateLimitType == "" && len(probe.Info.UnifiedWindows) == 0 {
				continue
			}
			found = true
			q.Status = probe.Info.Status
			if probe.Info.RateLimitType != "" {
				q.Windows[probe.Info.RateLimitType] = probe.Info.Utilization
				if probe.Info.ResetsAt != 0 {
					q.ResetsAt[probe.Info.RateLimitType] = probe.Info.ResetsAt
				}
			}
			for w, v := range probe.Info.UnifiedWindows {
				q.Windows[w] = v.Utilization
				if v.ResetsAt != 0 {
					q.ResetsAt[w] = v.ResetsAt
				}
			}
			break
		}
	}
	if !found {
		return q, fmt.Errorf("no rate_limit_event was found in the probe output, so remaining " +
			"campaign capacity could not be established")
	}
	return q, nil
}

// unescapedCandidates yields the raw line and its unescaped inner payload, so a
// rate-limit event survives being embedded in a session event's summary string.
func unescapedCandidates(line string) []string {
	out := []string{line}
	if i := strings.Index(line, `"summary":"`); i >= 0 {
		var wrapper struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal([]byte(line), &wrapper) == nil && wrapper.Summary != "" {
			out = append(out, wrapper.Summary)
		}
	}
	return out
}

// ErrInsufficientQuota is a refusal to begin a campaign that cannot finish.
type ErrInsufficientQuota struct{ Why string }

func (e ErrInsufficientQuota) Error() string {
	return "CAMPAIGN NOT ADMITTED (quota): " + e.Why +
		" — a wave that exhausts quota partway records its tail as INFRA_FAILURE and measures " +
		"the environment rather than the product"
}

// CapacityMargin is the headroom a campaign must leave beyond its projection.
//
// A projection built from a handful of arms is not precise, and the cost of
// being wrong is asymmetric: too much margin delays a run, too little destroys
// it partway through and wastes every arm already spent.
const CapacityMargin = 1.30

// AdmitCampaign decides whether a campaign may begin.
//
// perArm is the fraction of the binding window one arm is expected to consume.
// It is REQUIRED and has no default on purpose: the honest value comes from
// measurement, and a plausible-looking constant invented here would be exactly
// the kind of unfounded number this campaign exists to avoid. RecordedPerArm
// derives it from a ledger once any campaign has measured it.
func AdmitCampaign(q QuotaReading, armsRemaining int, perArm float64) error {
	if armsRemaining <= 0 {
		return nil
	}
	if perArm <= 0 {
		return ErrInsufficientQuota{"no per-arm consumption estimate is available, so campaign " +
			"capacity cannot be projected. Measure it with `proofbench capacity --observe` or " +
			"supply --per-arm explicitly"}
	}
	window, available := q.Tightest()
	need := float64(armsRemaining) * perArm * CapacityMargin
	if available >= need {
		return nil
	}
	return ErrInsufficientQuota{fmt.Sprintf(
		"%d arm(s) need about %.1f%% of the %s window (%.2f%% per arm x%.2f margin) but only %.1f%% "+
			"remains. Observed: %s",
		armsRemaining, need*100, window, perArm*100, CapacityMargin, available*100, q)}
}

// RecordedPerArm derives per-arm consumption from arms that recorded quota on
// both sides.
//
// Returns 0 when nothing has been measured, which AdmitCampaign turns into a
// refusal rather than a guess. The MAXIMUM observed delta is used rather than
// the mean: the question is whether the campaign can finish, and a mean lets a
// few cheap refusals hide the cost of the arms that do real work.
func RecordedPerArm(attempts []Attempt) float64 {
	worst := 0.0
	for _, a := range attempts {
		if a.QuotaBefore == nil || a.QuotaAfter == nil {
			continue
		}
		for w, before := range a.QuotaBefore.Windows {
			after, ok := a.QuotaAfter.Windows[w]
			if !ok {
				continue
			}
			// A window that reset mid-arm reads as negative consumption and is
			// not evidence about cost.
			if d := after - before; d > worst {
				worst = d
			}
		}
	}
	return worst
}
