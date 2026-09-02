// SPDX-License-Identifier: AGPL-3.0-only

// Package campaign records what a bounded self-improvement run actually cost
// and produced, so "Sensei is converging" becomes a measurement rather than an
// impression.
//
// A single successful self-repair proves nothing about convergence. The claim
// is about a TREND across campaigns, and a trend needs the same numbers
// collected the same way each time -- including the numbers that are
// unflattering, which are the ones an impression quietly drops.
//
// # What may not be traded
//
// A campaign may improve its numbers only by improving the system. Three
// figures are recorded as GUARDS rather than achievements, and a campaign that
// moves them in the wrong direction has failed regardless of everything else:
//
//	ProofStrength        falsifiers and mutation checks preserved or increased
//	FailClosed           Unknown/refusal behaviour preserved or increased
//	ConstitutionalMoves  MUST be zero
//
// Deleting a falsifier, turning an Unknown into an empty, lowering a severity
// because a gate is inconvenient, or letting a candidate satisfy its own
// admission criteria all make the other numbers look better. That is why they
// are counted separately and why one of them has no acceptable non-zero value.
//
// # What this package refuses to compute
//
// Several of the document's measures are not mechanical. "Previously learned
// laws autonomously surfaced" requires knowing whether a surfaced law was
// RELEVANT, and relevance is the rung that needs a reader. Those fields are
// typed as explicitly-unmeasured rather than defaulted to zero, because a zero
// reads as "none happened" and an absent measurement is not an observation.
package campaign

import (
	"fmt"
	"strings"
)

// Measured distinguishes a counted value from an unmeasured one.
//
// A plain int cannot: zero means both "none occurred" and "nobody looked", and
// those are the two answers this whole program exists to keep apart.
type Measured struct {
	Value int
	Known bool
}

// Count returns a measured value.
func Count(n int) Measured { return Measured{Value: n, Known: true} }

// Unmeasured returns a value that was not established.
func Unmeasured() Measured { return Measured{} }

func (m Measured) String() string {
	if !m.Known {
		return "unmeasured"
	}
	return fmt.Sprintf("%d", m.Value)
}

// Record is one bounded campaign.
type Record struct {
	Name string

	// IndependentFindings is what a reviewer that is not the implementer
	// found. The headline trend: it should fall.
	IndependentFindings Measured
	// RepeatedFindings instantiate a failure class already recorded in the
	// graph. NovelFindings do not. A campaign whose findings are mostly
	// repeats has learned nothing durable from the last one.
	RepeatedFindings Measured
	NovelFindings    Measured
	// HumanTransfers are the times a human carried a lesson the system already
	// held. Falling means the system is delivering its own knowledge.
	HumanTransfers Measured
	// AutonomousSurfaced counts laws the system brought to a change BEFORE a
	// human or reviewer pointed at the defect class. Rising is the capability
	// under test; it requires judging relevance, so it is often unmeasured.
	AutonomousSurfaced Measured
	// MechanismsRemoved counts duplicated implementations collapsed into one
	// owner. Semantic compression: fewer mechanisms carrying the same meaning.
	MechanismsRemoved Measured

	// GUARDS. Never traded for the numbers above.
	ProofStrengthDelta   Measured // falsifiers/mutation checks added minus removed
	FailClosedDelta      Measured // Unknown/refusal behaviours added minus removed
	ConstitutionalMoves  Measured // MUST be 0
	ConstitutionalDetail string
}

// Verdict is the closed set of campaign outcomes.
type Verdict string

const (
	// VerdictConverging: the trend numbers moved the right way and no guard moved wrong.
	VerdictConverging Verdict = "converging"
	// VerdictNotConverging: the work happened and the trend did not improve.
	VerdictNotConverging Verdict = "not_converging"
	// VerdictVoid: a guard moved the wrong way. The campaign's other numbers
	// are not reported as an achievement, because they may have been bought.
	VerdictVoid Verdict = "void_guard_moved"
	// VerdictUnmeasured: the trend could not be established. NOT converging,
	// and not failing either.
	VerdictUnmeasured Verdict = "unmeasured"
)

// Judge compares this campaign to the previous one.
//
// The guards are checked FIRST and independently. A campaign that weakened
// proof, weakened fail-closed, or moved constitutional authority is void
// whatever its other numbers say -- otherwise the cheapest way to converge is
// to delete the evidence that says you have not.
func (r Record) Judge(prev *Record) (Verdict, string) {
	if r.ConstitutionalMoves.Known && r.ConstitutionalMoves.Value != 0 {
		return VerdictVoid, "constitutional authority moved: " + orNone(r.ConstitutionalDetail)
	}
	if r.ProofStrengthDelta.Known && r.ProofStrengthDelta.Value < 0 {
		return VerdictVoid, fmt.Sprintf("proof strength fell by %d", -r.ProofStrengthDelta.Value)
	}
	if r.FailClosedDelta.Known && r.FailClosedDelta.Value < 0 {
		return VerdictVoid, fmt.Sprintf("fail-closed behaviour fell by %d", -r.FailClosedDelta.Value)
	}
	if prev == nil {
		return VerdictUnmeasured, "no previous campaign to compare against; this is a baseline, not a trend"
	}
	if !r.IndependentFindings.Known || !prev.IndependentFindings.Known {
		return VerdictUnmeasured, "independent findings were not measured in both campaigns"
	}
	if r.IndependentFindings.Value < prev.IndependentFindings.Value {
		return VerdictConverging, fmt.Sprintf("independent findings fell from %d to %d",
			prev.IndependentFindings.Value, r.IndependentFindings.Value)
	}
	return VerdictNotConverging, fmt.Sprintf("independent findings did not fall (%d then %d)",
		prev.IndependentFindings.Value, r.IndependentFindings.Value)
}

// Report renders the record with unmeasured fields VISIBLE as unmeasured, so a
// reader cannot mistake a gap for a zero.
func (r Record) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "campaign %s\n", r.Name)
	for _, row := range []struct {
		label string
		m     Measured
	}{
		{"independent findings", r.IndependentFindings},
		{"  repeated", r.RepeatedFindings},
		{"  novel", r.NovelFindings},
		{"human transfers of old lessons", r.HumanTransfers},
		{"laws autonomously surfaced", r.AutonomousSurfaced},
		{"mechanisms removed", r.MechanismsRemoved},
		{"GUARD proof strength delta", r.ProofStrengthDelta},
		{"GUARD fail-closed delta", r.FailClosedDelta},
		{"GUARD constitutional moves", r.ConstitutionalMoves},
	} {
		fmt.Fprintf(&b, "  %-34s %s\n", row.label, row.m)
	}
	return b.String()
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no detail recorded)"
	}
	return s
}
