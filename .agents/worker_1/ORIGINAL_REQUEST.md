## 2026-06-21T17:53:43Z
You are the E2E Test Suite Implementer.
Your task is to implement the E2E testing infrastructure and the 4 tiers of test cases (72 tests in total) as designed in /Users/viktorashi/nerdin/pro-fm/.agents/sub_orch_e2e_testing/SCOPE.md.

Specifically:
1. Modify `pkg/poller/whatsapp.go`:
   - Define a `WhatsAppClient` interface that matches the required `whatsmeow.Client` methods used by the application (Connect, Disconnect, IsConnected, IsLoggedIn, SendPresence, SendChatPresence, IsOnWhatsApp, Upload, SendMessage, AddEventHandler).
   - Refactor `InitWhatsApp` and `SendVoiceNote` to return and accept `WhatsAppClient` instead of `*whatsmeow.Client`.
   - Implement `MockWhatsAppClient` (which implements `WhatsAppClient`) that is returned by `InitWhatsApp` when the environment variable `MOCK_WHATSAPP=true` is set.
   - In `MockWhatsAppClient`, mock the pairing process (updates AppState status, QR code data) and the messaging process (saves sent voice note records including waveform bytes, phone, creation time to a JSON file specified by `MOCK_SENT_MESSAGES_PATH` or `/tmp/mock_sent_messages.json`).
   - Implement a method `SimulatePairing()` on `MockWhatsAppClient` to simulate a QR scan, transition status to connected, and save pairing state in the mock database file.

2. Modify `pkg/poller/server.go`:
   - Add a `wappClient WhatsAppClient` field to `TelemetryServer` and a `SetWhatsAppClient(client WhatsAppClient)` setter.
   - In `registerRoutes()`, if `MOCK_WHATSAPP=true` environment variable is set, register a route `POST /api/test/mock-scan` that calls `SimulatePairing()` on the mock client.

3. Modify `cmd/pro-fm-poller/main.go`:
   - Parse configuration parameters (`PORT`, `PROFM_API_URL`, `MOCK_WHATSAPP`) from environment variables instead of hardcoding port `8080` and the ProFM API URL.
   - Set the WhatsApp client on the telemetry server after both are initialized.

4. Create the E2E test suite in `tests/e2e/e2e_test.go`:
   - Group the 72 E2E test cases designed in SCOPE.md into 4 tiers using `t.Run()` (Tier 1: Feature Coverage 1-31, Tier 2: Boundary & Corner 32-61, Tier 3: Cross-Feature 62-67, Tier 4: Real-World Scenarios 68-72).
   - Implement the test runner setup that compiles the `pro-fm-poller` binary to a temp path, spins up a Mock ProFM Metadata API server and a Mock MP3 live stream server, and runs the compiled binary under isolated temp directories with custom env vars.
   - Implement assertions for the test cases. Since features R2, R3, R4, R5, and R6 are not yet implemented in the codebase, the tests for those features are expected to fail (or you can verify that they fail for the right reasons, e.g., HTTP 404 or missing endpoints/files, or skip them if they cannot compile/run, but the assertions themselves must be fully written out).

5. Verify that:
   - `go build` of the application succeeds.
   - Existing unit tests (`go test -count=1 ./pkg/...`) continue to pass.
   - The new test suite compiles and runs.

MANDATORY INTEGRITY WARNING: DO NOT CHEAT. All implementations must be genuine. DO NOT hardcode test results, create dummy/facade implementations, or circumvent the intended task. A Forensic Auditor will independently verify your work. Integrity violations WILL be detected and your work WILL be rejected.

Provide a detailed handoff report when complete, listing the files created/modified, the results of the unit tests, and how to run the E2E tests.
