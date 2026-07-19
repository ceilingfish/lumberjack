package daemon

import (
	"testing"
	"time"
)

// recv waits briefly for an event on ch, failing the test if none arrives.
func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestBroadcasterFanOut(t *testing.T) {
	b := NewBroadcaster()
	ch1, unsub1 := b.Subscribe()
	defer unsub1()
	ch2, unsub2 := b.Subscribe()
	defer unsub2()

	b.Publish(Event{Type: EventSyncStarted})

	ev1 := recv(t, ch1)
	ev2 := recv(t, ch2)
	if ev1.Type != EventSyncStarted || ev2.Type != EventSyncStarted {
		t.Errorf("both subscribers should see the event, got %+v, %+v", ev1, ev2)
	}
}

func TestBroadcasterUnsubscribeClosesChannel(t *testing.T) {
	b := NewBroadcaster()
	ch, unsub := b.Subscribe()
	unsub()

	if _, ok := <-ch; ok {
		t.Error("expected channel to be closed after unsubscribe")
	}
	// Publishing after unsubscribe must not panic (double-checks the map entry
	// was actually removed).
	b.Publish(Event{Type: EventSyncStarted})
}

func TestBroadcasterDropsSlowSubscriber(t *testing.T) {
	b := NewBroadcaster()
	slow, unsubSlow := b.Subscribe()
	defer unsubSlow()
	fast, unsubFast := b.Subscribe()
	defer unsubFast()

	// Fill the slow subscriber's buffer without draining it, draining the fast
	// one as we go. Publish must never block on the slow subscriber; the
	// subscriberBuffer+1'th publish overflows its buffer and drops it.
	publish := func() {
		done := make(chan struct{})
		go func() {
			b.Publish(Event{Type: EventSyncStarted})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Publish blocked on a slow subscriber instead of dropping it")
		}
	}
	for i := 0; i < subscriberBuffer+1; i++ {
		publish()
		recv(t, fast) // keep the fast subscriber's buffer from ever filling up
	}

	// The slow subscriber was disconnected: its channel is now closed once its
	// buffered events are drained.
	for range subscriberBuffer {
		if _, ok := <-slow; !ok {
			t.Fatal("slow subscriber's channel closed before its buffered events were delivered")
		}
	}
	if _, ok := <-slow; ok {
		t.Error("expected slow subscriber's channel to be closed after being dropped")
	}
}
