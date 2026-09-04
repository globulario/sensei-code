package principal

import (
	"errors"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/roles"
)

// clock is a hand-wound test clock. Expiry is the behaviour most worth testing
// and the least worth waiting for.
type clock struct{ at time.Time }

func (c *clock) now() time.Time          { return c.at }
func (c *clock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newTestRegistry(ttl time.Duration) (*Registry, *clock) {
	c := &clock{at: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	return NewRegistry(ttl, c.now), c
}

func register(t *testing.T, r *Registry, p ID, want ...roles.Role) Registration {
	t.Helper()
	reg, err := r.Register(Request{Principal: p, Workspace: "github.com/globulario/sensei-code", Roles: want})
	if err != nil {
		t.Fatalf("register %s: %v", p, err)
	}
	return reg
}

func TestTheArchitectRoleCanBeGrantedToARemotePrincipal(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect)

	lease, ok := reg.Lease(roles.Architect)
	if !ok {
		t.Fatalf("architect was not granted: %+v", reg.Refused)
	}
	if !lease.State.Holds() {
		t.Fatalf("a freshly granted lease does not hold: %s", lease.State)
	}
	if lease.Authority != authority.Architectural {
		t.Fatalf("architect lease carries %v, want architectural", lease.Authority)
	}
	if !lease.Grants(SubmitArchitecture) || !lease.Grants(InspectTask) {
		t.Fatalf("architect lease grants %v", lease.Capabilities)
	}
	if lease.Grants(SubmitReview) {
		t.Fatal("the architect role granted submit_review")
	}
}

func TestTheReviewerRoleCanBeGrantedToARemotePrincipal(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Reviewer)

	lease, ok := reg.Lease(roles.Reviewer)
	if !ok {
		t.Fatalf("reviewer was not granted: %+v", reg.Refused)
	}
	if lease.Authority != authority.Execution {
		t.Fatalf("reviewer lease carries %v, want execution", lease.Authority)
	}
	if !lease.Grants(SubmitReview) {
		t.Fatalf("reviewer lease grants %v", lease.Capabilities)
	}
}

func TestOnePrincipalMayHoldBothRolesAndGetsTwoSeparateSessions(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect, roles.Reviewer)

	if len(reg.Refused) != 0 {
		t.Fatalf("holding both roles was refused: %+v", reg.Refused)
	}
	arch, _ := reg.Lease(roles.Architect)
	rev, _ := reg.Lease(roles.Reviewer)
	if arch.ID == rev.ID {
		t.Fatal("both roles share one role session; they must be released, renewed and expired independently")
	}
}

func TestAnUnidentifiedPrincipalIsGrantedNothing(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	if _, err := r.Register(Request{Workspace: "w", Roles: []roles.Role{roles.Architect}}); err == nil {
		t.Fatal("a role was granted to a principal nobody can name")
	}
	if _, err := r.Register(Request{Principal: "agent-a", Roles: []roles.Role{roles.Architect}}); err == nil {
		t.Fatal("a role was granted over no workspace")
	}
}

func TestARemotePrincipalIsNeverGrantedARoleThatMutatesACandidate(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	for _, role := range roles.All {
		if Leasable(role) {
			continue
		}
		reg := register(t, r, "agent-a", role)
		if reg.Holds(role) {
			t.Fatalf("%s was granted to a remote principal", role)
		}
		if len(reg.Refused) != 1 || reg.Refused[0].Reason == "" {
			t.Fatalf("%s was refused without a reason: %+v", role, reg.Refused)
		}
	}
}

func TestNoLeaseEverCarriesHumanAuthority(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.All...)
	for _, lease := range reg.Granted {
		if lease.Authority == authority.Human {
			t.Fatalf("%s lease carries human authority", lease.Role)
		}
	}
}

func TestAnUnknownRoleIsRefusedRatherThanInvented(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Role("admin"))
	if len(reg.Granted) != 0 {
		t.Fatalf("an unknown role was granted: %+v", reg.Granted)
	}
	if len(reg.Refused) != 1 {
		t.Fatalf("an unknown role was dropped rather than refused: %+v", reg.Refused)
	}
}

func TestAReviewerLeaseCannotSubmitArchitecture(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Reviewer)
	lease, _ := reg.Lease(roles.Reviewer)

	if _, err := r.Authorize(lease.ID, SubmitArchitecture, "", ""); !errors.Is(err, ErrCapabilityNotGranted) {
		t.Fatalf("a reviewer session submitted architecture: %v", err)
	}
	if _, err := r.Authorize(lease.ID, SubmitReview, "", ""); err != nil {
		t.Fatalf("a reviewer session could not review: %v", err)
	}
}

func TestAnUnknownOperationIsRefusedByMembership(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect)
	lease, _ := reg.Lease(roles.Architect)

	if _, err := r.Authorize(lease.ID, Capability("run_shell"), "", ""); !errors.Is(err, ErrCapabilityNotGranted) {
		t.Fatalf("an operation this surface does not have was authorized: %v", err)
	}
}

func TestAReleasedRoleCannotIssueADecision(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect)
	lease, _ := reg.Lease(roles.Architect)

	released, err := r.Release(lease.ID)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released.State != Released {
		t.Fatalf("released lease is %s", released.State)
	}
	if _, err := r.Authorize(lease.ID, SubmitArchitecture, "", ""); !errors.Is(err, ErrLeaseReleased) {
		t.Fatalf("a released role session submitted architecture: %v", err)
	}
}

func TestAnExpiredRoleCannotIssueADecision(t *testing.T) {
	r, c := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect)
	lease, _ := reg.Lease(roles.Architect)

	c.advance(2 * time.Minute)
	if _, err := r.Authorize(lease.ID, SubmitArchitecture, "", ""); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("an expired role session submitted architecture: %v", err)
	}
}

func TestReleaseAndExpiryAreDifferentFactsInTheRecord(t *testing.T) {
	r, c := newTestRegistry(time.Minute)
	reg := register(t, r, "gone", roles.Architect)
	vanished, _ := reg.Lease(roles.Architect)

	other := register(t, r, "polite", roles.Reviewer)
	returned, _ := other.Lease(roles.Reviewer)
	if _, err := r.Release(returned.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	c.advance(2 * time.Minute)
	a, _ := r.Lookup(vanished.ID)
	b, _ := r.Lookup(returned.ID)
	if a.State != Expired {
		t.Fatalf("a client that stopped answering is recorded as %s", a.State)
	}
	if b.State != Released {
		t.Fatalf("a client that gave the role back is recorded as %s", b.State)
	}
	if a.State == b.State {
		t.Fatal("a vanished client and a finished one are indistinguishable in the record")
	}
}

func TestAVanishedArchitectDoesNotHoldTheRoleForever(t *testing.T) {
	r, c := newTestRegistry(time.Minute)
	register(t, r, "gone", roles.Architect)
	c.advance(2 * time.Minute)

	reg := register(t, r, "successor", roles.Architect)
	if !reg.Holds(roles.Architect) {
		t.Fatalf("the architect role stayed occupied by a client that is not there: %+v", reg.Refused)
	}
}

func TestASecondPrincipalIsRefusedARoleAnotherIsHolding(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	register(t, r, "agent-a", roles.Architect)

	reg := register(t, r, "agent-b", roles.Architect)
	if reg.Holds(roles.Architect) {
		t.Fatal("two principals hold the architect role over one workspace")
	}
	if len(reg.Refused) != 1 {
		t.Fatalf("the second architect was not told why: %+v", reg.Refused)
	}
}

func TestReconnectingRenewsTheSameRoleSessionAndKeepsItsTask(t *testing.T) {
	r, c := newTestRegistry(time.Minute)
	first := register(t, r, "agent-a", roles.Architect)
	lease, _ := first.Lease(roles.Architect)
	if _, err := r.Bind(lease.ID, "task-1"); err != nil {
		t.Fatalf("bind: %v", err)
	}

	c.advance(30 * time.Second)
	again := register(t, r, "agent-a", roles.Architect)
	back, ok := again.Lease(roles.Architect)
	if !ok {
		t.Fatalf("a reconnecting principal lost its role: %+v", again.Refused)
	}
	if back.ID != lease.ID {
		t.Fatal("reconnecting minted a second role session instead of resuming the one held")
	}
	if back.Task != "task-1" {
		t.Fatalf("reconnecting lost the task binding, got %q", back.Task)
	}
	if !back.ExpiresAt.After(lease.ExpiresAt) {
		t.Fatal("reconnecting did not extend the lease")
	}
}

func TestARoleSessionBoundToOneTaskCannotActOnAnother(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Reviewer)
	lease, _ := reg.Lease(roles.Reviewer)
	if _, err := r.Bind(lease.ID, "task-1"); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if _, err := r.Authorize(lease.ID, SubmitReview, "", "task-2"); !errors.Is(err, ErrBoundToAnotherTask) {
		t.Fatalf("a review bound to task-1 was authorized against task-2: %v", err)
	}
	if _, err := r.Authorize(lease.ID, SubmitReview, "", "task-1"); err != nil {
		t.Fatalf("the lease could not act on its own task: %v", err)
	}
	if _, err := r.Bind(lease.ID, "task-2"); !errors.Is(err, ErrBoundToAnotherTask) {
		t.Fatalf("a bound role session followed the client to another task: %v", err)
	}
}

func TestARoleSessionCannotActOverAnotherWorkspace(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect)
	lease, _ := reg.Lease(roles.Architect)

	if _, err := r.Authorize(lease.ID, SubmitArchitecture, "github.com/other/repo", ""); !errors.Is(err, ErrWorkspaceMismatch) {
		t.Fatalf("a lease acted over a workspace it is not held over: %v", err)
	}
}

func TestAnExpiredLeaseCannotBeRevivedByRenewing(t *testing.T) {
	r, c := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect)
	lease, _ := reg.Lease(roles.Architect)

	c.advance(2 * time.Minute)
	if _, err := r.Renew(lease.ID); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("an expired lease was revived: %v", err)
	}
}

func TestRenewingKeepsARoleThatWouldOtherwiseRunOut(t *testing.T) {
	r, c := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect)
	lease, _ := reg.Lease(roles.Architect)

	c.advance(45 * time.Second)
	if _, err := r.Renew(lease.ID); err != nil {
		t.Fatalf("renew: %v", err)
	}
	c.advance(45 * time.Second)
	if _, err := r.Authorize(lease.ID, SubmitArchitecture, "", ""); err != nil {
		t.Fatalf("a renewed lease expired anyway: %v", err)
	}
}

func TestAnUnknownRoleSessionAuthorizesNothing(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	if _, err := r.Authorize("rs-invented", InspectTask, "", ""); !errors.Is(err, ErrNoSuchLease) {
		t.Fatalf("an invented role session was authorized: %v", err)
	}
}

// A closed vocabulary is read by membership. Exclusion answers yes for every
// value added after the predicate was written, which is the direction that
// fails open.
func TestOnlyAnActiveLeaseHoldsItsRole(t *testing.T) {
	for _, state := range append(AllLeaseStates, LeaseState("suspended"), LeaseState("")) {
		if state.Holds() != (state == Active) {
			t.Fatalf("state %q reports holding as %v", state, state.Holds())
		}
	}
}

func TestTheCapabilityVocabularyIsClosed(t *testing.T) {
	for _, c := range AllCapabilities {
		if !c.Valid() {
			t.Fatalf("%s is in the closed set and reports invalid", c)
		}
	}
	for _, c := range []Capability{"", "run_shell", "write_file", "admit_change", "invoke_worker"} {
		if c.Valid() {
			t.Fatalf("%q is not an operation this surface has, and it validated", c)
		}
	}
}

func TestHolderOfAnswersPerTaskRatherThanPerWorkspace(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	reg := register(t, r, "agent-a", roles.Architect)
	lease, _ := reg.Lease(roles.Architect)
	if _, err := r.Bind(lease.ID, "task-1"); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if who, ok := r.HolderOf(roles.Architect, "task-1"); !ok || who != "agent-a" {
		t.Fatalf("HolderOf(task-1) = %q, %v", who, ok)
	}
	if who, ok := r.HolderOf(roles.Architect, "task-2"); ok {
		t.Fatalf("one task's architect was attributed to another task: %q", who)
	}
}
