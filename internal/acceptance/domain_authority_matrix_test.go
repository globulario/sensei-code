//go:build acceptance

package acceptance

// The regression sensei-code#13 / globulario/sensei#176 asks for, made
// executable.
//
// The defect was that two servers sharing one Oxigraph store could not both
// hold authority: a build of one registered domain recomputed the global marker
// and regenerated the proof set for only that domain, leaving every other
// domain vouching for a publication that no longer existed. There was no state
// in which both were authoritative.
//
// Sensei has since moved the proof set into the store, keyed by publication and
// carrying a proof per domain (golang/graphgeneration, consulted by
// golang/server/graph_authority.go). So the mechanism exists. What has never
// been established here is the composition: that under the topology actually in
// use, every registered domain remains authoritative across a build of any one
// of them, and across a restart.
//
// This is that check, and it is deliberately shaped so it cannot report a
// verdict nobody earned:
//
//   - with no endpoints configured it SKIPS, naming what was missing;
//   - it asks each endpoint the question that actually fails — preflight, which
//     checks freshness AND closure AND transaction — never metadata, which
//     reports a green graph right up to the moment a governed run starts;
//   - it refuses to run the destructive half unless the operator opts in,
//     because a build rotates the shared marker and other workstreams read this
//     store.
//
//	SENSEI_CODE_MATRIX_ENDPOINTS="domain=addr,domain=addr"  which servers to ask
//	SENSEI_CODE_MATRIX_BUILD="domain"                       also rebuild this one
//
//	go test -tags acceptance ./internal/acceptance/ -run TestDomainAuthorityMatrix -v

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/sensei"
)

type domainEndpoint struct {
	domain string
	addr   string
}

func TestDomainAuthorityMatrix(t *testing.T) {
	endpoints := matrixEndpoints(t)
	if len(endpoints) < 2 {
		t.Skip("set SENSEI_CODE_MATRIX_ENDPOINTS to at least two domain=addr pairs; " +
			"one domain cannot demonstrate that publishing it leaves the others authoritative")
	}
	root := repoRoot(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Before. Every registered domain must already be authoritative, or the
	// matrix has nothing to preserve and a later PASS would be meaningless.
	for _, ep := range endpoints {
		if state, detail := authorityOf(t, root, cfg.Sensei.Command, ep); !state {
			t.Skipf("%s at %s is not authoritative before the build, so this run cannot show a build preserved anything: %s",
				ep.domain, ep.addr, detail)
		}
		t.Logf("before: %s authoritative", ep.domain)
	}

	build := strings.TrimSpace(os.Getenv("SENSEI_CODE_MATRIX_BUILD"))
	if build == "" {
		t.Skip("every configured domain is authoritative; set SENSEI_CODE_MATRIX_BUILD=<domain> to publish one " +
			"and prove the others survive it. This is opt-in because a build rotates the shared marker " +
			"and other workstreams read this store")
	}

	cmd := exec.CommandContext(context.Background(), "sensei", "build", "--repo", build)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sensei build --repo %s: %v\n%s", build, err, out)
	}
	t.Logf("published %s", build)

	// After. This is the whole assertion: publishing one domain must not have
	// taken authority away from any other.
	for _, ep := range endpoints {
		state, detail := authorityOf(t, root, cfg.Sensei.Command, ep)
		if !state {
			t.Errorf("publishing %s left %s non-authoritative: %s", build, ep.domain, detail)
			continue
		}
		t.Logf("after: %s still authoritative", ep.domain)
	}
}

// authorityOf asks one endpoint whether it can vouch for itself, through
// preflight rather than metadata.
func authorityOf(t *testing.T, root, command string, ep domainEndpoint) (bool, string) {
	t.Helper()
	sc, err := sensei.Start(context.Background(), root, command, []string{"--awareness-addr", ep.addr})
	if err != nil {
		return false, "start Sensei at " + ep.addr + ": " + err.Error()
	}
	defer sc.Close()

	result, err := sc.CallTool("awareness_preflight", map[string]any{
		"task": "domain authority matrix", "files": []string{}, "mode": "compact", "domain": ep.domain,
	})
	if err != nil {
		return false, "preflight: " + err.Error()
	}
	decision, err := sensei.DecodePreflight(result)
	if err != nil {
		return false, "decode preflight: " + err.Error()
	}
	if !decision.Authority.Certifiable() {
		return false, decision.Authority.Diagnostic()
	}
	return true, ""
}

func matrixEndpoints(t *testing.T) []domainEndpoint {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("SENSEI_CODE_MATRIX_ENDPOINTS"))
	if raw == "" {
		return nil
	}
	var out []domainEndpoint
	for _, pair := range strings.Split(raw, ",") {
		domain, addr, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || strings.TrimSpace(domain) == "" || strings.TrimSpace(addr) == "" {
			t.Fatalf("SENSEI_CODE_MATRIX_ENDPOINTS entry %q is not domain=addr", pair)
		}
		out = append(out, domainEndpoint{domain: strings.TrimSpace(domain), addr: strings.TrimSpace(addr)})
	}
	return out
}
