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
	StatusSleeping          AppStatus = "Sleeping (Out of campaign hours)"
)

type WAConnectionState struct {
	Phone             string
	Status            AppStatus
	WhatsAppConnected bool
	QRCodeData        string
}

type AppState struct {
	Status              AppStatus
	Connections         []WAConnectionState
	KillSwitchActive    bool
	CurrentSong         string
	UnusedAudios        int
	UsedAudios          int
	LastError           string
	LastVoiceNoteSentAt time.Time
}

// StateManager holds the central state and broadcasts updates to SSE clients.
type StateManager struct {
	mu          sync.RWMutex
	state       AppState
	subscribers map[chan AppState]struct{}
}

func NewStateManager() *StateManager {
	return &StateManager{
		state: AppState{
			Status: StatusInitializing,
		},
		subscribers: make(map[chan AppState]struct{}),
	}
}

// Update mutates the state using a callback and then broadcasts the new state.
func (sm *StateManager) Update(fn func(state *AppState)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	fn(&sm.state)

	// Broadcast
	for ch := range sm.subscribers {
		select {
		case ch <- sm.state:
		default:
			// If channel is blocked, skip it to avoid blocking the state machine
		}
	}
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

	// Broadcast
	for ch := range sm.subscribers {
		select {
		case ch <- sm.state:
		default:
		}
	}
}

// Get returns a copy of the current state.
func (sm *StateManager) Get() AppState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state
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
