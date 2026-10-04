package workflow

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/validation"
)

// FINDING ACCOUNTING IS LOCATED BY STRUCTURE (DF-41A1). The report boundary
// finds the worker's accounting as a decoded JSON object in a canonical
// position, never by where the key's text appears, and a finding answered by
// more than one envelope is answered by none of them. These witnesses are
// self-contained: their fixtures are declared here.

var (
	faCode     = roles.Finding{ID: "f1", Severity: roles.Blocking, Class: roles.CodeFinding, Claim: "the locator finds the accounting", Reference: "internal/workflow/engine.go: parseFindingResponses", Reason: "it searches text"}
	faEvidence = roles.Finding{ID: "f2", Severity: roles.Major, Class: roles.EvidenceFinding, Claim: "the suite passes", Reference: "internal/workflow/engine.go", Reason: "no run on this candidate", ProofGap: "a passing run of go test ./... on this candidate"}
	faBoth     = []roles.Finding{faCode, faEvidence}

	faMoved    = map[string]bool{"internal/workflow/engine.go": true}
	faReadBack = map[string]string{"f1": "sha256:record-f1", "f2": "sha256:record-f2"}
	faBundle   = validation.Bundle{DiffDigest: "sha256:fa", Checks: []validation.Evidence{
		{Kind: "test", Command: "go", Args: []string{"test", "./..."}, Outcome: validation.Passed, ExitStatus: 0, Output: "ok", OutputDigest: validation.Digest("ok")},
	}}

	// faAccounting answers f1 and f2 validly.
	faAccounting = `{"finding_responses":[{"id":"f1","answered_by":"code","paths":["internal/workflow/engine.go"],"reason":"structural locator"},{"id":"f2","answered_by":"evidence","evidence":"go test ./...","reason":"suite ran"}]}`
)

// faAccount parses report and accounts f1 and f2 against it.
func faAccount(t *testing.T, report string) ([]findingResponse, error, findingAccount) {
	t.Helper()
	responses, err := parseFindingResponses(report)
	return responses, err, accountForFindings(faBoth, responses, faMoved, faBundle, faReadBack)
}

func faOpen(a findingAccount, id string) (openFinding, bool) {
	for _, o := range a.Open {
		if o.ID == id {
			return o, true
		}
	}
	return openFinding{}, false
}

// Control: the forms the protocol already uses still locate the accounting.
func TestFindingAccountingCanonicalFormsAreLocated(t *testing.T) {
	for name, report := range map[string]string{
		"whole report":      faAccounting,
		"final after prose": "done. " + faAccounting,
		"fenced final":      "Done.\n```json\n" + faAccounting + "\n```\n",
		"standalone":        "Summary.\n" + faAccounting + "\nNothing else changed.",
	} {
		responses, err, a := faAccount(t, report)
		if err != nil || len(responses) != 2 || !a.Settled() {
			t.Fatalf("%s: the canonical accounting was not located: err=%v responses=%+v %s", name, err, responses, a.Diagnosis())
		}
	}
}

// W14 PROSE BEFORE ACCOUNTING. The literal key in prose before the one
// canonical object neither hides nor invalidates it.
//
// Fails under the textual-key locator: the first occurrence is in prose with
// no brace before it, so it reports "not inside a JSON object".
func TestW14ProseMentioningTheKeyBeforeTheAccountingDoesNotHideIt(t *testing.T) {
	report := "I answered both findings; the `\"finding_responses\"` member below lists them.\n\n" + faAccounting
	responses, err, a := faAccount(t, report)
	if err != nil {
		t.Fatalf("prose before the accounting made it unreadable: %v", err)
	}
	if len(responses) != 2 || !a.Settled() {
		t.Fatalf("the accounting after prose was not accounted: %+v %s", responses, a.Diagnosis())
	}
}

// W15 PROSE AFTER ACCOUNTING. A standalone canonical object followed by prose
// that mentions the key, even with an inline example, is accounted once.
func TestW15ProseMentioningTheKeyAfterTheAccountingDoesNotDuplicateIt(t *testing.T) {
	report := "Responses:\n" + faAccounting + "\n\nNote: the \"finding_responses\" object above is final; an example such as {\"finding_responses\":[{\"id\":\"f1\",\"answered_by\":\"scope\"}]} is not a second one."
	responses, err, a := faAccount(t, report)
	if err != nil || len(responses) != 2 {
		t.Fatalf("prose after the accounting changed it: err=%v %+v", err, responses)
	}
	if !a.Settled() {
		t.Fatalf("prose after the accounting changed completion: %s", a.Diagnosis())
	}
}

// W16 TWO ACCOUNTING OBJECTS. Two independently decodable canonical objects
// are ambiguous: neither is chosen and nothing is merged, so no finding is
// answered -- even one answered identically in both.
func TestW16TwoAccountingObjectsAreRefusedAsAmbiguous(t *testing.T) {
	other := `{"finding_responses":[{"id":"f1","answered_by":"code","paths":["internal/workflow/engine.go"]}]}`
	for name, report := range map[string]string{
		"standalone then final":  faAccounting + "\n" + other,
		"identical twice":        faAccounting + "\n\n" + faAccounting,
		"standalone then fenced": "First:\n" + other + "\nThen:\n```json\n" + faAccounting + "\n```",
	} {
		responses, err, a := faAccount(t, report)
		if err == nil || !strings.Contains(err.Error(), "2 finding accounting objects") {
			t.Fatalf("%s: two accounting objects were not refused as ambiguous: err=%v", name, err)
		}
		if len(responses) != 0 || a.Settled() || len(a.Open) != 2 {
			t.Fatalf("%s: an ambiguous accounting answered a finding: %+v %s", name, responses, a.Diagnosis())
		}
	}
}

// W17 NO STRUCTURED ACCOUNTING. Prose that discusses the schema, names the key
// and quotes an example mid-sentence supplies no accounting.
func TestW17ProseWithoutACanonicalObjectAccountsNothing(t *testing.T) {
	report := "The \"finding_responses\" schema wants an id and answered_by, e.g. {\"finding_responses\":[{\"id\":\"f1\",\"answered_by\":\"code\",\"paths\":[\"internal/workflow/engine.go\"]}]} in the report. I have not written mine yet."
	responses, err, a := faAccount(t, report)
	if err != nil || len(responses) != 0 {
		t.Fatalf("prose became accounting: err=%v %+v", err, responses)
	}
	if a.Settled() || len(a.Open) != 2 {
		t.Fatalf("prose discharged a finding: %s", a.Diagnosis())
	}
	for _, o := range a.Open {
		if o.Why != "no response accounted for it" {
			t.Fatalf("finding %s is open for the wrong reason: %s", o.ID, o.Why)
		}
	}
	// A nested key is not a top-level accounting member.
	if responses, err := parseFindingResponses(`{"report":` + faAccounting + `}`); err != nil || len(responses) != 0 {
		t.Fatalf("a nested accounting member was treated as the accounting: err=%v %+v", err, responses)
	}
}

// An accounting-shaped object that is an element of an enclosing JSON array is
// part of that array, not an independent canonical object, whether the array
// is laid out on one line or many. Formatting must not decide governance.
//
// Fails if the locator decodes only from '{': the multiline element then sits
// on lines of its own and reads as a standalone accounting object.
func TestFindingAccountingObjectNestedInAnArrayAccountsNothing(t *testing.T) {
	for name, report := range map[string]string{
		"multiline array, final":      "Example of the shape:\n[\n  " + faAccounting + "\n]\n",
		"multiline array, then prose": "Example:\n[\n  " + faAccounting + ",\n  {\"note\": \"x\"}\n]\nNo accounting yet.",
		"fenced multiline array":      "Example:\n```json\n[\n" + faAccounting + "\n]\n```\n",
		"one-line array":              "Example: [" + faAccounting + "]",
	} {
		responses, err, a := faAccount(t, report)
		if err != nil || len(responses) != 0 {
			t.Fatalf("%s: an array element became accounting: err=%v %+v", name, err, responses)
		}
		if a.Settled() || len(a.Open) != 2 {
			t.Fatalf("%s: an array element discharged a finding: %s", name, a.Diagnosis())
		}
	}
	// Control: an array example before a canonical final object does not
	// hide or duplicate it.
	report := "Example:\n[\n  " + faAccounting + "\n]\nMine:\n" + faAccounting
	if responses, err, a := faAccount(t, report); err != nil || len(responses) != 2 || !a.Settled() {
		t.Fatalf("an array example hid the canonical accounting: err=%v %+v %s", err, responses, a.Diagnosis())
	}
}

// W18 RUN-2 REGRESSION. The captured shape of objective 70A run 2 cycle 3:
// explanatory prose names the key before a correct final fenced object
// answering f1 and f2.
//
// Fails under the textual-key locator with "not inside a JSON object".
func TestW18Run2ProseBeforeTheFinalObjectAccountsBothFindings(t *testing.T) {
	report := `Cycle 3 complete. I replaced the locator and added the witnesses.

- f1 (code): parseFindingResponses no longer searches for the finding_responses
  key as text; it decodes JSON objects and keeps the canonical one.
- f2 (evidence): the broker ran go test ./... on this candidate and it passed.

The "finding_responses" accounting for f1 and f2 follows as the final object.

` + "```json\n" + faAccounting + "\n```\n"
	responses, err, a := faAccount(t, report)
	if err != nil {
		if strings.Contains(err.Error(), "not inside a JSON object") {
			t.Fatalf("the run-2 report was refused by the textual locator: %v", err)
		}
		t.Fatalf("the run-2 report did not parse: %v", err)
	}
	if len(responses) != 2 || !a.Settled() || len(a.Evidenced) != 1 || a.Evidenced[0] != "f2" {
		t.Fatalf("the run-2 accounting did not answer f1 and f2: %+v %s", responses, a.Diagnosis())
	}
}

// Malformed accounting values and repeated members are refused whole.
func TestFindingAccountingMalformedOrRepeatedMembersAreRefused(t *testing.T) {
	for name, report := range map[string]string{
		"repeated member": `{"finding_responses":[{"id":"f1","answered_by":"code","paths":["internal/workflow/engine.go"]}],"finding_responses":[{"id":"f2","answered_by":"evidence","evidence":"go test ./..."}]}`,
		"string value":    `{"finding_responses":"f1 and f2 are done"}`,
		"null value":      `{"finding_responses":null}`,
		"object value":    `{"finding_responses":{"id":"f1","answered_by":"code"}}`,
		"wrong element":   `{"finding_responses":["f1"]}`,
	} {
		responses, err, a := faAccount(t, "Done.\n"+report)
		if err == nil || len(responses) != 0 || a.Settled() || len(a.Open) != 2 {
			t.Fatalf("%s: a malformed accounting was not refused: err=%v %+v %s", name, err, responses, a.Diagnosis())
		}
	}
}

// W3 MALFORMED CODE RESPONSE. id + answered_by without paths stays owed.
func TestW3FindingAccountingCodeResponseWithoutPathsStaysOwed(t *testing.T) {
	_, err, a := faAccount(t, `{"finding_responses":[{"id":"f1","answered_by":"code"},{"id":"f2","answered_by":"evidence","evidence":"go test ./..."}]}`)
	if err != nil {
		t.Fatal(err)
	}
	o, open := faOpen(a, "f1")
	if !open || !strings.Contains(o.Why, "names no file") {
		t.Fatalf("a code response with no paths discharged f1: %s", a.Diagnosis())
	}
	if _, open := faOpen(a, "f2"); open {
		t.Fatalf("the valid sibling was affected: %s", a.Diagnosis())
	}
}

// W4 MALFORMED EVIDENCE RESPONSE. An evidence answer without its citation
// stays owed.
func TestW4FindingAccountingEvidenceResponseWithoutCitationStaysOwed(t *testing.T) {
	_, err, a := faAccount(t, `{"finding_responses":[{"id":"f1","answered_by":"code","paths":["internal/workflow/engine.go"]},{"id":"f2","answered_by":"evidence"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	o, open := faOpen(a, "f2")
	if !open || !strings.Contains(o.Why, "names no check that executed") {
		t.Fatalf("an evidence response with no citation discharged f2: %s", a.Diagnosis())
	}
	if _, open := faOpen(a, "f1"); open {
		t.Fatalf("the valid sibling was affected: %s", a.Diagnosis())
	}
}

// W5 DUPLICATE/CONFLICTING RESPONSE. Two envelopes for one trimmed id leave
// that finding owed, naming the duplication, whichever envelope is valid; the
// sibling with exactly one valid response is unaffected.
//
// Fails under first-wins: the valid first envelope would discharge f1.
func TestW5FindingAccountingDuplicateOrConflictingResponsesStayOwed(t *testing.T) {
	valid := `{"id":"f1","answered_by":"code","paths":["internal/workflow/engine.go"]}`
	sibling := `{"id":"f2","answered_by":"evidence","evidence":"go test ./..."}`
	for name, envelopes := range map[string]string{
		"duplicate":           valid + "," + valid,
		"answer then dispute": valid + `,{"id":"f1","answered_by":"code","disputes_class":"scope","reason":"x"}`,
		"dispute then answer": `{"id":"f1","answered_by":"code","disputes_class":"scope","reason":"x"},` + valid,
		"two answers":         valid + `,{"id":"f1","answered_by":"scope","paths":["README.md"]}`,
		"trimmed id":          valid + `,{"id":" f1 ","answered_by":"code","paths":["internal/workflow/engine.go"]}`,
	} {
		_, err, a := faAccount(t, `{"finding_responses":[`+envelopes+","+sibling+`]}`)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		o, open := faOpen(a, "f1")
		if !open || !strings.Contains(o.Why, "2 responses for it") {
			t.Fatalf("%s: a duplicated response discharged f1 or did not name the duplication: %s", name, a.Diagnosis())
		}
		if len(a.Disputes) != 0 {
			t.Fatalf("%s: a dispute was selected out of conflicting envelopes: %+v", name, a.Disputes)
		}
		if _, open := faOpen(a, "f2"); open || len(a.Open) != 1 {
			t.Fatalf("%s: the sibling with one valid response was affected: %s", name, a.Diagnosis())
		}
	}
}
