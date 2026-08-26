package proofbench

// Which instrument produced a reading.
//
// Added after REPAIR_VERIFICATION was halted one arm in. A defect was found in
// the infrastructure classifier, and the obvious move -- fix it and continue
// with arms 2..11 -- would have produced a wave whose first arm was measured by
// one instrument and the rest by another. A number assembled from two rulers
// describes neither.
//
// So the instrument is versioned, every attempt records the version that
// produced it, and a report refuses to combine versions. Repairing the harness
// now costs a full re-run by construction, which is the correct price.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	// HarnessVersion is the execution and evidence-capture regime.
	//
	// v1: transcripts kept as a 20KB tail; classifier could override any
	//     terminal. Every proof-v1..v6 reading was taken under v1.
	// v2: complete transcripts, hash-recorded, with the span that caused a
	//     classification preserved beside it; structured terminals authoritative.
	HarnessVersion = "v2"

	// ClassifierVersion is the terminal/infrastructure decision rule
	// specifically, versioned separately because it can change without the
	// capture regime changing.
	//
	// v1-unanchored: free-text signals scanned across the whole stream and
	//     allowed to override any non-zero exit. Recorded a productive 22-minute
	//     timeout as INFRA_FAILURE -- an error in the product's favour.
	// v2-anchored:   a specific structured terminal wins outright; text may
	//     only fill in a cause the engine left generic.
	ClassifierVersion = "v2-anchored"
)

// CampaignLock freezes the instrument a campaign runs under.
//
// Written before the first arm and checked before every later one. Its purpose
// is to make a mid-campaign repair impossible to apply silently: change the
// harness and the lock stops matching, so the campaign must be voided and
// restarted rather than continued under a changed ruler.
type CampaignLock struct {
	HarnessVersion    string `json:"harness_version"`
	ClassifierVersion string `json:"classifier_version"`
	ManifestHash      string `json:"manifest_hash"`
	// ArmsScheduled is the denominator the campaign committed to. Recorded here
	// so end-to-end success cannot later be computed over whatever happened to
	// run.
	ArmsScheduled int `json:"arms_scheduled"`
	// Note carries the operator's reason for the campaign.
	Note string `json:"note,omitempty"`
}

// CurrentLock is the lock this build would write.
func CurrentLock(manifestHash string, arms int, note string) CampaignLock {
	return CampaignLock{HarnessVersion: HarnessVersion, ClassifierVersion: ClassifierVersion,
		ManifestHash: manifestHash, ArmsScheduled: arms, Note: note}
}

// ErrInstrumentChanged is a refusal to measure with a ruler that moved.
type ErrInstrumentChanged struct{ Why string }

func (e ErrInstrumentChanged) Error() string {
	return "MEASUREMENT_INTEGRITY_FAILURE (instrument): " + e.Why +
		" — the campaign must be voided and re-run from arm 1 under one frozen instrument"
}

// WriteLock creates the campaign lock, refusing to overwrite an existing one
// that disagrees.
//
// An existing lock that MATCHES is fine: that is the second and later arms of
// the same campaign. One that differs is the case this exists to catch.
func WriteLock(path string, want CampaignLock) error {
	if b, err := os.ReadFile(path); err == nil {
		var have CampaignLock
		if json.Unmarshal(b, &have) != nil {
			return ErrInstrumentChanged{"the campaign lock at " + path + " is unreadable"}
		}
		return have.Verify(want)
	}
	b, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Verify reports whether a campaign may continue under the running build.
func (have CampaignLock) Verify(want CampaignLock) error {
	var diff []string
	if have.HarnessVersion != want.HarnessVersion {
		diff = append(diff, fmt.Sprintf("harness %s -> %s", have.HarnessVersion, want.HarnessVersion))
	}
	if have.ClassifierVersion != want.ClassifierVersion {
		diff = append(diff, fmt.Sprintf("classifier %s -> %s", have.ClassifierVersion, want.ClassifierVersion))
	}
	if have.ManifestHash != want.ManifestHash {
		diff = append(diff, fmt.Sprintf("manifest %s -> %s", short(have.ManifestHash), short(want.ManifestHash)))
	}
	if len(diff) == 0 {
		return nil
	}
	return ErrInstrumentChanged{fmt.Sprintf("this campaign was locked to a different instrument (%s)",
		strings.Join(diff, "; "))}
}

// CheckInstrumentUniformity refuses a result assembled from several
// instruments.
//
// The retrospective half of the lock: the lock stops a campaign continuing
// under a changed build, and this stops a REPORT quietly averaging attempts
// that were taken under different ones.
func CheckInstrumentUniformity(attempts []Attempt) error {
	seen := map[string]int{}
	for _, a := range attempts {
		if a.MeasurementStatus != "" {
			continue // already excluded from every rate
		}
		key := a.HarnessVersion + "/" + a.ClassifierVersion
		if strings.TrimSpace(key) == "/" {
			key = "v1/v1-unanchored (unrecorded)"
		}
		seen[key]++
	}
	if len(seen) <= 1 {
		return nil
	}
	var parts []string
	for k, n := range seen {
		parts = append(parts, fmt.Sprintf("%s: %d attempt(s)", k, n))
	}
	sort.Strings(parts)
	return ErrInstrumentChanged{fmt.Sprintf("attempts span %d instrument versions (%s)",
		len(seen), strings.Join(parts, "; "))}
}
