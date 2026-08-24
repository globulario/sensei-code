package workflow

// Blind-spot classification.
//
// `decideRoute` used to route on `len(BlindSpots) != 0` without reading what a
// blind spot MEANS, and the two things it conflates are opposites.
//
// Measured against the live graph def94857 over the 135 tracked .go files in
// this repository (excluding testdata/ and .pb.go), the vocabulary splits
// cleanly along the preflight status:
//
//	PREFLIGHT_STATUS_OK        26 files, every one with 1-4 direct anchors
//	  22  file path under high-risk directory
//	  15  anchor with severity=critical
//	   4  anchored entity in security/auth/rbac/pki/jwt/cert namespace
//
//	PREFLIGHT_STATUS_EMPTY     84 files, every one with 0 direct anchors
//	  60  graph indexes this area but no anchored rules apply to the request
//	  24  coverage_insufficient: no direct anchors and no indexed files
//
//	PREFLIGHT_STATUS_DEGRADED  25 files, every one with 0 direct anchors
//	  25  high_risk_path_no_direct_anchors: ... graph has no facts about this file
//	  25  this is NOT proof of safety — the graph has no facts about this file
//
// On the OK files -- the only ones the graph actually has anchors for -- not
// one blind spot reports missing knowledge. All three are properties of
// knowledge the graph HAS: how severe the anchor is, what kind of path it sits
// on, what namespace it touches. `anchor with severity=critical` fires BECAUSE
// the graph knows something important, so a file becomes less grantable the
// more strongly it is governed.
//
// Two of those three are also the same inputs the risk classifier already
// consumed: on 22 of the 26 OK files ChangeRisk reached APPROVAL_GATE_NONE --
// no human approval required -- while the blind-spot channel escalated anyway.
// Two channels stating opposite things about the same file, from the same
// evidence, with the channel that merely LISTS the evidence overriding the
// channel that WEIGHS it.
//
// So a blind spot is classified by what it says, not counted.

import "strings"

// blindSpotKind is what a blind spot is evidence of.
type blindSpotKind int

const (
	// blindSpotUnrecognised is a blind spot this classifier has no reading for.
	//
	// It is deliberately first, so the zero value fails closed. A blind-spot
	// string nobody has classified must never become a bounded knowledge gap by
	// default: that would turn every future addition to Sensei's blind-spot
	// vocabulary into silent autonomy, which is the exact shape of "failure to
	// retrieve knowledge is not permission to experiment".
	blindSpotUnrecognised blindSpotKind = iota

	// blindSpotCoverage: the graph reports that it lacks facts here.
	//
	// This is epistemic incompleteness, and it is potentially closable by
	// establishing evidence. It is NOT permission to proceed.
	blindSpotCoverage

	// blindSpotConsequence: the graph has facts, and they say this region is
	// risky. Severity, path class and namespace are consequence signals, not
	// ignorance -- and they are the risk channel's to weigh, not this one's.
	blindSpotConsequence
)

func (k blindSpotKind) String() string {
	switch k {
	case blindSpotCoverage:
		return "coverage"
	case blindSpotConsequence:
		return "consequence"
	default:
		return "unrecognised"
	}
}

// coverageMarkers are phrasings that assert the graph LACKS facts.
//
// Checked before the consequence markers, and the order is load-bearing rather
// than incidental. The DEGRADED vocabulary contains
//
//	"high_risk_path_no_direct_anchors: file is under a high-risk directory but
//	 no awareness anchors apply — graph has no facts about this file"
//
// which names a high-risk directory AND states outright that the graph knows
// nothing. That is a coverage gap that happens to sit on a risky path, not a
// consequence verdict: the risk wording describes where the hole is, and the
// hole is still the finding. Matching consequence first would classify the
// clearest coverage gap in the whole vocabulary as a consequence signal.
var coverageMarkers = []string{
	"no direct anchors",
	"no anchored rules apply",
	"graph has no facts",
	"coverage_insufficient",
	"not proof of safety",
	"treat as unknown",
	"no awareness anchors apply",
}

// consequenceMarkers are phrasings that describe knowledge the graph HAS.
var consequenceMarkers = []string{
	"high-risk directory",
	"high_risk_directory",
	"severity=critical",
	"security/auth/rbac/pki/jwt/cert",
}

// classifyBlindSpot reads one blind spot.
func classifyBlindSpot(s string) blindSpotKind {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return blindSpotUnrecognised
	}
	for _, m := range coverageMarkers {
		if strings.Contains(t, m) {
			return blindSpotCoverage
		}
	}
	for _, m := range consequenceMarkers {
		if strings.Contains(t, m) {
			return blindSpotConsequence
		}
	}
	return blindSpotUnrecognised
}

// blindSpotReading is the classified split of a preflight's blind spots.
type blindSpotReading struct {
	Coverage     []string
	Consequence  []string
	Unrecognised []string
}

func readBlindSpots(spots []string) blindSpotReading {
	var r blindSpotReading
	for _, s := range spots {
		switch classifyBlindSpot(s) {
		case blindSpotCoverage:
			r.Coverage = append(r.Coverage, s)
		case blindSpotConsequence:
			r.Consequence = append(r.Consequence, s)
		default:
			r.Unrecognised = append(r.Unrecognised, s)
		}
	}
	return r
}
