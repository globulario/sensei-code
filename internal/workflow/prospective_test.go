package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The gosumcheck-shaped world: one covered surface S at the pinned world, and a
// plan that creates a regression test beside it.
const (
	prospectiveWorld = "0123456789abcdef0123456789abcdef01234567"
	gosumcheckS      = "gosumcheck/gosumcheck.go"
	gosumcheckF      = "gosumcheck/gosumcheck_test.go"
	gosumcheckSrc    = `package gosumcheck

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/mod/sumdb"
)

func Check() { fmt.Println(os.Args, strings.ToUpper("x"), sumdb.ErrSecurity) }
`
)

// worldOf is a pinned world with exactly the files given; reading anything
// else fails, as `git show <world>:<missing>` does.
func worldOf(files map[string]string) worldReader {
	return func(_ context.Context, world, file string) ([]byte, error) {
		if world != prospectiveWorld {
			return nil, errors.New("wrong world")
		}
		src, ok := files[file]
		if !ok {
			return nil, errors.New("not at world: " + file)
		}
		return []byte(src), nil
	}
}

func gosumcheckAnchors() []CoverageAnchor {
	return []CoverageAnchor{{File: gosumcheckS, Requirement: RequirementInvocationConfinement,
		Describe: "command_invocation_confined_to — derived in 0123456789ab over gosumcheck/gosumcheck.go; unobserved: nothing"}}
}

func gosumcheckDeclaration() ProspectiveSurface {
	return ProspectiveSurface{Path: gosumcheckF, Package: "gosumcheck", Role: roleGoRegressionTest,
		Dependencies: []string{"testing", "strings", "golang.org/x/mod/sumdb"}}
}

func prospectiveFor(t *testing.T, planned []string, decl []ProspectiveSurface, anchors []CoverageAnchor, files map[string]string) []prospectiveGrant {
	t.Helper()
	return prospectiveAnchors(context.Background(), prospectiveWorld, planned, decl, anchors, worldOf(files))
}

// The positive case: a correct declaration for gosumcheck-shaped input yields
// one PROSPECTIVE anchor carrying S's requirement and naming S.
func TestACorrectDeclarationYieldsAProspectiveAnchorCarryingSRequirement(t *testing.T) {
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	if len(grants) != 1 {
		t.Fatalf("expected one grant, got %+v", grants)
	}
	a := grants[0].Anchor
	if a.File != gosumcheckF || a.Requirement != RequirementInvocationConfinement {
		t.Fatalf("anchor does not carry S's requirement for F: %+v", a)
	}
	if !strings.HasPrefix(a.Describe, "PROSPECTIVE ") || !strings.Contains(a.Describe, gosumcheckS) {
		t.Fatalf("anchor description must say PROSPECTIVE and name S: %q", a.Describe)
	}
	if grants[0].Covering != gosumcheckS || !grants[0].Facts.Imports["golang.org/x/mod/sumdb"] {
		t.Fatalf("grant does not record S's facts at the pinned world: %+v", grants[0])
	}
}

// Each falsifier leaves F UNCOVERED.
func TestProspectiveFalsifiersLeaveTheFileUncovered(t *testing.T) {
	world := map[string]string{gosumcheckS: gosumcheckSrc}
	planned := []string{gosumcheckS, gosumcheckF}
	cases := []struct {
		name    string
		planned []string
		decl    []ProspectiveSurface
		anchors []CoverageAnchor
		files   map[string]string
	}{
		{"wrong package", planned, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Package = "gosumcheck_test"
			return d
		}()}, gosumcheckAnchors(), world},
		{"wrong directory", []string{gosumcheckS, "other/gosumcheck_test.go"}, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Path = "other/gosumcheck_test.go"
			return d
		}()}, gosumcheckAnchors(), world},
		{"wrong role", planned, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Role = "go-helper"
			return d
		}()}, gosumcheckAnchors(), world},
		{"path not matching the role", []string{gosumcheckS, "gosumcheck/helpers.go"}, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Path = "gosumcheck/helpers.go"
			return d
		}()}, gosumcheckAnchors(), world},
		{"novel import outside the allowance", planned, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Dependencies = append(d.Dependencies, "net/http")
			return d
		}()}, gosumcheckAnchors(), world},
		{"S not covered by any anchor", planned, []ProspectiveSurface{gosumcheckDeclaration()}, nil, world},
		{"declaration with no matching planned file", []string{gosumcheckS}, []ProspectiveSurface{gosumcheckDeclaration()}, gosumcheckAnchors(), world},
		{"planned file with no declaration at all", planned, nil, gosumcheckAnchors(), world},
		{"F already exists at world", planned, []ProspectiveSurface{gosumcheckDeclaration()}, gosumcheckAnchors(),
			map[string]string{gosumcheckS: gosumcheckSrc, gosumcheckF: "package gosumcheck\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if grants := prospectiveFor(t, c.planned, c.decl, c.anchors, c.files); len(grants) != 0 {
				t.Fatalf("%s: the file was covered: %+v", c.name, grants)
			}
		})
	}
}

// The engine reads only S's bytes at the pinned world, never the working tree:
// a covering surface the world cannot show establishes nothing.
func TestAcoveringSurfaceAbsentAtTheWorldGrantsNothing(t *testing.T) {
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		gosumcheckAnchors(), map[string]string{})
	if len(grants) != 0 {
		t.Fatalf("a surface unreadable at the world covered F: %+v", grants)
	}
}

func createdDiff(path, src string) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\nindex 0000000..1111111\n--- /dev/null\n+++ b/" + path + "\n@@ -0,0 +1 @@\n")
	for _, line := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

func gosumcheckFacts(t *testing.T) map[string]prospectiveFacts {
	t.Helper()
	facts, err := parseGoFacts([]byte(gosumcheckSrc))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]prospectiveFacts{gosumcheckF: facts}
}

// After creation, a file whose imports exceed S's imports plus the allowance
// is refuted, and the refusal says so in its first words.
func TestACreatedFileWhoseImportsExceedTheAllowanceIsRefuted(t *testing.T) {
	diff := createdDiff(gosumcheckF, "package gosumcheck\n\nimport (\n\t\"net/http\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = http.Get }\n")
	err := inspectProspectiveSurfaces(diff, []ProspectiveSurface{gosumcheckDeclaration()}, gosumcheckFacts(t))
	if err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") || !strings.Contains(err.Error(), "net/http") {
		t.Fatalf("expected a refutation naming net/http, got %v", err)
	}
}

// The authorized shape, produced: nothing to refute. And the other mismatches
// -- not created, wrong package -- are refuted the same way.
func TestPostCreationInspectionAcceptsTheAuthorizedShapeOnly(t *testing.T) {
	decl := []ProspectiveSurface{gosumcheckDeclaration()}
	good := createdDiff(gosumcheckF, "package gosumcheck\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = strings.ToUpper }\n")
	if err := inspectProspectiveSurfaces(good, decl, gosumcheckFacts(t)); err != nil {
		t.Fatalf("the authorized shape was refuted: %v", err)
	}
	if err := inspectProspectiveSurfaces("", decl, gosumcheckFacts(t)); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
		t.Fatalf("a declared file the candidate did not create was not refuted: %v", err)
	}
	wrongPkg := createdDiff(gosumcheckF, "package gosumcheck_test\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	if err := inspectProspectiveSurfaces(wrongPkg, decl, gosumcheckFacts(t)); err == nil || !strings.Contains(err.Error(), "package") {
		t.Fatalf("a wrong package clause was not refuted: %v", err)
	}
	// No grant recorded for the declaration: only the role allowance applies.
	if err := inspectProspectiveSurfaces(good, decl, nil); err == nil || !strings.Contains(err.Error(), `"strings"`) {
		t.Fatalf("an unauthorized declaration was inspected against imports nobody established: %v", err)
	}
}
