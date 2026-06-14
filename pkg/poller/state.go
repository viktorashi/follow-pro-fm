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
)

type AppState struct {
	Status              AppStatus
	WhatsAppConnected   bool
	CurrentSong         string
	UnusedAudios        int
	UsedAudios          int
	LastError           string
	LastVoiceNoteSentAt time.Time
	QRCodeData          string // Base64 or raw string for the QR code
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
