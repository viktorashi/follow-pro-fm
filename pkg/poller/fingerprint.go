package poller

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultFingerprintFormat   = "mp3"
	fingerprintSampleRate      = 8000
	fingerprintDownsampleStep  = 16
	fingerprintSimilarityFloor = 0.88
)

// MatchSignature checks if a signature exists within an audio stream.
func MatchSignature(stream []byte, signature []byte) bool {
	matched, err := matchSignatureWithFormats(stream, defaultFingerprintFormat, signature, defaultFingerprintFormat)
	return err == nil && matched
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

// SaveUnreviewedChunkIfDistinct skips saving when the captured chunk already contains
// a canonical signature that is on disk.
func SaveUnreviewedChunkIfDistinct(data []byte, unreviewedDir, canonicalDir, filename string) (bool, string, error) {
	match, matchedName, err := ChunkMatchesCanonical(data, canonicalDir)
	if err != nil {
		return false, "", err
	}
	if match {
		return false, matchedName, nil
	}
	if err := SaveUnreviewedChunk(data, unreviewedDir, filename); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// ChunkMatchesCanonical reports whether the chunk already contains a canonical signature.
func ChunkMatchesCanonical(data []byte, canonicalDir string) (bool, string, error) {
	return findMatchingCanonicalSignature(data, defaultFingerprintFormat, canonicalDir)
}

func findMatchingCanonicalSignature(stream []byte, streamFormat, canonicalDir string) (bool, string, error) {
	return findMatchingCanonicalSignatureInSet(stream, streamFormat, canonicalDir, nil)
}

func findMatchingCanonicalSignatureInSet(stream []byte, streamFormat, canonicalDir string, allowed map[string]struct{}) (bool, string, error) {
	sigs, err := GetCanonicalSignatures(canonicalDir)
	if err != nil {
		return false, "", err
	}

	var firstDecodeErr error
	for name, sig := range sigs {
		if allowed != nil {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		sigFormat := fingerprintFormatForName(name)
		matched, err := matchSignatureWithFormats(stream, streamFormat, sig, sigFormat)
		if err == nil {
			if matched {
				return true, name, nil
			}
			continue
		}
		if firstDecodeErr == nil {
			firstDecodeErr = err
		}
	}

	return false, "", firstDecodeErr
}

// CropAndMarkCanonical crops an unreviewed chunk and saves it as a canonical signature.
func CropAndMarkCanonical(unreviewedDir, canonicalDir, filename string, startBytes, endBytes int) error {
	sourcePath := filepath.Join(unreviewedDir, filename)
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}

	if startBytes < 0 || endBytes > len(data) || startBytes >= endBytes {
		return fmt.Errorf("invalid crop range %d-%d for file of size %d", startBytes, endBytes, len(data))
	}

	cropped := data[startBytes:endBytes]

	_ = os.MkdirAll(canonicalDir, 0755)
	targetPath := filepath.Join(canonicalDir, filename)
	if err := os.WriteFile(targetPath, cropped, 0644); err != nil {
		return err
	}

	info, err := os.Stat(sourcePath)
	if err == nil {
		_ = os.Chtimes(targetPath, info.ModTime(), info.ModTime())
	}

	return nil
}

func campaignArtistForTime(campaigns []Campaign, recordedAt time.Time) (string, bool) {
	for _, campaign := range campaigns {
		if campaign.IsActive(recordedAt) {
			return campaign.Artist, true
		}
	}
	return "", false
}

func signatureCampaignArtist(ctx context.Context, dbMgr *DBManager, campaigns []Campaign, bucket, filename, dir string) (string, error) {
	if dbMgr != nil {
		meta, err := dbMgr.GetSignatureFile(ctx, bucket, filename)
		if err == nil {
			return meta.CampaignArtist, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}

	info, err := os.Stat(filepath.Join(dir, filename))
	if err != nil {
		return "", err
	}
	campaignArtist, ok := campaignArtistForTime(campaigns, info.ModTime())
	if !ok {
		return "", fmt.Errorf("no active campaign found for %s at %s", filename, info.ModTime().Format(time.RFC3339))
	}
	if dbMgr != nil {
		_ = dbMgr.UpsertSignatureFile(ctx, bucket, filename, info.ModTime(), campaignArtist)
	}
	return campaignArtist, nil
}

func allowedCanonicalSignatureNames(ctx context.Context, dbMgr *DBManager, campaigns []Campaign, canonicalDir string, now time.Time) (map[string]struct{}, error) {
	entries, err := os.ReadDir(canonicalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	currentCampaignArtist, ok := campaignArtistForTime(campaigns, now)
	if !ok {
		return nil, nil
	}

	allowed := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		campaignArtist, err := signatureCampaignArtist(ctx, dbMgr, campaigns, "canonical", entry.Name(), canonicalDir)
		if err != nil {
			continue
		}
		if strings.EqualFold(currentCampaignArtist, campaignArtist) {
			allowed[entry.Name()] = struct{}{}
		}
	}
	return allowed, nil
}

func matchSignatureWithFormats(stream []byte, streamFormat string, signature []byte, signatureFormat string) (bool, error) {
	if len(stream) == 0 || len(signature) == 0 {
		return false, nil
	}

	streamFeatures, err := extractFingerprintFeatures(stream, streamFormat)
	if err != nil {
		return false, err
	}

	signatureFeatures, err := extractFingerprintFeatures(signature, signatureFormat)
	if err != nil {
		return false, err
	}

	if len(signatureFeatures) == 0 || len(streamFeatures) < len(signatureFeatures) {
		return false, nil
	}

	return maxFeatureSimilarity(streamFeatures, signatureFeatures) >= fingerprintSimilarityFloor, nil
}

func fingerprintFormatForName(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext == "" {
		return defaultFingerprintFormat
	}
	return ext
}

func extractFingerprintFeatures(data []byte, format string) ([]float64, error) {
	pcm, err := decodeAudioForFingerprinting(data, format)
	if err != nil {
		return nil, err
	}

	samples := pcmToFloatSamples(pcm)
	if len(samples) < fingerprintDownsampleStep {
		return nil, fmt.Errorf("decoded audio too short for fingerprinting")
	}

	features := make([]float64, 0, len(samples)/fingerprintDownsampleStep)
	for start := 0; start+fingerprintDownsampleStep <= len(samples); start += fingerprintDownsampleStep {
		window := samples[start : start+fingerprintDownsampleStep]
		sum := 0.0
		for _, sample := range window {
			sum += sample
		}
		features = append(features, sum/float64(len(window)))
	}

	if len(features) == 0 {
		return nil, fmt.Errorf("decoded audio produced no fingerprint features")
	}

	mean := 0.0
	for _, value := range features {
		mean += value
	}
	mean /= float64(len(features))

	maxAbs := 0.0
	for i := range features {
		features[i] -= mean
		if abs := math.Abs(features[i]); abs > maxAbs {
			maxAbs = abs
		}
	}
	if maxAbs == 0 {
		maxAbs = 1
	}
	for i := range features {
		features[i] /= maxAbs
	}

	return features, nil
}

func decodeAudioForFingerprinting(data []byte, format string) ([]byte, error) {
	ffmpegPath, err := ffmpegBinaryPath()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	args := []string{"-hide_banner", "-loglevel", "error"}
	if format = strings.TrimSpace(format); format != "" {
		args = append(args, "-f", format)
	}
	args = append(args, "-i", "pipe:0", "-f", "s16le", "-ac", "1", "-ar", fmt.Sprintf("%d", fingerprintSampleRate), "pipe:1")

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stdin = bytes.NewReader(data)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg decode returned empty PCM stream")
	}

	return stdout.Bytes(), nil
}

func pcmToFloatSamples(pcm []byte) []float64 {
	samples := make([]float64, 0, len(pcm)/2)
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		samples = append(samples, float64(sample)/32768.0)
	}
	return samples
}

func maxFeatureSimilarity(stream []float64, signature []float64) float64 {
	if len(signature) == 0 || len(stream) < len(signature) {
		return 0
	}

	sigNorm := vectorNorm(signature)
	if sigNorm == 0 {
		return 0
	}

	maxSimilarity := 0.0
	for offset := 0; offset <= len(stream)-len(signature); offset++ {
		window := stream[offset : offset+len(signature)]
		windowNorm := vectorNorm(window)
		if windowNorm == 0 {
			continue
		}

		similarity := dotProduct(window, signature) / (windowNorm * sigNorm)
		if similarity > maxSimilarity {
			maxSimilarity = similarity
		}
	}

	return maxSimilarity
}

func dotProduct(left []float64, right []float64) float64 {
	sum := 0.0
	for i := range left {
		sum += left[i] * right[i]
	}
	return sum
}

func vectorNorm(values []float64) float64 {
	sum := 0.0
	for _, value := range values {
		sum += value * value
	}
	return math.Sqrt(sum)
}
