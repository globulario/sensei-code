package workflow

import (
	"strings"
	"testing"
)

const strictProceed = `{"decision":"proceed","summary":"bounded change","plan":"implement the bound","mode":"modify","claims":[{"statement":"checked","about":"internal/x.go","source":"repository"}]}`
const strictEscalate = `{"decision":"escalate","summary":"knowledge gap","human_question":"May this contract change?","recommendation":"1","options":[{"id":"1","label":"change","description":"change the contract"},{"id":"2","label":"retain","description":"retain the contract"}]}`
const strictReview = `{"decision":"revise","summary":"missing proof","findings":[{"id":"f1","severity":"major","class":"evidence","claim":"no proof","reference":"test","reason":"unrun","proof_gap":"go test ./..."}]}`

func TestStrictArchitectureRequiredFields(t *testing.T) {
	for _, body := range []string{strictProceed, strictEscalate, `{"decision":"reply","message":"I cannot establish the requested facts."}`} {
		if err := StrictValidateArchitectureBody(body); err != nil {
			t.Fatalf("well formed body refused: %v", err)
		}
	}
	for _, body := range []string{
		strings.Replace(strictProceed, `,"claims":[{"statement":"checked","about":"internal/x.go","source":"repository"}]`, "", 1),
		strings.Replace(strictEscalate, `"human_question":"May this contract change?",`, "", 1),
		strings.Replace(strictEscalate, `"recommendation":"1",`, "", 1),
		strings.Replace(strictEscalate, `"options":[{"id":"1","label":"change","description":"change the contract"},{"id":"2","label":"retain","description":"retain the contract"}]`, `"options":[]`, 1),
	} {
		if err := StrictValidateArchitectureBody(body); err == nil {
			t.Fatalf("malformed architecture accepted: %s", body)
		}
	}
}

func TestStrictArchitectureClosedNestedVocabularies(t *testing.T) {
	for _, extra := range []string{
		`"prospective_surfaces":[{"path":"internal/new/new.go","package":"new","role":"go-library-package","covering":"internal/old/old.go","dependencies":["fmt"]}]`,
		`"test_edits":[{"path":"internal/x_test.go","operation":"create","package":"x","imports":["testing"]}]`,
		`"premise_resolutions":[{"gap":"p1","outcome":"unresolved"}]`,
	} {
		good := strings.TrimSuffix(strictProceed, "}") + "," + extra + "}"
		if err := StrictValidateArchitectureBody(good); err != nil {
			t.Fatalf("nested declaration refused: %v", err)
		}
		bad := strings.NewReplacer("go-library-package", "invented", "create", "Create", "unresolved", "perhaps").Replace(good)
		if err := StrictValidateArchitectureBody(bad); err == nil {
			t.Fatalf("unknown vocabulary accepted: %s", bad)
		}
	}
}

func TestStrictReviewRejectsFindingsBeforeNormalization(t *testing.T) {
	for _, body := range []string{strictReview, `{"decision":"accept","summary":"looks correct","findings":[]}`, `{"decision":"escalate","summary":"Cannot establish the evidence; request more knowledge."}`} {
		if err := StrictValidateReviewPayload(body); err != nil {
			t.Fatalf("well formed review refused: %v", err)
		}
	}
	for _, body := range []string{
		strings.Replace(strictReview, `"id":"f1",`, "", 1),
		strings.Replace(strictReview, `"severity":"major",`, "", 1),
		strings.Replace(strictReview, `"class":"evidence",`, "", 1),
		strings.Replace(strictReview, `"proof_gap":"go test ./..."`, `"correction":"add proof"`, 1),
		strings.Replace(strictReview, `"severity":"major"`, `"severity":"blocking"`, 1),
		strings.Replace(strictReview, `"reason":"unrun",`, "", 1),
	} {
		// The blocking fixture must actually omit the reference to be malformed.
		if strings.Contains(body, `"severity":"blocking"`) {
			body = strings.Replace(body, `"reference":"test",`, "", 1)
		}
		if err := StrictValidateReviewPayload(body); err == nil {
			t.Fatalf("malformed finding accepted: %s", body)
		}
	}
	// Existing lenient decoder and numbering still accept and default this shape.
	body := `{"decision":"revise","summary":"s","findings":[{"class":"code","claim":"c","reference":"a.go","reason":"r","correction":"fix"}]}`
	var d reviewDecision
	if err := decodeModelJSON("prose\n"+body, &d); err != nil {
		t.Fatal(err)
	}
	numbered := numberFindings(d.Findings)
	if numbered[0].ID != "f1" || numbered[0].Severity != "major" {
		t.Fatal("lenient normalization changed")
	}
	if err := StrictValidateReviewPayload(body); err == nil {
		t.Fatal("strict validator manufactured validity")
	}
}

func TestStrictBodiesRejectNonJSONArtifactsAndDuplicateFields(t *testing.T) {
	for _, body := range []string{"not JSON", "```json\n" + strictReview + "\n```", "prefix " + strictReview, strictReview + " {}", strings.Replace(strictReview, `"summary":"missing proof"`, `"summary":"missing proof\ue200cite\ue202"`, 1), strings.Replace(strictReview, `"decision":"revise"`, `"decision":"accept","decision":"revise"`, 1), strings.Replace(strictReview, `"id":"f1"`, `"id":"f0","id":"f1"`, 1)} {
		if err := StrictValidateReviewPayload(body); err == nil {
			t.Fatalf("artifact accepted: %s", body)
		}
	}
}
