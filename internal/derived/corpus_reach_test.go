package derived

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Does Repair 1 reach the corpus it was built to change?
//
// Repair 1 wired DerivedCoverage into authority routing so a derivable fact can
// close a coverage gap instead of a human. The wiring is real. This asks the
// separate question the wiring cannot answer: is there anything to send through
// it for the tasks the campaign measures?
//
// derivedCoverage loads docs/awareness/derived_recipes.json and covers a file
// only when a recipe's dir contains it. So the reach of the repair is bounded by
// the directories those recipes name, and that is checkable without spending a
// provider token -- which is the whole point of checking it here rather than
// discovering it after eleven governed arms.
func TestDerivedCoverageReachesTheBenchmarkCorpus(t *testing.T) {
	recipes, err := LoadRecipes("../../docs/awareness/derived_recipes.json")
	if err != nil {
		t.Fatalf("recipes: %v", err)
	}
	dirs := map[string]bool{}
	for _, r := range recipes {
		dirs[strings.Trim(r.Dir, "/")] = true
	}

	b, err := os.ReadFile("../../benchmark/repair-verification-v2/manifest.json")
	if err != nil {
		t.Skip("corpus absent")
	}
	var m struct {
		Tasks []struct {
			ID     string `json:"id"`
			Oracle struct {
				Paths []string `json:"paths"`
			} `json:"oracle"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}

	var reached []string
	for _, task := range m.Tasks {
		for _, p := range task.Oracle.Paths {
			if dirs[filepath.Dir(p)] {
				reached = append(reached, task.ID)
				break
			}
		}
	}

	t.Logf("recipes: %d, covering %v", len(recipes), keys(dirs))
	t.Logf("tasks reached: %d of %d", len(reached), len(m.Tasks))

	// A TRIPWIRE, not an assertion that zero is correct.
	//
	// Zero is the measured state today: one recipe, covering internal/event,
	// which no task in the corpus touches. Repair 1's channel is wired and
	// carries nothing here, so running REPAIR_VERIFICATION now would re-observe
	// the proof-v6 refusals and credit the repair for a result it could not
	// have caused.
	//
	// The moment a recipe covers a corpus directory this test fails, which is
	// the intended signal: the campaign's expectation must be restated and the
	// wave re-armed deliberately rather than inheriting an assumption.
	if len(reached) != 0 {
		t.Fatalf("derived coverage now reaches %d task(s) (%v). This is the tripwire firing, not "+
			"a defect: REPAIR_VERIFICATION was armed when the reach was zero. Restate what the "+
			"campaign expects to change and re-arm it before running", len(reached), reached)
	}
	t.Log("REACH IS ZERO — REPAIR_VERIFICATION cannot measure Repair 1 on this corpus")
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
