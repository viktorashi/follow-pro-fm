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
	Name               string
	Dir                string
	ExpectedNum        int
	IncludesAudiosFrom string
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
		expectedNum, includesAudiosFrom := loadMultipleFingerprintCaseConfig(t, filepath.Join(dir, "case.toml"))

		cases = append(cases, multipleFingerprintCase{
			Name:               entry.Name(),
			Dir:                dir,
			ExpectedNum:        expectedNum,
			IncludesAudiosFrom: includesAudiosFrom,
		})
	}
	return cases
}

func loadMultipleFingerprintCaseConfig(t *testing.T, path string) (int, string) {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", path, err)
	}
	defer func() { _ = file.Close() }()

	var expectedNum int
	var includesAudiosFrom string
	var foundShouldMatch bool

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

		k := strings.TrimSpace(key)
		v := strings.TrimSpace(value)

		if k == "should_match" {
			parsed, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("invalid should_match in %s: %v", path, err)
			}
			expectedNum = parsed
			foundShouldMatch = true
		} else if k == "includes_audios_from" {
			includesAudiosFrom = strings.Trim(v, `"'`)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("Scanner(%s) error = %v", path, err)
	}
	if !foundShouldMatch {
		t.Fatalf("missing should_match in %s", path)
	}
	return expectedNum, includesAudiosFrom
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

			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), "signature.mp3") {
					data := mustReadTestFile(t, tc.Dir, entry.Name())
					writeFile(t, filepath.Join(canonicalDir, entry.Name()), data)
				}
			}

			if tc.IncludesAudiosFrom != "" {
				sharedDir := filepath.Join("testdata", "multiple_fingerprints", "shared", tc.IncludesAudiosFrom)
				sharedEntries, err := os.ReadDir(sharedDir)
				if err != nil {
					t.Fatalf("ReadDir(%s) error = %v", sharedDir, err)
				}
				for _, entry := range sharedEntries {
					if strings.HasSuffix(entry.Name(), "signature.mp3") {
						data := mustReadTestFile(t, sharedDir, entry.Name())
						writeFile(t, filepath.Join(canonicalDir, entry.Name()), data)
					}
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
