//go:build acceptance

package acceptance

// The authority-boundary verification for sensei-code#10.
//
// A proposal carries a domain, and the domain shapes its content and its id.
// Neither of those decides where the candidate is written: the server's
// -awareness-dir does, alone. When sensei-code talked to a server whose write
// root was the services corpus, every governed run filed this repository's
// governance into another repository's review queue, and looked correct doing
// it — the request, the stored content and the generated id all said
// sensei-code, and only the filesystem disagreed.
//
// So this asks the one question that matters and refuses to infer it from
// anything else: does a proposal scoped to this repository land in this
// repository's corpus, and does the other corpus stay untouched.
//
//	go test -tags acceptance ./internal/acceptance/ -run TestAuthorityRoot -v

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/sensei"
)

// servicesAwareness is the other repository sharing this Oxigraph store. It is
// named explicitly rather than discovered, because the whole point is to prove
// a specific corpus was not written to.
const servicesAwareness = "/home/dave/Documents/github.com/globulario/services/docs/awareness"

func TestAuthorityRootOwnsItsOwnProposals(t *testing.T) {
	root := repoRoot(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	theirProposals := filepath.Join(servicesAwareness, "candidates", "proposals")

	before := digestTree(t, theirProposals)
	if before == "" {
		t.Skip("services corpus not present; nothing to prove about isolation")
	}
	t.Logf("services proposals before: %s", before)

	// Talk to Sensei exactly the way the workflow does, through the configured
	// endpoint. If the deployment is wrong, this test is wrong in the same way
	// the product is, which is the point.
	t.Logf("sensei endpoint: %s %v", cfg.Sensei.Command, cfg.Sensei.Args)
	sc, err := sensei.Start(context.Background(), root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		t.Fatalf("start Sensei: %v", err)
	}
	defer sc.Close()

	id := fmt.Sprintf("authority root verification %d", time.Now().UnixNano())
	result, err := sc.CallTool("awareness_propose", map[string]any{
		"kind":              "contract_unknown",
		"title":             "Authority-root verification for sensei-code#10",
		"domain":            "github.com/globulario/sensei-code",
		"repo":              "github.com/globulario/sensei-code",
		"proposed_contract": "A proposal scoped to this repository is written into this repository's corpus.",
		"description":       id,
		"evidence":          []string{"sensei-code#10 disposition check"},
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}

	var payload struct {
		Accepted      bool   `json:"accepted"`
		CandidatePath string `json:"candidate_path"`
		Status        string `json:"status"`
	}
	raw, _ := json.Marshal(result.Structured)
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode propose result: %v (%s)", err, raw)
	}
	if !payload.Accepted {
		t.Fatalf("proposal was not accepted: %s", raw)
	}
	if payload.CandidatePath == "" {
		t.Fatal("no candidate path returned, so custody cannot be checked")
	}
	t.Logf("accepted; candidate_path (relative): %s", payload.CandidatePath)

	// The returned path is relative. Resolving it against our own awareness
	// directory is the assertion: it must exist here.
	ours := filepath.Join(root, "docs", "awareness", filepath.FromSlash(payload.CandidatePath))
	defer os.Remove(ours)

	body, err := os.ReadFile(ours)
	if err != nil {
		t.Fatalf("the candidate did not land in this repository's corpus (%s): %v", ours, err)
	}
	t.Logf("resolved under this repository: %s", ours)
	if !strings.Contains(string(body), "github.com/globulario/sensei-code") {
		t.Errorf("the candidate does not declare the sensei-code domain:\n%s", body)
	}
	if !strings.Contains(string(body), id) {
		t.Error("the file found is not the proposal this test submitted")
	}

	// It must not also exist in the other corpus, and that corpus must be
	// byte-for-byte what it was.
	if _, err := os.Stat(filepath.Join(servicesAwareness, filepath.FromSlash(payload.CandidatePath))); err == nil {
		t.Error("the candidate ALSO landed in the services corpus")
	}
	after := digestTree(t, theirProposals)
	if after != before {
		t.Fatalf("the services proposals directory changed: %s -> %s", before, after)
	}
	t.Logf("services proposals unchanged: %s", after)

	// A proposal that only exists until the server restarts is not durable
	// custody. Re-reading through a fresh client proves the file is real rather
	// than an artifact of this connection.
	fresh, err := sensei.Start(context.Background(), root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		t.Fatalf("restart Sensei client: %v", err)
	}
	defer fresh.Close()
	meta, err := fresh.CallTool("awareness_metadata", map[string]any{"domain": "github.com/globulario/sensei-code"})
	if err != nil {
		t.Fatalf("metadata after restart: %v", err)
	}
	var authority struct {
		Authority struct {
			Authoritative bool   `json:"authoritative"`
			Verdict       string `json:"verdict"`
		} `json:"authority"`
		GraphFreshnessState string `json:"graph_freshness_state"`
		SeedState           string `json:"seed_state"`
	}
	rawMeta, _ := json.Marshal(meta.Structured)
	if err := json.Unmarshal(rawMeta, &authority); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	// Writing to the right place while going non-authoritative would be a
	// hollow success: the corpus would be correct and unusable.
	if !authority.Authority.Authoritative {
		t.Errorf("this server writes correctly but is not authoritative: verdict=%q freshness=%q seed=%q",
			authority.Authority.Verdict, authority.GraphFreshnessState, authority.SeedState)
	}
	t.Logf("authority after reconnect: authoritative=%v verdict=%s freshness=%s seed=%s",
		authority.Authority.Authoritative, authority.Authority.Verdict, authority.GraphFreshnessState, authority.SeedState)

	if _, err := os.Stat(ours); err != nil {
		t.Errorf("the proposal is no longer discoverable after reconnecting: %v", err)
	}
}

// digestTree hashes every file under dir, so "unchanged" means byte-for-byte
// rather than "same number of files".
func digestTree(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		body, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		fmt.Fprintf(h, "%s:%x\n", n, sha256.Sum256(body))
	}
	return hex.EncodeToString(h.Sum(nil))[:16] + fmt.Sprintf(" (%d files)", len(names))
}
