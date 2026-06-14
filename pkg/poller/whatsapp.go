package poller

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
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
		_, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON;", nil)
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
							_ = alerter.AlertCritical(fmt.Sprintf(
								"WhatsApp disconnected! Action required immediately.<br><br>Scan the QR below or click here: <a href='%s'>Live Dashboard</a><br><br><img src='%s/qr.png?t=%d'/>",
								baseURL, baseURL, time.Now().Unix(),
							))
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

// SendVoiceNote reads the ogg file, uploads it, and sends it as a PTT message (recorded voice note)
func SendVoiceNote(client *whatsmeow.Client, phone string, audioPath string) error {
	normalized := normalizePhoneNumber(phone)
	targetJID := types.NewJID(normalized, types.DefaultUserServer)

	// Read audio file
	audioData, err := os.ReadFile(audioPath)
	if err != nil {
		return fmt.Errorf("failed to read audio file at %s: %w", audioPath, err)
	}

	// Upload to WhatsApp servers
	uploaded, err := client.Upload(context.Background(), audioData, whatsmeow.MediaAudio)
	if err != nil {
		return fmt.Errorf("failed to upload audio to WhatsApp: %w", err)
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
			Seconds:       proto.Uint32(9),  // Approx duration for 1.ogg
		},
	}

	// Send message
	resp, err := client.SendMessage(context.Background(), targetJID, msg)
	if err != nil {
		return fmt.Errorf("failed to send message to %s: %w", targetJID, err)
	}

	fmt.Printf("   ✅ Voice note sent! JID: %s, Message ID: %s, Timestamp: %s\n", targetJID, resp.ID, resp.Timestamp)
	return nil
}
