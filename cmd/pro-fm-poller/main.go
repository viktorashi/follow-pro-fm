package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"pro-fm-poller/pkg/poller"

	"github.com/sendgrid/sendgrid-go"
)

type whatsAppInitFunc func(phone string, dbPath string, stateMgr *poller.StateManager, alerter poller.Alerter, baseURL string) (poller.WhatsAppClient, error)

func bootstrapSenderPhones(dbPath string) []string {
	var phones []string
	seen := make(map[string]struct{})

	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(dbPath), "wapp_*.sqlite"))
	sort.Strings(matches)
	for _, match := range matches {
		name := filepath.Base(match)
		phone := strings.TrimPrefix(name, "wapp_")
		phone = strings.TrimSuffix(phone, ".sqlite")
		if phone == "" || strings.HasPrefix(phone, "pairing_") {
			continue
		}

		normalized := "+" + phone
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		phones = append(phones, normalized)
	}

	return phones
}

func newPendingSessionFilename() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "wapp_pairing_" + hex.EncodeToString(b) + ".sqlite", nil
}

func ensureSenderConnectionState(stateMgr *poller.StateManager, phone string, personID *int64, personName, personSlug string) bool {
	added := false
	stateMgr.Update(func(s *poller.AppState) {
		for i := range s.Connections {
			if s.Connections[i].Phone == phone {
				if personID != nil {
					s.Connections[i].PersonID = personID
					s.Connections[i].PersonName = personName
					s.Connections[i].PersonSlug = personSlug
				}
				return
			}
		}
		s.Connections = append(s.Connections, poller.WAConnectionState{
			Phone:      phone,
			Status:     poller.StatusInitializing,
			PersonID:   personID,
			PersonName: personName,
			PersonSlug: personSlug,
		})
		added = true
	})
	return added
}

func removeSenderConnectionState(stateMgr *poller.StateManager, phone string) {
	stateMgr.Update(func(s *poller.AppState) {
		filtered := make([]poller.WAConnectionState, 0, len(s.Connections))
		for _, conn := range s.Connections {
			if conn.Phone != phone {
				filtered = append(filtered, conn)
			}
		}
		s.Connections = filtered
	})
}

func initSenderPhone(phone string, dbPath string, stateMgr *poller.StateManager, alerter poller.Alerter, baseURL string, personID *int64, personName, personSlug string, initWhatsApp whatsAppInitFunc) (poller.WhatsAppClient, error) {
	added := ensureSenderConnectionState(stateMgr, phone, personID, personName, personSlug)

	client, err := initWhatsApp(phone, dbPath, stateMgr, alerter, baseURL)
	if err != nil {
		if added {
			removeSenderConnectionState(stateMgr, phone)
		}
		return nil, fmt.Errorf("failed to initialize WhatsApp for %s: %v", phone, err)
	}

	return client, nil
}

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
	if err := dbMgr.AutoMigratePersons(context.Background(), audiosDir); err != nil {
		log.Printf("AutoMigratePersons warning: %v", err)
	}
	if err := poller.ReconcileAudioUsage(audiosDir, dbMgr); err != nil {
		log.Fatalf("Failed to reconcile historical audio usage: %v", err)
	}

	// 4. Initialize State Manager and SSE Broadcaster
	stateMgr := poller.NewStateManager()

	if persons, err := dbMgr.ListPersons(context.Background()); err == nil {
		stateMgr.Update(func(s *poller.AppState) {
			s.Persons = persons
		})
	}

	// Load Kill Switch state
	isKilled, err := dbMgr.IsKillSwitchActive(context.Background())
	if err == nil && isKilled {
		stateMgr.Update(func(s *poller.AppState) {
			s.KillSwitchActive = true
			s.Status = poller.StatusKilled
		})
	}
	gatheringEnabled, err := dbMgr.IsGatheringSignaturesEnabled(context.Background())
	if err == nil {
		stateMgr.Update(func(s *poller.AppState) {
			s.GatheringSignatures = gatheringEnabled
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
	tgAlerter := poller.NewTelegramAlerter(telegramToken, telegramChatID)
	emailFrom := os.Getenv("EMAIL_FROM")
	if emailFrom == "" {
		emailFrom = "notifications@yourdomain.com"
	}
	isProd := os.Getenv("FLY_APP_NAME") != "" || envName == "production" || envName == "prod"
	var emailClient poller.EmailSender
	if isProd {
		if sendgridKey != "" {
			emailClient = sendgrid.NewSendClient(sendgridKey)
		}
	} else {
		fmt.Println("🚀 Using local mailpit for emails. Watch emails at http://localhost:8025")
		emailHost := os.Getenv("MAILPIT_HOST")
		if emailHost == "" {
			emailHost = "localhost:1025" // Fallback to localhost if not set in docker-compose
		}
		emailClient = &poller.SMTPSender{Addr: emailHost}
	}

	emAlerter := poller.NewEmailAlerter(emailClient, emailFrom, poller.TrustedEmailsFilePath(appDBPath))
	dbAlerter := poller.NewDatabaseAlerter(dbMgr)
	alerter := poller.NewMultiAlerter(tgAlerter, emAlerter, dbAlerter)

	// 6. Initialize Auth Manager
	authMgr := poller.NewAuthManager(dbMgr, emailClient, emailFrom, adminPass, baseURL)

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
				_ = poller.AudioStatsComponent(state).Render(context.Background(), &audioBuf)
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

	// Load campaigns in memory
	activeCampaigns := poller.DefaultActiveCampaigns

	var transcribe func(context.Context, []byte) (string, error)

	transcriptionURL := os.Getenv("TRANSCRIPTION_URL")
	if transcriptionURL == "" {
		if isProd {
			transcriptionURL = "http://pro-fm-whisper.flycast/v1/audio/transcriptions"
		} else {
			transcriptionURL = "http://localhost:8000/v1/audio/transcriptions"
		}
	}
	transcribe = poller.NewHTTPTranscriber(transcriptionURL)
	streamingTranscriptionURL := os.Getenv("TRANSCRIPTION_WS_URL")
	if streamingTranscriptionURL == "" {
		streamingTranscriptionURL = websocketURL(transcriptionURL)
	}

	// 8. Start Web Dashboard (Telemetry Server)
	telemetryServer := poller.NewTelemetryServer(authMgr, stateMgr, sseBroadcaster, logWriter, dbMgr, filepath.Dir(dbPath), audiosDir, activeCampaigns, transcribe)
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

	addSenderPhone := func(p string, dbForPhone string, personID *int64, personName, personSlug string) error {
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

		if dbForPhone == "" {
			normalized := poller.NormalizePhone(p)
			dbForPhone = filepath.Join(filepath.Dir(dbPath), "wapp_"+normalized+".sqlite")
		}

		c, err := initSenderPhone(p, dbForPhone, stateMgr, alerter, baseURL, personID, personName, personSlug, func(phone string, dbPath string, stateMgr *poller.StateManager, alerter poller.Alerter, baseURL string) (poller.WhatsAppClient, error) {
			return poller.InitWhatsApp(phone, dbPath, stateMgr, alerter, baseURL)
		})
		if err != nil {
			return err
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

	startPairing := func(personID *int64) error {
		filename, err := newPendingSessionFilename()
		if err != nil {
			return err
		}
		pendingPhone := "New phone (scan QR)"
		dbForPairing := filepath.Join(filepath.Dir(dbPath), filename)

		var personName, personSlug string
		if personID != nil && dbMgr != nil {
			if p, err := dbMgr.GetPerson(context.Background(), *personID); err == nil && p != nil {
				personName = p.Name
				personSlug = p.Slug
			}
		}

		if !ensureSenderConnectionState(stateMgr, pendingPhone, personID, personName, personSlug) {
			return fmt.Errorf("a phone pairing is already in progress")
		}

		var client poller.WhatsAppClient
		client, err = poller.InitWhatsApp(pendingPhone, dbForPairing, stateMgr, alerter, baseURL, func(phone string) {
			phone = "+" + poller.NormalizePhone(phone)
			if phone == "+" {
				log.Printf("Paired WhatsApp account did not expose a phone number")
				return
			}
			if err := dbMgr.SetSenderSessionWithPerson(context.Background(), phone, filename, personID); err != nil {
				log.Printf("Failed to persist paired sender %s: %v", phone, err)
				return
			}

			wappMutex.Lock()
			defer wappMutex.Unlock()
			if existing, exists := wappClients[phone]; exists && existing != client {
				client.Disconnect()
				removeSenderConnectionState(stateMgr, pendingPhone)
				return
			}
			delete(wappClients, pendingPhone)
			wappClients[phone] = client
			stateMgr.ReplaceConnectionPhone(pendingPhone, phone)

			clients := make([]poller.WhatsAppClient, 0, len(wappClients))
			for _, connectedClient := range wappClients {
				clients = append(clients, connectedClient)
			}
			telemetryServer.SetWhatsAppClients(clients)
		})
		if err != nil {
			removeSenderConnectionState(stateMgr, pendingPhone)
			return err
		}

		wappMutex.Lock()
		wappClients[pendingPhone] = client
		clients := make([]poller.WhatsAppClient, 0, len(wappClients))
		for _, connectedClient := range wappClients {
			clients = append(clients, connectedClient)
		}
		telemetryServer.SetWhatsAppClients(clients)
		wappMutex.Unlock()
		return nil
	}

	telemetryServer.SetOnAddPhoneWithPerson(startPairing)
	telemetryServer.SetOnAddPhone(func() error {
		return startPairing(nil)
	})
	telemetryServer.SetOnDisconnectPhone(func(p string) error {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil
		}
		if !strings.HasPrefix(p, "+") && p != "New phone (scan QR)" {
			p = "+" + p
		}

		poller.CancelPairing(p)

		wappMutex.Lock()
		client, exists := wappClients[p]
		if exists {
			delete(wappClients, p)
			client.Disconnect()
		}

		var wappClientsSlice []poller.WhatsAppClient
		for _, connectedClient := range wappClients {
			wappClientsSlice = append(wappClientsSlice, connectedClient)
		}
		telemetryServer.SetWhatsAppClients(wappClientsSlice)
		wappMutex.Unlock()

		stateMgr.RemoveConnection(p)
		_ = dbMgr.RemoveSenderSession(context.Background(), p)

		log.Printf("🔌 Cleanly disconnected WhatsApp sender phone: %s", p)
		return nil
	})

	for _, phone := range bootstrapSenderPhones(dbPath) {
		if err := addSenderPhone(phone, "", nil, "", ""); err != nil {
			log.Printf("Error adding phone %s: %v", phone, err)
		}
	}
	if sessions, err := dbMgr.SenderSessions(context.Background()); err != nil {
		log.Printf("Error loading paired sender sessions: %v", err)
	} else {
		for _, session := range sessions {
			if err := addSenderPhone(session.Phone, filepath.Join(filepath.Dir(dbPath), session.DBFilename), session.PersonID, session.PersonName, session.PersonSlug); err != nil {
				log.Printf("Error restoring phone %s: %v", session.Phone, err)
			}
		}
	}

	if err := poller.InitRNGSchedule(dbMgr, activeCampaigns); err != nil {
		log.Fatalf("Failed to initialize RNG schedule: %v", err)
	}

	// Initialize Circular Audio Buffer for R3
	streamURL := os.Getenv("PROFM_STREAM_URL")
	if streamURL == "" {
		streamURL = "http://edge76.rcs-rds.ro:84/profm/profm.mp3"
	}
	// Keep roughly 3 minutes of MP3 pre-roll in memory for dashcam captures.
	audioBuffer := poller.NewCircularAudioBuffer(streamURL, 3*60*128000/8)
	defer audioBuffer.Stop()
	pcmInput, unsubscribePCM := audioBuffer.Subscribe(128)
	defer unsubscribePCM()
	var streamingTranscriber *poller.WebSocketTranscriber
	contestCheckCooldown := poller.DefaultContestCheckCooldown
	if configured := os.Getenv("CONTEST_CHECK_COOLDOWN"); configured != "" {
		parsed, err := time.ParseDuration(configured)
		if err != nil || parsed <= 0 {
			log.Printf("Invalid CONTEST_CHECK_COOLDOWN %q; using %s", configured, contestCheckCooldown)
		} else {
			contestCheckCooldown = parsed
		}
	}

	// 10. Start Poller
	p := &poller.Poller{
		APIURL:               profmAPIURL,
		PollInterval:         2 * time.Second,
		ActiveCampaigns:      activeCampaigns,
		TargetPhone:          targetPhone,
		StateMgr:             stateMgr,
		Alerter:              alerter,
		AudiosDir:            audiosDir,
		SignaturesDir:        filepath.Join(filepath.Dir(audiosDir), poller.DirSignatures),
		AudioBuffer:          audioBuffer,
		TranscriptionBuffer:  poller.NewTimeSeriesBuffer[string](10 * time.Minute),
		DBMgr:                dbMgr,
		BaseURL:              baseURL,
		ContestCheckCooldown: contestCheckCooldown,
		Transcribe:           transcribe,
		StreamingTranscriptionActive: func() bool {
			return streamingTranscriber != nil && streamingTranscriber.IsConnected()
		},
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
			if streamingTranscriber != nil {
				streamingTranscriber.SetEnabled(false)
			}
			wappMutex.RLock()
			defer wappMutex.RUnlock()
			for _, c := range wappClients {
				if c.IsLoggedIn() {
					c.Disconnect()
				}
			}
		},
		ConnectWhatsApp: func() error {
			if streamingTranscriber != nil {
				streamingTranscriber.SetEnabled(true)
			}
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
	streamingTranscriber = poller.NewWebSocketTranscriber(streamingTranscriptionURL, p.HandleStreamingTranscript)
	pcmConverter := poller.NewPCMConverter(pcmInput, streamingTranscriber.Audio())
	streamingTranscriber.Start(context.Background())
	pcmConverter.Start(context.Background())
	p.Start()
}

func websocketURL(transcriptionURL string) string {
	u, err := url.Parse(transcriptionURL)
	if err != nil {
		return transcriptionURL
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}

	q := u.Query()
	q.Set("vad_filter", "true")
	u.RawQuery = q.Encode()

	return u.String()
}

/// coaie de ce naiba nu vad aasta in git tracking?
