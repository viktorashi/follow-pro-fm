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
	"github.com/resend/resend-go/v3"
)

type AuthManager struct {
	db           *DBManager
	resendClient *resend.Client
	fromEmail    string
	adminPass    string
	baseURL      string
}

func NewAuthManager(db *DBManager, resendKey, fromEmail, adminPass, baseURL string) *AuthManager {
	var rc *resend.Client
	if resendKey != "" {
		rc = resend.NewClient(resendKey)
	}
	return &AuthManager{
		db:           db,
		resendClient: rc,
		fromEmail:    fromEmail,
		adminPass:    adminPass,
		baseURL:      baseURL,
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

	if a.resendClient == nil {
		return fmt.Errorf("resend not configured")
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

	params := &resend.SendEmailRequest{
		From:    a.fromEmail,
		To:      []string{email},
		Subject: "Your Magic Link - ProFM Poller",
		Html:    fmt.Sprintf("<p>Click the link below to login instantly:</p><p><a href='%s'>Login</a></p>", magicURL),
	}

	_, err = a.resendClient.Emails.Send(params)
	return err
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
	a.db.db.ExecContext(ctx, "DELETE FROM auth_tokens WHERE token = ?", token)

	if time.Now().After(expiresAt) {
		return "", fmt.Errorf("token expired")
	}

	trusted, _ := a.db.IsTrustedEmail(ctx, email)
	if !trusted {
		return "", echo.ErrUnauthorized
	}

	return email, nil
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
			
			// For simplicity, we just use the email as the cookie value since it's a personal app.
			// In a real app, this should be a signed JWT or session ID.
			// We check if the email in the cookie is trusted.
			email := cookie.Value
			trusted, err := a.db.IsTrustedEmail(c.Request().Context(), email)
			if err != nil || !trusted {
				return c.Redirect(http.StatusTemporaryRedirect, "/login")
			}

			c.Set("user_email", email)
			return next(c)
		}
	}
}

// SetSessionCookie helper
func SetSessionCookie(c *echo.Context, email string) {
	cookie := new(http.Cookie)
	cookie.Name = "session_token"
	cookie.Value = email
	cookie.Expires = time.Now().Add(24 * 7 * time.Hour) // 1 week
	cookie.Path = "/"
	cookie.HttpOnly = true
	
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
	cookie.Path = "/"
	c.SetCookie(cookie)
}
