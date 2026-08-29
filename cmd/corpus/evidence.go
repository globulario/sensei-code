package main

// The evidence representation model.
//
// This file exists because sixteen adversarial review rounds on PR #118
// found the same defect wearing sixteen faces: the reader INFERRED a schema
// from filenames, and three consumers -- discovery, part collection and
// opening -- each recombined the answers independently. Every repair in one
// semantic dimension exposed another, because there was no single place that
// said what a name IS.
//
// Two laws, and everything below is their mechanism.
//
//	LAW 1 (partition). Every physical name in a directory has exactly one
//	role and at most one owner. Ambiguity is an error, never precedence.
//
//	LAW 2 (certification). A logical stream's bytes become evidence only
//	when the observed physical representation exactly satisfies a complete
//	declared representation. Failure to observe an alternative
//	representation is not evidence that no alternative exists.
//
// The rules the rounds produced are consequences of those two:
//
//	recognition    one definition decides what a stream is (isStreamName)
//	resolution     a piece belongs to the LONGEST prefix that is itself a
//	               valid stream name (partOf)
//	exclusivity    a name is a stream, a piece, or an artifact -- never two
//	ownership      an artifact belongs to a stream that EXISTS beside it,
//	               recognised structurally rather than by a suffix list
//	completeness   pieces declare their total, and all of them are present
//	uniqueness     a stream is whole or split, never both
//	authority      every consumer reads this one classification
//
// Consumers do not parse names. They ask for an index.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Role is what a physical name is, exactly once.
type Role string

const (
	// RoleWholeStream: an evidence stream present as one file.
	RoleWholeStream Role = "whole-stream"
	// RolePart: one ordered piece of a stream too large to transit a tool
	// call, or a name claiming to be one. A CLAIM is enough to enter the
	// machinery: a malformed piece must be refused, never ignored.
	RolePart Role = "part"
	// RoleArtifact: a run artifact belonging to a stream that exists.
	RoleArtifact Role = "artifact"
	// RoleIrrelevant: nothing this corpus reads.
	RoleIrrelevant Role = "irrelevant"
)

// partMarker separates a stream from the sequence of one of its pieces.
const partMarker = ".part-"

// partSequence is a piece's tail: its number and the TOTAL, which every
// piece declares. Contiguity from 001 proves there is no hole; only the
// declared total proves the tail is present.
var partSequence = regexp.MustCompile(`^(\d{3,})-of-(\d{3,})$`)

// isStreamName decides, once, whether a name is an evidence stream.
//
// A receipts file is a stream's artifact, never a stream; it is exempt from
// part claims by OWNERSHIP (see artifactOf), never by how it is spelled.
func isStreamName(name string) bool {
	if strings.HasSuffix(name, ".receipts.jsonl") {
		return false
	}
	return strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".jsonl")
}

// streamBase is what a stream's artifacts are named from: the stream
// without its extension, which is what extract uses to find `.run`,
// `.receipts.jsonl` and `.recipes-after.json`.
func streamBase(stream string) string {
	return strings.TrimSuffix(strings.TrimSuffix(stream, ".log"), ".jsonl")
}

// partOf returns the logical stream a name claims to be a piece of, or "".
//
// A name is either a stream or a piece of one, never both. Among the
// prefixes that qualify, the LONGEST wins: `A.log.part-x.log` is itself a
// stream, so its piece belongs to it and not to the shorter `A.log`.
func partOf(name string) string {
	if isStreamName(name) {
		return ""
	}
	logical := ""
	for i := 0; i+len(partMarker) <= len(name); i++ {
		if name[i:i+len(partMarker)] == partMarker && isStreamName(name[:i]) {
			logical = name[:i]
		}
	}
	return logical
}

// wellFormedPart reports a piece's stream, index and declared total.
func wellFormedPart(name string) (logical string, index, total int, ok bool) {
	logical = partOf(name)
	if logical == "" {
		return "", 0, 0, false
	}
	m := partSequence.FindStringSubmatch(name[len(logical)+len(partMarker):])
	if m == nil {
		return logical, 0, 0, false
	}
	index, _ = strconv.Atoi(m[1])
	total, _ = strconv.Atoi(m[2])
	return logical, index, total, index >= 1 && total >= 1 && index <= total
}

// artifactOwners are the streams a name is a run artifact of.
//
// Plural on purpose. `A.log` and `A.jsonl` are two streams with the same
// base `A`, so `A.run` belongs to both -- and extract builds sidecar names
// from the base, so both encounters would silently receive the same run
// stamp and recipes. Law 1 admits at most ONE owner and says ambiguity is
// an error, never precedence: returning the first match quietly chose
// precedence (#119 review).
func artifactOwners(name string, streams []string) []string {
	// Resolution applies here exactly as it applies to pieces: the LONGEST
	// matching base wins. `N1.void1-provider-quota.run` matches the base of
	// both `N1.log` and `N1.void1-provider-quota.log`, and it belongs to the
	// second -- treating that as ambiguity would refuse two real encounters
	// in this repository's own corpus. What remains genuinely ambiguous is a
	// tie: `A.log` and `A.jsonl` share the base `A` exactly, so `A.run`
	// belongs to both and to neither.
	longest := -1
	var owners []string
	for _, stream := range streams {
		if name == stream {
			continue
		}
		base := streamBase(stream)
		if !strings.HasPrefix(name, base+".") {
			continue
		}
		if rest := name[len(base):]; strings.Contains(rest, partMarker) {
			continue
		}
		switch {
		case len(base) > longest:
			longest, owners = len(base), []string{stream}
		case len(base) == longest:
			owners = append(owners, stream)
		}
	}
	return owners
}

// artifactOf reports that a name is a run artifact of one of these streams.
//
// Ownership, not a suffix catalogue: a list is always incomplete, and the
// runs this repository commits carry `.graph.metadata.pre.json` and
// `.candidate.diff` beside the three files extract reads. An artifact is
// <streamBase> + rest for a stream that EXISTS, where rest carries no
// marker -- so a name claiming to be a piece is never exempted by looking
// like metadata.
func artifactOf(name string, streams []string) bool {
	return len(artifactOwners(name, streams)) != 0
}

// Part is one ordered piece of a split representation.
type Part struct {
	Name  string
	Index int
	Total int
}

// Representation is how one logical stream is physically present.
//
// Whole and Parts are mutually exclusive; Problem records why a stream
// cannot be read, and a stream with a Problem is never opened.
type Representation struct {
	Whole   string
	Parts   []Part
	Problem error
}

// EvidenceIndex is one directory's classification, computed once.
type EvidenceIndex struct {
	Dir     string
	Streams map[string]*Representation
	Roles   map[string]Role
}

// StreamNames are the logical streams, in a stable order.
func (ix *EvidenceIndex) StreamNames() []string {
	out := make([]string, 0, len(ix.Streams))
	for name := range ix.Streams {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Files are the physical files a stream's bytes come from, in order.
func (ix *EvidenceIndex) Files(stream string) ([]string, error) {
	rep, ok := ix.Streams[stream]
	if !ok {
		return nil, fmt.Errorf("%s: no representation of this stream is present", filepath.Join(ix.Dir, stream))
	}
	if rep.Problem != nil {
		return nil, rep.Problem
	}
	if rep.Whole != "" {
		return []string{filepath.Join(ix.Dir, rep.Whole)}, nil
	}
	out := make([]string, 0, len(rep.Parts))
	for _, p := range rep.Parts {
		out = append(out, filepath.Join(ix.Dir, p.Name))
	}
	return out, nil
}

// BuildEvidenceIndex classifies every name in a directory, exactly once.
//
// Ordering of the input cannot change the result: identities are resolved
// first, roles second, representations third.
func BuildEvidenceIndex(dir string, names []string) *EvidenceIndex {
	ix := &EvidenceIndex{Dir: dir, Streams: map[string]*Representation{}, Roles: map[string]Role{}}

	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	// 1. Identities: whole files, and streams present only as well-formed
	// pieces. A malformed piece cannot invent an identity for itself.
	var identities []string
	for _, name := range sorted {
		if isStreamName(name) {
			identities = append(identities, name)
			continue
		}
		if logical, _, _, ok := wellFormedPart(name); ok {
			identities = append(identities, logical)
		}
	}
	identities = dedupe(identities)

	// 2. Roles: exactly one per name.
	claims := map[string][]Part{}
	malformed := map[string][]string{}
	for _, name := range sorted {
		switch {
		case isStreamName(name):
			ix.Roles[name] = RoleWholeStream
			rep := ix.representation(name)
			if rep.Whole != "" && rep.Whole != name {
				rep.Problem = fmt.Errorf("%s: two whole files claim this stream", filepath.Join(dir, name))
			}
			rep.Whole = name
		case artifactOf(name, identities):
			ix.Roles[name] = RoleArtifact
			// At most one owner. Two streams sharing a base share every
			// artifact built from it, and neither may be read on the
			// strength of evidence that belongs to both.
			if owners := artifactOwners(name, identities); len(owners) > 1 {
				for _, owner := range owners {
					r := ix.representation(owner)
					if r.Problem == nil {
						r.Problem = fmt.Errorf("%s: %s is an artifact of %d streams (%s); one artifact has at most one owner",
							filepath.Join(dir, owner), name, len(owners), strings.Join(owners, ", "))
					}
				}
			}
		case partOf(name) != "":
			ix.Roles[name] = RolePart
			logical, index, total, ok := wellFormedPart(name)
			if !ok {
				malformed[logical] = append(malformed[logical], name)
				ix.representation(logical)
				continue
			}
			claims[logical] = append(claims[logical], Part{Name: name, Index: index, Total: total})
			ix.representation(logical)
		default:
			ix.Roles[name] = RoleIrrelevant
		}
	}

	// 3. Representations, and Law 2 over each.
	for logical, bad := range malformed {
		ix.Streams[logical].Problem = fmt.Errorf("%s: %s is not a well-formed piece (expected <stream>%s<nnn>-of-<mmm>)",
			filepath.Join(dir, logical), bad[0], partMarker)
	}
	for logical, parts := range claims {
		rep := ix.Streams[logical]
		if rep.Problem != nil {
			continue
		}
		// Numeric order, never lexical: `part-1000-of-1000` sorts between
		// 100 and 101 as a string, which rejected a complete stream.
		sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
		rep.Parts = parts
		if rep.Whole != "" {
			rep.Problem = fmt.Errorf("%s: the stream is present both whole and as %d piece(s); one encounter has one representation",
				filepath.Join(dir, logical), len(parts))
			continue
		}
		declared := parts[0].Total
		for i, p := range parts {
			if p.Total != declared {
				rep.Problem = fmt.Errorf("%s: the pieces disagree about how many there are: %d and %d",
					filepath.Join(dir, logical), declared, p.Total)
				break
			}
			if p.Index != i+1 {
				rep.Problem = fmt.Errorf("%s: the pieces are not contiguous: expected piece %d, found %s",
					filepath.Join(dir, logical), i+1, p.Name)
				break
			}
		}
		if rep.Problem == nil && len(parts) != declared {
			rep.Problem = fmt.Errorf("%s: the pieces declare %d but only %d are present; the stream is truncated",
				filepath.Join(dir, logical), declared, len(parts))
		}
	}
	return ix
}

func (ix *EvidenceIndex) representation(logical string) *Representation {
	if rep, ok := ix.Streams[logical]; ok {
		return rep
	}
	rep := &Representation{}
	ix.Streams[logical] = rep
	return rep
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
