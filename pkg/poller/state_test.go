package poller

import (
	"testing"
	"time"
)

func TestStateManager(t *testing.T) {
	sm := NewStateManager()

	// Initial state
	state := sm.Get()
	if len(state.Connections) > 0 && state.Connections[0].WhatsAppConnected {
		t.Error("expected initial state to be disconnected")
	}

	// Test Subscribe
	ch1 := sm.Subscribe()
	ch2 := sm.Subscribe()

	// Update state
	sm.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{{Phone: "+40", WhatsAppConnected: true}}
		s.CurrentSong = "Test Song"
	})

	// Verify Get reflects update
	newState := sm.Get()
	if len(newState.Connections) == 0 || !newState.Connections[0].WhatsAppConnected || newState.CurrentSong != "Test Song" {
		t.Errorf("Get() returned unexpected state: %+v", newState)
	}

	// Verify subscribers received the first update
	select {
	case s := <-ch1:
		if len(s.Connections) == 0 || !s.Connections[0].WhatsAppConnected || s.CurrentSong != "Test Song" {
			t.Errorf("ch1 received unexpected state: %+v", s)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for state on ch1")
	}

	select {
	case s := <-ch2:
		if len(s.Connections) == 0 || !s.Connections[0].WhatsAppConnected || s.CurrentSong != "Test Song" {
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
		if len(s.Connections) == 0 || !s.Connections[0].WhatsAppConnected || s.CurrentSong != "Another Song" {
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

func TestStateManagerDerivesAggregateConnectionStatus(t *testing.T) {
	sm := NewStateManager()

	sm.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{
			{Phone: "+401", Status: StatusPairingRequired},
			{Phone: "+402", Status: StatusConnected, WhatsAppConnected: true},
		}
	})

	state := sm.Get()
	if state.Status != StatusPairingRequired {
		t.Fatalf("status = %q, want %q", state.Status, StatusPairingRequired)
	}
	if !state.WhatsAppConnected {
		t.Fatal("expected aggregate WhatsAppConnected to be true when one sender is connected")
	}

	sm.UpdateConnection("+401", func(conn *WAConnectionState) {
		conn.Status = StatusConnected
		conn.WhatsAppConnected = true
		conn.QRCodeData = ""
	})

	state = sm.Get()
	if state.Status != StatusConnected {
		t.Fatalf("status = %q, want %q", state.Status, StatusConnected)
	}
	if !state.WhatsAppConnected {
		t.Fatal("expected aggregate WhatsAppConnected to stay true")
	}
}

func TestStateManagerKeepsRuntimeStatusWhenConnectionsChange(t *testing.T) {
	sm := NewStateManager()

	sm.Update(func(s *AppState) {
		s.Status = StatusPolling
		s.Connections = []WAConnectionState{
			{Phone: "+401", Status: StatusPairingRequired},
		}
	})

	state := sm.Get()
	if state.Status != StatusPolling {
		t.Fatalf("status = %q, want %q", state.Status, StatusPolling)
	}
}
