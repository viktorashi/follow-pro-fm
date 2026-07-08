# Handoff Report — WhatsApp Audio Waveform Data Investigation

## Observation
- In `go.mod` line 12, whatsmeow is imported:
  `go.mau.fi/whatsmeow v0.0.0-20260611094716-089932318bc2`
- The protobuf definition for `waE2E.AudioMessage` includes the `Waveform` field:
  ```go
  type AudioMessage struct {
      ...
      Waveform           []byte       `protobuf:"bytes,19,opt,name=waveform" json:"waveform,omitempty"`
      ...
  }
  ```
- In `pkg/poller/whatsapp.go` around line 286, `AudioMessage` is populated but `Waveform` is omitted:
  ```go
  // Construct AudioMessage with Push-To-Talk set to true (native voice note bubble)
  msg := &waE2E.Message{
      AudioMessage: &waE2E.AudioMessage{
          URL:           proto.String(uploaded.URL),
          DirectPath:    proto.String(uploaded.DirectPath),
          MediaKey:      uploaded.MediaKey,
          Mimetype:      proto.String("audio/ogg; codecs=opus"),
          FileEncSHA256: uploaded.FileEncSHA256,
          FileSHA256:    uploaded.FileSHA256,
          FileLength:    proto.Uint64(uint64(len(audioData))),
          PTT:           proto.Bool(true), // Makes it a native voice note
          Seconds:       proto.Uint32(estimatedSeconds),
      },
  }
  ```
- Tested single-file extraction using a custom script (`extract.go`) on `data/audios/WhatsApp Ptt 1.ogg` (4.72 seconds, 37743 samples decoded to PCM), which successfully extracted a 64-byte normalized waveform with values spanning `0-255` (peak value 255):
  `[0 0 6 2 20 7 1 255 236 0 0 1 1 4 38 10 233 219 109 114 159 153 93 56 110 9 13 210 170 143 122 108 77 5 1 0 33 127 35 121 210 136 65 165 140 67 184 96 117 117 34 158 195 169 155 96 22 140 113 90 7 76 63 26]`
- Verified multi-file consistency using `extract_multi.go`, confirming all `.ogg` files in `/Users/viktorashi/nerdin/pro-fm/data/audios` decode correctly and produce realistic waveform ranges.

## Logic Chain
- **Waveform Structure**: The `Waveform` field is a `[]byte` slice (serialized protobuf `bytes`). Each byte represents the relative height/amplitude of one vertical bar in the WhatsApp voice note bubble.
- **Opus Decoding**: Because Opus is a compressed audio format, packet headers and raw file sizes do not correlate with amplitude. The audio must be decoded to PCM to obtain amplitude levels.
- **PCM Extraction**: Decoding using ffmpeg with raw PCM output piped directly to stdout (`ffmpeg -i input.ogg -f s16le -ac 1 -ar 8000 -`) is highly performant and stable. It avoids writing temporary files and parsing unstructured ffmpeg stderr output.
- **Downsampling & Peak Detection**: Since a typical WhatsApp voice note visualizes about 64 bars, we can divide the total decoded 16-bit (`int16`) PCM samples into 64 uniform buckets. Finding the peak absolute amplitude within each bucket yields the amplitude envelope.
- **Normalization**: Different audio files have varying recording levels (e.g. peak amplitude of `13476` vs. `32767`). Absolute scaling would make quieter files look like flat lines. Normalizing each bucket's peak relative to the global peak value in the file ($V_i = \text{round}(Peak_i / GlobalPeak * 255)$) ensures that every voice note renders a detailed, high-fidelity waveform from `0` to `255`.

## Caveats
- **Minimal Sample Count**: Extremely short audio files (under 1 second) might have fewer than 64 samples at 8000Hz. If a file contains fewer than 64 samples, the bucket size will default to 1, and the resulting waveform should be padded with zeros to ensure a consistent length of 64 bytes.
- **FFmpeg Submodule dependencies**: To support this decoding, the minimal ffmpeg binary being prepared by explorer_2 must be configured with:
  - `--enable-demuxer=ogg`
  - `--enable-parser=opus`
  - `--enable-decoder=opus`
  - `--enable-muxer=pcm_s16le`
  - `--enable-encoder=pcm_s16le`

## Conclusion
- Waveform data in whatsmeow's `waE2E.AudioMessage` must be populated as a 64-byte `[]byte` slice containing peak amplitude values scaled to the range `0-255`.
- The recommended Go implementation runs `ffmpeg` to extract mono 16-bit PCM at 8000Hz, downsamples the signal to 64 buckets, and normalizes the peaks. Here is the proposed code snippet for `pkg/poller/whatsapp.go` (or a helper library):

```go
// ExtractWaveform decodes an OGG/Opus audio file using ffmpeg to mono 16-bit PCM,
// downsamples it to 64 buckets, normalizes the peaks, and returns a 64-byte slice
// representing the audio waveform for WhatsApp.
func ExtractWaveform(audioPath string) ([]byte, error) {
	// Decode OGG/Opus to 8000Hz mono 16-bit PCM and pipe to stdout
	cmd := exec.Command("ffmpeg", "-i", audioPath, "-f", "s16le", "-ac", "1", "-ar", "8000", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w (stderr: %s)", err, stderr.String())
	}

	pcmBytes := stdout.Bytes()
	sampleCount := len(pcmBytes) / 2
	if sampleCount == 0 {
		return make([]byte, 64), nil
	}

	// Parse raw PCM bytes to int16 samples
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

	// Scale peak amplitudes to range [0, 255] normalized to the global max peak
	waveform := make([]byte, numBuckets)
	for i, peak := range peaks {
		if maxPeak > 0 {
			waveform[i] = byte(math.Round(float64(peak) / float64(maxPeak) * 255.0))
		} else {
			waveform[i] = 0
		}
	}

	return waveform, nil
}
```

## Verification Method
- Execute the test script:
  `go run /Users/viktorashi/nerdin/pro-fm/.agents/explorer_3/extract.go`
- Verify it exits with code 0 and prints a non-empty 64-element byte slice where the largest value is exactly 255.
