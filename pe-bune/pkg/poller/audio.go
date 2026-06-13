package poller

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

// InitAudioPool ensures the used directory exists.
func InitAudioPool(audiosDir string) error {
	usedDir := filepath.Join(audiosDir, "used")
	return os.MkdirAll(usedDir, 0755)
}

// GetAudioStats returns the count of unused and used audio files.
func GetAudioStats(audiosDir string) (int, int) {
	unused := 0
	used := 0

	// Count unused
	entries, err := os.ReadDir(audiosDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".ogg") {
				unused++
			}
		}
	}

	// Count used
	usedEntries, err := os.ReadDir(filepath.Join(audiosDir, "used"))
	if err == nil {
		for _, e := range usedEntries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".ogg") {
				used++
			}
		}
	}

	return unused, used
}

// GetRandomAudio returns the absolute path to a random unused audio file.
// Returns an error if the pool is exhausted.
func GetRandomAudio(audiosDir string) (string, error) {
	entries, err := os.ReadDir(audiosDir)
	if err != nil {
		return "", fmt.Errorf("failed to read audios directory: %w", err)
	}

	var unusedFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".ogg") {
			unusedFiles = append(unusedFiles, e.Name())
		}
	}

	if len(unusedFiles) == 0 {
		return "", fmt.Errorf("audio pool exhausted")
	}

	selected := unusedFiles[rand.Intn(len(unusedFiles))]
	return filepath.Join(audiosDir, selected), nil
}

// MarkAudioUsed moves the specified audio file into the used/ subdirectory.
func MarkAudioUsed(audioPath string) error {
	dir := filepath.Dir(audioPath)
	base := filepath.Base(audioPath)
	usedDir := filepath.Join(dir, "used")

	if err := os.MkdirAll(usedDir, 0755); err != nil {
		return fmt.Errorf("failed to create used directory: %w", err)
	}

	newPath := filepath.Join(usedDir, base)
	if err := os.Rename(audioPath, newPath); err != nil {
		return fmt.Errorf("failed to move audio to used folder: %w", err)
	}

	return nil
}
