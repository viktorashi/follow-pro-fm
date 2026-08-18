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

func TestStateManagerSnapshotsDoNotExposeMutableState(t *testing.T) {
	sm := NewStateManager()
	personID := int64(7)
	sm.Update(func(s *AppState) {
		s.Persons = []Person{{ID: personID, Name: "Alice"}}
		s.Connections = []WAConnectionState{{Phone: "+401", PersonID: &personID, PersonName: "Alice"}}
	})

	snapshot := sm.Get()
	snapshot.Persons[0].Name = "mutated"
	snapshot.Connections[0].Phone = "+999"
	*snapshot.Connections[0].PersonID = 99

	got := sm.Get()
	if got.Persons[0].Name != "Alice" || got.Connections[0].Phone != "+401" || *got.Connections[0].PersonID != 7 {
		t.Fatalf("Get() exposed mutable state: %+v", got)
	}

	first := sm.Subscribe()
	second := sm.Subscribe()
	sm.Update(func(s *AppState) { s.CurrentSong = "BTS" })
	firstSnapshot := <-first
	secondSnapshot := <-second
	firstSnapshot.Connections[0].Phone = "+888"
	if secondSnapshot.Connections[0].Phone != "+401" || sm.Get().Connections[0].Phone != "+401" {
		t.Fatal("subscriber snapshots share mutable connection storage")
	}
}

func TestStateManagerReplaceAndRemoveConnection(t *testing.T) {
	sm := NewStateManager()
	sm.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{{Phone: "+401", Status: StatusConnected, WhatsAppConnected: true}}
	})

	sm.ReplaceConnectionPhone("+401", "+402")
	if got := sm.Get().Connections[0].Phone; got != "+402" {
		t.Fatalf("replaced phone = %q, want +402", got)
	}

	sm.RemoveConnection("+402")
	state := sm.Get()
	if len(state.Connections) != 0 || state.WhatsAppConnected || state.Status != StatusInitializing {
		t.Fatalf("state after removing last connection = %+v", state)
	}
}
