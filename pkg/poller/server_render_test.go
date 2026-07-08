package poller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
)

func TestRenderSetsHTMLContentTypeBeforeWrite(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	component := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := w.Write([]byte("<div>ok</div>"))
		return err
	})

	if err := Render(ctx, http.StatusCreated, component); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Header().Get(echo.HeaderContentType); got != echo.MIMETextHTML {
		t.Fatalf("Content-Type = %q, want %q", got, echo.MIMETextHTML)
	}
	if body := rec.Body.String(); body != "<div>ok</div>" {
		t.Fatalf("body = %q, want rendered HTML", body)
	}
}
