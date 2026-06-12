package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	"modernc.org/sqlite"
)

const apiUrl = "https://api.profm.ro/api/v1/radios/article/2918?appVersion=1.0.0&platform=android"

type EPGData struct {
	Data struct {
		Epg struct {
			Title    string `json:"playerExtendedSongTitle"`
			Subtitle string `json:"playerExtendedSongSubtitle"`
		} `json:"epg"`
	} `json:"data"`
}

type SongInfo struct {
	Artist string
	Title  string
}

// Campaign represents a multi-week date period where a specific artist is targeted
type Campaign struct {
	StartDate string // Format: "02-01-2006"
	EndDate   string // Format: "02-01-2006"
	Artist    string
}

// IsActive checks if the current time falls within the campaign date period
// and satisfies the global daily rules: Mon-Fri, 07:00-20:00.
func (c Campaign) IsActive(now time.Time) bool {
	// Global Rule 1: Monday to Friday only
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false
	}

	// Global Rule 2: Between 07:00 and 20:00 (up to 19:59:59)
	if now.Hour() < 7 || now.Hour() >= 20 {
		return false
	}

	layout := "02-01-2006"
	start, err1 := time.ParseInLocation(layout, c.StartDate, now.Location())
	end, err2 := time.ParseInLocation(layout, c.EndDate, now.Location())

	if err1 != nil || err2 != nil {
		return false
	}

	// End date includes the entire day
	end = end.Add(24*time.Hour - time.Second)

	return now.After(start) && now.Before(end)
}

// Define the campaigns as requested (2-week periods)
var activeCampaigns = []Campaign{
	{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
	{StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana"},
	{StartDate: "10-08-2026", EndDate: "21-08-2026", Artist: "The Weeknd"},
}

var (
	matchesToday int
	lastCheckDay int
	wappClient   *whatsmeow.Client
	targetPhone  string
)

func getNowPlaying() (SongInfo, error) {
	req, err := http.NewRequest("GET", apiUrl, nil)
	if err != nil {
		return SongInfo{}, err
	}

	// Be a good citizen with the user agent
	req.Header.Set("User-Agent", "ProFMNowPlayingGoClient/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return SongInfo{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return SongInfo{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SongInfo{}, err
	}

	var data EPGData
	if err := json.Unmarshal(body, &data); err != nil {
		return SongInfo{}, err
	}

	artist := data.Data.Epg.Title
	if artist == "" {
		artist = "Unknown Artist"
	}

	song := data.Data.Epg.Subtitle
	if song == "" {
		song = "Unknown Song"
	}

	// Clean up "2000 - LASA-MA PAPA LA MARE"
	if strings.Contains(song, " - ") {
		parts := strings.SplitN(song, " - ", 2)
		// Check if the first part is purely digits (a year)
		isYear := true
		for _, ch := range parts[0] {
			if ch < '0' || ch > '9' {
				isYear = false
				break
			}
		}
		if isYear {
			song = parts[1]
		}
	}

	return SongInfo{Artist: artist, Title: song}, nil
}

func main() {
	// Initialize target phone from environment variable
	targetPhone = os.Getenv("TARGET_PHONE")
	if targetPhone == "" {
		targetPhone = "+40762631673"
	}
	fmt.Printf("Destination phone number set to: %s\n", targetPhone)

	// Initialize WhatsApp client
	fmt.Println("Initializing WhatsApp client...")
	var err error
	wappClient, err = initWhatsApp()
	if err != nil {
		log.Fatalf("Failed to initialize WhatsApp: %v", err)
	}
	defer wappClient.Disconnect()

	fmt.Println("Fetching Now Playing from Pro FM...")
	fmt.Println(strings.Repeat("-", 40))

	var currentSong SongInfo

	// Use a cron-like Ticker instead of an infinite sleep loop
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Trigger immediately on start
	checkSong(&currentSong)

	// Cron-like polling
	for {
		<-ticker.C
		checkSong(&currentSong)
	}
}

func checkSong(currentSong *SongInfo) {
	now := time.Now()

	// Reset daily matches counter on a new day
	if now.YearDay() != lastCheckDay {
		matchesToday = 0
		lastCheckDay = now.YearDay()
	}

	song, err := getNowPlaying()
	if err != nil {
		log.Printf("Error fetching data: %v\n", err)
		return
	}

	if song != *currentSong {
		fmt.Printf("[%s] %s - %s\n", now.Format("15:04:05"), song.Artist, song.Title)

		// Only check campaigns if we haven't hit the daily limit of 6
		if matchesToday < 6 {
			for _, campaign := range activeCampaigns {
				if campaign.IsActive(now) {
					if strings.Contains(strings.ToLower(song.Artist), strings.ToLower(campaign.Artist)) {
						matchesToday++
						fmt.Printf("   🎉 [CAMPAIGN ALERT] %s is playing! (Match %d/6 for today)\n", song.Artist, matchesToday)

						// Trigger actual submission (WhatsApp Voice note)
						fmt.Println("   Sending WhatsApp voice note...")
						err := sendVoiceNote(wappClient, targetPhone, "audios/1.ogg")
						if err != nil {
							log.Printf("   ❌ Error sending voice note: %v\n", err)
						}
					}
				}
			}
		} else {
			// Optional: Print a debug message once that the daily quota is reached
			// fmt.Println("   [INFO] Daily limit of 6 matches reached. Ignoring further campaign matches for today.")
		}

		*currentSong = song
	}
}

func init() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		_, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON;", nil)
		return err
	})
}

// initWhatsApp initializes the WhatsApp client and handles connection/pairing
func initWhatsApp() (*whatsmeow.Client, error) {
	dbLog := waLog.Stdout("Database", "WARN", true)
	// Open connection to sqlite database using pure Go driver
	container, err := sqlstore.New(context.Background(), "sqlite", "file:wapp.sqlite?_foreign_keys=on", dbLog)
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
		for evt := range qrChan {
			if evt.Event == "code" {
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			} else {
				fmt.Println("Login event:", evt.Event)
			}
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

// sendVoiceNote reads the ogg file, uploads it, and sends it as a PTT message (recorded voice note)
func sendVoiceNote(client *whatsmeow.Client, phone string, audioPath string) error {
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
			Seconds:       proto.Uint32(9),    // Approx duration for 1.ogg
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
