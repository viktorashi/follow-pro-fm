package poller

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
	wappClients []WhatsAppClient
	onAddPhone  func(phone string) error
}

func NewTelemetryServer(authMgr *AuthManager, stateMgr *StateManager, broadcaster *SSEBroadcaster, logWriter *SSELogWriter, dbMgr *DBManager, dataDir string) *TelemetryServer {
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
	}

	ts.registerRoutes()
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
	protected.POST("/api/sender/add", s.handleAddSenderPhone)

	if os.Getenv("MOCK_WHATSAPP") == "true" {
		s.echo.POST("/api/test/mock-scan", s.handleMockScan)
	}
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

	SetSessionCookie(c, email)
	c.Response().Header().Set("HX-Redirect", "/")
	return c.Redirect(http.StatusFound, "/")
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

	SetSessionCookie(c, email)
	return c.Redirect(http.StatusFound, "/")
}

func (s *TelemetryServer) handleDashboardView(c *echo.Context) error {
	return Render(c, http.StatusOK, Dashboard())
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
	c.Response().WriteHeader(statusCode)
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTML)
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
			state.Status = StatusPolling
		}
	})

	return c.JSON(http.StatusOK, map[string]string{"status": "success"})
}

type FileInfo struct {
	Name    string
	Path    string
	Size    int64
	ModTime string
	IsDir   bool
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

func (s *TelemetryServer) SetOnAddPhone(fn func(phone string) error) {
	s.onAddPhone = fn
}

func (s *TelemetryServer) handleAddSenderPhone(c *echo.Context) error {
	phone := c.FormValue("phone")
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return c.String(http.StatusBadRequest, "Phone number is required")
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}

	if s.onAddPhone != nil {
		err := s.onAddPhone(phone)
		if err != nil {
			return c.String(http.StatusInternalServerError, "Error adding phone: "+err.Error())
		}
	} else {
		return c.String(http.StatusInternalServerError, "Add phone callback not set")
	}

	return c.String(http.StatusOK, "Phone added successfully. Connecting...")
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
