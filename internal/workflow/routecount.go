package workflow

// Route accounting.
//
// Item 9 of the brief, and the order matters: count before gating. A route that
// is measured can be argued about from evidence; a route that is gated on
// before anyone knows how often it fires is a policy invented from a diagram.
//
// None of these numbers is an authority input. Nothing in the router or the
// engine reads them, and that is deliberate — the moment a burden metric feeds
// a grant decision, the cheapest way to improve it is to grant more.
//
// The question they exist to answer is whether the fourth route actually
// reduces burden or merely relabels it:
//
//	recurrent_gap_rate           did the same gap come back
//	closure_to_grant_rate        did closing it make the work grantable
//	human_interruptions/task     did the burden fall

import "sort"

// RouteTally counts what the router decided across a set of decisions.
type RouteTally struct {
	Granted         int `json:"granted"`
	CloseGap        int `json:"close_gap"`
	Human           int `json:"human"`
	CannotEstablish int `json:"cannot_establish"`
	Total           int `json:"total"`
}

// Observe records one routing.
func (t *RouteTally) Observe(r Routing) {
	t.Total++
	switch r.Route {
	case RouteArchitectural:
		t.Granted++
	case RouteCloseGap:
		t.CloseGap++
	case RouteHuman:
		t.Human++
	case RouteCannotEstablish:
		t.CannotEstablish++
	}
}

// Rate is a count over a denominator, absent when the denominator is empty.
//
// A pointer rather than a float, for the reason the evaluation scorers already
// use one: an empty denominator has no rate, and printing 0.000 for "nothing
// happened yet" describes a quiet system as a failing one.
func Rate(numerator, denominator int) *float64 {
	if denominator <= 0 {
		return nil
	}
	r := float64(numerator) / float64(denominator)
	return &r
}

// GapLedger tracks gap conditions across a run so recurrence is visible.
//
// Recurrence is the measurement that says whether the ratchet works. A gap that
// is "closed" and then reappears identically was not closed; it was narrated.
type GapLedger struct {
	seen   map[string]int
	closed map[string]bool
}

// NewGapLedger returns an empty ledger.
func NewGapLedger() *GapLedger {
	return &GapLedger{seen: map[string]int{}, closed: map[string]bool{}}
}

// Met records that a gap condition was encountered.
func (g *GapLedger) Met(condition string) {
	if g.seen == nil {
		g.seen = map[string]int{}
	}
	g.seen[condition]++
}

// Closed records that a closure round reported success for a condition.
//
// It is the agent's report, not proof. Whether the gap ACTUALLY closed is
// answered by Recurrent below — by the condition not coming back — which is
// evidence rather than a claim.
func (g *GapLedger) Closed(condition string) {
	if g.closed == nil {
		g.closed = map[string]bool{}
	}
	g.closed[condition] = true
}

// Recurrent returns conditions met more than once, sorted.
//
// These are the failures of the ratchet: whatever the closure round reported,
// the router saw the same gap again.
func (g *GapLedger) Recurrent() []string {
	var out []string
	for c, n := range g.seen {
		if n > 1 {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// Distinct is how many different gaps were met.
func (g *GapLedger) Distinct() int { return len(g.seen) }

// RecurrentRate is recurrent gaps over distinct gaps, absent when none were met.
func (g *GapLedger) RecurrentRate() *float64 {
	return Rate(len(g.Recurrent()), len(g.seen))
}
