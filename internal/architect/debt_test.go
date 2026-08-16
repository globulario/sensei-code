package architect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProtectedPathsReadsEveryProtectsBlock(t *testing.T) {
	corpus := `invariants:
  - id: one
    protects:
      files:
        - internal/assist/
        - cmd/sensei-code/assist.go
    required_tests:
      - internal/assist/packet_test.go:TestX
  - id: two
    protects:
      files:
        - internal/sensei/mcp.go
`
	got := parseProtectedPaths(corpus)
	want := map[string]bool{"internal/assist/": true, "cmd/sensei-code/assist.go": true, "internal/sensei/mcp.go": true}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %d paths", got, len(want))
	}
	for _, p := range got {
		if !want[p] {
			t.Fatalf("parsed an unexpected path %q from %v", p, got)
		}
	}
}

func TestRequiredTestsAreNotMistakenForProtectedPaths(t *testing.T) {
	// required_tests entries are also "- " lines and would silently inflate
	// coverage if the scan did not stop at the end of the files block.
	for _, p := range parseProtectedPaths(`invariants:
  - id: one
    protects:
      files:
        - internal/assist/
    required_tests:
      - internal/assist/packet_test.go:TestX
`) {
		if strings.Contains(p, "TestX") {
			t.Fatalf("a required test was counted as a protected path: %q", p)
		}
	}
}

func TestProtectionMatchesDirectoriesAndFilesInside(t *testing.T) {
	protected := []string{"internal/assist/", "cmd/sensei-code/assist.go"}
	for _, dir := range []string{"internal/assist", "cmd/sensei-code"} {
		if !isProtected(dir, protected) {
			t.Fatalf("%s should be protected", dir)
		}
	}
	if isProtected("internal/tui", protected) {
		t.Fatal("internal/tui is not named by any invariant and must count as ungoverned")
	}
}

func TestMeasureRanksTheLargestUngovernedSurfaceFirst(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, lines int) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.Repeat("x\n", lines)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/assist/a.go", 10)
	write("internal/big/b.go", 200)
	write("internal/small/c.go", 20)
	write("internal/big/b_test.go", 500) // tests are not the governed surface

	debt, err := Measure(root, []string{"internal/assist/"}, []string{".go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(debt.Surfaces) == 0 || debt.Surfaces[0].Path != "internal/big" {
		t.Fatalf("largest ungoverned surface is not first: %+v", debt.Surfaces)
	}
	if debt.Surfaces[0].Lines != 200 {
		t.Fatalf("test files were counted into the governed surface: %+v", debt.Surfaces[0])
	}
	if debt.CoveredFiles != 1 || debt.TotalFiles != 3 {
		t.Fatalf("coverage = %d/%d, want 1/3", debt.CoveredFiles, debt.TotalFiles)
	}
}

func TestRenderNeverTruncatesSilently(t *testing.T) {
	debt := Debt{Source: "corpus", TotalFiles: 3}
	for _, name := range []string{"a", "b", "c"} {
		debt.Surfaces = append(debt.Surfaces, Surface{Path: name, Files: 1, Lines: 1})
	}
	out := debt.Render(1)
	if !strings.Contains(out, "and 2 more not shown") {
		t.Fatalf("a truncated list did not say so:\n%s", out)
	}
}
