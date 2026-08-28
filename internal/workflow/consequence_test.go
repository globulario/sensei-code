package workflow

import "testing"

func TestArchitecturalFilesSubtractAGrantWhateverThePlanSpelling(t *testing.T) {
	// The grant is recorded canonical; the plan may spell the same file
	// "./pkg/x_test.go" or with whitespace. The subtraction must still hold,
	// and the scope it returns must be the canonical spelling.
	a := Action{
		Files:                []string{" ./internal/derived/derived.go", "./internal/derived/derived_test.go "},
		OperationalAuthority: []string{"internal/derived/derived_test.go"},
	}
	got := a.architecturalFiles()
	if len(got) != 1 || got[0] != "internal/derived/derived.go" {
		t.Fatalf("architecturalFiles = %q; want the granted test subtracted and the rest canonical", got)
	}
}
