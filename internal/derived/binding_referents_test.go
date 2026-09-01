package derived

// A reference is not evidence that its referent exists.
//
// Authored awareness YAML binds invariants and failure modes to proofs by
// writing "<path>_test.go:TestName". Nothing validates that either half of
// that string resolves. A binding whose file was deleted, or whose function
// was renamed, is structurally perfect and carries ZERO protection: it reads
// as covered, it satisfies a reviewer skimming the entry, and the test it
// names can never fail because it does not exist.
//
// Found two live instances when the rule was finally checked rather than
// assumed (see the PR that added this file), one of them on a critical
// invariant. Both had live successors, so the knowledge was not wrong -- it
// had simply stopped pointing at anything.
//
// It lives in this package because internal/derived already owns the
// corpus-integrity tests that read ../../docs/awareness; it is not about
// derived recipes.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// binding matches "some/path/foo_test.go:TestName" anywhere on a line.
var bindingRe = regexp.MustCompile(`([\w./\-]+_test\.go):(Test\w+)`)

func TestEveryAuthoredTestBindingResolves(t *testing.T) {
	const corpus = "../../docs/awareness"
	const repoRoot = "../.."

	type ref struct{ file, fn, src string }
	var refs []ref

	err := filepath.Walk(corpus, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			// Commented-out template blocks legitimately carry placeholder
			// bindings such as "path/to/config_test.go:TestX". They are
			// documentation, not claims, and matching them would report a
			// defect that is not there.
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			for _, m := range bindingRe.FindAllStringSubmatch(line, -1) {
				refs = append(refs, ref{m[1], m[2], filepath.Base(path)})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", corpus, err)
	}
	if len(refs) == 0 {
		t.Fatal("no test bindings found in the corpus — this check would pass vacuously")
	}

	// Cache each test file's bytes: the corpus names the same file many times.
	seen := map[string]string{}
	read := func(p string) (string, bool) {
		if body, ok := seen[p]; ok {
			return body, body != ""
		}
		b, err := os.ReadFile(filepath.Join(repoRoot, p))
		if err != nil {
			seen[p] = ""
			return "", false
		}
		seen[p] = string(b)
		return string(b), true
	}

	var dead int
	for _, r := range refs {
		body, ok := read(r.file)
		if !ok {
			dead++
			t.Errorf("%s binds %s:%s — THAT FILE DOES NOT EXIST, so the binding proves nothing",
				r.src, r.file, r.fn)
			continue
		}
		if !regexp.MustCompile(`func\s+` + regexp.QuoteMeta(r.fn) + `\s*\(`).MatchString(body) {
			dead++
			t.Errorf("%s binds %s:%s — the file exists but declares no such function, so the binding proves nothing",
				r.src, r.file, r.fn)
		}
	}
	t.Logf("%d authored test bindings checked, %d dead", len(refs), dead)
}
