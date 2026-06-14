package poller

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"

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
}

func NewTelemetryServer(authMgr *AuthManager, stateMgr *StateManager, broadcaster *SSEBroadcaster, logWriter *SSELogWriter) *TelemetryServer {
	e := echo.New()

	if logWriter == nil {
		logWriter = NewSSELogWriter(os.Stdout, broadcaster) // fallback
	}

	// Use modern slog to the log writer
	logger := slog.New(slog.NewJSONHandler(logWriter, nil))
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			logger.Info("request",
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
	}

	ts.registerRoutes()
	return ts
}

func (s *TelemetryServer) registerRoutes() {
	// Public routes
	s.echo.GET("/login", s.handleLoginView)
	s.echo.POST("/login", s.handleLoginSubmit)
	s.echo.POST("/auth/magic/request", s.handleMagicLinkRequest)
	s.echo.GET("/auth/magic", s.handleMagicLinkVerify)
	s.echo.GET("/qr.png", s.handleQRImage) // New unauthenticated QR endpoint for email

	// Protected routes
	protected := s.echo.Group("", s.authMgr.RequireAuth())
	protected.GET("/", s.handleDashboardView)
	protected.GET("/logs", s.handleLogsView)
	protected.GET("/events/dashboard", s.handleDashboardStream)
	protected.GET("/events/logs", s.handleLogsStream)
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

func (s *TelemetryServer) handleQRImage(c *echo.Context) error {
	state := s.stateMgr.Get()
	b64 := state.QRCodeData
	if b64 == "" || state.Status != StatusPairingRequired {
		// Return 404 or a placeholder if no QR is needed
		return c.String(http.StatusNotFound, "No QR Code active")
	}

	prefix := "data:image/png;base64,"
	if len(b64) > len(prefix) {
		b64 = b64[len(prefix):]
	}

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
	}

	ch := s.broadcaster.Subscribe()
	defer s.broadcaster.Unsubscribe(ch)

	for {
		select {
		case <-c.Request().Context().Done():
			return nil
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
