package poller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func newTestAuthManager(t *testing.T) (*AuthManager, *DBManager, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	dbMgr, err := NewDBManager(dbPath)
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	email := "smoke@example.com"
	if err := os.WriteFile(TrustedEmailsFilePath(dbPath), []byte(email+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(trusted emails) error = %v", err)
	}
	return NewAuthManager(dbMgr, nil, "", "admin-pass", ""), dbMgr, email
}

func TestAuthSessionsRejectForgedEmailAndHonorExpiryAndRevocation(t *testing.T) {
	auth, dbMgr, email := newTestAuthManager(t)
	ctx := context.Background()

	token, err := auth.CreateSession(ctx, email)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if token == email || len(token) != 64 {
		t.Fatalf("session token = %q, want opaque 256-bit token", token)
	}
	if got, err := auth.SessionEmail(ctx, token); err != nil || got != email {
		t.Fatalf("SessionEmail(valid token) = %q, %v", got, err)
	}
	if _, err := auth.SessionEmail(ctx, email); err == nil {
		t.Fatal("trusted email must not be accepted as a session token")
	}

	if err := auth.RevokeSession(ctx, token); err != nil {
		t.Fatalf("RevokeSession() error = %v", err)
	}
	if _, err := auth.SessionEmail(ctx, token); err == nil {
		t.Fatal("revoked session must be rejected")
	}

	expired, err := auth.CreateSession(ctx, email)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if _, err := dbMgr.db.ExecContext(ctx, "UPDATE auth_sessions SET expires_at = ? WHERE token = ?", time.Now().Add(-time.Minute), expired); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if _, err := auth.SessionEmail(ctx, expired); err == nil {
		t.Fatal("expired session must be rejected")
	}
}

func TestRequireAuthAndSessionCookie(t *testing.T) {
	auth, _, email := newTestAuthManager(t)
	token, err := auth.CreateSession(context.Background(), email)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	e := echo.New()
	e.GET("/protected", func(c *echo.Context) error { return c.String(http.StatusOK, "ok") }, auth.RequireAuth())

	forged := httptest.NewRequest(http.MethodGet, "/protected", nil)
	forged.AddCookie(&http.Cookie{Name: "session_token", Value: email})
	forgedRec := httptest.NewRecorder()
	e.ServeHTTP(forgedRec, forged)
	if forgedRec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("forged email cookie status = %d, want %d", forgedRec.Code, http.StatusTemporaryRedirect)
	}

	valid := httptest.NewRequest(http.MethodGet, "/protected", nil)
	valid.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	validRec := httptest.NewRecorder()
	e.ServeHTTP(validRec, valid)
	if validRec.Code != http.StatusOK {
		t.Fatalf("opaque session cookie status = %d, want %d", validRec.Code, http.StatusOK)
	}

	t.Setenv("FLY_APP_NAME", "pro-fm-poller")
	cookieRec := httptest.NewRecorder()
	SetSessionCookie(e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), cookieRec), token)
	setCookie := cookieRec.Header().Get("Set-Cookie")
	for _, attribute := range []string{"HttpOnly", "Secure", "SameSite=Lax", "Max-Age="} {
		if !strings.Contains(setCookie, attribute) {
			t.Fatalf("Set-Cookie = %q, missing %s", setCookie, attribute)
		}
	}
}
