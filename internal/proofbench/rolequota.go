package proofbench

// Capacity is established PER ROLE, or not at all.
//
// Instrument defect #14: the gate read one global reading, emitted only by the
// Claude implementor, and only when an implementor actually ran. A cheap probe
// completes at the architect and never reaches an implementor, so the gate was
// blind to the role whose budget it existed to protect -- and refused a campaign
// it could not see. Failing closed was right; measuring nothing was not.
//
// So each role required by a campaign is read on its own terms, and a role whose
// budget cannot be read is UNREADABLE rather than assumed available. A campaign
// is admitted only when EVERY required role has provable capacity.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RoleCapacity is one role's reading.
type RoleCapacity struct {
	Role     string `json:"role"`
	Provider string `json:"provider"`
	// Readable says whether a quantitative reading exists. A provider that
	// reports no limits is not thereby unlimited.
	Readable bool `json:"readable"`
	// Available is the headroom on the tightest window when Readable.
	Window    string  `json:"window,omitempty"`
	Available float64 `json:"available,omitempty"`
	// Proven says the provider answered a real turn just now. Weaker than a
	// reading -- it proves "can start", not "can finish" -- and is recorded as
	// exactly that.
	Proven bool   `json:"proven"`
	Detail string `json:"detail,omitempty"`
}

// ReadClaudeCapacity asks the Claude provider directly, with one trivial turn.
//
// Direct rather than through a governed run, because a governed run only
// invokes Claude as an implementor and an implementor turn is not trivial. One
// "reply ok" is the cheapest reading the provider offers.
func ReadClaudeCapacity(ctx context.Context) RoleCapacity {
	rc := RoleCapacity{Role: "implementor", Provider: "claude"}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "Reply with the single word ok.",
		"--output-format", "stream-json", "--verbose")
	cmd.Env = strippedEnv()
	out, _ := cmd.CombinedOutput()
	q, err := parseQuota(string(out))
	if err != nil {
		rc.Detail = "no rate_limit_event in a direct claude turn: " + err.Error()
		return rc
	}
	rc.Readable = true
	rc.Window, rc.Available = q.Tightest()
	rc.Proven = strings.Contains(string(out), `"subtype":"success"`)
	if strings.Contains(string(out), `"status":"rejected"`) {
		rc.Available = 0
		rc.Detail = "provider reports status rejected"
	}
	return rc
}

// ProveArchitectCapacity runs one conversational governed turn and reports
// whether the architect answered.
//
// The architect provider reports no limits through this path, so this is a
// PROOF OF AVAILABILITY and not a reading. It is recorded as unreadable on
// purpose: a provider that answers once has not shown it can answer four
// times, and the admission rule treats it accordingly.
func ProveArchitectCapacity(ctx context.Context, binary, repoRoot string) RoleCapacity {
	rc := RoleCapacity{Role: "architect", Provider: "chatgpt"}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "run", "--task",
		"Report the current time. Change no files.", "--json", "--timeout", "3m")
	cmd.Dir = repoRoot
	cmd.Env = strippedEnv()
	out, _ := cmd.CombinedOutput()
	rc.Proven = strings.Contains(string(out), `"kind":"workflow.completed"`)
	if !rc.Proven {
		rc.Detail = "the conversational probe did not complete"
	}
	if q, err := parseQuota(string(out)); err == nil {
		rc.Readable = true
		rc.Window, rc.Available = q.Tightest()
	}
	return rc
}

// AdmitCampaignByRole admits only when every required role can finish.
//
// A readable role is held to the projection, as before. An unreadable role is
// admitted only if it is PROVEN and the operator has explicitly accepted that
// "can start" is the strongest evidence that provider offers -- recorded on the
// result so the admission says what it rested on.
func AdmitCampaignByRole(roles []RoleCapacity, armsRemaining int, perArm float64, acceptProvenOnly bool) error {
	if armsRemaining <= 0 {
		return nil
	}
	if perArm <= 0 {
		return ErrInsufficientQuota{"no per-arm consumption estimate is available"}
	}
	need := float64(armsRemaining) * perArm * CapacityMargin
	for _, r := range roles {
		switch {
		case r.Readable:
			if r.Available < need {
				return ErrInsufficientQuota{fmt.Sprintf("%s (%s): %d arm(s) need about %.1f%% of the %s "+
					"window but only %.1f%% remains", r.Role, r.Provider, armsRemaining, need*100,
					r.Window, r.Available*100)}
			}
		case r.Proven && acceptProvenOnly:
			// admitted on proof of availability; recorded by the caller
		case r.Proven:
			return ErrInsufficientQuota{fmt.Sprintf("%s (%s) is proven available but reports no "+
				"limits; capacity to FINISH cannot be established. Pass --accept-proven to admit "+
				"on availability alone, and the admission will say so", r.Role, r.Provider)}
		default:
			return ErrInsufficientQuota{fmt.Sprintf("%s (%s) is UNREADABLE and unproven: %s",
				r.Role, r.Provider, r.Detail)}
		}
	}
	return nil
}

// RolesJSON renders readings for the campaign record.
func RolesJSON(roles []RoleCapacity) string {
	b, _ := json.MarshalIndent(roles, "", "  ")
	return string(b)
}
