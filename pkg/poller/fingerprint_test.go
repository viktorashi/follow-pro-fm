package poller

import (
	"bytes"
	"testing"
)

func TestMatchSignature(t *testing.T) {
	// Let's create some dummy fixture data in memory for tests
	canonical := []byte("audio-intro-signature-12345")

	streamMatch := append([]byte("some-random-noise-before"), canonical...)
	streamMatch = append(streamMatch, []byte("some-random-noise-after")...)

	streamNoMatch := []byte("some-random-noise-without-the-signature-inside")

	if !MatchSignature(streamMatch, canonical) {
		t.Errorf("Expected match, but got none")
	}

	if MatchSignature(streamNoMatch, canonical) {
		t.Errorf("Expected NO match, but got one")
	}
}

func TestCropAndMarkCanonical(t *testing.T) {
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

	if !bytes.Equal(sigs[0], []byte("3456")) {
		t.Errorf("Expected cropped signature to be '3456', got '%s'", sigs[0])
	}
}
