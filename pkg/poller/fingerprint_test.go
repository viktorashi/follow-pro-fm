package poller

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMatchSignature(t *testing.T) {
	forEachFingerprintCase(t, func(t *testing.T, tc fingerprintCase, stream []byte, signature []byte) {
		t.Run(tc.Name, func(t *testing.T) {
			if got := MatchSignature(stream, signature); got != tc.ShouldMatch {
				t.Fatalf("MatchSignature() = %v, want %v", got, tc.ShouldMatch)
			}
		})
	})
}

func TestMatchSignatureScoresStaySeparated(t *testing.T) {
	forEachFingerprintCase(t, func(t *testing.T, tc fingerprintCase, stream []byte, signature []byte) {
		t.Run(tc.Name, func(t *testing.T) {
			matched, score, err := matchSignatureWithFormats(stream, "", signature, "")
			if err != nil {
				t.Fatalf("matchSignatureWithFormats() error = %v", err)
			}

			t.Logf("score=%.4f matched=%v", score, matched)
			if tc.ShouldMatch && score < fingerprintSimilarityFloor {
				t.Fatalf("positive score %.4f fell below floor %.2f", score, fingerprintSimilarityFloor)
			}
			if !tc.ShouldMatch && score >= fingerprintSimilarityFloor {
				t.Fatalf("negative score %.4f reached floor %.2f", score, fingerprintSimilarityFloor)
			}
		})
	})
}

func forEachFingerprintCase(t *testing.T, fn func(t *testing.T, tc fingerprintCase, stream []byte, signature []byte)) {
	t.Helper()

	if _, err := ffmpegBinaryPath(); err != nil {
		t.Skip("ffmpeg not installed, skipping audio fingerprint validation")
	}

	for _, tc := range loadFingerprintCases(t) {
		stream := mustReadFingerprintFixture(t, tc.Dir, "stream")
		signature := mustReadFingerprintFixture(t, tc.Dir, "signature")
		fn(t, tc, stream, signature)
	}
}

func TestAllowedCanonicalSignatureNamesUsesStoredCampaignOwnership(t *testing.T) {
	dbMgr := mustNewTestDBManager(t)
	canonicalDir := t.TempDir()
	ctx := context.Background()

	writeFile(t, filepath.Join(canonicalDir, "BTS - Dynamite.mp3"), []byte("signature"))
	writeFile(t, filepath.Join(canonicalDir, "Ed Sheeran - Shape of You.mp3"), []byte("signature"))

	if err := dbMgr.UpsertSignatureFile(ctx, "canonical", "BTS - Dynamite.mp3", bucharestTime(2026, time.June, 17, 12, 0, 0), "BTS", ""); err != nil {
		t.Fatalf("UpsertSignatureFile(BTS) error = %v", err)
	}
	if err := dbMgr.UpsertSignatureFile(ctx, "canonical", "Ed Sheeran - Shape of You.mp3", bucharestTime(2026, time.July, 22, 12, 0, 0), "Ariana", ""); err != nil {
		t.Fatalf("UpsertSignatureFile(Ed) error = %v", err)
	}

	allowed, err := allowedCanonicalSignatureNames(ctx, dbMgr, []Campaign{
		{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		{StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana"},
	}, canonicalDir, bucharestTime(2026, time.June, 17, 12, 0, 0))
	if err != nil {
		t.Fatalf("allowedCanonicalSignatureNames() error = %v", err)
	}

	if len(allowed) != 1 {
		t.Fatalf("len(allowed) = %d, want 1", len(allowed))
	}
	if _, ok := allowed["BTS - Dynamite.mp3"]; !ok {
		t.Fatalf("allowed set missing BTS signature: %#v", allowed)
	}
	if _, ok := allowed["Ed Sheeran - Shape of You.mp3"]; ok {
		t.Fatalf("allowed set should exclude other-campaign signature: %#v", allowed)
	}
}

func TestCampaignBoundFingerprintDetectionDoesNotTriggerOtherCampaignSignature(t *testing.T) {
	if _, err := ffmpegBinaryPath(); err != nil {
		t.Skip("ffmpeg not installed, skipping audio fingerprint validation")
	}

	dbMgr := mustNewTestDBManager(t)
	ctx := context.Background()
	canonicalDir := t.TempDir()

	stream := mustReadFingerprintFixture(t, filepath.Join("testdata", "fingerprint", "cases", "quiet_match"), "stream")
	signature := mustReadFingerprintFixture(t, filepath.Join("testdata", "fingerprint", "cases", "quiet_match"), "signature")
	writeFile(t, filepath.Join(canonicalDir, "Ariana - candidate.mp3"), signature)

	if err := dbMgr.UpsertSignatureFile(ctx, "canonical", "Ariana - candidate.mp3", bucharestTime(2026, time.July, 22, 12, 0, 0), "Ariana", ""); err != nil {
		t.Fatalf("UpsertSignatureFile() error = %v", err)
	}

	allowed, err := allowedCanonicalSignatureNames(ctx, dbMgr, []Campaign{
		{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		{StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana"},
	}, canonicalDir, bucharestTime(2026, time.June, 17, 12, 0, 0))
	if err != nil {
		t.Fatalf("allowedCanonicalSignatureNames() error = %v", err)
	}

	matched, name, err := findMatchingCanonicalSignatureInSet(stream, defaultFingerprintFormat, canonicalDir, allowed)
	if err != nil {
		t.Fatalf("findMatchingCanonicalSignatureInSet() error = %v", err)
	}
	if matched {
		t.Fatalf("matched = true with %q, want false for other-campaign signature", name)
	}
}

func TestSaveUnreviewedChunkIfDistinctSkipsCanonical(t *testing.T) {
	if _, err := ffmpegBinaryPath(); err != nil {
		t.Skip("ffmpeg not installed, skipping audio fingerprint validation")
	}

	unreviewedDir := t.TempDir()
	canonicalDir := t.TempDir()

	signature := mustReadTestFile(t, "testdata", "fingerprint", "cases", "match", "signature.mp3")
	stream := mustReadTestFile(t, "testdata", "fingerprint", "cases", "match", "stream.mp3")
	writeFile(t, filepath.Join(canonicalDir, "known.mp3"), signature)

	saved, matchedName, err := SaveUnreviewedChunkIfDistinct(stream, unreviewedDir, canonicalDir, "candidate.mp3")
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
	data, err := os.ReadFile("testdata/fingerprint/cases/match/stream.mp3")
	if err != nil {
		t.Fatalf("Failed to read fixture: %v", err)
	}
	err = SaveUnreviewedChunk(data, unreviewedDir, filename)
	if err != nil {
		t.Fatalf("Failed to save unreviewed chunk: %v", err)
	}

	recordedAt := bucharestTime(2026, time.June, 17, 12, 34, 56)
	if err := os.Chtimes(filepath.Join(unreviewedDir, filename), recordedAt, recordedAt); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	err = CropAndMarkCanonical(unreviewedDir, canonicalDir, filename, 0.1, 0.5)
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

	if len(sigs[filename]) == 0 {
		t.Errorf("Expected cropped signature to be non-empty")
	}

	info, err := os.Stat(filepath.Join(canonicalDir, filename))
	if err != nil {
		t.Fatalf("Stat(canonical) error = %v", err)
	}
	if !info.ModTime().Equal(recordedAt) {
		t.Fatalf("canonical modtime = %s, want %s", info.ModTime().Format(time.RFC3339), recordedAt.Format(time.RFC3339))
	}

	original, err := os.ReadFile(filepath.Join(unreviewedDir, filename))
	if err != nil {
		t.Fatalf("Expected original unreviewed chunk to remain: %v", err)
	}
	if len(original) == 0 {
		t.Fatalf("Expected original unreviewed chunk to be preserved and non-empty")
	}
}

type fingerprintCase struct {
	Name        string
	Dir         string
	ShouldMatch bool
}

func loadFingerprintCases(t *testing.T) []fingerprintCase {
	t.Helper()

	root := filepath.Join("testdata", "fingerprint", "cases")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", root, err)
	}

	var cases []fingerprintCase
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(root, entry.Name())
		shouldMatch := loadFingerprintCaseConfig(t, filepath.Join(dir, "case.toml"))
		audioNames := fingerprintAudioFiles(t, dir)
		if len(audioNames) != 2 {
			t.Fatalf("%s must contain exactly 2 audio files, found %d", dir, len(audioNames))
		}
		if !isFingerprintFixturePair(audioNames) {
			t.Fatalf("%s audio files must be stream.* and signature.*, found %v", dir, audioNames)
		}

		cases = append(cases, fingerprintCase{
			Name:        entry.Name(),
			Dir:         dir,
			ShouldMatch: shouldMatch,
		})
	}

	if len(cases) == 0 {
		t.Fatal("expected fingerprint cases")
	}
	return cases
}

func loadFingerprintCaseConfig(t *testing.T, path string) bool {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", path, err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != "should_match" {
			continue
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			t.Fatalf("invalid should_match in %s: %v", path, err)
		}
		return parsed
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Scanner(%s) error = %v", path, err)
	}
	t.Fatalf("missing should_match in %s", path)
	return false
}

func fingerprintAudioFiles(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", dir, err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() || strings.EqualFold(entry.Name(), "case.toml") {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

func mustReadFingerprintFixture(t *testing.T, dir string, base string) []byte {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || strings.EqualFold(entry.Name(), "case.toml") {
			continue
		}
		if strings.EqualFold(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), base) {
			return mustReadTestFile(t, dir, entry.Name())
		}
	}

	t.Fatalf("missing %s fixture in %s", base, dir)
	return nil
}

func isFingerprintFixturePair(names []string) bool {
	if len(names) != 2 {
		return false
	}

	var hasSignature, hasStream bool
	for _, name := range names {
		base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		switch base {
		case "signature":
			hasSignature = true
		case "stream":
			hasStream = true
		default:
			return false
		}
	}
	return hasSignature && hasStream
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

func mustNewTestDBManager(t *testing.T) *DBManager {
	t.Helper()

	dbMgr, err := NewDBManager(filepath.Join(t.TempDir(), "app.sqlite"))
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	return dbMgr
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
