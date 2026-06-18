package poller

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

func init() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		_, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;", nil)
		return err
	})
}

// InitWhatsApp initializes the WhatsApp client and handles connection/pairing
func InitWhatsApp(dbPath string, stateMgr *StateManager, alerter Alerter, baseURL string) (*whatsmeow.Client, error) {
	dbLog := waLog.Stdout("Database", "WARN", true)
	// Open connection to sqlite database using pure Go driver
	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+dbPath+"?_foreign_keys=on", dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get first device: %w", err)
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	client := whatsmeow.NewClient(deviceStore, clientLog)

	// Add event handlers to ensure we're processing E2E and presence
	client.AddEventHandler(func(evt interface{}) {
		switch evt.(type) {
		case *events.Connected:
			// Tell WhatsApp servers we are online.
			// Crucial for E2E prekey setups and for avoiding "Waiting for this message".
			_ = client.SendPresence(context.Background(), types.PresenceAvailable)
		case *events.OfflineSyncCompleted:
			// You could log or wait on this specifically, but PresenceAvailable is usually enough.
		case *events.LoggedOut:
			if stateMgr != nil {
				stateMgr.Update(func(s *AppState) {
					s.Status = StatusPairingRequired
					s.WhatsAppConnected = false
				})
			}
		}
	})

	// Set a realistic device name
	store.DeviceProps.Os = proto.String("Mac OS")

	// Run connection logic asynchronously so we don't block the telemetry server
	// and so we can retry on network failures.
	go func() {
		for {
			if client.Store.ID == nil {
				// No session exists, perform login
				qrChan, _ := client.GetQRChannel(context.Background())
				err = client.Connect()
				if err != nil {
					if stateMgr != nil {
						stateMgr.Update(func(s *AppState) {
							s.Status = StatusError
							s.WhatsAppConnected = false
						})
					}
					fmt.Printf("❌ Failed to connect for pairing (retrying in %s): %v\n", ConnectionRetryDelay, err)
					time.Sleep(ConnectionRetryDelay)
					continue
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
							stateMgr.Update(func(s *AppState) {
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
								stateMgr.Update(func(s *AppState) {
									s.Status = StatusConnected
									s.QRCodeData = ""
									s.WhatsAppConnected = true
								})
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
					fmt.Println("❌ Login timed out or failed, retrying...")
					client.Disconnect()
					time.Sleep(ConnectionRetryDelay)
					continue
				}

				for i := 0; i < ConnectionRetryAttempts; i++ {
					if client.IsLoggedIn() && client.IsConnected() {
						break
					}
					time.Sleep(500 * time.Millisecond)
				}
				break // Successfully paired and connected
			} else {
				// Session exists, connect automatically
				err := client.Connect()
				if err != nil {
					if stateMgr != nil {
						stateMgr.Update(func(s *AppState) {
							s.Status = StatusError
							s.WhatsAppConnected = false
						})
					}
					fmt.Printf("❌ Failed to connect (retrying in %s): %v\n", ConnectionRetryDelay, err)
					time.Sleep(ConnectionRetryDelay)
					continue
				}

				if stateMgr != nil {
					stateMgr.Update(func(s *AppState) {
						s.Status = StatusConnected
						s.WhatsAppConnected = true
					})
				}
				break // Successfully connected
			}
		}
	}()

	return client, nil
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

func SendVoiceNote(client *whatsmeow.Client, phone string, audioPath string) error {
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

	// 1. Remux the OGG file to inject current creation_time and guarantee a unique SHA256 hash
	// so WhatsApp doesn't deduplicate it and instead shows it as recorded right now.
	now := time.Now().UTC().Format(time.RFC3339)
	tmpPath := fmt.Sprintf("%s.tmp.ogg", audioPath)

	cmd := exec.Command("ffmpeg", "-y", "-i", audioPath, "-c", "copy", "-metadata", "creation_time="+now, tmpPath)
	if err := cmd.Run(); err != nil {
		fmt.Printf("   ⚠️ Failed to inject metadata with ffmpeg, falling back to original: %v\n", err)
		tmpPath = audioPath
	} else {
		defer func() { _ = os.Remove(tmpPath) }()
	}

	// Read audio file
	audioData, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to read audio file at %s: %w", audioPath, err)
	}

	// Get exact duration of the audio (falls back to heuristic if not supported)
	duration, err := GetAudioDuration(audioPath)
	if err != nil {
		fmt.Printf("   ⚠️ Failed to get audio duration for %s: %v\n", audioPath, err)
		// Fallback size heuristic if duration extraction totally failed
		duration = time.Duration(len(audioData)/2500) * time.Second
	}

	estimatedSeconds := uint32(duration.Seconds())
	if estimatedSeconds < 1 {
		estimatedSeconds = 1
	} else if estimatedSeconds > 30 {
		estimatedSeconds = 30
	}

	// 1. Send "recording audio" state to make it look authentic
	_ = client.SendChatPresence(context.Background(), targetJID, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)

	// 2. Sleep for the duration of the audio to simulate recording time
	time.Sleep(time.Duration(estimatedSeconds) * time.Second)

	// 3. Clear recording state (optional but good practice)
	_ = client.SendChatPresence(context.Background(), targetJID, types.ChatPresencePaused, types.ChatPresenceMediaAudio)

	// Upload to WhatsApp servers
	var uploaded whatsmeow.UploadResponse
	var uploadErr error
	for i := 0; i < 3; i++ {
		uploaded, uploadErr = client.Upload(context.Background(), audioData, whatsmeow.MediaAudio)
		if uploadErr == nil {
			break
		}
		fmt.Printf("   ⚠️ Upload attempt %d failed: %v. Retrying in 2s...\n", i+1, uploadErr)
		time.Sleep(2 * time.Second)
	}
	if uploadErr != nil {
		return fmt.Errorf("failed to upload audio to WhatsApp after retries: %w", uploadErr)
	}

	// Construct AudioMessage with Push-To-Talk set to true (native voice note bubble)
	msg := &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(audioData))),
			PTT:           proto.Bool(true), // Makes it a native voice note
			Seconds:       proto.Uint32(estimatedSeconds),
		},
	}

	// Send message
	var resp whatsmeow.SendResponse
	var sendErr error
	for i := 0; i < 3; i++ {
		resp, sendErr = client.SendMessage(context.Background(), targetJID, msg)
		if sendErr == nil {
			break
		}
		fmt.Printf("   ⚠️ Send message attempt %d failed: %v. Retrying in 2s...\n", i+1, sendErr)
		time.Sleep(2 * time.Second)
	}
	if sendErr != nil {
		return fmt.Errorf("failed to send message to %s after retries: %w", targetJID, sendErr)
	}

	fmt.Printf("   ✅ Voice note sent! JID: %s, Message ID: %s, Timestamp: %s\n", targetJID, resp.ID, resp.Timestamp)
	return nil
}
