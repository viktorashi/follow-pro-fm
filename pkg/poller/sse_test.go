package poller

import (
	"testing"
	"time"
)

func TestSSEBroadcaster(t *testing.T) {
	b := NewSSEBroadcaster()

	// Test subscribe
	ch1 := b.Subscribe()
	ch2 := b.Subscribe()

	// Test broadcast
	go func() {
		b.Broadcast("test-event", []byte("hello"))
	}()

	select {
	case msg := <-ch1:
		if msg.Event != "test-event" || string(msg.Data) != "hello" {
			t.Errorf("Unexpected message on ch1: %+v", msg)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for message on ch1")
	}

	select {
	case msg := <-ch2:
		if msg.Event != "test-event" || string(msg.Data) != "hello" {
			t.Errorf("Unexpected message on ch2: %+v", msg)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for message on ch2")
	}

	// Test unsubscribe
	b.Unsubscribe(ch1)

	go func() {
		b.Broadcast("test-event-2", []byte("world"))
	}()

	select {
	case msg := <-ch2:
		if msg.Event != "test-event-2" || string(msg.Data) != "world" {
			t.Errorf("Unexpected message on ch2: %+v", msg)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for message on ch2")
	}

	// ch1 shouldn't receive anything since it's unsubscribed (it should be closed)
	select {
	case msg, ok := <-ch1:
		if ok {
			t.Errorf("Received unexpected message on unsubscribed channel: %+v", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Channel should have been closed immediately")
	}
}
