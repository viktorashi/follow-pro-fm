# Codebase Exploration Report - E2E Test Suite

This report documents the E2E testing architecture for the ProFM Poller Go application, based on codebase exploration of entry points, configurations, existing tests, and requirements (R1-R6).

---

## 1. Entry Point Analysis: `cmd/pro-fm-poller/main.go`

The application entry point is located at `cmd/pro-fm-poller/main.go`.

### Current Configuration Injection
Configuration parameters are read from environment variables on startup. The following table details the current variables used and their defaults:

| Environment Variable | Description | Default Value |
| --- | --- | --- |
| `TARGET_PHONE` | Target WhatsApp phone number for campaign alerts | `+40770661491` |
| `WAPP_DB_PATH` | Path to whatsmeow WhatsApp connection store SQLite database | `/data/wapp.sqlite` |
| `APP_DB_PATH` | Path to the application state SQLite database | `/data/app.sqlite` |
| `SENDGRID_API_KEY` | SendGrid API key for email alerts | *(Empty)* |
| `EMAIL_FROM` | Sender address for notifications | `notifications@yourdomain.com` |
| `ADMIN_PASSWORD` | Password for admin authentication on dashboard | *(Empty)* |
| `BASE_URL` | Application root URL, used to build alert links | Derived from `FLY_APP_NAME` or defaults to `http://localhost:8080` |
| `TELEGRAM_BOT_TOKEN` | Telegram bot API token for Telegram alerts | *(Empty)* |
| `TELEGRAM_CHAT_ID` | Telegram chat ID where alerts are delivered | *(Empty)* |
| `AUDIOS_DIR` | Folder containing source voice notes (.ogg) | `/data/audios` |
| `ENVIRONMENT` | Application running mode/environment name | `production` |

### Hardcoded Parameters & Test Incompatibilities
To execute dynamic opaque-box E2E testing (running the actual compiled binary under test isolation), the following hardcoded elements present constraints that need correction:

1. **Telemetry Server Port**: Hardcoded in `main.go` to listen on `"0.0.0.0:8080"`. Running concurrent test instances will cause port conflicts.
   - *Recommendation*: Support reading a `PORT` or `TELEMETRY_ADDR` environment variable with `"0.0.0.0:8080"` as the fallback.
2. **ProFM Metadata URL**: The API endpoint `https://api.profm.ro/api/v1/radios/article/2918...` is defined as a `const` and passed directly to the `Poller`.
   - *Recommendation*: Read this URL from a `PROFM_API_URL` environment variable to redirect it to the Mock Metadata API server during E2E tests.
3. **ProFM Live Audio Stream URL**: The live audio stream URL (`http://edge76.rcs-rds.ro:84/profm/profm.mp3` used in R3) must be configurable.
   - *Recommendation*: Read this URL from a `PROFM_STREAM_URL` environment variable to redirect it to the Mock Live Stream server during E2E tests.

---

## 2. Existing Test Setup

### Existing Test Suite Files
1. **`pkg/poller/e2e_nowapp_test.go`**:
   - Runs with build tags: `//go:build e2e && nowapp`.
   - Bypasses real WhatsApp initialization by replacing the `Poller.SendVoiceNote` function pointer with a mock callback that logs the action.
   - Uses `httptest.NewServer` to mock the ProFM metadata API (returning a campaign hit `BTS` - `Dynamite`).
   - Directly calls `poller.checkSong(&currentSong, activeTime)` to verify metadata processing and trigger checks.
2. **`pkg/poller/e2e_wapp_test.go`**:
   - Runs with build tags: `//go:build e2e && !nowapp`.
   - Initializes a real `whatsmeow.Client` against a local SQLite database (`data/wapp.sqlite`) and prompts for actual QR pairing in the console if needed.
   - Requires a physical mobile device to scan the QR code and receive messages.
   - Listens for delivery and read receipts from WhatsApp servers using `whatsmeow.Client.AddEventHandler`.

### Execution Commands
Unit tests and E2E configurations are currently executed via `just` commands in the root `justfile`:

- **Unit tests**: `go test -count=1 ./pkg/...` (or `just test`)
- **No-WhatsApp E2E**: `go test -count=1 -v -tags="e2e,nowapp" ./pkg/...` (or `just test-cover-e2e-nowapp`)
- **All E2E (requires device)**: `go test -count=1 -v -tags=e2e ./pkg/...` (or `just test-cover-e2e-all`)

---

## 3. Comprehensive Opaque-Box E2E Testing Architecture

Opaque-box testing requires starting the server binary in a separate process, isolating its environment variables, and verifying behavior using public endpoints (HTTP dashboard, API endpoints, SSE streams, filesystem outputs) and external mocks.

### Mock 1: ProFM Metadata API Server
- **Type**: HTTP Server.
- **Port**: Configured dynamically.
- **Responsibility**: Expose the endpoint path `/api/v1/radios/article/2918` and return EPG JSON payloads matching `EPGData`.
- **Dynamic Controls**: The test runner can configure this mock via HTTP request headers or a dedicated control port to switch the currently playing song (e.g. return "BTS" to simulate a campaign hit, or "Kamrad" to simulate a non-campaign song, or alternate A and B to simulate metadata flickering).

### Mock 2: ProFM Live Audio Stream (MP3) Server
- **Type**: HTTP Stream Server.
- **Responsibility**: Serve a continuous chunk-by-chunk HTTP response containing MP3 frames mimicking `profm.mp3`.
- **Signatures**: To test audio fingerprinting (R4), the stream must output a loop containing specific audio signals (canonical intro signature) at known timestamps, allowing the E2E framework to verify if the server detects the stream intro and triggers voice note transmission before the Metadata API registers it.

### Mock 3: WhatsApp pairing and sending (WhatsApp Client Mock)
To test WhatsApp logic without actual external server connectivity or physical devices:
1. **Implementation via Env Flag**: Introduce a `MOCK_WHATSAPP=true` mode in the codebase.
2. **Mock Behavior**:
   - Instead of connecting to the real WhatsApp servers via WebSockets, the WhatsApp initialization generates a mock QR code string (e.g. `mock-qr-code-data-XYZ`) and updates the `StateManager` to `StatusPairingRequired`.
   - **Mock Scan API**: Expose a test-only API endpoint (e.g., `POST /api/test/mock-scan`) or a mechanism that simulates a successful QR code scan, triggering the mock client to transition to `StatusConnected` and creating the corresponding mock session record in the database.
   - **Mock Send**: Instead of uploading media to WhatsApp servers, the client writes the audio payloads, the waveform bytes, and metadata (phone, creation time, path) to a JSON file (e.g. `/data/mock_sent_messages.json`) or in-memory list which tests can assert.

---

## 4. Requirements Coverage Map (R1-R6)

The E2E test suite must verify the following core features:

### R1: WhatsApp send logic, ffmpeg waveforms, creation time, status
- **E2E verification**:
  - Verify that the server binary uses the custom compiled minimal `ffmpeg` binary to remux `.ogg` files.
  - Assert that the output file injected with metadata contains a `creation_time` exactly matching the send timestamp.
  - Parse the simulated sent message payload and check that the `Waveform` field in `waE2E.AudioMessage` is populated with a non-empty, non-flat array of byte values extracted from the audio.
  - Verify that during audio sending, the server state transitions: `StatusPolling` -> `StatusCampaignTriggered` -> `StatusSendingAudio` -> `StatusPolling` (success).

### R2: Multiple WhatsApp numbers, QR pairing, per-phone directories, no duplicate sends
- **E2E verification**:
  - Configure the application with multiple target WhatsApp phone numbers.
  - Verify that the dashboard displays independent QR codes for each unconnected number.
  - Simulate QR scans for multiple numbers, confirming that independent database files (`wapp_<phone>.sqlite`) are created.
  - Verify that a file uploaded or lookup operation for phone A is contained within `/data/audios/<phoneA>/` (or `/data/audios/` for the original canonical phone), and is non-recursive.
  - Assert the global deduplication invariant: triggering the same campaign song twice on different phone sessions does NOT send the voice note again, and verify that at least one intervening non-campaign song is required before another send.

### R3: Circular buffer recording, no double-triggering
- **E2E verification**:
  - Spin up the mock live audio stream.
  - Simulate an API metadata campaign match without previous fingerprint detection. Verify that the server saves a continuous 7-minute chunk (3 min pre-trigger, 4 min post-trigger) to the API listing endpoint.
  - Simulate an API metadata campaign match *after* an audio fingerprint intro was already matched. Verify that no new circular buffer chunk is saved (no double-triggering).

### R4: Signature gathering toggle, non-duplicate templates, crop/review endpoints, pre-metadata matching trigger
- **E2E verification**:
  - Verify `GatheringSignatures` defaults to `true`. Toggle it via the dashboard endpoint and check if state changes.
  - Toggle signature gathering on, stream new audio signals, and verify they are stored in the unreviewed folder. Ensure duplicate streams do not create duplicate unreviewed files.
  - Call the API endpoint to list, serve, and crop a segment, creating a canonical intro signature copy. Verify the original chunk remains untouched.
  - Inject a canonical signature, then stream the intro through the mock live stream. Verify that a voice note is triggered *before* the Metadata API updates.
  - Verify that subsequent metadata updates for the same song do not trigger a second send. Check that logs clearly state "detected via audio signature" vs "detected via API metadata".

### R5: Daily RNG selection, persistence, editing, and backfill
- **E2E verification**:
  - Start the application. Verify that a complete RNG selection map is generated for all active campaign days and stored in the database.
  - Restart the application. Assert that existing RNG configurations are preserved and only missing days are backfilled.
  - Call the schedule API endpoint to edit a day's schedule. Verify that validation enforces campaign boundaries (Mon-Fri, 07:00-20:00).
  - Verify that if the RNG schedule dictates "do not respond" for a specific song event, the poller log and state record the skip and do not send a voice note.

### R6: File upload per phone
- **E2E verification**:
  - Make a multipart POST request to the upload endpoint selecting a target phone number and uploading a test `.ogg` file.
  - Verify the file is correctly written to `/data/audios/<phone>/` (or root for the canonical phone).
  - Confirm the dashboard UI functions normally post-upload.

---

## 5. Opaque-Box Testing Strategy & Recommendations

1. **Modify Main to Support Test Parameters**:
   - Change `main.go` to parse the port, metadata API URL, and audio stream URL from environment variables.
2. **Implement WhatsApp Mock Client**:
   - Provide a mock client wrapper activated via `MOCK_WHATSAPP=true` that mimics whatsmeow event handlers, generates dummy base64 QR codes, and records sent messages locally.
3. **Use a Go E2E Runner**:
   - Create E2E test files in a dedicated test suite (e.g. `tests/e2e/e2e_test.go`) that compiles the binary, spins up mock HTTP services, launches the server process, runs tests using standard `net/http` clients, and asserts state using SSE/API responses.
4. **Isolate Test Database & Files**:
   - Use a unique directory path (e.g., `t.TempDir()`) for each test run, overriding `WAPP_DB_PATH`, `APP_DB_PATH`, and `AUDIOS_DIR` to avoid interference with production files.
