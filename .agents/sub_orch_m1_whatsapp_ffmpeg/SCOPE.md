# Scope: WhatsApp & ffmpeg

## Architecture
- **ffmpeg integration**: Pinned git submodule for ffmpeg. Script/Makefile to build minimal ffmpeg for local development, testing, and Docker environment.
- **Dockerfile integration**: Include minimal compiled ffmpeg binary inside the final application image.
- **WhatsApp Voice Note sending**:
  - `pkg/poller` / whatsmeow integration: Update `creation_time` injection during OGG/Opus parsing or wav/ogg conversion.
  - Waveform extraction: Extract Opus audio envelope/amplitude data using ffmpeg/ffprobe, scale/format it, and populate the `Waveform` byte slice in `waE2E.AudioMessage`.
  - Add unit tests verifying waveform extraction functionality.

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|---|---|---|---|
| 1.1 | git submodule | Add stable ffmpeg git submodule and write build script | None | PLANNED |
| 1.2 | Dockerfile | Update Dockerfile to compile and include minimal ffmpeg | 1.1 | PLANNED |
| 1.3 | Metadata & Waveform | Inject creation_time timestamp & extract real waveform data for `waE2E.AudioMessage` | 1.1 | PLANNED |
| 1.4 | Unit Tests | Add unit tests verifying waveform extraction | 1.3 | PLANNED |

## Interface Contracts
### whatsmeow/waE2E ↔ poller.WhatsAppClient
- Audio messages sent via whatsmeow should have `Waveform` field populated as a byte slice matching WhatsApp's expectation (normally 1-byte values representing audio levels, 1 to 64 bytes in length, scaled between 0 and 255 or 0 and 127).
- `creation_time` metadata must be set in the Opus header/ogg stream metadata or during upload to WhatsApp to match exact send timestamp.
