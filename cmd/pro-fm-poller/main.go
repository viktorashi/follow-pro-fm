package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"pro-fm-poller/pkg/poller"
)

const apiURL = "https://api.profm.ro/api/v1/radios/article/2918?appVersion=1.0.0&platform=android"

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
	appDBPath := os.Getenv("APP_DB_PATH")
	if appDBPath == "" {
		appDBPath = "/data/app.sqlite"
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

	// 3. Initialize SQLite DB for auth and app state
	dbMgr, err := poller.NewDBManager(appDBPath)
	if err != nil {
		log.Fatalf("Failed to init DB Manager: %v", err)
	}

	// 4. Initialize State Manager and SSE Broadcaster
	stateMgr := poller.NewStateManager()

	// Load Kill Switch state
	isKilled, err := dbMgr.IsKillSwitchActive(context.Background())
	if err == nil && isKilled {
		stateMgr.Update(func(s *poller.AppState) {
			s.KillSwitchActive = true
			s.Status = poller.StatusKilled
		})
	}

	sseBroadcaster := poller.NewSSEBroadcaster()

	// 4.5 Initialize SSE Logger
	logWriter := poller.NewSSELogWriter(os.Stdout, sseBroadcaster)
	log.SetOutput(logWriter)

	envName := os.Getenv("ENVIRONMENT")
	if envName == "" {
		envName = "prod"
	}

	// 5. Initialize Alerters
	tgAlerter := poller.NewTelegramAlerter(telegramToken, telegramChatID, envName)
	emailFrom := os.Getenv("EMAIL_FROM")
	if emailFrom == "" {
		emailFrom = "notifications@yourdomain.com"
	}
	emAlerter := poller.NewEmailAlerter(sendgridKey, emailFrom, "/data/trusted-emails.txt", envName)
	alerter := poller.NewMultiAlerter(tgAlerter, emAlerter)

	// 6. Initialize Auth Manager
	authMgr := poller.NewAuthManager(dbMgr, sendgridKey, emailFrom, adminPass, baseURL)

	// 7. Start the SSE Broadcaster Bridge
	go func() {
		sub := stateMgr.Subscribe()
		var lastState poller.AppState
		for state := range sub {
			// Broadcast Status
			if state.Status != lastState.Status || state.LastError != lastState.LastError || state.WhatsAppConnected != lastState.WhatsAppConnected {
				var statusBuf bytes.Buffer
				_ = poller.StatusComponent(state).Render(context.Background(), &statusBuf)
				sseBroadcaster.Broadcast("status", statusBuf.Bytes())
			}

			// Broadcast Song
			if state.CurrentSong != lastState.CurrentSong {
				var songBuf bytes.Buffer
				_ = poller.SongComponent(state.CurrentSong).Render(context.Background(), &songBuf)
				sseBroadcaster.Broadcast("song", songBuf.Bytes())
			}

			// Broadcast Audio Stats
			if state.UnusedAudios != lastState.UnusedAudios || state.UsedAudios != lastState.UsedAudios {
				var audioBuf bytes.Buffer
				_ = poller.AudioStatsComponent(state.UnusedAudios, state.UsedAudios).Render(context.Background(), &audioBuf)
				sseBroadcaster.Broadcast("audio", audioBuf.Bytes())
			}

			// Broadcast QR Code
			if state.QRCodeData != lastState.QRCodeData || state.Status != lastState.Status {
				var qrBuf bytes.Buffer
				_ = poller.QRComponent(state.QRCodeData).Render(context.Background(), &qrBuf)
				sseBroadcaster.Broadcast("qrcode", qrBuf.Bytes())
			}

			lastState = state
		}
	}()

	// 8. Start Web Dashboard (Telemetry Server)
	telemetryServer := poller.NewTelemetryServer(authMgr, stateMgr, sseBroadcaster, logWriter, dbMgr, filepath.Dir(dbPath))
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

	// Load campaigns from campaigns.json
	var activeCampaigns []poller.Campaign
	if campaignsData, err := os.ReadFile("campaigns.json"); err == nil {
		if err := json.Unmarshal(campaignsData, &activeCampaigns); err != nil {
			log.Printf("⚠️ Failed to parse campaigns.json: %v", err)
		}
	} else {
		log.Printf("⚠️ Failed to read campaigns.json: %v", err)
	}

	// 10. Start Poller
	p := &poller.Poller{
		APIURL:          apiURL,
		PollInterval:    2 * time.Second,
		ActiveCampaigns: activeCampaigns,
		TargetPhone:     targetPhone,
		StateMgr:        stateMgr,
		Alerter:         alerter,
		AudiosDir:       audiosDir,
		DBMgr:           dbMgr,
		BaseURL:         baseURL,
		SendVoiceNote: func(phone string, audioPath string) error {
			return poller.SendVoiceNote(wappClient, phone, audioPath)
		},
	}
	p.Start()
}

/// coaie de ce naiba nu vad aasta in git tracking?
