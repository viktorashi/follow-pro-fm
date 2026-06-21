# Original User Request

## 2026-06-21T14:49:26Z

Enhance an existing Go application (ProFM radio monitor + WhatsApp voice note sender) to fix critical sending bugs, add multi-phone WhatsApp support, implement a live audio fingerprinting/dashcam system, and add daily RNG selection logic to avoid suspicion.

Working directory: /Users/viktorashi/nerdin/pro-fm
Integrity mode: development

## Context

This is a Go application deployed on Fly.io that:
- Polls the ProFM radio API every 2 seconds for the currently playing song
- When a target artist's song is detected during active campaign hours (Mon-Fri 07:00-20:00), it sends a WhatsApp voice note to a contest phone number
- Uses whatsmeow library for WhatsApp, SQLite for state, templ for HTML templates, Echo v5 for HTTP, and SSE for live dashboard updates
- Campaign dates are hardcoded in main.go
- Voice notes are `.ogg` Opus files stored in `/data/audios/`, moved to `/data/audios/used/` after sending

The live radio stream is available at: `http://edge76.rcs-rds.ro:84/profm/profm.mp3`

**Critical invariant**: The voicenote state is 100% global. Never send the same voicenote twice. Never spam voicenotes without other artists' songs playing in between. A new voicenote may only be sent after at least one non-campaign-artist song has played since the last send.

**Known metadata behavior**: The ProFM API has a flickering bug where when a song changes from A to B, the metadata alternates: A playing → B playing → A playing → B playing... for several cycles before settling on B. The existing `checkSong` code already handles this by tracking `currentSong` and only acting on changes. Any new detection logic must account for this and NOT treat the flicker as multiple separate detections.

**Branch rules**: All work must be based off `dev` branch. Never merge or push to `main`. Create a separate PR branch for each logical feature. Use stacked PRs where features depend on each other. Run `just setup-dev` before doing anything in a new worktree.

**Testing rules**: Do NOT run E2E tests autonomously (they require a physical phone). You MAY write new E2E tests. Run `go test ./pkg/...` for unit tests and `just test` as verification.

## Requirements

### R1. Fix WhatsApp Voice Note Sending & Minimal ffmpeg (Critical Bug Fix)

Voice notes are currently failing with Error 463 (`NackCallerReachoutTimelocked`) and ffmpeg is missing from the Docker image.

- Add the ffmpeg source as a **git submodule** pinned to a specific stable release commit hash (reproducible builds — must work even if upstream releases new versions).
- Compile ffmpeg with minimal modules: `--disable-everything --enable-protocol=file --enable-demuxer=ogg --enable-muxer=ogg --enable-parser=opus`, plus any additional modules needed for **real waveform extraction** from .ogg files.
- The ffmpeg build must be DRY — one script/Makefile used for local dev, unit tests, AND the Docker container build.
- Fix the `creation_time` metadata injection to use the **exact timestamp** when the voice message is sent.
- Extract **real waveform data** from each audio file and populate the `Waveform` field in `waE2E.AudioMessage` so voice notes display proper audio waveforms on the receiver's phone (not flat lines — see the bug screenshot where auto-sent notes show no waveform but manually sent ones do).
- Update the Dockerfile to compile and include the minimal ffmpeg binary.

### R2. Multiple WhatsApp Connections

Support sending voice notes from multiple phone numbers to the same target number:

- Each WhatsApp session uses its own isolated `.sqlite` database file (matching whatsmeow default behavior).
- Refactor `InitWhatsApp` and `SendVoiceNote` to manage an array of `whatsmeow.Client` instances.
- Each connection shows its own QR pairing state on the dashboard.
- **Per-phone audio directories**: Each phone number gets its own subdirectory for voice notes, keyed by phone number (escaping the `+40` prefix as needed). For example: `/data/audios/40734788254/`, `/data/audios/40770661491/used/`, etc.
  - **Exception**: The original canonical phone number `+40 734 788 254` continues using the root `data/audios/` directory as before, for backward compatibility.
  - Audio file lookups must **NOT** be recursive — each phone's directory must only look at its own level to avoid pulling audios from other phones' directories.
- The global voicenote state invariant still applies across ALL connections — no duplicate sends, no spamming without intervening non-campaign songs.

### R3. Circular Audio Buffer ("Dashcam") — *stacked on R1*

Implement a rolling 3-minute audio buffer that continuously records from the ProFM live stream (`http://edge76.rcs-rds.ro:84/profm/profm.mp3`):

- The buffer extends and saves the full chunk (3 min before + 4 min after) **ONLY IF** an audio signature was NOT recognized since the last campaign song detection. This means the save only triggers when we detected a campaign song via API metadata (meaning we missed the intro). If we already detected the intro via audio fingerprinting, we do NOT save a new dashcam chunk for the same campaign turn — this prevents double-triggering.
- Saved recordings are available for later inspection via API endpoints.
- This feature depends on R1's ffmpeg being available.

### R4. Audio Signature Fingerprinting & Detection

Implement a state machine for gathering and using audio intro signatures:

- **Gathering mode**: Controlled by a runtime `StateManager` flag `GatheringSignatures` (defaults to `true` on startup). A dashboard button toggles this flag on/off.
- When gathering is enabled, the system continuously records/saves audio samples from the dashcam buffer. Newly recorded audio samples are **only** saved persistently if they **don't match** any canonical signature already on file (to avoid duplicates).
- **No auto-assignment**: Recorded samples are NEVER automatically marked as canonical intro signatures. They require manual review.
- **Dashboard review section**: Unreviewed audio samples appear in a dedicated section of the dashboard. Users receive alerts (via the existing alerter system) when new unreviewed samples are available.
- **Editing endpoints**: REST API endpoints that allow:
  - Listing all recorded (unreviewed) dashcam audio chunks
  - Serving/playing individual audio chunks
  - Cropping a section of a recording and marking it as "THIS IS THE INTRO SIGNATURE"
  - Cropped versions are stored as **additional copies** alongside the originals — originals are never deleted or overwritten
  - These cropped copies become canonical waveforms used for matching
- **Detection phase**: When canonical signatures exist, compare the live audio stream against them to detect a contest intro BEFORE the API metadata updates. When a signature match triggers a voicenote send, this must set a flag so that the subsequent API metadata detection for the same song does NOT trigger a second send. Be aware of the metadata flickering behavior (A→B→A→B) described in Context.
- **Logging**: Clear logs distinguishing "detected via audio signature" vs "detected via API metadata".

### R5. Daily RNG Selection

Implement randomized per-day selection logic to avoid being suspiciously accurate:

- At startup, pre-populate a selection map for ALL campaign days across ALL campaigns. For each day, randomly decide which song detections to respond to (not all of them). All dates must respect the campaign rules (Mon-Fri, 07:00-20:00, within campaign date ranges).
- Persist these selections in the database so they survive restarts. On startup, only generate RNG for days that are missing from the persisted state — never overwrite existing selections.
- Expose the daily selection schedule via an API endpoint so the dashboard can display it.
- **Allow user editing**: The schedule must be editable by the user through API endpoints. All edits remain bounded by the campaign date rules.
- The voicenote global state invariant still applies.

### R6. Dashboard: Per-Phone Audio Upload — *stacked on R4*

Add a file upload endpoint and form to the existing web dashboard:
- Allows uploading new `.ogg` voice note files
- Uploads are **per phone number** — each phone's audios go into their respective directory (matching R2's per-phone directory structure)
- The upload form lets the user select which phone number the audio is for

## Acceptance Criteria

### Voice Note Sending (R1)
- [ ] `ffmpeg` source is added as a git submodule pinned to a stable release commit
- [ ] A single build script compiles minimal ffmpeg for local dev, tests, and Docker
- [ ] The Dockerfile produces a working image with ffmpeg included
- [ ] `creation_time` in remuxed files equals the actual send timestamp
- [ ] Voice notes include real `Waveform` byte data in the AudioMessage proto
- [ ] Unit tests verify waveform extraction from `.ogg` files
- [ ] `go test ./pkg/...` passes

### Multiple Connections (R2)
- [ ] Each connection uses its own `.sqlite` file
- [ ] Per-phone audio directories exist, with the canonical number using root `data/audios/`
- [ ] Audio lookups are non-recursive (each phone only sees its own directory)
- [ ] Audio pool state is globally consistent — no duplicate sends across any connection
- [ ] Each connection has independent QR pairing on the dashboard

### Audio Buffer (R3)
- [ ] The circular buffer continuously records from the MP3 stream
- [ ] Buffer saves only trigger when the intro was NOT already detected via fingerprinting
- [ ] Saved chunks are accessible via API endpoints
- [ ] Does not double-trigger sends (one from signature, one from metadata)

### Audio Fingerprinting (R4)
- [ ] `GatheringSignatures` flag defaults to `true`, toggleable via dashboard
- [ ] New samples only saved if they don't match existing canonical signatures
- [ ] REST endpoints exist for listing, serving, cropping, and marking recordings
- [ ] Cropping creates copies, never modifies originals
- [ ] When canonical signatures exist, live audio matching triggers before metadata
- [ ] Signature-based detection prevents duplicate metadata-based triggering
- [ ] Unit tests exist with version-controlled audio fixtures (in `pkg/poller/testdata/` or similar, NOT in `data/`) with explicit "expected match" and "expected not match" pairs

### RNG Selection (R5)
- [ ] All campaign days pre-populated with RNG selections at startup
- [ ] Selections persisted in database, surviving restarts
- [ ] Missing days backfilled without overwriting existing
- [ ] API endpoints for viewing and editing the schedule
- [ ] Edits bounded by campaign date rules

### Dashboard Upload (R6)
- [ ] Upload form allows selecting target phone number
- [ ] Files uploaded to correct per-phone directory
- [ ] Existing dashboard functionality unaffected

### General
- [ ] All branches based off `dev` — never merged to `main`
- [ ] `go build` succeeds
- [ ] Existing unit tests continue to pass
- [ ] No E2E tests run autonomously
- [ ] Each feature on its own PR branch with stacking where needed (R3→R1, R6→R4)
