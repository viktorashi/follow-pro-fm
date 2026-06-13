package poller

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// DBManager handles application-specific data stored alongside whatsmeow's data.
type DBManager struct {
	db *sql.DB
}

func NewDBManager(dbPath string) (*DBManager, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := initSchema(db); err != nil {
		return nil, err
	}

	return &DBManager{db: db}, nil
}

func initSchema(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS trusted_emails (
			email TEXT PRIMARY KEY
		);`,
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

func (m *DBManager) AddTrustedEmail(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := m.db.ExecContext(ctx, "INSERT OR IGNORE INTO trusted_emails (email) VALUES (?)", email)
	return err
}

func (m *DBManager) RemoveTrustedEmail(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	_, err := m.db.ExecContext(ctx, "DELETE FROM trusted_emails WHERE email = ?", email)
	return err
}

func (m *DBManager) IsTrustedEmail(ctx context.Context, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var count int
	err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM trusted_emails WHERE email = ?", email).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (m *DBManager) GetAllTrustedEmails(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT email FROM trusted_emails ORDER BY email ASC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var emails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}
	return emails, rows.Err()
}
