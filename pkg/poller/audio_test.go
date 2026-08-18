package poller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReconcileAudioUsagePersistsHistoricalHashesAndMovesActiveDuplicates(t *testing.T) {
	rootDir := t.TempDir()
	usedDir := filepath.Join(rootDir, "used")
	phoneDir := filepath.Join(rootDir, "40700000000")
	if err := os.MkdirAll(usedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(phoneDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	duplicateContent := []byte("same-audio-content")
	if err := os.WriteFile(filepath.Join(usedDir, "already-used.ogg"), duplicateContent, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	activeDuplicate := filepath.Join(phoneDir, "duplicate.ogg")
	if err := os.WriteFile(activeDuplicate, duplicateContent, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	activeUnique := filepath.Join(phoneDir, "unique.ogg")
	if err := os.WriteFile(activeUnique, []byte("fresh-audio-content"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	dbPath := filepath.Join(rootDir, "app.sqlite")
	dbMgr, err := NewDBManager(dbPath)
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	if err := ReconcileAudioUsage(rootDir, dbMgr); err != nil {
		t.Fatalf("ReconcileAudioUsage() error = %v", err)
	}

	duplicateHash, err := HashAudioFile(filepath.Join(usedDir, "already-used.ogg"))
	if err != nil {
		t.Fatalf("HashAudioFile() error = %v", err)
	}
	used, err := dbMgr.IsAudioHashUsed(context.Background(), duplicateHash)
	if err != nil {
		t.Fatalf("IsAudioHashUsed() error = %v", err)
	}
	if !used {
		t.Fatalf("Expected used audio hash to be persisted in the database")
	}

	if _, err := os.Stat(activeDuplicate); !os.IsNotExist(err) {
		t.Fatalf("Expected active duplicate to be moved out of the active pool, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(phoneDir, "used", "duplicate.ogg")); err != nil {
		t.Fatalf("Expected duplicate to be moved into the sender used/ directory, err = %v", err)
	}
	if _, err := os.Stat(activeUnique); err != nil {
		t.Fatalf("Expected unique audio to remain active, err = %v", err)
	}
}

func TestGetRandomAvailableAudioSkipsGloballyUsedContentHashes(t *testing.T) {
	rootDir := t.TempDir()
	audioPath := filepath.Join(rootDir, "dup.ogg")
	if err := os.WriteFile(audioPath, []byte("duplicate-content"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	dbPath := filepath.Join(rootDir, "app.sqlite")
	dbMgr, err := NewDBManager(dbPath)
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	contentHash, err := HashAudioFile(audioPath)
	if err != nil {
		t.Fatalf("HashAudioFile() error = %v", err)
	}
	if err := dbMgr.RegisterUsedAudioHash(context.Background(), contentHash, time.Now()); err != nil {
		t.Fatalf("RegisterUsedAudioHash() error = %v", err)
	}

	_, _, err = GetRandomAvailableAudio(rootDir, func(hash string) (bool, error) {
		return dbMgr.IsAudioHashUsed(context.Background(), hash)
	})
	if err == nil {
		t.Fatalf("Expected audio pool to be exhausted when only globally used content remains")
	}
}

func TestMoveAudioFilesRejectsDestinationCollisions(t *testing.T) {
	for _, used := range []bool{false, true} {
		name := "active"
		if used {
			name = "used"
		}
		t.Run(name, func(t *testing.T) {
			sourceDir := filepath.Join(t.TempDir(), "source")
			targetDir := filepath.Join(t.TempDir(), "target")
			if err := InitAudioPool(sourceDir); err != nil {
				t.Fatal(err)
			}
			if err := InitAudioPool(targetDir); err != nil {
				t.Fatal(err)
			}
			if used {
				sourceDir = filepath.Join(sourceDir, "used")
				targetDir = filepath.Join(targetDir, "used")
			}
			filename := "voice.ogg"
			sourcePath := filepath.Join(sourceDir, filename)
			targetPath := filepath.Join(targetDir, filename)
			if err := os.WriteFile(sourcePath, []byte("source"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(targetPath, []byte("target"), 0o644); err != nil {
				t.Fatal(err)
			}

			moveSource := sourceDir
			moveTarget := targetDir
			if used {
				moveSource = filepath.Dir(sourceDir)
				moveTarget = filepath.Dir(targetDir)
			}
			if err := MoveAudioFiles(moveSource, moveTarget, []string{filename}); err == nil {
				t.Fatal("MoveAudioFiles() overwrote an existing destination")
			}
			if got, _ := os.ReadFile(sourcePath); string(got) != "source" {
				t.Fatalf("source changed after collision: %q", got)
			}
			if got, _ := os.ReadFile(targetPath); string(got) != "target" {
				t.Fatalf("destination was overwritten: %q", got)
			}
		})
	}
}
