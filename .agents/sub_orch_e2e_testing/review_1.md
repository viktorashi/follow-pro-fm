# Code Review & Adversarial Challenge Report — Reviewer 1

## Quality Review Report

### Review Summary
**Verdict**: REQUEST_CHANGES

The E2E Test Suite and corresponding integration code in `feature/m1-whatsapp-ffmpeg` are functionally complete enough to compile and execute successfully. However, there are critical architectural and logical flaws that must be addressed before this code is ready for staging or merging:
1. **Critical correctness bug** in timezone handling where Bucharest campaign dates and times are evaluated in the server's local timezone (usually UTC on Fly.io).
2. **Major concurrency data race** on the `wappClient` pointer in `TelemetryServer` during application startup.
3. **Major safety risk** from invoking external `ffmpeg` commands without a timeout context, which can cause background goroutines to hang indefinitely.
4. **Facade verification** in the E2E test suite that masks the true behavior of the `ffmpeg` metadata injection.

---

### Findings

#### [Critical] Finding 1: Timezone Correctness Violation
- **What**: The campaign active-time checks evaluate dates and times in the server's local timezone instead of Romania's timezone (`Europe/Bucharest`).
- **Where**: `pkg/poller/poller.go:44` (`IsActive` method) and `pkg/poller/poller.go:236` (`checkSong` reset logic).
- **Why**: Romania operates in EET/EEST (UTC+2/UTC+3). If the server runs on UTC (default on Fly.io or standard containers), campaign windows (07:00 to 20:00) and weekend checks will be calculated incorrectly. This leads to polling and sending messages during the night or weekends in Romania, or failing to capture matches during early campaign hours.
- **Suggestion**: Load the Bucharest timezone explicitly using `time.LoadLocation("Europe/Bucharest")` and convert the server's current time to Bucharest time before executing any campaign status checks:
  ```go
  loc, err := time.LoadLocation("Europe/Bucharest")
  if err != nil {
      log.Printf("Failed to load Bucharest timezone: %v", err)
      // fallback to EET/EEST UTC offset if timezone db is missing
  }
  nowBucharest := now.In(loc)
  ```

#### [Major] Finding 2: Concurrency Data Race on `wappClient` in Telemetry Server
- **What**: The `wappClient` interface field is read and written concurrently across different goroutines without synchronization.
- **Where**: `pkg/poller/server.go:361` (`SetWhatsAppClient`) and `pkg/poller/server.go:365` (`handleMockScan`).
- **Why**: `SetWhatsAppClient` is invoked in the main goroutine during startup *after* the telemetry server has been started in a separate goroutine (`go telemetryServer.Start(...)`). If a request hits `/api/test/mock-scan` concurrently with initialization, it results in a data race on the `wappClient` interface value (a 16-byte structure in Go containing a type descriptor and a data pointer). Concurrently modifying this interface while reading it is unsafe and can lead to panics or memory corruption.
- **Suggestion**: Protect the `wappClient` pointer using a `sync.RWMutex` or an `atomic.Value` inside `TelemetryServer`.

#### [Major] Finding 3: Missing Timeout Context on External `ffmpeg` Calls
- **What**: Invocation of the `ffmpeg` CLI runs without a timeout constraint.
- **Where**: `pkg/poller/whatsapp.go:292` (metadata injection) and `pkg/poller/whatsapp.go:549` (`ExtractWaveform`).
- **Why**: Running external CLI utilities like `ffmpeg` without a context timeout can cause the poller/uploader goroutines to block indefinitely if `ffmpeg` hangs on corrupted files or runs slowly under high resource utilization.
- **Suggestion**: Use `exec.CommandContext` with a reasonable timeout (e.g., 5 seconds) instead of `exec.Command` to prevent goroutine starvation.

#### [Minor] Finding 4: Untracked Comments in Main File
- **What**: Leftover debug/gripe comments.
- **Where**: `cmd/pro-fm-poller/main.go:193` (`/// coaie de ce naiba nu vad aasta in git tracking?`).
- **Why**: Reduces code cleanliness and professionalism.
- **Suggestion**: Remove debug comments before submitting for final integration.

---

### Verified Claims

- **Claim 1**: E2E test suite compiles and runs successfully.
  - *Method*: Executed `PATH="./bin:$PATH" go test -count=1 -v ./tests/e2e/...` in the repository root.
  - *Status*: PASS (took 28.69s).
- **Claim 2**: Waveform extraction extracts a valid 64-byte PCM slice and downsamples/normalizes it correctly.
  - *Method*: Verified by examining the output of `F1_Test_03_Waveform_Field` and analyzing `ExtractWaveform` in `pkg/poller/whatsapp.go`.
  - *Status*: PASS (successfully normalizes peak sample values relative to the maximum peak).

---

### Coverage Gaps

- **Multiple WhatsApp Connections (F2)** — risk level: HIGH — recommendation: Gaps exist where multi-phone databases and directories are skipped/placeholders in tests. Investigate and implement tests for multiple SQL store instances.
- **Circular Audio Buffer (F3)** — risk level: HIGH — recommendation: Circular buffer chunk list checking is skipped. Investigate and add verification for REST endpoints.
- **Audio Signature Fingerprinting (F4)** — risk level: MEDIUM — recommendation: Toggle endpoints and fingerprint matching are unimplemented and skipped. Recommend adding mocks for raw audio matching.
- **Daily RNG Selection (F5)** — risk level: HIGH — recommendation: RNG-based Daily Schedule persistence and overrides are skipped. Validate persistence across app restarts.
- **Dashboard Audio Upload (F6)** — risk level: MEDIUM — recommendation: Multipart uploads are skipped in tests. Recommend testing file validation limits.

---

### Unverified Items

- **Real WhatsApp Client integration** — The actual uploader integration with whatsmeow was not verified against real WhatsApp servers as we are operating in a mocked network environment.

---

## Adversarial Review (Challenge) Report

### Challenge Summary
**Overall risk assessment**: HIGH

While the core functionality of waveform extraction and server mock endpoints works in happy-path simulations, several hidden assumptions make the system vulnerable to failure and mask issues during testing.

---

### Challenges

#### [Critical] Challenge 1: Facade E2E Verification of Audio Metadata Injection
- **Assumption challenged**: The test `F1_Test_02_Audio_Remux_CreationTime` verifies that `ffmpeg` successfully injects `creation_time` into the OGG metadata.
- **Attack scenario**: If `ffmpeg` fails to compile, fails to execute, or writes corrupted metadata, the E2E test will still PASS.
- **Blast radius**: The `MockWhatsAppClient`'s mock uploader completely ignores the uploaded bytes. Instead, its mock `SendMessage` method hardcodes the `CreationTime` field to `time.Now().UTC()`, and the test validates this mock-generated time. Therefore, broken metadata injection will bypass automated testing and only be caught in production.
- **Mitigation**: Update `MockWhatsAppClient.Upload` to parse the OGG metadata of the received byte slice (using a Go OGG parser or running `ffprobe` in the mock client) and return an error or register the parsed metadata timestamp in the JSON records.

#### [Medium] Challenge 2: Timezone Test Bypasses Campaign Time Checks
- **Assumption challenged**: `F1_Test_35_Timezone_Boundary_Transitions` verifies timezone handling rules under boundary constraints (e.g. UTC+14).
- **Attack scenario**: If the timezone logic in `poller.go` fails to handle boundary changes, the test still passes.
- **Blast radius**: The E2E test sets `BYPASS_CAMPAIGN_TIME_CHECKS=true`. This causes `IsActive` to return `true` immediately without evaluating any hour/weekday rules, rendering the timezone setting (`TZ=Pacific/Kiritimati`) useless for checking active campaign boundaries. Timezone boundary bugs will go undetected.
- **Mitigation**: Write an E2E test case where `BYPASS_CAMPAIGN_TIME_CHECKS` is set to `false`, and instead inject a mocked clock/time (or bypass target time) via environment variables or control APIs to verify transition boundaries.

#### [Low] Challenge 3: PCM Downsampling Memory limits
- **Assumption challenged**: `ExtractWaveform` assumes `ffmpeg` output will always fit in memory safely.
- **Attack scenario**: If a user uploads a very long audio file (e.g., several hours of audio) to the uploader endpoint, `stdout.Bytes()` will grow proportionally to the raw PCM data size, risking out-of-memory (OOM) crashes.
- **Blast radius**: Telemetry server crash due to memory exhaustion.
- **Mitigation**: Limit the maximum size or duration of audio processed by `ExtractWaveform` by passing `-t` (duration limit) to `ffmpeg` or checking the file size before invoking `ffmpeg`.
