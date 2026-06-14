package poller

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// DBManager handles application-specific data stored alongside whatsmeow's data.
type DBManager struct {
	db                *sql.DB
	trustedEmailsPath string
}

func NewDBManager(dbPath string) (*DBManager, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
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
			_ = os.WriteFile(m.trustedEmailsPath, []byte(""), 0644)
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
