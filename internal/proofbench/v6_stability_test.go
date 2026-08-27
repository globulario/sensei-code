package proofbench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadLedger(t *testing.T, root string) []Attempt {
	t.Helper()
	var out []Attempt
	err := filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
		if e != nil || i.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		b, _ := os.ReadFile(p)
		var a Attempt
		if json.Unmarshal(b, &a) != nil || a.MeasurementStatus != "" {
			return nil
		}
		out = append(out, a)
		return nil
	})
	if err != nil || len(out) == 0 {
		t.Skipf("ledger %s absent", root)
	}
	return out
}

// The frozen FINAL_VERDICT must not move under the repaired scorer.
//
// The repair is deliberately inert on attempts recorded before it: they carry
// no TerminalSource and keep their original precedence. Rescoring a published
// result under a rule written afterwards would be the same offence as the
// defect itself, pointed the other way.
func TestTheFrozenFinalVerdictDoesNotMove(t *testing.T) {
	count := func(rows []Attempt, arm Arm) (map[Terminal]int, map[Correctness]int) {
		te, co := map[Terminal]int{}, map[Correctness]int{}
		for _, a := range rows {
			if a.Arm != arm {
				continue
			}
			s := Score(a, "")
			te[s.Terminal]++
			co[s.Correctness]++
		}
		return te, co
	}

	_, rawCorr := count(loadLedger(t, "../../benchmark/proof-v5/runs"), ArmRaw)
	if rawCorr[CorrectnessCorrect] != 8 || rawCorr[CorrectnessIncorrect] != 3 {
		t.Fatalf("RAW moved from 8 CORRECT / 3 INCORRECT to %d / %d",
			rawCorr[CorrectnessCorrect], rawCorr[CorrectnessIncorrect])
	}

	coldTerm, coldCorr := count(loadLedger(t, "../../benchmark/proof-v6/runs"), Arm("COLD"))
	if coldCorr[CorrectnessCorrect] != 4 {
		t.Fatalf("COLD CORRECT moved from 4 to %d", coldCorr[CorrectnessCorrect])
	}
	for term, want := range map[Terminal]int{
		TerminalCompleted: 4, TerminalRefused: 5, TerminalTimeout: 1, TerminalInfraFailure: 1,
	} {
		if coldTerm[term] != want {
			t.Fatalf("COLD %s moved from %d to %d", term, want, coldTerm[term])
		}
	}
}

// Defect #13 was already present in proof-v6 and nobody noticed.
//
// internal-session-4d32937 ended `workflow.awaiting_authority` -- exit code 3,
// the engine naming a refusal -- and was scored INFRA_FAILURE because the word
// "unauthorized" appears somewhere in the transcript of a task about SESSION
// handling, where it is ordinary vocabulary.
//
// The frozen record stands. This test pins what the defect did and what it
// would cost to correct, so the disclosure in the evidence note cannot drift
// from the data.
func TestDefectThirteenAlsoTouchedProofV6(t *testing.T) {
	rows := loadLedger(t, "../../benchmark/proof-v6/runs")
	var found *Attempt
	for i := range rows {
		if rows[i].Task == "internal-session-4d32937" && rows[i].Arm == Arm("COLD") {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Skip("arm absent")
	}
	if found.Terminal != "workflow.awaiting_authority" {
		t.Fatalf("fixture changed: terminal is %q", found.Terminal)
	}
	if found.Infrastructure != "unauthorized" {
		t.Fatalf("fixture changed: infrastructure is %q", found.Infrastructure)
	}
	// As recorded: an outage.
	if got := Score(*found, "").Terminal; got != TerminalInfraFailure {
		t.Fatalf("as frozen this arm scores %s, expected INFRA_FAILURE", got)
	}
	// Under the repaired rule it is what the engine said it was: a refusal.
	repaired := *found
	repaired.TerminalSource = string(TerminalStructuredSpecific)
	if got := Score(repaired, "").Terminal; got != TerminalRefused {
		t.Fatalf("the repair leaves this arm as %s; the engine named it a refusal", got)
	}
	// And the two-axis headline is untouched either way: both are NOT_EVALUATED
	// for correctness and both are end-to-end failures.
	if Score(*found, "").Correctness != Score(repaired, "").Correctness {
		t.Fatal("the correction changed the correctness axis; it must not")
	}
	if Score(*found, "").Delivered != Score(repaired, "").Delivered {
		t.Fatal("the correction changed delivery; it must not")
	}
}
