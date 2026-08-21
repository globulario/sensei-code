package workflow

import (
	"strings"
	"testing"
)

// The failure must actually reach the outcome. Describing it correctly is no
// use if surveyPlan still throws the errors away.
func TestSurveyFailuresReachTheOutcome(t *testing.T) {
	body := funcBody(t, "internal/workflow/assisted.go", "surveyPlan")
	if !strings.Contains(body, "failed") || !strings.Contains(body, "reason") {
		t.Fatal("surveyPlan no longer counts the queries that failed")
	}
	if !strings.Contains(body, "Failed") || !strings.Contains(body, "Reason") {
		t.Fatal("the failure count does not reach SurveyOutcome")
	}
}
