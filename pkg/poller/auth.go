package poller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

type AuthManager struct {
	db             *DBManager
	sendgridClient *sendgrid.Client
	fromEmail      string
	adminPass      string
	baseURL        string
}

const sessionDuration = 7 * 24 * time.Hour

func NewAuthManager(db *DBManager, sendgridKey, fromEmail, adminPass, baseURL string) *AuthManager {
	var sc *sendgrid.Client
	if sendgridKey != "" {
		sc = sendgrid.NewSendClient(sendgridKey)
	}
	return &AuthManager{
		db:             db,
		sendgridClient: sc,
		fromEmail:      fromEmail,
		adminPass:      adminPass,
		baseURL:        baseURL,
	}
}

// CheckLogin verifies if the user can log in via password.
func (a *AuthManager) CheckLogin(ctx context.Context, email, password string) error {
	trusted, err := a.db.IsTrustedEmail(ctx, email)
	if err != nil {
		return err
	}
	if !trusted {
		return echo.ErrUnauthorized
	}

	if password != a.adminPass {
		return echo.ErrUnauthorized
	}
	return nil
}

// GenerateAndSendMagicLink creates a token and emails it to the user.
func (a *AuthManager) GenerateAndSendMagicLink(ctx context.Context, email string) error {
	trusted, err := a.db.IsTrustedEmail(ctx, email)
	if err != nil {
		return err
	}
	if !trusted {
		return fmt.Errorf("email not trusted")
	}

	if a.sendgridClient == nil {
		return fmt.Errorf("sendgrid not configured")
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := hex.EncodeToString(b)
	expiresAt := time.Now().Add(15 * time.Minute)

	_, err = a.db.db.ExecContext(ctx, "INSERT INTO auth_tokens (token, email, expires_at) VALUES (?, ?, ?)", token, email, expiresAt)
	if err != nil {
		return err
	}

	magicURL := fmt.Sprintf("%s/auth/magic?token=%s", a.baseURL, token)

	from := mail.NewEmail("ProFM Poller", a.fromEmail)
	subject := "Your Magic Link - ProFM Poller"
	to := mail.NewEmail("", email)
	htmlContent := fmt.Sprintf("<p>Click the link below to login instantly:</p><p><a href='%s'>Login</a></p>", magicURL)
	message := mail.NewSingleEmail(from, subject, to, "", htmlContent)

	response, err := a.sendgridClient.Send(message)
	if err != nil {
		return err
	}

	if response.StatusCode >= 400 {
		return fmt.Errorf("sendgrid returned status %d: %s", response.StatusCode, response.Body)
	}

	return nil
}

// VerifyMagicLink consumes the token and returns the associated email.
func (a *AuthManager) VerifyMagicLink(ctx context.Context, token string) (string, error) {
	var email string
	var expiresAt time.Time

	err := a.db.db.QueryRowContext(ctx, "SELECT email, expires_at FROM auth_tokens WHERE token = ?", token).Scan(&email, &expiresAt)
	if err != nil {
		return "", echo.ErrUnauthorized
	}

	// Delete token so it can only be used once
	_, _ = a.db.db.ExecContext(ctx, "DELETE FROM auth_tokens WHERE token = ?", token)

	if time.Now().After(expiresAt) {
		return "", fmt.Errorf("token expired")
	}

	trusted, err := a.db.IsTrustedEmail(ctx, email)
	if err != nil || !trusted {
		return "", echo.ErrUnauthorized
	}

	return email, nil
}

func (a *AuthManager) CreateSession(ctx context.Context, email string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	_, err := a.db.db.ExecContext(ctx,
		"INSERT INTO auth_sessions (token, email, expires_at) VALUES (?, ?, ?)",
		token, email, time.Now().Add(sessionDuration),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (a *AuthManager) SessionEmail(ctx context.Context, token string) (string, error) {
	var email string
	var expiresAt time.Time
	err := a.db.db.QueryRowContext(ctx, "SELECT email, expires_at FROM auth_sessions WHERE token = ?", token).Scan(&email, &expiresAt)
	if err != nil || time.Now().After(expiresAt) {
		_ = a.RevokeSession(ctx, token)
		return "", echo.ErrUnauthorized
	}

	trusted, err := a.db.IsTrustedEmail(ctx, email)
	if err != nil || !trusted {
		return "", echo.ErrUnauthorized
	}
	return email, nil
}

func (a *AuthManager) RevokeSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	_, err := a.db.db.ExecContext(ctx, "DELETE FROM auth_sessions WHERE token = ?", token)
	return err
}

// Session middleware for Echo v5
func (a *AuthManager) RequireAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			cookie, err := c.Cookie("session_token")
			if err != nil || cookie.Value == "" {
				// If HTMX request, we can send a redirect header
				if c.Request().Header.Get("HX-Request") == "true" {
					c.Response().Header().Set("HX-Redirect", "/login")
					return c.NoContent(http.StatusUnauthorized)
				}
				return c.Redirect(http.StatusTemporaryRedirect, "/login")
			}

			email, err := a.SessionEmail(c.Request().Context(), cookie.Value)
			if err != nil {
				return c.Redirect(http.StatusTemporaryRedirect, "/login")
			}

			c.Set("user_email", email)
			return next(c)
		}
	}
}

func SetSessionCookie(c *echo.Context, token string) {
	cookie := new(http.Cookie)
	cookie.Name = "session_token"
	cookie.Value = token
	cookie.Expires = time.Now().Add(sessionDuration)
	cookie.MaxAge = int(sessionDuration.Seconds())
	cookie.Path = "/"
	cookie.HttpOnly = true
	cookie.SameSite = http.SameSiteLaxMode

	// If running over HTTPS (like on Fly.io), set Secure
	if os.Getenv("FLY_APP_NAME") != "" {
		cookie.Secure = true
	}

	c.SetCookie(cookie)
}

// ClearSessionCookie helper
func ClearSessionCookie(c *echo.Context) {
	cookie := new(http.Cookie)
	cookie.Name = "session_token"
	cookie.Value = ""
	cookie.Expires = time.Now().Add(-1 * time.Hour)
	cookie.MaxAge = -1
	cookie.Path = "/"
	cookie.HttpOnly = true
	cookie.SameSite = http.SameSiteLaxMode
	c.SetCookie(cookie)
}
