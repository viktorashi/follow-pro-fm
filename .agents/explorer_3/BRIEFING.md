# BRIEFING — 2026-06-21T17:51:10+03:00

## Mission
Investigate WhatsApp's audio message Waveform format, OGG/Opus amplitude extraction via ffmpeg/ffprobe, and whatsmeow scaling/formatting.

## 🔒 My Identity
- Archetype: explorer
- Roles: Read-only investigator, analyzer
- Working directory: /Users/viktorashi/nerdin/pro-fm/.agents/explorer_3
- Original parent: 277d0024-d894-41ff-8acf-460beee764fa
- Milestone: WhatsApp Audio Waveform Data Investigation

## 🔒 Key Constraints
- Read-only investigation — do NOT implement
- Code-only network mode (no external web access, no external HTTP clients)

## Current Parent
- Conversation ID: 277d0024-d894-41ff-8acf-460beee764fa
- Updated: 2026-06-21T17:51:10+03:00

## Investigation State
- **Explored paths**:
  - `pkg/poller/whatsapp.go` (checked where AudioMessage is constructed)
  - `data/audios/` (examined existing sample OGG files)
  - `/Users/viktorashi/nerdin/pro-fm/.agents/explorer_3/extract.go` (created and tested single-file extraction script)
  - `/Users/viktorashi/nerdin/pro-fm/.agents/explorer_3/extract_multi.go` (created and tested multi-file extraction script)
- **Key findings**:
  - `waE2E.AudioMessage.Waveform` is structured as a `[]byte` slice. Each byte represents the height of a waveform bar (amplitude value) in the WhatsApp chat bubble.
  - Decoding OGG/Opus to raw `s16le` mono 8000Hz PCM using `ffmpeg` is highly robust, fast, and avoids text-parsing dependencies.
  - Normalizing bucket peaks relative to the global maximum peak value in the file and scaling to `0-255` produces a clean and high-fidelity waveform.
- **Unexplored areas**: None.

## Key Decisions Made
- Chose raw PCM decoding via `ffmpeg -f s16le` over text-based filter parsing (like `astats` or `volumedetect`).
- Chose peak amplitude extraction over RMS to better capture rapid voice changes/bursts.
- Standardized on a 64-bucket fixed length normalized to the file's peak amplitude and scaled to 0-255.

## Artifact Index
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_3/extract.go — Go code for single file waveform extraction
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_3/extract_multi.go — Script to run and verify waveform extraction on all sample files
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_3/handoff.md — Final investigation findings report

