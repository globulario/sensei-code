package workflow

// Three authorities, and none of them inherits from another.
//
// A live self-improvement task asked sensei-code to consolidate `internal/`
// because it had "too much surface area". The architect accepted that premise,
// produced a consolidation plan, and emitted no question. Three different
// things had been collapsed into one:
//
//	requested objective/value   "reduce internal surface area"
//	technical premise           "these packages can be consolidated without
//	                             changing observable behavior"
//	consequence judgment        "this amount of architectural disruption is
//	                             acceptable"
//
// The first is the human's to state, the second is the repository's to answer,
// and the third is a consequence decision the router already assesses. What was
// missing is that nothing kept them apart, and nothing could SAY which was
// which for a planned change.
//
// This file is that statement. It grants nothing and refuses nothing: routing
// is decided in authority.go and consequence.go exactly as before. What it adds
// is the ability to answer, for one plan, which parts came from the requested
// objective, which are technical claims that still need evidence, and which
// consequence decisions remain authority-sensitive.

import (
	"strings"

	"github.com/globulario/sensei-code/internal/finding"
)

// Objective is what was asked for, together with what established that anyone
// asked for it.
//
// Text and Provenance are captured at submission and never revised. Nothing
// downstream may write to this type: an architect that proposes a better
// interpretation of the objective has proposed something, and a proposal is not
// a request.
type Objective struct {
	// Text is the task as submitted, verbatim.
	Text string
	// Provenance is how the task entered, from the entrypoint that started it.
	Provenance Provenance
}

// HumanAuthorized reports whether a person is established to have asked for
// this objective.
//
// True only for RequestedByHuman, which is /run in the interactive session --
// the one entrypoint where the evidence is the interaction itself. Everything
// else is false, including a headless submission whose wording is identical to
// one a human typed: identical wording is not identical provenance, and that is
// precisely the substitution this separation exists to refuse.
//
// There is deliberately no way to set this from a flag, an argument, or a
// provider response. A flag asserting human presence would be the forgeable
// claim it replaces.
//
// ResumedGoverned reads as NOT human-authorized, and that is a known
// understatement rather than a judgement: the resume path does not carry the
// original provenance, so a task a human really did request comes back as
// unestablished. Understating what was established is the safe direction for
// this particular unknown, and TestAResumedTaskDoesNotInventHumanAuthority
// pins the current answer so restoring the carried provenance is a visible
// change rather than a silent one.
func (o Objective) HumanAuthorized() bool { return o.Provenance == RequestedByHuman }

// Lane is which authority a statement belongs to.
type Lane string

const (
	// LaneObjective: what was asked for, and by whom. Value authority.
	LaneObjective Lane = "requested objective"
	// LaneTechnical: a proposition about the repository. Evidence decides it,
	// never a person and never the objective that motivated it.
	LaneTechnical Lane = "technical claim"
	// LaneConsequence: what this action can reach past its boundary. Assessed
	// from the action, independently of who asked and of whether the technical
	// claims hold.
	LaneConsequence Lane = "consequence decision"
)

// AuthorityStatement is what sensei-code can say about one planned change.
type AuthorityStatement struct {
	// Objective is the request, as submitted.
	Objective Objective
	// ObjectiveEstablished reports whether a person is established to have
	// asked for it.
	ObjectiveEstablished bool
	// Technical is every premise the plan rests on, with whether its
	// provenance bears evidence. Populated from the plan's own claims, never
	// from the objective.
	Technical []TechnicalPremise
	// ArchitectProposals are criteria the architect introduced that the
	// objective does not contain. They are the architect's, and stay so.
	ArchitectProposals []string
	// Consequence is what the router assessed about this action.
	Consequence ConsequenceAssessment
	// Route is where the plan was sent, carried so the statement and the
	// decision cannot describe different moments.
	Route Route
}

// TechnicalPremise is one claim the plan rests on.
type TechnicalPremise struct {
	Statement string
	About     string
	// Source is the provenance the architect declared, verbatim -- including a
	// misspelled or empty one, because what it SAID is part of the record.
	Source string
	// EvidenceBearing reports whether that provenance rests on something
	// independently re-checkable. Read through the same closed vocabulary the
	// router and the finding bridge use, so the three cannot drift apart.
	EvidenceBearing bool
}

// StateAuthority separates one planned change into its three authorities.
//
// Every argument is something already established elsewhere: the objective from
// submission, the claims from the plan, the assessment from the action. Nothing
// is re-derived here, because a second derivation is a second answer, and two
// answers about the same moment is the defect this repository keeps finding.
//
// The objective lane is populated ONLY from the objective. That is the whole
// separation, expressed as a data-flow rule rather than as a warning: there is
// no path by which an architect's rationale, a plan's steps, or a provider's
// wording can reach it, so no actor gains authority over one lane by
// controlling another.
func StateAuthority(objective Objective, claims []Claim, assessment ConsequenceAssessment, route Route, d architectureDecision) AuthorityStatement {
	s := AuthorityStatement{
		Objective:            objective,
		ObjectiveEstablished: objective.HumanAuthorized(),
		Consequence:          assessment,
		Route:                route,
	}
	for _, c := range claims {
		s.Technical = append(s.Technical, TechnicalPremise{
			Statement:       strings.TrimSpace(c.Statement),
			About:           strings.TrimSpace(c.About),
			Source:          strings.TrimSpace(c.Source),
			EvidenceBearing: finding.Classify(c.Source).EvidenceBearing(),
		})
	}
	s.ArchitectProposals = architectProposals(objective.Text, d)
	return s
}

// architectProposals is what the architect brought that the request did not.
//
// The check is deliberately crude and its crudeness is the point: it asks
// whether an optimization criterion the architect stated appears in the words
// the human actually wrote. It cannot tell a paraphrase from a new idea, so it
// over-reports -- an architect restating the objective in its own words is
// listed as a proposal.
//
// Over-reporting is the safe direction here. The failure this guards against is
// an architect criterion QUIETLY BECOMING the user's value ("minimize package
// count at all costs" was never asked for), and listing one proposal too many
// costs a line of output while missing one costs the separation.
//
// It is not a relevance judgement and must not grow into one. If this ever
// needs to decide whether a proposal is GOOD, that is a different question with
// a different owner.
func architectProposals(objective string, d architectureDecision) []string {
	asked := strings.ToLower(objective)
	seen := map[string]bool{}
	var out []string
	stated := append([]string{d.Summary, d.Plan, d.Consequences}, d.Steps...)
	for _, stated := range stated {
		stated = strings.TrimSpace(stated)
		if stated == "" {
			continue
		}
		for _, criterion := range optimizationCriteria {
			if !strings.Contains(strings.ToLower(stated), criterion) || strings.Contains(asked, criterion) {
				// Absent from the plan, or present in the request — in which
				// case it is the human's and not the architect's.
				continue
			}
			if seen[criterion] {
				continue
			}
			seen[criterion] = true
			out = append(out, criterion+" — stated by the architect, absent from the request")
		}
	}
	return out
}

// optimizationCriteria are the shapes an unrequested goal arrives in.
//
// A small measured list, not an ontology. These are the phrasings seen in the
// consolidation run that produced this brief; the list grows when a real run
// produces a shape it misses, and never to anticipate one.
var optimizationCriteria = []string{
	"minimize", "minimise", "at all costs", "as few as possible",
	"consolidate", "merge the packages", "reduce the number of",
	"surface area", "simplify the structure",
}

// Render is the statement as a person reads it.
//
// Ordered objective, technical, consequence, and never merged into one list.
// The three are different kinds of thing and a single list of "considerations"
// is how they collapsed in the first place.
func (s AuthorityStatement) Render() string {
	var b strings.Builder
	b.WriteString("Authority for this plan, by lane.\n\n")

	b.WriteString(string(LaneObjective) + ":\n")
	if text := strings.TrimSpace(s.Objective.Text); text != "" {
		b.WriteString("  " + text + "\n")
	}
	if s.ObjectiveEstablished {
		b.WriteString("  requested by a human in the interactive session; accepted as the objective " +
			"within what it states\n")
	} else {
		b.WriteString("  NOT established as a human objective — " + string(s.Objective.Provenance) + "\n")
		b.WriteString("  the wording is the task; it is not a person's authorization for it\n")
	}
	if len(s.ArchitectProposals) != 0 {
		b.WriteString("  the architect additionally proposed, and the request did not ask for:\n")
		for _, p := range s.ArchitectProposals {
			b.WriteString("    - " + p + "\n")
		}
	}

	b.WriteString("\n" + string(LaneTechnical) + "s:\n")
	if len(s.Technical) == 0 {
		b.WriteString("  none stated\n")
	}
	for _, t := range s.Technical {
		mark := "NEEDS EVIDENCE"
		if t.EvidenceBearing {
			mark = "evidence-bearing"
		}
		about := ""
		if t.About != "" {
			about = " (" + t.About + ")"
		}
		source := t.Source
		if source == "" {
			source = "no source declared"
		}
		b.WriteString("  [" + mark + "] " + t.Statement + about + " — source: " + source + "\n")
	}
	b.WriteString("  the objective does not establish any of these. Evidence does.\n")

	b.WriteString("\n" + string(LaneConsequence) + ":\n")
	b.WriteString("  " + string(s.Consequence.Result) + " — " + s.Consequence.Boundary + "\n")
	for _, e := range s.Consequence.Effects {
		b.WriteString("    effect: " + e + "\n")
	}
	b.WriteString("  assessed from the action, not from who asked and not from whether the plan " +
		"satisfies the objective.\n")
	b.WriteString("\nrouted: " + string(s.Route) + "\n")
	return b.String()
}
