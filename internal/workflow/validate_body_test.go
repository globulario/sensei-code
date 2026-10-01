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
		// The schema-valid refusal the answerer's W3 control posts unchanged
		// (internal/answerer/answerer_test.go), so that control proves byte
		// preservation for a body production would accept.
		"answerer W3 refusal": `{"decision":"escalate","summary":"I cannot answer this from the request alone",` +
			`"human_question":"Which base should this request be read against?","recommendation":"1",` +
			`"options":[{"id":"1","label":"Name the base","description":"Re-send the request with its base."},` +
			`{"id":"2","label":"Withdraw","description":"Withdraw the request."}]}`,
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

func TestStrictValidateArchitectureBodyAcceptsEachTestEditOperation(t *testing.T) {
	for _, op := range []string{"edit", "create", "delete", "rename"} {
		body := `{` + proceedCore + `,"test_edits":[{"path":"a_test.go","operation":"` + op + `"}]}`
		if err := StrictValidateArchitectureBody(body); err != nil {
			t.Errorf("operation %q was refused: %v", op, err)
		}
	}
}

// The written contract says an absent operation, or one outside the closed
// vocabulary, declares NOTHING, and that an omitted field is checked only once
// the candidate exists. Such a body is well formed, so the strict validator
// must not suppress it.
func TestStrictValidateArchitectureBodyAcceptsADeclarativeNoOpTestEdit(t *testing.T) {
	for name, edit := range map[string]string{
		"omitted op":     `{"path":"a_test.go"}`,
		"empty op":       `{"path":"a_test.go","operation":""}`,
		"padded op":      `{"path":"a_test.go","operation":" edit "}`,
		"cased op":       `{"path":"a_test.go","operation":"Edit"}`,
		"omitted fields": `{"path":"a_test.go","operation":"edit"}`,
		"no path":        `{"operation":"edit"}`,
	} {
		body := `{` + proceedCore + `,"test_edits":[` + edit + `]}`
		if err := StrictValidateArchitectureBody(body); err != nil {
			t.Errorf("%s: a test edit the contract reads as declaring nothing was refused: %v", name, err)
		}
	}
}

// A field the contract reserves for one decision, carried by another, makes the
// body state two decisions. Each case is a complete body plus exactly that one
// field, and the refusal must name it.
func TestStrictValidateArchitectureBodyRejectsFieldsReservedForAnotherDecision(t *testing.T) {
	const reply = `"decision":"reply","message":"m"`
	const escalate = `"decision":"escalate","summary":"s","human_question":"q","recommendation":"1",` + escalateOptions
	const surfaces = `"prospective_surfaces":[{"path":"internal/n/n.go","package":"n","role":"go-library-package","covering":"internal/m/m.go"}]`
	const testEdits = `"test_edits":[{"path":"internal/x/x_test.go","operation":"edit","package":"x","imports":["testing"]}]`
	for name, c := range map[string]struct{ body, field string }{
		"proceed+human_question": {`{` + proceedCore + `,"human_question":"q"}`, "human_question"},
		"proceed+recommendation": {`{` + proceedCore + `,"recommendation":"1"}`, "recommendation"},
		"proceed+options":        {`{` + proceedCore + `,` + escalateOptions + `}`, "options"},
		"proceed+message":        {`{` + proceedCore + `,"message":"m"}`, "message"},
		"reply+human_question":   {`{` + reply + `,"human_question":"q"}`, "human_question"},
		"reply+recommendation":   {`{` + reply + `,"recommendation":"1"}`, "recommendation"},
		"reply+options":          {`{` + reply + `,` + escalateOptions + `}`, "options"},
		"reply+plan":             {`{` + reply + `,"plan":"p"}`, "plan"},
		"escalate+plan":          {`{` + escalate + `,"plan":"p"}`, "plan"},
		"escalate+message":       {`{` + escalate + `,"message":"m"}`, "message"},
		"reply+summary":          {`{` + reply + `,"summary":"s"}`, "summary"},
		"reply+steps":            {`{` + reply + `,"steps":["one"]}`, "steps"},
		"reply+consequences":     {`{` + reply + `,"consequences":"c"}`, "consequences"},
		"reply+files":            {`{` + reply + `,"files":["a.go"]}`, "files"},
		"reply+mode":             {`{` + reply + `,"mode":"modify"}`, "mode"},
		"reply+invariants":       {`{` + reply + `,"related_invariants":["inv.x"]}`, "related_invariants"},
		"reply+surfaces":         {`{` + reply + `,` + surfaces + `}`, "prospective_surfaces"},
		"reply+test_edits":       {`{` + reply + `,` + testEdits + `}`, "test_edits"},
		"reply+claims":           {`{` + reply + `,"claims":[{"statement":"s","source":"graph"}]}`, "claims"},
		"escalate+steps":         {`{` + escalate + `,"steps":["one"]}`, "steps"},
		"escalate+consequences":  {`{` + escalate + `,"consequences":"c"}`, "consequences"},
		"escalate+files":         {`{` + escalate + `,"files":["a.go"]}`, "files"},
		"escalate+mode":          {`{` + escalate + `,"mode":"modify"}`, "mode"},
		"escalate+invariants":    {`{` + escalate + `,"related_invariants":["inv.x"]}`, "related_invariants"},
		"escalate+surfaces":      {`{` + escalate + `,` + surfaces + `}`, "prospective_surfaces"},
		"escalate+test_edits":    {`{` + escalate + `,` + testEdits + `}`, "test_edits"},
	} {
		err := StrictValidateArchitectureBody(c.body)
		if err == nil || !containsText(err.Error(), c.field) {
			t.Errorf("%s: a field reserved for another decision was not refused for it: %v", name, err)
		}
	}
	// Control: the bases are well formed, and an empty reserved field is the
	// field left out, as the contract's own shape lists every key.
	for name, body := range map[string]string{
		"reply":           `{` + reply + `}`,
		"escalate":        `{` + escalate + `}`,
		"proceed, blanks": `{` + proceedCore + `,"message":"","human_question":"","recommendation":"","options":[]}`,
		"escalate, blank": `{` + escalate + `,"plan":""}`,
		"reply, blanks":   `{` + reply + `,"summary":"","plan":"","steps":[],"consequences":"","files":[],"mode":"","related_invariants":[],"prospective_surfaces":[],"test_edits":[],"claims":[]}`,
		"escalate+claims": `{` + escalate + `,"claims":[{"statement":"s","source":"repository"}]}`,
		"proceed, full":   `{` + proceedCore + `,"related_invariants":["inv.x"],` + surfaces + `,` + testEdits + `}`,
	} {
		if err := StrictValidateArchitectureBody(body); err != nil {
			t.Errorf("control %s: refused: %v", name, err)
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

// TestStrictValidateArchitectureBodyAppliesTheRolePathAndPackageRules pins
// strict validation to the canonical role/path predicate and the static package
// clause each role requires. Every refusal is a declaration the closed role set
// names, so only the path shape or package clause can be what refuses it.
func TestStrictValidateArchitectureBodyAppliesTheRolePathAndPackageRules(t *testing.T) {
	surface := func(p, pkg, role, covering string) string {
		c := ""
		if covering != "" {
			c = `,"covering":"` + covering + `"`
		}
		return `{` + proceedCore + `,"prospective_surfaces":[{"path":"` + p + `","package":"` + pkg + `","role":"` + role + `"` + c + `}]}`
	}
	for name, body := range map[string]string{
		"regression test, non-test path":  surface("internal/x/x.go", "x", "go-regression-test", ""),
		"regression test, non-go path":    surface("internal/x/x_test.txt", "x", "go-regression-test", ""),
		"library package, test path":      surface("internal/n/n_test.go", "n", "go-library-package", "internal/m/m.go"),
		"command package, test path":      surface("cmd/n/main_test.go", "main", "go-command-package", "cmd/m/main.go"),
		"library package, non-go path":    surface("internal/n/n.txt", "n", "go-library-package", "internal/m/m.go"),
		"command package, non-main":       surface("cmd/n/main.go", "n", "go-command-package", "cmd/m/main.go"),
		"library package, main":           surface("internal/n/n.go", "main", "go-library-package", "internal/m/m.go"),
		"library package, not identifier": surface("internal/n/n.go", "n-pkg", "go-library-package", "internal/m/m.go"),
		"library package, keyword":        surface("internal/n/n.go", "func", "go-library-package", "internal/m/m.go"),
		"library package, padded clause":  surface("internal/n/n.go", " n", "go-library-package", "internal/m/m.go"),
	} {
		if err := StrictValidateArchitectureBody(body); err == nil {
			t.Errorf("%s: a declaration outside the role's path or package rule was accepted", name)
		}
	}
	for name, body := range map[string]string{
		"regression test":         surface("internal/x/x_test.go", "x", "go-regression-test", ""),
		"regression test covered": surface("internal/x/x_test.go", "x", "go-regression-test", "internal/x/x.go"),
		"library package":         surface("internal/n/n.go", "n", "go-library-package", "internal/m/m.go"),
		"command package":         surface("cmd/n/main.go", "main", "go-command-package", "cmd/m/main.go"),
	} {
		if err := StrictValidateArchitectureBody(body); err != nil {
			t.Errorf("control %s: a declaration the role admits was refused: %v", name, err)
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

// Every field the contract reserves for a decision is one the refusal can see:
// a reserved field architectureFieldsCarried does not report is a field the
// strict validator would let any decision carry.
func TestArchitectureFieldsCarriedSeesEveryReservedField(t *testing.T) {
	var full architectureDecision
	if err := strictDecodeModelJSON(`{"decision":"proceed","message":"m","summary":"s","plan":"p","steps":["one"],
	 "consequences":"c","files":["a.go"],"mode":"modify","related_invariants":["inv.x"],
	 "prospective_surfaces":[{"path":"a/a.go"}],"test_edits":[{"path":"a_test.go"}],
	 "human_question":"q","recommendation":"1","options":[{"id":"1"}],"claims":[{"statement":"s"}]}`, &full); err != nil {
		t.Fatal(err)
	}
	carried := architectureFieldsCarried(full)
	if len(carried) != len(architectureDecisionFields) {
		t.Errorf("carried reports %d fields, the contract reserves %d", len(carried), len(architectureDecisionFields))
	}
	for _, f := range architectureDecisionFields {
		if !carried[f.field] {
			t.Errorf("reserved field %s is not seen when carried", f.field)
		}
	}
}
