package bus

// bus_test.go — regression tests for issue #32: a nil context used to spawn
// a goroutine that called Done() on a nil interface, killing the whole
// process with an unrecovered panic (invisible in -H windowsgui builds).

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSubscribeNilContext verifies the #32 regression fix: subscribing with
// a nil ctx must not panic and must behave like context.Background().
func TestSubscribeNilContext(t *testing.T) {
	b := New(nil)

	var got atomic.Int32
	cancel := b.Subscribe(nil, "topic", func(string, any) { got.Add(1) })
	defer cancel()

	b.Emit("topic", nil)
	if got.Load() != 1 {
		t.Fatalf("handler called %d times, want 1", got.Load())
	}
}

// TestSubscribeNilContextDoesNotKillProcess hammers Subscribe with nil
// contexts from many goroutines — with the bug, the goroutine panic would
// crash the test binary.
func TestSubscribeNilContextDoesNotKillProcess(t *testing.T) {
	b := New(nil)
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel := b.Subscribe(nil, "t", func(string, any) {})
			cancel()
		}()
	}
	wg.Wait()
}

// TestEmitSkipsNilContextSubscribers verifies Emit does not dereference a
// subscriber whose ctx was captured as nil before the #32 fix.
func TestEmitSkipsNilContextSubscribers(t *testing.T) {
	b := New(nil)

	// Simulate a legacy subscription created before the fix.
	b.mu.Lock()
	b.subs["x"] = append(b.subs["x"], &subscription{handler: func(string, any) {}, ctx: nil})
	b.mu.Unlock()

	b.Emit("x", nil) // must not panic
}

// TestSubscribeCancelledContext removes the subscriber on the next emit.
func TestSubscribeCancelledContext(t *testing.T) {
	b := New(nil)

	ctx, cancel := context.WithCancel(context.Background())
	var got atomic.Int32
	b.Subscribe(ctx, "topic", func(string, any) { got.Add(1) })
	cancel()

	// Give the cleanup goroutine time to unregister.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.Emit("topic", nil)
		if got.Load() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("handler still called after ctx cancellation")
}
