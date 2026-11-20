// Package bus implements a minimal synchronous publish/subscribe event bus.
//
// DESIGN: Observer pattern. The Wails layer subscribes once per event name and
// forwards payloads to the webview; internal packages publish without knowing
// about Wails, which keeps them unit-testable (see docs/ARCHITECTURE.md).
//
// Delivery semantics:
//   - Handlers run on the publisher's goroutine: keep them fast.
//   - A panicking handler is recovered and logged; it never takes the app down.
//   - Subscriptions are removed automatically when their channel is closed or
//     the context passed at subscription time is cancelled.
//
// AI-DEVELOPER NOTES:
//   - To add a new event: define the name in internal/nr, publish via
//     bus.Emit, and subscribe where needed.
package bus

import (
	"context"
	"log/slog"
	"sync"
)

// Handler receives the payload of an event. Payloads must be JSON-serializable
// when they are forwarded to the frontend.
type Handler func(topic string, payload any)

type subscription struct {
	handler Handler
	ctx     context.Context
}

// Bus fan-outs events to per-topic handler lists.
type Bus struct {
	mu   sync.RWMutex
	subs map[string][]*subscription
	log  *slog.Logger
}

// New creates a ready-to-use Bus.
func New(log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}
	return &Bus{subs: make(map[string][]*subscription), log: log}
}

// Subscribe registers h for topic until ctx is done. Returns a cancel func
// for manual removal.
//
// ISSUE #32: a nil ctx here used to spawn a goroutine calling Done() on a
// nil interface — an unrecovered goroutine panic that killed the whole
// process silently (invisible in -H windowsgui builds). The bus now refuses
// to panic: nil or already-dead contexts degrade to context.Background().
func (b *Bus) Subscribe(ctx context.Context, topic string, h Handler) func() {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	sub := &subscription{handler: h, ctx: ctx}
	b.subs[topic] = append(b.subs[topic], sub)

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		b.remove(topic, sub)
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

func (b *Bus) remove(topic string, sub *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[topic]
	for i, s := range list {
		if s == sub {
			b.subs[topic] = append(list[:i], list[i+1:]...)
			return
		}
	}
}

// Emit delivers payload to every live subscriber of topic.
//
// ISSUE #32: subscribers created before the runtime context existed (nil)
// are skipped defensively instead of dereferencing nil.
func (b *Bus) Emit(topic string, payload any) {
	b.mu.RLock()
	list := b.subs[topic]
	// Copy the slice: handlers may (un)subscribe while we iterate.
	handlers := make([]Handler, 0, len(list))
	for _, s := range list {
		if s.ctx != nil && s.ctx.Err() == nil {
			handlers = append(handlers, s.handler)
		}
	}
	b.mu.RUnlock()

	for _, h := range handlers {
		b.safeCall(topic, payload, h)
	}
}

func (b *Bus) safeCall(topic string, payload any, h Handler) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("event bus handler panic", "topic", topic, "panic", r)
		}
	}()
	h(topic, payload)
}
