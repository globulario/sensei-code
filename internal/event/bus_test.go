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
