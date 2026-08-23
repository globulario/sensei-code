package workflow

import "testing"

func TestRouteTallyCountsEveryDestination(t *testing.T) {
	var tally RouteTally
	for _, r := range []Routing{
		{Route: RouteArchitectural}, {Route: RouteCloseGap}, {Route: RouteCloseGap},
		{Route: RouteHuman}, {Route: RouteCannotEstablish},
	} {
		tally.Observe(r)
	}
	if tally.Total != 5 || tally.Granted != 1 || tally.CloseGap != 2 || tally.Human != 1 || tally.CannotEstablish != 1 {
		t.Fatalf("%+v", tally)
	}
}

// An empty denominator has no rate. Printing 0.000 for "nothing has happened
// yet" describes a quiet system as a failing one.
func TestRateIsAbsentWithoutADenominator(t *testing.T) {
	if Rate(0, 0) != nil {
		t.Fatal("an empty denominator produced a rate")
	}
	if r := Rate(1, 4); r == nil || *r != 0.25 {
		t.Fatalf("got %v", r)
	}
}

// A gap that is reported closed and then reappears identically was not closed.
// Recurrence is evidence; the closure report is only a claim.
func TestRecurrenceIsMeasuredByTheGapComingBack(t *testing.T) {
	g := NewGapLedger()
	g.Met("coverage absent for internal/foo")
	g.Closed("coverage absent for internal/foo")
	if len(g.Recurrent()) != 0 {
		t.Fatal("a gap met once is not recurrent")
	}

	g.Met("coverage absent for internal/foo")
	rec := g.Recurrent()
	if len(rec) != 1 || rec[0] != "coverage absent for internal/foo" {
		t.Fatalf("a gap reported closed that came back was not counted as recurrent: %v", rec)
	}
	// Reporting closure does not suppress the recurrence. That is the whole
	// point: the ratchet is judged by the router, not by the agent's account.
	if r := g.RecurrentRate(); r == nil || *r != 1.0 {
		t.Fatalf("got %v", r)
	}
}

func TestGapLedgerRateIsAbsentBeforeAnyGap(t *testing.T) {
	if NewGapLedger().RecurrentRate() != nil {
		t.Fatal("a ledger with no gaps produced a rate")
	}
}
