# Codex Audit Status — June 26, 2026

This note records the currently verified state of the `dev` branch after the sequential Codex rework that followed the earlier antigravity run.

## Verified Commit Stack

Ordered from oldest relevant verified slice to newest:

1. `1de0380` `Fix minimal ffmpeg build and real waveform extraction`
2. `1aeff59` `Add dashboard-driven multi-phone runtime controls`
3. `26e7d9c` `Enforce global voice note reuse and resend gating`
4. `e7c4070` `Update E2E harness for multi-sender poller changes`
5. `178a812` `Enforce campaign schedule bounds and dashcam timing`
6. `4ae0f9b` `Add audio-aware intro fingerprint matching`
7. `347a9a1` `Fix reproducible ffmpeg Docker builds`
8. `a854334` `Add live mock smoke test runner`

## Requirement Coverage Snapshot

### R1. WhatsApp voice note sending and minimal ffmpeg

- `third_party/ffmpeg` is a git submodule pinned to a specific commit.
- `scripts/build_ffmpeg.sh` is the single ffmpeg build entrypoint used for local binaries and Docker.
- Docker image build was verified with `docker build -t pro-fm-poller:test .`.
- Runtime image now contains Linux `ffmpeg`, not a leaked Mach-O host binary.
- Image-local `ffmpeg` exposes `mp3float`, `opus`, and the required `mp3` / `ogg` demuxers.
- `creation_time` remuxing and waveform extraction are covered by Go tests in `pkg/poller`.

### R2. Multiple WhatsApp connections

- Dynamic sender phone add flow exists on the dashboard.
- Each phone gets its own sqlite database file and is assigned to a person.
- Audio lookup remains non-recursive per person pool.
- Send-state reuse protection remains global across connections.

### R3. Circular audio buffer

- Rolling stream buffer is wired into the poller.
- Metadata-triggered capture now preserves approximately 3 minutes of pre-roll and extends 4 more minutes.
- Dashboard/API paths exist for inspecting recorded chunks.
- Metadata dedupe is tied to fingerprint detection to avoid double-triggering.

### R4. Audio fingerprinting

- Gathering flag defaults to `true` and can be toggled from the dashboard.
- Unreviewed chunks are listed, served, and crop-promoted to canonical signatures.
- Cropping preserves originals.
- Matching is now audio-aware rather than raw byte containment.
- Canonical fixtures are real committed audio files under `pkg/poller/testdata/fingerprint/`.

### R5. RNG selection

- Startup backfills all missing campaign weekdays into the persisted schedule table.
- Schedule persistence and edit API are present.
- Schedule writes are bounded to campaign weekdays and valid match indices.

### R6. Dashboard per-person upload

- Dashboard upload form targets a chosen person.
- Upload endpoint writes `.ogg` files into the selected person's active pool.

## Verified Commands

These commands were run successfully against the current tree during the Codex rework:

```sh
go test -count=1 ./pkg/poller
go build ./cmd/pro-fm-poller
just test
just smoke-live-mock
docker build -t pro-fm-poller:test .
```

## Runtime Smoke Evidence

Two smoke levels were verified:

1. Fully local mock runtime:
   - dead local PRO FM endpoints
   - `MOCK_WHATSAPP=true`
   - login, dashboard, schedule API, and upload API exercised successfully

2. Live PRO FM mock runtime:
   - real PRO FM API
   - real PRO FM stream
   - `MOCK_WHATSAPP=true`
   - poller observed real now-playing changes and dashboard reflected live song state

## Remaining Unproven Areas

These are still not fully proven by local or mock evidence alone:

1. Real WhatsApp delivery on actual linked devices
2. Real live contest intro fingerprint triggering during an actual campaign turn
3. Remote PR stack state, because no push/PR creation has been done from this repo state

## Local Branch Notes

- `dev` contains the currently verified stack.
- Older local `feature/*` branches appear stale relative to `dev`.
- Fresh local stacked refs were created during the Codex pass under:
  - `codex/m1-whatsapp-ffmpeg`
  - `codex/m2-multiple-connections`
  - `codex/m3-rng-and-dashcam`
  - `codex/m4-audio-fingerprinting`
  - `codex/r1-docker-repro`
