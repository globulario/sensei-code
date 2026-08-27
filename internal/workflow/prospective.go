package workflow

import (
	"context"
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
	Package string
	Imports map[string]bool
}

// worldReader returns the bytes of a path at the pinned world, or an error
// when the path is not there. It exists so the predicate can be tested without
// a repository; the engine supplies gitShowAt.
type worldReader func(ctx context.Context, world, file string) ([]byte, error)

// gitShowAt reads `git show <world>:<file>` in root. It never consults the
// working tree.
func gitShowAt(root string) worldReader {
	return func(ctx context.Context, world, file string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", "-C", root, "--no-optional-locks", "show", world+":"+file)
		b, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git show %s:%s: %w", world, file, err)
		}
		return b, nil
	}
}

// prospectiveGrant is one admissible declaration: the anchor the router reads
// and the facts about S the post-creation inspection checks the created file
// against.
type prospectiveGrant struct {
	Surface  ProspectiveSurface
	Covering string
	Facts    prospectiveFacts
	Anchor   CoverageAnchor
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
		if _, err := read(ctx, world, f); err == nil {
			continue // F exists at world: it is not a create
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
			grants = append(grants, prospectiveGrant{
				Surface:  d,
				Covering: s.File,
				Facts:    facts,
				Anchor: CoverageAnchor{
					File:        f,
					Requirement: s.Requirement,
					Describe: fmt.Sprintf("PROSPECTIVE %s %s: create authorized by %s at %s; %s",
						d.Role, f, s.File, shortWorldID(world), s.Describe),
				},
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
// covering surface's facts at the pinned world; a declaration with no entry
// was authorized by nothing, so only the role's own allowance applies to it.
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
		allowed := facts[f].Imports
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
