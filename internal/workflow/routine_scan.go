package workflow

import (
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/sensei"
)

// Stage 2A: counterfactual qualification measurement.
//
// Stage 1 established that the tier is sound and left one question it could not
// answer: does the boundary have useful recall? Nothing in this repository has
// ever qualified, and that is not evidence of bad recall, because the
// instrument only ran inside the governed candidate loop — a telescope inside
// the box it was pointed at. Sensei governing Sensei is also a pathological
// calibration target: a repository whose own governance corpus, authority
// machinery and derived high-risk scope are exactly what one would expect to be
// protected aggressively.
//
// So the classifier is made runnable over any change, whether or not that
// change ever entered the governed loop. It grants nothing here either. It
// answers one question:
//
//	Had routine classification been authoritative for this change, would it
//	have qualified, and on which conditions?
//
// The conditions themselves are frozen. Measuring a boundary and moving it are
// different activities, and doing both at once produces a boundary that fits
// whatever was measured.

// ChangedPathsIn is the paths a diff touches, exported for the counterfactual
// scanner. It is the same reader the governed loop uses, so a replayed change
// and a live candidate are described identically.
func ChangedPathsIn(diff string) []string { return changedPaths(diff) }

// Counterfactual is one change's answer, with the honesty the answer needs.
type Counterfactual struct {
	// Ref identifies the change: a commit, a range, or a synthetic label.
	Ref      string          `json:"ref"`
	Decision RoutineDecision `json:"decision"`
	// Assumed names conditions that could not be evaluated for this change and
	// were treated as satisfied.
	//
	// This is the difference between a measurement and an overstatement. A
	// historical commit carries no architect plan and no claims, so conditions 8
	// and 9 have nothing to check. Counting them silently as satisfied would
	// inflate the qualification rate with conditions nobody tested; refusing the
	// change on their absence would deflate it for a reason that has nothing to
	// do with whether the change was routine. Neither is a measurement. Saying
	// which were assumed is.
	Assumed []string `json:"assumed,omitempty"`
	// Paths is what the change touched, kept so a surprising verdict can be
	// looked at rather than argued about.
	Paths []string `json:"paths,omitempty"`
}

// Qualified reports the counterfactual answer.
func (c Counterfactual) Qualified() bool { return c.Decision.Routine }

// Trustworthy reports whether every condition was actually evaluated. A
// qualification resting on assumed conditions is a weaker claim and is counted
// separately.
func (c Counterfactual) Trustworthy() bool { return len(c.Assumed) == 0 }

// ClassifyCounterfactual answers the Stage 2A question for one change.
//
// planned is the architect's file list where one exists. When it is nil — a
// historical commit, a replayed corpus, anything that never had a plan — the
// changed set stands in for it and condition 8 is recorded as assumed rather
// than tested. claims works the same way: absent claims cannot disqualify, and
// cannot be counted as verified either.
func ClassifyCounterfactual(ref string, scoped sensei.PreflightDecision, edit sensei.EditCheckResult,
	diff string, planned []string, claims []Claim, claimsKnown bool) Counterfactual {

	shape := shapeOf(diff)
	paths := shape.Paths()

	var assumed []string
	if planned == nil {
		planned = paths
		assumed = append(assumed, "8: scope did not widen (no plan existed to widen from)")
	}
	if !claimsKnown {
		assumed = append(assumed, "9: no claim is marked inference (no architect claims were recorded)")
	}

	return Counterfactual{
		Ref:      ref,
		Decision: classifyRoutine(scoped, claims, edit, planned, shape),
		Assumed:  assumed,
		Paths:    paths,
	}
}

// Distribution is what a corpus replay produces.
//
// The count that matters least is the qualification rate. What decides whether
// the tier is sound-but-useless, correctly-idle, or over-constrained is which
// condition does the blocking, and that is only visible as a distribution.
type Distribution struct {
	Corpus      string         `json:"corpus"`
	Total       int            `json:"total"`
	Qualified   int            `json:"qualified"`
	FullyTested int            `json:"fully_tested"`
	Blocking    map[string]int `json:"blocking"`
	// Examples keeps one reference per blocking class, so a category can be
	// inspected instead of speculated about.
	Examples map[string]string `json:"examples,omitempty"`
	// QualifiedRefs enumerates what qualified. A rate without the population is
	// a number nobody can check.
	QualifiedRefs []string `json:"qualified_refs,omitempty"`
}

// Measure builds the distribution for a corpus.
//
// Classes come from generalise, which groups runs stopped by the same condition
// rather than by the same file.
func Measure(corpus string, results []Counterfactual) Distribution {
	d := Distribution{Corpus: corpus, Blocking: map[string]int{}, Examples: map[string]string{}}
	for _, r := range results {
		d.Total++
		if r.Trustworthy() {
			d.FullyTested++
		}
		if r.Qualified() {
			d.Qualified++
			d.QualifiedRefs = append(d.QualifiedRefs, r.Ref)
			continue
		}
		class := generalise(r.Decision.Blocking)
		d.Blocking[class]++
		if _, seen := d.Examples[class]; !seen {
			d.Examples[class] = r.Ref
		}
	}
	return d
}

// Render is the distribution as a person reads it.
func (d Distribution) Render() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s: %d change(s), %d qualified", d.Corpus, d.Total, d.Qualified))
	if d.Total > 0 {
		b.WriteString(fmt.Sprintf(" (%.0f%%)", 100*float64(d.Qualified)/float64(d.Total)))
	}
	b.WriteString("\n")
	if d.Qualified > 0 && d.FullyTested < d.Total {
		b.WriteString(fmt.Sprintf("  %d of %d had every condition evaluated; the rest rested on assumed conditions\n",
			d.FullyTested, d.Total))
	}
	for _, class := range sortedByCount(d.Blocking) {
		line := fmt.Sprintf("  %4d × %s", d.Blocking[class], class)
		if ex := d.Examples[class]; ex != "" {
			line += "   e.g. " + ex
		}
		b.WriteString(line + "\n")
	}
	for _, ref := range d.QualifiedRefs {
		b.WriteString("  qualified: " + ref + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Verdict states what the distribution supports, and refuses to state more.
//
// The three readings are genuinely different actions, and the difference is not
// in the qualification rate but in which population produced it. A zero from a
// repository that is almost entirely governed material says nothing about the
// conditions; a zero from a deliberately constructed positive control says a
// great deal.
func (d Distribution) Verdict() string {
	switch {
	case d.Total == 0:
		return "no changes were measured, so this corpus supports no reading at all"
	case d.Qualified == 0:
		return "nothing qualified: this corpus cannot distinguish a correctly idle tier from an over-constrained one on its own"
	case d.Qualified == d.Total:
		return "everything qualified: a corpus with no negatives cannot show the conditions refusing anything"
	default:
		return fmt.Sprintf("%d of %d qualified, and %d condition class(es) did the blocking",
			d.Qualified, d.Total, len(d.Blocking))
	}
}
