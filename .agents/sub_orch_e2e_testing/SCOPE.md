# Scope: ProFM Go E2E Test Suite

## Architecture
- The E2E tests operate strictly from an opaque-box perspective, executing the ProFM application binary and using exposed HTTP/SSE APIs and Mock/Stub targets.
- Interfaces under test include:
  - Radio stream input (mp3 stream mock)
  - ProFM metadata API (poller mock)
  - WhatsApp Client / Whatsmeow interaction (mocked WhatsApp client that captures sends to a JSON file)
  - HTTP REST endpoints & Server-Sent Events (SSE) stream

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| 1 | Discovery & Design | Explore existing codebase and formulate exact test case scenarios | none | DONE |
| 2 | Test Infra Setup | Build mock server endpoints for ProFM metadata API, radio MP3 stream, and WhatsApp message sink | M1 | IN_PROGRESS |
| 3 | Tier 1 Tests | Implement Feature Coverage tests (31 tests) | M2 | PLANNED |
| 4 | Tier 2 Tests | Implement Boundary & Corner Cases (30 tests) | M3 | PLANNED |
| 5 | Tier 3 Tests | Implement Cross-Feature Combinations (6 tests) | M4 | PLANNED |
| 6 | Tier 4 Tests | Implement Real-World Application Scenarios (5 tests) | M5 | PLANNED |
| 7 | Documentation & Publish | Produce TEST_INFRA.md and publish TEST_READY.md | M6 | PLANNED |

## Interface Contracts
### E2E Tests ↔ ProFM Application
- The ProFM Go binary will be run in a separate process with custom configuration (port, DB paths, environment variables).
- E2E Tests will interact with:
  - HTTP endpoints for Pairing QR, States, RNG Schedule, Dashboard Upload, Dashcam REST API
  - Mocked endpoints simulating ProFM API and MP3 live stream.

## Feature Inventory & Test Cases

### Feature A: WhatsApp Send & Waveforms (F1)
#### Tier 1: Feature Coverage (Happy-Path)
1. Test WhatsApp status transitions to `StatusPolling` upon successful connection.
2. Test that when a campaign song is detected, the audio file is correctly remuxed and its `creation_time` matches the send timestamp.
3. Test that the `Waveform` field in the sent audio message contains non-flat, non-empty waveform bytes.
4. Test that the voice note is marked as used and moved to the `/used` directory.
5. Test that the alerter triggers (Telegram/Email) when WhatsApp transitions to `StatusPairingRequired`.
#### Tier 2: Boundary & Corner Cases
32. Send with empty/missing audio file pool (verify error state or graceful fallback).
33. Send with a corrupted/malformed `.ogg` file (verify ffmpeg handles it gracefully without crashing).
34. Telemetry port conflicts: check that trying to bind to an in-use port handles the error (or we test the port parsing bounds).
35. Verify `creation_time` format injection handles timezone boundary transitions correctly.
36. Verify waveform extraction from extremely short audio clips (e.g., <1 second) works and produces valid bytes.

### Feature B: Multiple WhatsApp Connections (F2)
#### Tier 1: Feature Coverage (Happy-Path)
6. Test initialization of multiple independent SQLite databases (one for each connection).
7. Test dashboard displays independent QR codes for each unconnected WhatsApp number.
8. Test pairing multiple sessions simultaneously.
9. Test that voice notes are stored and retrieved from independent, non-recursive, per-phone subdirectories.
10. Test that sending is deduplicated globally (e.g., if one connection sends, the other doesn't send the same voice note or trigger again immediately).
#### Tier 2: Boundary & Corner Cases
37. Test invalid phone number formats are normalized correctly (e.g. spaces, brackets, prefix variations).
38. Test lookup behaviour when a phone directory contains files with naming collisions or sub-directories (non-recursive assertion).
39. Test session database file lock recovery when multiple instances start simultaneously (SQLite WAL mode check).
40. Test pairing timeout: verify that when a QR code pairing times out, the client updates the status correctly.
41. Test disconnect/reconnect endpoints for a specific client session.

### Feature C: Circular Audio Buffer (F3)
#### Tier 1: Feature Coverage (Happy-Path)
11. Test that the circular buffer records continuously from the MP3 stream mock.
12. Test that a 7-minute buffer chunk is saved upon API metadata campaign match when no signature was matched first.
13. Test that no buffer chunk is saved if the campaign song was already matched via audio signature (no double-triggering).
14. Test listing saved buffer chunks via REST API.
15. Test serving/playing saved buffer chunks via REST API.
#### Tier 2: Boundary & Corner Cases
42. MP3 stream mock disconnects mid-buffer (verify recovery of recording loop).
43. Metadata API updates with a non-campaign song (verify buffer is NOT saved).
44. Stream latency: API metadata is received *before* the MP3 stream intro buffer is filled to 3 minutes (verify short chunk saving).
45. Buffering memory bounds: verify circular buffer does not grow beyond 3 minutes in memory.
46. Call buffer endpoint with invalid/non-existent chunk ID (verify 404).

### Feature D: Audio Signature Fingerprinting & Detection (F4)
#### Tier 1: Feature Coverage (Happy-Path)
16. Test that `GatheringSignatures` defaults to true on startup and can be toggled via dashboard.
17. Test that new unreviewed audio samples are saved in `/unreviewed` only if they don't match existing canonical signatures.
18. Test dashboard alerts are created when new unreviewed samples are available.
19. Test cropping an audio chunk via REST API creates a canonical signature copy while leaving the original untouched.
20. Test that playing an intro signature in the mock MP3 stream triggers a voice note send before the Metadata API registers it, logging the source as "audio signature".
#### Tier 2: Boundary & Corner Cases
47. Toggle `GatheringSignatures` repeatedly rapidly (verify state consistency).
48. Signature matching confidence boundary: verify signature matches just at the threshold vs just below the threshold.
49. Attempt signature crop with invalid/negative bounds (verify validation rejection).
50. Verify matching algorithm handles noisy streams (e.g., background static added to canonical signature).
51. Multiple overlapping intro signatures in the stream (verify deduplication).

### Feature E: Daily RNG Selection (F5)
#### Tier 1: Feature Coverage (Happy-Path)
21. Test pre-population of daily RNG selection map for all campaign days at startup.
22. Test persistence of RNG selections in SQLite (survives restart, no override of existing).
23. Test backfilling missing days on startup.
24. Test retrieving the daily RNG schedule via REST API.
25. Test updating a day's schedule via REST API within valid boundaries.
26. Test that when RNG schedule says "do not send", the song is skipped and no voice note is sent.
#### Tier 2: Boundary & Corner Cases
52. Pre-population when campaign date range contains no business days (verify it handles empty intervals gracefully).
53. Update schedule with date outside campaign boundaries (verify 400 validation error).
54. Update schedule with invalid time format (verify 400).
55. Startup backfill when database is read-only (verify graceful error handling).
56. RNG selection when multiple campaigns overlap (verify correct day allocation).

### Feature F: Dashboard Per-Phone Audio Upload (F6)
#### Tier 1: Feature Coverage (Happy-Path)
27. Test uploading a `.ogg` file to the canonical phone directory (root of `audios`).
28. Test uploading a `.ogg` file to a non-canonical phone directory (`audios/<phone>`).
29. Test uploading invalid file formats (e.g., `.txt`) is rejected.
30. Test that the upload form handles non-existent directories by creating them.
31. Test that the file upload does not interfere with the active connections list on the dashboard.
#### Tier 2: Boundary & Corner Cases
57. Upload file exceeding maximum size limit (verify rejection).
58. Upload with empty phone parameter (verify rejection).
59. Concurrent uploads of the same file name to different phone directories (verify no collisions).
60. Upload filename with directory traversal characters (e.g. `../../used/`) (verify path traversal protection).
61. Upload file when disk is full / permissions are read-only (verify graceful error).

### Tier 3: Cross-Feature Combinations
62. **F2 + F5 (Multi-Phone + RNG)**: Test that when daily RNG is enabled, different phone connections use their own per-connection database and state but respect the same global RNG schedule.
63. **F3 + F4 (Circular Buffer + Fingerprinting)**: Test that an intro matched via signature prevents the circular buffer from saving for that same detection (verifying R3 + R4 interaction).
64. **F1 + F6 (Send + Upload)**: Test that uploading a new voice note via dashboard immediately makes it available in the audio pool for WhatsApp sending on the correct phone.
65. **F2 + F4 (Multi-Phone + Fingerprinting)**: Test that when an audio signature is matched on the stream, the voice note is sent only via the active/selected WhatsApp connection.
66. **F1 + F2 + F5 (Send + Multi-Phone + RNG)**: Test a scenario where RNG enables a day, but one phone is disconnected (StatusPairingRequired) and another is connected. The system should send the voice note from the connected phone.
67. **F3 + F4 + F5 (Circular Buffer + Fingerprinting + RNG)**: Test that if RNG selection has disabled the song for today, an audio signature match does NOT trigger a voice note send, nor does the metadata API, but signature gathering still records the sample if gathering is enabled.

### Tier 4: Real-World Application Scenarios
68. **Scenario 1: Complete Campaign Day Lifecycle (Happy Path)**:
    - Server starts up, generates RNG schedule, initializes multiple WhatsApp clients.
    - User logs in, scans QR code for client 1.
    - Non-campaign songs play on the radio.
    - A campaign artist (e.g., BTS) song plays (during campaign hours, allowed by RNG).
    - Poller detects it, remuxes a voice note with correct `creation_time`, sends it, extracts waveform, marks it used, and alerts.
    - Another non-campaign song plays.
    - A second campaign song plays, poller sends another voice note.
69. **Scenario 2: The Metadata Flicker and Deduplication**:
    - The ProFM API has the flickering bug: A -> B -> A -> B.
    - Test that the poller handles this flicker without sending multiple voice notes, keeping the global state consistent (only 1 voice note sent).
70. **Scenario 3: Signature Fingerprint Early Detection and Metadata Deduplication**:
    - Live MP3 stream plays the campaign song intro.
    - Audio fingerprinting matches it and triggers a voice note send via WhatsApp client.
    - A few seconds later, the ProFM metadata API updates to show the campaign song is playing.
    - The poller recognizes the song is already sent for this campaign turn and does NOT send another voice note (no double-triggering).
71. **Scenario 4: RNG Schedule Enforcement and Manual Override**:
    - Startup initializes a schedule. Today is scheduled to be skipped.
    - A campaign song plays. The poller logs that it is skipped due to RNG.
    - The user visits the dashboard, views the schedule, and manually overrides today's schedule to "SEND".
    - The next time the campaign song plays, the poller detects it and successfully sends the voice note.
72. **Scenario 5: Multi-Phone Failover and Audio Separation**:
    - Phone A and Phone B are configured. Phone A is disconnected (pairing required), Phone B is connected.
    - A campaign song plays. The system attempts to send. Since Phone A is offline, it fails over to Phone B and sends the voice note using Phone B's audio directory pool.
    - Later, the user uploads a new custom audio file specifically for Phone B.
    - When another campaign song plays, Phone B sends this newly uploaded audio file, while Phone A's pool remains unchanged.
