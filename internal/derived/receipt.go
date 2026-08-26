package derived

// An inference run must be able to say who it was.
//
// V2_ARCHITECTURE_DIRECTION §6.3 requires an immutable receipt for every
// inference run: model identity and version, artifact digest, feature-extractor
// version, input graph digest, configuration, output candidate digests,
// timestamp, resource limits, post-processing version, and any nondeterminism
// declaration.
//
// A closure round proposing a question IS an inference run. It grants no
// architectural truth, which is why it may be autonomous -- but the question of
// WHO PROPOSED IT, FROM WHAT STATE, UNDER WHICH MODEL still has to be
// answerable, or the record of which questions proved useful cannot be traced
// back to the investigator that produced them. That record is the training data
// Phase C would rank, and an unattributable corpus is a bad foundation for it.
//
// # One receipt per RUN, not per recipe
//
// A run that proposes a duplicate, or proposes nothing, still ran. Those are the
// recurrence and decline signals, and storing receipts inside recipes would
// discard exactly the ones that carry them. So receipts are append-only and
// separate, and a recipe is referenced by digest rather than containing its own
// provenance of inference.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PostProcessingVersion is the version of the deterministic handling applied to
// a proposal after the model returned it -- validation, canonical identity,
// deduplication and provenance stamping.
//
// Bumped whenever that handling changes, because the same proposal can be
// accepted by one version and refused by the next, and a receipt that did not
// say which one ran would make two incomparable outcomes look like one.
const PostProcessingVersion = "closure-recipe/v1"

// FeatureExtractorVersion identifies what built the investigator's inputs.
//
// For an LLM investigator that is the prompt construction, not a tensor
// pipeline; the requirement is the same either way. If the prompt changes, the
// question distribution changes, and a receipt that cannot say which prompt ran
// cannot support a comparison across the change.
// v3 removed a placeholder that happened to name a field in the cold-start
// fixture ("lock":"<mu>"). Idiomatic Go, and still a needless coincidence with
// the subject about to be measured -- the selector may know the fixture contains
// an expressible relationship, the investigator must discover which question is
// worth asking from its own inputs.
const FeatureExtractorVersion = "gap-closure-prompt/v3"

// InferenceOutcome is what a run produced. Every value is data about the loop,
// including the ones that produced nothing.
type InferenceOutcome string

const (
	// OutcomeRecorded: a new question was written.
	OutcomeRecorded InferenceOutcome = "RECORDED"
	// OutcomeDuplicate: the question was already known. This is recurrence, and
	// it is a signal rather than a non-event -- it says an investigator keeps
	// arriving at the same question.
	OutcomeDuplicate InferenceOutcome = "DUPLICATE"
	// OutcomeRefused: the proposal did not survive validation.
	OutcomeRefused InferenceOutcome = "REFUSED"
	// OutcomeNoProposal: the round proposed nothing. A legitimate outcome, and
	// the denominator of every rate computed over this loop.
	OutcomeNoProposal InferenceOutcome = "NO_PROPOSAL"
)

// InferenceReceipt is the immutable record of one investigator run.
type InferenceReceipt struct {
	// Model identity. Version and ArtifactDigest are frequently unavailable for
	// a hosted model, and are recorded as empty rather than invented -- see
	// Nondeterminism.
	ModelName      string `json:"model_name"`
	ModelVersion   string `json:"model_version,omitempty"`
	ModelArtifact  string `json:"model_artifact_digest,omitempty"`
	ModelInvoked   string `json:"model_invocation,omitempty"`
	FeatureVersion string `json:"feature_extractor_version"`
	PostProcessing string `json:"post_processing_version"`

	// InputGraphDigest is a hash of the workspace authority the run saw --
	// which graph, how fresh, built from what. It is what makes two runs
	// comparable or not.
	InputGraphDigest string `json:"input_graph_digest"`
	// InputGraphState carries the readable half, so a receipt is auditable
	// without the graph that produced it still being available.
	InputGraphState string `json:"input_graph_state,omitempty"`

	// Config is the inference configuration: the gap being closed, which round,
	// and the region in scope.
	OriginTask string   `json:"origin_task"`
	OriginGap  string   `json:"origin_gap"`
	Round      int      `json:"closure_round"`
	Region     []string `json:"region,omitempty"`

	// Limits are the resource limits the run was subject to.
	ClosureBudget int `json:"closure_budget"`

	// Outcome and CandidateDigest describe what came out. CandidateDigest is
	// the question's content hash, present for every outcome that had a
	// proposal at all -- including a refused one, so a rejected proposal is
	// still identifiable.
	Outcome         InferenceOutcome `json:"outcome"`
	CandidateDigest string           `json:"output_candidate_digest,omitempty"`
	CandidateID     string           `json:"output_candidate_identity,omitempty"`
	// Detail is why, for REFUSED.
	Detail string `json:"detail,omitempty"`

	// Nondeterminism is declared, never assumed absent.
	//
	// §6.3 asks for "any nondeterminism declaration". A hosted LLM is
	// nondeterministic and its weights are not addressable by the caller, so
	// this states both plainly. Leaving it blank would imply a reproducibility
	// this run does not have.
	Nondeterminism string `json:"nondeterminism_declaration"`

	At string `json:"at"`
}

// LLMNondeterminism is the standing declaration for a hosted-model investigator.
const LLMNondeterminism = "the investigator is a hosted large language model: " +
	"sampling is nondeterministic, the served weights are not addressable by the caller, " +
	"and no model artifact digest is obtainable. Re-running this configuration may " +
	"produce a different question, and identical output is not evidence of a deterministic path"

// DigestOf is the canonical content hash of a proposed question.
//
// Computed from the QUESTION's terms only, matching Identity, so a recipe
// reworded between runs digests the same. Provenance and prose are excluded:
// the digest identifies WHAT WAS ASKED, not who asked or how they phrased it.
func DigestOf(r Recipe) string {
	sum := sha256.Sum256([]byte(r.Identity()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// GraphDigest hashes the workspace authority a run observed.
func GraphDigest(fields map[string]string) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, fields[k])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AppendReceipt records one inference run, append-only.
//
// Never rewrites and never deduplicates: §6.3 requires that a model update not
// silently rewrite candidate history, and two runs of the same configuration
// producing the same question are two runs, not one.
func AppendReceipt(path string, r InferenceReceipt) error {
	if r.At == "" {
		r.At = time.Now().UTC().Format(time.RFC3339)
	}
	if r.Nondeterminism == "" {
		r.Nondeterminism = LLMNondeterminism
	}
	if r.FeatureVersion == "" {
		r.FeatureVersion = FeatureExtractorVersion
	}
	if r.PostProcessing == "" {
		r.PostProcessing = PostProcessingVersion
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// LoadReceipts reads the append-only log.
func LoadReceipts(path string) ([]InferenceReceipt, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []InferenceReceipt
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r InferenceReceipt
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
