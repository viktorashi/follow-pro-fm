package poller

import (
	"testing"
	"time"
)

func TestStateManager(t *testing.T) {
	sm := NewStateManager()

	// Initial state
	state := sm.Get()
	if state.WhatsAppConnected {
		t.Error("expected initial state to be disconnected")
	}

	// Test Subscribe
	ch1 := sm.Subscribe()
	ch2 := sm.Subscribe()

	// Update state
	sm.Update(func(s *AppState) {
		s.WhatsAppConnected = true
		s.CurrentSong = "Test Song"
	})

	// Verify Get reflects update
	newState := sm.Get()
	if !newState.WhatsAppConnected || newState.CurrentSong != "Test Song" {
		t.Errorf("Get() returned unexpected state: %+v", newState)
	}

	// Verify subscribers received the first update
	select {
	case s := <-ch1:
		if !s.WhatsAppConnected || s.CurrentSong != "Test Song" {
			t.Errorf("ch1 received unexpected state: %+v", s)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for state on ch1")
	}

	select {
	case s := <-ch2:
		if !s.WhatsAppConnected || s.CurrentSong != "Test Song" {
			t.Errorf("ch2 received unexpected state: %+v", s)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for state on ch2")
	}

	// Test Unsubscribe
	sm.Unsubscribe(ch1)

	sm.Update(func(s *AppState) {
		s.CurrentSong = "Another Song"
	})

	select {
	case s := <-ch2:
		if !s.WhatsAppConnected || s.CurrentSong != "Another Song" {
			t.Errorf("ch2 received unexpected state: %+v", s)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for state on ch2")
	}

	// ch1 shouldn't receive anything since it's unsubscribed (it should be closed)
	select {
	case s, ok := <-ch1:
		if ok {
			t.Errorf("Received unexpected state on unsubscribed channel: %+v", s)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Channel should have been closed immediately")
	}
}
