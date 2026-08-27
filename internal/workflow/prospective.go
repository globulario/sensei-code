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
}

// prospectiveRole is one governed shape a created file may take. The set is
// closed and read by membership: a role name absent from prospectiveRoles is
// UNRESOLVED, never "some other role".
type prospectiveRole struct {
	// pathGlob is matched against the file's base name.
	pathGlob string
	// novel is the only import set the role admits beyond S's own imports.
	novel map[string]bool
}

// roleGoRegressionTest is the one role this change defines.
const roleGoRegressionTest = "go-regression-test"

var prospectiveRoles = map[string]prospectiveRole{
	roleGoRegressionTest: {pathGlob: "*_test.go", novel: map[string]bool{"testing": true}},
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
	}
	return nil
}

// sameSurface compares declarations field by field, dependencies as a set.
func sameSurface(a, b ProspectiveSurface) bool {
	if path.Clean(strings.TrimSpace(a.Path)) != path.Clean(strings.TrimSpace(b.Path)) || a.Package != b.Package || a.Role != b.Role || len(a.Dependencies) != len(b.Dependencies) {
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

	var grants []prospectiveGrant
	for _, d := range declarations {
		f := path.Clean(strings.TrimSpace(d.Path))
		if f == "." || !isPlanned[f] {
			continue // a declaration with no matching planned file
		}
		if _, err := read(ctx, world, f); !confirmedMissing(err) {
			continue // F exists at world, or its existence could not be established: not a create
		}
		// Clause C, read by membership.
		role, ok := prospectiveRoles[d.Role]
		if !ok {
			continue
		}
		if matched, err := path.Match(role.pathGlob, path.Base(f)); err != nil || !matched {
			continue
		}
		for _, s := range byDir[path.Dir(f)] {
			if s.File == f {
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
	return grants
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
		if matched, err := path.Match(role.pathGlob, path.Base(f)); err != nil || !matched {
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
		for _, imp := range imports {
			if !allowed[imp] && !role.novel[imp] {
				return fmt.Errorf("prospective surface refuted: %s imports %q, which is outside the covering surface's imports and the %s allowance", f, imp, d.Role)
			}
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
