package poller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsTrustedEmail(t *testing.T) {
	tempDir := t.TempDir()
	trustedPath := filepath.Join(tempDir, "trusted-emails.txt")

	// Initially file does not exist
	m := &DBManager{
		trustedEmailsPath: trustedPath,
	}

	ctx := context.Background()

	// Should return false and create empty file
	ok, err := m.IsTrustedEmail(ctx, "test@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false for missing file")
	}

	// Verify file was created
	if _, err := os.Stat(trustedPath); os.IsNotExist(err) {
		t.Error("expected trusted-emails.txt to be created")
	}

	// Write some emails to it
	err = os.WriteFile(trustedPath, []byte("admin@example.com\nUSER@test.com\n"), 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tests := []struct {
		email string
		want  bool
	}{
		{"admin@example.com", true},
		{"Admin@Example.com", true}, // Case insensitive
		{"user@test.com", true},     // Handled properly
		{"unknown@test.com", false},
		{"", false},
		{"   admin@example.com  ", true}, // Whitespace trimmed
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			got, err := m.IsTrustedEmail(ctx, tt.email)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("IsTrustedEmail(%q) = %v, want %v", tt.email, got, tt.want)
			}
		})
	}
}
