package reviewartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

const payload = `{"decision":"accept","summary":"the ledger invariant holds at physical position 0","instructions":"","findings":[]}`

func complete() Artifact {
	return Artifact{
		ReviewerProvider: "chatgpt",
		TaskID:           "task-1789498413173471174",
		RequestID:        "r-0123456789abcdef",
		BaseSHA:          "91b475a172bba0257fd2ffd8a55d3edce582e883",
		CandidateDigest:  "sha256:caa4e6970000000000000000000000000000000000000000000000000000beef",
		CandidateTree:    "1b713c41d4d6ed058313ce940b0cc481e4b22b18",
		ReviewCommit:     "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
		Body:             payload,
	}
}

func render(t *testing.T, a Artifact) string {
	t.Helper()
	raw, err := a.Render()
	if err != nil {
		t.Fatalf("rendering a complete artifact: %v", err)
	}
	return raw
}

// A complete artifact survives render -> parse with every semantic field and
// the reviewer payload unchanged. If a field did not round-trip, an adapter
// that re-rendered an artifact would silently answer about something else.
func TestACompleteArtifactRoundTrips(t *testing.T) {
	want := complete()
	got, err := Parse(render(t, want))
	if err != nil {
		t.Fatalf("parsing a rendered artifact: %v", err)
	}
	for _, f := range []struct{ name, want, got string }{
		{"reviewer", want.ReviewerProvider, got.ReviewerProvider},
		{"task", want.TaskID, got.TaskID},
		{"request", want.RequestID, got.RequestID},
		{"base", want.BaseSHA, got.BaseSHA},
		{"candidate_digest", want.CandidateDigest, got.CandidateDigest},
		{"candidate_tree", want.CandidateTree, got.CandidateTree},
		{"review_commit", want.ReviewCommit, got.ReviewCommit},
		{"body", want.Body, got.Body},
	} {
		if f.got != f.want {
			t.Errorf("%s round-tripped as %q, want %q", f.name, f.got, f.want)
		}
	}
}

// Raw is the reviewer's bytes, not a normalized re-rendering of them. The
// artifact here carries leading blank space and CRLF line ends that a repairing
// parser would quietly drop.
func TestRawKeepsTheReviewersExactBytes(t *testing.T) {
	raw := "\n " + strings.ReplaceAll(render(t, complete()), "\n", "\r\n")
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if got.Raw != raw {
		t.Fatalf("Raw is %q, want the exact input bytes %q", got.Raw, raw)
	}
}

// The digest names the exact input bytes: SHA-256 of Raw, and of nothing else.
func TestDigestIsSHA256OfTheExactRawBytes(t *testing.T) {
	raw := render(t, complete())
	got, err := Parse(raw)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	sum := sha256.Sum256([]byte(raw))
	want := "sha256:" + hex.EncodeToString(sum[:])
	if got.Digest != want {
		t.Fatalf("Digest is %s, want %s", got.Digest, want)
	}
	if Digest(raw) != want {
		t.Fatalf("Digest(raw) is %s, want %s", Digest(raw), want)
	}
}

// One byte anywhere in the artifact changes the digest, including a byte the
// semantic fields do not carry. A digest that only covered the parsed fields
// would let the prose a human reads be edited in flight without trace.
func TestOneByteChangeChangesTheDigest(t *testing.T) {
	base := render(t, complete())
	baseDigest := Digest(base)
	for name, raw := range map[string]string{
		"a semantic field":         strings.Replace(base, "chatgpt", "chatgpu", 1),
		"a byte inside the body":   strings.Replace(base, "position 0", "position 1", 1),
		"trailing whitespace only": base + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if raw == base {
				t.Fatal("the mutation did not change the artifact; this case proves nothing")
			}
			if got := Digest(raw); got == baseDigest {
				t.Fatalf("a changed artifact kept digest %s", got)
			}
		})
	}
}

// Every refusal below is a semantic field that must participate. The artifact
// is otherwise complete in each case, so a passing parse means exactly one rule
// stopped applying.
func TestAnIncompleteOrMalformedArtifactIsRefusedWhole(t *testing.T) {
	full := render(t, complete())
	for name, raw := range map[string]string{
		"no reviewer line":      strings.Replace(full, "reviewer=chatgpt\n", "", 1),
		"empty reviewer":        strings.Replace(full, "reviewer=chatgpt", "reviewer=", 1),
		"reviewer out of shape": strings.Replace(full, "reviewer=chatgpt", "reviewer=ChatGPT!", 1),
		"no task line":          strings.Replace(full, "task=task-1789498413173471174\n", "", 1),
		"empty task":            strings.Replace(full, "task=task-1789498413173471174", "task=", 1),
		"no request line":       strings.Replace(full, "request=r-0123456789abcdef\n", "", 1),
		"empty request":         strings.Replace(full, "request=r-0123456789abcdef", "request=", 1),
		"no base line":          strings.Replace(full, "base=91b475a172bba0257fd2ffd8a55d3edce582e883\n", "", 1),
		"short base":            strings.Replace(full, "base=91b475a172bba0257fd2ffd8a55d3edce582e883", "base=91b475a", 1),
		"uppercase base":        strings.Replace(full, "base=91b475a172bba0257fd2ffd8a55d3edce582e883", "base=91B475A172BBA0257FD2FFD8A55D3EDCE582E883", 1),
		"no candidate digest":   strings.Replace(full, "candidate_digest=sha256:caa4e6970000000000000000000000000000000000000000000000000000beef\n", "", 1),
		"malformed candidate digest": strings.Replace(full,
			"candidate_digest=sha256:caa4e6970000000000000000000000000000000000000000000000000000beef", "candidate_digest=sha", 1),
		"no candidate tree": strings.Replace(full, "candidate_tree=1b713c41d4d6ed058313ce940b0cc481e4b22b18\n", "", 1),
		"short candidate tree": strings.Replace(full,
			"candidate_tree=1b713c41d4d6ed058313ce940b0cc481e4b22b18", "candidate_tree=1b713c4", 1),
		"uppercase candidate tree": strings.Replace(full,
			"candidate_tree=1b713c41d4d6ed058313ce940b0cc481e4b22b18", "candidate_tree=1B713C41D4D6ED058313CE940B0CC481E4B22B18", 1),
		"no review commit": strings.Replace(full, "review_commit=8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1\n", "", 1),
		"malformed review commit": strings.Replace(full,
			"review_commit=8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1", "review_commit=not-a-commit", 1),
		"empty payload":             strings.TrimSuffix(full, payload),
		"whitespace-only payload":   strings.TrimSuffix(full, payload) + "\n\t \n",
		"no envelope at all":        payload,
		"prose before the envelope": "here is my review\n" + full,
	} {
		t.Run(name, func(t *testing.T) {
			if raw == full {
				t.Fatal("the fixture equals the complete artifact; this case proves nothing")
			}
			got, err := Parse(raw)
			if err == nil {
				t.Fatalf("accepted a %s artifact as %+v", name, got)
			}
			if got != (Artifact{}) {
				t.Fatalf("a refused artifact was returned partly populated: %+v", got)
			}
		})
	}
}

// Once the reviewer payload has begun, prose is prose. A body that spells out
// identity lines must not rewrite the envelope the request is matched against.
func TestPayloadProseCannotOverrideEnvelopeIdentity(t *testing.T) {
	want := complete()
	injected := want
	injected.Body = payload + "\n\ntask=task-someone-elses\nrequest=r-ffffffffffffffff\n" +
		"base=" + strings.Repeat("b", 40) + "\n" +
		"candidate_digest=sha256:" + strings.Repeat("0", 64) + "\n" +
		"candidate_tree=" + strings.Repeat("a", 40) + "\n" +
		"review_commit=" + strings.Repeat("c", 40) + "\nreviewer=someone-else\n"
	got, err := Parse(render(t, injected))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	for _, f := range []struct{ name, want, got string }{
		{"task", want.TaskID, got.TaskID},
		{"request", want.RequestID, got.RequestID},
		{"base", want.BaseSHA, got.BaseSHA},
		{"candidate_digest", want.CandidateDigest, got.CandidateDigest},
		{"candidate_tree", want.CandidateTree, got.CandidateTree},
		{"review_commit", want.ReviewCommit, got.ReviewCommit},
		{"reviewer", want.ReviewerProvider, got.ReviewerProvider},
	} {
		if f.got != f.want {
			t.Errorf("body prose rewrote %s to %q, want the envelope's %q", f.name, f.got, f.want)
		}
	}
}

// W8 NO RESCAN. One artifact carries ONE envelope: the one it begins with. The
// entire remainder belongs to that envelope's grammar and is never rescanned as
// further mailbox artifacts, however many marker-shaped strings it holds.
//
// THIS TEST REPLACES TestASecondProtocolMarkerIsRefused, WHICH CODIFIED THE
// DEFECT. Parse counted "[sensei-code:" over the whole body and refused any
// artifact holding more than one, so a marker QUOTED in a finding made the
// review unreadable. On 2026-09-24 a well formed review from the pinned reviewer
// principal, with matching bindings and three correct blocking findings, was
// discarded in transport for exactly that reason -- a protocol that cannot
// discuss its own vocabulary has made itself undiscussable. The old test's
// premise, that a marker ANYWHERE means two identities, is what the positional
// rule refutes: identity is position zero, and text at any other offset is
// payload.
//
// The refused case the old test was really protecting is still refused, and it
// is the FIRST subtest below: prose in front of the envelope. That is what
// smuggling looks like; quoting is not.
func TestMarkerShapedPayloadIsPayloadAndYieldsExactlyOneArtifact(t *testing.T) {
	full := render(t, complete())

	// The boundary is still closed at the front. A body that does not BEGIN with
	// the envelope is not an artifact, whatever it contains further down.
	for name, raw := range map[string]string{
		"a review envelope behind prose":  "here is my review\n" + full,
		"a review envelope behind a list": "- point one\n" + full,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := Parse(raw); err == nil {
				t.Fatalf("content merely CONTAINING an envelope was read as one: %+v", got)
			}
		})
	}

	// And open behind it. Each payload below holds marker-shaped text that the
	// whole-body count would have refused.
	for name, quoted := range map[string]string{
		"a quoted review envelope":  "a bare " + Marker + " comment is classified as ordinary content",
		"a quoted request envelope": "the " + requestMarkerForTest + " envelope opens the question",
		"a quoted relay receipt":    "the " + relayMarkerForTest + " envelope opens the receipt",
		"a whole second artifact":   "the reviewer quoted an entire artifact:\n" + full,
		"several markers at once": strings.Join([]string{
			Marker, requestMarkerForTest, relayMarkerForTest, "[sensei-code:refused]",
			"[sensei-code:wake]", "[sensei-code:withdrawn]", "[sensei-code:attestation]",
		}, "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			a := complete()
			a.Body = payload + "\n\n" + quoted
			raw := render(t, a)
			got, err := Parse(raw)
			if err != nil {
				t.Fatalf("a review whose payload quotes a protocol marker was refused: %v", err)
			}
			// ONE artifact, identified by the envelope at position zero, with
			// the quoted text intact as payload rather than consumed as a
			// second artifact's envelope.
			if got.RequestID != a.RequestID || got.TaskID != a.TaskID ||
				got.CandidateTree != a.CandidateTree || got.ReviewerProvider != a.ReviewerProvider {
				t.Errorf("the quoted text rewrote the artifact's identity: %+v", got)
			}
			if got.Body != a.Body {
				t.Errorf("the payload was altered:\n got %q\nwant %q", got.Body, a.Body)
			}
			if !strings.Contains(got.Body, quoted) {
				t.Errorf("the quoted marker text did not survive as payload: %q", got.Body)
			}
			if got.Raw != raw || got.Digest != Digest(raw) {
				t.Error("the artifact no longer names the exact bytes it was given")
			}
		})
	}
}

// The markers of OTHER protocols, spelled here rather than imported.
//
// ghbridge owns them and importing it would invert this package's one dependency
// rule, so these are literals on purpose: what is being proved is that a
// marker-shaped STRING in a payload is inert, and a literal is exactly the
// marker-shaped string a reviewer would type.
const (
	requestMarkerForTest = "[sensei-code:review-request]"
	relayMarkerForTest   = "[sensei-code:relayed-review]"
)

// W3 CONTROL. Ordinary content that merely contains marker-shaped text later is
// ORDINARY CONTENT: it is not identified as an artifact of any kind.
//
// The other direction of the same rule, and the one that keeps the repair from
// becoming permissive. Without it, "stop counting markers" could be satisfied by
// a reader that accepted anything containing one.
func TestOrdinaryContentContainingAMarkerIsNotAnArtifact(t *testing.T) {
	for name, body := range map[string]string{
		"prose naming the review envelope": "I could not apply this: a bare " + Marker +
			" comment is classified as ordinary content.\n",
		"a bare marker behind one word": "note " + Marker + "\n",
		"a marker behind one space":     " x" + Marker + "\ntask=t\n",
		"a complete artifact behind prose": "quoting the reviewer below:\n" +
			render(t, complete()),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := EnvelopeAt(body, Marker); ok {
				t.Fatalf("ordinary content was identified as a review envelope: %q", body)
			}
			got, err := Parse(body)
			if err == nil {
				t.Fatalf("ordinary content was parsed as a review: %+v", got)
			}
			if got != (Artifact{}) {
				t.Fatalf("a refused body was returned partly populated: %+v", got)
			}
		})
	}
}

// W4 CONTROL, AT THE PARSER. A body whose first bytes claim the review envelope
// and whose grammar is then wrong REMAINS a failed review artifact: the error
// names the review rule it broke rather than saying "this is not a review".
//
// A7: identification precedes parsing, and the order is what keeps this
// attributable. A reader that identified an artifact only once it parsed would
// return the same "not an artifact" answer for a malformed review and for a
// shopping list, and everything downstream that must tell a bad reviewer from an
// absent one would lose the distinction.
func TestAMalformedEnvelopeAtPositionZeroStaysAReviewFailure(t *testing.T) {
	for name, tc := range map[string]struct{ raw, names string }{
		"no delimiter after the marker": {Marker + " task=t\n\n" + payload, "reviewer"},
		"identity missing":              {Marker + "\n\n" + payload, "reviewer"},
		"base out of shape": {strings.Replace(render(t, complete()),
			"base=91b475a172bba0257fd2ffd8a55d3edce582e883", "base=nope", 1), "base"},
		"no payload": {strings.TrimSuffix(render(t, complete()), payload), "payload"},
	} {
		t.Run(name, func(t *testing.T) {
			// IDENTIFIED: these bytes claim the review envelope.
			if _, ok := EnvelopeAt(tc.raw, Marker); !ok {
				t.Fatalf("the fixture does not claim the envelope, so it proves nothing: %q", tc.raw)
			}
			_, err := Parse(tc.raw)
			if err == nil {
				t.Fatal("a malformed review artifact was accepted")
			}
			// AND ATTRIBUTABLE: the diagnostic names the review rule broken.
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the diagnostic does not name %q, so the failure is not attributable "+
					"to this envelope's grammar: %v", tc.names, err)
			}
		})
	}
}

// The bound is the artifact's, and it is exact: MaxBytes parses and one byte
// more refuses.
func TestAnArtifactLargerThanTheBoundIsRefused(t *testing.T) {
	a := complete()
	head := len(render(t, a)) - len(payload)

	a.Body = payload + strings.Repeat("x", MaxBytes-head-len(payload))
	atLimit := render(t, a)
	if len(atLimit) != MaxBytes {
		t.Fatalf("the at-limit fixture is %d bytes, want exactly %d", len(atLimit), MaxBytes)
	}
	if _, err := Parse(atLimit); err != nil {
		t.Fatalf("an artifact of exactly %d bytes was refused: %v", MaxBytes, err)
	}

	over := atLimit + "x"
	if len(over) != MaxBytes+1 {
		t.Fatalf("the over-limit fixture is %d bytes, want %d", len(over), MaxBytes+1)
	}
	if _, err := Parse(over); err == nil {
		t.Fatalf("an artifact of %d bytes was accepted past the %d bound", len(over), MaxBytes)
	}
}

// The writer is bound exactly as the reader is.
//
// The largest artifact Render will produce is the largest artifact Parse will
// accept, and one byte more is refused by Render ITSELF rather than by whoever
// reads it next. A writer that could emit what its own reader rejects would let
// an over-sized artifact be produced, digested, stored and carried, and fail
// only at the far end -- the writer/reader asymmetry #181 recorded.
func TestRenderIsBoundedExactlyAsParseIs(t *testing.T) {
	a := complete()
	head := len(Marker+"\n") +
		len("task="+a.TaskID+"\n") +
		len("request="+a.RequestID+"\n") +
		len("base="+a.BaseSHA+"\n") +
		len("candidate_digest="+a.CandidateDigest+"\n") +
		len("candidate_tree="+a.CandidateTree+"\n") +
		len("review_commit="+a.ReviewCommit+"\n") +
		len("reviewer="+a.ReviewerProvider+"\n")

	a.Body = payload + strings.Repeat("x", MaxBytes-head-len(payload))
	atLimit, err := a.Render()
	if err != nil {
		t.Fatalf("Render refused an artifact of exactly %d bytes: %v", MaxBytes, err)
	}
	if len(atLimit) != MaxBytes {
		t.Fatalf("the at-limit render is %d bytes, want exactly %d", len(atLimit), MaxBytes)
	}
	// The reader agrees: the writer's largest output is not the reader's refusal.
	if _, err := Parse(atLimit); err != nil {
		t.Fatalf("Parse refused the largest artifact Render produced: %v", err)
	}

	a.Body += "x"
	over, err := a.Render()
	if err == nil {
		t.Fatalf("Render produced %d bytes, past the %d bound", len(over), MaxBytes)
	}
	if over != "" {
		t.Fatalf("a refused render returned %d bytes", len(over))
	}
	if !strings.Contains(err.Error(), "bounded at") {
		t.Fatalf("Render refused for some other reason than the bound: %v", err)
	}
}

// The verdict lives in the payload and nowhere else. A rendered artifact that
// also stated the decision in its envelope would carry two representations of
// it, agreeing only until one is edited.
func TestRenderStatesTheVerdictOnlyInThePayload(t *testing.T) {
	raw := render(t, complete())
	header := raw[:strings.Index(raw, payload)]
	for _, word := range []string{"decision", "verdict", "accept", "revise", "standing", "summary", "findings"} {
		if strings.Contains(strings.ToLower(header), word) {
			t.Errorf("the rendered envelope states %q; the verdict belongs only in the reviewer payload\n%s", word, header)
		}
	}
	if n := strings.Count(raw, `"decision"`); n != 1 {
		t.Fatalf("the rendered artifact represents the decision %d times, want once", n)
	}
}

// Render refuses what Parse refuses: one validator, used by both directions.
func TestRenderRefusesAnIncompleteArtifact(t *testing.T) {
	for name, mut := range map[string]func(*Artifact){
		"no reviewer":      func(a *Artifact) { a.ReviewerProvider = "" },
		"no task":          func(a *Artifact) { a.TaskID = "" },
		"no request":       func(a *Artifact) { a.RequestID = "" },
		"short base":       func(a *Artifact) { a.BaseSHA = "91b475a" },
		"no digest":        func(a *Artifact) { a.CandidateDigest = "" },
		"short tree":       func(a *Artifact) { a.CandidateTree = "1b713c4" },
		"no review commit": func(a *Artifact) { a.ReviewCommit = "" },
		"empty payload":    func(a *Artifact) { a.Body = "" },
	} {
		t.Run(name, func(t *testing.T) {
			a := complete()
			mut(&a)
			raw, err := a.Render()
			if err == nil {
				t.Fatalf("rendered an artifact with %s:\n%s", name, raw)
			}
			if raw != "" {
				t.Fatalf("a refused render returned %q", raw)
			}
		})
	}
}

// Artifact semantics sit beneath transport, never the other way round. A
// dependency on a delivery adapter here would make the meaning of a review
// depend on the pipe that carried it, which is the law this package exists to
// hold.
func TestThisPackageDependsOnNoTransport(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"internal/ghbridge", "internal/workflow", "internal/control", "internal/roles"}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if strings.HasSuffix(path, bad) {
						t.Errorf("%s imports %s; transport depends on artifact semantics, never the reverse", name, path)
					}
				}
			}
		}
	}
}

// A SECOND PROTOCOL MARKER IS REFUSED ANY IDENTITY, and refused the review path.
//
// THE NAME IS RETAINED DELIBERATELY. An authored invariant,
// sensei_code.reviewartifact.one_grammar_owns_what_a_review_means, binds this
// exact function as one of its required tests; renaming it would leave that
// binding naming nothing, and the awareness corpus is not this change's to edit.
// The architect owns whether the binding is renamed.
//
// WHAT IT PROVED, AND WHAT IT PROVES NOW. Until 2026-09-24 it asserted that Parse
// counted "[sensei-code:" over the whole body and refused any artifact holding
// more than one. That rule is refuted: it discarded a correct review for quoting
// the marker it was explaining. See
// TestMarkerShapedPayloadIsPayloadAndYieldsExactlyOneArtifact for the positive
// case.
//
// The PROTECTION behind it is unchanged, and is what this asserts now: a second
// marker cannot give one artifact a second identity, and cannot smuggle another
// protocol's object through the review path. Counting was one way to get that;
// position zero is a stronger one, because it also refuses the second marker
// that a count of one would have allowed through.
func TestASecondProtocolMarkerIsRefused(t *testing.T) {
	want := complete()
	full := render(t, want)

	// REFUSED ANY IDENTITY. The payload states a complete rival envelope with
	// every field different, and changes nothing: identity is read from position
	// zero and from nowhere else.
	rival := Artifact{
		ReviewerProvider: "someone-else",
		TaskID:           "task-someone-elses",
		RequestID:        "r-ffffffffffffffff",
		BaseSHA:          strings.Repeat("b", 40),
		CandidateDigest:  "sha256:" + strings.Repeat("0", 64),
		CandidateTree:    strings.Repeat("a", 40),
		ReviewCommit:     strings.Repeat("c", 40),
		Body:             payload,
	}
	injected := want
	injected.Body = payload + "\n\n" + render(t, rival)
	got, err := Parse(render(t, injected))
	if err != nil {
		t.Fatalf("parsing an artifact whose payload quotes a second envelope: %v", err)
	}
	for _, f := range []struct{ name, want, got string }{
		{"reviewer", want.ReviewerProvider, got.ReviewerProvider},
		{"task", want.TaskID, got.TaskID},
		{"request", want.RequestID, got.RequestID},
		{"base", want.BaseSHA, got.BaseSHA},
		{"candidate_digest", want.CandidateDigest, got.CandidateDigest},
		{"candidate_tree", want.CandidateTree, got.CandidateTree},
		{"review_commit", want.ReviewCommit, got.ReviewCommit},
	} {
		if f.got != f.want {
			t.Errorf("a second envelope rewrote %s to %q, want the first envelope's %q",
				f.name, f.got, f.want)
		}
	}

	// REFUSED THE REVIEW PATH. Another protocol's envelope at position zero is
	// not a review, however much of one follows it. This is the smuggling the
	// old rule named, and it is closed by position rather than by counting.
	for name, raw := range map[string]string{
		"a request envelope opening the comment": requestMarkerForTest + "\nrequest=r-ffffffffffffffff\n\n" + full,
		"a relay receipt opening the comment":    relayMarkerForTest + "\nrequest=r-ffffffffffffffff\n\n" + full,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(raw); err == nil {
				t.Fatal("another protocol's object was read as a review")
			}
		})
	}
}
