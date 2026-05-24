package events

import (
	"sync"
	"testing"

	"github.com/gitpcl/ant/internal/engine"
)

func TestPublishOrderedDelivery(t *testing.T) {
	b := NewBus()
	sub := b.Subscribe()

	const n = 50
	go func() {
		for i := 0; i < n; i++ {
			b.Publish(Event{
				Type:          TypeDetectFinding,
				DetectFinding: &DetectFindingPayload{RunID: "r", Finding: engine.Finding{Message: "f", Severity: engine.SeverityLow}},
			})
		}
		b.Close()
	}()

	var got []int
	for ev := range sub.C {
		got = append(got, ev.Seq)
	}
	if len(got) != n {
		t.Fatalf("received %d events, want %d", len(got), n)
	}
	for i, seq := range got {
		if seq != i+1 {
			t.Fatalf("event %d has Seq %d, want %d (out of order or dropped)", i, seq, i+1)
		}
	}
}

func TestMultipleSubscribersEachGetAll(t *testing.T) {
	b := NewBus()
	s1 := b.Subscribe()
	s2 := b.Subscribe()

	const n = 10
	go func() {
		for i := 0; i < n; i++ {
			b.Publish(Event{Type: TypeAntStart, AntStart: &AntStartPayload{AntID: i}})
		}
		b.Close()
	}()

	count := func(sub *Subscription) int {
		c := 0
		for range sub.C {
			c++
		}
		return c
	}

	var wg sync.WaitGroup
	wg.Add(2)
	var c1, c2 int
	go func() { defer wg.Done(); c1 = count(s1) }()
	go func() { defer wg.Done(); c2 = count(s2) }()
	wg.Wait()

	if c1 != n || c2 != n {
		t.Fatalf("subscribers got (%d, %d), want (%d, %d)", c1, c2, n, n)
	}
}

// TestConcurrentPublishersNoRace publishes from many goroutines at once. Run
// with -race: the global lock around sequence assignment + fan-out must keep it
// race-free, and every event must be delivered with a unique sequence number.
func TestConcurrentPublishersNoRace(t *testing.T) {
	b := NewBus(WithBuffer(256))
	sub := b.Subscribe()

	const publishers = 8
	const perPublisher = 100
	total := publishers * perPublisher

	// Drain concurrently so a full buffer never deadlocks the publishers.
	seen := make(map[int]bool, total)
	done := make(chan struct{})
	go func() {
		for ev := range sub.C {
			seen[ev.Seq] = true
		}
		close(done)
	}()

	var wg sync.WaitGroup
	wg.Add(publishers)
	for p := 0; p < publishers; p++ {
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				b.Publish(Event{Type: TypeAntVerified, AntVerified: &AntVerifiedPayload{AntID: p}})
			}
		}(p)
	}
	wg.Wait()
	b.Close()
	<-done

	if len(seen) != total {
		t.Fatalf("saw %d distinct sequence numbers, want %d (lost or duplicated events)", len(seen), total)
	}
	for seq := 1; seq <= total; seq++ {
		if !seen[seq] {
			t.Fatalf("missing sequence number %d (sequence not contiguous)", seq)
		}
	}
}

func TestPublishAfterCloseIsNoOp(t *testing.T) {
	b := NewBus()
	sub := b.Subscribe()
	b.Close()

	// Channel must already be closed.
	if _, ok := <-sub.C; ok {
		t.Fatalf("subscriber channel should be closed after bus Close")
	}
	// Must not panic.
	b.Publish(Event{Type: TypeRunEnd, RunEnd: &RunEndPayload{}})
	b.Close() // idempotent
}

func TestSubscribeAfterCloseGetsClosedChannel(t *testing.T) {
	b := NewBus()
	b.Close()
	sub := b.Subscribe()
	if _, ok := <-sub.C; ok {
		t.Fatalf("subscribing after close should yield a closed channel")
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := NewBus()
	keep := b.Subscribe()
	drop := b.Subscribe()
	drop.Unsubscribe()
	drop.Unsubscribe() // idempotent, must not panic

	b.Publish(Event{Type: TypeRunStart, RunStart: &RunStartPayload{RunID: "r"}})
	b.Close()

	if _, ok := <-drop.C; ok {
		t.Fatalf("unsubscribed channel should be closed and empty")
	}
	got := 0
	for range keep.C {
		got++
	}
	if got != 1 {
		t.Fatalf("kept subscriber got %d events, want 1", got)
	}
}
