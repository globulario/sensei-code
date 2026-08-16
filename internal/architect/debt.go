package architect

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Surface is one part of the repository, and whether anything governs it.
type Surface struct {
	Path  string
	Files int
	Lines int
	// Protected is true when an authored invariant names this path or something
	// inside it.
	Protected bool
}

// Debt is the ungoverned part of a repository, largest first.
//
// This is the debt that accumulates fastest when agents write the code. An
// agent will not refuse a change to a surface nothing governs, because there is
// nothing there to refuse it: no invariant to check, no forbidden fix to
// recognise, no required test to run. Coverage percentages hide that, so this
// names the specific places instead.
type Debt struct {
	// Source names where the protected paths came from, because this is derived
	// from the authored corpus rather than from the live graph.
	Source       string
	Surfaces     []Surface
	TotalFiles   int
	CoveredFiles int
}

// ProtectedPaths reads the paths the authored invariants claim to protect.
//
// It reads the repository's own corpus rather than the graph because the graph
// publishes no per-path index, and it deliberately does not interpret anything
// beyond which paths are named: deciding what protection means is Sensei's, not
// this tool's.
func ProtectedPaths(corpusDir string) ([]string, error) {
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(corpusDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		for _, p := range parseProtectedPaths(string(body)) {
			seen[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// parseProtectedPaths pulls the entries of every `protects: files:` block. It is
// a deliberate line scan rather than a YAML parse: the shape is fixed by
// Sensei's own schema, and `sensei check` is what validates the corpus.
func parseProtectedPaths(body string) []string {
	var out []string
	inProtects, inFiles := false, false
	for _, raw := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(raw)
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		switch {
		case trimmed == "protects:":
			inProtects, inFiles = true, false
		case inProtects && trimmed == "files:":
			inFiles = true
		case inFiles && strings.HasPrefix(trimmed, "- "):
			out = append(out, strings.Trim(strings.TrimPrefix(trimmed, "- "), `"' `))
		case trimmed == "":
			// blank lines do not end a block
		case indent == 0 || (inFiles && !strings.HasPrefix(trimmed, "- ")):
			inProtects, inFiles = false, false
		}
	}
	return out
}

// skipDirs are never reported: they are not this repository's source.
var skipDirs = map[string]bool{
	".git": true, "vendor": true, "node_modules": true, "bin": true,
	".sensei": true, ".sensei-code": true, ".claude": true, ".agents": true, ".cursor": true,
}

// Measure walks the repository and classifies each source directory.
func Measure(repoRoot string, protected []string, extensions []string) (Debt, error) {
	debt := Debt{Source: "authored invariants in docs/awareness"}
	byDir := map[string]*Surface{}
	err := filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, p)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && rel != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !hasExtension(rel, extensions) || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		dir := path.Dir(filepath.ToSlash(rel))
		if _, ok := byDir[dir]; !ok {
			byDir[dir] = &Surface{Path: dir}
		}
		byDir[dir].Files++
		byDir[dir].Lines += countLines(p)
		return nil
	})
	if err != nil {
		return Debt{}, err
	}
	for _, s := range byDir {
		s.Protected = isProtected(s.Path, protected)
		debt.TotalFiles += s.Files
		if s.Protected {
			debt.CoveredFiles += s.Files
		}
		debt.Surfaces = append(debt.Surfaces, *s)
	}
	// Largest ungoverned surface first: that is where an unchecked change does
	// the most damage.
	sort.SliceStable(debt.Surfaces, func(i, j int) bool {
		if debt.Surfaces[i].Protected != debt.Surfaces[j].Protected {
			return !debt.Surfaces[i].Protected
		}
		return debt.Surfaces[i].Lines > debt.Surfaces[j].Lines
	})
	return debt, nil
}

// isProtected reports whether an authored invariant names this directory, a
// parent of it, or a file inside it.
func isProtected(dir string, protected []string) bool {
	dir = strings.TrimSuffix(filepath.ToSlash(dir), "/")
	for _, p := range protected {
		p = strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(p)), "/")
		if p == "" {
			continue
		}
		if p == dir || strings.HasPrefix(dir+"/", p+"/") || strings.HasPrefix(p+"/", dir+"/") {
			return true
		}
	}
	return false
}

func hasExtension(name string, extensions []string) bool {
	for _, ext := range extensions {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

func countLines(p string) int {
	body, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	return strings.Count(string(body), "\n")
}

// Render lists the ungoverned surfaces.
func (d Debt) Render(limit int) string {
	var b strings.Builder
	b.WriteString("Ungoverned surfaces\n")
	b.WriteString("  protected paths read from " + d.Source + "\n")

	var ungoverned []Surface
	for _, s := range d.Surfaces {
		if !s.Protected {
			ungoverned = append(ungoverned, s)
		}
	}
	if d.TotalFiles > 0 {
		b.WriteString(fmt.Sprintf("  %d of %d source files sit under a path some invariant names\n",
			d.CoveredFiles, d.TotalFiles))
	}
	if len(ungoverned) == 0 {
		b.WriteString("\n  every source directory is named by at least one invariant\n")
	} else {
		b.WriteString("\n  nothing governs these, largest first:\n")
		shown := ungoverned
		if limit > 0 && len(shown) > limit {
			shown = shown[:limit]
		}
		for _, s := range shown {
			b.WriteString(fmt.Sprintf("    %-34s %3d files  %5d lines\n", s.Path, s.Files, s.Lines))
		}
		if len(shown) < len(ungoverned) {
			// Never truncate silently: a hidden remainder reads as a shorter list.
			b.WriteString(fmt.Sprintf("    … and %d more not shown\n", len(ungoverned)-len(shown)))
		}
	}

	b.WriteString("\n  what this does not establish:\n")
	b.WriteString("    - a named path is not a checked one: naming a directory in an invariant\n")
	b.WriteString("      does not mean the rule is enforced over everything inside it\n")
	b.WriteString("    - this reads the authored corpus, so an invariant not yet published to\n")
	b.WriteString("      the graph still counts here, and one published from elsewhere does not\n")
	return strings.TrimRight(b.String(), "\n")
}
