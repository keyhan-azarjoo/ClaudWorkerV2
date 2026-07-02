// Package controlplane is the Control Plane (docs/09, docs/21 S10A — renamed from Dashboard).
//
// The Control Plane is the single API surface in front of the engine. It owns the REST API,
// WebSocket/SSE events, authentication, commands, queries, status, metrics, and streaming updates —
// and NO business logic. Every subsystem stays independent; the Control Plane merely exposes them by
// running injected handlers and streaming events they publish.
//
// This makes the Dashboard just one CLIENT of the Control Plane. The Web Dashboard, Flutter desktop,
// Flutter mobile, and CLI all speak the same API — none may touch internal packages directly, and no
// client holds unique business logic (every UI feature is an API call).
//
// The package is a leaf (stdlib only): it imports no subsystem, so it cannot duplicate their logic.
// The wiring layer registers handlers that call the real subsystems.
package controlplane

import (
	"sync"
	"time"
)

// Event-type vocabulary. It is an open set of strings (new subsystems add their own without changing
// the Control Plane); these are the documented core events.
const (
	EventAssignmentCreated    = "AssignmentCreated"
	EventAssignmentCompleted  = "AssignmentCompleted"
	EventVerificationStarted  = "VerificationStarted"
	EventVerificationFinished = "VerificationFinished"
	EventLeaseGranted         = "LeaseGranted"
	EventLeaseExpired         = "LeaseExpired"
	EventRuntimeStarted       = "RuntimeStarted"
	EventRuntimeFinished      = "RuntimeFinished"
	EventKnowledgeUpdated     = "KnowledgeUpdated"
	EventPolicyDecision       = "PolicyDecision"
)

// Event is one thing that happened in a subsystem. Data is any JSON-serialisable payload.
type Event struct {
	Seq       uint64    `json:"seq"`       // monotonic sequence (also the SSE id, enables replay)
	Type      string    `json:"type"`      // e.g. "AssignmentCreated"
	Subsystem string    `json:"subsystem"` // e.g. "assignment"
	Time      time.Time `json:"time"`
	Data      any       `json:"data,omitempty"`
}

// Bus is the publish/subscribe hub. Subsystems publish; the Control Plane streams to clients. It keeps
// a bounded ring of recent events so a reconnecting client can replay what it missed.
type Bus struct {
	mu      sync.Mutex
	subs    map[int]chan Event
	nextSub int
	seq     uint64
	ring    []Event
	ringCap int
	now     func() time.Time
}

// BusOption configures a Bus.
type BusOption func(*Bus)

// WithClock overrides the time source (deterministic events in tests).
func WithClock(now func() time.Time) BusOption { return func(b *Bus) { b.now = now } }

// WithRingCapacity sets how many recent events are retained for replay (default 256).
func WithRingCapacity(n int) BusOption {
	return func(b *Bus) {
		if n > 0 {
			b.ringCap = n
		}
	}
}

// NewBus returns an empty Bus.
func NewBus(opts ...BusOption) *Bus {
	b := &Bus{subs: map[int]chan Event{}, ringCap: 256, now: time.Now}
	for _, o := range opts {
		o(b)
	}
	return b
}

// Publish records an event and delivers it to every subscriber. Delivery is non-blocking: a
// subscriber whose buffer is full is skipped for this event (it can replay from Recent on reconnect),
// so a slow client never stalls a publishing subsystem. Returns the assigned event.
func (b *Bus) Publish(eventType, subsystem string, data any) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	ev := Event{Seq: b.seq, Type: eventType, Subsystem: subsystem, Time: b.now().UTC(), Data: data}
	// append to bounded ring
	b.ring = append(b.ring, ev)
	if len(b.ring) > b.ringCap {
		b.ring = b.ring[len(b.ring)-b.ringCap:]
	}
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default: // subscriber buffer full → skip (replayable via Recent)
		}
	}
	return ev
}

// Subscribe returns a subscriber id and a channel of future events. Call Unsubscribe with the id when
// done. The channel is buffered; consume promptly to avoid skips.
func (b *Bus) Subscribe(buffer int) (int, <-chan Event) {
	if buffer <= 0 {
		buffer = 64
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextSub
	b.nextSub++
	ch := make(chan Event, buffer)
	b.subs[id] = ch
	return id, ch
}

// Unsubscribe removes and closes a subscriber's channel.
func (b *Bus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

// Recent returns retained events with Seq strictly greater than afterSeq (0 = all retained), oldest
// first. Used for SSE replay via Last-Event-ID.
func (b *Bus) Recent(afterSeq uint64) []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Event, 0, len(b.ring))
	for _, ev := range b.ring {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out
}
