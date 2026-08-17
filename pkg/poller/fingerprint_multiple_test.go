//go:build e2e

package poller

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type multipleFingerprintCase struct {
	Name        string
	Dir         string
	ExpectedNum int
}

func loadMultipleFingerprintCases(t *testing.T) []multipleFingerprintCase {
	t.Helper()

	root := filepath.Join("testdata", "multiple_fingerprints", "cases")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", root, err)
	}

	var cases []multipleFingerprintCase
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		dir := filepath.Join(root, entry.Name())
		expectedNum := loadMultipleFingerprintCaseConfig(t, filepath.Join(dir, "case.toml"))

		cases = append(cases, multipleFingerprintCase{
			Name:        entry.Name(),
			Dir:         dir,
			ExpectedNum: expectedNum,
		})
	}
	return cases
}

func loadMultipleFingerprintCaseConfig(t *testing.T, path string) int {
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

		// Strip inline comments
		if idx := strings.Index(line, "#"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != "should_match" {
			continue
		}

		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			t.Fatalf("invalid should_match in %s: %v", path, err)
		}
		return parsed
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Scanner(%s) error = %v", path, err)
	}
	t.Fatalf("missing should_match in %s", path)
	return 0
}

func TestMatchMultipleSignatures(t *testing.T) {
	if _, err := ffmpegBinaryPath(); err != nil {
		t.Fatal("ffmpeg not installed...")
	}

	for _, tc := range loadMultipleFingerprintCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			streamData := mustReadTestFile(t, tc.Dir, "stream.mp3")

			canonicalDir := t.TempDir()

			entries, err := os.ReadDir(tc.Dir)
			if err != nil {
				t.Fatalf("ReadDir(%s) error = %v", tc.Dir, err)
			}

			var signatureCount int
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), "signature.mp3") {
					data := mustReadTestFile(t, tc.Dir, entry.Name())
					writeFile(t, filepath.Join(canonicalDir, entry.Name()), data)
					signatureCount++
				}
			}

			matched, matchName, err := findMatchingCanonicalSignatureInSet(streamData, defaultFingerprintFormat, canonicalDir, nil)
			if err != nil {
				t.Fatalf("findMatchingCanonicalSignatureInSet error = %v", err)
			}

			if tc.ExpectedNum == 0 {
				if matched {
					t.Fatalf("Expected no match, but matched %s", matchName)
				}
			} else {
				if !matched {
					t.Fatalf("Expected match with %dsignature.mp3, but found no match", tc.ExpectedNum)
				}
				expectedName := fmt.Sprintf("%dsignature.mp3", tc.ExpectedNum)
				if matchName != expectedName {
					t.Fatalf("Expected match with %s, but got %s", expectedName, matchName)
				}
			}
		})
	}
}
