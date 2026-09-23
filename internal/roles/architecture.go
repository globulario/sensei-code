package roles

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// ArchitectureBinding names the exact pre-candidate question an architect is
// allowed to answer.
//
// It is deliberately separate from Binding. A review is about candidate
// content {task, base, digest, tree}; architecture happens before a candidate
// exists and is instead about {task, objective, base, graph}. Reusing the
// review binding would either invent candidate identity or leave the architect
// unbound to the objective that gave the task meaning.
type ArchitectureBinding struct {
	TaskID          string `json:"task_id"`
	ObjectiveDigest string `json:"objective_digest"`
	BaseSHA         string `json:"base_sha"`
	// GraphRepository and GraphBuildCommit are ONE fact in two fields, and
	// neither half is meaningful alone.
	//
	// BaseSHA is a WORKSPACE object; GraphBuildCommit is SENSEI GRAPH
	// provenance, and the two live in different repositories. A pinned commit
	// emitted without the repository that owns it leaves its consumer to guess
	// which repository to look in -- and a guess that happens to be wrong looks
	// exactly like a commit that does not exist. On 2026-09-22 graph commit
	// 05feaf64d2694e97ac42b6bb93fbb49b9851a1f1 was looked up in
	// globulario/sensei-code, which answered 422 no commit found, while the
	// commit sat in globulario/sensei the whole time.
	GraphRepository  string `json:"graph_repository"`
	GraphBuildCommit string `json:"graph_build_commit"`
}

var lowerHex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
var lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// repositoryPath is an "owner/name" GitHub repository, the only shape that can
// route a lookup. It deliberately admits nothing else: a half-written value is
// an unusable route, and routing must fail closed rather than nearly work.
var repositoryPath = regexp.MustCompile(`^[0-9A-Za-z._-]+/[0-9A-Za-z._-]+$`)

// BindArchitecture computes the objective identity from the exact submitted
// bytes. It does not trim or normalize them: two objectives that render alike
// but differ in bytes are two inputs, and a reply to one must not answer the
// other. An absent objective produces no digest at all, so a missing workflow
// record cannot masquerade as the perfectly valid SHA-256 of an empty string.
func BindArchitecture(taskID, objective, baseSHA, graphRepository, graphBuildCommit string) ArchitectureBinding {
	digest := ""
	if objective != "" {
		sum := sha256.Sum256([]byte(objective))
		digest = hex.EncodeToString(sum[:])
	}
	return ArchitectureBinding{
		TaskID:           strings.TrimSpace(taskID),
		ObjectiveDigest:  digest,
		BaseSHA:          strings.TrimSpace(baseSHA),
		GraphRepository:  strings.TrimSpace(graphRepository),
		GraphBuildCommit: strings.TrimSpace(graphBuildCommit),
	}
}

// Valid reports whether every referent is present in canonical form.
func (b ArchitectureBinding) Valid() bool {
	return strings.TrimSpace(b.TaskID) != "" &&
		lowerHex64.MatchString(b.ObjectiveDigest) &&
		lowerHex40.MatchString(b.BaseSHA) &&
		b.GraphProvenanceValid()
}

// GraphProvenanceValid reports whether the graph provenance pair is complete.
//
// The pair is inseparable: a repository with no commit names no evidence, and a
// commit with no repository can only be resolved by inferring one. Absence is
// therefore invalid rather than partially usable, and it is never filled in
// from the workspace or mailbox repository -- those own different evidence.
func (b ArchitectureBinding) GraphProvenanceValid() bool {
	return repositoryPath.MatchString(b.GraphRepository) &&
		lowerHex40.MatchString(b.GraphBuildCommit)
}

// Same reports whether two architecture artifacts answer the same exact
// objective in the same repository and graph world.
func (b ArchitectureBinding) Same(other ArchitectureBinding) bool {
	return b == other
}

// CheckObjective proves the digest still names the objective text in hand.
func (b ArchitectureBinding) CheckObjective(objective string) error {
	want := BindArchitecture(b.TaskID, objective, b.BaseSHA, b.GraphRepository, b.GraphBuildCommit).ObjectiveDigest
	if want == "" || b.ObjectiveDigest != want {
		return fmt.Errorf("objective digest %s does not name the supplied objective %s", b.ObjectiveDigest, want)
	}
	return nil
}
