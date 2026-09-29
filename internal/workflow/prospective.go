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
	// when given, restricts the covering surface to that file.
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
}

// The closed role set. go-regression-test is the only test role; the two
// new-package roles are production roles and never admit a *_test.go.
const (
	roleGoRegressionTest = "go-regression-test"
	roleGoLibraryPackage = "go-library-package"
	roleGoCommandPackage = "go-command-package"
)

var prospectiveRoles = map[string]prospectiveRole{
	roleGoRegressionTest: {pathGlob: "*_test.go", novel: map[string]bool{"testing": true}},
	roleGoLibraryPackage: {pathGlob: "*.go", novel: map[string]bool{}, newPackage: true},
	roleGoCommandPackage: {pathGlob: "*.go", novel: map[string]bool{}, newPackage: true},
}

// roleAdmitsPath reports whether f has the role's path shape. A production
// role never admits a test file: go-regression-test is the only test role.
func roleAdmitsPath(role prospectiveRole, f string) bool {
	base := path.Base(f)
	if matched, err := path.Match(role.pathGlob, base); err != nil || !matched {
		return false
	}
	return !role.newPackage || !strings.HasSuffix(base, "_test.go")
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
	// Edge is the one dependency a go-command-package grant admits beyond its
	// covering surface's imports: the import path of a go-library-package
	// directory declared in the same plan and granted whole at the same world
	// and module. Nil for every other grant. It is recorded and restored with
	// the grant and never reconstructed.
	Edge *prospectiveEdge `json:"edge,omitempty"`
}

// prospectiveEdge names the same-plan library a command grant depends on:
// its import path, its directory, and the directory of the go.mod both the
// library and the command resolve to at the pinned world.
type prospectiveEdge struct {
	ImportPath string `json:"import_path"`
	Dir        string `json:"dir"`
	Module     string `json:"module"`
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
func matchGrantsToDeclarations(declared []ProspectiveSurface, grants []prospectiveGrant) error {
	byPath := map[string]prospectiveGrant{}
	for _, g := range grants {
		f := path.Clean(strings.TrimSpace(g.Anchor.File))
		if _, dup := byPath[f]; dup {
			return fmt.Errorf("the recorded prospective authorization holds two grants for %s", f)
		}
		byPath[f] = g
	}
	if len(byPath) != len(declared) {
		return fmt.Errorf("the recorded prospective authorization holds %d grant(s) for %d declared surface(s)", len(byPath), len(declared))
	}
	for _, d := range declared {
		f := path.Clean(strings.TrimSpace(d.Path))
		g, ok := byPath[f]
		if !ok {
			return fmt.Errorf("the recorded prospective authorization holds no grant for declared surface %s", f)
		}
		if !sameSurface(g.Surface, d) {
			return fmt.Errorf("the recorded grant for %s was issued for a different declaration", f)
		}
		if strings.TrimSpace(g.Covering) == "" || strings.TrimSpace(g.Facts.Package) == "" || g.Facts.Imports == nil {
			return fmt.Errorf("the recorded grant for %s names no covering surface or carries no pinned-world facts", f)
		}
		if err := grantTracesToItsSurface(f, g, byPath); err != nil {
			return err
		}
		if err := grantEdgeHolds(f, g, byPath); err != nil {
			return err
		}
	}
	return nil
}

// grantEdgeHolds is the record-side form of the dependency edge. A command
// grant whose declaration lists a dependency beyond its covering surface's
// imports must carry exactly that dependency as its edge, and the edge must
// name a directory of recorded library grants in the same module. Any other
// grant carries no edge. Nothing is inferred: a missing edge is refused, not
// rebuilt from the declaration.
func grantEdgeHolds(f string, g prospectiveGrant, byPath map[string]prospectiveGrant) error {
	role := prospectiveRoles[g.Surface.Role]
	command := g.Surface.Role == roleGoCommandPackage && g.Via == ""
	var novel []string
	for _, dep := range g.Surface.Dependencies {
		if !g.Facts.Imports[dep] && !role.novel[dep] {
			novel = append(novel, dep)
		}
	}
	if g.Edge == nil {
		if command && len(novel) != 0 {
			return fmt.Errorf("the recorded command grant for %s declares %s beyond its covering surface but carries no dependency edge", f, strings.Join(novel, ", "))
		}
		return nil
	}
	if !command {
		return fmt.Errorf("the recorded grant for %s carries a dependency edge, which only a command grant may", f)
	}
	e, dir := *g.Edge, path.Dir(f)
	if len(novel) != 1 || novel[0] != e.ImportPath {
		return fmt.Errorf("the recorded command grant for %s carries edge %q, not its one declared dependency beyond the covering surface", f, e.ImportPath)
	}
	if !underModule(e.Module, e.Dir) || !underModule(e.Module, dir) || !strings.HasSuffix(e.ImportPath, "/"+moduleRel(e.Module, e.Dir)) {
		return fmt.Errorf("the recorded command grant for %s carries an edge %+v that does not name a library directory in its module", f, e)
	}
	library := false
	for p, lg := range byPath {
		if path.Dir(p) == e.Dir && lg.Surface.Role == roleGoLibraryPackage && lg.Via == "" {
			library = true
		}
	}
	if !library {
		return fmt.Errorf("the recorded command grant for %s depends on %s, which holds no recorded library grant", f, e.Dir)
	}
	// One edge per command package: a sibling command grant that carries an
	// edge carries this one.
	for p, sg := range byPath {
		if path.Dir(p) == dir && sg.Edge != nil && *sg.Edge != e {
			return fmt.Errorf("the recorded command grants in %s carry two dependency edges, %q and %q", dir, e.ImportPath, sg.Edge.ImportPath)
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

// sameSurface compares declarations field by field, dependencies as a set.
func sameSurface(a, b ProspectiveSurface) bool {
	if path.Clean(strings.TrimSpace(a.Path)) != path.Clean(strings.TrimSpace(b.Path)) || a.Package != b.Package || a.Role != b.Role || a.Covering != b.Covering || len(a.Dependencies) != len(b.Dependencies) {
		return false
	}
	seen := map[string]bool{}
	for _, d := range a.Dependencies {
		seen[d] = true
	}
	for _, d := range b.Dependencies {
		if !seen[d] {
			return false
		}
	}
	return true
}

// prospectiveRecord is the ProspectiveGranted payload: the grants and the
// world identity they were read at. World is checked against the candidate's
// pinned base on resume; a record from another world authorizes nothing here.
type prospectiveRecord struct {
	World  string             `json:"world"`
	Grants []prospectiveGrant `json:"grants"`
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
	// Library directories are decided before commands, so a command can
	// depend only on a library this plan was granted whole. The output keeps
	// the declaration order of the directories.
	byNewDir := map[string][]prospectiveGrant{}
	libraries := map[string]prospectiveEdge{}
	for _, dir := range newDirs {
		if newPackageRole(dir, declarations) == roleGoCommandPackage {
			continue
		}
		byNewDir[dir] = newPackageGrants(ctx, world, dir, planned, declarations, existing, read, nil)
		if len(byNewDir[dir]) == 0 {
			continue
		}
		if e, ok := libraryEdge(ctx, world, dir, read); ok {
			libraries[e.ImportPath] = e
		}
	}
	for _, dir := range newDirs {
		if newPackageRole(dir, declarations) == roleGoCommandPackage {
			byNewDir[dir] = newPackageGrants(ctx, world, dir, planned, declarations, existing, read, libraries)
		}
	}
	for _, dir := range newDirs {
		grants = append(grants, byNewDir[dir]...)
	}
	return grants
}

// prospectiveSurfaceScope is which derived subject files may serve as covering
// surfaces for a plan: those in a planned file's directory (the per-file
// roles' same-directory surfaces), and those a new-package declaration names
// as its covering surface, deduplicated. No other file is a surface.
func prospectiveSurfaceScope(planned []string, declarations []ProspectiveSurface) (sameDir, named map[string]bool) {
	sameDir, named = map[string]bool{}, map[string]bool{}
	for _, p := range planned {
		sameDir[path.Dir(path.Clean(p))] = true
	}
	for _, d := range declarations {
		if s := strings.TrimSpace(d.Covering); s != "" && prospectiveRoles[d.Role].newPackage {
			named[path.Clean(s)] = true
		}
	}
	return sameDir, named
}

// newPackageRole is the role of the first new-package declaration in dir, the
// one newPackageGrants requires every production declaration there to share.
func newPackageRole(dir string, declarations []ProspectiveSurface) string {
	for _, d := range declarations {
		if prospectiveRoles[d.Role].newPackage && path.Dir(path.Clean(strings.TrimSpace(d.Path))) == dir {
			return d.Role
		}
	}
	return ""
}

// libraryEdge is the edge a command may take to the granted library in dir:
// its import path, read from the module path in the governing go.mod at the
// pinned world. A module whose path cannot be read offers no edge.
func libraryEdge(ctx context.Context, world, dir string, read worldReader) (prospectiveEdge, bool) {
	mod, ok := goModuleDir(ctx, world, dir, read)
	if !ok {
		return prospectiveEdge{}, false
	}
	f := "go.mod"
	if mod != "." {
		f = mod + "/go.mod"
	}
	src, err := read(ctx, world, f)
	if err != nil {
		return prospectiveEdge{}, false
	}
	modPath := goModulePath(src)
	if modPath == "" {
		return prospectiveEdge{}, false
	}
	return prospectiveEdge{ImportPath: modPath + "/" + moduleRel(mod, dir), Dir: dir, Module: mod}, true
}

// goModulePath reads the module directive of a go.mod, or "" when it has none.
func goModulePath(src []byte) string {
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			if p, err := strconv.Unquote(fields[1]); err == nil {
				return p
			}
			return fields[1]
		}
	}
	return ""
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
//	    except that a go-command-package declaration may list, once, the
//	    import path of a library in libraries -- a same-plan library
//	    directory granted whole -- that resolves to the same go.mod as dir.
//	    That dependency is recorded on the grant as its edge.
//
// A go-regression-test in dir is admitted only through a production grant in
// dir, over the same S and package: the one prospective-to-prospective step.
func newPackageGrants(ctx context.Context, world, dir string, planned []string, declarations []ProspectiveSurface, existing []CoverageAnchor, read worldReader, libraries map[string]prospectiveEdge) []prospectiveGrant {
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
		if role.newPackage {
			production = append(production, f)
		} else {
			tests = append(tests, f)
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
	// N7. The edge belongs to the command package, not to one of its files:
	// every file that takes it takes the same library, and a second distinct
	// library anywhere in the directory refuses the directory whole.
	var edge *prospectiveEdge
	edges := map[string]*prospectiveEdge{}
	for _, f := range files {
		d := declared[f]
		if d.Package != first.Package {
			return nil // a test in the new package carries the package's clause
		}
		if named := strings.TrimSpace(d.Covering); named != "" && path.Clean(named) != covering {
			return nil
		}
		role := prospectiveRoles[d.Role]
		for _, dep := range d.Dependencies {
			if facts.Imports[dep] || role.novel[dep] {
				continue
			}
			lib, ok := libraries[dep]
			if d.Role != roleGoCommandPackage || edges[f] != nil || !ok || lib.Module != mod {
				return nil
			}
			if edge == nil {
				edge = &prospectiveEdge{ImportPath: lib.ImportPath, Dir: lib.Dir, Module: lib.Module}
			}
			if *edge != lib {
				return nil
			}
			edges[f] = edge
		}
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
	return inspectProspectiveSurfacesWithEdges(diff, declarations, facts, nil)
}

// inspectProspectiveSurfacesWithEdges is inspectProspectiveSurfaces with the
// recorded dependency edges, keyed by declared path: a created command may
// import, beyond its covering surface's imports, exactly its recorded edge.
func inspectProspectiveSurfacesWithEdges(diff string, declarations []ProspectiveSurface, facts map[string]prospectiveFacts, edges map[string]string) error {
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
		allowed := recorded.Imports
		imports := make([]string, 0, len(actual.Imports))
		for imp := range actual.Imports {
			imports = append(imports, imp)
		}
		sort.Strings(imports)
		edge := ""
		if d.Role == roleGoCommandPackage {
			edge = edges[f]
		}
		for _, imp := range imports {
			if !allowed[imp] && !role.novel[imp] && (edge == "" || imp != edge) {
				return fmt.Errorf("prospective surface refuted: %s imports %q, which is outside the covering surface's imports and the %s allowance", f, imp, d.Role)
			}
		}
	}
	// A new package was admitted as exactly its declared files: anything else
	// created in that directory was authorized by nothing.
	isDeclared, newDirs := map[string]bool{}, map[string]bool{}
	for _, d := range declarations {
		f := path.Clean(strings.TrimSpace(d.Path))
		isDeclared[f] = true
		if prospectiveRoles[d.Role].newPackage {
			newDirs[path.Dir(f)] = true
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
		for imp := range g.Facts.Imports {
			allowed[imp] = true
		}
		if known {
			for imp := range role.novel {
				allowed[imp] = true
			}
		}
		if g.Edge != nil {
			allowed[g.Edge.ImportPath] = true
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
		} else {
			fmt.Fprintf(&b, "    package: %s (must equal the covering surface's package)\n", g.Surface.Package)
		}
		fmt.Fprintf(&b, "    role: %s\n", g.Surface.Role)
		fmt.Fprintf(&b, "    declared dependencies: %s\n", strings.Join(g.Surface.Dependencies, ", "))
		if g.Edge != nil {
			fmt.Fprintf(&b, "    dependency edge: %s (same-plan library %s)\n", g.Edge.ImportPath, g.Edge.Dir)
		}
		fmt.Fprintf(&b, "    EFFECTIVE ALLOWED IMPORTS (covering surface's imports at the pinned world + the role allowance): %s\n", strings.Join(imports, ", "))
		fmt.Fprintf(&b, "    architectural requirements carried: %s\n", strings.Join(requirements, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}
