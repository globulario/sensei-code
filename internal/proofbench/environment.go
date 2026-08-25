package proofbench

// Is the governed child process talking to the graph we think it is?
//
// A proof-v6 COLD wave halted after one arm because every subsequent governed
// run refused at the start gate: the MCP the product spawned was reaching a
// throwaway DEV graph in a scratchpad, while the CLI reached the authoritative
// one. The product was right to refuse -- that is the start gate working -- but
// the harness had no way to know before spending 20 minutes finding out.
//
// So the environment is now identified BEFORE a wave, and by AUTHORITY IDENTITY
// rather than by triple count. Counts are diagnostic: two graphs can share a
// count and differ in everything that matters, and a count that changed is a
// symptom rather than a cause. What decides is whether the graph vouches for
// itself -- authoritative, stamped provenance -- and whether its identity is the
// one the wave started with.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GraphIdentity is who the graph says it is.
//
// Read from the product's own workspace-status answer, through the same process
// path a governed arm uses, because the question is what the CHILD reaches and
// not what this process can reach.
type GraphIdentity struct {
	Authoritative bool   `json:"authoritative"`
	Provenance    string `json:"build_provenance_state"`
	Freshness     string `json:"graph_freshness_state"`
	SeedState     string `json:"seed_state"`
	// GraphCommit and SeedDigest are the stable identity. A wave pins them at
	// the start and halts if either moves.
	GraphCommit string `json:"certified_awareness_graph_commit"`
	SeedDigest  string `json:"embedded_seed_digest_sha256"`
	// Composition is the workspace contract's own verdict.
	Composition string `json:"composition_state"`
	// TripleCount is diagnostic only and decides nothing.
	TripleCount int `json:"live_store_graph_triple_count"`
}

// Usable reports an identity a wave may run against.
//
// Deliberately strict and deliberately not about size. A graph that cannot
// vouch for itself produces refusals, and a wave of refusals measures the
// environment rather than the product.
func (g GraphIdentity) Usable() bool {
	return g.Authoritative &&
		g.Provenance == "BUILD_PROVENANCE_STATE_STAMPED" &&
		g.Composition == "complete"
}

// Same reports whether two identities are the same graph.
//
// By commit and seed digest. A wave that started against one authority and
// continued against another has measured two different things and said so with
// one number.
func (g GraphIdentity) Same(other GraphIdentity) bool {
	return g.GraphCommit == other.GraphCommit && g.SeedDigest == other.SeedDigest
}

func (g GraphIdentity) String() string {
	return fmt.Sprintf("authoritative=%v provenance=%s composition=%s commit=%s seed=%s (triples=%d)",
		g.Authoritative, g.Provenance, g.Composition,
		short(g.GraphCommit), short(g.SeedDigest), g.TripleCount)
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "(none)"
	}
	return s
}

// ErrEnvironment is a measurement-integrity failure about the environment.
//
// Never a REFUSED, never an INCORRECT, and never a Sensei-code score: the
// product did not fail, the instrument was pointed at the wrong world.
type ErrEnvironment struct{ Why string }

func (e ErrEnvironment) Error() string {
	return "MEASUREMENT_INTEGRITY_FAILURE (environment): " + e.Why +
		" — this is not a Sensei-code result and must not be recorded as one"
}

// InspectEnvironment asks the product, through the path a governed arm uses,
// which graph its spawned MCP reaches.
//
// It runs one real `sensei-code run` with a trivial task and a short budget,
// and reads the workspace-status event the engine emits before any provider is
// invoked. No provider token is spent: the answer arrives from the start gate,
// which runs first.
func InspectEnvironment(ctx context.Context, binary, repoRoot string) (GraphIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "run",
		"--task", "environment identity probe", "--json", "--timeout", "30s")
	cmd.Dir = repoRoot
	cmd.Env = strippedEnv()
	out, _ := cmd.CombinedOutput()

	var found GraphIdentity
	var seen bool
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e SessionEvent
		if json.Unmarshal([]byte(line), &e) != nil || len(e.Payload) == 0 {
			continue
		}
		var payload struct {
			Composition string `json:"composition_state"`
			Authority   struct {
				Authoritative bool   `json:"authoritative"`
				Provenance    string `json:"build_provenance_state"`
				Freshness     string `json:"graph_freshness_state"`
				SeedState     string `json:"seed_state"`
				GraphCommit   string `json:"certified_awareness_graph_commit"`
				SeedDigest    string `json:"embedded_seed_digest_sha256"`
				Triples       int    `json:"live_store_graph_triple_count"`
			} `json:"graph_authority"`
		}
		if json.Unmarshal(e.Payload, &payload) != nil || payload.Authority.Provenance == "" {
			continue
		}
		found = GraphIdentity{
			Authoritative: payload.Authority.Authoritative,
			Provenance:    payload.Authority.Provenance,
			Freshness:     payload.Authority.Freshness,
			SeedState:     payload.Authority.SeedState,
			GraphCommit:   payload.Authority.GraphCommit,
			SeedDigest:    payload.Authority.SeedDigest,
			Composition:   payload.Composition,
			TripleCount:   payload.Authority.Triples,
		}
		seen = true
		break
	}
	if !seen {
		return GraphIdentity{}, ErrEnvironment{"the governed run reported no graph authority at all, " +
			"so the identity of the graph its MCP reaches could not be established"}
	}
	return found, nil
}

// RequireEnvironment refuses to start a wave against a graph that cannot vouch
// for itself.
func RequireEnvironment(g GraphIdentity) error {
	if g.Usable() {
		return nil
	}
	var missing []string
	if !g.Authoritative {
		missing = append(missing, "not authoritative")
	}
	if g.Provenance != "BUILD_PROVENANCE_STATE_STAMPED" {
		missing = append(missing, "provenance is "+g.Provenance+" rather than STAMPED")
	}
	if g.Composition != "complete" {
		missing = append(missing, "workspace composition is "+g.Composition)
	}
	return ErrEnvironment{fmt.Sprintf("the graph the governed child reaches is not one a wave may be "+
		"measured against: %s. Observed: %s", strings.Join(missing, "; "), g)}
}

// RequireStableEnvironment halts a wave whose authority moved underneath it.
func RequireStableEnvironment(pinned, now GraphIdentity) error {
	if err := RequireEnvironment(now); err != nil {
		return err
	}
	if !pinned.Same(now) {
		return ErrEnvironment{fmt.Sprintf("the authoritative graph changed during the wave: pinned %s, "+
			"now %s. Arms before and after that change measured different worlds",
			short(pinned.GraphCommit), short(now.GraphCommit))}
	}
	return nil
}
