// SPDX-License-Identifier: AGPL-3.0-only

package admission

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type scriptedRun struct {
	code   int
	output string
	err    error
}

type scriptedRunner struct {
	runs  map[Step]scriptedRun
	calls []Invocation
}

func (r *scriptedRunner) Run(_ context.Context, invocation Invocation) (string, int, error) {
	r.calls = append(r.calls, invocation)
	if run, ok := r.runs[invocation.Step]; ok {
		return run.output, run.code, run.err
	}
	return "ok", 0, nil
}

func validRequest() Request {
	return Request{
		LineagePath: "/run/candidate.lineage.json",
		BundleDir:   "/run/bundle",
		RequestPath: "/run/admission.request.json",
		DecisionPath:"/run/admission.decision.json",
		GraphNT:     "/run/graph.nt",
		Repo:        "/repo/source",
		Target:      "/repo/admitted-worktree",
		PolicyID:    "strict",
	}
}

func TestExecuteRunsCanonicalChainInOrder(t *testing.T) {
	runner := &scriptedRunner{}

	got, err := Execute(context.Background(), validRequest(), runner)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !got.Complete {
		t.Fatalf("Complete = false, outcomes = %#v", got.Outcomes)
	}
	if got.StoppedAt != "" {
		t.Fatalf("StoppedAt = %q, want empty", got.StoppedAt)
	}
	if !Verified(got.Outcomes) {
		t.Fatalf("Verified = false, outcomes = %#v", got.Outcomes)
	}

	wantSteps := []Step{Compose, Decide, Apply, Verify}
	gotSteps := make([]Step, 0, len(runner.calls))
	for _, call := range runner.calls {
		gotSteps = append(gotSteps, call.Step)
	}
	if !reflect.DeepEqual(gotSteps, wantSteps) {
		t.Fatalf("steps = %#v, want %#v", gotSteps, wantSteps)
	}

	wantInvocations, err := validRequest().Chain()
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if !reflect.DeepEqual(runner.calls, wantInvocations) {
		t.Fatalf("executed invocations differ from canonical Chain\n got: %#v\nwant: %#v", runner.calls, wantInvocations)
	}
}

func TestExecuteStopsWhenCompositionRefusesCandidate(t *testing.T) {
	runner := &scriptedRunner{runs: map[Step]scriptedRun{
		Compose: {code: 3, output: "refused"},
	}}

	got, err := Execute(context.Background(), validRequest(), runner)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Complete {
		t.Fatal("Complete = true after refusal")
	}
	if got.StoppedAt != Compose {
		t.Fatalf("StoppedAt = %q, want %q", got.StoppedAt, Compose)
	}
	if len(runner.calls) != 1 || runner.calls[0].Step != Compose {
		t.Fatalf("calls = %#v, want only compose", runner.calls)
	}
	if len(got.Outcomes) != 1 || !got.Outcomes[0].Refused {
		t.Fatalf("outcomes = %#v, want canonical compose refusal", got.Outcomes)
	}
}

func TestExecuteDoesNotApplyAfterDecisionFailure(t *testing.T) {
	runner := &scriptedRunner{runs: map[Step]scriptedRun{
		Decide: {code: 4, output: "not admitted"},
	}}

	got, err := Execute(context.Background(), validRequest(), runner)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Complete || got.StoppedAt != Decide {
		t.Fatalf("result = %#v, want stop at decision", got)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("ran %d steps, want compose+decide only", len(runner.calls))
	}
	if Admitted(got.Outcomes) {
		t.Fatalf("Admitted = true after decision exit 4: %#v", got.Outcomes)
	}
}

func TestExecuteFailsClosedOnUnknownFutureExitCode(t *testing.T) {
	runner := &scriptedRunner{runs: map[Step]scriptedRun{
		Apply: {code: 42, output: "new outcome this build does not know"},
	}}

	got, err := Execute(context.Background(), validRequest(), runner)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Complete || got.StoppedAt != Apply {
		t.Fatalf("result = %#v, want fail-closed stop at apply", got)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("ran %d steps, want compose+decide+apply only", len(runner.calls))
	}
	if Verified(got.Outcomes) {
		t.Fatalf("Verified = true after unknown non-zero exit: %#v", got.Outcomes)
	}
}

func TestExecuteReturnsInfrastructureFailureWithoutInventingOutcome(t *testing.T) {
	boom := errors.New("executable unavailable")
	runner := &scriptedRunner{runs: map[Step]scriptedRun{
		Compose: {err: boom},
	}}

	got, err := Execute(context.Background(), validRequest(), runner)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if len(got.Outcomes) != 0 {
		t.Fatalf("outcomes = %#v, infrastructure failure is not a Sensei verdict", got.Outcomes)
	}
	if got.Complete {
		t.Fatal("Complete = true after infrastructure failure")
	}
}

func TestExecuteRejectsNilRunner(t *testing.T) {
	if _, err := Execute(context.Background(), validRequest(), nil); err == nil {
		t.Fatal("Execute(nil runner) returned nil error")
	}
}
