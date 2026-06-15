package poller

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DBManager handles application-specific data stored alongside whatsmeow's data.
type DBManager struct {
	db                *sql.DB
	trustedEmailsPath string
}

func NewDBManager(dbPath string) (*DBManager, error) {
	dsn := "file:" + dbPath
	if strings.Contains(dbPath, "?") {
		dsn += "&_foreign_keys=on"
	} else {
		dsn += "?_foreign_keys=on"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;"); err != nil {
		return nil, fmt.Errorf("failed to set pragmas: %w", err)
	}
	if err := initSchema(db); err != nil {
		return nil, err
	}

	return &DBManager{
		db:                db,
		trustedEmailsPath: filepath.Join(filepath.Dir(dbPath), "trusted-emails.txt"),
	}, nil
}

func initSchema(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS auth_tokens (
			token TEXT PRIMARY KEY,
			email TEXT NOT NULL,
			expires_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS played_songs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			artist TEXT NOT NULL,
			title TEXT NOT NULL,
			played_datetime TEXT NOT NULL,
			UNIQUE(artist, title, played_datetime)
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("failed to init schema: %w", err)
		}
	}
	return nil
}

func (m *DBManager) IsTrustedEmail(ctx context.Context, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false, nil
	}

	data, err := os.ReadFile(m.trustedEmailsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create the file empty if it doesn't exist
			_ = os.WriteFile(m.trustedEmailsPath, []byte(""), 0o644)
			return false, nil
		}
		return false, fmt.Errorf("failed to read trusted emails file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.ToLower(strings.TrimSpace(line))
		if line == email {
			return true, nil
		}
	}

	return false, nil
}

func (m *DBManager) HasSongPlayedTheLastHalfHour(ctx context.Context, artist, title string, date time.Time) (bool, error) {
	dateStr := date.Format("2006-01-02 15:04")
	var id int
	err := m.db.QueryRowContext(ctx, "SELECT id FROM played_songs WHERE artist = ? AND title = ? AND played_datetime >= datetime(?, '-30 minutes')", artist, title, dateStr).Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RecordSongPlay records that a song was played today.
func (m *DBManager) RecordSongPlay(ctx context.Context, artist, title string, date time.Time) error {
	dateStr := date.Format("2006-01-02 15:04")
	_, err := m.db.ExecContext(ctx, "INSERT OR IGNORE INTO played_songs (artist, title, played_datetime) VALUES (?, ?, ?)", artist, title, dateStr)
	return err
}

// SetKillSwitch updates the kill switch state in the database
func (m *DBManager) SetKillSwitch(ctx context.Context, active bool) error {
	valStr := "false"
	if active {
		valStr = "true"
	}
	_, err := m.db.ExecContext(ctx, "INSERT OR REPLACE INTO app_settings (key, value) VALUES ('kill_switch', ?)", valStr)
	return err
}

// IsKillSwitchActive reads the kill switch state from the database
func (m *DBManager) IsKillSwitchActive(ctx context.Context) (bool, error) {
	var valStr string
	err := m.db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key = 'kill_switch'").Scan(&valStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return valStr == "true", nil
}
