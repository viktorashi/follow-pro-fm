package poller

import (
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
}

func NewTelemetryServer(authMgr *AuthManager, stateMgr *StateManager, broadcaster *SSEBroadcaster) *TelemetryServer {
	e := echo.New()

	// Use modern slog
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
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

	// Protected routes
	protected := s.echo.Group("", s.authMgr.RequireAuth())
	protected.GET("/", s.handleDashboardView)
	protected.GET("/sse", s.handleSSE)
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

func (s *TelemetryServer) handleSSE(c *echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
	c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
	c.Response().Header().Set(echo.HeaderConnection, "keep-alive")

	ch := s.broadcaster.Subscribe()
	defer s.broadcaster.Unsubscribe(ch)

	for {
		select {
		case <-c.Request().Context().Done():
			return nil
		case ev := <-ch:
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
