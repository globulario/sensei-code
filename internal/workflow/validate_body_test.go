package workflow

import "testing"

// These witness the REAL strict validators the unattended answerer is handed.
// Each refusal case is a body the lenient readers would accept or repair: that
// difference is the property under test, so every refusal is paired with the
// well-formed body it departs from.

const wellFormedProceed = `{"decision":"proceed","summary":"s","plan":"do the bounded thing","steps":["one"],
 "files":["internal/x/x.go"],"mode":"modify",
 "claims":[{"statement":"x.go owns the parser","about":"internal/x/x.go","source":"repository"}],
 "prospective_surfaces":[{"path":"internal/n/n.go","package":"n","role":"go-library-package","covering":"internal/m/m.go","dependencies":["fmt"]}],
 "test_edits":[{"path":"internal/x/x_test.go","operation":"edit","package":"x","imports":["testing"]}],
 "premise_resolutions":[{"gap":"p-1","outcome":"established","evidence":"read x.go"}]}`

const wellFormedEscalate = `{"decision":"escalate","summary":"s","human_question":"May the contract change?",
 "recommendation":"1","options":[{"id":"1","label":"yes","description":"change it"},{"id":"2","label":"no"}]}`

func TestStrictValidateArchitectureBodyAcceptsWellFormedProceedAndEscalate(t *testing.T) {
	for name, body := range map[string]string{
		"proceed":  wellFormedProceed,
		"escalate": wellFormedEscalate,
		"reply":    `{"decision":"reply","message":"hello"}`,
		"padded":   "\n  " + wellFormedProceed + "\n",
	} {
		if err := StrictValidateArchitectureBody(body); err != nil {
			t.Errorf("%s: a well-formed body was refused: %v", name, err)
		}
	}
}

func TestStrictValidateArchitectureBodyRejectsProceedWithoutClaims(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `{"decision":"proceed","summary":"s","plan":"p","mode":"modify"}`,
		"empty":  `{"decision":"proceed","summary":"s","plan":"p","mode":"modify","claims":[]}`,
		"null":   `{"decision":"proceed","summary":"s","plan":"p","mode":"modify","claims":null}`,
	} {
		// The lenient reader accepts it: the gap is exactly what strictness adds.
		var d architectureDecision
		if err := decodeModelJSON(body, &d); err != nil {
			t.Fatalf("%s: the lenient reader should decode this body: %v", name, err)
		}
		if err := StrictValidateArchitectureBody(body); err == nil {
			t.Errorf("%s: proceed without claims was accepted", name)
		}
	}
}

func TestStrictValidateArchitectureBodyRejectsIncompleteEscalate(t *testing.T) {
	for name, body := range map[string]string{
		"no human_question": `{"decision":"escalate","summary":"s","recommendation":"1","options":[{"id":"1","label":"a"},{"id":"2","label":"b"}]}`,
		"no options":        `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1"}`,
		"one option":        `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1","options":[{"id":"1","label":"a"}]}`,
		"no recommendation": `{"decision":"escalate","summary":"s","human_question":"q","options":[{"id":"1","label":"a"},{"id":"2","label":"b"}]}`,
		"foreign recommend": `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"9","options":[{"id":"1","label":"a"},{"id":"2","label":"b"}]}`,
		"model outcome":     `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1","options":[{"id":"1","label":"a","outcome":"proceed"},{"id":"2","label":"b"}]}`,
	} {
		if err := StrictValidateArchitectureBody(body); err == nil {
			t.Errorf("%s: an incomplete escalate was accepted", name)
		}
	}
}

// An absent operation declares nothing, so the strict contract admits exactly
// the four written operations and refuses the omission (see "omitted op" below).
func TestStrictValidateArchitectureBodyAcceptsEachTestEditOperation(t *testing.T) {
	for _, op := range []string{"edit", "create", "delete", "rename"} {
		body := `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}],"test_edits":[{"path":"a_test.go","operation":"` + op + `"}]}`
		if err := StrictValidateArchitectureBody(body); err != nil {
			t.Errorf("operation %q was refused: %v", op, err)
		}
	}
}

func TestStrictValidateArchitectureBodyReadsVocabulariesByExactMembership(t *testing.T) {
	for name, body := range map[string]string{
		"cased decision":  `{"decision":"Proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}]}`,
		"unknown mode":    `{"decision":"proceed","plan":"p","mode":"audit","claims":[{"statement":"s","source":"graph"}]}`,
		"missing mode":    `{"decision":"proceed","plan":"p","claims":[{"statement":"s","source":"graph"}]}`,
		"claim source":    `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"memory"}]}`,
		"surface role":    `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}],"prospective_surfaces":[{"path":"a/a.go","package":"a","role":"go-anything"}]}`,
		"no covering":     `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}],"prospective_surfaces":[{"path":"a/a.go","package":"a","role":"go-library-package"}]}`,
		"padded op":       `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}],"test_edits":[{"path":"a_test.go","operation":" edit "}]}`,
		"omitted op":      `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}],"test_edits":[{"path":"a_test.go"}]}`,
		"empty op":        `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}],"test_edits":[{"path":"a_test.go","operation":""}]}`,
		"premise outcome": `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}],"premise_resolutions":[{"gap":"p-1","outcome":"maybe"}]}`,
		"adjudication":    `{"decision":"proceed","plan":"p","mode":"modify","claims":[{"statement":"s","source":"graph"}],"adjudication":"stands"}`,
		"unknown field":   `{"decision":"reply","message":"m","verdict":"accept"}`,
		"fenced":          "```json\n{\"decision\":\"reply\",\"message\":\"m\"}\n```",
		"prose prefix":    `Here you go: {"decision":"reply","message":"m"}`,
		"trailing object": `{"decision":"reply","message":"m"} {"decision":"reply","message":"n"}`,
		"not json":        `I cannot answer this.`,
	} {
		if err := StrictValidateArchitectureBody(body); err == nil {
			t.Errorf("%s: a body outside the contract was accepted", name)
		}
	}
}

const wellFormedReview = `{"decision":"revise","summary":"one defect","instructions":"fix it",
 "findings":[{"id":"f1","severity":"blocking","class":"code","claim":"c","reference":"internal/x/x.go","reason":"r","correction":"do y"},
             {"id":"f2","severity":"minor","class":"evidence","claim":"c","reason":"r","proof_gap":"go test ./internal/x"}]}`

func TestStrictValidateReviewPayloadAcceptsWellFormedReview(t *testing.T) {
	for name, body := range map[string]string{
		"revise": wellFormedReview,
		"accept": `{"decision":"accept","summary":"stands"}`,
	} {
		if err := StrictValidateReviewPayload(body); err != nil {
			t.Errorf("%s: a well-formed review was refused: %v", name, err)
		}
	}
}

func TestStrictValidateReviewPayloadRejectsMalformedFindingsBeforeNormalization(t *testing.T) {
	for name, body := range map[string]string{
		// numberFindings would give these an id and a severity.
		"no id":           `{"decision":"revise","summary":"s","findings":[{"severity":"major","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"no severity":     `{"decision":"revise","summary":"s","findings":[{"id":"f1","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"no class":        `{"decision":"revise","summary":"s","findings":[{"id":"f1","severity":"major","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"bad class":       `{"decision":"revise","summary":"s","findings":[{"id":"f1","severity":"major","class":"style","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"code no ref":     `{"decision":"revise","summary":"s","findings":[{"id":"f1","severity":"major","class":"code","claim":"c","reason":"r","correction":"y"}]}`,
		"evidence no gap": `{"decision":"revise","summary":"s","findings":[{"id":"f1","severity":"major","class":"evidence","claim":"c","reason":"r","correction":"y"}]}`,
		"duplicate id":    `{"decision":"revise","summary":"s","findings":[{"id":"f1","severity":"minor","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"},{"id":"f1","severity":"minor","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"accept blocking": `{"decision":"accept","summary":"s","findings":[{"id":"f1","severity":"blocking","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"cased decision":  `{"decision":"Accept","summary":"s"}`,
		"no summary":      `{"decision":"accept"}`,
		"empty revise":    `{"decision":"revise","summary":"s"}`,
		"unknown field":   `{"decision":"accept","summary":"s","standing":"independent"}`,
		"fenced":          "```json\n{\"decision\":\"accept\",\"summary\":\"s\"}\n```",
	} {
		if err := StrictValidateReviewPayload(body); err == nil {
			t.Errorf("%s: a malformed review payload was accepted", name)
		}
	}
	// The normalizing reader manufactures validity for the first two; the
	// strict validator must not share that outcome.
	var d reviewDecision
	if err := decodeModelJSON(`{"decision":"revise","summary":"s","findings":[{"class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`, &d); err != nil {
		t.Fatal(err)
	}
	if got := numberFindings(d.Findings); got[0].ID == "" || !got[0].Severity.Valid() {
		t.Fatalf("control: numberFindings no longer supplies id and severity, so this witness proves nothing: %+v", got)
	}
}
