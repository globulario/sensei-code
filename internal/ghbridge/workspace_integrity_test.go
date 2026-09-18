package ghbridge

// THE GOVERNED CHAIN IS WRITTEN ONE WAY (sensei-code#184).
//
// Read as production syntax, because "we made them consistent" is a claim about
// every writer, and the two that were inconsistent are the two nobody had
// looked at: the obligation owner and the task state.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// governedRecordWriters are the local stores whose mutation can materially
// affect governance.
var governedRecordWriters = []string{
	"../reviewstore/store.go",
	"../ghbridge/attestation.go",
	"../ghbridge/exchange.go",
	"../taskstate/state.go",
}

var rawModeWrite = regexp.MustCompile(`(os\.WriteFile|os\.OpenFile|os\.MkdirAll)\([^)]*0o[0-7]{3}`)

func TestNoGovernedStoreInventsItsOwnWritePosture(t *testing.T) {
	checked := 0
	for _, rel := range governedRecordWriters {
		blob, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		checked++
		src := string(blob)
		if m := rawModeWrite.FindString(src); m != "" {
			t.Errorf("%s writes with its own mode (%s); governedfile owns that decision", rel, m)
		}
		if strings.Contains(src, `".tmp"`) {
			t.Errorf("%s keeps its own temp-file replace; governedfile owns that mechanism", rel)
		}
		if !strings.Contains(src, "governedfile.") {
			t.Errorf("%s does not write through governedfile", rel)
		}
	}
	if checked != len(governedRecordWriters) {
		t.Fatalf("inspected %d of %d governed writers", checked, len(governedRecordWriters))
	}
}

// The obligation owner's records survive being written, and are replaced
// atomically rather than truncated in place.
func TestAnObligationIsReplacedAtomically(t *testing.T) {
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}
	rec := ExchangeRecord{
		TaskID: "T", RequestID: "r-0123456789abcdef", Kind: ExchangeReview,
		Conversation: "157", BaseSHA: "91b475a172bba0257fd2ffd8a55d3edce582e883",
		CandidateDigest: "sha256:beef", CandidateTree: "1b713c41d4d6ed058313ce940b0cc481e4b22b18",
		ReviewCommit: "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
	}
	if err := log.Open(rec); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(log.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("an obligation write left %s behind", e.Name())
		}
	}
	owed, err := log.PendingReviews()
	if err != nil || len(owed) != 1 {
		t.Fatalf("the obligation did not survive its own write: %d %v", len(owed), err)
	}
	fi, err := os.Stat(filepath.Join(log.Dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		t.Fatalf("an obligation is group/other writable (%v)", fi.Mode().Perm())
	}
}
