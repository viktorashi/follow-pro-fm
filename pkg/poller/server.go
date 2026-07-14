package poller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// TelemetryServer runs the embedded HTTP dashboard.
type TelemetryServer struct {
	echo        *echo.Echo
	authMgr     *AuthManager
	stateMgr    *StateManager
	broadcaster *SSEBroadcaster
	logWriter   *SSELogWriter
	dbMgr       *DBManager
	dataDir     string
	audiosDir   string
	campaigns   []Campaign
	wappClients []WhatsAppClient
	onAddPhone  func() error
	timeNow     func() time.Time
}

type ScheduleEntry struct {
	Date          string
	TargetMatches []int
}

func NewTelemetryServer(authMgr *AuthManager, stateMgr *StateManager, broadcaster *SSEBroadcaster, logWriter *SSELogWriter, dbMgr *DBManager, dataDir string, audiosDir string, campaigns []Campaign) *TelemetryServer {
	e := echo.New()

	if logWriter == nil {
		logWriter = NewSSELogWriter(os.Stdout, broadcaster) // fallback
	}

	// Use modern slog to the log writer
	logger := slog.New(slog.NewJSONHandler(logWriter, nil))
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			logger.Info(
				"request",
				slog.String("URI", v.URI),
				slog.Int("status", v.Status),
			)
			return nil
		},
	}))
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
	}

	ts.registerRoutes()
	e.POST("/api/campaigns/phrases", ts.handleAddCampaignPhrase, ts.authMgr.RequireAuth())

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
	protected.GET("/radio-logs", s.handleRadioLogsView)
	protected.GET("/data", s.handleDataView)
	protected.Static("/raw-data", s.dataDir)
	protected.GET("/events/dashboard", s.handleDashboardStream)
	protected.GET("/events/logs", s.handleLogsStream)
	protected.POST("/api/kill-switch", s.handleKillSwitch)
	protected.GET("/api/schedule", s.handleGetSchedule)
	protected.POST("/api/schedule", s.handleSetSchedule)
	protected.POST("/api/sender/add", s.handleAddSenderPhone)
	protected.POST("/api/audio/upload", s.handleAudioUpload)

	if os.Getenv("MOCK_WHATSAPP") == "true" {
		protected.POST("/api/test/mock-scan", s.handleMockScan)
	}
	protected.POST("/api/settings/gathering", s.handleToggleGathering)
	protected.GET("/api/unreviewed", s.handleUnreviewedList)
	protected.GET("/api/unreviewed/file", s.handleUnreviewedFile)
	protected.POST("/unreviewed/crop", s.handleUnreviewedCrop)
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
	chunks, err := s.listUnreviewedChunks()
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error reading unreviewed signatures: "+err.Error())
	}
	schedules, err := s.listScheduleEntries(c.Request().Context())
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error reading schedule entries: "+err.Error())
	}
	return Render(c, http.StatusOK, Dashboard(state, chunks, dashboardUploadPhones(state.Connections), schedules))
}

func (s *TelemetryServer) handleLogsView(c *echo.Context) error {
	return Render(c, http.StatusOK, LogsPage())
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
		_ = AudioStatsComponent(state.UnusedAudios, state.UsedAudios).Render(c.Request().Context(), &audioBuf)
		_, _ = c.Response().Write((&SSEEvent{Event: "audio", Data: audioBuf.Bytes()}).Marshal())

		var qrBuf bytes.Buffer
		_ = QRComponent(state.Connections).Render(c.Request().Context(), &qrBuf)
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
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	ModTime        string `json:"mod_time"`
	PlayURL        string `json:"play_url"`
	Transcript     string `json:"transcript"`
	CampaignArtist string `json:"campaign_artist"`
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

func (s *TelemetryServer) SetOnAddPhone(fn func() error) {
	s.onAddPhone = fn
}

func (s *TelemetryServer) handleAddSenderPhone(c *echo.Context) error {
	if s.onAddPhone != nil {
		err := s.onAddPhone()
		if err != nil {
			return c.String(http.StatusConflict, "Could not start QR pairing: "+err.Error())
		}
	} else {
		return c.String(http.StatusInternalServerError, "Add phone callback not set")
	}

	return c.String(http.StatusOK, "QR pairing started. Scan the code above.")
}

func (s *TelemetryServer) handleAudioUpload(c *echo.Context) error {
	phone := strings.TrimSpace(c.FormValue("phone"))
	if phone == "" {
		return c.String(http.StatusBadRequest, "Phone is required")
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}

	fileHeader, err := c.FormFile("audio")
	if err != nil {
		return c.String(http.StatusBadRequest, "Audio file is required")
	}

	filename := filepath.Base(fileHeader.Filename)
	if filename == "." || filename == "" {
		return c.String(http.StatusBadRequest, "Invalid filename")
	}
	if !strings.EqualFold(filepath.Ext(filename), ".ogg") {
		return c.String(http.StatusBadRequest, "Only .ogg files are allowed")
	}

	audioDir := GetAudioDirForPhone(phone, s.audiosDir)
	activePath := filepath.Join(audioDir, filename)
	usedPath := filepath.Join(audioDir, "used", filename)

	if _, err := os.Stat(activePath); err == nil {
		return c.String(http.StatusConflict, "An active audio with that filename already exists")
	} else if !os.IsNotExist(err) {
		return c.String(http.StatusInternalServerError, "Failed to inspect audio pool")
	}

	if _, err := os.Stat(usedPath); err == nil {
		return c.String(http.StatusConflict, "A used audio with that filename already exists")
	} else if !os.IsNotExist(err) {
		return c.String(http.StatusInternalServerError, "Failed to inspect used audio pool")
	}

	src, err := fileHeader.Open()
	if err != nil {
		return c.String(http.StatusBadRequest, "Failed to read uploaded file")
	}
	defer func() {
		_ = src.Close()
	}()

	if err := os.MkdirAll(audioDir, 0755); err != nil {
		return c.String(http.StatusInternalServerError, "Failed to prepare audio directory")
	}

	dst, err := os.OpenFile(activePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			return c.String(http.StatusConflict, "An active audio with that filename already exists")
		}
		return c.String(http.StatusInternalServerError, "Failed to create destination file")
	}

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(activePath)
		return c.String(http.StatusInternalServerError, "Failed to save uploaded file")
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(activePath)
		return c.String(http.StatusInternalServerError, "Failed to finalize uploaded file")
	}

	return c.String(http.StatusOK, "Upload successful: "+filename+" -> "+phone)
}

func dashboardUploadPhones(conns []WAConnectionState) []string {
	seen := map[string]struct{}{
		CanonicalSenderPhone: {},
	}
	phones := []string{CanonicalSenderPhone}

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

	sort.Slice(phones[1:], func(i, j int) bool {
		return phones[1:][i] < phones[1:][j]
	})

	return phones
}

func isKnownDashboardPhone(phone string, conns []WAConnectionState) bool {
	normalized := NormalizePhone(phone)
	for _, candidate := range dashboardUploadPhones(conns) {
		if NormalizePhone(candidate) == normalized {
			return true
		}
	}
	return false
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
	chunks, err := s.listUnreviewedChunks()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, chunks)
}

func (s *TelemetryServer) handleUnreviewedFile(c *echo.Context) error {
	filename := c.QueryParam("name")
	if !isSafeFilename(filename) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid filename"})
	}

	path := filepath.Join(s.dataDir, "signatures", "unreviewed", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "file not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filename))
	return c.Blob(http.StatusOK, audioMimeType(filename), data)
}

func (s *TelemetryServer) handleUnreviewedCrop(c *echo.Context) error {
	filename := c.FormValue("filename")
	if !isSafeFilename(filename) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid filename"})
	}
	var startBytes, endBytes int
	_, _ = fmt.Sscanf(c.FormValue("start_bytes"), "%d", &startBytes)
	_, _ = fmt.Sscanf(c.FormValue("end_bytes"), "%d", &endBytes)

	unreviewedDir := filepath.Join(s.dataDir, "signatures", "unreviewed")
	canonicalDir := filepath.Join(s.dataDir, "signatures", "canonical")

	err := CropAndMarkCanonical(unreviewedDir, canonicalDir, filename, startBytes, endBytes)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if s.dbMgr != nil {
		if err := s.dbMgr.CopySignatureFile(c.Request().Context(), "unreviewed", "canonical", filename); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

func (s *TelemetryServer) listUnreviewedChunks() ([]ReviewChunk, error) {
	unreviewedDir := filepath.Join(s.dataDir, "signatures", "unreviewed")
	entries, err := os.ReadDir(unreviewedDir)
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

		var transcript string
		var campaignArtist string
		if s.dbMgr != nil {
			meta, err := s.dbMgr.GetSignatureFile(context.Background(), "unreviewed", entry.Name())
			if err == nil {
				transcript = meta.Transcript
				campaignArtist = meta.CampaignArtist
			}
		}

		chunks = append(chunks, ReviewChunk{
			Name:           entry.Name(),
			Size:           info.Size(),
			ModTime:        info.ModTime().Format("2006-01-02 15:04:05"),
			PlayURL:        "/api/unreviewed/file?name=" + url.QueryEscape(entry.Name()),
			Transcript:     transcript,
			CampaignArtist: campaignArtist,
		})
	}

	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].ModTime > chunks[j].ModTime
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
	if req.Phrase == "" || req.CampaignArtist == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "artist and phrase required"})
	}
	if s.dbMgr == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "no db config"})
	}
	if err := s.dbMgr.AddCampaignPhrase(c.Request().Context(), req.CampaignArtist, req.Phrase); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.NoContent(http.StatusOK)
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
