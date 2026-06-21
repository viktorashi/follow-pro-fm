# WhatsApp Voice Note Investigation & Metadata Injection

## 1. Observation
We observed the following in the codebase:
- In `pkg/poller/whatsapp.go` (lines 230-239), the metadata injection via `ffmpeg` is performed *before* the composing/recording presence simulation sleep (lines 282-287):
  ```go
  230: 	now := time.Now().UTC().Format(time.RFC3339)
  231: 	tmpPath := fmt.Sprintf("%s.tmp.ogg", audioPath)
  232: 
  233: 	cmd := exec.Command("ffmpeg", "-y", "-i", audioPath, "-c", "copy", "-metadata", "creation_time="+now, tmpPath)
  234: 	if err := cmd.Run(); err != nil {
  235: 		fmt.Printf("   ⚠️ Failed to inject metadata with ffmpeg, falling back to original: %v\n", err)
  236: 		tmpPath = audioPath
  237: 	} else {
  238: 		defer func() { _ = os.Remove(tmpPath) }()
  239: 	}
  ```
  And then, the sleep takes place:
  ```go
  282: 	// 1. Send "recording audio" state to make it look authentic
  283: 	_ = client.SendChatPresence(context.Background(), targetJID, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)
  284: 
  285: 	// 2. Sleep for the duration of the audio to simulate recording time
  286: 	time.Sleep(time.Duration(estimatedSeconds) * time.Second)
  ```
- The creation of `waE2E.AudioMessage` is in `pkg/poller/whatsapp.go` (lines 288-299):
  ```go
  288: 	msg := &waE2E.Message{
  289: 		AudioMessage: &waE2E.AudioMessage{
  290: 			URL:           proto.String(uploaded.URL),
  291: 			DirectPath:    proto.String(uploaded.DirectPath),
  292: 			MediaKey:      uploaded.MediaKey,
  293: 			Mimetype:      proto.String("audio/ogg; codecs=opus"),
  294: 			FileEncSHA256: uploaded.FileEncSHA256,
  295: 			FileSHA256:    uploaded.FileSHA256,
  296: 			FileLength:    proto.Uint64(uint64(len(audioData))),
  297: 			PTT:           proto.Bool(true), // Makes it a native voice note
  298: 			Seconds:       proto.Uint32(estimatedSeconds),
  299: 		},
  300: 	}
  ```
- Running `go doc go.mau.fi/whatsmeow/proto/waE2E AudioMessage` showed the structural fields of `AudioMessage` which include:
  - `Mimetype *string`
  - `PTT *bool`
  - `Seconds *uint32`
  - `MediaKeyTimestamp *int64`
- There is a `patch_whatsapp.sh` in the project root directory which contains inline contents of `whatsapp.go` and overwrites `pkg/poller/whatsapp.go` when run.

## 2. Logic Chain
1. The metadata `creation_time` is injected into the `.ogg` file container via ffmpeg using `time.Now().UTC().Format(time.RFC3339)`.
2. Currently, this injection occurs *before* simulating the recording (which introduces a delay of `estimatedSeconds` - up to 30 seconds) and before uploading the audio to WhatsApp servers.
3. Therefore, by the time `client.SendMessage` is invoked and the message is delivered, the `creation_time` inside the OGG metadata is out of sync with the actual WhatsApp send/delivery timestamp by ~15-35 seconds.
4. To match the exact send timestamp, the remuxing step and timestamp generation should be shifted to occur *after* the simulated recording sleep completes, right before uploading the file.
5. In addition, `waE2E.AudioMessage` contains a `MediaKeyTimestamp *int64` field. Aligning this with `time.Now().Unix()` further synchronizes the internal WhatsApp media timestamp with the send time.

## 3. Caveats
- The metadata injection relies on the external `ffmpeg` binary. If `ffmpeg` is missing on the host or fails, the code defaults back to the original unmodified audio file (in which case the creation time will not match the send timestamp, and the file hash may not be unique).
- We assume that WhatsApp client apps use the internal OGG Vorbis comment tag `creation_time` to render the voice note timestamp.

## 4. Conclusion
We can successfully inject the exact send timestamp by shifting the metadata generation and `ffmpeg` remuxing step to execute immediately after the `time.Sleep(...)` (recording simulation) block. Additionally, we can optionally populate the `MediaKeyTimestamp` field in `waE2E.AudioMessage` to further match the message send time.

### Recommended Implementation Steps (for Implementer)
1. In `pkg/poller/whatsapp.go` (and also update the template in `patch_whatsapp.sh` if it is still used):
   - Move the duration extraction `GetAudioDuration(audioPath)` up, so we can determine `estimatedSeconds` before the sleep.
   - Run the presence composer and sleep.
   - After the sleep:
     - Generate `now := time.Now().UTC().Format(time.RFC3339)`.
     - Perform the `ffmpeg` remuxing command to inject the metadata.
     - Read the remuxed `tmpPath` bytes.
     - Upload the bytes to WhatsApp.
     - Construct the `waE2E.AudioMessage` with `MediaKeyTimestamp: proto.Int64(time.Now().Unix())`.
     - Call `SendMessage`.

## 5. Verification Method
- Execute the test suite using `just test` to verify that the core functions and duration extraction logic are not broken.
- Execute `just test-cover-e2e-nowapp` to verify that the simulated send flow works correctly with the rearranged function.
