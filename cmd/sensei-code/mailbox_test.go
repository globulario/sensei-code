package main

import (
	"os"
	"strings"
	"testing"
)

// The mailbox is a pull request conversation now, because the remote actor is
// woken by pull request activity. -github-review-issue predates that and stays
// as an alias so an existing deployment does not lose its mailbox on upgrade.
//
// An alias is not a second authority. These pin the one rule that keeps it from
// becoming one: the two flags may name the same mailbox or one of them may be
// silent, and disagreement is refused. A deployment that disagreed with itself
// would post requests into one conversation and wait for answers in another,
// which looks exactly like a remote party that never replied — the failure this
// whole change exists to stop being invisible.
func TestTheMailboxFlagsResolveToOneTarget(t *testing.T) {
	for name, tc := range map[string]struct {
		pr, legacy, want string
	}{
		"neither configured leaves the bridge off": {"", "", ""},
		"the pr flag alone":                        {"157", "", "157"},
		"the deprecated alias alone":               {"", "157", "157"},
		"both agreeing is not a conflict":          {"157", "157", "157"},
		"whitespace is not disagreement":           {" 157 ", "157", "157"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := effectiveMailbox(tc.pr, tc.legacy)
			if err != nil {
				t.Fatalf("refused a resolvable configuration: %v", err)
			}
			if got != tc.want {
				t.Fatalf("mailbox = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDisagreeingMailboxFlagsAreRefused(t *testing.T) {
	got, err := effectiveMailbox("157", "156")
	if err == nil {
		t.Fatal("two flags naming different mailboxes were accepted; one of them would " +
			"have silently won and half the protocol would have gone to the wrong conversation")
	}
	if got != "" {
		t.Errorf("a refused configuration still produced mailbox %q", got)
	}
	for _, want := range []string{"157", "156"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name #%s, so an operator cannot see which two disagree: %v", want, err)
		}
	}
}

// The alias buys compatibility, never leniency about what the number must be.
// Whichever flag supplied it, the resulting mailbox is still established as a
// pull request before the bridge is announced.
func TestTheDeprecatedAliasStillYieldsAVerifiedTarget(t *testing.T) {
	gh := configuredBridge()
	gh.MailboxPR = ""
	resolved, err := effectiveMailbox("", "157")
	if err != nil {
		t.Fatal(err)
	}
	gh.MailboxPR = resolved

	server := newControlServer(t)
	got, err := composeEngineResolver(server, testRepoRoot, "sess-alias", gh)
	if err != nil {
		t.Fatalf("alias-configured bridge refused: %v", err)
	}
	if got.Mailbox.Number != "157" {
		t.Fatalf("installed mailbox is #%s, not the aliased #157", got.Mailbox.Number)
	}
	if !strings.Contains(got.Banner, "PR #157") {
		t.Errorf("the operator's line does not say the mailbox is a PR conversation: %q", got.Banner)
	}
}

// The banner is read off the mailbox that was installed. An operator who reads
// "PR #157" must not be looking at a bridge holding something else.
func TestTheBannerNamesTheInstalledConversation(t *testing.T) {
	gh := configuredBridge()
	gh.MailboxPR = "412"

	server := newControlServer(t)
	got, err := composeEngineResolver(server, testRepoRoot, "sess-banner", gh)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mailbox.Number != "412" {
		t.Fatalf("installed mailbox #%s disagrees with configuration #412", got.Mailbox.Number)
	}
	if !strings.Contains(got.Banner, "PR #412") {
		t.Errorf("banner %q does not name the installed conversation", got.Banner)
	}
	if strings.Contains(got.Banner, "issue #") {
		t.Errorf("banner still promises issue semantics: %q", got.Banner)
	}
}

// The last wire of the mailbox repair, pinned the same way the resolver
// installation is: by source, because an assignment that is missing cannot be
// observed by calling the function that was never called.
//
// VerifyMailboxIsPullRequest is proved behaviourally in ghbridge. What this
// pins is that startup actually ASKS it, and asks it before the operator is told
// the bridge is up. Deleting the call leaves every unit test green, every
// configuration accepted, and every request posted into a conversation nothing
// wakes on — which is precisely the four-day failure this change exists to
// prevent, reappearing with no test to notice.
func TestTheControlCommandEstablishesTheMailboxIsAPullRequest(t *testing.T) {
	source, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatalf("read control.go: %v", err)
	}
	src := string(source)
	if !strings.Contains(src, "ghbridge.VerifyMailboxIsPullRequest(verifyCtx, runners.Mailbox)") {
		t.Error("control starts the github bridge without establishing that its mailbox is a pull request")
	}
	if !strings.Contains(src, "effectiveMailbox(*ghMailboxPR, *ghIssue)") {
		t.Error("control reads a mailbox flag without resolving the two flags to one target")
	}
	// Verification must gate the banner, not trail it: an operator who has
	// already read "bridge up" has been told something not yet established.
	verify := strings.Index(src, "ghbridge.VerifyMailboxIsPullRequest")
	banner := strings.Index(src, "fmt.Println(runners.Banner)")
	if verify < 0 || banner < 0 || verify > banner {
		t.Error("the mailbox is announced before it is established")
	}
}
