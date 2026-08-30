package legacy

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheGovernedLoopNeverReachesThisAdapter is the boundary itself, not a
// description of one.
//
// Moving FromEvents into its own package keeps the CORE clean, but nothing
// stopped internal/workflow from importing the adapter directly. Saying the
// governed loop must not reach it, in a comment, is the same class of claim
// that let a parser be tested against invented specimens: documented, not
// enforced. So it is enforced here -- the reconstruct-afterwards architecture
// cannot come back wearing a Go package.
func TestTheGovernedLoopNeverReachesThisAdapter(t *testing.T) {
	const adapter = "github.com/globulario/sensei-code/internal/runreceipt/legacy"
	root := "../../.."
	fset := token.NewFileSet()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules", "experiments":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// The adapter and its own tests are allowed to be itself.
		if strings.Contains(filepath.ToSlash(path), "internal/runreceipt/legacy/") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return nil // unparseable files are not import evidence
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == adapter {
				t.Errorf("%s imports the legacy adapter. The governed loop reconstructs nothing: "+
					"it emits its own receipt, and this adapter exists only to read historical streams.", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}
