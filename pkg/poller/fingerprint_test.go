package poller

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMatchSignature(t *testing.T) {
	if _, err := ffmpegBinaryPath(); err != nil {
		t.Skip("ffmpeg not installed, skipping audio fingerprint validation")
	}

	canonical := mustReadTestFile(t, "testdata", "fingerprint", "canonical_intro.mp3")
	streamMatch := mustReadTestFile(t, "testdata", "fingerprint", "stream_match.mp3")
	streamQuietMatch := mustReadTestFile(t, "testdata", "fingerprint", "stream_match_quiet.mp3")
	streamNoMatch := mustReadTestFile(t, "testdata", "fingerprint", "stream_no_match.mp3")

	if !MatchSignature(streamMatch, canonical) {
		t.Errorf("Expected match, but got none")
	}
	if !MatchSignature(streamQuietMatch, canonical) {
		t.Errorf("Expected quieter shifted match, but got none")
	}

	if MatchSignature(streamNoMatch, canonical) {
		t.Errorf("Expected NO match, but got one")
	}
}

func TestSaveUnreviewedChunkIfDistinctSkipsCanonical(t *testing.T) {
	unreviewedDir := t.TempDir()
	canonicalDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(canonicalDir, "known.mp3"), []byte("signature"), 0644); err != nil {
		t.Fatalf("Failed to seed canonical signature: %v", err)
	}

	saved, matchedName, err := SaveUnreviewedChunkIfDistinct([]byte("prefix-signature-suffix"), unreviewedDir, canonicalDir, "candidate.mp3")
	if err != nil {
		t.Fatalf("SaveUnreviewedChunkIfDistinct failed: %v", err)
	}
	if saved {
		t.Fatal("Expected duplicate chunk to be skipped")
	}
	if matchedName != "known.mp3" {
		t.Fatalf("Expected matched canonical name to be known.mp3, got %q", matchedName)
	}

	entries, err := os.ReadDir(unreviewedDir)
	if err != nil {
		t.Fatalf("Failed to read unreviewed dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Expected no saved unreviewed chunks, found %d", len(entries))
	}
}

func TestCropAndMarkCanonicalPreservesOriginal(t *testing.T) {
	unreviewedDir := t.TempDir()
	canonicalDir := t.TempDir()

	filename := "test_chunk.mp3"
	data := []byte("0123456789")
	err := SaveUnreviewedChunk(data, unreviewedDir, filename)
	if err != nil {
		t.Fatalf("Failed to save unreviewed chunk: %v", err)
	}

	// Crop "3456"
	err = CropAndMarkCanonical(unreviewedDir, canonicalDir, filename, 3, 7)
	if err != nil {
		t.Fatalf("Crop failed: %v", err)
	}

	sigs, err := GetCanonicalSignatures(canonicalDir)
	if err != nil {
		t.Fatalf("Failed to get signatures: %v", err)
	}

	if len(sigs) != 1 {
		t.Fatalf("Expected 1 canonical signature, got %d", len(sigs))
	}

	if !bytes.Equal(sigs[filename], []byte("3456")) {
		t.Errorf("Expected cropped signature to be '3456', got '%s'", sigs[filename])
	}

	original, err := os.ReadFile(filepath.Join(unreviewedDir, filename))
	if err != nil {
		t.Fatalf("Expected original unreviewed chunk to remain: %v", err)
	}
	if !bytes.Equal(original, data) {
		t.Fatalf("Expected original unreviewed chunk to be preserved")
	}
}

func mustReadTestFile(t *testing.T, elems ...string) []byte {
	t.Helper()

	path := filepath.Join(elems...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", path, err)
	}

	return data
}
