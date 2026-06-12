package poller

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	"modernc.org/sqlite"
)

func init() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		_, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON;", nil)
		return err
	})
}

// InitWhatsApp initializes the WhatsApp client and handles connection/pairing
func InitWhatsApp(dbPath string) (*whatsmeow.Client, error) {
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

	if client.Store.ID == nil {
		// No session exists, perform login
		qrChan, _ := client.GetQRChannel(context.Background())
		err = client.Connect()
		if err != nil {
			return nil, fmt.Errorf("failed to connect for pairing: %w", err)
		}

		fmt.Println("\n👉 Please scan the QR code below using your WhatsApp Business/personal app (Settings -> Linked Devices -> Link a Device):")
		paired := false
		for evt := range qrChan {
			if evt.Event == "code" {
				// Clear screen and reset cursor to override previous QR code
				fmt.Print("\033[H\033[2J")
				fmt.Println("\n👉 Please scan the QR code below using your WhatsApp Business/personal app (Settings -> Linked Devices -> Link a Device):")
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else {
				// Clear the QR code from the screen for other events
				fmt.Print("\033[H\033[2J")
				switch evt.Event {
				case "success":
					fmt.Println("✅ Successfully paired!")
					paired = true
				case "timeout":
					fmt.Println("⏳ QR code scan timed out. Please run the program again.")
				case "error":
					fmt.Printf("❌ Pairing error: %v\n", evt.Error)
				default:
					fmt.Printf("ℹ️ Login event: %s\n", evt.Event)
				}
			}
		}

		if !paired {
			return nil, fmt.Errorf("login timed out or failed")
		}
	} else {
		// Session exists, connect automatically
		err := client.Connect()
		if err != nil {
			return nil, fmt.Errorf("failed to connect: %w", err)
		}
	}

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
