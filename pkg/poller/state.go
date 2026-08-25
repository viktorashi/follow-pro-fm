package poller

import (
	"sync"
	"time"
)

type AppStatus string

const (
	StatusInitializing      AppStatus = "Initializing"
	StatusPairingRequired   AppStatus = "Pairing Required"
	StatusConnected         AppStatus = "Connected"
	StatusPolling           AppStatus = "Polling"
	StatusCampaignTriggered AppStatus = "Campaign Triggered"
	StatusSendingAudio      AppStatus = "Sending Audio"
	StatusAudioExhausted    AppStatus = "Audio Exhausted (Critical)"
	StatusError             AppStatus = "Error"
	StatusKilled            AppStatus = "Killed (Won Prize)"
	StatusSleeping          AppStatus = "Somn usor fra 💤💤😴😴(Out of campaign hours)"
)

type WAConnectionState struct {
	Phone             string
	Status            AppStatus
	WhatsAppConnected bool
	QRCodeData        string
	PersonID          *int64
	PersonName        string
	PersonSlug        string
}

type AppState struct {
	GatheringSignatures   bool
	Status                AppStatus
	WhatsAppConnected     bool
	Connections           []WAConnectionState
	Persons               []Person
	UnassignedPhonesCount int
	HasMegaCriticalAlert  bool
	KillSwitchActive      bool
	CurrentSong           string
	UnusedAudios          int
	UsedAudios            int
	LastError             string
	LastVoiceNoteSentAt   time.Time
}

type connectionSummary struct {
	anyConnected       bool
	anyPairingRequired bool
	anyError           bool
	unassigned         int
}

func summarizeConnections(connections []WAConnectionState) connectionSummary {
	var summary connectionSummary
	for _, conn := range connections {
		if conn.WhatsAppConnected {
			summary.anyConnected = true
		}
		if conn.PersonID == nil || *conn.PersonID == 0 || conn.PersonSlug == "" {
			summary.unassigned++
		}
		switch conn.Status {
		case StatusPairingRequired:
			summary.anyPairingRequired = true
		case StatusError:
			summary.anyError = true
		}
	}
	return summary
}

func (s *AppState) reconcileConnectionState() {
	summary := summarizeConnections(s.Connections)
	s.UnassignedPhonesCount = summary.unassigned
	s.HasMegaCriticalAlert = len(s.Persons) == 0 && len(s.Connections) > 0

	s.WhatsAppConnected = summary.anyConnected

	switch s.Status {
	case StatusInitializing, StatusConnected, StatusPairingRequired, StatusError:
		switch {
		case len(s.Connections) == 0:
			s.Status = StatusInitializing
		case summary.anyPairingRequired:
			s.Status = StatusPairingRequired
		case summary.anyConnected:
			s.Status = StatusConnected
		case summary.anyError:
			s.Status = StatusError
		case len(s.Connections) > 0:
			s.Status = StatusInitializing
		}
	}
}

// StateManager holds the central state and broadcasts updates to SSE clients.
type StateManager struct {
	mu          sync.RWMutex
	state       AppState
	subscribers map[chan AppState]struct{}
}

func (sm *StateManager) reconcileAndBroadcast() {
	sm.state.reconcileConnectionState()
	for ch := range sm.subscribers {
		select {
		case ch <- cloneAppState(sm.state):
		default:
			// If channel is blocked, skip it to avoid blocking the state machine.
		}
	}
}

func NewStateManager() *StateManager {
	return &StateManager{
		state: AppState{
			GatheringSignatures: true,
			Status:              StatusInitializing,
		},
		subscribers: make(map[chan AppState]struct{}),
	}
}

// Update mutates the state using a callback and then broadcasts the new state.
func (sm *StateManager) Update(fn func(state *AppState)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	fn(&sm.state)
	sm.state = cloneAppState(sm.state)
	sm.reconcileAndBroadcast()
}

// UpdateConnection updates only the state of a specific WhatsApp connection.
func (sm *StateManager) UpdateConnection(phone string, fn func(conn *WAConnectionState)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for i := range sm.state.Connections {
		if sm.state.Connections[i].Phone == phone {
			fn(&sm.state.Connections[i])
			break
		}
	}
	sm.reconcileAndBroadcast()
}

func (sm *StateManager) ReplaceConnectionPhone(from, to string) {
	sm.Update(func(state *AppState) {
		for i := range state.Connections {
			if state.Connections[i].Phone == from {
				state.Connections[i].Phone = to
				return
			}
		}
	})
}

func (sm *StateManager) RemoveConnection(phone string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	filtered := make([]WAConnectionState, 0, len(sm.state.Connections))
	for _, conn := range sm.state.Connections {
		if conn.Phone != phone {
			filtered = append(filtered, conn)
		}
	}
	sm.state.Connections = filtered
	sm.reconcileAndBroadcast()
}

// Get returns a copy of the current state.
func (sm *StateManager) Get() AppState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return cloneAppState(sm.state)
}

func cloneAppState(state AppState) AppState {
	state.Persons = append([]Person(nil), state.Persons...)
	state.Connections = append([]WAConnectionState(nil), state.Connections...)
	for i := range state.Connections {
		if state.Connections[i].PersonID != nil {
			personID := *state.Connections[i].PersonID
			state.Connections[i].PersonID = &personID
		}
	}
	return state
}

// Subscribe returns a channel that receives state updates.
func (sm *StateManager) Subscribe() chan AppState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ch := make(chan AppState, 10)
	sm.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber.
func (sm *StateManager) Unsubscribe(ch chan AppState) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.subscribers, ch)
	close(ch)
}
