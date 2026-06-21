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
| 0 | E2E Test Suite | Create opaque-box E2E test infra and cases (Tiers 1-4). | None | IN_PROGRESS (Conv: 22bce552-2cc6-4f98-97b2-2227559dfdb7) |
| 1 | WhatsApp & ffmpeg | R1: git submodule ffmpeg, minimal compile script, dockerfile integration, send-timestamp creation_time, waveform extraction, unit tests. | None | IN_PROGRESS (Conv: 277d0024-d894-41ff-8acf-460beee764fa) |
| 2 | Multi-WhatsApp | R2: multi-session client, SQLite db per connection, QR pairing, per-phone directories, non-recursive audio pools. | M1 | PLANNED |
| 3 | Buffer & Fingerprinting | R3 & R4: Dashcam buffer stream recorder, signature gathering/dashboard state, cropping REST endpoints, matching signature detector, duplicate de-duplication, version-controlled unit tests. | M2, M1 | PLANNED |
| 4 | RNG Selection | R5: daily RNG selection, db persistence, startup backfill, schedule dashboard display/edit endpoints, campaign rules boundary. | M3 | PLANNED |
| 5 | Dashboard Upload | R6: REST file upload form, phone number selector, upload to per-phone audio directory, no side-effects on dashboard. | M3 | PLANNED |

## Interface Contracts
### whatsmeow.Client ↔ poller.Poller
- Poller uses WhatsApp connection manager to send voice notes.
- Send voice note needs client index/phone context.
