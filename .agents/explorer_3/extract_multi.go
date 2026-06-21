package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	audioDir := "/Users/viktorashi/nerdin/pro-fm/data/audios"
	files, err := os.ReadDir(audioDir)
	if err != nil {
		fmt.Printf("Error reading audio dir: %v\n", err)
		return
	}

	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(strings.ToLower(file.Name()), ".ogg") {
			continue
		}

		audioPath := filepath.Join(audioDir, file.Name())

		// Get duration using ffprobe
		durationCmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", audioPath)
		var durOut bytes.Buffer
		durationCmd.Stdout = &durOut
		if err := durationCmd.Run(); err != nil {
			fmt.Printf("Error getting duration for %s: %v\n", file.Name(), err)
			continue
		}
		durationStr := strings.TrimSpace(durOut.String())

		// Run ffmpeg to decode to 8000Hz 16-bit mono PCM
		cmd := exec.Command("ffmpeg", "-i", audioPath, "-f", "s16le", "-ac", "1", "-ar", "8000", "-")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if err := cmd.Run(); err != nil {
			fmt.Printf("ffmpeg error for %s: %v\n", file.Name(), err)
			continue
		}

		pcmBytes := stdout.Bytes()
		sampleCount := len(pcmBytes) / 2
		if sampleCount == 0 {
			fmt.Printf("File %s has 0 samples\n", file.Name())
			continue
		}

		samples := make([]int16, sampleCount)
		for i := 0; i < sampleCount; i++ {
			samples[i] = int16(binary.LittleEndian.Uint16(pcmBytes[i*2 : i*2+2]))
		}

		// Downsample to 64 buckets
		numBuckets := 64
		bucketSize := sampleCount / numBuckets
		if bucketSize == 0 {
			bucketSize = 1
		}

		peaks := make([]int16, numBuckets)
		maxPeak := int16(0)

		for i := 0; i < numBuckets; i++ {
			start := i * bucketSize
			end := start + bucketSize
			if end > sampleCount {
				end = sampleCount
			}

			maxVal := int16(0)
			for j := start; j < end; j++ {
				val := samples[j]
				if val < 0 {
					if val == -32768 {
						val = 32767
					} else {
						val = -val
					}
				}
				if val > maxVal {
					maxVal = val
				}
			}
			peaks[i] = maxVal
			if maxVal > maxPeak {
				maxPeak = maxVal
			}
		}

		// Scale to 0-255 (relative to file peak)
		waveform := make([]byte, numBuckets)
		for i, peak := range peaks {
			if maxPeak > 0 {
				waveform[i] = byte(math.Round(float64(peak) / float64(maxPeak) * 255.0))
			} else {
				waveform[i] = 0
			}
		}

		// Print summary
		fmt.Printf("File: %-25s | Duration: %5s s | Samples: %6d | Max Peak: %5d | Waveform (first 10): %v\n",
			file.Name(), durationStr, sampleCount, maxPeak, waveform[:10])
	}
}
