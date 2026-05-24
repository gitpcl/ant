package events

import (
	"sync"
	"time"
)

// Subscription is a consumer's handle on the event stream. Read from C until it
// is closed (which happens when the bus is closed). Call Unsubscribe to detach
// early; it is safe to call more than once.
type Subscription struct {
	C   <-chan Event
	bus *Bus
	ch  chan Event
}

// Unsubscribe detaches the subscription and closes its channel. Safe to call
// multiple times and concurrently with bus shutdown.
func (s *Subscription) Unsubscribe() {
	s.bus.unsubscribe(s.ch)
}

// Bus is an in-process publish/subscribe event bus. Publish assigns a global
// monotonic sequence number under a single lock, so every subscriber observes
// events in the same total order even when many goroutines publish
// concurrently. Each subscriber has its own buffered channel; a slow consumer
// applies backpressure to publishers rather than dropping events.
//
// The bus is the single source of truth feeding both the TUI renderer and the
// --json renderer (TECHSPEC §3, §11).
type Bus struct {
	mu          sync.Mutex
	subscribers map[chan Event]struct{}
	seq         int
	closed      bool
	bufSize     int
	now         func() time.Time
}

// busOption configures a Bus.
type busOption func(*Bus)

// WithBuffer sets the per-subscriber channel buffer size. A larger buffer lets
// publishers run ahead of a slow consumer before backpressure kicks in.
func WithBuffer(n int) busOption {
	return func(b *Bus) {
		if n >= 0 {
			b.bufSize = n
		}
	}
}

// WithClock injects the clock the bus stamps events with. It exists so golden
// --json tests can produce byte-stable output (TECHSPEC §12): a fixed clock
// makes the event stream deterministic without post-hoc timestamp scrubbing.
// Production callers omit it and get time.Now.
func WithClock(now func() time.Time) busOption {
	return func(b *Bus) {
		if now != nil {
			b.now = now
		}
	}
}

// NewBus creates an empty bus with no subscribers.
func NewBus(opts ...busOption) *Bus {
	b := &Bus{
		subscribers: make(map[chan Event]struct{}),
		bufSize:     64,
		now:         time.Now,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Subscribe registers a new consumer and returns its Subscription. Subscribing
// after the bus is closed returns a subscription whose channel is already
// closed.
func (b *Bus) Subscribe() *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, b.bufSize)
	if b.closed {
		close(ch)
		return &Subscription{C: ch, bus: b, ch: ch}
	}
	b.subscribers[ch] = struct{}{}
	return &Subscription{C: ch, bus: b, ch: ch}
}

// Publish stamps the event with the next sequence number and current time, then
// delivers it to every subscriber in registration-independent total order.
// Delivery holds the bus lock, which both guarantees the global ordering and
// serializes the fan-out; sends block if a subscriber's buffer is full
// (backpressure, never drop). Publishing to a closed bus is a no-op.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.seq++
	ev.Seq = b.seq
	if ev.Time == "" {
		ev.Time = b.now().UTC().Format(time.RFC3339Nano)
	}
	for ch := range b.subscribers {
		ch <- ev
	}
}

// unsubscribe removes a subscriber channel and closes it. Idempotent.
func (b *Bus) unsubscribe(ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
}

// Close shuts the bus down: no further events are delivered and every remaining
// subscriber channel is closed, unblocking consumers ranging over them. Close
// is idempotent.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subscribers {
		delete(b.subscribers, ch)
		close(ch)
	}
}
