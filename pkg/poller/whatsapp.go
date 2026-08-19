package poller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	"modernc.org/sqlite"
)

const (
	ConnectionRetryDelay    = 5 * time.Second
	ConnectionRetryAttempts = 30
)

var (
	pairingCancelsMutex sync.Mutex
	pairingCancels      = make(map[string]context.CancelFunc)
)

func CancelPairing(phone string) {
	pairingCancelsMutex.Lock()
	defer pairingCancelsMutex.Unlock()
	if cancel, ok := pairingCancels[phone]; ok {
		cancel()
		delete(pairingCancels, phone)
	}
}

func init() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		_, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;", nil)
		return err
	})
}

type WhatsAppClient interface {
	Connect() error
	Disconnect()
	IsConnected() bool
	IsLoggedIn() bool
	SendPresence(ctx context.Context, presence types.Presence) error
	SubscribePresence(ctx context.Context, jid types.JID) error
	SendChatPresence(ctx context.Context, jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error
	IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error)
	Upload(ctx context.Context, data []byte, mediaType whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
	SendMessage(ctx context.Context, to types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
	AddEventHandler(handler whatsmeow.EventHandler) uint32
	RemoveEventHandler(id uint32)
}

func waitRetry(ctx context.Context) bool {
	select {
	case <-time.After(ConnectionRetryDelay):
		return true
	case <-ctx.Done():
		return false
	}
}

func handleConnectionError(err error, label string, phone string, stateMgr *StateManager, ctx context.Context) bool {
	if stateMgr != nil {
		stateMgr.UpdateConnection(phone, func(s *WAConnectionState) {
			s.Status = StatusError
			s.WhatsAppConnected = false
		})
	}
	fmt.Printf("❌ %s (retrying in %s): %v\n", label, ConnectionRetryDelay, err)
	return waitRetry(ctx)
}

type whatsappClientWrapper struct {
	mu       sync.RWMutex
	client   *whatsmeow.Client
	handlers []whatsmeow.EventHandler
}

func withClient[T any](w *whatsappClientWrapper, action func(*whatsmeow.Client) (T, error)) (T, error) {
	w.mu.RLock()
	c := w.client
	w.mu.RUnlock()
	if c != nil {
		return action(c)
	}
	var zero T
	return zero, fmt.Errorf("no client")
}

func withClientErr(w *whatsappClientWrapper, action func(*whatsmeow.Client) error) error {
	w.mu.RLock()
	c := w.client
	w.mu.RUnlock()
	if c != nil {
		return action(c)
	}
	return fmt.Errorf("no client")
}

func withClientVal[T any](w *whatsappClientWrapper, action func(*whatsmeow.Client) T) T {
	w.mu.RLock()
	c := w.client
	w.mu.RUnlock()
	if c != nil {
		return action(c)
	}
	var zero T
	return zero
}

func withClientVoid(w *whatsappClientWrapper, action func(*whatsmeow.Client)) {
	w.mu.RLock()
	c := w.client
	w.mu.RUnlock()
	if c != nil {
		action(c)
	}
}

func (w *whatsappClientWrapper) setClient(c *whatsmeow.Client) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.client = c
	for _, h := range w.handlers {
		c.AddEventHandler(h)
	}
}

func (w *whatsappClientWrapper) Connect() error {
	return withClientErr(w, func(c *whatsmeow.Client) error { return c.Connect() })
}

func (w *whatsappClientWrapper) Disconnect() {
	withClientVoid(w, func(c *whatsmeow.Client) { c.Disconnect() })
}

func (w *whatsappClientWrapper) IsConnected() bool {
	return withClientVal(w, func(c *whatsmeow.Client) bool { return c.IsConnected() })
}

func (w *whatsappClientWrapper) IsLoggedIn() bool {
	return withClientVal(w, func(c *whatsmeow.Client) bool { return c.IsLoggedIn() })
}

func (w *whatsappClientWrapper) SendPresence(ctx context.Context, presence types.Presence) error {
	return withClientErr(w, func(c *whatsmeow.Client) error { return c.SendPresence(ctx, presence) })
}

func (w *whatsappClientWrapper) SubscribePresence(ctx context.Context, jid types.JID) error {
	return withClientErr(w, func(c *whatsmeow.Client) error { return c.SubscribePresence(ctx, jid) })
}

func (w *whatsappClientWrapper) SendChatPresence(ctx context.Context, jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error {
	return withClientErr(w, func(c *whatsmeow.Client) error { return c.SendChatPresence(ctx, jid, state, media) })
}

func (w *whatsappClientWrapper) IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	return withClient(w, func(c *whatsmeow.Client) ([]types.IsOnWhatsAppResponse, error) {
		return c.IsOnWhatsApp(ctx, phones)
	})
}

func (w *whatsappClientWrapper) Upload(ctx context.Context, data []byte, mediaType whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	return withClient(w, func(c *whatsmeow.Client) (whatsmeow.UploadResponse, error) {
		return c.Upload(ctx, data, mediaType)
	})
}

func (w *whatsappClientWrapper) SendMessage(ctx context.Context, to types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	return withClient(w, func(c *whatsmeow.Client) (whatsmeow.SendResponse, error) {
		return c.SendMessage(ctx, to, message, extra...)
	})
}

func (w *whatsappClientWrapper) AddEventHandler(handler whatsmeow.EventHandler) uint32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers = append(w.handlers, handler)
	if w.client != nil {
		w.client.AddEventHandler(handler)
	}
	return uint32(len(w.handlers))
}

func (w *whatsappClientWrapper) RemoveEventHandler(id uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.client != nil {
		w.client.RemoveEventHandler(id)
	}
}

// InitWhatsApp initializes the WhatsApp client and handles connection/pairing
func InitWhatsApp(phone string, dbPath string, stateMgr *StateManager, alerter Alerter, baseURL string, onPaired ...func(string)) (WhatsAppClient, error) {
	if os.Getenv("MOCK_WHATSAPP") == "true" {
		client := &MockWhatsAppClient{
			phone:     phone,
			dbPath:    dbPath,
			stateMgr:  stateMgr,
			alerter:   alerter,
			baseURL:   baseURL,
			connected: true,
			loggedIn:  true,
		}
		_ = client.Connect()
		return client, nil
	}

	dbLog := waLog.Stdout("Database", "WARN", true)
	// Open connection to sqlite database using pure Go driver
	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+dbPath+"?_foreign_keys=on", dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	wrapper := &whatsappClientWrapper{}

	// Add event handlers to ensure we're processing E2E and presence
	wrapper.AddEventHandler(func(evt interface{}) {
		switch evt.(type) {
		case *events.Connected:
			// Tell WhatsApp servers we are online.
			// Crucial for E2E prekey setups and for avoiding "Waiting for this message".
			_ = wrapper.SendPresence(context.Background(), types.PresenceAvailable)
			if stateMgr != nil {
				isSleeping := stateMgr.Get().Status == StatusSleeping
				stateMgr.UpdateConnection(phone, func(s *WAConnectionState) {
					s.WhatsAppConnected = true
					if s.Status != StatusPairingRequired {
						if isSleeping {
							s.Status = StatusSleeping
						} else {
							s.Status = StatusConnected
						}
					}
				})
			}
		case *events.Disconnected:
			fmt.Println("🔌 Disconnected from WhatsApp servers")
			if stateMgr != nil {
				isSleeping := stateMgr.Get().Status == StatusSleeping
				stateMgr.UpdateConnection(phone, func(s *WAConnectionState) {
					s.WhatsAppConnected = false
					if s.Status != StatusPairingRequired {
						if isSleeping {
							s.Status = StatusSleeping
						} else {
							s.Status = StatusError
						}
					}
				})
			}
		case *events.LoggedOut:
			if stateMgr != nil {
				stateMgr.UpdateConnection(phone, func(s *WAConnectionState) {
					s.Status = StatusPairingRequired
					s.WhatsAppConnected = false
				})
			}
		}
	})

	// Set a realistic device name
	store.DeviceProps.Os = proto.String("Mac OS")

	ctx, cancel := context.WithCancel(context.Background())
	pairingCancelsMutex.Lock()
	pairingCancels[phone] = cancel
	pairingCancelsMutex.Unlock()

	// Run connection logic asynchronously so we don't block the telemetry server
	// and so we can retry on network failures.
	go func() {
		defer func() {
			pairingCancelsMutex.Lock()
			delete(pairingCancels, phone)
			pairingCancelsMutex.Unlock()
			cancel()
		}()

		for {
			if ctx.Err() != nil {
				return
			}

			deviceStore, err := container.GetFirstDevice(context.Background())
			if err != nil {
				fmt.Printf("❌ Failed to get first device: %v\n", err)
				time.Sleep(ConnectionRetryDelay)
				continue
			}

			// Always re-create the client in this loop to avoid using a deleted device after logout
			client := whatsmeow.NewClient(deviceStore, clientLog)
			wrapper.setClient(client)

			if client.Store.ID == nil {
				// No session exists, perform login
				qrChan, _ := client.GetQRChannel(ctx)
				err = client.Connect()
				if err != nil {
					if handleConnectionError(err, "Failed to connect for pairing", phone, stateMgr, ctx) {
						continue
					}
					return
				}

				fmt.Print("\033[s") // Save cursor position
				fmt.Println("\n👉 Please scan the QR code below using your WhatsApp Business/personal app (Settings -> Linked Devices -> Link a Device):")
				paired := false
				alertSent := false
				for evt := range qrChan {
					if evt.Event == "code" {
						if stateMgr != nil {
							png, _ := qrcode.Encode(evt.Code, qrcode.Medium, 256)
							b64 := base64.StdEncoding.EncodeToString(png)
							stateMgr.UpdateConnection(phone, func(s *WAConnectionState) {
								s.Status = StatusPairingRequired
								s.QRCodeData = "data:image/png;base64," + b64
							})
						}

						if !alertSent && alerter != nil && baseURL != "" {
							_ = alerter.AlertCritical(AlertEvent{
								Title:       "WhatsApp Disconnected",
								Message:     "WhatsApp disconnected! Action required immediately. Scan the QR code on the dashboard.",
								ActionLabel: "Open Live Dashboard",
								ActionURL:   baseURL,
							})
							alertSent = true
						}

						fmt.Print("\033[u\033[J") // Restore cursor and clear to end of screen
						fmt.Println("\n👉 Please scan the QR code below using your WhatsApp Business/personal app (Settings -> Linked Devices -> Link a Device):")
						qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
					} else {
						fmt.Print("\033[u\033[J") // Restore cursor and clear to end of screen
						switch evt.Event {
						case "success":
							fmt.Println("✅ Successfully paired!")
							paired = true
							if stateMgr != nil {
								stateMgr.UpdateConnection(phone, func(s *WAConnectionState) {
									s.Status = StatusConnected
									s.QRCodeData = ""
									s.WhatsAppConnected = true
								})
							}
							if len(onPaired) > 0 && client.Store.ID != nil && client.Store.ID.User != "" {
								onPaired[0]("+" + client.Store.ID.User)
							}
						case "timeout":
							fmt.Println("⏳ QR code scan timed out. Retrying connection...")
						case "error":
							fmt.Printf("❌ Pairing error: %v\n", evt.Error)
						default:
							fmt.Printf("ℹ️ Login event: %s\n", evt.Event)
						}
					}
				}

				if !paired {
					if ctx.Err() != nil {
						return
					}
					fmt.Println("❌ Login timed out or failed, retrying...")
					client.Disconnect()
					if waitRetry(ctx) {
						continue
					}
					return
				}

				for i := 0; i < ConnectionRetryAttempts; i++ {
					if client.IsLoggedIn() && client.IsConnected() {
						break
					}
					select {
					case <-time.After(500 * time.Millisecond):
					case <-ctx.Done():
						return
					}
				}
			} else {
				// Session exists, connect automatically
				err := client.Connect()
				if err != nil {
					if handleConnectionError(err, "Failed to connect", phone, stateMgr, ctx) {
						continue
					}
					return
				}

				if stateMgr != nil {
					stateMgr.UpdateConnection(phone, func(s *WAConnectionState) {
						s.Status = StatusConnected
						s.WhatsAppConnected = true
					})
				}
			}

			// Wait for LoggedOut event to restart the connection/pairing loop,
			// or ctx.Done() to terminate the loop cleanly when disconnected from UI.
			logoutChan := make(chan struct{})
			handlerID := client.AddEventHandler(func(evt interface{}) {
				if _, ok := evt.(*events.LoggedOut); ok {
					select {
					case <-logoutChan:
					default:
						close(logoutChan)
					}
				}
			})
			select {
			case <-logoutChan:
				client.RemoveEventHandler(handlerID)
				client.Disconnect()
			case <-ctx.Done():
				client.RemoveEventHandler(handlerID)
				client.Disconnect()
				return
			}
		}
	}()

	return wrapper, nil
}

func retryWhatsAppOperation[T any](operationName string, action func() (T, error)) (T, error) {
	var result T
	var err error
	for i := 0; i < 3; i++ {
		result, err = action()
		if err == nil {
			break
		}
		if strings.Contains(err.Error(), "463") || strings.Contains(err.Error(), "ReachoutTimelocked") {
			break
		}
		fmt.Printf("   ⚠️ %s attempt %d failed: %v. Retrying in 2s...\n", operationName, i+1, err)
		time.Sleep(2 * time.Second)
	}
	return result, err
}

// normalizePhoneNumber normalizes Romanian and international numbers to numbers-only format
func normalizePhoneNumber(phone string) string {
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "+", "")
	phone = strings.ReplaceAll(phone, "(", "")
	phone = strings.ReplaceAll(phone, ")", "")

	if strings.HasPrefix(phone, "0") && len(phone) == 10 {
		phone = "40" + phone[1:]
	}
	return phone
}

func SendVoiceNote(client WhatsAppClient, phone string, audioPath string) error {
	normalized := normalizePhoneNumber(phone)
	// Wait up to 15 seconds for the client to be fully connected and logged in
	for i := 0; i < 30; i++ {
		if client.IsConnected() && client.IsLoggedIn() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !client.IsConnected() || !client.IsLoggedIn() {
		return fmt.Errorf("whatsapp client is not fully connected or logged in after waiting")
	}

	// Resolve canonical JID (handles LID migration)
	targetJID := types.NewJID(normalized, types.DefaultUserServer)
	isOnWA, err := client.IsOnWhatsApp(context.Background(), []string{normalized})
	if err == nil && len(isOnWA) > 0 && isOnWA[0].IsIn {
		targetJID = isOnWA[0].JID
		fmt.Printf("   ℹ️ Resolved WhatsApp JID for %s: %s\n", phone, targetJID.String())
	} else {
		fmt.Printf("   ⚠️ Failed to resolve canonical JID for %s: %v. Falling back to default JID.\n", phone, err)
	}

	// Get exact duration of the audio (falls back to heuristic if not supported)
	duration, err := GetAudioDuration(audioPath)
	if err != nil {
		fmt.Printf("   ⚠️ Failed to get audio duration for %s: %v\n", audioPath, err)
		// Fallback size heuristic if duration extraction totally failed
		if info, statErr := os.Stat(audioPath); statErr == nil {
			duration = time.Duration(info.Size()/2500) * time.Second
		} else {
			duration = 10 * time.Second
		}
	}

	estimatedSeconds := uint32(duration.Seconds())
	if estimatedSeconds < 1 {
		estimatedSeconds = 1
	} else if estimatedSeconds > 30 {
		estimatedSeconds = 30
	}

	// 0. Proactively subscribe to presence to refresh tokens/privacy state if needed
	_ = client.SubscribePresence(context.Background(), targetJID)

	// 1. Send "recording audio" presence and sleep for estimatedSeconds
	_ = client.SendChatPresence(context.Background(), targetJID, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)

	// 2. Sleep for the duration of the audio to simulate recording time
	if os.Getenv("MOCK_WHATSAPP") != "true" {
		time.Sleep(time.Duration(estimatedSeconds) * time.Second)
	}

	// 3. Clear recording state (paused presence)
	_ = client.SendChatPresence(context.Background(), targetJID, types.ChatPresencePaused, types.ChatPresenceMediaAudio)

	// 4. Generate the exact send timestamp
	now := time.Now().UTC().Format(time.RFC3339)
	tmpPath := fmt.Sprintf("%s.tmp.ogg", audioPath)

	// 5. Run ffmpeg to inject this timestamp into the OGG metadata creation_time
	ffmpegPath, ffmpegErr := ffmpegBinaryPath()
	if ffmpegErr != nil {
		fmt.Printf("   ⚠️ Failed to locate ffmpeg for metadata injection, falling back to original: %v\n", ffmpegErr)
		tmpPath = audioPath
	} else {
		cmd := exec.Command(ffmpegPath, "-y", "-i", audioPath, "-c", "copy", "-metadata", "creation_time="+now, tmpPath)
		if err := cmd.Run(); err != nil {
			fmt.Printf("   ⚠️ Failed to inject metadata with ffmpeg, falling back to original: %v\n", err)
			tmpPath = audioPath
		} else {
			defer func() { _ = os.Remove(tmpPath) }()
		}
	}

	// 6. Read the newly modified file
	audioData, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to read audio file at %s: %w", tmpPath, err)
	}

	// Upload to WhatsApp servers
	uploaded, uploadErr := retryWhatsAppOperation("Upload", func() (whatsmeow.UploadResponse, error) {
		return client.Upload(context.Background(), audioData, whatsmeow.MediaAudio)
	})
	if uploadErr != nil {
		return fmt.Errorf("failed to upload audio to WhatsApp after retries: %w", uploadErr)
	}

	// Extract waveform
	waveform, err := ExtractWaveform(audioPath)
	if err != nil {
		fmt.Printf("   ⚠️ Failed to extract waveform for %s: %v\n", audioPath, err)
	}

	// Construct AudioMessage with Push-To-Talk set to true (native voice note bubble)
	msg := &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			URL:               proto.String(uploaded.URL),
			DirectPath:        proto.String(uploaded.DirectPath),
			MediaKey:          uploaded.MediaKey,
			Mimetype:          proto.String("audio/ogg; codecs=opus"),
			FileEncSHA256:     uploaded.FileEncSHA256,
			FileSHA256:        uploaded.FileSHA256,
			FileLength:        proto.Uint64(uint64(len(audioData))),
			PTT:               proto.Bool(true), // Makes it a native voice note
			Seconds:           proto.Uint32(estimatedSeconds),
			Waveform:          waveform,
			MediaKeyTimestamp: proto.Int64(time.Now().Unix()),
		},
	}

	// Send message
	resp, sendErr := retryWhatsAppOperation("Send message", func() (whatsmeow.SendResponse, error) {
		return client.SendMessage(context.Background(), targetJID, msg)
	})
	if sendErr != nil {
		return fmt.Errorf("failed to send message to %s after retries: %w", targetJID, sendErr)
	}

	fmt.Printf("   ✅ Voice note sent! JID: %s, Message ID: %s, Timestamp: %s\n", targetJID, resp.ID, resp.Timestamp)
	return nil
}

type MockSentMessage struct {
	Phone        string    `json:"phone"`
	Waveform     []byte    `json:"waveform"`
	CreationTime time.Time `json:"creation_time"`
}

type MockWhatsAppClient struct {
	phone         string
	dbPath        string
	stateMgr      *StateManager
	alerter       Alerter
	baseURL       string
	connected     bool
	loggedIn      bool
	uploadedAudio []byte
	eventHandlers []whatsmeow.EventHandler
	mu            sync.Mutex
}

func (m *MockWhatsAppClient) Connect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.connected = true
	if _, err := os.Stat(m.dbPath); err == nil {
		data, err := os.ReadFile(m.dbPath)
		if err == nil && string(data) == "paired" {
			m.loggedIn = true
		}
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		m.mu.Lock()
		loggedIn := m.loggedIn
		handlers := m.eventHandlers
		m.mu.Unlock()

		if loggedIn {
			if m.stateMgr != nil {
				m.stateMgr.UpdateConnection(m.phone, func(s *WAConnectionState) {
					s.Status = StatusConnected
					s.WhatsAppConnected = true
					s.QRCodeData = ""
				})
			}
			for _, h := range handlers {
				h(&events.Connected{})
			}
		} else {
			if m.stateMgr != nil {
				png, _ := qrcode.Encode("mock-qr-code", qrcode.Medium, 256)
				b64 := base64.StdEncoding.EncodeToString(png)
				m.stateMgr.UpdateConnection(m.phone, func(s *WAConnectionState) {
					s.Status = StatusPairingRequired
					s.QRCodeData = "data:image/png;base64," + b64
					s.WhatsAppConnected = false
				})
			}
			if m.alerter != nil && m.baseURL != "" {
				_ = m.alerter.AlertCritical(AlertEvent{
					Title:       "WhatsApp Disconnected",
					Message:     "WhatsApp disconnected! Action required immediately. Scan the QR code on the dashboard.",
					ActionLabel: "Open Live Dashboard",
					ActionURL:   m.baseURL,
				})
			}
		}
	}()

	return nil
}

func (m *MockWhatsAppClient) Disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
}

func (m *MockWhatsAppClient) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

func (m *MockWhatsAppClient) IsLoggedIn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loggedIn
}

func (m *MockWhatsAppClient) SendPresence(ctx context.Context, presence types.Presence) error {
	return nil
}

func (m *MockWhatsAppClient) SubscribePresence(ctx context.Context, jid types.JID) error {
	return nil
}

func (m *MockWhatsAppClient) SendChatPresence(ctx context.Context, jid types.JID, state types.ChatPresence, media types.ChatPresenceMedia) error {
	return nil
}

func (m *MockWhatsAppClient) IsOnWhatsApp(ctx context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
	var resp []types.IsOnWhatsAppResponse
	for _, p := range phones {
		resp = append(resp, types.IsOnWhatsAppResponse{
			IsIn: true,
			JID:  types.NewJID(p, types.DefaultUserServer),
		})
	}
	return resp, nil
}

func (m *MockWhatsAppClient) Upload(ctx context.Context, data []byte, mediaType whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	m.mu.Lock()
	m.uploadedAudio = append(m.uploadedAudio[:0], data...)
	m.mu.Unlock()

	return whatsmeow.UploadResponse{
		URL:           "https://mock.whatsapp.net/media",
		DirectPath:    "/mock/media/path",
		MediaKey:      []byte("mock-media-key-1234567890123456789012"),
		FileEncSHA256: []byte{1, 2, 3},
		FileSHA256:    []byte{4, 5, 6},
	}, nil
}

func (m *MockWhatsAppClient) SendMessage(ctx context.Context, to types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var waveform []byte
	if message.AudioMessage != nil {
		waveform = message.AudioMessage.Waveform
	}

	record := MockSentMessage{
		Phone:        to.User,
		Waveform:     waveform,
		CreationTime: time.Now().UTC(),
	}

	path := os.Getenv("MOCK_SENT_MESSAGES_PATH")
	if path == "" {
		path = "/tmp/mock_sent_messages.json"
	}

	var records []MockSentMessage
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &records)
	}
	records = append(records, record)
	if data, err := json.MarshalIndent(records, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}

	return whatsmeow.SendResponse{
		ID:        "MOCK_MSG_ID_" + fmt.Sprintf("%d", time.Now().UnixNano()),
		Timestamp: time.Now(),
	}, nil
}

func (m *MockWhatsAppClient) AddEventHandler(handler whatsmeow.EventHandler) uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventHandlers = append(m.eventHandlers, handler)
	return uint32(len(m.eventHandlers))
}

func (m *MockWhatsAppClient) RemoveEventHandler(id uint32) {
	// Not implemented for mock
}

func (m *MockWhatsAppClient) SimulatePairing() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loggedIn = true
	m.connected = true

	_ = os.WriteFile(m.dbPath, []byte("paired"), 0o644)

	if m.stateMgr != nil {
		m.stateMgr.UpdateConnection(m.phone, func(s *WAConnectionState) {
			s.Status = StatusConnected
			s.WhatsAppConnected = true
			s.QRCodeData = ""
		})
	}

	for _, h := range m.eventHandlers {
		h(&events.Connected{})
	}
}

// ExtractWaveform decodes an OGG/Opus audio file using ffmpeg to mono 16-bit PCM,
// downsamples it to 64 buckets, normalizes the peaks, and returns a 64-byte slice
// representing the audio waveform for WhatsApp.
func ExtractWaveform(audioPath string) ([]byte, error) {
	zeroSlice := make([]byte, 64)
	ffmpegPath, err := ffmpegBinaryPath()
	if err != nil {
		return zeroSlice, fmt.Errorf("ffmpeg not found: %w", err)
	}

	cmd := exec.Command(ffmpegPath, "-i", audioPath, "-f", "s16le", "-ac", "1", "-ar", "8000", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return zeroSlice, fmt.Errorf("ffmpeg failed: %w (stderr: %s)", err, stderr.String())
	}

	pcmBytes := stdout.Bytes()
	sampleCount := len(pcmBytes) / 2
	if sampleCount == 0 {
		return zeroSlice, nil
	}

	samples := make([]int16, sampleCount)
	for i := 0; i < sampleCount; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(pcmBytes[i*2 : i*2+2]))
	}

	numBuckets := 64
	bucketSize := sampleCount / numBuckets
	if bucketSize == 0 {
		bucketSize = 1
	}

	peaks := make([]int16, numBuckets)
	maxPeak := int16(0)

	for i := 0; i < numBuckets; i++ {
		start := i * bucketSize
		end := start + bucketSize
		if end > sampleCount {
			end = sampleCount
		}

		maxVal := int16(0)
		for j := start; j < end; j++ {
			val := samples[j]
			if val < 0 {
				if val == -32768 {
					val = 32767
				} else {
					val = -val
				}
			}
			if val > maxVal {
				maxVal = val
			}
		}
		peaks[i] = maxVal
		if maxVal > maxPeak {
			maxPeak = maxVal
		}
	}

	if maxPeak == 0 {
		return zeroSlice, nil
	}

	waveform := make([]byte, numBuckets)
	for i, peak := range peaks {
		waveform[i] = byte(math.Round(float64(peak) / float64(maxPeak) * 255.0))
	}

	return waveform, nil
}
