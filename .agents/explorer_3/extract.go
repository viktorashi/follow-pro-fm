package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os/exec"
)

func main() {
	audioPath := "/Users/viktorashi/nerdin/pro-fm/data/audios/WhatsApp Ptt 1.ogg"

	// Run ffmpeg to decode to 8000Hz 16-bit mono PCM
	cmd := exec.Command("ffmpeg", "-i", audioPath, "-f", "s16le", "-ac", "1", "-ar", "8000", "-")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		fmt.Printf("ffmpeg error: %v\nstderr: %s\n", err, stderr.String())
		return
	}

	pcmBytes := stdout.Bytes()
	sampleCount := len(pcmBytes) / 2
	fmt.Printf("Decoded %d PCM bytes, %d samples\n", len(pcmBytes), sampleCount)

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

	fmt.Printf("Max peak in file: %d\n", maxPeak)

	// Scale to 0-255 (relative to file peak)
	scaled255Peak := make([]byte, numBuckets)
	for i, peak := range peaks {
		if maxPeak > 0 {
			scaled255Peak[i] = byte(math.Round(float64(peak) / float64(maxPeak) * 255.0))
		} else {
			scaled255Peak[i] = 0
		}
	}

	// Scale to 0-127 (relative to file peak)
	scaled127Peak := make([]byte, numBuckets)
	for i, peak := range peaks {
		if maxPeak > 0 {
			scaled127Peak[i] = byte(math.Round(float64(peak) / float64(maxPeak) * 127.0))
		} else {
			scaled127Peak[i] = 0
		}
	}

	// Scale to 0-255 (relative to absolute max 32768)
	scaled255Abs := make([]byte, numBuckets)
	for i, peak := range peaks {
		scaled255Abs[i] = byte(math.Round(float64(peak) / 32768.0 * 255.0))
	}

	fmt.Println("Scaled to 0-255 (Peak normalized):")
	fmt.Printf("%v\n", scaled255Peak)

	fmt.Println("Scaled to 0-127 (Peak normalized):")
	fmt.Printf("%v\n", scaled127Peak)

	fmt.Println("Scaled to 0-255 (Absolute):")
	fmt.Printf("%v\n", scaled255Abs)
}
