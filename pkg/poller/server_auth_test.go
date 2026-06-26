package poller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestHandleLogoutClearsSessionAndRedirects(t *testing.T) {
	server := &TelemetryServer{}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(&http.Cookie{
		Name:  "session_token",
		Value: "smoke@example.com",
		Path:  "/",
	})
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := server.handleLogout(ctx); err != nil {
		t.Fatalf("handleLogout() error = %v", err)
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if location := rec.Header().Get("Location"); location != "/login" {
		t.Fatalf("Location = %q, want /login", location)
	}
	if hxRedirect := rec.Header().Get("HX-Redirect"); hxRedirect != "/login" {
		t.Fatalf("HX-Redirect = %q, want /login", hxRedirect)
	}

	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "session_token=") {
		t.Fatalf("Set-Cookie = %q, want session_token cookie", setCookie)
	}
	if !strings.Contains(setCookie, "Path=/") {
		t.Fatalf("Set-Cookie = %q, want cookie path", setCookie)
	}
}
