package architect

import (
	"strings"
	"testing"
)

func metadata() map[string]any {
	return map[string]any{
		"authority":              map[string]any{"verdict": "authoritative", "state": "current"},
		"graph_freshness_state":  "GRAPH_FRESHNESS_STATE_CURRENT",
		"coverage_state":         "COVERAGE_STATE_SUFFICIENT",
		"triple_count":           float64(1866),
		"invariant_count":        float64(144),
		"meta_principle_count":   float64(138),
		"failure_mode_count":     float64(3),
		"forbidden_fix_count":    float64(2),
		"required_test_count":    float64(11),
		"source_file_count":      float64(14),
		"incident_pattern_count": float64(0),
	}
}

func TestInstalledPackIsNotCreditedAsAuthoredGovernance(t *testing.T) {
	// 144 invariants reads as a well governed repository. 138 of them are the
	// portable pack; six were written for this code.
	r := FromMetadata("d", metadata())
	var invariants Count
	for _, c := range r.Authored {
		if c.Label == "invariants" {
			invariants = c
		}
	}
	if invariants.Value != 6 {
		t.Fatalf("authored invariants = %d, want 6 (144 total less the 138 installed)", invariants.Value)
	}
	if !strings.Contains(invariants.Note, "138") {
		t.Fatalf("the installed pack was subtracted without saying so: %q", invariants.Note)
	}
	if len(r.Installed) != 1 || r.Installed[0].Value != 138 {
		t.Fatalf("the pack is not reported separately: %+v", r.Installed)
	}
}

func TestClassesWithNoPublishedTotalAreNotShownAsZero(t *testing.T) {
	r := FromMetadata("d", metadata())
	rendered := r.Render(90)
	for _, class := range []string{"contracts", "decisions", "design patterns", "implementation patterns"} {
		if !strings.Contains(rendered, class) {
			t.Fatalf("%s is not mentioned at all, so its absence is invisible", class)
		}
	}
	if !strings.Contains(rendered, "publishes no total") {
		t.Fatal("unpublished classes are not distinguished from measured zeroes")
	}
}

func TestEmptyClassesAreNamedAsGaps(t *testing.T) {
	r := FromMetadata("d", metadata())
	found := false
	for _, gap := range r.Gaps {
		if gap == "incident patterns" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a class with an exact zero was not named as a gap: %v", r.Gaps)
	}
}

func TestReportRefusesToPresentCountsAsAnAssessment(t *testing.T) {
	rendered := FromMetadata("d", metadata()).Render(90)
	for _, want := range []string{
		"knowledge it was never given",
		"never whether the code obeys it",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("report is missing the limit %q", want)
		}
	}
}

func TestUnstatedAuthorityIsNotReadAsAuthoritative(t *testing.T) {
	r := FromMetadata("d", map[string]any{})
	if r.Authority != "unstated" {
		t.Fatalf("authority = %q, want unstated when Sensei said nothing", r.Authority)
	}
	if r.Freshness != "unknown" || r.Coverage != "unknown" {
		t.Fatalf("absent states were smoothed into something reassuring: %+v", r)
	}
}

func TestUnknownPackSizeIsNotPassedOffAsAuthoredGovernance(t *testing.T) {
	// The MCP surface omits meta_principle_count. Rendering 144 as authored
	// invariants would credit this repository with a pack it merely installed.
	m := metadata()
	delete(m, "meta_principle_count")
	r := FromMetadata("d", m)
	var invariants Count
	for _, c := range r.Authored {
		if c.Label == "invariants" {
			invariants = c
		}
	}
	if invariants.Exact {
		t.Fatal("an invariant total that may be mostly an installed pack was presented as exact")
	}
	if !strings.Contains(invariants.Note, "does not publish") {
		t.Fatalf("the unknown split was not stated: %q", invariants.Note)
	}
}

func TestCountsAreReadFromBothSenseiSurfaces(t *testing.T) {
	// The MCP payload sends numbers; the CLI sends some of them as strings.
	// A reader that understands only one silently reports zero on the other.
	if got := toInt(float64(138)); got != 138 {
		t.Fatalf("numeric count = %d, want 138", got)
	}
	if got := toInt("138"); got != 138 {
		t.Fatalf("string count = %d, want 138", got)
	}
	if got := toInt("not a number"); got != 0 {
		t.Fatalf("unparseable count = %d, want 0", got)
	}
}
