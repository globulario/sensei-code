package event

import (
	"testing"
	"time"
)

func TestBusPublish(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe(1)
	defer cancel()
	want := New("s", "t", SourceSensei, Status, "ok", nil)
	b.Publish(want)
	got := <-ch
	if got.Summary != want.Summary || got.Source != want.Source {
		t.Fatalf("got %#v", got)
	}
}

// What the read lock is actually for.
//
// Publish sends while holding RLock; cancel closes the subscriber's channel
// while holding Lock. Those are mutually exclusive, so a channel can never be
// closed while a send to it is in flight — and that, not the map, is what the
// mutex is protecting. Go panics on a send to a closed channel, and the lock
// discipline is the only thing standing between this bus and that panic.
//
// The refactor this guards against is the natural one: "don't block while
// holding a lock", i.e. copy the channels under RLock, release it, then send.
// That version is correct about the map and wrong about the lifetime, because
// cancel can complete in the window between the release and the send.
//
// This test cannot observe the absence of a race directly, so it pins the two
// properties the argument rests on instead: a cancelled subscriber's channel is
// closed, and Publish after cancel does not send to it.
func TestCancelClosesTheChannelAndPublishSkipsIt(t *testing.T) {
	b := NewBus()
	ch, cancel := b.Subscribe(1)
	cancel()

	if _, open := <-ch; open {
		t.Fatal("cancel did not close the subscriber channel")
	}
	// Publish must not panic: cancel removed the subscriber from the map under
	// the same lock Publish reads it under, so there is nothing left to send to.
	b.Publish(New("s", "t", SourceSensei, Status, "after cancel", nil))

	// A second cancel is a no-op rather than a double close.
	cancel()
}

// The property the non-blocking send exists for.
//
// A subscriber that stops reading must not stop the publisher. Before this,
// Publish blocked on a full channel while holding the read lock, so any wedged
// observer stopped the workflow mid-run — silently, and with the cause
// somewhere else entirely.
func TestAStalledSubscriberCannotBlockPublish(t *testing.T) {
	b := NewBus()
	stalled, cancel := b.Subscribe(1)
	defer cancel()
	_ = stalled // deliberately never read

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish(New("s", "t", SourceSensei, Status, "tick", nil))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a subscriber that stopped reading stopped the publisher")
	}
}

// One wedged observer must not starve the others.
func TestAHealthySubscriberKeepsReceivingWhileAnotherIsStalled(t *testing.T) {
	b := NewBus()
	_, cancelStalled := b.Subscribe(1)
	defer cancelStalled()
	healthy, cancelHealthy := b.Subscribe(8)
	defer cancelHealthy()

	for i := 0; i < 4; i++ {
		b.Publish(New("s", "t", SourceSensei, Status, "tick", nil))
	}
	for i := 0; i < 4; i++ {
		select {
		case got := <-healthy:
			if got.Summary != "tick" {
				t.Fatalf("healthy subscriber got %q", got.Summary)
			}
		default:
			t.Fatalf("the healthy subscriber received only %d of 4 events", i)
		}
	}
}

// A skipped delivery is a gap in what a watcher saw. Counting it is what lets
// "the transcript looks complete" and "the transcript is complete" be told
// apart, so the drop must not be silent.
func TestASkippedDeliveryIsCountedRatherThanSilent(t *testing.T) {
	b := NewBus()
	if b.Dropped() != 0 {
		t.Fatalf("a fresh bus has already dropped %d", b.Dropped())
	}
	_, cancel := b.Subscribe(1)
	defer cancel()

	for i := 0; i < 5; i++ {
		b.Publish(New("s", "t", SourceSensei, Status, "tick", nil))
	}
	// One fits in the buffer; the rest have nowhere to go.
	if got := b.Dropped(); got != 4 {
		t.Fatalf("dropped %d of 5 deliveries into a buffer of 1, want 4", got)
	}
}

// Delivering to nobody is not a drop. A run with no watcher attached has not
// lost anything.
func TestPublishingToNobodyDropsNothing(t *testing.T) {
	b := NewBus()
	b.Publish(New("s", "t", SourceSensei, Status, "tick", nil))
	if got := b.Dropped(); got != 0 {
		t.Fatalf("publishing with no subscribers counted %d drops", got)
	}
}
