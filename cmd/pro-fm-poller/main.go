package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"pro-fm-poller/pkg/poller"
)

const apiURL = "https://api.profm.ro/api/v1/radios/article/2918?appVersion=1.0.0&platform=android"

var activeCampaigns = []poller.Campaign{
	{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
	{StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana"},
	{StartDate: "10-08-2026", EndDate: "21-08-2026", Artist: "The Weeknd"},
}

func main() {
	// 1. Env Vars
	targetPhone := os.Getenv("TARGET_PHONE")
	if targetPhone == "" {
		targetPhone = "+40770661491"
	}
	dbPath := os.Getenv("WAPP_DB_PATH")
	if dbPath == "" {
		dbPath = "/data/wapp.sqlite"
	}
	sendgridKey := os.Getenv("SENDGRID_API_KEY")
	adminPass := os.Getenv("ADMIN_PASSWORD")
	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		if appName := os.Getenv("FLY_APP_NAME"); appName != "" {
			baseURL = fmt.Sprintf("https://%s.fly.dev", appName)
		} else {
			baseURL = "http://localhost:8080"
		}
	}

	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	telegramChatID := os.Getenv("TELEGRAM_CHAT_ID")

	audiosDir := os.Getenv("AUDIOS_DIR")
	if audiosDir == "" {
		audiosDir = "/data/audios"
	}

	// 2. Initialize Audio Pool
	if err := poller.InitAudioPool(audiosDir); err != nil {
		log.Fatalf("Failed to init audio pool: %v", err)
	}

	// 3. Initialize SQLite DB for auth
	dbMgr, err := poller.NewDBManager(dbPath)
	if err != nil {
		log.Fatalf("Failed to init DB Manager: %v", err)
	}

	// 4. Initialize State Manager and SSE Broadcaster
	stateMgr := poller.NewStateManager()
	sseBroadcaster := poller.NewSSEBroadcaster()

	// 4.5 Initialize SSE Logger
	logWriter := poller.NewSSELogWriter(os.Stdout, sseBroadcaster)
	log.SetOutput(logWriter)

	// 5. Initialize Alerters
	tgAlerter := poller.NewTelegramAlerter(telegramToken, telegramChatID)
	emailFrom := os.Getenv("EMAIL_FROM")
	if emailFrom == "" {
		emailFrom = "notifications@yourdomain.com"
	}
	emAlerter := poller.NewEmailAlerter(sendgridKey, emailFrom, "/data/trusted-emails.txt")
	alerter := poller.NewMultiAlerter(tgAlerter, emAlerter)

	// 6. Initialize Auth Manager
	authMgr := poller.NewAuthManager(dbMgr, sendgridKey, emailFrom, adminPass, baseURL)

	// 7. Start the SSE Broadcaster Bridge
	go func() {
		sub := stateMgr.Subscribe()
		for state := range sub {
			// Broadcast Status
			var statusBuf bytes.Buffer
			_ = poller.StatusComponent(state).Render(context.Background(), &statusBuf)
			sseBroadcaster.Broadcast("status", statusBuf.Bytes())

			// Broadcast Song
			var songBuf bytes.Buffer
			_ = poller.SongComponent(state.CurrentSong).Render(context.Background(), &songBuf)
			sseBroadcaster.Broadcast("song", songBuf.Bytes())

			// Broadcast Audio Stats
			var audioBuf bytes.Buffer
			_ = poller.AudioStatsComponent(state.UnusedAudios, state.UsedAudios).Render(context.Background(), &audioBuf)
			sseBroadcaster.Broadcast("audio", audioBuf.Bytes())

			// Broadcast QR Code
			var qrBuf bytes.Buffer
			_ = poller.QRComponent(state.QRCodeData).Render(context.Background(), &qrBuf)
			sseBroadcaster.Broadcast("qrcode", qrBuf.Bytes())
		}
	}()

	// 8. Start Web Dashboard (Telemetry Server)
	telemetryServer := poller.NewTelemetryServer(authMgr, stateMgr, sseBroadcaster, logWriter)
	go func() {
		fmt.Println("🚀 Telemetry UI available at", baseURL)
		if err := telemetryServer.Start("0.0.0.0:8080"); err != nil {
			log.Fatalf("Failed to start telemetry server: %v", err)
		}
	}()

	// 9. Initialize WhatsApp Client
	fmt.Println("Initializing WhatsApp client...")
	wappClient, err := poller.InitWhatsApp(dbPath, stateMgr, alerter, baseURL)
	if err != nil {
		log.Fatalf("Failed to initialize WhatsApp: %v", err)
	}
	defer wappClient.Disconnect()

	// 10. Start Poller
	p := &poller.Poller{
		APIURL:          apiURL,
		PollInterval:    2 * time.Second,
		ActiveCampaigns: activeCampaigns,
		TargetPhone:     targetPhone,
		StateMgr:        stateMgr,
		Alerter:         alerter,
		AudiosDir:       audiosDir,
		SendVoiceNote: func(phone string, audioPath string) error {
			return poller.SendVoiceNote(wappClient, phone, audioPath)
		},
	}
	p.Start()
}

/// coaie de ce naiba nu vad aasta in git tracking?
