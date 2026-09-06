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
	TaskID           string `json:"task_id"`
	ObjectiveDigest  string `json:"objective_digest"`
	BaseSHA          string `json:"base_sha"`
	GraphBuildCommit string `json:"graph_build_commit"`
}

var lowerHex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
var lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BindArchitecture computes the objective identity from the exact submitted
// bytes. It does not trim or normalize them: two objectives that render alike
// but differ in bytes are two inputs, and a reply to one must not answer the
// other.
func BindArchitecture(taskID, objective, baseSHA, graphBuildCommit string) ArchitectureBinding {
	sum := sha256.Sum256([]byte(objective))
	return ArchitectureBinding{
		TaskID:           strings.TrimSpace(taskID),
		ObjectiveDigest:  hex.EncodeToString(sum[:]),
		BaseSHA:          strings.TrimSpace(baseSHA),
		GraphBuildCommit: strings.TrimSpace(graphBuildCommit),
	}
}

// Valid reports whether all four referents are present in canonical form.
func (b ArchitectureBinding) Valid() bool {
	return strings.TrimSpace(b.TaskID) != "" &&
		lowerHex64.MatchString(b.ObjectiveDigest) &&
		lowerHex40.MatchString(b.BaseSHA) &&
		lowerHex40.MatchString(b.GraphBuildCommit)
}

// Same reports whether two architecture artifacts answer the same exact
// objective in the same repository and graph world.
func (b ArchitectureBinding) Same(other ArchitectureBinding) bool {
	return b == other
}

// CheckObjective proves the digest still names the objective text in hand.
func (b ArchitectureBinding) CheckObjective(objective string) error {
	want := BindArchitecture(b.TaskID, objective, b.BaseSHA, b.GraphBuildCommit).ObjectiveDigest
	if b.ObjectiveDigest != want {
		return fmt.Errorf("objective digest %s does not name the supplied objective %s", b.ObjectiveDigest, want)
	}
	return nil
}
