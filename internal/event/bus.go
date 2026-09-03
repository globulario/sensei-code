package event

import (
	"sync"
	"sync/atomic"
)

// Bus fans an event out to whoever is watching.
//
// It is delivery, not record. The durable account of a run is the session
// store, which Engine.emit appends to BEFORE publishing here — so a subscriber
// that misses an event has an incomplete view, and the run still has a complete
// record. That ordering is what makes the policy below safe.
type Bus struct {
	mu   sync.RWMutex
	next uint64
	subs map[uint64]chan Event
	// dropped counts events a subscriber had no room for. It is atomic because
	// Publish holds only a read lock, so several publishes run concurrently.
	dropped atomic.Uint64
}

func NewBus() *Bus { return &Bus{subs: make(map[uint64]chan Event)} }

func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer < 1 {
		buffer = 1
	}
	b.mu.Lock()
	id := b.next
	b.next++
	ch := make(chan Event, buffer)
	b.subs[id] = ch
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// Publish delivers to every subscriber that has room, and never waits for one
// that does not.
//
// The send used to block. That made every subscriber a potential brake on the
// workflow: a consumer that stopped reading — a UI wedged on a render, a
// watcher whose own work stalled, anything reached over a network — would stop
// the engine mid-run, holding the read lock while it did. The engine would not
// crash and would not report anything; it would simply stop, and the cause
// would be somewhere else entirely.
//
// A run must not be pausable by an observer. So a subscriber with no room is
// skipped and the miss is counted, and the counting is the honest half: an
// event that was not delivered is a gap in what a watcher saw, and Dropped is
// how a watcher can find out rather than quietly presenting a partial view as
// the whole story. The run's own record is unaffected — see the type comment.
//
// The send stays INSIDE the read lock, and that is not an oversight to be
// tidied up later. cancel closes a subscriber's channel while holding the write
// lock, so holding the read lock across the send is the only thing preventing a
// send on a closed channel. The natural refactor — copy the channels, release
// the lock, then send — is correct about the map and wrong about the lifetime.
// TestCancelClosesTheChannelAndPublishSkipsIt pins that argument.
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			b.dropped.Add(1)
		}
	}
}

// Dropped is how many deliveries were skipped because a subscriber had no room.
//
// It exists so that "the transcript looks complete" and "the transcript is
// complete" can be told apart. A non-zero count means some watcher saw less
// than happened; it never means the run recorded less than happened.
func (b *Bus) Dropped() uint64 { return b.dropped.Load() }
