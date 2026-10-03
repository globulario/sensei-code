package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/globulario/sensei-code/internal/report"
)

// ProspectiveSurface is a plan's declaration that it will CREATE a file that
// does not exist at the pinned world (sensei#312).
//
// A file that does not exist cannot be observed, so no derivation can cover
// it. Coverage for it is PROSPECTIVE: established facts about a covering
// surface S in the same directory, plus this declaration, authorize a bounded
// create. The declaration is a claim the predicate checks against S's bytes at
// the pinned world; it grants nothing on its own.
type ProspectiveSurface struct {
	Path         string   `json:"path"`
	Package      string   `json:"package"`
	Role         string   `json:"role"`
	Dependencies []string `json:"dependencies,omitempty"`
	// Covering names the one existing, covered file S at the pinned world a
	// new-package declaration is admitted against. It is REQUIRED for the
	// go-library-package and go-command-package roles: a directory absent at
	// the pinned world holds no surface, so S must be named and then satisfy
	// the role's structural rule. For go-regression-test it is optional and,
	// when given, restricts the covering surface to that file. For
	// go-existing-package it is REQUIRED and names an existing non-test Go
	// file in the created file's own directory and package.
	Covering string `json:"covering,omitempty"`
}

// prospectiveRole is one governed shape a created file may take. The set is
// closed and read by membership: a role name absent from prospectiveRoles is
// UNRESOLVED, never "some other role".
type prospectiveRole struct {
	// pathGlob is matched against the file's base name.
	pathGlob string
	// novel is the only import set the role admits beyond S's own imports.
	novel map[string]bool
	// newPackage marks a production role for a Go package directory ABSENT at
	// the pinned world. Its covering surface is named by the declaration and
	// lies outside the new directory; see newPackageGrants.
	newPackage bool
	// existingPackage marks the production role for one non-test Go file in
	// a package directory PRESENT at the pinned world. Its covering surface is
	// named by the declaration and lies in the same directory and package;
	// see existingPackageGrant.
	existingPackage bool
}

// The closed role set. go-regression-test is the only test role; the two
// new-package roles and go-existing-package are production roles and never
// admit a *_test.go.
const (
	roleGoRegressionTest  = "go-regression-test"
	roleGoLibraryPackage  = "go-library-package"
	roleGoCommandPackage  = "go-command-package"
	roleGoExistingPackage = "go-existing-package"
)

var prospectiveRoles = map[string]prospectiveRole{
	roleGoRegressionTest:  {pathGlob: "*_test.go", novel: map[string]bool{"testing": true}},
	roleGoLibraryPackage:  {pathGlob: "*.go", novel: map[string]bool{}, newPackage: true},
	roleGoCommandPackage:  {pathGlob: "*.go", novel: map[string]bool{}, newPackage: true},
	roleGoExistingPackage: {pathGlob: "*.go", novel: map[string]bool{}, existingPackage: true},
}

// roleAdmitsPath reports whether f has the role's path shape. A production
// role never admits a test file: go-regression-test is the only test role.
func roleAdmitsPath(role prospectiveRole, f string) bool {
	base := path.Base(f)
	if matched, err := path.Match(role.pathGlob, base); err != nil || !matched {
		return false
	}
	return !(role.newPackage || role.existingPackage) || !strings.HasSuffix(base, "_test.go")
}

// prospectiveFacts is what was read from S at the pinned world: its package
// clause and its import set. Nothing here comes from the working tree.
type prospectiveFacts struct {
	Package string          `json:"package"`
	Imports map[string]bool `json:"imports"`
}

// errNotAtWorld is the one read failure that establishes ABSENCE: the pinned
// world's tree was consulted and holds no entry at the path. Every other
// failure (git unavailable, an unreadable object, a cancelled context) says
// nothing about whether the path exists there, and a reader must not let it
// pass for absence: a file whose existence cannot be established is neither
// present nor confirmed missing, and it receives no authority of either kind.
var errNotAtWorld = errors.New("not at the pinned world")

// confirmedMissing reports whether err positively established that the path
// is absent at the pinned world.
func confirmedMissing(err error) bool { return errors.Is(err, errNotAtWorld) }

// worldReader returns the bytes of a path at the pinned world. A path the
// world's tree provably lacks returns an error wrapping errNotAtWorld; any
// other failure is an unclassified read failure. It exists so the predicate
// can be tested without a repository; the engine supplies gitShowAt.
type worldReader func(ctx context.Context, world, file string) ([]byte, error)

// gitShowAt reads `git show <world>:<file>` in root. It never consults the
// working tree. When show fails, absence is established separately by listing
// the world's tree at the path: an empty listing from a tree git could read is
// the only thing that says "missing"; a listing that fails leaves the read
// unclassified.
func gitShowAt(root string) worldReader {
	return func(ctx context.Context, world, file string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", "-C", root, "--no-optional-locks", "show", world+":"+file)
		b, err := cmd.Output()
		if err == nil {
			return b, nil
		}
		ls := exec.CommandContext(ctx, "git", "-C", root, "--no-optional-locks", "ls-tree", "--full-tree", world, "--", file)
		out, lsErr := ls.Output()
		if lsErr == nil && len(bytes.TrimSpace(out)) == 0 {
			return nil, fmt.Errorf("git show %s:%s: %w", world, file, errNotAtWorld)
		}
		return nil, fmt.Errorf("git show %s:%s: unclassified read failure: %w", world, file, err)
	}
}

// prospectiveGrant is one admissible declaration: the anchor the router reads
// and the facts about S the post-creation inspection checks the created file
// against. It is recorded verbatim in the session (event.ProspectiveGranted)
// so a resumed task inspects against the same facts.
type prospectiveGrant struct {
	Surface  ProspectiveSurface `json:"surface"`
	Covering string             `json:"covering"`
	Facts    prospectiveFacts   `json:"facts"`
	// Anchor is the first admissible anchor over the covering surface, and
	// Anchors is every one of them. A covering surface may carry several
	// derivations with different requirements; keeping only the first let the
	// filename order decide which requirement the prospective file reported,
	// and a plan could then read as uncovered for a gap a later anchor
	// answered. All are emitted, and the consumer selects by requirement.
	Anchor  CoverageAnchor   `json:"anchor"`
	Anchors []CoverageAnchor `json:"anchors,omitempty"`
	// Via names the same-directory new-package production grant a
	// go-regression-test in a newly admitted package traces through to its
	// covering surface. Empty for every other grant.
	Via string `json:"via,omitempty"`
	// Edge is the one same-plan library package a go-command-package grant
	// may import beyond its covering surface's imports. Nil for every other
	// grant, and for a command that declared no such dependency.
	Edge *prospectiveEdge `json:"edge,omitempty"`
	// Existing is the dependency envelope of a go-existing-package grant, and
	// nil for every other grant. For such a grant Facts holds the package
	// facts: the covering file's package clause and the union of the imports
	// of every covered non-test Go file in the directory with that clause.
	Existing *prospectiveExisting `json:"existing,omitempty"`
}

// prospectiveExisting is the pinned module facts a go-existing-package grant
// was decided on and the exact import envelope it authorizes. Envelope is the
// declaration's dependencies, each admitted by the closed dependency rule
// (dependencyAdmitted) against Facts.Imports, Module and Requires; the created
// file may import exactly those and nothing else.
type prospectiveExisting struct {
	Module    string   `json:"module"`
	ModuleDir string   `json:"module_dir"`
	Requires  []string `json:"requires"`
	Envelope  []string `json:"envelope"`
}

// prospectiveEdge is a command-to-library dependency edge: the module import
// path of a new go-library-package directory declared in the same plan, every
// planned production create of which was granted at the same pinned world in
// the same Go module. The module is read from that module's go.mod at the
// world; the edge is established by those facts, never by the declaration.
type prospectiveEdge struct {
	Import    string `json:"import"`
	Library   string `json:"library"`
	Module    string `json:"module"`
	ModuleDir string `json:"module_dir"`
}

// matchGrantsToDeclarations proves a recorded grant set is exactly the
// authorization for these declarations: one grant per declared path, each
// bound to that declaration, naming a covering surface, and carrying the
// pinned-world facts. No missing grants, no duplicates, no extras.
//
// It exists because a record that parses and names the right world is not
// yet a receipt. An empty or stale grant list would otherwise be restored
// intact, and a declaration with no facts behind it would then be inspected
// against nothing but the role allowance -- role alone made sufficient by a
// damaged record, which is the predicate changing across a restart.
//
// It is the ONE declaration/grant rule. Plan admission (routePlan, through
// reconcileProspectiveGrants), restoration (restoreProspectiveGrants) and
// candidate inspection (inspectProspectiveGrants) all read it, so the grant
// set that admits a plan is the grant set that would resume and inspect it.
// Every fault names the declaration it is about and why no canonical grant
// stands for it; every declaration is judged, so a plan with one ungranted
// declaration beside a granted one is refused naming only the ungranted one.
func matchGrantsToDeclarations(declared []ProspectiveSurface, grants []prospectiveGrant) error {
	byPath := map[string]prospectiveGrant{}
	count := map[string]int{}
	for _, g := range grants {
		f := path.Clean(strings.TrimSpace(g.Anchor.File))
		count[f]++
		if count[f] == 1 {
			byPath[f] = g
		}
	}
	var faults []error
	seen := map[string]bool{}
	for _, d := range declared {
		f := path.Clean(strings.TrimSpace(d.Path))
		if seen[f] {
			faults = append(faults, fmt.Errorf("declared prospective surface %s is declared more than once, so no one grant can be its canonical authorization", f))
			continue
		}
		seen[f] = true
		if err := grantFault(f, d, count[f], byPath, declared); err != nil {
			faults = append(faults, err)
		}
	}
	extras := make([]string, 0, len(byPath))
	for f := range byPath {
		if !seen[f] {
			extras = append(extras, f)
		}
	}
	sort.Strings(extras)
	for _, f := range extras {
		faults = append(faults, fmt.Errorf("the recorded prospective authorization holds a grant for %s, which no declaration names", f))
	}
	if len(faults) != 0 {
		return errors.Join(faults...)
	}
	return oneEdgePerCommand(grants)
}

// grantFault is matchGrantsToDeclarations for one declaration f, given how
// many recorded grants name f.
func grantFault(f string, d ProspectiveSurface, n int, byPath map[string]prospectiveGrant, declared []ProspectiveSurface) error {
	switch {
	case n == 0:
		return fmt.Errorf("declared prospective surface %s (role %s, covering %q) holds no recorded grant: no canonical grant was derived for it at the pinned world", f, d.Role, d.Covering)
	case n > 1:
		return fmt.Errorf("declared prospective surface %s holds %d recorded grants, not exactly one", f, n)
	}
	g := byPath[f]
	if !sameSurface(g.Surface, d) {
		return fmt.Errorf("the recorded grant for declared prospective surface %s was issued for a different declaration", f)
	}
	if strings.TrimSpace(g.Covering) == "" || strings.TrimSpace(g.Facts.Package) == "" || g.Facts.Imports == nil {
		return fmt.Errorf("the recorded grant for declared prospective surface %s names no covering surface or carries no pinned-world facts", f)
	}
	if err := grantTracesToItsSurface(f, g, byPath); err != nil {
		return err
	}
	if err := edgeFault(f, g, declared, byPath); err != nil {
		return err
	}
	return existingFault(f, g)
}

// edgeFault is the record-side form of the command-to-library rule. A grant
// that is not a go-command-package production grant carries no edge. A
// command grant's declared dependencies beyond its covering surface's imports
// are exactly its edge's import, or nothing; and the edge must name a
// same-plan go-library-package directory in the command's own module whose
// every declared file holds a grant for its own declaration here. A command
// grant whose edge was dropped, or whose edge disagrees with the record, is
// refused rather than restored with its dependency unauthorized.
func edgeFault(f string, g prospectiveGrant, declared []ProspectiveSurface, byPath map[string]prospectiveGrant) error {
	if g.Surface.Role != roleGoCommandPackage || g.Via != "" {
		if g.Edge != nil {
			return fmt.Errorf("the recorded grant for %s carries a library edge, which only a command grant may", f)
		}
		return nil
	}
	role := prospectiveRoles[g.Surface.Role]
	var novel []string
	for _, dep := range g.Surface.Dependencies {
		if !g.Facts.Imports[dep] && !role.novel[dep] {
			novel = append(novel, dep)
		}
	}
	if g.Edge == nil {
		if len(novel) != 0 {
			return fmt.Errorf("the recorded command grant for %s declares %q beyond its covering surface but records no library edge", f, novel[0])
		}
		return nil
	}
	e := *g.Edge
	if len(novel) != 1 || novel[0] != e.Import {
		return fmt.Errorf("the recorded library edge of %s is not its one declared dependency beyond its covering surface", f)
	}
	lib, cmdDir := path.Clean(e.Library), path.Dir(f)
	if e.Library != lib || lib == cmdDir || strings.TrimSpace(e.Module) == "" || strings.TrimSpace(e.ModuleDir) == "" ||
		!underModule(e.ModuleDir, lib) || !underModule(e.ModuleDir, cmdDir) || e.Import != e.Module+"/"+moduleRel(e.ModuleDir, lib) {
		return fmt.Errorf("the recorded library edge of %s does not name a library package in its own module", f)
	}
	production := 0
	for _, d := range declared {
		df := path.Clean(strings.TrimSpace(d.Path))
		if path.Dir(df) != lib {
			continue
		}
		lg, ok := byPath[df]
		if !ok || !sameSurface(lg.Surface, d) {
			return fmt.Errorf("the recorded library edge of %s names %s, whose declared file %s holds no grant", f, lib, df)
		}
		switch {
		case d.Role == roleGoLibraryPackage && lg.Via == "":
			production++
		case d.Role == roleGoRegressionTest:
		default:
			return fmt.Errorf("the recorded library edge of %s names %s, which is not a same-plan library package", f, lib)
		}
	}
	if production == 0 {
		return fmt.Errorf("the recorded library edge of %s names %s, which holds no granted library package", f, lib)
	}
	return nil
}

// existingFault is the record-side form of the go-existing-package rule,
// checked against the recorded facts alone and never by re-reading the world:
// only a grant of that role carries an envelope, and its grant covers by the
// declared surface, a non-test Go file in the created file's own directory,
// for the package clause recorded there; its envelope is exactly the
// declaration's dependencies, each still admitted by the closed rule against
// the recorded package and module facts. A record whose envelope was widened,
// narrowed, or no longer follows from its own facts is refused.
func existingFault(f string, g prospectiveGrant) error {
	if g.Surface.Role != roleGoExistingPackage {
		if g.Existing != nil {
			return fmt.Errorf("the recorded grant for %s carries an existing-package envelope, which only a %s grant may", f, roleGoExistingPackage)
		}
		return nil
	}
	if g.Existing == nil || g.Via != "" || g.Edge != nil {
		return fmt.Errorf("the recorded existing-package grant for %s carries no envelope of its own", f)
	}
	named := strings.TrimSpace(g.Surface.Covering)
	if named == "" || path.Clean(named) != g.Covering || !sameDirectoryNonTestGo(f, g.Covering) {
		return fmt.Errorf("the recorded existing-package grant for %s does not cover by a declared non-test Go file in its own directory", f)
	}
	if g.Facts.Package != g.Surface.Package {
		return fmt.Errorf("the recorded existing-package grant for %s holds package %q for declared package %q", f, g.Facts.Package, g.Surface.Package)
	}
	x := *g.Existing
	if strings.TrimSpace(x.Module) == "" || strings.TrimSpace(x.ModuleDir) == "" || !(x.ModuleDir == path.Dir(f) || underModule(x.ModuleDir, path.Dir(f))) {
		return fmt.Errorf("the recorded existing-package grant for %s names no module governing its directory", f)
	}
	envelope := map[string]bool{}
	for _, imp := range x.Envelope {
		envelope[imp] = true
	}
	declared := map[string]bool{}
	for _, dep := range g.Surface.Dependencies {
		declared[dep] = true
	}
	if len(envelope) != len(x.Envelope) || len(envelope) != len(declared) {
		return fmt.Errorf("the recorded envelope of %s is not exactly its declared dependencies", f)
	}
	for imp := range envelope {
		if !declared[imp] {
			return fmt.Errorf("the recorded envelope of %s holds %q, which was not declared", f, imp)
		}
		if !dependencyAdmitted(imp, g.Facts.Imports, x.Module, x.Requires) {
			return fmt.Errorf("the recorded envelope of %s holds %q, which its recorded facts do not admit", f, imp)
		}
	}
	return nil
}

// sameDirectoryNonTestGo reports whether covering is a non-test Go file in
// f's own directory, other than f.
func sameDirectoryNonTestGo(f, covering string) bool {
	return covering != f && path.Dir(covering) == path.Dir(f) && path.Ext(covering) == ".go" && !strings.HasSuffix(covering, "_test.go")
}

// oneEdgePerCommand refuses a command package whose grants bind more than one
// library: a command may depend on exactly one same-plan library package. The
// refusal names both declared command files whose grants disagree, so a
// conflict is reported against declarations, not only their directory.
func oneEdgePerCommand(grants []prospectiveGrant) error {
	type binding struct{ file, library string }
	first := map[string]binding{}
	for _, g := range grants {
		if g.Edge == nil {
			continue
		}
		f := path.Clean(strings.TrimSpace(g.Anchor.File))
		dir := path.Dir(f)
		b, ok := first[dir]
		if !ok {
			first[dir] = binding{f, g.Edge.Library}
			continue
		}
		if b.library != g.Edge.Library {
			return fmt.Errorf("declared prospective surfaces %s and %s are files of one command package %s whose recorded grants bind two library packages, %s and %s: a command may depend on exactly one same-plan library package, so no canonical grant set authorizes both", b.file, f, dir, b.library, g.Edge.Library)
		}
	}
	return nil
}

// grantTracesToItsSurface is the record-side form of the new-package rules: a
// production grant covers by exactly the surface its declaration named, and a
// test that traces through a production grant names one recorded in the same
// directory, for the same package, over the same covering surface.
func grantTracesToItsSurface(f string, g prospectiveGrant, byPath map[string]prospectiveGrant) error {
	role := prospectiveRoles[g.Surface.Role]
	if named := strings.TrimSpace(g.Surface.Covering); named != "" && path.Clean(named) != g.Covering {
		return fmt.Errorf("the recorded grant for %s covers by %s, not the declared covering surface %s", f, g.Covering, named)
	}
	if role.newPackage {
		if strings.TrimSpace(g.Surface.Covering) == "" || g.Via != "" {
			return fmt.Errorf("the recorded new-package grant for %s does not cover by its declared surface alone", f)
		}
		return nil
	}
	if g.Via == "" {
		// An untraced test grant is an ordinary one, and an ordinary grant
		// covers by a surface in its own directory. A test in a new package
		// covers by an external surface only through its production trace.
		if path.Dir(g.Covering) != path.Dir(path.Clean(f)) {
			return fmt.Errorf("the recorded grant for %s covers by %s outside its directory with no production trace", f, g.Covering)
		}
		return nil
	}
	p, ok := byPath[path.Clean(g.Via)]
	if !ok || !prospectiveRoles[p.Surface.Role].newPackage || path.Dir(path.Clean(g.Via)) != path.Dir(f) ||
		p.Covering != g.Covering || p.Surface.Package != g.Surface.Package {
		return fmt.Errorf("the recorded grant for %s traces through %s, which is not a same-directory production grant over %s", f, g.Via, g.Covering)
	}
	return nil
}

// sameSurface compares declarations field by field, dependencies as a
// multiset: both sides sorted and compared element by element. A one-way
// membership test after a length check would let a duplicate stand in for a
// dropped dependency, so ["os","os"] would match a record of
// ["os","golang.org/x/sys/unix"] and restore the dependency undeclared.
func sameSurface(a, b ProspectiveSurface) bool {
	if path.Clean(strings.TrimSpace(a.Path)) != path.Clean(strings.TrimSpace(b.Path)) || a.Package != b.Package || a.Role != b.Role || a.Covering != b.Covering || len(a.Dependencies) != len(b.Dependencies) {
		return false
	}
	da := append([]string(nil), a.Dependencies...)
	db := append([]string(nil), b.Dependencies...)
	sort.Strings(da)
	sort.Strings(db)
	for i := range da {
		if da[i] != db[i] {
			return false
		}
	}
	return true
}

// prospectiveRecord is the ProspectiveGranted payload: the grants and the
// world identity they were read at. World is checked against the candidate's
// pinned base on resume; a record from another world authorizes nothing here.
type prospectiveRecord struct {
	// PlanAttemptID is the plan attempt this grant state belongs to. A record
	// carrying none predates plan attempts.
	PlanAttemptID string             `json:"plan_attempt_id,omitempty"`
	World         string             `json:"world"`
	Grants        []prospectiveGrant `json:"grants"`
}

// parseGoFacts reads a Go file's package clause and imports. Parse failure is
// an error rather than empty facts: a surface whose bytes cannot be read as Go
// establishes nothing.
func parseGoFacts(src []byte) (prospectiveFacts, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
	if err != nil {
		return prospectiveFacts{}, err
	}
	facts := prospectiveFacts{Package: f.Name.Name, Imports: map[string]bool{}}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return prospectiveFacts{}, err
		}
		facts.Imports[p] = true
	}
	return facts, nil
}

// prospectiveAnchors applies PROSPECTIVE_CREATE_ADMISSIBLE to every planned
// file absent at world that the plan declared. It returns one grant per
// admissible file; anything the predicate cannot express stays uncovered.
// Absence is re-established here for each F rather than trusted from the
// caller: only a read that wraps errNotAtWorld is a create.
//
//	A. F's directory holds a surface S that a derived anchor covers at world;
//	B. F's declared package equals S's package clause read from S at world;
//	C. F's role is a member of the closed role table and F's path matches it;
//	D. every declared dependency is in S's imports or the role's novel allowance.
//
// A go-existing-package declaration is decided by existingPackageGrant instead
// of clauses A-D, and new-package directories by newPackageGrants.
//
// The anchor carries S's requirement and a description that says PROSPECTIVE
// and names S, so it never reads as an observation of F.
func prospectiveAnchors(ctx context.Context, world string, planned []string, declarations []ProspectiveSurface, existing []CoverageAnchor, read worldReader) []prospectiveGrant {
	if len(declarations) == 0 || read == nil {
		return nil
	}
	isPlanned := map[string]bool{}
	for _, p := range planned {
		isPlanned[path.Clean(p)] = true
	}
	// Clause A candidates: covering surfaces by directory, in a fixed order.
	byDir := map[string][]CoverageAnchor{}
	for _, a := range existing {
		byDir[path.Dir(a.File)] = append(byDir[path.Dir(a.File)], a)
	}
	for _, list := range byDir {
		sort.SliceStable(list, func(i, j int) bool { return list[i].File < list[j].File })
	}
	// Directories a new-package role is declared in are decided whole, by
	// newPackageGrants, and never by the per-file loop below.
	var newDirs []string
	isNewDir := map[string]bool{}
	for _, d := range declarations {
		if !prospectiveRoles[d.Role].newPackage {
			continue
		}
		dir := path.Dir(path.Clean(strings.TrimSpace(d.Path)))
		if !isNewDir[dir] {
			isNewDir[dir] = true
			newDirs = append(newDirs, dir)
		}
	}

	declaredTimes := map[string]int{}
	for _, d := range declarations {
		declaredTimes[path.Clean(strings.TrimSpace(d.Path))]++
	}

	var grants []prospectiveGrant
	for _, d := range declarations {
		f := path.Clean(strings.TrimSpace(d.Path))
		if f == "." || !isPlanned[f] {
			continue // a declaration with no matching planned file
		}
		if isNewDir[path.Dir(f)] {
			continue
		}
		if _, err := read(ctx, world, f); !confirmedMissing(err) {
			continue // F exists at world, or its existence could not be established: not a create
		}
		// Clause C, read by membership.
		role, ok := prospectiveRoles[d.Role]
		if !ok || role.newPackage {
			continue
		}
		if role.existingPackage {
			if declaredTimes[f] != 1 {
				continue // one path, one declaration: two say no single thing
			}
			if g, ok := existingPackageGrant(ctx, world, f, d, byDir[path.Dir(f)], read); ok {
				grants = append(grants, g)
			}
			continue
		}
		if !roleAdmitsPath(role, f) {
			continue
		}
		named := strings.TrimSpace(d.Covering)
		for _, s := range byDir[path.Dir(f)] {
			if s.File == f || (named != "" && s.File != path.Clean(named)) {
				continue
			}
			src, err := read(ctx, world, s.File)
			if err != nil {
				continue
			}
			facts, err := parseGoFacts(src)
			if err != nil {
				continue
			}
			if reason := admissibleAgainst(d, facts, role); reason != "" {
				continue
			}
			// Every anchor over this covering surface is carried, so the
			// consumer can select by the requirement its gap names rather
			// than by whichever derivation sorted first.
			var carried []CoverageAnchor
			for _, a := range byDir[path.Dir(f)] {
				if a.File != s.File {
					continue
				}
				carried = append(carried, CoverageAnchor{
					File:        f,
					Requirement: a.Requirement,
					Describe: fmt.Sprintf("PROSPECTIVE %s %s: create authorized by %s at %s; %s",
						d.Role, f, s.File, shortWorldID(world), a.Describe),
				})
			}
			grants = append(grants, prospectiveGrant{
				Surface:  d,
				Covering: s.File,
				Facts:    facts,
				Anchor:   carried[0],
				Anchors:  carried,
			})
			break
		}
	}
	// Library directories are decided first, so a command directory can bind
	// its one library edge only to a group already granted whole. The output
	// keeps declaration order.
	isCommandDir := map[string]bool{}
	for _, d := range declarations {
		if d.Role == roleGoCommandPackage {
			isCommandDir[path.Dir(path.Clean(strings.TrimSpace(d.Path)))] = true
		}
	}
	byNewDir := map[string][]prospectiveGrant{}
	libraries := map[string][]prospectiveGrant{}
	for _, dir := range newDirs {
		if isCommandDir[dir] {
			continue
		}
		byNewDir[dir] = newPackageGrants(ctx, world, dir, planned, declarations, existing, nil, read)
		if len(byNewDir[dir]) != 0 {
			libraries[dir] = byNewDir[dir]
		}
	}
	for _, dir := range newDirs {
		if isCommandDir[dir] {
			byNewDir[dir] = newPackageGrants(ctx, world, dir, planned, declarations, existing, libraries, read)
		}
	}
	for _, dir := range newDirs {
		grants = append(grants, byNewDir[dir]...)
	}
	return grants
}

// existingPackageGrant decides one go-existing-package declaration d for the
// planned file f, already confirmed ABSENT at world. surfaces are the covered
// surfaces in f's directory. The grant is issued only on facts read at world:
//
//	E1. f has the role's path shape: a non-test *.go file;
//	E2. d names a covering file S: a non-test Go file in f's own directory,
//	    other than f, covered by a derived anchor and readable at world -- so
//	    the directory exists there and f is not in a new package;
//	E3. d's package equals S's package clause read at world;
//	E4. the package facts are S's clause and the union of the imports of
//	    every covered, readable non-test Go file in the directory that
//	    carries that clause;
//	E5. the go.mod governing the directory at world is read for its module
//	    path and its require directives;
//	E6. every declared dependency is admitted by dependencyAdmitted against
//	    those facts, and the declared dependencies are the whole envelope.
//
// Absence is a precondition here, never authority: nothing is granted unless
// E1-E6 hold. File name, GOOS suffix and build constraints play no part.
func existingPackageGrant(ctx context.Context, world, f string, d ProspectiveSurface, surfaces []CoverageAnchor, read worldReader) (prospectiveGrant, bool) {
	role := prospectiveRoles[roleGoExistingPackage]
	// E1.
	if !roleAdmitsPath(role, f) {
		return prospectiveGrant{}, false
	}
	// E2.
	named := strings.TrimSpace(d.Covering)
	if named == "" {
		return prospectiveGrant{}, false
	}
	covering := path.Clean(named)
	if !sameDirectoryNonTestGo(f, covering) {
		return prospectiveGrant{}, false
	}
	var carriedFrom []CoverageAnchor
	for _, a := range surfaces {
		if a.File == covering {
			carriedFrom = append(carriedFrom, a)
		}
	}
	if len(carriedFrom) == 0 {
		return prospectiveGrant{}, false
	}
	src, err := read(ctx, world, covering)
	if err != nil {
		return prospectiveGrant{}, false
	}
	sFacts, err := parseGoFacts(src)
	if err != nil {
		return prospectiveGrant{}, false
	}
	// E3.
	if strings.TrimSpace(d.Package) == "" || d.Package != sFacts.Package {
		return prospectiveGrant{}, false
	}
	// E4.
	facts := prospectiveFacts{Package: sFacts.Package, Imports: map[string]bool{}}
	seen := map[string]bool{}
	for _, a := range surfaces {
		g := a.File
		if seen[g] || g == f || !sameDirectoryNonTestGo(f, g) {
			continue
		}
		seen[g] = true
		b, err := read(ctx, world, g)
		if err != nil {
			continue
		}
		gf, err := parseGoFacts(b)
		if err != nil || gf.Package != facts.Package {
			continue
		}
		for imp := range gf.Imports {
			facts.Imports[imp] = true
		}
	}
	// E5.
	dir := path.Dir(f)
	mod, ok := goModuleDir(ctx, world, dir, read)
	if !ok || !(mod == dir || underModule(mod, dir)) {
		return prospectiveGrant{}, false
	}
	gomod := "go.mod"
	if mod != "." {
		gomod = mod + "/go.mod"
	}
	modSrc, err := read(ctx, world, gomod)
	if err != nil {
		return prospectiveGrant{}, false
	}
	module, ok := goModulePath(modSrc)
	if !ok {
		return prospectiveGrant{}, false
	}
	requires := goModRequires(modSrc)
	// E6.
	envelope := []string{}
	inEnvelope := map[string]bool{}
	for _, dep := range d.Dependencies {
		if !dependencyAdmitted(dep, facts.Imports, module, requires) {
			return prospectiveGrant{}, false
		}
		if !inEnvelope[dep] {
			inEnvelope[dep] = true
			envelope = append(envelope, dep)
		}
	}
	sort.Strings(envelope)

	var carried []CoverageAnchor
	for _, a := range carriedFrom {
		carried = append(carried, CoverageAnchor{
			File:        f,
			Requirement: a.Requirement,
			Describe: fmt.Sprintf("PROSPECTIVE %s %s: create authorized by %s at %s; %s",
				d.Role, f, covering, shortWorldID(world), a.Describe),
		})
	}
	return prospectiveGrant{
		Surface:  d,
		Covering: covering,
		Facts:    facts,
		Anchor:   carried[0],
		Anchors:  carried,
		Existing: &prospectiveExisting{Module: module, ModuleDir: mod, Requires: requires, Envelope: envelope},
	}, true
}

// dependencyAdmitted is the closed dependency rule of go-existing-package. A
// declared import is admitted when it is already in the pinned package's
// import set, or when the module that owns it is one the governing go.mod
// explicitly requires. Ownership is longest path-segment matching over the
// required modules and the main module itself: an import inside the main
// module is owned by it and is not admitted by any shorter requirement. Only
// a declared import is ever asked about; go.mod presence alone grants nothing.
func dependencyAdmitted(imp string, packageImports map[string]bool, module string, requires []string) bool {
	if imp == "" {
		return false
	}
	if packageImports[imp] {
		return true
	}
	owner, required := "", false
	consider := func(m string, req bool) {
		if m == "" || (imp != m && !strings.HasPrefix(imp, m+"/")) {
			return
		}
		if len(m) > len(owner) || (len(m) == len(owner) && !req) {
			owner, required = m, req
		}
	}
	for _, r := range requires {
		consider(r, true)
	}
	consider(module, false)
	return owner != "" && required
}

// goModRequires reads the module paths a go.mod's require directives name,
// in both the single-line and the block form, sorted and without duplicates.
func goModRequires(src []byte) []string {
	seen := map[string]bool{}
	var out []string
	add := func(fields []string) {
		if len(fields) < 2 {
			return
		}
		p := fields[0]
		if unq, err := strconv.Unquote(p); err == nil {
			p = unq
		}
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	inBlock := false
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case inBlock && fields[0] == ")":
			inBlock = false
		case inBlock:
			add(fields)
		case fields[0] == "require" && len(fields) == 2 && fields[1] == "(":
			inBlock = true
		case fields[0] == "require":
			add(fields[1:])
		}
	}
	sort.Strings(out)
	return out
}

// newPackageGrants decides a directory a new-package role is declared in, as
// a whole: every declared file in it is granted, or none is. The covering
// surface S is the one the declarations NAME, and it is admitted only on
// facts read at the pinned world:
//
//	N1. dir is confirmed ABSENT at world (only errNotAtWorld establishes it);
//	N2. every planned file in dir is declared exactly once, with a role from
//	    the closed set whose path shape it matches, and is confirmed absent;
//	N3. the production declarations share one new-package role, one package
//	    clause (main for a command, a non-main identifier for a library) and
//	    one named covering surface S;
//	N4. S is a covered, readable, non-test Go file outside dir;
//	N5. S's directory and dir resolve to the same go.mod at world;
//	N6. go-command-package: S is package main and S's package directory and
//	    dir are children of the same existing directory named "cmd";
//	    go-library-package: S is not package main and S's package directory
//	    and dir lie under the same top-level directory of that module;
//	N7. every declared dependency is in S's imports or the role allowance,
//	    except that a go-command-package declaration may bind at most one
//	    other dependency, and only as the edge libraryEdge establishes; the
//	    command package as a whole binds at most one library.
//
// libraries holds the same-plan go-library-package directories already
// granted whole at this world; it is nil when dir is itself a library.
//
// A go-regression-test in dir is admitted only through a production grant in
// dir, over the same S and package: the one prospective-to-prospective step.
func newPackageGrants(ctx context.Context, world, dir string, planned []string, declarations []ProspectiveSurface, existing []CoverageAnchor, libraries map[string][]prospectiveGrant, read worldReader) []prospectiveGrant {
	if dir == "." || dir == ".." || strings.HasPrefix(dir, "../") || path.IsAbs(dir) {
		return nil
	}
	// N1.
	if _, err := read(ctx, world, dir); !confirmedMissing(err) {
		return nil
	}
	// N2.
	declared := map[string]ProspectiveSurface{}
	for _, d := range declarations {
		f := path.Clean(strings.TrimSpace(d.Path))
		if path.Dir(f) != dir {
			continue
		}
		if _, dup := declared[f]; dup {
			return nil
		}
		declared[f] = d
	}
	isPlanned := map[string]bool{}
	for _, p := range planned {
		f := path.Clean(p)
		if path.Dir(f) != dir {
			continue
		}
		isPlanned[f] = true
		if _, ok := declared[f]; !ok {
			return nil // an undeclared create in the new directory
		}
	}
	for f := range declared {
		if !isPlanned[f] {
			return nil // a declaration with no matching planned file
		}
	}
	files := make([]string, 0, len(declared))
	for f := range declared {
		files = append(files, f)
	}
	sort.Strings(files)
	var production, tests []string
	for _, f := range files {
		d := declared[f]
		role, ok := prospectiveRoles[d.Role]
		if !ok || !roleAdmitsPath(role, f) {
			return nil
		}
		if _, err := read(ctx, world, f); !confirmedMissing(err) {
			return nil
		}
		switch {
		case role.newPackage:
			production = append(production, f)
		case d.Role == roleGoRegressionTest:
			tests = append(tests, f)
		default:
			return nil // go-existing-package never creates in a new directory
		}
	}
	// N3.
	if len(production) == 0 {
		return nil
	}
	first := declared[production[0]]
	covering := path.Clean(strings.TrimSpace(first.Covering))
	if strings.TrimSpace(first.Covering) == "" {
		return nil
	}
	for _, f := range production {
		d := declared[f]
		if d.Role != first.Role || d.Package != first.Package || path.Clean(strings.TrimSpace(d.Covering)) != covering || strings.TrimSpace(d.Covering) == "" {
			return nil
		}
	}
	command := first.Role == roleGoCommandPackage
	if command && first.Package != "main" {
		return nil
	}
	if !command && (first.Package == "main" || !token.IsIdentifier(first.Package)) {
		return nil
	}
	// N4.
	var carriedFrom []CoverageAnchor
	for _, a := range existing {
		if a.File == covering {
			carriedFrom = append(carriedFrom, a)
		}
	}
	sDir := path.Dir(covering)
	if len(carriedFrom) == 0 || sDir == dir || path.Ext(covering) != ".go" || strings.HasSuffix(covering, "_test.go") {
		return nil
	}
	src, err := read(ctx, world, covering)
	if err != nil {
		return nil
	}
	facts, err := parseGoFacts(src)
	if err != nil {
		return nil
	}
	// N5.
	mod, ok := goModuleDir(ctx, world, dir, read)
	if !ok {
		return nil
	}
	if sMod, ok := goModuleDir(ctx, world, sDir, read); !ok || sMod != mod {
		return nil
	}
	// N6.
	if command {
		parent := path.Dir(dir)
		if facts.Package != "main" || path.Base(parent) != "cmd" || path.Dir(sDir) != parent || !underModule(mod, parent) {
			return nil
		}
	} else {
		relNew, relS := strings.Split(moduleRel(mod, dir), "/"), strings.Split(moduleRel(mod, sDir), "/")
		if facts.Package == "main" || !underModule(mod, dir) || !underModule(mod, sDir) ||
			len(relNew) < 2 || len(relS) < 2 || relNew[0] != relS[0] {
			return nil
		}
	}
	// N7.
	edges := map[string]*prospectiveEdge{}
	var bound *prospectiveEdge
	for _, f := range files {
		d := declared[f]
		if d.Package != first.Package {
			return nil // a test in the new package carries the package's clause
		}
		if named := strings.TrimSpace(d.Covering); named != "" && path.Clean(named) != covering {
			return nil
		}
		role := prospectiveRoles[d.Role]
		var novel []string
		for _, dep := range d.Dependencies {
			if !facts.Imports[dep] && !role.novel[dep] {
				novel = append(novel, dep)
			}
		}
		if len(novel) == 0 {
			continue
		}
		if d.Role != roleGoCommandPackage || len(novel) != 1 {
			return nil
		}
		edge, ok := libraryEdge(ctx, world, dir, mod, novel[0], declarations, libraries, read)
		if !ok || (bound != nil && *bound != *edge) {
			return nil
		}
		bound, edges[f] = edge, edge
	}

	grantFor := func(f, via string) prospectiveGrant {
		d := declared[f]
		authorizedBy := covering
		if via != "" {
			authorizedBy = via + " tracing to " + covering
		}
		var carried []CoverageAnchor
		for _, a := range carriedFrom {
			carried = append(carried, CoverageAnchor{
				File:        f,
				Requirement: a.Requirement,
				Describe: fmt.Sprintf("PROSPECTIVE %s %s: create authorized by %s at %s; %s",
					d.Role, f, authorizedBy, shortWorldID(world), a.Describe),
			})
		}
		return prospectiveGrant{Surface: d, Covering: covering, Facts: facts, Anchor: carried[0], Anchors: carried, Via: via, Edge: edges[f]}
	}
	var grants []prospectiveGrant
	for _, f := range production {
		grants = append(grants, grantFor(f, ""))
	}
	for _, f := range tests {
		grants = append(grants, grantFor(f, production[0]))
	}
	return grants
}

// goModuleDir returns the directory of the go.mod that governs dir at world,
// walking up from dir. Only confirmed absence moves the walk up; any other
// read failure establishes no module at all.
func goModuleDir(ctx context.Context, world, dir string, read worldReader) (string, bool) {
	for d := dir; ; d = path.Dir(d) {
		f := "go.mod"
		if d != "." {
			f = d + "/go.mod"
		}
		_, err := read(ctx, world, f)
		if err == nil {
			return d, true
		}
		if !confirmedMissing(err) || d == "." {
			return "", false
		}
	}
}

// libraryEdge establishes the one dependency a command in cmdDir may take
// beyond its covering surface's imports: dep must be exactly the module import
// path, read from the go.mod of the command's module mod at world, of one
// same-plan go-library-package directory in that same module, and every
// declared file in that directory must hold a grant issued at this world. A
// dependency that names anything else -- another module, a package absent from
// the plan, or a library any planned create of which was refused -- binds no
// edge.
func libraryEdge(ctx context.Context, world, cmdDir, mod, dep string, declarations []ProspectiveSurface, libraries map[string][]prospectiveGrant, read worldReader) (*prospectiveEdge, bool) {
	if len(libraries) == 0 {
		return nil, false
	}
	gomod := "go.mod"
	if mod != "." {
		gomod = mod + "/go.mod"
	}
	src, err := read(ctx, world, gomod)
	if err != nil {
		return nil, false
	}
	module, ok := goModulePath(src)
	if !ok {
		return nil, false
	}
	dirs := make([]string, 0, len(libraries))
	for dir := range libraries {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, lib := range dirs {
		if lib == cmdDir || !underModule(mod, lib) || dep != module+"/"+moduleRel(mod, lib) {
			continue
		}
		if libMod, ok := goModuleDir(ctx, world, lib, read); !ok || libMod != mod {
			return nil, false
		}
		granted := map[string]prospectiveGrant{}
		for _, g := range libraries[lib] {
			granted[path.Clean(g.Anchor.File)] = g
		}
		production := 0
		for _, d := range declarations {
			f := path.Clean(strings.TrimSpace(d.Path))
			if path.Dir(f) != lib {
				continue
			}
			g, ok := granted[f]
			if !ok || !sameSurface(g.Surface, d) {
				return nil, false // a declared create in the library was refused
			}
			switch {
			case d.Role == roleGoLibraryPackage && g.Via == "":
				production++
			case d.Role == roleGoRegressionTest:
			default:
				return nil, false
			}
		}
		if production == 0 {
			return nil, false
		}
		return &prospectiveEdge{Import: dep, Library: lib, Module: module, ModuleDir: mod}, true
	}
	return nil, false
}

// goModulePath reads the module path from a go.mod's module directive.
func goModulePath(src []byte) (string, bool) {
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "module" {
			continue
		}
		p := fields[1]
		if unq, err := strconv.Unquote(p); err == nil {
			p = unq
		}
		return p, p != ""
	}
	return "", false
}

func underModule(mod, dir string) bool {
	return mod == "." || strings.HasPrefix(dir, mod+"/")
}

func moduleRel(mod, dir string) string {
	if mod == "." {
		return dir
	}
	return strings.TrimPrefix(dir, mod+"/")
}

// admissibleAgainst is clauses B and D for one declaration against one
// covering surface. It returns "" when both hold, else the clause that failed.
func admissibleAgainst(d ProspectiveSurface, s prospectiveFacts, role prospectiveRole) string {
	if strings.TrimSpace(d.Package) == "" || d.Package != s.Package {
		return fmt.Sprintf("package %q is not the covering surface's package %q", d.Package, s.Package)
	}
	for _, dep := range d.Dependencies {
		if !s.Imports[dep] && !role.novel[dep] {
			return fmt.Sprintf("dependency %q is neither imported by the covering surface nor in the %s allowance", dep, d.Role)
		}
	}
	return ""
}

// inspectProspectiveSurfaces checks every declared surface against what the
// candidate actually created. facts is keyed by declared path and holds the
// covering surface's facts at the pinned world. A declaration with no entry
// was authorized by nothing and is REFUTED: the earlier reading -- "only the
// role's allowance applies" -- let a role alone stand in for a grant whenever
// the facts were missing, which is exactly the shape a damaged resume record
// takes.
//
// The first mismatch is returned as an error beginning "prospective surface
// refuted:". Nothing is reinterpreted.
func inspectProspectiveSurfaces(diff string, declarations []ProspectiveSurface, facts map[string]prospectiveFacts) error {
	return inspectProspective(diff, declarations, facts, nil, nil)
}

// inspectProspectiveGrants is the candidate inspection against the recorded
// grants themselves: each declaration is checked against its grant's covering
// facts, and a command's created imports may exceed them by exactly the one
// library edge its grant records. An edge is honored only when it still holds
// against the declarations and the other grants (edgeFault); an absent or
// tampered edge authorizes no import, so the command importing it is refuted.
// A go-existing-package file is checked against its grant's recorded envelope
// alone, honored only when the grant was issued for that exact declaration
// and still holds against its own recorded facts (existingFault).
func inspectProspectiveGrants(diff string, declarations []ProspectiveSurface, grants []prospectiveGrant) error {
	facts := map[string]prospectiveFacts{}
	byPath := map[string]prospectiveGrant{}
	for _, g := range grants {
		f := path.Clean(strings.TrimSpace(g.Anchor.File))
		facts[f] = g.Facts
		byPath[f] = g
	}
	edges := map[string]string{}
	if oneEdgePerCommand(grants) == nil {
		for f, g := range byPath {
			if g.Edge != nil && edgeFault(f, g, declarations, byPath) == nil {
				edges[f] = g.Edge.Import
			}
		}
	}
	envelopes := map[string]map[string]bool{}
	for _, d := range declarations {
		f := path.Clean(strings.TrimSpace(d.Path))
		g, ok := byPath[f]
		if !ok || g.Existing == nil || !sameSurface(g.Surface, d) || existingFault(f, g) != nil {
			continue
		}
		envelope := map[string]bool{}
		for _, imp := range g.Existing.Envelope {
			envelope[imp] = true
		}
		envelopes[f] = envelope
	}
	if err := inspectProspective(diff, declarations, facts, edges, envelopes); err != nil {
		return err
	}
	// The candidate's content is judged above; the grant set itself is then
	// held to the one declaration/grant rule admission and restoration read,
	// so a grant set that could not have admitted the plan cannot pass here.
	if len(declarations) == 0 {
		return nil
	}
	if err := matchGrantsToDeclarations(declarations, grants); err != nil {
		return fmt.Errorf("prospective surface refuted: %w", err)
	}
	return nil
}

// inspectProspective is inspectProspectiveSurfaces with, per declared path,
// the one recorded library edge the created file may import beyond its facts,
// and, for a go-existing-package declaration, the recorded envelope that is
// the whole of what it may import.
func inspectProspective(diff string, declarations []ProspectiveSurface, facts map[string]prospectiveFacts, edges map[string]string, envelopes map[string]map[string]bool) error {
	if len(declarations) == 0 {
		return nil
	}
	created := addedFiles(diff)
	for _, d := range declarations {
		f := path.Clean(strings.TrimSpace(d.Path))
		role, ok := prospectiveRoles[d.Role]
		if !ok {
			return fmt.Errorf("prospective surface refuted: %s declares role %q, which is not a governed role", f, d.Role)
		}
		if !roleAdmitsPath(role, f) {
			return fmt.Errorf("prospective surface refuted: %s does not match the %s path shape %s", f, d.Role, role.pathGlob)
		}
		src, ok := created[f]
		if !ok {
			return fmt.Errorf("prospective surface refuted: %s was declared but the candidate did not create it", f)
		}
		actual, err := parseGoFacts([]byte(src))
		if err != nil {
			return fmt.Errorf("prospective surface refuted: %s could not be read as Go: %v", f, err)
		}
		if actual.Package != d.Package {
			return fmt.Errorf("prospective surface refuted: %s has package %q, the declaration said %q", f, actual.Package, d.Package)
		}
		recorded, ok := facts[f]
		if !ok || recorded.Imports == nil {
			return fmt.Errorf("prospective surface refuted: %s was declared but no recorded grant carries its covering surface's facts", f)
		}
		if role.existingPackage {
			if err := inspectExisting(f, actual, envelopes[f]); err != nil {
				return err
			}
			continue
		}
		allowed := recorded.Imports
		imports := make([]string, 0, len(actual.Imports))
		for imp := range actual.Imports {
			imports = append(imports, imp)
		}
		sort.Strings(imports)
		for _, imp := range imports {
			if !allowed[imp] && !role.novel[imp] && (edges[f] == "" || imp != edges[f]) {
				return fmt.Errorf("prospective surface refuted: %s imports %q, which is outside the covering surface's imports, the %s allowance and any recorded library edge", f, imp, d.Role)
			}
		}
	}
	// A new package was admitted as exactly its declared files: anything else
	// created in that directory was authorized by nothing. So was a create in
	// an existing package a go-existing-package declaration admitted into: the
	// declaration names the one file, never its undeclared siblings.
	isDeclared, newDirs, existingDirs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, d := range declarations {
		f := path.Clean(strings.TrimSpace(d.Path))
		isDeclared[f] = true
		if prospectiveRoles[d.Role].newPackage {
			newDirs[path.Dir(f)] = true
		}
		if prospectiveRoles[d.Role].existingPackage {
			existingDirs[path.Dir(f)] = true
		}
	}
	names := make([]string, 0, len(created))
	for f := range created {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		if newDirs[path.Dir(f)] && !isDeclared[f] {
			return fmt.Errorf("prospective surface refuted: %s was created in a new package but no declaration admitted it", f)
		}
		if existingDirs[path.Dir(f)] && !isDeclared[f] {
			return fmt.Errorf("prospective surface refuted: %s was created beside a %s declaration but no declaration admitted it", f, roleGoExistingPackage)
		}
	}
	return nil
}

// inspectExisting checks a created go-existing-package file against its
// recorded grant: imports drawn from the recorded envelope alone. The package
// clause was already held to the declaration, and an envelope is honored only
// when existingFault bound that declaration to the recorded package clause. A
// missing envelope authorizes no import and no file.
func inspectExisting(f string, actual prospectiveFacts, envelope map[string]bool) error {
	if envelope == nil {
		return fmt.Errorf("prospective surface refuted: %s was declared %s but no intact recorded grant carries its dependency envelope", f, roleGoExistingPackage)
	}
	imports := make([]string, 0, len(actual.Imports))
	for imp := range actual.Imports {
		imports = append(imports, imp)
	}
	sort.Strings(imports)
	for _, imp := range imports {
		if !envelope[imp] {
			return fmt.Errorf("prospective surface refuted: %s imports %q, which is outside its recorded %s envelope", f, imp, roleGoExistingPackage)
		}
	}
	return nil
}

// addedFiles reconstructs the content of every file the diff creates. Only
// added files are reconstructed: a created file has no context lines, so its
// "+" lines are the whole of it.
func addedFiles(diff string) map[string]string {
	out := map[string]string{}
	var current string
	var added bool
	var body []string
	flush := func() {
		if current != "" && added {
			out[current] = strings.Join(body, "\n") + "\n"
		}
		current, added, body = "", false, nil
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			for _, c := range report.FromDiff(line + "\n").Files {
				current = path.Clean(c.Path)
			}
		case strings.HasPrefix(line, "new file mode"):
			added = true
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			body = append(body, line[1:])
		}
	}
	flush()
	return out
}

func shortWorldID(w string) string {
	if len(w) > 12 {
		return w[:12]
	}
	return w
}

// renderProspectiveGrants states, for the worker, the CREATE authority the
// run already established for its declared surfaces.
//
// It exists because the grant was a fence and nothing else. Series B of the
// #89 natural reproducer ran three independent workers under three admissible
// grants, and all three were refuted after creation for importing "bytes":
// the router knew the envelope, the inspection enforced it, and the worker --
// handed the plan and never the grant -- chose its own imports. Authority that
// constrains execution must be visible at the execution boundary. Showing the
// grant confers nothing: the worker learns the authority it already operates
// under, and the post-creation inspection is unchanged.
func renderProspectiveGrants(grants []prospectiveGrant) string {
	if len(grants) == 0 {
		return ""
	}
	var b strings.Builder
	for _, g := range grants {
		role, known := prospectiveRoles[g.Surface.Role]
		allowed := map[string]bool{}
		existing := known && role.existingPackage && g.Existing != nil
		if existing {
			for _, imp := range g.Existing.Envelope {
				allowed[imp] = true
			}
		} else {
			for imp := range g.Facts.Imports {
				allowed[imp] = true
			}
			if known {
				for imp := range role.novel {
					allowed[imp] = true
				}
			}
		}
		if g.Edge != nil {
			allowed[g.Edge.Import] = true
		}
		imports := make([]string, 0, len(allowed))
		for imp := range allowed {
			imports = append(imports, imp)
		}
		sort.Strings(imports)
		reqs := map[string]bool{}
		for _, a := range g.Anchors {
			reqs[string(a.Requirement)] = true
		}
		if len(g.Anchors) == 0 {
			reqs[string(g.Anchor.Requirement)] = true
		}
		requirements := make([]string, 0, len(reqs))
		for r := range reqs {
			requirements = append(requirements, r)
		}
		sort.Strings(requirements)
		fmt.Fprintf(&b, "- CREATE %s\n", path.Clean(strings.TrimSpace(g.Surface.Path)))
		fmt.Fprintf(&b, "    covering surface: %s\n", g.Covering)
		if g.Via != "" {
			fmt.Fprintf(&b, "    traced through production grant: %s\n", g.Via)
		}
		if known && role.newPackage || g.Via != "" {
			fmt.Fprintf(&b, "    package: %s (the new package's declared clause)\n", g.Surface.Package)
		} else if known && role.existingPackage {
			fmt.Fprintf(&b, "    package: %s (must equal the existing package's clause)\n", g.Surface.Package)
		} else {
			fmt.Fprintf(&b, "    package: %s (must equal the covering surface's package)\n", g.Surface.Package)
		}
		fmt.Fprintf(&b, "    role: %s\n", g.Surface.Role)
		if g.Edge != nil {
			fmt.Fprintf(&b, "    same-plan library edge: %s (the granted package %s)\n", g.Edge.Import, g.Edge.Library)
		}
		fmt.Fprintf(&b, "    declared dependencies: %s\n", strings.Join(g.Surface.Dependencies, ", "))
		if existing {
			fmt.Fprintf(&b, "    EFFECTIVE ALLOWED IMPORTS (exactly the declared dependencies the pinned package and go.mod admit; nothing else): %s\n", strings.Join(imports, ", "))
			fmt.Fprintf(&b, "    an undeclared file created in %s is refuted\n", path.Dir(path.Clean(strings.TrimSpace(g.Surface.Path))))
		} else {
			fmt.Fprintf(&b, "    EFFECTIVE ALLOWED IMPORTS (covering surface's imports at the pinned world + the role allowance): %s\n", strings.Join(imports, ", "))
		}
		fmt.Fprintf(&b, "    architectural requirements carried: %s\n", strings.Join(requirements, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}
