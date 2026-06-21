# Handoff Report - Code Review of E2E Test Suite and integrations

## 1. Observation
- `pkg/poller/poller.go:44`: The campaign check evaluates hour and weekday using the server's local location:
  ```go
  if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday { ... }
  if now.Hour() < 7 || now.Hour() >= 20 { ... }
  ```
- `pkg/poller/server.go:361`, `365`: `SetWhatsAppClient` writes to `s.wappClient` concurrently with `handleMockScan` reading it, without synchronization.
- `pkg/poller/whatsapp.go:292`, `549`: Invocation of `ffmpeg` CLI is executed via:
  ```go
  cmd := exec.Command("ffmpeg", ...)
  ```
  without context timeout.
- `pkg/poller/whatsapp.go:493`: The mock client's `SendMessage` writes a hardcoded timestamp:
  ```go
  record := MockSentMessage{
      Phone:        to.User,
      Waveform:     waveform,
      CreationTime: time.Now().UTC(),
  }
  ```
- `tests/e2e/e2e_test.go:596`: The timezone boundary test runs with:
  ```go
  "BYPASS_CAMPAIGN_TIME_CHECKS=true",
  ```
- Running `PATH="./bin:$PATH" go test -count=1 -v ./tests/e2e/...` returns `PASS` (28.69s).

## 2. Logic Chain
- Standard servers (such as Fly.io) default to UTC. Since Romania operates in EET/EEST (UTC+2/UTC+3), comparing UTC hour directly to Romanian campaign hours (07:00-20:00) causes campaign active checks to evaluate incorrectly.
- Writing to `s.wappClient` from the main thread while reading from it concurrently in an Echo handler thread creates a Go data race on the interface value.
- Invoking `ffmpeg` without a timeout context runs the risk of indefinitely hanging background poller and server goroutines if `ffmpeg` hangs.
- Hardcoding the mock client creation time and setting bypass flags in tests masks failures, creating a facade verification that does not test actual file remuxing or timezone rules.

## 3. Caveats
- Review was conducted solely on branch `feature/m1-whatsapp-ffmpeg`. Unimplemented features (F2 to F6) were skipped in E2E tests and could not be reviewed.

## 4. Conclusion
- The branch `feature/m1-whatsapp-ffmpeg` requires changes before staging or merging. The review verdict is **REQUEST_CHANGES** due to:
  1. Incorrect timezone matching for Romanian campaigns.
  2. Concurrency data race on `wappClient`.
  3. Indefinite hang risks in `ffmpeg` command invocations.
  4. Facade E2E test assertions.

## 5. Verification Method
- Execute the E2E test suite:
  ```bash
  PATH="./bin:$PATH" go test -count=1 -v ./tests/e2e/...
  ```
- Inspect findings in the newly generated `/Users/viktorashi/nerdin/pro-fm/.agents/sub_orch_e2e_testing/review_1.md` report.
