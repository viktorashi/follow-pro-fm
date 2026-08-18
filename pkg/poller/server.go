package poller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// TelemetryServer runs the embedded HTTP dashboard.
type TelemetryServer struct {
	echo                 *echo.Echo
	authMgr              *AuthManager
	stateMgr             *StateManager
	broadcaster          *SSEBroadcaster
	logWriter            *SSELogWriter
	dbMgr                *DBManager
	dataDir              string
	audiosDir            string
	campaigns            []Campaign
	wappClients          []WhatsAppClient
	onAddPhone           func() error
	onAddPhoneWithPerson func(personID *int64) error
	onDisconnectPhone    func(phone string) error
	timeNow              func() time.Time
	transcribe           func(context.Context, []byte) (string, error)
	alerter              Alerter
}

type ScheduleEntry struct {
	Date          string
	TargetMatches []int
}

type PersonAudioFiles struct {
	Active []string `json:"active"`
	Used   []string `json:"used"`
}

func NewTelemetryServer(authMgr *AuthManager, stateMgr *StateManager, broadcaster *SSEBroadcaster, logWriter *SSELogWriter, dbMgr *DBManager, dataDir string, audiosDir string, campaigns []Campaign, transcribe func(context.Context, []byte) (string, error)) *TelemetryServer {
	e := echo.New()

	if logWriter == nil {
		logWriter = NewSSELogWriter(os.Stdout, broadcaster) // fallback
	}

	e.Use(middleware.Recover())

	ts := &TelemetryServer{
		echo:        e,
		authMgr:     authMgr,
		stateMgr:    stateMgr,
		broadcaster: broadcaster,
		logWriter:   logWriter,
		dbMgr:       dbMgr,
		dataDir:     dataDir,
		audiosDir:   audiosDir,
		campaigns:   append([]Campaign(nil), campaigns...),
		timeNow:     time.Now,
		transcribe:  transcribe,
	}

	ts.registerRoutes()
	e.POST("/api/campaigns/phrases", ts.handleAddCampaignPhrase, ts.authMgr.RequireAuth())
	e.DELETE("/api/campaigns/phrases", ts.handleDeleteCampaignPhrase, ts.authMgr.RequireAuth())

	return ts
}

func (s *TelemetryServer) registerRoutes() {
	// Public routes
	// Find the static directory
	staticDirs := []string{"static", "../static", "../../static"}
	var staticPath string
	for _, dir := range staticDirs {
		if _, err := os.Stat(dir); err == nil {
			staticPath = dir
			break
		}
	}
	if staticPath == "" {
		staticPath = "static" // fallback
	}
	s.echo.Static("/static", staticPath)
	s.echo.GET("/login", s.handleLoginView)
	s.echo.POST("/login", s.handleLoginSubmit)
	s.echo.GET("/logout", s.handleLogout)
	s.echo.POST("/auth/magic/request", s.handleMagicLinkRequest)
	s.echo.GET("/auth/magic", s.handleMagicLinkVerify)
	s.echo.GET("/qr.png", s.handleQRImage) // New unauthenticated QR endpoint for email

	// Protected routes
	protected := s.echo.Group("", s.authMgr.RequireAuth())
	protected.GET("/", s.handleDashboardView)
	protected.GET("/logs", s.handleLogsView)
	protected.GET("/alerts", s.handleAlertsView)
	protected.GET("/radio-logs", s.handleRadioLogsView)
	protected.GET("/data", s.handleDataView)
	protected.Static("/raw-data", s.dataDir)
	protected.GET("/events/dashboard", s.handleDashboardStream)
	protected.GET("/events/logs", s.handleLogsStream)
	protected.POST("/api/kill-switch", s.handleKillSwitch)
	protected.GET("/api/schedule", s.handleGetSchedule)
	protected.POST("/api/schedule", s.handleSetSchedule)
	protected.POST("/api/sender/add", s.handleAddSenderPhone)
	protected.POST("/api/sender/disconnect", s.handleDisconnectSenderPhone)
	protected.POST("/api/audio/upload", s.handleAudioUpload)
	protected.GET("/api/persons", s.handleListPersons)
	protected.POST("/api/persons", s.handleCreatePerson)
	protected.POST("/api/persons/:id/edit", s.handleEditPerson)
	protected.POST("/api/persons/:id/delete", s.handleDeletePerson)
	protected.POST("/api/phones/assign", s.handleAssignPhone)
	protected.POST("/api/audios/batch-move", s.handleBatchMoveAudios)
	protected.GET("/api/audio/play", s.handleAudioPlay)

	if os.Getenv("MOCK_WHATSAPP") == "true" {
		protected.POST("/api/test/mock-scan", s.handleMockScan)
	}
	protected.POST("/api/settings/gathering", s.handleToggleGathering)
	protected.GET("/api/unreviewed", s.handleUnreviewedList)
	protected.GET("/api/signatures/file", s.handleSignatureFile)
	protected.POST("/api/signatures/transcribe", s.handleTranscribeSignature)
	protected.POST("/unreviewed/crop", s.handleUnreviewedCrop)
	protected.POST("/unreviewed/delete", s.handleUnreviewedDelete)
	protected.POST("/api/unreviewed/batch-delete", s.handleBatchDeleteUnreviewed)
	protected.POST("/canonical/delete", s.handleCanonicalDelete)
	protected.POST("/api/canonical/batch-delete", s.handleBatchDeleteCanonical)
	protected.POST("/api/unreviewed/remux-all", s.handleRemuxAllUnreviewed)
}

func (s *TelemetryServer) Start(addr string) error {
	return s.echo.Start(addr)
}

func (s *TelemetryServer) handleLoginView(c *echo.Context) error {
	return Render(c, http.StatusOK, Login())
}

func (s *TelemetryServer) handleLoginSubmit(c *echo.Context) error {
	email := c.FormValue("email")
	password := c.FormValue("password")

	err := s.authMgr.CheckLogin(c.Request().Context(), email, password)
	if err != nil {
		return c.String(http.StatusUnauthorized, "Invalid credentials or email not trusted")
	}

	token, err := s.authMgr.CreateSession(c.Request().Context(), email)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Could not create session")
	}
	SetSessionCookie(c, token)
	c.Response().Header().Set("HX-Redirect", "/")
	return c.Redirect(http.StatusFound, "/")
}

func (s *TelemetryServer) handleLogout(c *echo.Context) error {
	if cookie, err := c.Cookie("session_token"); err == nil {
		_ = s.authMgr.RevokeSession(c.Request().Context(), cookie.Value)
	}
	ClearSessionCookie(c)
	c.Response().Header().Set("HX-Redirect", "/login")
	return c.Redirect(http.StatusFound, "/login")
}

func (s *TelemetryServer) handleMagicLinkRequest(c *echo.Context) error {
	email := c.FormValue("email")
	err := s.authMgr.GenerateAndSendMagicLink(c.Request().Context(), email)
	if err != nil {
		// Do not leak if email exists or not for security, just say OK
		return c.String(http.StatusOK, "If your email is trusted, a link has been sent.")
	}
	return c.String(http.StatusOK, "Magic link sent to your email!")
}

func (s *TelemetryServer) handleMagicLinkVerify(c *echo.Context) error {
	token := c.QueryParam("token")
	email, err := s.authMgr.VerifyMagicLink(c.Request().Context(), token)
	if err != nil {
		return c.String(http.StatusUnauthorized, "Invalid or expired token")
	}

	sessionToken, err := s.authMgr.CreateSession(c.Request().Context(), email)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Could not create session")
	}
	SetSessionCookie(c, sessionToken)
	return c.Redirect(http.StatusFound, "/")
}

func (s *TelemetryServer) handleDashboardView(c *echo.Context) error {
	state := s.stateMgr.Get()
	chunks, err := s.listChunks(BucketUnreviewed)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error reading unreviewed signatures: "+err.Error())
	}
	canonicalChunks, err := s.listChunks(BucketCanonical)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error reading canonical signatures: "+err.Error())
	}
	schedules, err := s.listScheduleEntries(c.Request().Context())
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error reading schedule entries: "+err.Error())
	}

	var persons []Person
	personAudios := make(map[string]PersonAudioFiles)
	if s.dbMgr != nil {
		if pList, err := s.dbMgr.ListPersons(c.Request().Context()); err == nil {
			persons = pList
			for _, p := range persons {
				active, used, _ := ListAudioFilesForPerson(p.Slug, s.audiosDir)
				personAudios[p.Slug] = PersonAudioFiles{Active: active, Used: used}
			}
		}
	}

	s.refreshStatePersons(c.Request().Context())
	state = s.stateMgr.Get()

	campaignArtists := s.getCampaignArtists()
	campaignPhrases := make(map[string][]string)
	if s.dbMgr != nil {
		for _, artist := range campaignArtists {
			phrases, _ := s.dbMgr.GetCampaignPhrases(c.Request().Context(), artist)
			if len(phrases) > 0 {
				campaignPhrases[artist] = phrases
			}
		}
	}

	return Render(c, http.StatusOK, Dashboard(state, chunks, canonicalChunks, dashboardUploadPhones(state.Connections), schedules, campaignArtists, campaignPhrases, persons, personAudios))
}

func (s *TelemetryServer) getCampaignArtists() []string {
	campaigns := s.campaigns
	if len(campaigns) == 0 {
		campaigns = DefaultActiveCampaigns
	}
	seen := make(map[string]bool)
	var artists []string
	for _, c := range campaigns {
		artist := strings.TrimSpace(c.Artist)
		if artist != "" && !seen[strings.ToUpper(artist)] {
			seen[strings.ToUpper(artist)] = true
			artists = append(artists, artist)
		}
	}
	return artists
}

func (s *TelemetryServer) handleLogsView(c *echo.Context) error {
	return Render(c, http.StatusOK, LogsPage())
}

func (s *TelemetryServer) handleAlertsView(c *echo.Context) error {
	var alerts []AlertRecord
	if s.dbMgr != nil {
		l, err := s.dbMgr.GetRecentAlerts(c.Request().Context(), 200)
		if err == nil {
			alerts = l
		}
	}
	return Render(c, http.StatusOK, AlertsPage(alerts))
}

func (s *TelemetryServer) handleRadioLogsView(c *echo.Context) error {
	var logs []RadioLog
	if s.dbMgr != nil {
		l, err := s.dbMgr.GetRadioLogs(c.Request().Context(), 100)
		if err == nil {
			logs = l
		}
	}
	return Render(c, http.StatusOK, RadioLogsPage(logs))
}

func (s *TelemetryServer) handleQRImage(c *echo.Context) error {
	state := s.stateMgr.Get()
	var b64 string
	for _, conn := range state.Connections {
		if conn.QRCodeData != "" && conn.Status == StatusPairingRequired {
			b64 = conn.QRCodeData
			break
		}
	}

	if b64 == "" {
		// Return 404 or a placeholder if no QR is needed
		return c.String(http.StatusNotFound, "No QR Code active")
	}

	prefix := "data:image/png;base64,"
	b64 = strings.TrimPrefix(b64, prefix)

	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Failed to decode QR code")
	}

	// Tell email clients not to cache this image
	c.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")

	return c.Blob(http.StatusOK, "image/png", decoded)
}

func (s *TelemetryServer) handleDashboardStream(c *echo.Context) error {
	return s.streamEvents(c, false)
}

func (s *TelemetryServer) handleLogsStream(c *echo.Context) error {
	return s.streamEvents(c, true)
}

func (s *TelemetryServer) streamEvents(c *echo.Context, isLogs bool) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
	c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
	c.Response().Header().Set(echo.HeaderConnection, "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
	if f, ok := c.Response().(http.Flusher); ok {
		f.Flush()
	}
	if isLogs && s.logWriter != nil {
		s.logWriter.AddSubscriber()
		defer s.logWriter.RemoveSubscriber()

		// Send recent history
		for _, line := range s.logWriter.GetRecentLogs() {
			ev := &SSEEvent{Event: "log", Data: line}
			if _, err := c.Response().Write(ev.Marshal()); err != nil {
				return nil
			}
		}
		if f, ok := c.Response().(http.Flusher); ok {
			f.Flush()
		}
	} else if !isLogs && s.stateMgr != nil {
		// Send current state for dashboard
		state := s.stateMgr.Get()

		var statusBuf bytes.Buffer
		_ = StatusComponent(state).Render(c.Request().Context(), &statusBuf)
		_, _ = c.Response().Write((&SSEEvent{Event: "status", Data: statusBuf.Bytes()}).Marshal())

		var songBuf bytes.Buffer
		_ = SongComponent(state.CurrentSong).Render(c.Request().Context(), &songBuf)
		_, _ = c.Response().Write((&SSEEvent{Event: "song", Data: songBuf.Bytes()}).Marshal())

		var audioBuf bytes.Buffer
		_ = AudioStatsComponent(state).Render(c.Request().Context(), &audioBuf)
		_, _ = c.Response().Write((&SSEEvent{Event: "audio", Data: audioBuf.Bytes()}).Marshal())

		var qrBuf bytes.Buffer
		_ = QRComponent(state).Render(c.Request().Context(), &qrBuf)
		_, _ = c.Response().Write((&SSEEvent{Event: "qrcode", Data: qrBuf.Bytes()}).Marshal())

		if f, ok := c.Response().(http.Flusher); ok {
			f.Flush()
		}
	}

	ch := s.broadcaster.Subscribe()
	defer s.broadcaster.Unsubscribe(ch)

	// Keep-alive to prevent Fly.io proxy from closing idle connections
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request().Context().Done():
			return nil
		case <-ticker.C:
			if _, err := c.Response().Write([]byte(": keepalive\n\n")); err != nil {
				return nil
			}
			if f, ok := c.Response().(http.Flusher); ok {
				f.Flush()
			}
		case ev := <-ch:
			// If this client is not on the logs page, ignore "log" events to save bandwidth
			if !isLogs && ev.Event == "log" {
				continue
			}
			if _, err := c.Response().Write(ev.Marshal()); err != nil {
				return nil
			}
			if f, ok := c.Response().(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// Render is a helper to render Templ components in Echo
func Render(c *echo.Context, statusCode int, t templ.Component) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTML)
	c.Response().WriteHeader(statusCode)
	return t.Render(c.Request().Context(), c.Response())
}
func (s *TelemetryServer) handleKillSwitch(c *echo.Context) error {
	var payload struct {
		Password string `json:"password"`
		Active   bool   `json:"active"`
	}
	if err := c.Bind(&payload); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid payload"})
	}

	adminPass := os.Getenv("ADMIN_PASSWORD")
	if payload.Password != adminPass {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid password"})
	}

	if s.dbMgr != nil {
		err := s.dbMgr.SetKillSwitch(c.Request().Context(), payload.Active)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save to database"})
		}
	}

	s.stateMgr.Update(func(state *AppState) {
		state.KillSwitchActive = payload.Active
		if payload.Active {
			state.Status = StatusKilled
		} else {
			state.Status = s.statusAfterKillSwitchDisabled(*state, s.timeNow())
		}
	})

	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

func (s *TelemetryServer) statusAfterKillSwitchDisabled(state AppState, now time.Time) AppStatus {
	if len(s.campaigns) > 0 && !isAnyCampaignActive(s.campaigns, now) {
		return StatusSleeping
	}

	anyConnected := false
	anyPairingRequired := false
	anyError := false

	for _, conn := range state.Connections {
		if conn.WhatsAppConnected {
			anyConnected = true
		}
		switch conn.Status {
		case StatusPairingRequired:
			anyPairingRequired = true
		case StatusError:
			anyError = true
		}
	}

	switch {
	case anyPairingRequired:
		return StatusPairingRequired
	case anyConnected:
		return StatusPolling
	case anyError:
		return StatusError
	case len(state.Connections) > 0:
		return StatusInitializing
	default:
		return StatusInitializing
	}
}

func isAnyCampaignActive(campaigns []Campaign, now time.Time) bool {
	return slices.ContainsFunc(campaigns, func(campaign Campaign) bool {
		return campaign.IsActive(now)
	})
}

type FileInfo struct {
	Name    string
	Path    string
	Size    int64
	ModTime string
	IsDir   bool
}

type ReviewChunk struct {
	Name              string              `json:"name"`
	Size              int64               `json:"size"`
	ModTime           time.Time           `json:"mod_time"`
	PlayURL           string              `json:"play_url"`
	ParsedTranscripts []TimedItem[string] `json:"parsed_transcripts"`
	CampaignArtist    string              `json:"campaign_artist"`
	DurationSeconds   float64             `json:"duration_seconds"`
	ParsedTags        []ContestTag        `json:"parsed_tags"`
}

func (s *TelemetryServer) handleDataView(c *echo.Context) error {
	var files []FileInfo

	err := filepath.WalkDir(s.dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return nil // skip
		}

		rel, _ := filepath.Rel(s.dataDir, path)
		if rel == "." {
			return nil
		}

		files = append(files, FileInfo{
			Name:    d.Name(),
			Path:    rel,
			Size:    info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
			IsDir:   d.IsDir(),
		})

		return nil
	})

	if err != nil {
		return c.String(http.StatusInternalServerError, "Error reading data directory: "+err.Error())
	}

	t := DataViewer(files)
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTML)
	return t.Render(c.Request().Context(), c.Response())
}

func (s *TelemetryServer) SetWhatsAppClients(clients []WhatsAppClient) {
	s.wappClients = clients
}

func (s *TelemetryServer) SetAlerter(alerter Alerter) {
	s.alerter = alerter
}

func (s *TelemetryServer) SetOnAddPhone(fn func() error) {
	s.onAddPhone = fn
}

func (s *TelemetryServer) SetOnAddPhoneWithPerson(fn func(personID *int64) error) {
	s.onAddPhoneWithPerson = fn
}

func (s *TelemetryServer) SetOnDisconnectPhone(fn func(phone string) error) {
	s.onDisconnectPhone = fn
}

func (s *TelemetryServer) handleAddSenderPhone(c *echo.Context) error {
	pIDStr := strings.TrimSpace(c.FormValue("person_id"))
	if pIDStr == "" {
		pIDStr = strings.TrimSpace(c.QueryParam("person_id"))
	}
	id, err := strconv.ParseInt(pIDStr, 10, 64)
	if err != nil || id <= 0 {
		return c.String(http.StatusBadRequest, "A valid Person is required before pairing")
	}
	personID := &id
	if s.dbMgr != nil {
		person, err := s.dbMgr.GetPerson(c.Request().Context(), id)
		if err != nil || person == nil {
			return c.String(http.StatusBadRequest, "Selected Person does not exist")
		}
	}

	if s.onAddPhoneWithPerson != nil {
		if err := s.onAddPhoneWithPerson(personID); err != nil {
			return c.String(http.StatusConflict, "Could not start QR pairing: "+err.Error())
		}
	} else if s.onAddPhone != nil {
		if err := s.onAddPhone(); err != nil {
			return c.String(http.StatusConflict, "Could not start QR pairing: "+err.Error())
		}
	} else {
		return c.String(http.StatusInternalServerError, "Add phone callback not set")
	}

	return c.String(http.StatusOK, "QR pairing started. Scan the code above.")
}

func (s *TelemetryServer) handleDisconnectSenderPhone(c *echo.Context) error {
	phone := strings.TrimSpace(c.FormValue("phone"))
	if phone == "" {
		return c.String(http.StatusBadRequest, "Phone is required")
	}

	if s.onDisconnectPhone != nil {
		if err := s.onDisconnectPhone(phone); err != nil {
			return c.String(http.StatusInternalServerError, "Could not disconnect phone: "+err.Error())
		}
	} else {
		return c.String(http.StatusInternalServerError, "Disconnect phone callback not set")
	}

	var qrBuf bytes.Buffer
	state := s.stateMgr.Get()
	_ = QRComponent(state).Render(c.Request().Context(), &qrBuf)
	return c.HTML(http.StatusOK, qrBuf.String())
}

func (s *TelemetryServer) handleAudioUpload(c *echo.Context) error {
	personSlug := strings.TrimSpace(c.FormValue("person_slug"))
	if personSlug == "" {
		return c.String(http.StatusBadRequest, "Person is required")
	}
	audioDir, err := s.personAudioDir(c.Request().Context(), personSlug)
	if err != nil {
		return c.String(http.StatusBadRequest, "Unknown person")
	}

	form, err := c.MultipartForm()
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid multipart form")
	}

	files := form.File["audio"]
	if len(files) == 0 {
		return c.String(http.StatusBadRequest, "At least one audio file is required")
	}

	if err := os.MkdirAll(audioDir, 0755); err != nil {
		return c.String(http.StatusInternalServerError, "Failed to prepare audio directory")
	}

	var saved []string
	for _, fileHeader := range files {
		filename := filepath.Base(fileHeader.Filename)
		if filename == "." || filename == "" || !strings.EqualFold(filepath.Ext(filename), ".ogg") {
			continue // Skip invalid files when uploading multiple
		}

		activePath := filepath.Join(audioDir, filename)
		usedPath := filepath.Join(audioDir, "used", filename)

		if _, err := os.Stat(activePath); err == nil {
			continue // Skip if already active
		}
		if _, err := os.Stat(usedPath); err == nil {
			continue // Skip if already used
		}

		src, err := fileHeader.Open()
		if err != nil {
			continue
		}

		dst, err := os.OpenFile(activePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			_ = src.Close()
			continue
		}

		if _, err := io.Copy(dst, src); err == nil {
			_ = dst.Close()
			saved = append(saved, filename)
		} else {
			_ = dst.Close()
			_ = os.Remove(activePath)
		}
		_ = src.Close()
	}

	if len(saved) == 0 {
		return c.String(http.StatusBadRequest, "No new valid .ogg files were uploaded")
	}

	s.refreshStatePersons(c.Request().Context())
	return c.String(http.StatusOK, fmt.Sprintf("Successfully uploaded %d files to %s", len(saved), personSlug))
}

func (s *TelemetryServer) handleAudioPlay(c *echo.Context) error {
	slug := strings.TrimSpace(c.QueryParam("slug"))
	if slug == "" {
		slug = strings.TrimSpace(c.QueryParam("person_slug"))
	}
	filename := filepath.Base(c.QueryParam("file"))
	if filename == "" || filename == "." || filename == ".." {
		return c.String(http.StatusBadRequest, "Invalid file name")
	}

	targetDir, err := s.personAudioDir(c.Request().Context(), slug)
	if err != nil {
		return c.String(http.StatusBadRequest, "Unknown person")
	}
	filePath := filepath.Join(targetDir, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// Check used folder if not found in active
		filePath = filepath.Join(targetDir, "used", filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return c.String(http.StatusNotFound, "Audio file not found")
		}
	}

	c.Response().Header().Set(echo.HeaderContentType, "audio/ogg")
	http.ServeFile(c.Response(), c.Request(), filePath)
	return nil
}

func (s *TelemetryServer) handleListPersons(c *echo.Context) error {
	if s.dbMgr == nil {
		return c.JSON(http.StatusOK, []Person{})
	}
	persons, err := s.dbMgr.ListPersons(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, persons)
}

func (s *TelemetryServer) handleCreatePerson(c *echo.Context) error {
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		var req struct {
			Name string `json:"name"`
		}
		_ = c.Bind(&req)
		name = strings.TrimSpace(req.Name)
	}
	if name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Person name is required"})
	}

	if s.dbMgr == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Database not initialized"})
	}

	person, err := s.dbMgr.CreatePerson(c.Request().Context(), name)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	_ = InitAudioPool(GetAudioDirForPerson(person.Slug, s.audiosDir))
	s.refreshStatePersons(c.Request().Context())

	if c.Request().Header.Get("HX-Request") == "true" {
		c.Response().Header().Set("HX-Refresh", "true")
		return c.NoContent(http.StatusOK)
	}
	return c.JSON(http.StatusOK, person)
}

func (s *TelemetryServer) handleEditPerson(c *echo.Context) error {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid person ID"})
	}
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		var req struct {
			Name string `json:"name"`
		}
		_ = c.Bind(&req)
		name = strings.TrimSpace(req.Name)
	}
	if name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Person name is required"})
	}

	if s.dbMgr == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Database not initialized"})
	}

	if err := s.dbMgr.UpdatePerson(c.Request().Context(), id, name); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	s.refreshStatePersons(c.Request().Context())

	if c.Request().Header.Get("HX-Request") == "true" {
		c.Response().Header().Set("HX-Refresh", "true")
		return c.NoContent(http.StatusOK)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *TelemetryServer) handleDeletePerson(c *echo.Context) error {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid person ID"})
	}

	if s.dbMgr == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Database not initialized"})
	}

	if err := s.dbMgr.DeletePerson(c.Request().Context(), id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	s.refreshStatePersons(c.Request().Context())

	if c.Request().Header.Get("HX-Request") == "true" {
		c.Response().Header().Set("HX-Refresh", "true")
		return c.NoContent(http.StatusOK)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *TelemetryServer) handleAssignPhone(c *echo.Context) error {
	phone := strings.TrimSpace(c.FormValue("phone"))
	personIDStr := strings.TrimSpace(c.FormValue("person_id"))

	if phone == "" {
		var req struct {
			Phone    string `json:"phone"`
			PersonID string `json:"person_id"`
		}
		_ = c.Bind(&req)
		phone = strings.TrimSpace(req.Phone)
		personIDStr = strings.TrimSpace(req.PersonID)
	}

	if phone == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Phone is required"})
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}

	var personID *int64
	if personIDStr != "" && personIDStr != "0" && personIDStr != "null" {
		if id, err := strconv.ParseInt(personIDStr, 10, 64); err == nil && id > 0 {
			personID = &id
		}
	}

	if s.dbMgr != nil {
		if err := s.dbMgr.AssignPhoneToPerson(c.Request().Context(), phone, personID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	s.refreshStatePersons(c.Request().Context())

	if c.Request().Header.Get("HX-Request") == "true" {
		c.Response().Header().Set("HX-Refresh", "true")
		return c.NoContent(http.StatusOK)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *TelemetryServer) handleBatchMoveAudios(c *echo.Context) error {
	var req struct {
		FromSlug string   `json:"from_slug"`
		ToSlug   string   `json:"to_slug"`
		Files    []string `json:"files"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
	}

	req.FromSlug = strings.TrimSpace(req.FromSlug)
	req.ToSlug = strings.TrimSpace(req.ToSlug)
	if req.FromSlug == "" || req.ToSlug == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "from_slug and to_slug are required"})
	}
	if len(req.Files) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "At least one file must be selected"})
	}

	srcDir, err := s.personAudioDir(c.Request().Context(), req.FromSlug)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown source person"})
	}
	dstDir, err := s.personAudioDir(c.Request().Context(), req.ToSlug)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unknown target person"})
	}

	if err := MoveAudioFiles(srcDir, dstDir, req.Files); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	s.refreshStatePersons(c.Request().Context())
	return c.JSON(http.StatusOK, map[string]interface{}{
		"status": "ok",
		"moved":  len(req.Files),
	})
}

func (s *TelemetryServer) personAudioDir(ctx context.Context, slug string) (string, error) {
	slug = strings.TrimSpace(slug)
	if s.dbMgr == nil || !isSafeFilename(slug) {
		return "", fmt.Errorf("invalid person slug")
	}
	person, err := s.dbMgr.GetPersonBySlug(ctx, slug)
	if err != nil {
		return "", err
	}
	if !isSafeFilename(person.Slug) {
		return "", fmt.Errorf("invalid stored person slug")
	}
	return filepath.Join(s.audiosDir, person.Slug), nil
}

// HydrateConnectionPersons loads persons and sender_sessions from the DB
// and enriches the in-memory connection state with PersonID/PersonName/PersonSlug
// as well as the audio stats (unused/used) for the assigned person.
func HydrateConnectionPersons(dbMgr *DBManager, stateMgr *StateManager, audiosDir string, ctx context.Context) {
	persons, err := dbMgr.ListPersons(ctx)
	if err != nil {
		return
	}
	sessions, err := dbMgr.SenderSessions(ctx)
	if err != nil {
		return
	}
	sessionMap := make(map[string]SenderSession)
	for _, sess := range sessions {
		sessionMap[sess.Phone] = sess
	}

	personStats := GetAudioStatsPerPerson(persons, audiosDir)

	stateMgr.Update(func(st *AppState) {
		st.Persons = persons
		for i, conn := range st.Connections {
			if sess, ok := sessionMap[conn.Phone]; ok {
				st.Connections[i].PersonID = sess.PersonID
				st.Connections[i].PersonName = sess.PersonName
				st.Connections[i].PersonSlug = sess.PersonSlug

				if sess.PersonSlug != "" {
					if stats, ok := personStats[sess.PersonSlug]; ok {
						st.Connections[i].UnusedAudios = stats.Unused
						st.Connections[i].UsedAudios = stats.Used
					} else {
						st.Connections[i].UnusedAudios = 0
						st.Connections[i].UsedAudios = 0
					}
				} else {
					st.Connections[i].UnusedAudios = 0
					st.Connections[i].UsedAudios = 0
				}
			}
		}
	})
}

func (s *TelemetryServer) refreshStatePersons(ctx context.Context) {
	if s.dbMgr == nil || s.stateMgr == nil {
		return
	}
	HydrateConnectionPersons(s.dbMgr, s.stateMgr, s.audiosDir, ctx)
	AlertUnassignedSenderPhones(s.stateMgr.Get(), s.alerter)
}

func AlertUnassignedSenderPhones(state AppState, alerter Alerter) {
	if alerter == nil {
		return
	}
	phones := make([]string, 0, state.UnassignedPhonesCount)
	for _, conn := range state.Connections {
		if conn.PersonID == nil || *conn.PersonID == 0 || conn.PersonSlug == "" {
			phones = append(phones, conn.Phone)
		}
	}
	if len(phones) == 0 {
		return
	}
	sort.Strings(phones)
	_ = alerter.AlertCritical(AlertEvent{
		Title:       "Unassigned WhatsApp Sender",
		Message:     "Cannot send voice notes until these sender phones are assigned to a person: " + strings.Join(phones, ", "),
		ActionLabel: "Assign Sender",
		ActionURL:   "/",
	})
}

func dashboardUploadPhones(conns []WAConnectionState) []string {
	seen := make(map[string]struct{})
	var phones []string

	for _, conn := range conns {
		phone := strings.TrimSpace(conn.Phone)
		if phone == "" {
			continue
		}
		if !strings.HasPrefix(phone, "+") {
			phone = "+" + phone
		}
		if _, ok := seen[phone]; ok {
			continue
		}
		seen[phone] = struct{}{}
		phones = append(phones, phone)
	}

	sort.Strings(phones)
	return phones
}

func (s *TelemetryServer) handleMockScan(c *echo.Context) error {
	if len(s.wappClients) == 0 {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "WhatsApp clients not set"})
	}
	for _, client := range s.wappClients {
		if mock, ok := client.(*MockWhatsAppClient); ok {
			mock.SimulatePairing()
		}
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "paired"})
}

func (s *TelemetryServer) handleToggleGathering(c *echo.Context) error {
	nextValue := false
	if s.stateMgr != nil {
		nextValue = !s.stateMgr.Get().GatheringSignatures
	}
	if s.dbMgr != nil {
		if err := s.dbMgr.SetGatheringSignatures(c.Request().Context(), nextValue); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save gathering setting"})
		}
	}
	s.stateMgr.Update(func(state *AppState) {
		state.GatheringSignatures = nextValue
	})
	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

type scheduleUpdateRequest struct {
	Date          string          `json:"date"`
	TargetMatches json.RawMessage `json:"target_matches"`
}

func (s *TelemetryServer) handleGetSchedule(c *echo.Context) error {
	if s.dbMgr == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
	}

	schedules, err := s.dbMgr.GetAllSchedules(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	resp := make(map[string][]int, len(schedules))
	for date, raw := range schedules {
		targetMatches, err := ParseSchedule(raw)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("invalid stored schedule for %s", date)})
		}
		resp[date] = targetMatches
	}

	return c.JSON(http.StatusOK, resp)
}

func (s *TelemetryServer) handleSetSchedule(c *echo.Context) error {
	if s.dbMgr == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "database not configured"})
	}

	var req scheduleUpdateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid payload"})
	}

	scheduleJSON, err := normalizeSchedulePayload(req.TargetMatches)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	if req.Date == "*" {
		if len(s.campaigns) == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "campaigns not configured"})
		}

		updated := 0
		for _, campaign := range s.campaigns {
			dates, err := campaignWeekdays(campaign, bucharestLocation)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			}
			for _, day := range dates {
				if err := s.dbMgr.SetDailySchedule(c.Request().Context(), day.Format("2006-01-02"), scheduleJSON); err != nil {
					return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
				}
				updated++
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"date":           req.Date,
			"status":         "ok",
			"updated_days":   updated,
			"target_matches": json.RawMessage(scheduleJSON),
		})
	}

	if _, err := time.Parse("2006-01-02", req.Date); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "date must be in YYYY-MM-DD format"})
	}
	if len(s.campaigns) > 0 {
		allowed, err := isScheduleDateAllowed(req.Date, s.campaigns, bucharestLocation)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if !allowed {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "date must be a campaign weekday within the configured campaign windows"})
		}
	}

	if err := s.dbMgr.SetDailySchedule(c.Request().Context(), req.Date, scheduleJSON); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"date":           req.Date,
		"target_matches": json.RawMessage(scheduleJSON),
		"status":         "ok",
	})
}

func (s *TelemetryServer) listScheduleEntries(ctx context.Context) ([]ScheduleEntry, error) {
	if s.dbMgr == nil {
		return nil, nil
	}

	schedules, err := s.dbMgr.GetAllSchedules(ctx)
	if err != nil {
		return nil, err
	}

	entries := make([]ScheduleEntry, 0, len(schedules))
	for date, raw := range schedules {
		targetMatches, err := ParseSchedule(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid stored schedule for %s: %w", date, err)
		}
		entries = append(entries, ScheduleEntry{
			Date:          date,
			TargetMatches: targetMatches,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Date < entries[j].Date
	})

	return entries, nil
}

func (s *TelemetryServer) handleUnreviewedList(c *echo.Context) error {
	chunks, err := s.listChunks(BucketUnreviewed)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, chunks)
}

func (s *TelemetryServer) handleSignatureFile(c *echo.Context) error {
	filename := c.QueryParam("name")
	bucket := c.QueryParam("bucket")
	if bucket == "" {
		bucket = BucketUnreviewed
	}
	if !isSafeFilename(filename) || !isSafeFilename(bucket) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid filename or bucket"})
	}

	path := filepath.Join(s.dataDir, DirSignatures, bucket, filename)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filename))
	http.ServeFile(c.Response(), c.Request(), path)
	return nil
}

func (s *TelemetryServer) handleUnreviewedCrop(c *echo.Context) error {
	filename := c.FormValue("filename")
	if !isSafeFilename(filename) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid filename"})
	}
	var startSeconds, endSeconds float64
	_, _ = fmt.Sscanf(c.FormValue("start_seconds"), "%f", &startSeconds)
	_, _ = fmt.Sscanf(c.FormValue("end_seconds"), "%f", &endSeconds)

	unreviewedDir := filepath.Join(s.dataDir, DirSignatures, BucketUnreviewed)
	canonicalDir := filepath.Join(s.dataDir, DirSignatures, BucketCanonical)

	err := CropAndMarkCanonical(unreviewedDir, canonicalDir, filename, startSeconds, endSeconds)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	transcript := ""
	if s.transcribe != nil {
		targetPath := filepath.Join(canonicalDir, filename)
		if data, err := os.ReadFile(targetPath); err == nil {
			ctx, cancel := context.WithTimeout(c.Request().Context(), 120*time.Second)
			defer cancel()
			if t, err := s.transcribe(ctx, data); err == nil {
				transcript = t
			}
		}
	}

	if s.dbMgr != nil {
		if err := s.dbMgr.CopySignatureFile(c.Request().Context(), BucketUnreviewed, BucketCanonical, filename); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		savePhrase := strings.TrimSpace(c.FormValue("save_phrase"))
		if savePhrase != "" {
			meta, err := s.dbMgr.GetSignatureFile(c.Request().Context(), BucketCanonical, filename)
			if err == nil && meta.CampaignArtist != "" {
				_ = s.dbMgr.AddCampaignPhrase(c.Request().Context(), meta.CampaignArtist, savePhrase)
			}
		}
		if transcript != "" {
			_ = s.dbMgr.UpdateSignatureTranscript(c.Request().Context(), BucketCanonical, filename, transcript)
		}
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

func (s *TelemetryServer) handleUnreviewedDelete(c *echo.Context) error {
	filename := c.FormValue("filename")
	if !isSafeFilename(filename) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid filename"})
	}

	unreviewedDir := filepath.Join(s.dataDir, DirSignatures, BucketUnreviewed)
	targetPath := filepath.Join(unreviewedDir, filename)

	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	if err := os.Remove(targetPath); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to delete file: " + err.Error()})
	}

	if s.dbMgr != nil {
		_ = s.dbMgr.DeleteSignatureFile(c.Request().Context(), BucketUnreviewed, filename)
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

func (s *TelemetryServer) handleBatchDeleteUnreviewed(c *echo.Context) error {
	var req struct {
		Files []string `json:"files"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
	}
	if len(req.Files) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "At least one file must be selected"})
	}

	unreviewedDir := filepath.Join(s.dataDir, DirSignatures, BucketUnreviewed)
	deletedCount := 0

	for _, filename := range req.Files {
		filename = strings.TrimSpace(filename)
		if !isSafeFilename(filename) {
			continue
		}
		targetPath := filepath.Join(unreviewedDir, filename)
		if err := os.Remove(targetPath); err == nil {
			deletedCount++
			if s.dbMgr != nil {
				_ = s.dbMgr.DeleteSignatureFile(c.Request().Context(), BucketUnreviewed, filename)
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"deleted": deletedCount,
	})
}

func (s *TelemetryServer) handleCanonicalDelete(c *echo.Context) error {
	filename := c.FormValue("filename")
	if !isSafeFilename(filename) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid filename"})
	}

	canonicalDir := filepath.Join(s.dataDir, DirSignatures, BucketCanonical)
	targetPath := filepath.Join(canonicalDir, filename)

	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	if err := os.Remove(targetPath); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to delete file: " + err.Error()})
	}

	if s.dbMgr != nil {
		_ = s.dbMgr.DeleteSignatureFile(c.Request().Context(), BucketCanonical, filename)
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

func (s *TelemetryServer) handleBatchDeleteCanonical(c *echo.Context) error {
	var req struct {
		Files []string `json:"files"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
	}
	if len(req.Files) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "At least one file must be selected"})
	}

	canonicalDir := filepath.Join(s.dataDir, DirSignatures, BucketCanonical)
	deletedCount := 0

	for _, filename := range req.Files {
		filename = strings.TrimSpace(filename)
		if !isSafeFilename(filename) {
			continue
		}
		targetPath := filepath.Join(canonicalDir, filename)
		if err := os.Remove(targetPath); err == nil {
			deletedCount++
			if s.dbMgr != nil {
				_ = s.dbMgr.DeleteSignatureFile(c.Request().Context(), BucketCanonical, filename)
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"deleted": deletedCount,
	})
}

func (s *TelemetryServer) listChunks(bucket string) ([]ReviewChunk, error) {
	dir := filepath.Join(s.dataDir, DirSignatures, bucket)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	chunks := make([]ReviewChunk, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".mp3") && !strings.HasSuffix(entry.Name(), ".ogg")) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

		var parsedTranscripts []TimedItem[string]
		var campaignArtist string
		var parsedTags []ContestTag
		if s.dbMgr != nil {
			meta, err := s.dbMgr.GetSignatureFile(context.Background(), bucket, entry.Name())
			if err == nil {
				if meta.Transcript != "" {
					_ = json.Unmarshal([]byte(meta.Transcript), &parsedTranscripts)
				}
				campaignArtist = meta.CampaignArtist
				if meta.Tags != "" {
					_ = json.Unmarshal([]byte(meta.Tags), &parsedTags)
				}
			}
		}

		duration, _ := GetAudioDuration(filepath.Join(dir, entry.Name()))

		chunks = append(chunks, ReviewChunk{
			Name:              entry.Name(),
			Size:              info.Size(),
			ModTime:           info.ModTime(),
			PlayURL:           "/api/signatures/file?bucket=" + url.QueryEscape(bucket) + "&name=" + url.QueryEscape(entry.Name()) + "&t=" + fmt.Sprintf("%d", info.ModTime().Unix()),
			ParsedTranscripts: parsedTranscripts,
			CampaignArtist:    campaignArtist,
			DurationSeconds:   duration.Seconds(),
			ParsedTags:        parsedTags,
		})
	}

	// Sort newest first
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].ModTime.After(chunks[j].ModTime)
	})
	return chunks, nil
}

func (s *TelemetryServer) handleAddCampaignPhrase(c *echo.Context) error {
	type reqBody struct {
		CampaignArtist string `json:"campaign_artist"`
		Phrase         string `json:"phrase"`
	}
	var req reqBody
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	req.Phrase = strings.TrimSpace(req.Phrase)
	req.CampaignArtist = strings.TrimSpace(req.CampaignArtist)
	if req.Phrase == "" || req.CampaignArtist == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "artist and phrase required"})
	}
	if s.dbMgr == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "no db config"})
	}
	if err := s.dbMgr.AddCampaignPhrase(c.Request().Context(), req.CampaignArtist, req.Phrase); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *TelemetryServer) handleDeleteCampaignPhrase(c *echo.Context) error {
	var req struct {
		CampaignArtist string `json:"campaign_artist"`
		Phrase         string `json:"phrase"`
	}
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
	}

	req.CampaignArtist = strings.TrimSpace(req.CampaignArtist)
	req.Phrase = strings.TrimSpace(req.Phrase)

	if req.Phrase == "" || req.CampaignArtist == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "artist and phrase required"})
	}

	if s.dbMgr == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "DB not initialized"})
	}

	if err := s.dbMgr.DeleteCampaignPhrase(c.Request().Context(), req.CampaignArtist, req.Phrase); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func isSafeFilename(name string) bool {
	return name != "" && name != "." && filepath.Base(name) == name
}

func audioMimeType(name string) string {
	if mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); mimeType != "" {
		return mimeType
	}
	return "application/octet-stream"
}

func normalizeSchedulePayload(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("target_matches must be a JSON array of unique ints within 1..6")
	}

	var direct []int
	if err := json.Unmarshal(raw, &direct); err == nil {
		return MarshalSchedule(direct)
	}

	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return "", fmt.Errorf("target_matches must be a JSON array of unique ints within 1..6")
	}

	return NormalizeScheduleJSON(encoded)
}

func formatScheduleTargets(targets []int) string {
	parts := make([]string, 0, len(targets))
	for _, target := range targets {
		parts = append(parts, fmt.Sprintf("%d", target))
	}
	return strings.Join(parts, ", ")
}

func (s *TelemetryServer) handleRemuxAllUnreviewed(c *echo.Context) error {
	unreviewedDir := filepath.Join(s.dataDir, DirSignatures, BucketUnreviewed)
	entries, err := os.ReadDir(unreviewedDir)
	if err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusOK, map[string]interface{}{"status": "success", "count": 0})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".mp3") {
			continue
		}
		path := filepath.Join(unreviewedDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		remuxed, err := RemuxToMP3(data)
		if err == nil {
			_ = os.WriteFile(path, remuxed, 0o644)
			count++
		}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{"status": "success", "count": count})
}

func (s *TelemetryServer) handleTranscribeSignature(c *echo.Context) error {
	var req struct {
		Filename string `json:"filename"`
		Bucket   string `json:"bucket"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	if !isSafeFilename(req.Filename) || !isSafeFilename(req.Bucket) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid filename or bucket"})
	}

	if s.transcribe == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "transcription service not configured"})
	}

	path := filepath.Join(s.dataDir, DirSignatures, req.Bucket, req.Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read file"})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 120*time.Second)
	defer cancel()
	transcript, err := s.transcribe(ctx, data)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "transcription failed: " + err.Error()})
	}

	if s.dbMgr != nil && transcript != "" {
		_ = s.dbMgr.UpdateSignatureTranscript(c.Request().Context(), req.Bucket, req.Filename, transcript)
	}

	return c.JSON(http.StatusOK, map[string]string{"transcript": transcript})
}
