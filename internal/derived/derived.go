// Package derived consumes Sensei's machine-derived architectural facts.
//
// The ratchet this closes: a coverage gap that an agent investigated once
// should not have to be investigated again. What persists is NOT the answer —
// it is the question worth asking here, as a typed proposition plus the
// derivation that can decide it. Sensei recomputes the answer against the world
// being assessed, every time.
//
//	recipe        durable, inert, may be written by anyone
//	revalidate    `sensei derive` reads pinned source and computes
//	anchor        coverage, and only from a successful current-world derivation
//
// # The collapse this package must never permit
//
//	recipe present  ->  coverage
//
// A recipe is a question. Counting questions as knowledge turns "I know what to
// ask here" into "I know the answer", and would let a fabricated file close a
// real gap. So Anchor has unexported fields and no exported constructor: the
// only way to obtain one is Revalidate returning a DERIVED outcome. A caller
// holding a Recipe has nothing to pass anywhere.
//
// Recipes are therefore safe to commit and safe to let an agent write. The
// worst a forged one achieves is spending one derivation.
package derived

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Recipe is a durable question: which proposition is worth checking here.
//
// Inert by construction. Nothing in it asserts the proposition holds, and no
// field is read as authority.
type Recipe struct {
	Kind  string `json:"kind"`
	Dir   string `json:"dir"`
	Type  string `json:"type"`
	Field string `json:"field"`
	Lock  string `json:"lock"`
	// Why records the investigation that produced the question. Provenance for
	// a human reader; never consulted when deciding anything.
	Why string `json:"why,omitempty"`
}

func (r Recipe) String() string {
	return fmt.Sprintf("%s(%s.%s under %s.%s) in %s", r.Kind, r.Type, r.Field, r.Type, r.Lock, r.Dir)
}

// Outcome mirrors `sensei derive`'s three answers. UNKNOWN stays distinct from
// NOT_DERIVED: "nobody taught Sensei to compute this" and "Sensei checked and
// it is false" are different findings.
type Outcome string

const (
	Derived    Outcome = "DERIVED"
	NotDerived Outcome = "NOT_DERIVED"
	Unknown    Outcome = "UNKNOWN"
)

// Anchor is coverage backed by a derivation that succeeded in the world being
// assessed.
//
// Unexported fields, no exported constructor. The only producer is Revalidate.
type Anchor struct {
	recipe Recipe
	world  string
	// files are the SUBJECT files: what the proposition is about. Not what the
	// derivation read — those are different sets, and using the second was a
	// shipped defect that covered internal/event/event.go with a proposition
	// about Bus.subs under Bus.mu.
	files []string
	scope []string
}

// Covers reports whether this anchor speaks for a file in a world.
//
// Both must match. An anchor established at the base does not cover a candidate
// world, which is the temporal hole: reusing it across assessments would let a
// fact outlive the code it described.
func (a Anchor) Covers(world, file string) bool {
	if a.world == "" || a.world != world {
		return false
	}
	for _, f := range a.files {
		if f == file {
			return true
		}
	}
	return false
}

// World is the revision this anchor speaks for.
func (a Anchor) World() string { return a.world }

// Files are the inputs the derivation read.
func (a Anchor) Files() []string { return append([]string(nil), a.files...) }

// Describe renders the anchor with the derivation's envelope attached, so a
// coverage claim never reads stronger than what produced it.
func (a Anchor) Describe() string {
	return fmt.Sprintf("%s — derived in %s over %s; unobserved: %s",
		a.recipe, shortWorld(a.world), strings.Join(a.files, ", "), strings.Join(a.scope, "; "))
}

// Result is one revalidation.
type Result struct {
	Recipe  Recipe
	Outcome Outcome
	Detail  string
	Anchor  *Anchor
}

// Revalidator recomputes a recipe against a world.
type Revalidator interface {
	Revalidate(ctx context.Context, repoRoot, revision string, r Recipe) Result
}

// receipt is the subset of `sensei derive -json` this package reads.
type receipt struct {
	Result string `json:"result"`
	Detail string `json:"detail"`
	Commit string `json:"pinned_commit"`
	// Inputs are the files the derivation READ. They are deliberately not used
	// for coverage: a derivation parses a whole package to resolve types, so
	// this list includes files the proposition says nothing about.
	Inputs []string `json:"independently_observed_inputs"`
	// Subjects are the entities the proposition is ABOUT, computed by the
	// derivation from its own proof. Coverage comes from here.
	Subjects []struct {
		File   string `json:"file"`
		Entity string `json:"entity"`
		Role   string `json:"role"`
	} `json:"subjects"`
	CompletenessScope []string `json:"completeness_scope"`
}

// subjectFiles are the distinct files the proposition is about.
//
// internal/event/event.go contains neither Bus nor mu and was once covered by a
// proposition about Bus.subs under Bus.mu, because it appeared in Inputs. A file
// may be necessary to compute a truth without being something that truth says
// anything about.
func (r receipt) subjectFiles() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range r.Subjects {
		f := strings.TrimSpace(s.File)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// CLI revalidates by running `sensei derive`.
//
// Sensei reads the pinned source itself. This process supplies the question and
// none of the answer, which is the same rule that governs evidence references.
type CLI struct{ Bin string }

func (c CLI) Revalidate(ctx context.Context, repoRoot, revision string, r Recipe) Result {
	bin := c.Bin
	if strings.TrimSpace(bin) == "" {
		bin = "sensei"
	}
	cmd := exec.CommandContext(ctx, bin, "derive", "-json",
		"-repo-root", repoRoot, "-revision", revision,
		"-kind", r.Kind, "-dir", r.Dir, "-type", r.Type, "-field", r.Field, "-lock", r.Lock)
	out, err := cmd.Output()
	if len(out) == 0 {
		return Result{Recipe: r, Outcome: Unknown,
			Detail: fmt.Sprintf("sensei derive produced no receipt: %v", err)}
	}
	var rec receipt
	if jsonErr := json.Unmarshal(out, &rec); jsonErr != nil {
		return Result{Recipe: r, Outcome: Unknown,
			Detail: fmt.Sprintf("sensei derive returned an unreadable receipt: %v", jsonErr)}
	}
	res := Result{Recipe: r, Outcome: Outcome(rec.Result), Detail: rec.Detail}
	if res.Outcome != Derived {
		return res
	}
	// The one construction site. Everything above is inert until here, and this
	// runs only on a DERIVED receipt from the world being assessed.
	//
	// Extent is the SUBJECT files. A derivation that named no subjects covers
	// nothing rather than falling back to everything it read.
	subjects := rec.subjectFiles()
	if len(subjects) == 0 {
		res.Outcome = Unknown
		res.Detail = "the derivation named no subjects, so there is nothing this proposition is about; " +
			"coverage may not fall back to the files it read — " + rec.Detail
		return res
	}
	res.Anchor = &Anchor{recipe: r, world: rec.Commit, files: subjects, scope: rec.CompletenessScope}
	return res
}

// LoadRecipes reads the committed questions. A missing file is no recipes,
// which is the normal first state rather than an error.
func LoadRecipes(path string) ([]Recipe, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc struct {
		Recipes []Recipe `json:"recipes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return doc.Recipes, nil
}

// AnchorsFor revalidates every recipe against one world and returns the anchors
// that derived there.
//
// Recipes that do not derive contribute nothing. There is no partial credit and
// no "probably still true".
func AnchorsFor(ctx context.Context, rv Revalidator, repoRoot, revision string, recipes []Recipe) ([]Anchor, []Result) {
	var anchors []Anchor
	var results []Result
	for _, r := range recipes {
		res := rv.Revalidate(ctx, repoRoot, revision, r)
		results = append(results, res)
		if res.Anchor != nil {
			anchors = append(anchors, *res.Anchor)
		}
	}
	return anchors, results
}

// CoveredFiles returns which of the planned files a set of anchors covers in a
// world, and which they do not.
func CoveredFiles(anchors []Anchor, world string, planned []string) (covered, uncovered []string) {
	for _, f := range planned {
		hit := false
		for _, a := range anchors {
			if a.Covers(world, f) {
				hit = true
				break
			}
		}
		if hit {
			covered = append(covered, f)
		} else {
			uncovered = append(uncovered, f)
		}
	}
	sort.Strings(covered)
	sort.Strings(uncovered)
	return covered, uncovered
}

func shortWorld(w string) string {
	if len(w) > 12 {
		return w[:12]
	}
	return w
}
