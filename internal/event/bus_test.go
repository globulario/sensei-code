package event

import "testing"

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
