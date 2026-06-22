package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pro-fm-poller/pkg/poller"
)

func main() {
	// 1. Env Vars
	profmAPIURL := os.Getenv("PROFM_API_URL")
	if profmAPIURL == "" {
		profmAPIURL = "https://api.profm.ro/api/v1/radios/article/2918?appVersion=1.0.0&platform=android"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
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
			baseURL = "http://localhost:" + port
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
	if err := poller.ReconcileAudioUsage(audiosDir, dbMgr); err != nil {
		log.Fatalf("Failed to reconcile historical audio usage: %v", err)
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
		envName = "production"
	}

	// 5. Initialize Alerters
	tgAlerter := poller.NewTelegramAlerter(telegramToken, telegramChatID, envName, baseURL)
	emailFrom := os.Getenv("EMAIL_FROM")
	if emailFrom == "" {
		emailFrom = "notifications@yourdomain.com"
	}
	emAlerter := poller.NewEmailAlerter(sendgridKey, emailFrom, "/data/trusted-emails.txt", envName, baseURL)
	alerter := poller.NewMultiAlerter(tgAlerter, emAlerter)

	// 6. Initialize Auth Manager
	authMgr := poller.NewAuthManager(dbMgr, sendgridKey, emailFrom, adminPass, baseURL)

	// 7. Start the SSE Broadcaster Bridge
	go func() {
		sub := stateMgr.Subscribe()
		var lastState poller.AppState
		for state := range sub {
			// Broadcast Status
			connsStr := fmt.Sprintf("%v", state.Connections)
			lastConnsStr := fmt.Sprintf("%v", lastState.Connections)
			if state.Status != lastState.Status || state.LastError != lastState.LastError || connsStr != lastConnsStr {
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
			if connsStr != lastConnsStr || state.Status != lastState.Status {
				var qrBuf bytes.Buffer
				_ = poller.QRComponent(state.Connections).Render(context.Background(), &qrBuf)
				sseBroadcaster.Broadcast("qrcode", qrBuf.Bytes())
			}

			lastState = state
		}
	}()

	// 8. Start Web Dashboard (Telemetry Server)
	telemetryServer := poller.NewTelemetryServer(authMgr, stateMgr, sseBroadcaster, logWriter, dbMgr, filepath.Dir(dbPath), audiosDir)
	go func() {
		fmt.Println("🚀 Telemetry UI available at", baseURL)
		if err := telemetryServer.Start("0.0.0.0:" + port); err != nil {
			log.Fatalf("Failed to start telemetry server: %v", err)
		}
	}()

	// 9. Initialize WhatsApp Clients
	fmt.Println("Initializing WhatsApp clients...")

	wappClients := make(map[string]poller.WhatsAppClient)
	var wappMutex sync.RWMutex

	addSenderPhone := func(p string) error {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil
		}
		if !strings.HasPrefix(p, "+") {
			p = "+" + p
		}

		wappMutex.RLock()
		if _, exists := wappClients[p]; exists {
			wappMutex.RUnlock()
			return nil // Already added
		}
		wappMutex.RUnlock()

		normalized := poller.NormalizePhone(p)
		dbForPhone := filepath.Join(filepath.Dir(dbPath), "wapp_"+normalized+".sqlite")
		if normalized == poller.CanonicalSenderPhoneNormalized {
			dbForPhone = dbPath
		}

		stateMgr.Update(func(s *poller.AppState) {
			found := false
			for i := range s.Connections {
				if s.Connections[i].Phone == p {
					found = true
					break
				}
			}
			if !found {
				s.Connections = append(s.Connections, poller.WAConnectionState{
					Phone:  p,
					Status: poller.StatusInitializing,
				})
			}
		})

		c, err := poller.InitWhatsApp(p, dbForPhone, stateMgr, alerter, baseURL)
		if err != nil {
			return fmt.Errorf("failed to initialize WhatsApp for %s: %v", p, err)
		}

		wappMutex.Lock()
		wappClients[p] = c

		var wappClientsSlice []poller.WhatsAppClient
		for _, client := range wappClients {
			wappClientsSlice = append(wappClientsSlice, client)
		}
		telemetryServer.SetWhatsAppClients(wappClientsSlice)
		wappMutex.Unlock()

		return nil
	}

	telemetryServer.SetOnAddPhone(addSenderPhone)

	// Scan existing db files
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(dbPath), "wapp_*.sqlite"))
	for _, m := range matches {
		name := filepath.Base(m)
		phone := strings.TrimPrefix(name, "wapp_")
		phone = strings.TrimSuffix(phone, ".sqlite")
		if phone != "" {
			if err := addSenderPhone("+" + phone); err != nil {
				log.Printf("Error adding phone %s: %v", phone, err)
			}
		}
	}

	// Default phone if none found so a fresh environment can still pair or mock-pair.
	wappMutex.RLock()
	clientsCount := len(wappClients)
	wappMutex.RUnlock()
	if clientsCount == 0 {
		if err := addSenderPhone(poller.CanonicalSenderPhone); err != nil {
			log.Printf("Error adding default phone: %v", err)
		}
	}

	// Load campaigns in memory
	activeCampaigns := []poller.Campaign{
		{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		{StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana"},
		{StartDate: "10-08-2026", EndDate: "21-08-2026", Artist: "The Weeknd"},
	}

	if err := poller.InitRNGSchedule(dbMgr, activeCampaigns); err != nil {
		log.Fatalf("Failed to initialize RNG schedule: %v", err)
	}

	// Initialize Circular Audio Buffer for R3
	streamURL := os.Getenv("PROFM_STREAM_URL")
	if streamURL == "" {
		streamURL = "http://edge76.rcs-rds.ro:84/profm/profm.mp3"
	}
	// 3 minutes at 128kbps is ~2.88MB, we use 8MB buffer
	audioBuffer := poller.NewCircularAudioBuffer(streamURL, 8*1024*1024)
	audioBuffer.Start()
	defer audioBuffer.Stop()

	// 10. Start Poller
	p := &poller.Poller{
		APIURL:          profmAPIURL,
		PollInterval:    2 * time.Second,
		ActiveCampaigns: activeCampaigns,
		TargetPhone:     targetPhone,
		StateMgr:        stateMgr,
		Alerter:         alerter,
		AudiosDir:       audiosDir,
		SignaturesDir:   filepath.Join(filepath.Dir(audiosDir), "signatures"),
		AudioBuffer:     audioBuffer,
		DBMgr:           dbMgr,
		BaseURL:         baseURL,
		SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
			wappMutex.RLock()
			c, ok := wappClients[senderPhone]
			wappMutex.RUnlock()

			if !ok {
				return fmt.Errorf("client for sender phone %s not found", senderPhone)
			}
			if c.IsConnected() && c.IsLoggedIn() {
				return poller.SendVoiceNote(c, targetPhone, audioPath)
			}
			return fmt.Errorf("sender phone %s is not connected or logged in", senderPhone)
		},
		DisconnectWhatsApp: func() {
			wappMutex.RLock()
			defer wappMutex.RUnlock()
			for _, c := range wappClients {
				c.Disconnect()
			}
		},
		ConnectWhatsApp: func() error {
			wappMutex.RLock()
			defer wappMutex.RUnlock()
			for _, c := range wappClients {
				err := c.Connect()
				if err != nil {
					return err
				}
			}
			return nil
		},
	}
	p.Start()
}

/// coaie de ce naiba nu vad aasta in git tracking?
