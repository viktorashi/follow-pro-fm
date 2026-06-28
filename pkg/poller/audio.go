package poller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	CanonicalSenderPhone           = "+40734788254"
	CanonicalSenderPhoneNormalized = "40734788254"
)

func NormalizePhone(phone string) string {
	normalized := strings.ReplaceAll(phone, " ", "")
	normalized = strings.ReplaceAll(normalized, "+", "")
	return normalized
}

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

func HashAudioFile(audioPath string) (string, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return "", fmt.Errorf("failed to open audio file for hashing: %w", err)
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", fmt.Errorf("failed to hash audio file: %w", err)
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func GetRandomAvailableAudio(audiosDir string, isHashUsed func(string) (bool, error)) (string, string, error) {
	entries, err := os.ReadDir(audiosDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to read audios directory: %w", err)
	}

	var candidates []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".ogg") {
			candidates = append(candidates, filepath.Join(audiosDir, e.Name()))
		}
	}

	if len(candidates) == 0 {
		return "", "", fmt.Errorf("audio pool exhausted")
	}

	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	for _, candidate := range candidates {
		contentHash, err := HashAudioFile(candidate)
		if err != nil {
			return "", "", err
		}
		if isHashUsed == nil {
			return candidate, contentHash, nil
		}

		used, err := isHashUsed(contentHash)
		if err != nil {
			return "", "", err
		}
		if !used {
			return candidate, contentHash, nil
		}
	}

	return "", "", fmt.Errorf("audio pool exhausted")
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

	// Update the file's modification time so the user knows exactly when it was used
	now := time.Now()
	_ = os.Chtimes(newPath, now, now)

	return nil
}

func MarkAudioUsedByHash(rootDir, sentAudioPath, contentHash string) error {
	files, err := listActiveAudioFiles(rootDir)
	if err != nil {
		return err
	}

	for _, audioPath := range files {
		fileHash, err := HashAudioFile(audioPath)
		if err != nil {
			return err
		}
		if fileHash != contentHash {
			continue
		}
		if err := MarkAudioUsed(audioPath); err != nil {
			return err
		}
	}

	if _, err := os.Stat(sentAudioPath); err == nil {
		return MarkAudioUsed(sentAudioPath)
	}

	return nil
}

func ReconcileAudioUsage(rootDir string, dbMgr *DBManager) error {
	if dbMgr == nil {
		return nil
	}

	ctx := context.Background()
	usedFiles, err := listUsedAudioFiles(rootDir)
	if err != nil {
		return err
	}
	for _, audioPath := range usedFiles {
		contentHash, err := HashAudioFile(audioPath)
		if err != nil {
			return err
		}
		if err := dbMgr.RegisterUsedAudioHash(ctx, contentHash, time.Now()); err != nil {
			return err
		}
	}

	activeFiles, err := listActiveAudioFiles(rootDir)
	if err != nil {
		return err
	}
	for _, audioPath := range activeFiles {
		contentHash, err := HashAudioFile(audioPath)
		if err != nil {
			return err
		}
		used, err := dbMgr.IsAudioHashUsed(ctx, contentHash)
		if err != nil {
			return err
		}
		if used {
			if err := MarkAudioUsed(audioPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func listActiveAudioFiles(rootDir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "used" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".ogg") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func listUsedAudioFiles(rootDir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "used" && strings.HasSuffix(strings.ToLower(d.Name()), ".ogg") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func GetAudioDirForPhone(phone string, rootDir string) string {
	normalized := NormalizePhone(phone)
	if normalized == CanonicalSenderPhoneNormalized {
		return rootDir
	}
	// ensure the directory exists
	dir := filepath.Join(rootDir, normalized)
	_ = InitAudioPool(dir)
	return dir
}

func GetTotalAudioStats(conns []WAConnectionState, rootDir string) (int, int) {
	totalUnused := 0
	totalUsed := 0
	for _, conn := range conns {
		dir := GetAudioDirForPhone(conn.Phone, rootDir)
		u, us := GetAudioStats(dir)
		totalUnused += u
		totalUsed += us
	}
	return totalUnused, totalUsed
}
