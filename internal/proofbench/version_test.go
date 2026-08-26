package proofbench

import (
	"path/filepath"
	"strings"
	"testing"
)

// The move this prevents: repair the harness after arm 1, continue with arms
// 2..11, and report a number assembled from two different rulers.
func TestACampaignCannotContinueUnderARepairedHarness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.lock.json")
	locked := CampaignLock{HarnessVersion: "v1", ClassifierVersion: "v1-unanchored",
		ManifestHash: "sha256:aaa", ArmsScheduled: 11}
	if err := WriteLock(path, locked); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// The same instrument continuing the same campaign is fine.
	if err := WriteLock(path, locked); err != nil {
		t.Fatalf("arms 2..N of the same campaign were refused: %v", err)
	}
	// A repaired harness is not.
	repaired := locked
	repaired.HarnessVersion = "v2"
	repaired.ClassifierVersion = "v2-anchored"
	err := WriteLock(path, repaired)
	if err == nil {
		t.Fatal("a repaired harness was allowed to continue a campaign it did not start")
	}
	if !strings.Contains(err.Error(), "voided") {
		t.Fatalf("the refusal must say what to do instead: %v", err)
	}
}

func TestAChangedCorpusIsAlsoAChangedInstrument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harness.lock.json")
	base := CurrentLock("sha256:aaa", 11, "")
	if err := WriteLock(path, base); err != nil {
		t.Fatal(err)
	}
	moved := CurrentLock("sha256:bbb", 11, "")
	if err := WriteLock(path, moved); err == nil {
		t.Fatal("the corpus changed underneath a running campaign and nothing objected")
	}
}

// The retrospective half: a report must not average across instruments.
func TestAReportRefusesToCombineInstrumentVersions(t *testing.T) {
	mixed := []Attempt{
		{Task: "a", HarnessVersion: "v2", ClassifierVersion: "v2-anchored"},
		{Task: "b"}, // recorded under v1, before versions existed
	}
	err := CheckInstrumentUniformity(mixed)
	if err == nil {
		t.Fatal("attempts from two instruments were combined into one result")
	}
	if !strings.Contains(err.Error(), "unrecorded") {
		t.Fatalf("an unversioned attempt must be named as the v1 reading it is: %v", err)
	}

	uniform := []Attempt{
		{Task: "a", HarnessVersion: "v2", ClassifierVersion: "v2-anchored"},
		{Task: "b", HarnessVersion: "v2", ClassifierVersion: "v2-anchored"},
	}
	if err := CheckInstrumentUniformity(uniform); err != nil {
		t.Fatalf("one instrument, one result: %v", err)
	}
}

// A voided attempt is evidence about the instrument, not about the product, and
// must not block a clean re-run.
func TestAVoidedAttemptDoesNotPoisonUniformity(t *testing.T) {
	attempts := []Attempt{
		{Task: "a", MeasurementStatus: "VOID_MEASUREMENT"}, // arm 1, harness v1
		{Task: "a", HarnessVersion: "v2", ClassifierVersion: "v2-anchored"},
	}
	if err := CheckInstrumentUniformity(attempts); err != nil {
		t.Fatalf("a voided v1 attempt blocked a clean v2 campaign: %v", err)
	}
}
