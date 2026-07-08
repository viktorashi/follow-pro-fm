# E2E Test Suite & Branch Review Report — Reviewer 2

## Review Summary

**Verdict**: APPROVE

This E2E test suite and corresponding feature implementation (`feature/m1-whatsapp-ffmpeg`) are extremely clean, robust, and compile and pass all tests successfully. No integrity violations or dummy shortcuts were detected.

---

## Quality Review

### Findings

#### [Minor] Finding 1: Timezone Reliance on Environment Variables
- **What**: The timezone location for campaign check logic (`IsActive` in `pkg/poller/poller.go`) relies on the system's local time (`time.Now()`).
- **Where**: `pkg/poller/poller.go`, line 44 (`IsActive` function).
- **Why**: If a developer or a deployment environment does not configure the environment variable `TZ=Europe/Bucharest`, the campaign hours (07:00 to 20:00) will be checked against the host's system time, which might be different from Romanian local broadcast hours.
- **Suggestion**: Although `Dockerfile` and `fly.toml` correctly specify `TZ=Europe/Bucharest`, it is recommended to add a fallback or log a warning if the application starts up in a timezone other than `Europe/Bucharest`, or explicitly parse/verify using a fixed Romanian timezone location (`time.LoadLocation("Europe/Bucharest")`).

#### [Minor] Finding 2: Direct Ffmpeg Binary Execution
- **What**: The `ExtractWaveform` and `SendVoiceNote` functions execute `ffmpeg` using `exec.Command("ffmpeg", ...)`.
- **Where**: `pkg/poller/whatsapp.go`, lines 292 & 549.
- **Why**: This assumes `ffmpeg` is available on the system `PATH`. While the E2E tests compile it locally, and Docker/Fly have it installed, local development runtimes might fail silently or error out if `ffmpeg` isn't installed.
- **Suggestion**: Add a startup check in `main.go` that runs `exec.LookPath("ffmpeg")` and prints a helpful error message if it is not found.

### Verified Claims
- **WhatsApp status transitions correctly** → verified via `F1_Test_01_WhatsApp_Status_Transitions` → PASS
- **FFmpeg copy metadata injection adds `creation_time`** → verified via `F1_Test_02_Audio_Remux_CreationTime` → PASS
- **Waveform extraction decodes and normalizes to a 64-byte slice** → verified via `F1_Test_03_Waveform_Field` & `TestExtractWaveform_Stress` → PASS
- **Voice note files are successfully marked as used and moved** → verified via `F1_Test_04_Audio_Marked_Used` → PASS
- **Alerter triggers when pairing is required** → verified via `F1_Test_05_Alerter_Triggers_On_Disconnect` → PASS
- **Flicker deduplication prevents double-transmissions** → verified via `Scenario_69_Metadata_Flicker_And_Deduplication` → PASS

### Coverage Gaps
- **Multiple WhatsApp Connections (F2)** — risk level: LOW (planned for future milestones, currently skipped as placeholder E2E tests).
- **Circular Audio Buffer (F3)** — risk level: LOW (planned for future milestones).
- **Audio Signature Fingerprinting (F4)** — risk level: LOW (planned for future milestones).
- **Daily RNG Selection (F5)** — risk level: LOW (planned for future milestones).
- **Dashboard Audio Upload (F6)** — risk level: LOW (planned for future milestones).

### Unverified Items
- None. All current features have corresponding automated test cases.

---

## Challenge Report / Adversarial Review

**Overall risk assessment**: LOW

### Challenges

#### [Medium] Challenge 1: Empty or Missing Audio Files
- **Assumption challenged**: The audio directory will always contain valid Opus files.
- **Attack scenario**: If the `audios` directory is empty or contains only corrupted files, the application might panic or enter an infinite loop.
- **Blast radius**: Poller fails to fetch audio, state transitions to `StatusAudioExhausted`, and alerts are sent. However, the server remains running.
- **Mitigation**: The code handles this gracefully. The `GetRandomAudio` helper returns an error which is captured, updating state to `StatusAudioExhausted` and triggering a critical alert without crashing.

#### [Low] Challenge 2: Int16 Peak Overflow in Ffmpeg Waveform Processing
- **Assumption challenged**: Mathematical negation of sample values is always safe.
- **Attack scenario**: The absolute value of the minimum possible 16-bit signed integer (`-32768`) is representationally too large for a positive signed 16-bit integer, which can cause mathematical overflow/underflow loops.
- **Blast radius**: Faulty peak calculations if `-32768` occurs in the stream.
- **Mitigation**: The implementation in `ExtractWaveform` explicitly handles this boundary value:
  ```go
  if val == -32768 {
      val = 32767
  }
  ```
  This is a highly robust mitigation.

#### [Low] Challenge 3: Port Collision on Dashboard Startup
- **Assumption challenged**: The port configured via `PORT` env var is always free.
- **Attack scenario**: Starting two poller instances on the same host.
- **Blast radius**: The telemetry server fails to bind and exits immediately, terminating the application.
- **Mitigation**: Checked in `F1_Test_34_Telemetry_Port_Conflicts` E2E test. The application exits cleanly with a log error, which is the correct behavior for containerized orchestration.

### Stress Test Results
- **Stress-testing ExtractWaveform with silent audio** → returns all-zero slice → PASS
- **Stress-testing ExtractWaveform with corrupted file** → fails gracefully returning error → PASS
- **Stress-testing ExtractWaveform with loud-then-silent clips** → returns correct normalized buckets → PASS
- **Stress-testing ExtractWaveform with extremely short file** → pads to 64 buckets without panic → PASS
