package poller

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMatchSignature(t *testing.T) {
	if _, err := ffmpegBinaryPath(); err != nil {
		t.Skip("ffmpeg not installed, skipping audio fingerprint validation")
	}

	for _, tc := range loadFingerprintCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			stream := mustReadTestFile(t, tc.Dir, "stream.mp3")
			signature := mustReadTestFile(t, tc.Dir, "signature.mp3")
			if got := MatchSignature(stream, signature); got != tc.ShouldMatch {
				t.Fatalf("MatchSignature() = %v, want %v", got, tc.ShouldMatch)
			}
		})
	}
}

func TestAllowedCanonicalSignatureNamesUsesStoredCampaignOwnership(t *testing.T) {
	dbMgr := mustNewTestDBManager(t)
	canonicalDir := t.TempDir()
	ctx := context.Background()

	writeFile(t, filepath.Join(canonicalDir, "BTS - Dynamite.mp3"), []byte("signature"))
	writeFile(t, filepath.Join(canonicalDir, "Ed Sheeran - Shape of You.mp3"), []byte("signature"))

	if err := dbMgr.UpsertSignatureFile(ctx, "canonical", "BTS - Dynamite.mp3", bucharestTime(2026, time.June, 17, 12, 0, 0), "BTS"); err != nil {
		t.Fatalf("UpsertSignatureFile(BTS) error = %v", err)
	}
	if err := dbMgr.UpsertSignatureFile(ctx, "canonical", "Ed Sheeran - Shape of You.mp3", bucharestTime(2026, time.July, 22, 12, 0, 0), "Ariana"); err != nil {
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

	stream := mustReadTestFile(t, "testdata", "fingerprint", "cases", "quiet_match", "stream.mp3")
	signature := mustReadTestFile(t, "testdata", "fingerprint", "cases", "quiet_match", "signature.mp3")
	writeFile(t, filepath.Join(canonicalDir, "Ariana - candidate.mp3"), signature)

	if err := dbMgr.UpsertSignatureFile(ctx, "canonical", "Ariana - candidate.mp3", bucharestTime(2026, time.July, 22, 12, 0, 0), "Ariana"); err != nil {
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
	unreviewedDir := t.TempDir()
	canonicalDir := t.TempDir()

	writeFile(t, filepath.Join(canonicalDir, "known.mp3"), []byte("signature"))

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

	recordedAt := bucharestTime(2026, time.June, 17, 12, 34, 56)
	if err := os.Chtimes(filepath.Join(unreviewedDir, filename), recordedAt, recordedAt); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

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
	if !bytes.Equal(original, data) {
		t.Fatalf("Expected original unreviewed chunk to be preserved")
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
		if audioNames[0] != "signature.mp3" || audioNames[1] != "stream.mp3" {
			t.Fatalf("%s audio files must be signature.mp3 and stream.mp3, found %v", dir, audioNames)
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
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".mp3" {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
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
