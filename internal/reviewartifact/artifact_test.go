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

// One artifact carries one envelope. A second protocol marker anywhere would
// let one artifact claim two identities, or smuggle a request or a relay
// receipt through the review path.
func TestASecondProtocolMarkerIsRefused(t *testing.T) {
	full := render(t, complete())
	for name, raw := range map[string]string{
		"a second review envelope":       full + "\n" + full,
		"a request envelope in the body": full + "\n[sensei-code:review-request]\nrequest=r-ffffffffffffffff\n",
		"a relay receipt in the body":    full + "\n[sensei-code:relayed-review]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(raw); err == nil {
				t.Fatal("an artifact carrying two protocol envelopes was accepted")
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
