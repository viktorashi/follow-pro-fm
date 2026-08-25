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
	"sync"
	"time"

	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	defaultFingerprintFormat    = "mp3"
	fingerprintSampleRate       = 8000
	fingerprintFFTSize          = 512
	fingerprintHopSize          = 128
	fingerprintMelBandCount     = 40
	fingerprintCoefficientCount = 20
	fingerprintSimilarityFloor  = 0.58
)

// MatchSignature checks if a signature exists within an audio stream.
func MatchSignature(stream []byte, signature []byte) bool {
	matched, _, err := matchSignatureWithFormats(stream, "", signature, "")
	if err == nil {
		return matched
	}
	return len(signature) > 0 && bytes.Contains(stream, signature)
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
	_ = os.MkdirAll(unreviewedDir, 0o755)

	remuxed, err := RemuxToMP3(data)
	if err == nil {
		data = remuxed
	}

	return os.WriteFile(filepath.Join(unreviewedDir, filename), data, 0o644)
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
	return findMatchingCanonicalSignature(data, "", canonicalDir)
}

func findMatchingCanonicalSignature(stream []byte, streamFormat, canonicalDir string) (bool, string, error) {
	return findMatchingCanonicalSignatureInSet(stream, streamFormat, canonicalDir, nil)
}

type cachedSignature struct {
	raw      []byte
	features []float64
	modTime  time.Time
}

var signatureCache sync.Map // map[string]*cachedSignature

func getCachedSignature(canonicalDir, name string, info os.FileInfo) (*cachedSignature, error) {
	path := filepath.Join(canonicalDir, name)

	cacheKey := path
	if val, ok := signatureCache.Load(cacheKey); ok {
		cached := val.(*cachedSignature)
		if cached.modTime.Equal(info.ModTime()) {
			return cached, nil
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	sigFormat := fingerprintFormatForName(name)
	features, err := extractFingerprintFeatures(data, sigFormat)
	if err != nil {
		return nil, err
	}

	cached := &cachedSignature{
		raw:      data,
		features: features,
		modTime:  info.ModTime(),
	}
	signatureCache.Store(cacheKey, cached)

	return cached, nil
}

type canonicalDirCacheEntry struct {
	modTime time.Time
	entries []os.DirEntry
}

var dirCache sync.Map // map[string]*canonicalDirCacheEntry

func findMatchingCanonicalSignatureInSet(stream []byte, streamFormat, canonicalDir string, allowed map[string]struct{}) (bool, string, error) {
	dirInfo, err := os.Stat(canonicalDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}

	var entries []os.DirEntry
	if val, ok := dirCache.Load(canonicalDir); ok {
		cached := val.(*canonicalDirCacheEntry)
		if cached.modTime.Equal(dirInfo.ModTime()) {
			entries = cached.entries
		}
	}

	if entries == nil {
		entries, err = os.ReadDir(canonicalDir)
		if err != nil {
			return false, "", err
		}
		dirCache.Store(canonicalDir, &canonicalDirCacheEntry{
			modTime: dirInfo.ModTime(),
			entries: entries,
		})
	}

	var firstDecodeErr error

	// Pre-extract stream features once outside the loop to avoid O(N) redundant extractions
	streamFeatures, streamErr := extractFingerprintFeatures(stream, streamFormat)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if allowed != nil {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		cached, err := getCachedSignature(canonicalDir, name, info)
		if err != nil {
			continue
		}

		if len(cached.raw) > 0 && bytes.Contains(stream, cached.raw) {
			return true, name, nil
		}

		if streamErr != nil {
			if firstDecodeErr == nil {
				firstDecodeErr = streamErr
			}
			continue
		}

		if len(cached.features) == 0 || len(streamFeatures) < len(cached.features) {
			continue
		}

		score := maxFeatureSimilarity(streamFeatures, cached.features, fingerprintCoefficientCount)
		if score >= fingerprintSimilarityFloor {
			return true, name, nil
		}
	}

	return false, "", firstDecodeErr
}

// CropAndMarkCanonical crops an unreviewed chunk and saves it as a canonical signature using ffmpeg time-based cropping.
func CropAndMarkCanonical(unreviewedDir, canonicalDir, filename string, startSeconds, endSeconds float64) error {
	sourcePath := filepath.Join(unreviewedDir, filename)

	if startSeconds < 0 || endSeconds <= startSeconds {
		return fmt.Errorf("invalid crop range %.2f-%.2f", startSeconds, endSeconds)
	}

	_ = os.MkdirAll(canonicalDir, 0o755)
	targetPath := filepath.Join(canonicalDir, filename)

	ffmpegPath, err := ffmpegBinaryPath()
	if err != nil {
		return fmt.Errorf("ffmpeg not found: %w", err)
	}

	// Create a temp file first so we don't end up with a partial file on failure
	tmpPath := targetPath + ".tmp"
	defer func() { _ = os.Remove(tmpPath) }()

	cmd := exec.Command(ffmpegPath,
		"-y",                                     // overwrite
		"-ss", fmt.Sprintf("%.3f", startSeconds), // start time
		"-to", fmt.Sprintf("%.3f", endSeconds), // end time
		"-i", sourcePath, // input file
		"-c", "copy", // stream copy (no re-encoding)
		"-f", "mp3", // format
		tmpPath,
	)

	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg crop failed: %w (output: %s)", err, output)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("failed to move cropped file: %w", err)
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
		_ = dbMgr.UpsertSignatureFile(ctx, bucket, filename, info.ModTime(), campaignArtist, "", "")
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
		campaignArtist, err := signatureCampaignArtist(ctx, dbMgr, campaigns, BucketCanonical, entry.Name(), canonicalDir)
		if err != nil {
			continue
		}
		if strings.EqualFold(currentCampaignArtist, campaignArtist) {
			allowed[entry.Name()] = struct{}{}
		}
	}
	return allowed, nil
}

func matchSignatureWithFormats(stream []byte, streamFormat string, signature []byte, signatureFormat string) (bool, float64, error) {
	if len(stream) == 0 || len(signature) == 0 {
		return false, 0, nil
	}

	streamFeatures, err := extractFingerprintFeatures(stream, streamFormat)
	if err != nil {
		return false, 0, err
	}

	signatureFeatures, err := extractFingerprintFeatures(signature, signatureFormat)
	if err != nil {
		return false, 0, err
	}

	if len(signatureFeatures) == 0 || len(streamFeatures) < len(signatureFeatures) {
		return false, 0, nil
	}

	score := maxFeatureSimilarity(streamFeatures, signatureFeatures, fingerprintCoefficientCount)
	return score >= fingerprintSimilarityFloor, score, nil
}

func fingerprintFormatForName(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext == "" {
		return defaultFingerprintFormat
	}
	return ext
}

// extractFingerprintFeatures builds MFCC-style features:
// windowed FFT -> mel filter bank -> log energies -> DCT coefficients.
func extractFingerprintFeatures(data []byte, format string) ([]float64, error) {
	pcm, err := decodeAudioForFingerprinting(data, format)
	if err != nil {
		return nil, err
	}

	samples := pcmToFloatSamples(pcm)
	if len(samples) < fingerprintFFTSize {
		return nil, fmt.Errorf("decoded audio too short for fingerprinting")
	}

	window := fingerprintWindow()
	filterBank := melFilterBank(fingerprintSampleRate, fingerprintFFTSize, fingerprintMelBandCount)
	fft := fourier.NewFFT(fingerprintFFTSize)
	dct := fourier.NewDCT(fingerprintMelBandCount)

	frame := make([]float64, fingerprintFFTSize)
	melEnergies := make([]float64, fingerprintMelBandCount)
	features := make([]float64, 0, ((len(samples)-fingerprintFFTSize)/fingerprintHopSize+1)*fingerprintCoefficientCount)

	for start := 0; start+fingerprintFFTSize <= len(samples); start += fingerprintHopSize {
		for i := range fingerprintFFTSize {
			frame[i] = samples[start+i] * window[i]
		}

		coeffs := fft.Coefficients(nil, frame)
		power := make([]float64, len(coeffs))
		for i, coeff := range coeffs {
			power[i] = real(coeff)*real(coeff) + imag(coeff)*imag(coeff)
		}

		for band := range fingerprintMelBandCount {
			energy := 0.0
			for bin, weight := range filterBank[band] {
				energy += power[bin] * weight
			}
			melEnergies[band] = math.Log(energy + 1e-12)
		}

		cepstral := dct.Transform(nil, melEnergies)
		features = append(features, cepstral[:fingerprintCoefficientCount]...)
	}

	if len(features) == 0 {
		return nil, fmt.Errorf("decoded audio produced no fingerprint features")
	}

	normalizeFeatureFrames(features, fingerprintCoefficientCount)

	return features, nil
}

func fingerprintWindow() []float64 {
	window := make([]float64, fingerprintFFTSize)
	for i := range window {
		window[i] = 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(fingerprintFFTSize-1))
	}
	return window
}

func melFilterBank(sampleRate, fftSize, bandCount int) [][]float64 {
	nyquist := float64(sampleRate) / 2
	maxMel := hzToMel(nyquist)
	melPoints := make([]float64, bandCount+2)
	for i := range melPoints {
		melPoints[i] = float64(i) * maxMel / float64(len(melPoints)-1)
	}

	binCount := fftSize/2 + 1
	binFreqs := make([]float64, binCount)
	for i := range binFreqs {
		binFreqs[i] = float64(i) * float64(sampleRate) / float64(fftSize)
	}

	filterBank := make([][]float64, bandCount)
	for band := range bandCount {
		leftHz := melToHz(melPoints[band])
		centerHz := melToHz(melPoints[band+1])
		rightHz := melToHz(melPoints[band+2])

		weights := make([]float64, binCount)
		for bin, freq := range binFreqs {
			switch {
			case freq <= leftHz || freq >= rightHz:
				continue
			case freq <= centerHz:
				weights[bin] = (freq - leftHz) / (centerHz - leftHz)
			default:
				weights[bin] = (rightHz - freq) / (rightHz - centerHz)
			}
		}
		filterBank[band] = weights
	}

	return filterBank
}

func hzToMel(hz float64) float64 {
	return 2595 * math.Log10(1+hz/700)
}

func melToHz(mel float64) float64 {
	return 700 * (math.Pow(10, mel/2595) - 1)
}

func normalizeFeatureFrames(features []float64, width int) {
	frameCount := len(features) / width
	if frameCount == 0 {
		return
	}

	for coeff := range width {
		mean := 0.0
		for frame := range frameCount {
			mean += features[frame*width+coeff]
		}
		mean /= float64(frameCount)

		variance := 0.0
		for frame := range frameCount {
			idx := frame*width + coeff
			features[idx] -= mean
			variance += features[idx] * features[idx]
		}

		scale := math.Sqrt(variance/float64(frameCount)) + 1e-6
		for frame := range frameCount {
			features[frame*width+coeff] /= scale
		}
	}
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

func maxFeatureSimilarity(stream []float64, signature []float64, width int) float64 {
	if len(signature) == 0 || len(stream) < len(signature) || width <= 0 {
		return 0
	}

	sigFrames := len(signature) / width
	streamFrames := len(stream) / width
	if sigFrames == 0 || streamFrames < sigFrames {
		return 0
	}

	sigNorm := vectorNorm(signature)
	if sigNorm == 0 {
		return 0
	}

	maxSimilarity := 0.0
	for offset := 0; offset <= streamFrames-sigFrames; offset++ {
		start := offset * width
		window := stream[start : start+len(signature)]
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
