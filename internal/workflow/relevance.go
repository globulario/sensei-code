package workflow

// Truthfulness is necessary for coverage. It is not sufficient.
//
// The first two derivation families proved Sensei can establish narrow
// technical facts from project reality. The mirror-image failure mode is that a
// proposition can be true, independently derivable, and still architecturally
// useless for the uncertainty that blocked the task. The preserved adversary is
// a broad layering fact:
//
//	package P does not import net/http
//
// 100% true, 100% derivable, and it says almost nothing about the architectural
// question that caused CLOSE_GAP. While ANY derived fact over a file closed a
// coverage gap over that file, an agent could manufacture autonomy honestly —
// by selecting cheap, wide, irrelevant truths.
//
// The rule for this slice:
//
//	A derived anchor may close only the specific architectural uncertainty it
//	actually resolves.
//
// What is NOT here, deliberately: a relevance engine, a semantic ontology, and
// any use of a model's agreement as the establishing test. The relation below
// is a small closed mapping the CONSUMER owns, in the same fail-closed shape as
// the blind-spot classifier and the claim-provenance reader. Reach is a
// tiebreaker after significance and soundness, never a selector.

import (
	"sort"
	"strings"
)

// Requirement is the kind of architectural question a gap needs answered, or
// that a derivation is able to answer.
type Requirement string

const (
	// RequirementUnrecognised is a derivation family this consumer has no
	// reading for, including one that arrives with no kind at all.
	//
	// It resolves nothing. That is the whole defence against the adversary: a
	// new family cannot manufacture coverage by arriving before anyone decided
	// what architectural question it answers. Same rule as an unrecognised
	// blind spot and an unrecognised claim source, for the same reason.
	RequirementUnrecognised Requirement = "unrecognised"
	// RequirementUnqualified is a GAP that did not say what it needs.
	//
	// Distinct from unrecognised, and the distinction carries the honesty of
	// this whole file: unrecognised means "something was said and nobody has
	// read it", unqualified means "nothing was said at all". Sensei's coverage
	// vocabulary is measured, and today it is entirely unqualified — see
	// TestEveryProbedCoverageGapIsUnqualified.
	RequirementUnqualified Requirement = "unqualified"
	// RequirementLockDiscipline: which lock guards which field, and whether
	// every access takes it. Answered by field_access_under_lock.
	RequirementLockDiscipline Requirement = "lock discipline"
	// RequirementInvocationConfinement: whether one executable is invoked only
	// from the package claimed to own it. Answered by
	// command_invocation_confined_to.
	RequirementInvocationConfinement Requirement = "invocation confinement"
	// RequirementMutationConfinement: whether an exported field is written
	// only from the package that declares its type. Answered by
	// state_mutation_confined_to_owner. A composition family: state identity,
	// ownership, mutation sites and the authorized boundary, related.
	RequirementMutationConfinement Requirement = "mutation confinement"
)

// requirementOfFamily says what a derivation family is able to answer.
//
// Owned HERE, in the consumer, and that is the point rather than an
// implementation detail. A recipe is data anyone may commit — the package doc
// in internal/derived says so, and says the worst a forged one achieves is
// spending one derivation. This mapping is the reason that stays true: a recipe
// cannot declare what it resolves, because the declaration is not a field.
//
// A family nobody has taught this switch resolves NOTHING, however true its
// propositions are. Adding one is a deliberate act with a documented gate —
// see TestAFamilyMustAnswerTheAdmissionQuestions.
func requirementOfFamily(kind string) Requirement {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "field_access_under_lock":
		return RequirementLockDiscipline
	case "command_invocation_confined_to":
		return RequirementInvocationConfinement
	case "state_mutation_confined_to_owner":
		return RequirementMutationConfinement
	default:
		return RequirementUnrecognised
	}
}

// CoverageAnchor is one planned file a derivation covered in this world,
// carrying what that derivation is able to answer.
//
// This replaced a bare []string of file paths. The list said a file was
// covered and could not say covered FOR WHAT, so subject overlap alone decided
// coverage — which is exactly the adversary: `P is DERIVED` was read as
// `P resolves gap G`.
//
// Requirement is computed by the engine from the anchor's family through
// requirementOfFamily. It is never read off the wire and never set by a
// provider.
type CoverageAnchor struct {
	File        string
	Requirement Requirement
	// Describe is the derivation's own envelope, carried so a refusal can say
	// what the anchor actually established rather than only that it was
	// rejected.
	Describe string
}

// gapRequirement reads what the graph said it lacks.
//
// Structured identity for the unresolved gap, computed from Sensei's OWN
// response before any proposition is known — never free text invented after the
// fact, and never anything the plan supplied.
//
// The honest measured answer today is RequirementUnqualified for every marker
// in the observed vocabulary: Sensei reports THAT it has no facts about a
// region and never which architectural question it therefore cannot answer.
// The markers below exist so that a future vocabulary which does say gets read
// rather than flattened, and so the day it starts saying is a test failure
// rather than a silent no-op.
func gapRequirement(spots []string) Requirement {
	for _, s := range spots {
		t := strings.ToLower(strings.TrimSpace(s))
		for marker, req := range qualifiedGapMarkers {
			if strings.Contains(t, marker) {
				return req
			}
		}
	}
	return RequirementUnqualified
}

// qualifiedGapMarkers are coverage blind spots that name the question they
// cannot answer. None is in the currently observed vocabulary.
var qualifiedGapMarkers = map[string]Requirement{
	"no lock discipline established": RequirementLockDiscipline,
	"concurrent access unverified":   RequirementLockDiscipline,
	"invocation sites unknown":       RequirementInvocationConfinement,
	"confinement unverified":         RequirementInvocationConfinement,
}

// derivationClosesGap reports whether these anchors resolve this gap over these
// files.
//
// Two conditions, and neither alone is coverage:
//
//   - every planned file is covered. Partial derived coverage is not coverage:
//     a plan touching a file the derivation says nothing about is a plan over an
//     uncovered region, whatever it established elsewhere.
//   - the covering anchor's requirement is one this consumer recognises, and —
//     where the gap named a requirement — is that requirement.
//
// Subject overlap is therefore necessary and no longer sufficient. A true
// negative layering fact over the same files resolves nothing, because nobody
// has established which architectural question the layering family answers.
func derivationClosesGap(gap Requirement, anchors []CoverageAnchor, planned []string) (bool, string) {
	if len(planned) == 0 || len(anchors) == 0 {
		return false, ""
	}
	byFile := map[string][]CoverageAnchor{}
	for _, a := range anchors {
		byFile[a.File] = append(byFile[a.File], a)
	}
	var resolved []string
	for _, f := range planned {
		hit := false
		for _, a := range byFile[f] {
			if !satisfies(a.Requirement, gap) {
				continue
			}
			hit = true
			resolved = append(resolved, a.Describe)
			break
		}
		if !hit {
			return false, ""
		}
	}
	sort.Strings(resolved)
	return true, strings.Join(dedupe(resolved), "; ")
}

// satisfies is the sufficiency relation, and it is deliberately tiny.
//
// An unrecognised derivation satisfies nothing at all, including an unqualified
// gap. That is the fail-closed half. The other half is that a RECOGNISED
// derivation satisfies an unqualified gap: the gap did not state a requirement,
// and refusing every derivation on that basis would mean no gap in the
// currently observed vocabulary is ever closable — which would delete the
// working lock-discipline closure for internal/event/bus.go along with the
// adversary.
//
// That asymmetry is the live limit of this slice and is stated rather than
// hidden: while gaps stay unqualified, relevance is enforced at the FAMILY
// level — has anyone established what this kind of proposition answers — and
// not at the proposition level. Closing that gap needs Sensei's coverage
// response to say what it cannot answer, which is upstream of this repository.
func satisfies(anchor, gap Requirement) bool {
	// Read from the closed set of what a derivation can RESOLVE, rather than by
	// excluding the two sentinels.
	//
	// The first draft excluded them, and this file's own attack table caught it
	// within the hour: an arbitrary value -- a provider-supplied "relevant", or
	// the empty string -- is neither sentinel and therefore satisfied an
	// unqualified gap. Identical fail-open shape to the claim-provenance defect
	// (#80), where recognising only "inference" let every other unrecognised
	// source through, and to the blind-spot classifier before it. Three
	// instances now: a closed vocabulary read by exclusion is read wrong.
	if !resolvable[anchor] {
		return false
	}
	if gap == RequirementUnqualified {
		return true
	}
	return anchor == gap
}

// resolvable is every requirement a derivation can actually resolve.
//
// Membership is the whole check. RequirementUnrecognised and
// RequirementUnqualified are absent because they are not answers, and anything
// nobody added is absent because nobody added it -- which is how a future
// requirement fails closed instead of arriving as silent autonomy.
var resolvable = map[Requirement]bool{
	RequirementLockDiscipline:        true,
	RequirementInvocationConfinement: true,
	RequirementMutationConfinement:   true,
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
