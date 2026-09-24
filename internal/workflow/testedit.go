package workflow

// M2.2 -- existing-test edit authority. See docs/work/m2.2-existing-test-edit-authority.md.
//
// A governed change that responsibly edits its own EXISTING test file beside a
// covered production file read `1 anchor over 2 planned files` and stayed cold
// (experiments/mutation-v2, E2): every derivation family reads non-test files
// only, so a test file can never be an anchor's subject. #312 opened this for
// NEW test files as prospective CREATE authority. This is the sibling seam.
//
// It is an OPERATIONAL authority, not coverage. Nothing here produces a
// CoverageAnchor, and routing carries the two kinds side by side: S holds
// architectural authority from a derived anchor; F holds a bounded grant to be
// edited. The grant authorizes editing regression evidence. It does not
// establish that the test is sufficient, correct, relevant, or passing.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// inspectTestEdits checks every granted test file in the candidate against
// its exact grant: edited in place (not created, deleted, or renamed), same
// package clause, same build constraints, imports a subset of the imports at
// the pinned world. A grant whose file the candidate did not touch is not a
// mismatch. The first mismatch is returned as an error beginning
// "test edit refuted:" and is terminal, as a prospective refutation is.
func inspectTestEdits(diff string, grants []testEditGrant, candidate func(path string) ([]byte, error)) error {
	if len(grants) == 0 {
		return nil
	}
	touched, created, deleted, renamed := diffFileStates(diff)
	for _, g := range grants {
		f := g.Path
		if !touched[f] {
			continue
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
		imports := make([]string, 0, len(facts.Imports))
		for imp := range facts.Imports {
			imports = append(imports, imp)
		}
		sort.Strings(imports)
		for _, imp := range imports {
			if !g.Facts.Imports[imp] {
				return fmt.Errorf("test edit refuted: %s imports %q, which it did not import at the pinned world; the %s role admits no novel import", f, imp, roleGoRegressionTestEdit)
			}
		}
	}
	return nil
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

// matchTestEditGrants proves a recorded grant set is one grant per planned
// test path it names, all inside the plan, none duplicated, at the pinned
// world, with facts present. Not every planned test must hold a grant -- an
// ungranted test is legitimate -- but no grant may name a file the plan does
// not, and none may lack the facts it will be inspected against.
func matchTestEditGrants(planned []string, grants []testEditGrant, world string) error {
	isPlanned := map[string]bool{}
	for _, f := range planned {
		isPlanned[path.Clean(strings.TrimSpace(f))] = true
	}
	seen := map[string]bool{}
	for _, g := range grants {
		f := path.Clean(strings.TrimSpace(g.Path))
		if !isPlanned[f] {
			return fmt.Errorf("the recorded test-edit authorization names %s, which the plan does not", f)
		}
		if seen[f] {
			return fmt.Errorf("the recorded test-edit authorization holds two grants for %s", f)
		}
		seen[f] = true
		if strings.TrimSpace(g.World) != strings.TrimSpace(world) || strings.TrimSpace(g.Covering) == "" || strings.TrimSpace(g.BaseHash) == "" || g.Facts.Imports == nil || g.Facts.Package == "" {
			return fmt.Errorf("the recorded grant for %s is not bound to world %s with a covering subject, base hash and facts", f, shortWorldID(world))
		}
	}
	return nil
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
		imports := make([]string, 0, len(g.Facts.Imports))
		for imp := range g.Facts.Imports {
			imports = append(imports, imp)
		}
		sort.Strings(imports)
		fmt.Fprintf(&b, "- EDIT %s (existing regression test, role %s)\n", g.Path, roleGoRegressionTestEdit)
		fmt.Fprintf(&b, "    beside covered subject: %s\n", g.Covering)
		fmt.Fprintf(&b, "    package: %s (may not change); build constraints: %s (may not change)\n", g.Facts.Package, constraintsOrNone(strings.Join(g.Facts.Constraints, "; ")))
		fmt.Fprintf(&b, "    ALLOWED IMPORTS (exactly what it imports at the pinned world; no novel import): %s\n", strings.Join(imports, ", "))
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

// restoreTestEditGrants re-establishes recorded test-edit grants on resume,
// or refuses NON-DESTRUCTIVELY.
//
// The record alone is not authority: routing does not re-run on resume, so a
// stale, damaged or edited local record would otherwise become operational
// authority by being present (sensei-code#101 review). DERIVED grants are
// therefore RECOMPUTED from the pinned world by the same predicate routing used
// -- F and S planned, same directory and package at W, S covered by a derived
// anchor at W, F's bytes and facts at W -- and the recorded DERIVED portion must
// match that recomputation exactly: same paths, same covering subjects, same
// base hashes, same facts.
//
// EXACTLY AND ONLY the DERIVED portion. The recomputation is one instrument, and
// AUTHORED grants were established by another -- per-file authored governance
// read from the graph at routing time. Comparing the DERIVED recomputation with
// the whole record reported "the pinned world authorises 0" about a world it
// never measured, and destroyed the task it refused (2026-09-23 measurement; see
// the restoration section below). AUTHORED authority is not adjudicated here at
// all: until qualifying historical provenance exists -- a later, separately
// governed run -- it refuses for that reason, by name.
//
// Every refusal is a typed *RestorationRefusal, which terminateRun routes to an
// invocation terminal that PRESERVES the task. Nothing is installed unless the
// WHOLE recorded authority was verified: a partial set would continue execution
// under authority nobody established.
func (e *Engine) restoreTestEditGrants(task session.Interrupted, recomputed []testEditGrant, planned []string, world string) error {
	if len(task.TestEditRecord) == 0 {
		if len(recomputed) != 0 {
			// The world now authorises what the run never recorded: the run
			// did not operate under it, so neither does its resumption.
			e.setTestEditGrants(task.TaskID, nil)
		}
		return nil
	}
	var rec testEditRecord
	if err := json.Unmarshal(task.TestEditRecord, &rec); err != nil {
		return restorationRefused(task.TaskID, RestorationInstrumentRecord, RestorationRecordUnreadable,
			"the recorded test-edit authorization is unreadable: "+err.Error())
	}
	if strings.TrimSpace(rec.World) != strings.TrimSpace(world) {
		return restorationRefused(task.TaskID, RestorationInstrumentRecord, RestorationWorldMismatch,
			fmt.Sprintf("the recorded test-edit authorization was read at world %s, not the candidate's pinned base %s",
				shortWorldID(rec.World), shortWorldID(world)))
	}
	if err := matchTestEditGrants(planned, rec.Grants, world); err != nil {
		return restorationRefused(task.TaskID, RestorationInstrumentRecord, RestorationRecordInconsistent, err.Error())
	}

	recorded := partitionByInstrument(rec.Grants)
	fresh := partitionByInstrument(recomputed)
	counts := recorded.counts(len(fresh.derived))
	// THE RECOMPUTATION IS THE DERIVED INSTRUMENT AND NOTHING ELSE. Resume
	// recomputes through coverageAtWorld, which is handed no authored evidence,
	// so every grant it produces is derived. Checked rather than assumed: if
	// that ever stops being true, the comparison below would silently become
	// the cross-instrument comparison this repair removed.
	if len(fresh.authored)+len(fresh.unbound) > 0 {
		return restorationRefusedAfterPartition(task.TaskID, RestorationInstrumentUnreadable, RestorationInstrumentAmbiguous,
			"the pinned-base recomputation produced authority this resume cannot attribute to the DERIVED instrument: "+
				grantPaths(append(append([]testEditGrant{}, fresh.authored...), fresh.unbound...)), counts)
	}
	// A record that cannot prove which instrument produced a grant is refused
	// BEFORE any comparison. Deciding it from the current derivation's output --
	// "the recomputation authorises it, so call it derived" -- would be inferring
	// provenance from the very evidence the provenance is supposed to qualify.
	if len(recorded.unbound) > 0 {
		return restorationRefusedAfterPartition(task.TaskID, RestorationInstrumentUnreadable, RestorationInstrumentAmbiguous,
			"the record does not identify the instrument that established: "+grantPaths(recorded.unbound)+
				"; this resume does not infer it from current graph state or from current derivation output", counts)
	}
	// UNCONDITIONAL BEFORE CONDITIONAL, and this is the ordering the refusal
	// turns on. AUTHORED authority is a different instrument and is NOT judged by
	// what the DERIVED one would have said: a record consistent with the pinned
	// checkout proves CONSISTENCY, not ADMISSION, and this resume holds no
	// qualifying historical provenance to verify it against.
	//
	// So this check precedes the DERIVED comparison rather than following it. A
	// DERIVED disagreement is CONDITIONAL -- a record agreeing about paths,
	// identity, bytes and facts resolves it -- while an unverifiable AUTHORED
	// grant is UNCONDITIONAL: no amount of DERIVED agreement verifies it, because
	// the DERIVED instrument cannot adjudicate AUTHORED authority at all. Where a
	// path recorded under AUTHORED is ALSO authorised by the DERIVED instrument at
	// the pinned base, derivedDisagreement sees N recomputed against zero recorded
	// DERIVED grants and reported the mismatch first, typing the refusal
	// instrument=derived while the blocker that could not be resolved by any
	// record edit went unnamed. That is a conditional disagreement reported in
	// place of an unconditional one, and it points the operator at a repair that
	// could never have worked.
	//
	// It is NOT a weakening of the DERIVED comparison: nothing is installed on
	// this path either, and a record with no AUTHORED grants still reaches the
	// comparison below unchanged. It is also not conditional on that comparison
	// having passed -- an AUTHORED grant refuses whether the DERIVED portion
	// agrees or not. Continuing with the verified DERIVED subset alone would
	// execute under a reduced authority set the original run never operated under.
	if len(recorded.authored) > 0 {
		return restorationRefusedAfterPartition(task.TaskID, RestorationInstrumentAuthored, RestorationAuthoredUnverifiable,
			"the record holds existing-test edit authority established by AUTHORED production governance ("+
				grantPaths(recorded.authored)+") and no qualifying historical provenance exists for this resume to verify it "+
				"against; DERIVED recomputation cannot adjudicate it, and execution does not continue under the DERIVED subset alone"+
				derivedAlsoAuthorises(recorded.authored, fresh.derived), counts)
	}
	// With no AUTHORED and no unbound grants the recorded authority IS its
	// DERIVED portion, and the exact comparison is unchanged: same equality, same
	// identity, same hashes, same facts, same non-destructive refusal.
	if err := derivedDisagreement(recorded, fresh.derived); err != nil {
		return restorationRefusedAfterPartition(task.TaskID, RestorationInstrumentDerived, RestorationDerivedMismatch,
			err.Error(), counts)
	}
	// Whole-set installation: that DERIVED portion is the whole recorded
	// authority, and it was verified exactly. Nothing partial is ever installed.
	e.setTestEditGrants(task.TaskID, recomputed)
	return nil
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

// joinGrants renders both grant kinds for the worker, each under its own
// heading so neither reads as the other.
func joinGrants(prospective, edits string) string {
	var parts []string
	if strings.TrimSpace(prospective) != "" {
		parts = append(parts, prospective)
	}
	if strings.TrimSpace(edits) != "" {
		parts = append(parts, "EXISTING-TEST EDIT GRANTS (operational authority to edit regression evidence; not coverage, not proof the test is sufficient):\n"+edits)
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

// authoredTestEditGrants applies the SAME predicate over the authored instrument only.
//
// Separate from the derived pass because of when each fact becomes available, not because
// the rule differs: derived coverage is computed before routing, authored governance after
// the per-file probe. Runs no derivations, so it costs one read per candidate neighbour.
//
// Returns nothing when no world was recorded: a grant must be bound to the world its
// coverage was computed in, and inventing one here is exactly the substitution the
// pinned-world discipline exists to refuse.
func (e *Engine) authoredTestEditGrants(ctx context.Context, taskID string, planned []string, authored authoredEvidence) []testEditGrant {
	world := e.coverageWorld(taskID)
	if strings.TrimSpace(world) == "" || len(planned) == 0 {
		return nil
	}
	already := map[string]bool{}
	for _, g := range e.testEditGrants(taskID) {
		already[g.Path] = true
	}
	grants, _ := testEditGrants(ctx, world, planned, nil, authored, gitShowAt(e.Repo.Root))
	var out []testEditGrant
	for _, g := range grants {
		if !already[g.Path] {
			out = append(out, g)
		}
	}
	return out
}

// A RESTORATION FAILURE MUST NOT DESTROY THE OBLIGATION IT WAS PROTECTING, AND
// NO RESTORATION PREDICATE MAY ADJUDICATE AUTHORITY PRODUCED BY ONE INSTRUMENT
// USING EVIDENCE PRODUCED BY ANOTHER.
//
// Measured 2026-09-23. Test-edit grants are composed at ROUTING from TWO
// instruments: DERIVED grants, recomputed from a derivation at the task's
// pinned base, and AUTHORED grants, read from the live Sensei graph per planned
// file and merged into the same record. Resume recomputes through the DERIVED
// instrument ONLY and then demanded exact equality with the WHOLE record, so a
// task whose record held AUTHORED grants refused with
//
//	cannot resume <task>: the pinned world authorises 0 existing-test edit(s)
//	and the record holds N; the record is not re-established
//
// ...as WorkflowFailed, which FindInterrupted reads as final. Two defects in one
// sentence:
//
//   - THE DIAGNOSIS IS FALSE. "The pinned world authorises 0" is a statement
//     about ONE INSTRUMENT phrased as a statement about the world. The
//     unresolved condition is that historical AUTHORED authority cannot be
//     adjudicated by DERIVED recomputation at all.
//   - THE REFUSAL IS DESTRUCTIVE. task-1789960053774525922 (2026-09-21, 0 vs 7)
//     left the resumable set permanently, removed by the safety check that
//     existed to protect it.
//
// The class was already named in the file that broke it: testedit.go's grant
// predicate carries the repair for "two coverage instruments, one word".
// That repair was applied at the GRANT predicate and never at the RESTORE one.
//
// What this file adds is the boundary, not the cure. DERIVED restoration is
// unchanged and still exact. AUTHORED restoration REFUSES -- truthfully, naming
// the instrument whose historical provenance this resume cannot independently
// verify -- until a qualifying provenance anchor exists, which is a later,
// separately governed run. A record that cannot say which instrument produced a
// grant refuses as an ambiguous binding rather than guessing, and the guess is
// never taken from the current graph or from the current derivation's output.
//
// Two recorded reasons survive this repair unweakened:
//
//	sensei-code#101   the record alone is not authority: routing does not re-run
//	                  on resume, so a stale, damaged or edited local record would
//	                  otherwise become operational authority by being present.
//	SIDE-EFFECT FREE  a resume computes and compares; it never records. Recording
//	                  here minted authority on a SECOND resume, which read the
//	                  first resume's write as the run's own record.

// The subjects a restoration refusal can be about. One today; named rather than
// implied so a second subject cannot arrive as an unlabelled string.
const restorationSubjectTestEdit = "existing-test-edit-authority"

// The instrument vocabulary, read by MEMBERSHIP and never by exclusion.
//
// RestorationInstrumentDerived and RestorationInstrumentAuthored are the two
// instruments that produce test-edit authority, and they carry the same names
// the grant record writes (evidenceDerived, evidenceAuthored) so a refusal and
// a grant cannot drift into two spellings of one fact.
const (
	RestorationInstrumentDerived  = evidenceDerived
	RestorationInstrumentAuthored = evidenceAuthored
	// RestorationInstrumentUnreadable is what a record names when it cannot
	// say which instrument produced a grant. It is not a third instrument: it
	// is the absence of a readable binding, stated positively.
	RestorationInstrumentUnreadable = "unavailable_or_ambiguous"
	// RestorationInstrumentRecord is the durable record itself, before any
	// instrument is consulted -- an unreadable payload, or one bound to another
	// world. Naming the record is truthful here precisely because no instrument
	// was reached.
	RestorationInstrumentRecord = "record"
)

// The binding vocabulary: WHAT about the instrument could not be read or
// verified. An operator must be able to tell provenance absence, world
// mismatch, DERIVED mismatch and instrument ambiguity apart without parsing
// prose, which is the difference this whole repair turns on.
const (
	// RestorationRecordUnreadable: the durable payload does not parse.
	RestorationRecordUnreadable = "record_unreadable"
	// RestorationWorldMismatch: the record was read at another world, and
	// evidence from another world describes another program.
	RestorationWorldMismatch = "world_mismatch"
	// RestorationRecordInconsistent: the record's own shape disagrees with the
	// plan it claims to authorize.
	RestorationRecordInconsistent = "record_inconsistent_with_plan"
	// RestorationDerivedMismatch: the DERIVED recomputation at the pinned base
	// disagrees with the record's DERIVED portion. This is the ONE binding that
	// may cite a recomputation, and it may cite it only against that portion.
	RestorationDerivedMismatch = "derived_mismatch"
	// RestorationAuthoredUnverifiable: the record holds AUTHORED authority and
	// this resume has no qualifying historical provenance to verify it against.
	// Not a mismatch: nothing was compared, because nothing may be.
	RestorationAuthoredUnverifiable = "authored_verification_unavailable"
	// RestorationInstrumentAmbiguous: the record cannot prove which instrument
	// produced a grant. The answer is not inferred from current graph state or
	// current derivation output.
	RestorationInstrumentAmbiguous = "instrument_binding_unavailable_or_ambiguous"
)

// RestorationCounts are the quantities a refusal may cite.
//
// They are split by instrument because a count is only comparable with a count
// produced BY THE SAME MEANS. RecomputedDerived may be compared with
// RecordedDerived and with nothing else; the destroyed-task diagnosis compared
// it with the whole record, which is how "the pinned world authorises 0" came
// to be said about a world nobody measured.
type RestorationCounts struct {
	RecordedDerived  int `json:"recorded_derived"`
	RecordedAuthored int `json:"recorded_authored"`
	RecordedUnbound  int `json:"recorded_unbound"`
	// RecomputedDerived is what the DERIVED instrument produced at the pinned
	// base during THIS resume.
	RecomputedDerived int `json:"recomputed_derived"`
}

// RestorationRefusal is the durable record of a WorkflowRestorationRefused
// terminal: which authority, which instrument, which binding, and the
// quantities -- if any -- that were actually measured.
type RestorationRefusal struct {
	TaskID string `json:"task_id"`
	// Subject is the authority that could not be re-established.
	Subject string `json:"subject"`
	// Instrument is the authority instrument whose binding could not be read or
	// verified, or the record itself when no instrument was reached.
	Instrument string `json:"instrument"`
	// Binding names WHAT could not be read or verified about it.
	Binding string `json:"binding"`
	// Detail is the specific, operator-readable account: which paths, which
	// identities, which worlds.
	Detail string `json:"detail"`
	// Measured is present exactly when the record was partitioned by
	// instrument, and absent otherwise. A refusal that never partitioned cites
	// no quantity rather than citing a zero it did not measure.
	Measured *RestorationCounts `json:"measured,omitempty"`
}

func (r *RestorationRefusal) Error() string {
	return fmt.Sprintf("cannot resume %s: %s. Nothing was executed under unverified authority; "+
		"the task and its candidate are preserved", r.TaskID, r.Describe())
}

// Describe is the one-line account the receipt and the terminal carry. It never
// speaks about "the world": every sentence names the instrument it measured.
func (r RestorationRefusal) Describe() string {
	what := r.Subject + " could not be re-established"
	switch r.Binding {
	case RestorationDerivedMismatch:
		what = r.Subject + ": the DERIVED instrument disagrees with the record's DERIVED authority"
	case RestorationAuthoredUnverifiable:
		what = r.Subject + ": the record holds AUTHORED authority that this resume cannot independently verify"
	case RestorationInstrumentAmbiguous:
		what = r.Subject + ": the record cannot say which instrument produced its authority"
	case RestorationWorldMismatch:
		what = r.Subject + ": the record describes another world"
	case RestorationRecordUnreadable:
		what = r.Subject + ": the record cannot be read"
	case RestorationRecordInconsistent:
		what = r.Subject + ": the record disagrees with the plan it claims to authorize"
	}
	out := fmt.Sprintf("%s [instrument %s, binding %s]: %s", what, r.Instrument, r.Binding, r.Detail)
	if m := r.Measured; m != nil {
		out += fmt.Sprintf(" (recorded: %d derived, %d authored, %d unbound; the DERIVED instrument recomputed %d at the pinned base)",
			m.RecordedDerived, m.RecordedAuthored, m.RecordedUnbound, m.RecomputedDerived)
	}
	return out
}

// validRestorationInstrument and validRestorationBinding read membership by
// enumeration. An unrecognised string is not a new instrument; it is an invalid
// record, and a reader that fell open on one would grant exactly the silent
// grandfathering this repair refuses.
func validRestorationInstrument(s string) bool {
	switch s {
	case RestorationInstrumentDerived, RestorationInstrumentAuthored,
		RestorationInstrumentUnreadable, RestorationInstrumentRecord:
		return true
	}
	return false
}

func validRestorationBinding(s string) bool {
	switch s {
	case RestorationRecordUnreadable, RestorationWorldMismatch, RestorationRecordInconsistent,
		RestorationDerivedMismatch, RestorationAuthoredUnverifiable, RestorationInstrumentAmbiguous:
		return true
	}
	return false
}

// bindingFollowsAPartition reports whether a binding is one that could only be
// reached AFTER the record was split by instrument -- and therefore whether it
// must carry the counts, and whether a binding that did not reach the split may
// not carry them.
func bindingFollowsAPartition(binding string) bool {
	switch binding {
	case RestorationDerivedMismatch, RestorationAuthoredUnverifiable, RestorationInstrumentAmbiguous:
		return true
	}
	return false
}

// ParseRestorationRefusal reads a recorded refusal back, refusing one that
// cannot say which task, which instrument or which binding it is about -- or
// one that cites quantities it could not have measured.
func ParseRestorationRefusal(raw json.RawMessage) (RestorationRefusal, error) {
	var r RestorationRefusal
	if err := json.Unmarshal(raw, &r); err != nil {
		return RestorationRefusal{}, fmt.Errorf("the restoration refusal record is unreadable: %w", err)
	}
	if strings.TrimSpace(r.TaskID) == "" {
		return RestorationRefusal{}, errors.New("the restoration refusal record names no task")
	}
	if strings.TrimSpace(r.Subject) == "" {
		return RestorationRefusal{}, errors.New("the restoration refusal record names no authority it could not restore")
	}
	if !validRestorationInstrument(r.Instrument) {
		return RestorationRefusal{}, fmt.Errorf("the restoration refusal record names no known instrument (%q)", r.Instrument)
	}
	if !validRestorationBinding(r.Binding) {
		return RestorationRefusal{}, fmt.Errorf("the restoration refusal record names no known binding (%q)", r.Binding)
	}
	if strings.TrimSpace(r.Detail) == "" {
		return RestorationRefusal{}, errors.New("the restoration refusal record does not say what could not be read or verified")
	}
	if got, want := r.Measured != nil, bindingFollowsAPartition(r.Binding); got != want {
		if want {
			return RestorationRefusal{}, fmt.Errorf("the restoration refusal record's binding %q follows a partition of the record and states no measured counts", r.Binding)
		}
		return RestorationRefusal{}, fmt.Errorf("the restoration refusal record's binding %q is reached before the record is partitioned, so its counts were never measured", r.Binding)
	}
	return r, nil
}

// restorationPartition is a grant set split by the instrument that established
// each grant.
//
// The split is the repair. A restoration predicate that cannot say which
// instrument produced a grant will compare whatever it recomputed against
// whatever it holds, and the comparison will look correct while being about two
// different questions.
type restorationPartition struct {
	derived  []testEditGrant
	authored []testEditGrant
	// unbound are grants whose instrument the record cannot prove. They are
	// never assigned to an instrument by inference: an identity that cannot be
	// named is not evidence, and a legacy record that predates the binding
	// cannot be read as either instrument without guessing.
	unbound []testEditGrant
}

func (p restorationPartition) counts(recomputedDerived int) *RestorationCounts {
	return &RestorationCounts{
		RecordedDerived:   len(p.derived),
		RecordedAuthored:  len(p.authored),
		RecordedUnbound:   len(p.unbound),
		RecomputedDerived: recomputedDerived,
	}
}

// partitionByInstrument splits grants by their recorded covering evidence,
// reading the vocabulary by membership.
func partitionByInstrument(grants []testEditGrant) restorationPartition {
	var p restorationPartition
	for _, g := range grants {
		switch {
		case !namesAnIdentity(g):
			// Recorded under an instrument it cannot name. The grant predicate
			// says an identity that cannot be named is not evidence; a
			// restoration that accepted the label alone would be trusting the
			// word "derived" rather than anything derived.
			p.unbound = append(p.unbound, g)
		case g.CoveringEvidence == evidenceDerived:
			p.derived = append(p.derived, g)
		case g.CoveringEvidence == evidenceAuthored:
			p.authored = append(p.authored, g)
		default:
			p.unbound = append(p.unbound, g)
		}
	}
	return p
}

func namesAnIdentity(g testEditGrant) bool {
	for _, id := range g.CoveringIdentity {
		if strings.TrimSpace(id) != "" {
			return true
		}
	}
	return false
}

// canonicalIdentity reduces a recorded identity vector to the claim it actually
// makes: the SET of ids the instrument named.
//
// ORDER IS NOT PART OF THE CLAIM. A derived identity is the derivation
// requirement that established the covering subject; an authored one is the
// invariant set that did. Both are produced by iterating a collection whose
// order is an artifact of how it was walked, not something either instrument
// asserts -- so two orderings of the same ids are the same evidence, and a
// comparison that read them as different would refuse a record that nothing is
// wrong with, in the destructive direction this repair exists to close.
//
// A BLANK ENTRY IS NOT AN ID. namesAnIdentity already reads a blank as naming
// nothing, and the grant predicate records that an identity that cannot be
// named is not evidence. If the exact comparison counted a blank as a
// difference, the same vector would be evidence at one predicate and a mismatch
// at the other. Duplicates collapse for the same reason: naming an id twice
// adds no second thing that was named.
//
// What this rule deliberately does NOT do is collapse a DIFFERENT id. That is
// the whole point of comparing identity at all: a record whose grant cites
// another derivation requirement was established by another question, and no
// amount of agreement about paths, bytes and facts makes it the same authority.
func canonicalIdentity(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// sameIdentity reports whether two identity vectors name the same evidence
// under canonicalIdentity.
func sameIdentity(a, b []string) bool {
	ca, cb := canonicalIdentity(a), canonicalIdentity(b)
	if len(ca) != len(cb) {
		return false
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}

// renderIdentity writes an identity vector the way the comparison read it, so a
// refusal shows the operator the canonical form that actually disagreed rather
// than a raw slice they would have to normalise themselves.
func renderIdentity(ids []string) string {
	c := canonicalIdentity(ids)
	if len(c) == 0 {
		return "no identity"
	}
	return strings.Join(c, ", ")
}

// grantPaths renders a grant set as paths with the instrument each one claims,
// so a refusal names the files an operator has to look at.
func grantPaths(grants []testEditGrant) string {
	out := make([]string, 0, len(grants))
	for _, g := range grants {
		claim := strings.TrimSpace(g.CoveringEvidence)
		if claim == "" {
			claim = "no instrument recorded"
		}
		if !namesAnIdentity(g) {
			claim += ", no identity recorded"
		}
		out = append(out, fmt.Sprintf("%s (%s)", g.Path, claim))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// crossInstrumentAgreementNote opens the sentence that names what the OTHER
// instrument says about the same path. It is a named constant because the
// witness asserts on it: a diagnostic an operator depends on must not be
// rewordable without a test noticing.
const crossInstrumentAgreementNote = "the DERIVED instrument at the pinned base also authorises"

// derivedAlsoAuthorises names the paths the record holds under AUTHORED that the
// DERIVED recomputation at the pinned base ALSO authorises, or "" when there are
// none.
//
// This exists because CHANGING WHICH REFUSAL WINS MUST NOT DELETE WHAT THE
// MESSAGE SAYS. The AUTHORED refusal now precedes the DERIVED comparison, so
// derivedDisagreement -- which names a path held under another instrument when it
// reports a cardinality difference -- is no longer reached in the one shape where
// the two instruments overlap. Without this note the operator reads a refusal
// that never mentions the DERIVED instrument at all, while the DERIVED instrument
// visibly authorises the exact path in question: an unexplained absence, which is
// the class of sentence this whole repair exists to remove.
//
// It is a DIAGNOSTIC and not a verification. Agreement by the DERIVED instrument
// does not verify an AUTHORED grant -- that is precisely why the AUTHORED refusal
// outranks it -- so this sentence says what was seen and says that it does not
// count.
func derivedAlsoAuthorises(authored, recomputedDerived []testEditGrant) string {
	recomputed := map[string]bool{}
	for _, g := range recomputedDerived {
		recomputed[path.Clean(g.Path)] = true
	}
	var both []string
	for _, g := range authored {
		if p := path.Clean(g.Path); recomputed[p] {
			both = append(both, p)
		}
	}
	if len(both) == 0 {
		return ""
	}
	sort.Strings(both)
	return "; " + crossInstrumentAgreementNote + " " + strings.Join(both, ", ") +
		", and that agreement does not verify the AUTHORED grant: one instrument's answer is not the other's provenance"
}

// derivedDisagreement compares the record's DERIVED portion with the DERIVED
// recomputation at the pinned base, and returns the disagreement or nil.
//
// Every check the exact restoration already made is kept: one grant per path,
// the same covering subject, the same base hash, the same facts. What changed is
// the POPULATION on both sides -- derived against derived -- the sentence, which
// now says which instrument measured which number, and one check the exact
// restoration never made: the same derivation IDENTITY. Agreement about paths,
// bytes and facts is agreement that the FILES are unchanged; only the identity
// says the authority over them was established by the same question.
func derivedDisagreement(recorded restorationPartition, recomputedDerived []testEditGrant) error {
	fresh := map[string]testEditGrant{}
	for _, g := range recomputedDerived {
		fresh[path.Clean(g.Path)] = g
	}
	byPath := map[string]testEditGrant{}
	for _, g := range recorded.derived {
		byPath[path.Clean(g.Path)] = g
	}
	// Where the record holds a path under ANOTHER instrument, say so. Without
	// it a legitimate derived disagreement reads as an unexplained absence,
	// which is the class of sentence this repair exists to remove.
	otherInstrument := map[string]string{}
	for _, g := range append(append([]testEditGrant{}, recorded.authored...), recorded.unbound...) {
		claim := strings.TrimSpace(g.CoveringEvidence)
		if claim == "" || !namesAnIdentity(g) {
			claim = "an instrument the record cannot name"
		}
		otherInstrument[path.Clean(g.Path)] = claim
	}
	if len(fresh) != len(byPath) {
		var notes []string
		for p := range fresh {
			if _, ok := byPath[p]; ok {
				continue
			}
			if claim, held := otherInstrument[p]; held {
				notes = append(notes, fmt.Sprintf("%s is recorded under %s", p, claim))
				continue
			}
			notes = append(notes, p+" is not in the record at all")
		}
		sort.Strings(notes)
		msg := fmt.Sprintf("the DERIVED instrument authorises %d existing-test edit(s) at the pinned base and the record holds %d DERIVED grant(s)",
			len(fresh), len(byPath))
		if len(notes) > 0 {
			msg += ": " + strings.Join(notes, "; ")
		}
		return errors.New(msg)
	}
	for _, g := range recorded.derived {
		f, ok := fresh[path.Clean(g.Path)]
		switch {
		case !ok:
			return fmt.Errorf("the DERIVED instrument does not authorise the recorded edit of %s at the pinned base", g.Path)
		case f.Covering != g.Covering:
			return fmt.Errorf("the record says %s is covered beside %s; the DERIVED instrument says %s", g.Path, g.Covering, f.Covering)
		case !sameIdentity(f.CoveringIdentity, g.CoveringIdentity):
			// THE IDENTITY IS PART OF THE EQUALITY, not a label beside it. The
			// partition asks only whether a grant can name AN identity, which is
			// the question "is this attributable to an instrument at all". It is
			// not the question "is it the same evidence the pinned base produces
			// now", and a record that agreed about the path, the covering
			// subject, the bytes and the facts while citing ANOTHER derivation
			// requirement would otherwise be installed as re-established
			// authority. A stale or edited record is exactly the population
			// sensei-code#101 names: the record alone is not authority.
			return fmt.Errorf("the record says %s is governed beside %s by derivation identity [%s]; the DERIVED instrument at the pinned base names [%s]",
				g.Path, g.Covering, renderIdentity(g.CoveringIdentity), renderIdentity(f.CoveringIdentity))
		case f.BaseHash != g.BaseHash:
			return fmt.Errorf("the recorded base hash of %s does not match its bytes at the pinned base", g.Path)
		case !sameTestFacts(f.Facts, g.Facts):
			return fmt.Errorf("the recorded facts of %s do not match the pinned base", g.Path)
		}
	}
	return nil
}

// restorationRefused builds a refusal that never reached the instrument split,
// and therefore cites no quantity.
func restorationRefused(taskID, instrument, binding, detail string) *RestorationRefusal {
	return &RestorationRefusal{
		TaskID: taskID, Subject: restorationSubjectTestEdit,
		Instrument: instrument, Binding: binding, Detail: detail,
	}
}

// restorationRefusedAfterPartition builds a refusal that did split the record,
// and therefore states what it counted on each side.
func restorationRefusedAfterPartition(taskID, instrument, binding, detail string, counts *RestorationCounts) *RestorationRefusal {
	r := restorationRefused(taskID, instrument, binding, detail)
	r.Measured = counts
	return r
}
