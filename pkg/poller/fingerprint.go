package poller

import (
	"bytes"
	"fmt"

	"os"
	"path/filepath"
)

// MatchSignature checks if a signature exists within an audio stream.
// This is a naive byte-matching algorithm suitable for test fixtures.
// In a real-world scenario, this would use cross-correlation or robust audio fingerprinting (e.g. chromaprint).
func MatchSignature(stream []byte, signature []byte) bool {
	if len(signature) == 0 {
		return false
	}
	return bytes.Contains(stream, signature)
}

// GetCanonicalSignatures loads all canonical signature bytes from the given directory.
func GetCanonicalSignatures(canonicalDir string) (map[string][]byte, error) {
	sigs := make(map[string][]byte)

	files, err := os.ReadDir(canonicalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return sigs, nil // No signatures yet
		}
		return nil, err
	}

	for _, f := range files {
		if !f.IsDir() {
			data, err := os.ReadFile(filepath.Join(canonicalDir, f.Name()))
			if err == nil && len(data) > 0 {
				sigs[f.Name()] = data
			}
		}
	}
	return sigs, nil
}

// SaveUnreviewedChunk saves an audio chunk for manual review.
func SaveUnreviewedChunk(data []byte, unreviewedDir string, filename string) error {
	_ = os.MkdirAll(unreviewedDir, 0755)
	return os.WriteFile(filepath.Join(unreviewedDir, filename), data, 0644)
}

// CropAndMarkCanonical crops an unreviewed chunk and saves it as a canonical signature.
func CropAndMarkCanonical(unreviewedDir, canonicalDir, filename string, startBytes, endBytes int) error {
	data, err := os.ReadFile(filepath.Join(unreviewedDir, filename))
	if err != nil {
		return err
	}

	if startBytes < 0 || endBytes > len(data) || startBytes >= endBytes {
		return fmt.Errorf("invalid crop range %d-%d for file of size %d", startBytes, endBytes, len(data))
	}

	cropped := data[startBytes:endBytes]

	_ = os.MkdirAll(canonicalDir, 0755)
	return os.WriteFile(filepath.Join(canonicalDir, filename), cropped, 0644)
}
