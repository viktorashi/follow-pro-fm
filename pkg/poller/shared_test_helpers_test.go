package poller

import (
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v5"
)

func mustNewTestDBManager(t *testing.T) *DBManager {
	t.Helper()

	dbMgr, err := NewDBManager(filepath.Join(t.TempDir(), "app.sqlite"))
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	return dbMgr
}

func newTestEcho() *echo.Echo {
	return echo.New()
}
