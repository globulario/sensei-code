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
// or refuses.
//
// The record alone is not authority: routing does not re-run on resume, so a
// stale, damaged or edited local record would otherwise become operational
// authority by being present. The grants are therefore RECOMPUTED from the
// pinned world by the same predicate routing used -- F and S planned, same
// directory and package at W, S covered by a derived anchor at W, F's bytes
// and facts at W -- and the record must match the recomputation exactly:
// same paths, same covering subjects, same base hashes, same facts. Any
// difference, in either direction, refuses the resume (sensei-code#101
// review).
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
		return fmt.Errorf("cannot resume %s: the recorded test-edit authorization is unreadable: %v", task.TaskID, err)
	}
	if strings.TrimSpace(rec.World) != strings.TrimSpace(world) {
		return fmt.Errorf("cannot resume %s: the recorded test-edit authorization was read at world %s, not the candidate's pinned base %s", task.TaskID, shortWorldID(rec.World), shortWorldID(world))
	}
	if err := matchTestEditGrants(planned, rec.Grants, world); err != nil {
		return fmt.Errorf("cannot resume %s: %w", task.TaskID, err)
	}
	fresh := map[string]testEditGrant{}
	for _, g := range recomputed {
		fresh[path.Clean(g.Path)] = g
	}
	if len(fresh) != len(rec.Grants) {
		return fmt.Errorf("cannot resume %s: the pinned world authorises %d existing-test edit(s) and the record holds %d; the record is not re-established", task.TaskID, len(fresh), len(rec.Grants))
	}
	for _, g := range rec.Grants {
		f, ok := fresh[path.Clean(g.Path)]
		switch {
		case !ok:
			return fmt.Errorf("cannot resume %s: the pinned world does not authorise the recorded edit of %s", task.TaskID, g.Path)
		case f.Covering != g.Covering:
			return fmt.Errorf("cannot resume %s: the record says %s is covered beside %s; the pinned world says %s", task.TaskID, g.Path, g.Covering, f.Covering)
		case f.BaseHash != g.BaseHash:
			return fmt.Errorf("cannot resume %s: the recorded base hash of %s does not match its bytes at the pinned world", task.TaskID, g.Path)
		case !sameTestFacts(f.Facts, g.Facts):
			return fmt.Errorf("cannot resume %s: the recorded facts of %s do not match the pinned world", task.TaskID, g.Path)
		}
	}
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
