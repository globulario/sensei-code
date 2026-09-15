package derived

// CLI.Revalidate builds the argv for each recipe family. A family it does not know falls
// to the default branch, which passes -lock and NO -search — the shape only
// field_access_under_lock wants.
//
// So committing a construction_confined_to_owner recipe is not enough: the revalidator
// would invoke `sensei derive -kind construction_confined_to_owner -dir … -type … -field …
// -lock ""` with no search path, the CLI would refuse it for missing --search, and the
// recipe would silently establish nothing. A recipe that cannot be invoked is worse than
// an absent one, because the corpus then claims a question is being asked.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// argvRecorder is a fake `sensei` that writes its own argv where the test can read it and
// prints a minimal receipt so Revalidate parses something.
func argvRecorder(t *testing.T) (bin, out string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the recorder is a POSIX shell script")
	}
	dir := t.TempDir()
	out = filepath.Join(dir, "argv")
	bin = filepath.Join(dir, "sensei")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + out + "\n" +
		`printf '{"result":"NOT_DERIVED","detail":"recorder","derivation_id":"x","derivation_version":"v1"}'` + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, out
}

func recordedArgs(t *testing.T, r Recipe) []string {
	t.Helper()
	bin, out := argvRecorder(t)
	CLI{Bin: bin}.Revalidate(context.Background(), "/repo", "rev1", r)
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the recorder captured no argv: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func TestAConstructionRecipeIsInvokedWithItsSearchScope(t *testing.T) {
	args := recordedArgs(t, Recipe{
		Kind: "construction_confined_to_owner", Dir: "internal/ghbridge",
		Type: "ExchangeRecord", Field: "Deadline",
		SearchPaths: []string{"internal/ghbridge", "cmd/sensei-code"},
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{"-kind construction_confined_to_owner", "-dir internal/ghbridge",
		"-type ExchangeRecord", "-field Deadline", "-search internal/ghbridge", "-search cmd/sensei-code"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv is missing %q: %s", want, joined)
		}
	}
	// -lock belongs to the lock family and would be a meaningless argument here.
	if strings.Contains(joined, "-lock") {
		t.Errorf("argv passes -lock to a construction recipe: %s", joined)
	}
}

// The other families keep the argv they had.
func TestTheOtherFamiliesKeepTheirArgv(t *testing.T) {
	lock := strings.Join(recordedArgs(t, Recipe{
		Kind: "field_access_under_lock", Dir: "internal/event", Type: "Bus", Field: "subs", Lock: "mu",
	}), " ")
	if !strings.Contains(lock, "-lock mu") {
		t.Errorf("the lock family lost -lock: %s", lock)
	}
	cmdFam := strings.Join(recordedArgs(t, Recipe{
		Kind: "command_invocation_confined_to", Command: "gh", Owner: "internal/ghbridge",
		SearchPaths: []string{"internal/ghbridge"},
	}), " ")
	if !strings.Contains(cmdFam, "-command gh") || !strings.Contains(cmdFam, "-owner internal/ghbridge") {
		t.Errorf("the command family's argv changed: %s", cmdFam)
	}
	mut := strings.Join(recordedArgs(t, Recipe{
		Kind: "state_mutation_confined_to_owner", Dir: "internal/ghbridge", Type: "ExchangeRecord",
		Field: "Deadline", SearchPaths: []string{"internal/ghbridge"},
	}), " ")
	if strings.Contains(mut, "-lock") || !strings.Contains(mut, "-search internal/ghbridge") {
		t.Errorf("the mutation family's argv changed: %s", mut)
	}
}
