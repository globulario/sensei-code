package workflow

import "testing"

// These witness the REAL strict validators the unattended answerer is handed.
// Each refusal case is a body the lenient readers would accept or repair: that
// difference is the property under test, so every refusal is paired with the
// well-formed body it departs from.

const wellFormedProceed = `{"decision":"proceed","summary":"s","plan":"do the bounded thing","steps":["one"],
 "consequences":"x.go parses one more form","files":["internal/x/x.go"],"mode":"modify",
 "claims":[{"statement":"x.go owns the parser","about":"internal/x/x.go","source":"repository"}],
 "prospective_surfaces":[{"path":"internal/n/n.go","package":"n","role":"go-library-package","covering":"internal/m/m.go","dependencies":["fmt"]}],
 "test_edits":[{"path":"internal/x/x_test.go","operation":"edit","package":"x","imports":["testing"]}],
 "premise_resolutions":[{"gap":"p-1","outcome":"established","evidence":"read x.go"}]}`

const wellFormedEscalate = `{"decision":"escalate","summary":"s","human_question":"May the contract change?",
 "recommendation":"1","options":[{"id":"1","label":"yes","description":"change it"},{"id":"2","label":"no","description":"keep it"}]}`

// proceedCore is every field the written contract requires of proceed. Cases
// below append to it, so each refusal departs from a complete body in exactly
// the one way it names.
const proceedCore = `"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify","claims":[{"statement":"s","source":"graph"}]`

const escalateOptions = `"options":[{"id":"1","label":"a","description":"da"},{"id":"2","label":"b","description":"db"}]`

func containsText(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

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
		"absent": `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify"}`,
		"empty":  `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify","claims":[]}`,
		"null":   `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify","claims":null}`,
	} {
		// The lenient reader accepts it: the gap is exactly what strictness adds.
		var d architectureDecision
		if err := decodeModelJSON(body, &d); err != nil {
			t.Fatalf("%s: the lenient reader should decode this body: %v", name, err)
		}
		if err := StrictValidateArchitectureBody(body); err == nil || !containsText(err.Error(), "claims") {
			t.Errorf("%s: proceed without claims was not refused for its claims: %v", name, err)
		}
	}
}

func TestStrictValidateArchitectureBodyRejectsIncompleteEscalate(t *testing.T) {
	for name, body := range map[string]string{
		"no human_question": `{"decision":"escalate","summary":"s","recommendation":"1",` + escalateOptions + `}`,
		"no summary":        `{"decision":"escalate","human_question":"q","recommendation":"1",` + escalateOptions + `}`,
		"no options":        `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1"}`,
		"one option":        `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1","options":[{"id":"1","label":"a","description":"da"}]}`,
		"no description":    `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1","options":[{"id":"1","label":"a","description":"da"},{"id":"2","label":"b"}]}`,
		"blank description": `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1","options":[{"id":"1","label":"a","description":"da"},{"id":"2","label":"b","description":"  "}]}`,
		"no recommendation": `{"decision":"escalate","summary":"s","human_question":"q",` + escalateOptions + `}`,
		"foreign recommend": `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"9",` + escalateOptions + `}`,
		"model outcome":     `{"decision":"escalate","summary":"s","human_question":"q","recommendation":"1","options":[{"id":"1","label":"a","description":"da","outcome":"proceed"},{"id":"2","label":"b","description":"db"}]}`,
	} {
		if err := StrictValidateArchitectureBody(body); err == nil {
			t.Errorf("%s: an incomplete escalate was accepted", name)
		}
	}
}

// Each case is a complete proceed with exactly one required field omitted or
// emptied; the refusal must name that field, so it cannot pass for another one.
func TestStrictValidateArchitectureBodyRejectsProceedMissingARequiredField(t *testing.T) {
	const claims = `"claims":[{"statement":"s","source":"graph"}]`
	for field, body := range map[string]string{
		"summary":         `{"decision":"proceed","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify",` + claims + `}`,
		"summary (blank)": `{"decision":"proceed","summary":" ","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify",` + claims + `}`,
		"plan":            `{"decision":"proceed","summary":"s","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify",` + claims + `}`,
		"steps":           `{"decision":"proceed","summary":"s","plan":"p","consequences":"c","files":["a.go"],"mode":"modify",` + claims + `}`,
		"steps (empty)":   `{"decision":"proceed","summary":"s","plan":"p","steps":[],"consequences":"c","files":["a.go"],"mode":"modify",` + claims + `}`,
		"steps (blank)":   `{"decision":"proceed","summary":"s","plan":"p","steps":["one",""],"consequences":"c","files":["a.go"],"mode":"modify",` + claims + `}`,
		"consequences":    `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"files":["a.go"],"mode":"modify",` + claims + `}`,
		"files":           `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","mode":"modify",` + claims + `}`,
		"files (empty)":   `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":[],"mode":"modify",` + claims + `}`,
		"files (blank)":   `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":[" "],"mode":"modify",` + claims + `}`,
		"mode":            `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],` + claims + `}`,
	} {
		name := field
		for i := range field {
			if field[i] == ' ' {
				name = field[:i]
				break
			}
		}
		err := StrictValidateArchitectureBody(body)
		if err == nil || !containsText(err.Error(), name) {
			t.Errorf("proceed without %s was not refused for it: %v", field, err)
		}
	}
	if err := StrictValidateArchitectureBody(`{` + proceedCore + `}`); err != nil {
		t.Fatalf("control: the complete core every case departs from was refused: %v", err)
	}
}

// An absent operation declares nothing, so the strict contract admits exactly
// the four written operations and refuses the omission (see "omitted op" below).
func TestStrictValidateArchitectureBodyAcceptsEachTestEditOperation(t *testing.T) {
	for _, op := range []string{"edit", "create", "delete", "rename"} {
		body := `{` + proceedCore + `,"test_edits":[{"path":"a_test.go","operation":"` + op + `"}]}`
		if err := StrictValidateArchitectureBody(body); err != nil {
			t.Errorf("operation %q was refused: %v", op, err)
		}
	}
}

func TestStrictValidateArchitectureBodyReadsVocabulariesByExactMembership(t *testing.T) {
	for name, body := range map[string]string{
		"cased decision":  `{"decision":"Proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify","claims":[{"statement":"s","source":"graph"}]}`,
		"unknown mode":    `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"audit","claims":[{"statement":"s","source":"graph"}]}`,
		"missing mode":    `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"claims":[{"statement":"s","source":"graph"}]}`,
		"claim source":    `{"decision":"proceed","summary":"s","plan":"p","steps":["one"],"consequences":"c","files":["a.go"],"mode":"modify","claims":[{"statement":"s","source":"memory"}]}`,
		"surface role":    `{` + proceedCore + `,"prospective_surfaces":[{"path":"a/a.go","package":"a","role":"go-anything"}]}`,
		"no covering":     `{` + proceedCore + `,"prospective_surfaces":[{"path":"a/a.go","package":"a","role":"go-library-package"}]}`,
		"padded op":       `{` + proceedCore + `,"test_edits":[{"path":"a_test.go","operation":" edit "}]}`,
		"omitted op":      `{` + proceedCore + `,"test_edits":[{"path":"a_test.go"}]}`,
		"empty op":        `{` + proceedCore + `,"test_edits":[{"path":"a_test.go","operation":""}]}`,
		"premise outcome": `{` + proceedCore + `,"premise_resolutions":[{"gap":"p-1","outcome":"maybe"}]}`,
		"adjudication":    `{` + proceedCore + `,"adjudication":"stands"}`,
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
		"revise":   wellFormedReview,
		"accept":   `{"decision":"accept","summary":"stands"}`,
		"escalate": `{"decision":"escalate","summary":"authority question","instructions":"ask the owner whether the contract may change"}`,
	} {
		if err := StrictValidateReviewPayload(body); err != nil {
			t.Errorf("%s: a well-formed review was refused: %v", name, err)
		}
	}
}

func TestStrictValidateReviewPayloadRejectsMalformedFindingsBeforeNormalization(t *testing.T) {
	for name, body := range map[string]string{
		// numberFindings would give these an id and a severity.
		"no id":            `{"decision":"revise","summary":"s","instructions":"i","findings":[{"severity":"major","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"no severity":      `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"no class":         `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"major","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"bad class":        `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"major","class":"style","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"code no ref":      `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"major","class":"code","claim":"c","reason":"r","correction":"y"}]}`,
		"evidence no gap":  `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"major","class":"evidence","claim":"c","reason":"r","correction":"y"}]}`,
		"duplicate id":     `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"minor","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"},{"id":"f1","severity":"minor","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"accept blocking":  `{"decision":"accept","summary":"s","findings":[{"id":"f1","severity":"blocking","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"cased decision":   `{"decision":"Accept","summary":"s"}`,
		"no summary":       `{"decision":"accept"}`,
		"empty revise":     `{"decision":"revise","summary":"s"}`,
		"code no correct":  `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"major","class":"code","claim":"c","reference":"x.go","reason":"r","proof_gap":"g"}]}`,
		"scope no correct": `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"major","class":"scope","claim":"c","reference":"x.go","reason":"r","proof_gap":"g"}]}`,
		"unknown field":    `{"decision":"accept","summary":"s","standing":"independent"}`,
		"fenced":           "```json\n{\"decision\":\"accept\",\"summary\":\"s\"}\n```",
	} {
		if err := StrictValidateReviewPayload(body); err == nil {
			t.Errorf("%s: a malformed review payload was accepted", name)
		}
	}
	// Revise and escalate must say what to do, findings or not.
	for name, body := range map[string]string{
		"revise with findings": `{"decision":"revise","summary":"s","findings":[{"id":"f1","severity":"major","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"revise blank":         `{"decision":"revise","summary":"s","instructions":"  ","findings":[{"id":"f1","severity":"major","class":"code","claim":"c","reference":"x.go","reason":"r","correction":"y"}]}`,
		"escalate":             `{"decision":"escalate","summary":"s"}`,
	} {
		if err := StrictValidateReviewPayload(body); err == nil || !containsText(err.Error(), "instructions") {
			t.Errorf("%s: a review without instructions was not refused for them: %v", name, err)
		}
	}
	for name, body := range map[string]string{
		"code":  `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"major","class":"code","claim":"c","reference":"x.go","reason":"r","proof_gap":"g"}]}`,
		"scope": `{"decision":"revise","summary":"s","instructions":"i","findings":[{"id":"f1","severity":"major","class":"scope","claim":"c","reference":"x.go","reason":"r","proof_gap":"g"}]}`,
	} {
		if err := StrictValidateReviewPayload(body); err == nil || !containsText(err.Error(), "correction") {
			t.Errorf("%s finding with only a proof_gap was not refused for its correction: %v", name, err)
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
