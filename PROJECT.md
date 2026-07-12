# Project: ProFM Go Application Enhancements

## Architecture
- Modules/packages:
  - `cmd/pro-fm-poller`: Binary entrypoint.
  - `pkg/poller`: Business logic, polling loop, SQLite DB manager, State manager, SSE logger and broadcaster, HTTP Server (Echo v5) with templ templates, WhatsApp messenger (whatsmeow).
- Data Flow:
  - Live streams (live audio + API metadata) -> Poller state machine -> StateMgr / DbMgr -> WhatsApp clients.
  - User Dashboard -> Echo Server APIs -> StateMgr / DbMgr -> WhatsApp clients.

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|---|---|---|---|
| 0 | E2E Test Suite | Opaque-box harness for production-relevant flows. | None | IN_PROGRESS: core smoke coverage exists; scenario coverage continues with feature changes. |
| 1 | WhatsApp & ffmpeg | Minimal pinned FFmpeg build, Docker integration, voice-note creation time and waveform extraction. | None | COMPLETE |
| 2 | Multi-WhatsApp | Multi-session clients, QR pairing, runtime-connected phone list, per-phone directories and global sent-audio state. | M1 | COMPLETE |
| 3 | Buffer & Fingerprinting | Dashcam buffer, signature gathering/review/cropping, matching and fixture-backed tests. | M2, M1 | COMPLETE |
| 4 | RNG Selection | Persisted daily schedule, dashboard editing and all-campaign-day fill through the general schedule endpoint. | M3 | COMPLETE |
| 5 | Dashboard Upload | Authenticated upload to the selected connected phone's audio pool, including canonical/root audio. | M3 | COMPLETE |

## Interface Contracts
### whatsmeow.Client ↔ poller.Poller
- Poller uses WhatsApp connection manager to send voice notes.
- Send voice note needs client index/phone context.
