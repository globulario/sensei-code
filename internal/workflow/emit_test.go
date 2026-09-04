package workflow

import (
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// A run must not be pausable by an observer.
//
// Engine.emit is on every path in the workflow — it is how status, agent
// output, authority questions and terminal verdicts leave the engine. While
// Publish blocked on a full subscriber channel, any watcher that stopped
// reading stopped the run itself, holding the bus read lock while it did.
// Nothing crashed and nothing was reported; the run simply stopped, with the
// cause in another package.
func TestAStalledWatcherCannotStopTheWorkflow(t *testing.T) {
	bus := event.NewBus()
	stalled, cancel := bus.Subscribe(1)
	defer cancel()
	_ = stalled // deliberately never read

	store, err := session.New(t.TempDir(), "sess-1")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	e := &Engine{Bus: bus, Store: store, SessionID: "sess-1"}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			e.emit(event.New("sess-1", "task-1", event.SourceSystem, event.Status, "working", nil))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a watcher that stopped reading stopped the workflow")
	}

	// The other half of the bargain: the watcher missed events and the RUN did
	// not. The store is appended to before anything is published, so a skipped
	// delivery is a gap in what somebody saw and never a gap in the record.
	if bus.Dropped() == 0 {
		t.Fatal("this test proved nothing: the stalled subscriber never actually filled up")
	}
	recorded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recorded) != 500 {
		t.Fatalf("the record holds %d of 500 events; a dropped delivery lost part of the run", len(recorded))
	}
}
