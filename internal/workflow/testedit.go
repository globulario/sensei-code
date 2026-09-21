package workflow

// Regression-test witness authority.
//
// A governed change that responsibly edits its own test file read `1 anchor over
// 2 planned files` and stayed cold (experiments/mutation-v2, E2): every
// derivation family reads non-test files only, so a test file can never be an
// anchor's subject. #312 opened this for NEW test files as prospective CREATE
// authority; M2.2 opened it for existing ones through the sibling seam -- a test
// was editable because it sat in the directory of a covered production file.
//
// DF-20 removed the sibling seam and replaced it with a DECLARED proof
// obligation (see TestWitness). Proximity both granted what nobody declared and
// refused what a plan required, and the second failure blocked DF-20 twice. The
// M2.2 predicate below (testEditGrants) is retained only because its regression
// tests live in files DF-20 had no authority to edit; nothing calls it.
//
// It is an OPERATIONAL authority, not coverage. Nothing here produces a
// CoverageAnchor, and routing carries the two kinds side by side: the subject
// holds architectural authority from a derived anchor or an authored invariant;
// the witness holds a bounded grant to be edited or created. The grant
// authorizes producing regression evidence. It does not establish that the test
// is sufficient, correct, relevant, or passing.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/globulario/sensei-code/internal/report"
	"github.com/globulario/sensei-code/internal/session"
)

// testEditFacts are the facts about F at the pinned world the grant is bound
// to and the candidate's F is inspected against.
type testEditFacts struct {
	Package     string          `json:"package"`
	Imports     map[string]bool `json:"imports"`
	Constraints []string        `json:"constraints,omitempty"` // //go:build and // +build lines, verbatim, in order
}

// testEditGrant is one admissible existing-test edit: which file, beside which
// governed subject, at which world, with which base bytes and facts -- and by WHICH
// instrument that subject was governed.
//
// The evidence class and identity are recorded rather than reduced to the bare fact of a
// grant. A grant is an authority record; one that said only "the neighbour was covered"
// could not later answer which instrument established it, and the two are not
// interchangeable -- a derived anchor is recomputed against the world, an authored
// invariant is admitted knowledge about it.
type testEditGrant struct {
	Path     string        `json:"path"`
	Covering string        `json:"covering"`
	World    string        `json:"world"`
	BaseHash string        `json:"base_sha256"`
	Facts    testEditFacts `json:"facts"`
	// CoveringEvidence is evidenceDerived or evidenceAuthored: which instrument
	// established that Covering is governed at World.
	CoveringEvidence string `json:"covering_evidence"`
	// CoveringIdentity is what that instrument named -- the derivation requirement(s) or
	// the invariant id(s). Never empty in a grant: an identity that cannot be named is
	// not evidence.
	CoveringIdentity []string `json:"covering_identity"`

	// --- DF-20: the grant is bound to ONE proof obligation ------------------
	//
	// Binding is which task, which objective and which plan this grant belongs
	// to. A grant that named only a path and a world would be re-usable by any
	// later run at the same base, and "the same files at the same commit" is not
	// the same proof obligation.
	Binding planBinding `json:"binding,omitempty"`
	// Declared is the plan's witness declaration this grant answers, verbatim.
	// Kept whole so a resume can prove the recorded grant was issued for the
	// declaration the resumed plan still carries, rather than for one that has
	// since been edited into something else.
	//
	// Declared.Operation decides what BaseHash and Facts mean:
	//
	//	edit   BaseHash is sha256 of the test's bytes at World, and Facts are
	//	       that file's own package, imports and build constraints there.
	//	create BaseHash is empty -- the file is absent at World, so there are no
	//	       bytes to hash -- and Facts are the DECLARED envelope: the package
	//	       the created file must have and the imports it may take.
	Declared TestWitness `json:"declared,omitempty"`
}

// TestWitness is a plan's explicit declaration that ONE exact regression test is
// the witness for ONE exact governed production subject (DF-20).
//
// It is the whole of the repair. Before it, a test file became editable because
// it sat in a directory beside a covered production file, which is an authority
// nobody declared and which no proof obligation bounds: the directory decides,
// so every sibling test in it is equally close and equally editable. DF-20 was
// blocked twice by the other end of the same rule -- an admissible production
// plan whose required witnesses lived where the proximity rule could not reach,
// and one of which did not exist yet at all.
//
// The relationship this type states is:
//
//	governed production claim -> required witness -> exact test path
//
// and never
//
//	test file is nearby -> directory is trusted -> siblings become editable.
//
// A declaration grants nothing on its own. It is a claim the predicate in
// testWitnessGrants checks against the pinned world and against the evidence
// already governing the subject it names.
type TestWitness struct {
	// Path is the exact *_test.go path. Not a directory and not a pattern.
	Path string `json:"path"`
	// Operation is witnessEdit or witnessCreate, read by membership: an
	// operation absent from witnessOperations is UNRESOLVED, never "some other
	// operation".
	Operation string `json:"operation"`
	// Role is the closed regression-test role the operation requires. Declared
	// rather than inferred from the operation so a plan that means one and says
	// the other is refused instead of silently corrected.
	Role string `json:"role"`
	// Subject is the exact governed production file whose behaviour this test is
	// required to prove. It is where the authority comes from; the test path is
	// only where the authority goes.
	Subject string `json:"subject"`
	// Package is the package clause a CREATED witness must declare. Ignored for
	// an edit, whose package is measured from its bytes at the pinned base.
	Package string `json:"package,omitempty"`
	// Dependencies are the imports a CREATED witness may take, beyond the role's
	// novel allowance. Ignored for an edit, which may take no novel import at
	// all.
	Dependencies []string `json:"dependencies,omitempty"`
}

// The two legitimate instruments of production governance. Named, not booleaned: the
// grant must be able to say which one it used.
const (
	evidenceDerived  = "derived"
	evidenceAuthored = "authored"
)

// authoredEvidence is per-file authored governance observed at ONE world.
//
// The world travels with it because a grant is bound to a world, and authored evidence
// read at another revision describes a different program. ByFile is keyed by the exact
// planned path, which is the same per-file relation preflight uses when it reports a file
// governed -- deliberately not directory-level, not package-wide, and not "an invariant
// somewhere mentions this name".
type authoredEvidence struct {
	World  string
	ByFile map[string][]string
}

// governs reports the identities by which file is authored-governed at world, or nothing.
func (a authoredEvidence) governs(world, file string) ([]string, bool) {
	if strings.TrimSpace(a.World) == "" || a.World != world || a.ByFile == nil {
		return nil, false
	}
	var ids []string
	for _, id := range a.ByFile[path.Clean(strings.TrimSpace(file))] {
		if t := strings.TrimSpace(id); t != "" {
			ids = append(ids, t)
		}
	}
	if len(ids) == 0 {
		return nil, false
	}
	sort.Strings(ids)
	return ids, true
}

// governedNeighbour is one candidate S with the instrument that governs it.
type governedNeighbour struct {
	file     string
	evidence string
	identity []string
}

// testEditRecord is the TestEditGranted payload.
type testEditRecord struct {
	World  string          `json:"world"`
	Grants []testEditGrant `json:"grants"`
}

const roleGoRegressionTestEdit = "go-regression-test-edit"

// testEditGrants applies EXISTING_TEST_EDIT_ADMISSIBLE(F, S, W) to the plan.
//
// SUPERSEDED BY DF-20 AND NO LONGER AN AUTHORITY SOURCE. Nothing in the engine
// calls it: testWitnessGrants is the only predicate that issues a grant. It is
// kept compiling because its regression tests live in
// internal/workflow/authored_neighbour_test.go and
// internal/workflow/w3_grant_proof_test.go, which this task held no test-edit
// authority over -- deleting the predicate without deleting the tests that
// exercise it would have left the package unbuildable. Predicate and tests must
// be removed together by a task granted edit authority over both files.
//
// The rule it encodes -- a test is editable because it sits in the directory of
// a governed file -- is exactly what DF-20 removed: proximity is not a proof
// obligation, so it both granted what nobody declared and refused what a plan
// required.
//
// F must be a planned *_test.go positively present at W; S must be a planned
// file in F's directory that a derived anchor covers at W; F and S must
// declare the same package at W (an external test package `p_test` is a
// foreign package and gets no grant). Everything is read at W through the
// reader; nothing is read from the working tree. Anything the predicate
// cannot establish leaves F ungranted, silently to routing and named in the
// returned reasons for the record.
func testEditGrants(ctx context.Context, world string, planned []string, covered []CoverageAnchor, authored authoredEvidence, read worldReader) ([]testEditGrant, []string) {
	if read == nil {
		return nil, nil
	}
	isPlanned := map[string]bool{}
	for _, f := range planned {
		isPlanned[path.Clean(strings.TrimSpace(f))] = true
	}
	// WHAT COUNTS AS A GOVERNED NEIGHBOUR, and this is the whole repair.
	//
	// The rule is unchanged: a planned same-package test beside a planned, governed
	// production neighbour at this world. What changes is that "governed" no longer means
	// "covered by a derived anchor" alone. cmd/sensei-code/control.go is governed by an
	// authored invariant -- preflight reports it sufficient with two anchors -- and the
	// grant refused it for that reason only, which is the recorded "two coverage
	// instruments, one word" defect arriving at the grant predicate.
	//
	// Derived is preferred where both hold: it is recomputed against the world being
	// assessed, where an authored anchor is admitted knowledge about it.
	byDir := map[string][]governedNeighbour{}
	seen := map[string]bool{}
	add := func(f, evidence string, identity []string) {
		f = path.Clean(f)
		if !isPlanned[f] || seen[f] || strings.HasSuffix(f, "_test.go") {
			return
		}
		seen[f] = true
		byDir[path.Dir(f)] = append(byDir[path.Dir(f)], governedNeighbour{file: f, evidence: evidence, identity: identity})
	}
	for _, c := range covered {
		add(c.File, evidenceDerived, []string{string(c.Requirement)})
	}
	for _, f := range planned {
		c := path.Clean(strings.TrimSpace(f))
		if ids, ok := authored.governs(world, c); ok {
			add(c, evidenceAuthored, ids)
		}
	}
	for _, list := range byDir {
		sort.Slice(list, func(i, j int) bool { return list[i].file < list[j].file })
	}

	var grants []testEditGrant
	var reasons []string
	for _, raw := range planned {
		f := path.Clean(strings.TrimSpace(raw))
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := read(ctx, world, f)
		if err != nil {
			if confirmedMissing(err) {
				reasons = append(reasons, f+": absent at the pinned world (a CREATE is #312's case, not an edit)")
			} else {
				reasons = append(reasons, f+": unreadable at the pinned world; presence not established")
			}
			continue
		}
		facts, err := testFacts(src)
		if err != nil {
			reasons = append(reasons, f+": cannot be read as Go at the pinned world: "+err.Error())
			continue
		}
		siblings := byDir[path.Dir(f)]
		if len(siblings) == 0 {
			reasons = append(reasons, f+": no planned file in its directory is governed at the pinned world, by a derived anchor or by an authored invariant"+staleAuthoredNote(authored, world))
			continue
		}
		granted := false
		for _, n := range siblings {
			ssrc, err := read(ctx, world, n.file)
			if err != nil {
				continue
			}
			sfacts, err := parseGoFacts(ssrc)
			if err != nil {
				continue
			}
			if sfacts.Package != facts.Package {
				reasons = append(reasons, fmt.Sprintf("%s: declares package %q, its governed sibling %s declares %q (a foreign-package test is not the owner's evidence)", f, facts.Package, n.file, sfacts.Package))
				continue
			}
			sum := sha256.Sum256(src)
			grants = append(grants, testEditGrant{Path: f, Covering: n.file, World: world,
				BaseHash: hex.EncodeToString(sum[:]), Facts: facts,
				CoveringEvidence: n.evidence, CoveringIdentity: n.identity})
			granted = true
			break
		}
		_ = granted
	}
	return grants, reasons
}

// testFacts reads a test file's package, imports, and build constraints.
func testFacts(src []byte) (testEditFacts, error) {
	pf, err := parseGoFacts(src)
	if err != nil {
		return testEditFacts{}, err
	}
	return testEditFacts{Package: pf.Package, Imports: pf.Imports, Constraints: buildConstraints(src)}, nil
}

// buildConstraints returns the //go:build and // +build lines that precede
// the package clause, verbatim and in order.
func buildConstraints(src []byte) []string {
	var out []string
	for _, line := range strings.Split(string(src), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "package ") {
			break
		}
		if strings.HasPrefix(t, "//go:build") || strings.HasPrefix(t, "// +build") {
			out = append(out, t)
		}
	}
	return out
}

// diffFileStates reads the candidate diff through the repository's one
// Git-aware parser, so a granted path containing a space is seen exactly as
// Git wrote it and cannot slip past inspection by failing to match.
func diffFileStates(diff string) (touched, created, deleted, renamed map[string]bool) {
	touched, created, deleted, renamed = map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range report.FromDiff(diff).Files {
		touched[f.Path] = true
		switch f.Status {
		case report.Added:
			created[f.Path] = true
		case report.Deleted:
			deleted[f.Path] = true
		case report.Renamed:
			renamed[f.Path] = true
			if f.OldPath != "" {
				renamed[f.OldPath] = true
				touched[f.OldPath] = true
			}
		}
	}
	return touched, created, deleted, renamed
}

// renderTestEditGrants states, for the worker, the edit authority it already
// operates under -- the same law as M2.1: authority that constrains
// execution must be visible at the execution boundary.
func renderTestEditGrants(grants []testEditGrant) string {
	if len(grants) == 0 {
		return ""
	}
	var b strings.Builder
	for _, g := range grants {
		fmt.Fprintf(&b, "- EDIT %s (existing regression test, role %s)\n", g.Path, roleGoRegressionTestEdit)
		// "witness for", never "beside": what authorizes this edit is the proof
		// obligation the plan declared, not where the file sits.
		fmt.Fprintf(&b, "    witness for governed subject: %s [%s: %s]\n", g.Covering, g.CoveringEvidence, strings.Join(g.CoveringIdentity, ", "))
		fmt.Fprintf(&b, "    package: %s (may not change); build constraints: %s (may not change)\n", g.Facts.Package, constraintsOrNone(strings.Join(g.Facts.Constraints, "; ")))
		fmt.Fprintf(&b, "    ALLOWED IMPORTS (exactly what it imports at the pinned world; no novel import): %s\n", strings.Join(sortedImports(g.Facts.Imports), ", "))
		fmt.Fprintf(&b, "    edit in place only: do not create, delete, or rename it\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func constraintsOrNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

func (e *Engine) setTestEditGrants(taskID string, grants []testEditGrant) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.testEdits == nil {
		e.testEdits = map[string][]testEditGrant{}
	}
	e.testEdits[taskID] = grants
}

func (e *Engine) testEditGrants(taskID string) []testEditGrant {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.testEdits[taskID]
}

// operationalFiles are the planned files a test-edit grant authorizes.
func operationalFiles(grants []testEditGrant) []string {
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.Path)
	}
	sort.Strings(out)
	return out
}

func sameTestFacts(a, b testEditFacts) bool {
	if a.Package != b.Package || len(a.Imports) != len(b.Imports) || strings.Join(a.Constraints, "\n") != strings.Join(b.Constraints, "\n") {
		return false
	}
	for imp := range a.Imports {
		if !b.Imports[imp] {
			return false
		}
	}
	return true
}

// joinGrants renders every grant kind for the worker, each under its own
// heading so none reads as another.
//
// Three headings and not one list, because the three are different authorities:
// a prospective CREATE is architectural authority over a new production surface,
// an existing-test EDIT is operational authority over regression evidence that
// already exists, and a PLANNED TEST CREATE is operational authority over one
// regression test that does not exist yet. Summing them under one heading is how
// a reader comes to believe the run is governed more broadly than it is.
func joinGrants(prospective, edits, creates string) string {
	var parts []string
	if strings.TrimSpace(prospective) != "" {
		parts = append(parts, prospective)
	}
	if strings.TrimSpace(edits) != "" {
		parts = append(parts, "EXISTING-TEST EDIT GRANTS (operational authority to edit regression evidence; not coverage, not proof the test is sufficient):\n"+edits)
	}
	if strings.TrimSpace(creates) != "" {
		parts = append(parts, "PLANNED TEST CREATE GRANTS (operational authority to create ONE declared regression test at ONE exact path; the path has no graph identity and gains none by being created):\n"+creates)
	}
	return strings.Join(parts, "\n\n")
}

// staleAuthoredNote names a world mismatch in the refusal, so authored evidence that
// exists but describes another revision is not silently indistinguishable from none.
func staleAuthoredNote(a authoredEvidence, world string) string {
	if w := strings.TrimSpace(a.World); w != "" && w != world {
		return fmt.Sprintf(" (authored evidence was observed at world %s, not %s, and evidence from another world describes another program)", w, world)
	}
	return ""
}

// setCoverageWorld records the world the coverage computation used, so a later pass
// cannot resolve a second one. Two worlds in one authorization would authorize an edit
// against bytes neither answer describes.
func (e *Engine) setCoverageWorld(taskID, world string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.coverageWorlds == nil {
		e.coverageWorlds = map[string]string{}
	}
	e.coverageWorlds[taskID] = world
}

func (e *Engine) coverageWorld(taskID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.coverageWorlds[taskID]
}

// authoredTestEditGrants applies the SAME witness predicate over the authored
// instrument only.
//
// Separate from the derived pass because of when each fact becomes available, not because
// the rule differs: derived coverage is computed before routing, authored governance after
// the per-file probe. Runs no derivations, so it costs one read per declared witness.
//
// Returns nothing when no world was recorded: a grant must be bound to the world its
// coverage was computed in, and inventing one here is exactly the substitution the
// pinned-world discipline exists to refuse.
func (e *Engine) authoredTestEditGrants(ctx context.Context, taskID string, bind planBinding, planned []string, declared []TestWitness, prospective []ProspectiveSurface, authored authoredEvidence) []testEditGrant {
	world := e.coverageWorld(taskID)
	if strings.TrimSpace(world) == "" || len(planned) == 0 || len(declared) == 0 {
		return nil
	}
	already := map[string]bool{}
	for _, g := range e.testEditGrants(taskID) {
		already[path.Clean(g.Path)] = true
	}
	grants, _ := testWitnessGrants(ctx, bind, world, planned, declared, prospective, nil, authored, gitShowAt(e.Repo.Root))
	var out []testEditGrant
	for _, g := range grants {
		if !already[path.Clean(g.Path)] {
			out = append(out, g)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// DF-20: REGRESSION-TEST WITNESS AUTHORITY.
//
// A governed repair must be able to create or edit the tests required to prove
// its own bounded claims. Before this, it could not: authority over a test file
// came from the directory it sat in, so DF-20's admissible production plan was
// blocked twice -- three of its required witnesses were existing test files with
// no governed neighbour the proximity rule could reach, one lived in a package
// that holds no production file at all, and one did not exist at the pinned base.
//
// Nothing here widens implementation authority. A witness grant is OPERATIONAL:
// it authorizes exactly one path, for exactly one operation, for exactly one
// task/objective/plan, at exactly one pinned base. It produces no CoverageAnchor,
// it makes no production surface governed, it mints no graph identity for a file
// that does not exist, and it is discharged by ordinary validation and review of
// the candidate's real bytes -- never by having been granted.
// ---------------------------------------------------------------------------

// The closed operation vocabulary. Read by membership: an operation absent here
// is UNRESOLVED, never "some other operation".
const (
	// witnessEdit is a regression test that EXISTS at the pinned base.
	witnessEdit = "edit"
	// witnessCreate is a regression test ABSENT at the pinned base.
	//
	// This is a narrow future-artifact authorization over one declared test
	// path, NOT the general prospective CREATE of #312/DF-19: it carries no
	// anchor, contributes nothing to derived coverage, and is admissible only
	// where an already-governed subject supplies the authority.
	witnessCreate = "create"
)

// roleGoRegressionTestCreate is the role a PLANNED TEST CREATE takes. Distinct
// from roleGoRegressionTest (the #312 prospective role) so neither can be
// written where the other is meant and pass for it.
const roleGoRegressionTestCreate = "go-regression-test-create"

// witnessOperation is one governed shape a declared witness may take.
type witnessOperation struct {
	// role is the only role name this operation admits.
	role string
	// pathGlob is matched against the declared path's base name.
	pathGlob string
	// novel is the only import allowance beyond the grant's envelope. An edit
	// has none: a test that exists takes exactly the imports it already had.
	novel map[string]bool
}

var witnessOperations = map[string]witnessOperation{
	witnessEdit:   {role: roleGoRegressionTestEdit, pathGlob: "*_test.go"},
	witnessCreate: {role: roleGoRegressionTestCreate, pathGlob: "*_test.go", novel: map[string]bool{"testing": true}},
}

// planBinding is the exact authority a witness grant belongs to.
//
// Path and world are not enough. Two runs over the same files at the same commit
// are two proof obligations, and a grant that could not tell them apart would let
// one run's recorded authority be replayed by another. Objective and plan are
// carried as identities rather than as their text: the comparison is equality of
// authority, and two renderings of one plan that differ by a trailing newline are
// the same plan.
type planBinding struct {
	Task      string `json:"task"`
	Objective string `json:"objective"`
	Plan      string `json:"plan"`
}

// complete reports whether every part of the binding is established. An
// incomplete binding issues no grant: authority that cannot say which obligation
// it serves is authority nothing bounds.
func (b planBinding) complete() bool {
	return strings.TrimSpace(b.Task) != "" && strings.TrimSpace(b.Objective) != "" && strings.TrimSpace(b.Plan) != ""
}

func (b planBinding) equal(o planBinding) bool {
	return strings.TrimSpace(b.Task) == strings.TrimSpace(o.Task) &&
		strings.TrimSpace(b.Objective) == strings.TrimSpace(o.Objective) &&
		strings.TrimSpace(b.Plan) == strings.TrimSpace(o.Plan)
}

// identityOf is the stable identity of a free-text authority.
func identityOf(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// planIdentity is the plan's identity: a supplied plan's own digest where it has
// one, and otherwise the digest of the plan text the architect produced.
func planIdentity(plan, digest string) string {
	if d := strings.TrimSpace(digest); d != "" {
		return d
	}
	return identityOf(plan)
}

// governedSubjects indexes, by EXACT planned path, the production files this
// plan carries that some instrument governs at world.
//
// Exact paths only. Not the directory, not the package, not "an invariant
// somewhere mentions this name" -- those are the relations DF-20 removed.
// Derived is preferred where both instruments hold: it is recomputed against the
// world being assessed, where an authored anchor is admitted knowledge about it.
func governedSubjects(world string, planned []string, covered []CoverageAnchor, authored authoredEvidence) map[string]governedNeighbour {
	eligible := map[string]bool{}
	for _, f := range planned {
		c := path.Clean(strings.TrimSpace(f))
		if productionGo(c) {
			eligible[c] = true
		}
	}
	out := map[string]governedNeighbour{}
	ids := map[string]map[string]bool{}
	// An instrument that cannot NAME what it established is not evidence, so a
	// blank identity records nothing at all. Dropping such a file afterwards
	// would be worse than never recording it: the authored pass skips whatever
	// the derived pass already claimed, so a file claimed under a blank
	// requirement would have shut out the authored invariant that did govern it.
	note := func(f, evidence string, identity []string) {
		if !eligible[f] {
			return
		}
		named := make([]string, 0, len(identity))
		for _, id := range identity {
			if t := strings.TrimSpace(id); t != "" {
				named = append(named, t)
			}
		}
		if len(named) == 0 {
			return
		}
		if n, ok := out[f]; ok && n.evidence != evidence {
			return // derived is recorded first and is preferred
		}
		if ids[f] == nil {
			ids[f] = map[string]bool{}
		}
		for _, id := range named {
			ids[f][id] = true
		}
		out[f] = governedNeighbour{file: f, evidence: evidence}
	}
	for _, c := range covered {
		note(path.Clean(strings.TrimSpace(c.File)), evidenceDerived, []string{string(c.Requirement)})
	}
	for f := range eligible {
		if _, already := out[f]; already {
			continue
		}
		if identity, ok := authored.governs(world, f); ok {
			note(f, evidenceAuthored, identity)
		}
	}
	for f, n := range out {
		identity := make([]string, 0, len(ids[f]))
		for id := range ids[f] {
			identity = append(identity, id)
		}
		sort.Strings(identity)
		n.identity = identity
		out[f] = n
	}
	return out
}

// productionGo reports whether a path is a Go production surface: compiled into
// the package and readable by every derivation family. A *_test.go is not one,
// so a test can never be the subject another test's authority is derived from.
func productionGo(file string) bool {
	return strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go")
}

// testWitnessGrants applies REGRESSION_TEST_WITNESS_ADMISSIBLE to the plan.
//
// Every clause must hold, and none of them is proximity:
//
//	A. the binding is complete -- this run can say which task, objective and
//	   plan the grant serves;
//	B. the path is declared exactly once, by this plan, as a witness;
//	C. the operation is in the closed vocabulary and the declared role is the
//	   one that operation admits;
//	D. the path is a regression-test surface (*_test.go) -- test authority never
//	   reaches a production path;
//	E. the path is one of the plan's own files, so candidate confinement already
//	   bounds it, and it is not ALSO declared as a prospective CREATE surface;
//	F. the declared subject is a Go production file this plan carries, readable
//	   as Go at the pinned world, that a derived anchor or an authored invariant
//	   governs THERE;
//	G. edit: the path is positively present at the pinned world, parses, and its
//	   bytes and facts are recorded as what the candidate is inspected against;
//	   create: the pinned world's tree provably LACKS it, and the declaration
//	   states the package and dependency envelope the created bytes must satisfy.
//
// Everything is read at the pinned world through the reader; nothing is read
// from the working tree. Anything the predicate cannot establish leaves the
// witness ungranted, silently to routing and named in the returned reasons.
func testWitnessGrants(ctx context.Context, bind planBinding, world string, planned []string, declared []TestWitness, prospective []ProspectiveSurface, covered []CoverageAnchor, authored authoredEvidence, read worldReader) ([]testEditGrant, []string) {
	if read == nil || len(declared) == 0 {
		return nil, nil
	}
	if !bind.complete() {
		// Clause A. Refused for every declaration at once: the defect is the
		// run's, not any one witness's.
		return nil, []string{"no regression-test witness is authorized: this run cannot name the task, objective and plan a grant would be bound to"}
	}
	isPlanned := map[string]bool{}
	for _, f := range planned {
		isPlanned[path.Clean(strings.TrimSpace(f))] = true
	}
	declaredCreate := map[string]bool{}
	for _, p := range prospective {
		declaredCreate[path.Clean(strings.TrimSpace(p.Path))] = true
	}
	governed := governedSubjects(world, planned, covered, authored)

	var grants []testEditGrant
	var reasons []string
	seen := map[string]bool{}
	for _, d := range declared {
		f := path.Clean(strings.TrimSpace(d.Path))
		if f == "." || strings.TrimSpace(d.Path) == "" {
			reasons = append(reasons, "a regression-test witness was declared with no path")
			continue
		}
		refuse := func(format string, a ...any) {
			reasons = append(reasons, f+": "+fmt.Sprintf(format, a...))
		}
		if seen[f] {
			refuse("is declared twice; one path takes one witness grant")
			continue
		}
		seen[f] = true
		op, known := witnessOperations[strings.TrimSpace(d.Operation)]
		if !known {
			refuse("declares operation %q, which is not an operation this build knows", d.Operation)
			continue
		}
		if strings.TrimSpace(d.Role) != op.role {
			refuse("declares role %q; the %s operation admits only %s", d.Role, d.Operation, op.role)
			continue
		}
		if matched, err := path.Match(op.pathGlob, path.Base(f)); err != nil || !matched || !strings.HasSuffix(f, "_test.go") {
			refuse("is not a regression-test surface; witness authority is evidence authority and never reaches a production path")
			continue
		}
		if !isPlanned[f] {
			refuse("is not one of this plan's files, so nothing confines the candidate to it")
			continue
		}
		if declaredCreate[f] {
			refuse("is declared both as a prospective CREATE surface and as a regression-test witness; one path takes one authority")
			continue
		}
		s := path.Clean(strings.TrimSpace(d.Subject))
		if strings.TrimSpace(d.Subject) == "" || s == "." {
			refuse("names no governed subject; a test path cannot bootstrap governance for what it purports to prove")
			continue
		}
		if !productionGo(s) {
			refuse("names %s as its governed subject, which is not a Go production surface", s)
			continue
		}
		if !isPlanned[s] {
			refuse("names governed subject %s, which this plan does not carry, so this task is authorized neither to change nor to preserve it", s)
			continue
		}
		n, ok := governed[s]
		if !ok {
			refuse("names subject %s, which no derived anchor and no authored invariant governs at the pinned world%s", s, staleAuthoredNote(authored, world))
			continue
		}
		ssrc, err := read(ctx, world, s)
		if err != nil {
			refuse("subject %s could not be read at the pinned world; a subject that is not there establishes nothing", s)
			continue
		}
		if _, err := parseGoFacts(ssrc); err != nil {
			refuse("subject %s cannot be read as Go at the pinned world: %v", s, err)
			continue
		}
		g := testEditGrant{
			Path: f, Covering: s, World: world,
			CoveringEvidence: n.evidence, CoveringIdentity: n.identity,
			Binding: bind, Declared: d,
		}
		src, readErr := read(ctx, world, f)
		switch strings.TrimSpace(d.Operation) {
		case witnessEdit:
			if readErr != nil {
				if confirmedMissing(readErr) {
					refuse("is declared as an EDIT but the pinned world's tree lacks it; a witness that does not exist yet is a %s", witnessCreate)
				} else {
					refuse("is declared as an EDIT and its presence at the pinned world could not be established")
				}
				continue
			}
			facts, err := testFacts(src)
			if err != nil {
				refuse("cannot be read as Go at the pinned world: %v", err)
				continue
			}
			sum := sha256.Sum256(src)
			g.BaseHash = hex.EncodeToString(sum[:])
			g.Facts = facts
		case witnessCreate:
			// STRICT ABSENCE. Only a read that positively established the
			// world's tree lacks the path is a create. A git failure, an
			// unreadable object or a cancelled context says nothing about
			// whether the file is there, and must never become authority to
			// create it.
			if !confirmedMissing(readErr) {
				if readErr == nil {
					refuse("is declared as a PLANNED TEST CREATE but already exists at the pinned world; declare it as an %s", witnessEdit)
				} else {
					refuse("is declared as a PLANNED TEST CREATE and its absence at the pinned world could not be established")
				}
				continue
			}
			if strings.TrimSpace(d.Package) == "" {
				refuse("is declared as a PLANNED TEST CREATE with no package; the created bytes would have nothing to be checked against")
				continue
			}
			g.Facts = declaredEnvelope(d)
		}
		grants = append(grants, g)
	}
	return grants, reasons
}

// declaredEnvelope is the shape a PLANNED TEST CREATE's bytes are inspected
// against: the package it must declare and the imports it may take.
//
// It comes from the DECLARATION and not from the subject's own imports. The
// subject supplies authority, not shape: a witness may legitimately live in
// another package from the behaviour it proves, and taking that package's import
// set as the envelope would be the proximity rule again in another form. The
// envelope is rendered to the worker before it writes a line, so a witness that
// needs an import the plan did not declare is the architect's decision to widen
// and not the worker's to assume.
func declaredEnvelope(d TestWitness) testEditFacts {
	imports := map[string]bool{}
	for _, dep := range d.Dependencies {
		if t := strings.TrimSpace(dep); t != "" {
			imports[t] = true
		}
	}
	return testEditFacts{Package: strings.TrimSpace(d.Package), Imports: imports}
}

// editGrants and createGrants split a grant set by operation, for the two
// renderings and the two inspections.
func editGrants(grants []testEditGrant) []testEditGrant {
	return grantsFor(grants, witnessEdit)
}

func createGrants(grants []testEditGrant) []testEditGrant {
	return grantsFor(grants, witnessCreate)
}

// grantsFor selects by operation, reading an unset operation as an edit -- the
// same default inspectTestWitnesses applies. The two must agree: a grant the
// rendering dropped and the inspection still enforced would leave the worker
// operating under an envelope it was never shown.
func grantsFor(grants []testEditGrant, operation string) []testEditGrant {
	var out []testEditGrant
	for _, g := range grants {
		op := strings.TrimSpace(g.Declared.Operation)
		if op == "" {
			op = witnessEdit
		}
		if op == operation {
			out = append(out, g)
		}
	}
	return out
}

// inspectTestWitnesses is the post-validation inspection of every regression
// test the candidate actually touched, against the exact grants this run holds.
//
// Two obligations, both enforced:
//
//  1. HOLD EACH GRANT TO ITS EXACT SHAPE. An edit is edited in place with its
//     package, build constraints and import set unchanged; a planned create is
//     created at that exact path with the declared package and inside the
//     declared envelope.
//  2. REFUSE THE UNDECLARED. Any *_test.go the candidate touched that holds no
//     grant ends the run. This is the negative the whole repair turns on: no
//     directory, no sibling, no filename and no "it is obviously a test" makes a
//     second test file editable.
//
// The grants are checked FIRST so a refutation names the defect rather than a
// symptom of it. Renaming a granted witness produces both failures at once --
// the grant was not edited in place, and the new path is an undeclared test --
// and "it renamed its witness" is what happened; "some unknown test appeared" is
// what that looks like from the far side. Nothing is skipped by the ordering:
// the undeclared sweep runs over the same diff immediately afterwards.
//
// The bytes are read from the candidate, never from the diff or the grant: the
// prospective authorization is discharged HERE, by what exists, and cannot
// survive as a substitute for examining it.
func inspectTestWitnesses(diff string, grants []testEditGrant, candidate func(path string) ([]byte, error)) error {
	touched, created, deleted, renamed := diffFileStates(diff)
	byPath := map[string]testEditGrant{}
	for _, g := range grants {
		byPath[path.Clean(strings.TrimSpace(g.Path))] = g
	}
	for _, g := range grants {
		f := path.Clean(strings.TrimSpace(g.Path))
		var err error
		switch strings.TrimSpace(g.Declared.Operation) {
		case witnessCreate:
			err = inspectCreatedWitness(f, g, touched, created, deleted, renamed, candidate)
		default:
			err = inspectEditedWitness(f, g, touched, created, deleted, renamed, candidate)
		}
		if err != nil {
			return err
		}
	}
	undeclared := make([]string, 0, len(touched))
	for f := range touched {
		if strings.HasSuffix(f, "_test.go") && !byPath[f].granted() {
			undeclared = append(undeclared, f)
		}
	}
	sort.Strings(undeclared)
	if len(undeclared) != 0 {
		return fmt.Errorf("test witness refuted: the candidate touches %s, which this plan declared no regression-test witness for; being a test file, or sitting beside one, authorizes nothing", strings.Join(undeclared, ", "))
	}
	return nil
}

// granted reports whether a grant was found rather than zero-valued.
func (g testEditGrant) granted() bool { return strings.TrimSpace(g.Path) != "" }

// inspectEditedWitness holds one existing-test grant to its exact shape. A grant
// whose file the candidate did not touch is not a mismatch: a plan may declare a
// witness it turns out not to need, and the grant authorized an edit rather than
// requiring one.
func inspectEditedWitness(f string, g testEditGrant, touched, created, deleted, renamed map[string]bool, candidate func(path string) ([]byte, error)) error {
	if !touched[f] {
		return nil
	}
	switch {
	case created[f]:
		return fmt.Errorf("test edit refuted: %s was granted as an EDIT of an existing file but the candidate creates it", f)
	case deleted[f]:
		return fmt.Errorf("test edit refuted: %s was granted as an EDIT but the candidate deletes it", f)
	case renamed[f]:
		return fmt.Errorf("test edit refuted: %s was granted as an EDIT but the candidate renames it", f)
	}
	after, err := candidate(f)
	if err != nil {
		return fmt.Errorf("test edit refuted: %s could not be read from the candidate: %v", f, err)
	}
	facts, err := testFacts(after)
	if err != nil {
		return fmt.Errorf("test edit refuted: %s could not be read as Go after the edit: %v", f, err)
	}
	if facts.Package != g.Facts.Package {
		return fmt.Errorf("test edit refuted: %s changed its package clause from %q to %q", f, g.Facts.Package, facts.Package)
	}
	if strings.Join(facts.Constraints, "\n") != strings.Join(g.Facts.Constraints, "\n") {
		return fmt.Errorf("test edit refuted: %s changed its build constraints (%q -> %q)", f, strings.Join(g.Facts.Constraints, "; "), strings.Join(facts.Constraints, "; "))
	}
	for _, imp := range sortedImports(facts.Imports) {
		if !g.Facts.Imports[imp] {
			return fmt.Errorf("test edit refuted: %s imports %q, which it did not import at the pinned world; the %s role admits no novel import", f, imp, roleGoRegressionTestEdit)
		}
	}
	return nil
}

// inspectCreatedWitness discharges one PLANNED TEST CREATE against the bytes the
// candidate actually produced.
//
// A declared create that was not created is refuted rather than ignored: the
// authorization was for a witness this plan said it needed, and a run that
// reaches review without it has proved nothing it claimed it would.
func inspectCreatedWitness(f string, g testEditGrant, touched, created, deleted, renamed map[string]bool, candidate func(path string) ([]byte, error)) error {
	switch {
	case !touched[f]:
		return fmt.Errorf("test witness refuted: %s was granted as a PLANNED TEST CREATE but the candidate did not create it", f)
	case deleted[f], renamed[f]:
		return fmt.Errorf("test witness refuted: %s was granted as a PLANNED TEST CREATE but the candidate deletes or renames it", f)
	case !created[f]:
		return fmt.Errorf("test witness refuted: %s was granted as a PLANNED TEST CREATE but the candidate modifies an existing file at that path", f)
	}
	src, err := candidate(f)
	if err != nil {
		return fmt.Errorf("test witness refuted: %s could not be read from the candidate: %v", f, err)
	}
	facts, err := parseGoFacts(src)
	if err != nil {
		return fmt.Errorf("test witness refuted: %s could not be read as Go: %v", f, err)
	}
	if facts.Package != g.Facts.Package {
		return fmt.Errorf("test witness refuted: %s has package %q, the declaration said %q", f, facts.Package, g.Facts.Package)
	}
	novel := witnessOperations[witnessCreate].novel
	for _, imp := range sortedImports(facts.Imports) {
		if !g.Facts.Imports[imp] && !novel[imp] {
			return fmt.Errorf("test witness refuted: %s imports %q, which is outside the declared dependencies and the %s allowance", f, imp, roleGoRegressionTestCreate)
		}
	}
	return nil
}

func sortedImports(imports map[string]bool) []string {
	out := make([]string, 0, len(imports))
	for imp := range imports {
		out = append(out, imp)
	}
	sort.Strings(out)
	return out
}

// matchTestWitnessGrants proves a recorded grant set is authority THIS resumed
// task holds: one grant per path, every path declared by the plan it is resuming
// under, every grant bound to this task, objective, plan and pinned base, and
// every grant carrying what its operation is inspected against.
//
// Grants are a SUBSET of the declarations rather than equal to them, and
// deliberately so: a declared witness the router refused leaves the run
// ungranted, which is a legitimate outcome that must stay resumable. What may
// never happen is the other direction -- a grant for a path the plan does not
// declare, or for a declaration that has since become a different one.
func matchTestWitnessGrants(bind planBinding, planned []string, declared []TestWitness, grants []testEditGrant, world string) error {
	isPlanned := map[string]bool{}
	for _, f := range planned {
		isPlanned[path.Clean(strings.TrimSpace(f))] = true
	}
	byPath := map[string]TestWitness{}
	for _, d := range declared {
		byPath[path.Clean(strings.TrimSpace(d.Path))] = d
	}
	seen := map[string]bool{}
	for _, g := range grants {
		f := path.Clean(strings.TrimSpace(g.Path))
		if seen[f] {
			return fmt.Errorf("the recorded regression-test witness authorization holds two grants for %s", f)
		}
		seen[f] = true
		if !isPlanned[f] {
			return fmt.Errorf("the recorded regression-test witness authorization names %s, which the plan does not", f)
		}
		d, ok := byPath[f]
		if !ok {
			return fmt.Errorf("the recorded grant for %s answers no witness this plan declares", f)
		}
		if !sameWitness(d, g.Declared) {
			return fmt.Errorf("the recorded grant for %s was issued for a different witness declaration", f)
		}
		if !g.Binding.equal(bind) {
			return fmt.Errorf("the recorded grant for %s is bound to another task, objective or plan", f)
		}
		if strings.TrimSpace(g.World) != strings.TrimSpace(world) {
			return fmt.Errorf("the recorded grant for %s was read at world %s, not the candidate's pinned base %s", f, shortWorldID(g.World), shortWorldID(world))
		}
		if path.Clean(strings.TrimSpace(g.Covering)) != path.Clean(strings.TrimSpace(d.Subject)) {
			return fmt.Errorf("the recorded grant for %s names subject %s; the declaration names %s", f, g.Covering, d.Subject)
		}
		if strings.TrimSpace(g.CoveringEvidence) == "" || len(g.CoveringIdentity) == 0 {
			return fmt.Errorf("the recorded grant for %s cannot name the instrument that governed %s, so it records no evidence", f, g.Covering)
		}
		if g.Facts.Imports == nil || strings.TrimSpace(g.Facts.Package) == "" {
			return fmt.Errorf("the recorded grant for %s carries no facts to inspect the candidate against", f)
		}
		switch strings.TrimSpace(d.Operation) {
		case witnessEdit:
			if strings.TrimSpace(g.BaseHash) == "" {
				return fmt.Errorf("the recorded EDIT grant for %s records no base hash, so nothing pins the bytes it was issued over", f)
			}
		case witnessCreate:
			if strings.TrimSpace(g.BaseHash) != "" {
				return fmt.Errorf("the recorded PLANNED TEST CREATE grant for %s carries base bytes for a file that is absent at the pinned base", f)
			}
			// A created witness has no bytes at the pinned base, so there is
			// nothing to re-read its envelope from: the envelope IS the
			// declaration. Recomputing it from the declaration is therefore the
			// only thing that pins it -- without this, a record whose Declared
			// still matched the plan could carry a wider Facts.Imports and the
			// created file would be inspected against an envelope the plan never
			// stated.
			if !sameTestFacts(g.Facts, declaredEnvelope(d)) {
				return fmt.Errorf("the recorded PLANNED TEST CREATE grant for %s carries an envelope the declaration does not state", f)
			}
		default:
			return fmt.Errorf("the recorded grant for %s names operation %q, which is not an operation this build knows", f, d.Operation)
		}
	}
	return nil
}

// sameWitness compares declarations field by field, dependencies as a set.
func sameWitness(a, b TestWitness) bool {
	if path.Clean(strings.TrimSpace(a.Path)) != path.Clean(strings.TrimSpace(b.Path)) ||
		strings.TrimSpace(a.Operation) != strings.TrimSpace(b.Operation) ||
		strings.TrimSpace(a.Role) != strings.TrimSpace(b.Role) ||
		path.Clean(strings.TrimSpace(a.Subject)) != path.Clean(strings.TrimSpace(b.Subject)) ||
		strings.TrimSpace(a.Package) != strings.TrimSpace(b.Package) ||
		len(a.Dependencies) != len(b.Dependencies) {
		return false
	}
	seen := map[string]bool{}
	for _, d := range a.Dependencies {
		seen[strings.TrimSpace(d)] = true
	}
	for _, d := range b.Dependencies {
		if !seen[strings.TrimSpace(d)] {
			return false
		}
	}
	return true
}

// verifyWitnessBaseFacts re-establishes, from the PINNED BASE alone, the facts
// each recorded grant was issued over.
//
// The pinned base is immutable, so this asks the same question the router asked
// and can only get the same answer -- unlike the graph, which may have been
// rebuilt while the task was not running. That is the whole distinction this
// resume rests on: base facts are re-read, current graph authority is not
// re-run, and no grant is minted either way.
//
// A planned create has no bytes here to re-read, so only its ABSENCE is
// re-established; its envelope is pinned by matchTestWitnessGrants against the
// declaration, which is the only thing that states it.
func verifyWitnessBaseFacts(ctx context.Context, grants []testEditGrant, world string, read worldReader) error {
	if len(grants) == 0 {
		return nil
	}
	if read == nil {
		return fmt.Errorf("the pinned base cannot be read, so no recorded regression-test witness grant can be re-established")
	}
	for _, g := range grants {
		f := path.Clean(strings.TrimSpace(g.Path))
		src, err := read(ctx, world, f)
		switch strings.TrimSpace(g.Declared.Operation) {
		case witnessCreate:
			if !confirmedMissing(err) {
				return fmt.Errorf("the recorded PLANNED TEST CREATE grant for %s no longer describes the pinned base: its absence there is not established", f)
			}
		default:
			if err != nil {
				return fmt.Errorf("the recorded EDIT grant for %s names a file the pinned base does not establish", f)
			}
			sum := sha256.Sum256(src)
			if hex.EncodeToString(sum[:]) != strings.TrimSpace(g.BaseHash) {
				return fmt.Errorf("the recorded base hash of %s does not match its bytes at the pinned base", f)
			}
			facts, err := testFacts(src)
			if err != nil {
				return fmt.Errorf("the recorded EDIT grant for %s names a file the pinned base cannot read as Go: %v", f, err)
			}
			if !sameTestFacts(facts, g.Facts) {
				return fmt.Errorf("the recorded facts of %s do not match the pinned base", f)
			}
		}
	}
	return nil
}

// restoreTestWitnessGrants re-establishes a resumed task's witness authority
// from the record the original run wrote, and from nothing else.
//
// Three rules, and they are the reason this is not the old re-derivation:
//
//   - NO MINTING. A task whose original run recorded no grant resumes with none,
//     however broadly the world would authorize it today. Presence of a record is
//     the only thing that makes a grant operational
//     (invariant sensei_code.workflow.repeated_resume_cannot_mint_authority).
//   - NO CURRENT GRAPH AUTHORITY. The grants are not recomputed from today's
//     anchors or invariants. The graph may have been rebuilt while the task was
//     not running, and a resume that re-derived would either lose a grant the run
//     legitimately held or hand it one the run never had.
//   - EXACTLY WHAT WAS RECORDED. The record must still be this task's, this
//     objective's, this plan's, at this pinned base, for declarations this plan
//     still makes, over base facts the pinned base still shows.
func (e *Engine) restoreTestWitnessGrants(ctx context.Context, task session.Interrupted, bind planBinding, planned []string, declared []TestWitness, world string, read worldReader) error {
	if len(task.TestEditRecord) == 0 {
		return nil
	}
	var rec testEditRecord
	if err := json.Unmarshal(task.TestEditRecord, &rec); err != nil {
		return fmt.Errorf("cannot resume %s: the recorded regression-test witness authorization is unreadable: %v", task.TaskID, err)
	}
	if strings.TrimSpace(rec.World) != strings.TrimSpace(world) {
		return fmt.Errorf("cannot resume %s: the recorded regression-test witness authorization was read at world %s, not the candidate's pinned base %s", task.TaskID, shortWorldID(rec.World), shortWorldID(world))
	}
	if err := matchTestWitnessGrants(bind, planned, declared, rec.Grants, world); err != nil {
		return fmt.Errorf("cannot resume %s: %w", task.TaskID, err)
	}
	if err := verifyWitnessBaseFacts(ctx, rec.Grants, world, read); err != nil {
		return fmt.Errorf("cannot resume %s: %w", task.TaskID, err)
	}
	e.setTestEditGrants(task.TaskID, rec.Grants)
	return nil
}

// renderTestWitnessCreates states, for the worker, the PLANNED TEST CREATE
// authority it already operates under -- the same law as the edit grants and the
// prospective grants: authority that constrains execution must be visible at the
// execution boundary, or the worker discovers the envelope only by being refuted.
func renderTestWitnessCreates(grants []testEditGrant) string {
	if len(grants) == 0 {
		return ""
	}
	novel := sortedImports(witnessOperations[witnessCreate].novel)
	var b strings.Builder
	for _, g := range grants {
		allowed := append(sortedImports(g.Facts.Imports), novel...)
		sort.Strings(allowed)
		fmt.Fprintf(&b, "- CREATE %s (planned regression test, role %s)\n", g.Path, roleGoRegressionTestCreate)
		fmt.Fprintf(&b, "    witness for governed subject: %s [%s: %s]\n", g.Covering, g.CoveringEvidence, strings.Join(g.CoveringIdentity, ", "))
		fmt.Fprintf(&b, "    absent at the pinned world %s: this path has no graph identity and gains none by being created\n", shortWorldID(g.World))
		fmt.Fprintf(&b, "    package: %s (must be exactly this)\n", g.Facts.Package)
		fmt.Fprintf(&b, "    ALLOWED IMPORTS (the plan's declared dependencies + the role allowance): %s\n", strings.Join(allowed, ", "))
		fmt.Fprintf(&b, "    create this exact path and no other: not a sibling, not a directory, not a production file\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// resumeWitnessAuthority re-establishes a restarted process's witness authority
// from the record, and is the only restoration path Resume takes.
//
// THE OBJECTIVE IDENTITY COMES FIRST, AND FROM THE RECORD. A grant is bound to
// the task, the objective and the plan, and a fresh process holds none of the
// first two in memory: planBindingFor would hash an empty objective, the
// binding would be incomplete, and every grant the original run legitimately
// held would be refused -- not because the authority changed but because the
// process forgot what the task was. recordObjectiveIfAbsent restores the
// recorded task bytes, which is what the submission hashed.
//
// IfAbsent, and under the resumption's own provenance, in both directions that
// matter: a process that still holds the submission keeps its provenance
// exactly, so a resume cannot demote a task a human asked for; a restarted one
// gets ResumedGoverned, which establishes no human. Neither invents authority.
//
// The plan is passed as the resumed bound rather than re-read, and the world
// reader is the caller's: Resume supplies the pinned base through Git, and
// nothing here asks the current graph anything.
func (e *Engine) resumeWitnessAuthority(ctx context.Context, task session.Interrupted, bound resumedBound, world string, read worldReader) error {
	e.recordObjectiveIfAbsent(task.TaskID, Objective{Text: task.Task, Provenance: ResumedGoverned})
	bind := e.planBindingFor(task.TaskID, bound.Plan)
	return e.restoreTestWitnessGrants(ctx, task, bind, bound.Files, bound.Witnesses, world, read)
}
