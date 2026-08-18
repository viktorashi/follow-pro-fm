package poller

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalSignatureLoading(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if got, err := GetCanonicalSignatures(missing); err != nil || len(got) != 0 {
		t.Fatalf("missing signatures = %v, %v", got, err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "valid.mp3"), []byte("signature"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.mp3"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := GetCanonicalSignatures(dir)
	if err != nil || len(got) != 1 || string(got["valid.mp3"]) != "signature" {
		t.Fatalf("signatures = %v, %v", got, err)
	}
}

func TestFingerprintMathHelpers(t *testing.T) {
	if got := fingerprintFormatForName("VOICE.WAV"); got != "wav" {
		t.Fatalf("format = %q", got)
	}
	if got := fingerprintFormatForName("no-extension"); got != defaultFingerprintFormat {
		t.Fatalf("default format = %q", got)
	}

	samples := pcmToFloatSamples([]byte{0, 0, 0xff, 0x7f, 0xff})
	if len(samples) != 2 || samples[0] != 0 || math.Abs(samples[1]-32767.0/32768.0) > 1e-9 {
		t.Fatalf("PCM samples = %v", samples)
	}

	features := []float64{1, 10, 2, 20, 3, 30}
	normalizeFeatureFrames(features, 2)
	for coefficient := range 2 {
		if math.Abs(features[coefficient]+features[2+coefficient]+features[4+coefficient]) > 1e-9 {
			t.Fatalf("coefficient %d is not centered: %v", coefficient, features)
		}
	}
	if score := maxFeatureSimilarity([]float64{0, 1, 2, 3, 4, 5}, []float64{2, 3}, 2); math.Abs(score-1) > 1e-9 {
		t.Fatalf("similarity = %v, want 1", score)
	}
	if score := maxFeatureSimilarity(nil, []float64{1}, 1); score != 0 {
		t.Fatalf("empty similarity = %v", score)
	}
}
