package architect

import (
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

type fakeCaller struct {
	args    map[string]map[string]any
	results map[string]sensei.ToolResult
	fail    string
}

func (f *fakeCaller) CallTool(name string, args map[string]any) (sensei.ToolResult, error) {
	if f.args == nil {
		f.args = map[string]map[string]any{}
	}
	f.args[name] = args
	if name == f.fail {
		return sensei.ToolResult{}, errors.New("unavailable")
	}
	return f.results[name], nil
}

func TestReportScopesToTheRepositoryDomain(t *testing.T) {
	c := &fakeCaller{results: map[string]sensei.ToolResult{
		"awareness_metadata": {Structured: map[string]any{"invariant_count": float64(6)}},
	}}
	if _, err := RunReport(c, "github.com/globulario/sensei-code", 90); err != nil {
		t.Fatal(err)
	}
	if got := c.args["awareness_metadata"]["domain"]; got != "github.com/globulario/sensei-code" {
		t.Fatalf("metadata domain = %v; an unscoped query would report another repository's graph", got)
	}
}

func TestReportRefusesAnEmptyMetadataAnswer(t *testing.T) {
	// A metadata call that returns nothing is not an empty repository.
	c := &fakeCaller{results: map[string]sensei.ToolResult{"awareness_metadata": {}}}
	if _, err := RunReport(c, "d", 90); err == nil {
		t.Fatal("an empty metadata answer was rendered as a report")
	}
}

func TestFocusNeedsAPath(t *testing.T) {
	if _, err := RunFocus(&fakeCaller{}, "d", "  "); err == nil {
		t.Fatal("/focus accepted no path")
	}
}

func TestFocusReportsBriefingAndRisk(t *testing.T) {
	briefing := sensei.ToolResult{Structured: map[string]any{"status": "BRIEFING_STATUS_DEGRADED"}}
	briefing.Content = append(briefing.Content, struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "text", Text: "governed by invariant X"})
	c := &fakeCaller{results: map[string]sensei.ToolResult{
		"awareness_briefing": briefing,
		"awareness_preflight": {Structured: map[string]any{
			"risk_class":       "SECURITY_RISK",
			"confidence":       "CONFIDENCE_HIGH",
			"required_actions": []any{"Verify invariant still holds: credentials stay provider-owned"},
			"blind_spots":      []any{"coverage_insufficient"},
		}},
	}}
	out, err := RunFocus(c, "d", "internal/provider/provider.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"degraded", "governed by invariant X", "SECURITY_RISK", "credentials stay provider-owned", "coverage_insufficient"} {
		if !strings.Contains(out, want) {
			t.Fatalf("focus output is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "thin coverage rather than a safe change") {
		t.Fatal("focus presented an empty briefing as safety")
	}
}

func TestFocusFailsWhenSenseiRefuses(t *testing.T) {
	c := &fakeCaller{fail: "awareness_briefing"}
	if _, err := RunFocus(c, "d", "a.go"); err == nil {
		t.Fatal("focus reported on a briefing Sensei never gave")
	}
}
