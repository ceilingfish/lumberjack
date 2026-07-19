package daemon

import (
	"sync"

	"github.com/ceilingfish/lumberjack/internal/database/schema"
)

// EventType is the kind of change an Event carries — the domain mirror of
// lumberjackv1.WatchResponseType (mapping.go converts at the gRPC boundary).
type EventType int

// The EventType values a Broadcaster can publish.
const (
	EventUnspecified EventType = iota
	// EventWorktreeChanged is a worktree created, adopted, updated, or
	// deleted; Event.Change carries which and the worktree's state.
	EventWorktreeChanged
	// EventSyncStarted is a repository sync beginning.
	EventSyncStarted
	// EventSyncFinished is a repository sync ending; Event carries its result.
	EventSyncFinished
)

// Event is one broadcaster event: a change to a repository or one of its
// worktrees, published at the sync engine's and delete path's existing
// mutation points.
type Event struct {
	Type       EventType
	Repository *schema.Repository
	// Populated for EventWorktreeChanged.
	Change *WorktreeChange
	// Populated for EventSyncFinished.
	SyncCreated int
	SyncRemoved int
	SyncErr     error
}

// subscriberBuffer is how many events a Watch subscriber can lag behind before
// it is disconnected. Bounded so a slow reader can never make Publish block.
const subscriberBuffer = 64

// Broadcaster fans out daemon Events to concurrent Watch subscribers. It is
// the daemon's only new piece of shared state beyond the database — the
// daemon remains the single writer, broadcasting only observes mutations that
// already happened.
//
// Publish never blocks: each subscriber has a bounded buffered channel, and a
// subscriber whose buffer is full is dropped (its channel closed) rather than
// stalling the sync loop or any other subscriber.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewBroadcaster constructs an empty Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[chan Event]struct{})}
}

// Subscribe registers a new subscriber, returning its event channel and an
// unsubscribe function the caller must call (typically via defer) when it
// stops watching. The channel is closed on unsubscribe or when the subscriber
// is dropped for falling behind — callers should check the ok value of a
// channel receive to distinguish "dropped" from "no event yet".
func (b *Broadcaster) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, unsubscribe
}

// Publish fans ev out to every current subscriber, dropping (and closing the
// channel of) any subscriber whose buffer is already full.
func (b *Broadcaster) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			delete(b.subs, ch)
			close(ch)
		}
	}
}
