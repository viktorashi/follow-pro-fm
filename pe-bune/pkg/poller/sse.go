package poller

import (
	"bytes"
	"fmt"
	"sync"
)

// SSEEvent represents a Server-Sent Event
type SSEEvent struct {
	Event string
	Data  []byte
}

func (e *SSEEvent) Marshal() []byte {
	var buf bytes.Buffer
	if e.Event != "" {
		buf.WriteString(fmt.Sprintf("event: %s\n", e.Event))
	}
	// HTMX SSE expects data lines
	lines := bytes.Split(e.Data, []byte("\n"))
	for _, line := range lines {
		buf.WriteString(fmt.Sprintf("data: %s\n", line))
	}
	buf.WriteString("\n")
	return buf.Bytes()
}

// SSEBroadcaster manages SSE client connections and broadcasts events.
type SSEBroadcaster struct {
	mu          sync.RWMutex
	subscribers map[chan *SSEEvent]struct{}
}

func NewSSEBroadcaster() *SSEBroadcaster {
	return &SSEBroadcaster{
		subscribers: make(map[chan *SSEEvent]struct{}),
	}
}

// Subscribe returns a channel that receives SSE events.
func (b *SSEBroadcaster) Subscribe() chan *SSEEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan *SSEEvent, 100)
	b.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber.
func (b *SSEBroadcaster) Unsubscribe(ch chan *SSEEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.subscribers, ch)
	close(ch)
}

// Broadcast sends an event to all connected clients.
func (b *SSEBroadcaster) Broadcast(event string, data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	ev := &SSEEvent{Event: event, Data: data}

	for ch := range b.subscribers {
		select {
		case ch <- ev:
		default:
			// If channel is full, drop the event for this slow client
		}
	}
}
