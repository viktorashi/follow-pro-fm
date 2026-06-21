# BRIEFING — 2026-06-21T14:53:02Z

## Mission
Investigate and document the integration of a minimal FFmpeg binary compiled from a pinned git submodule.

## 🔒 My Identity
- Archetype: Teamwork explorer
- Roles: Read-only investigator, analyzer
- Working directory: /Users/viktorashi/nerdin/pro-fm/.agents/explorer_2
- Original parent: 277d0024-d894-41ff-8acf-460beee764fa
- Milestone: FFmpeg Minimal Build Integration

## 🔒 Key Constraints
- Read-only investigation — do NOT implement (do not run commands that make git submodules or build/install/commit unless for local verification that is non-intrusive)
- CODE_ONLY network mode: no external HTTP/wget/curl requests.

## Current Parent
- Conversation ID: 277d0024-d894-41ff-8acf-460beee764fa
- Updated: 2026-06-21T14:53:02Z

## Investigation State
- **Explored paths**: `Dockerfile`, `justfile`, `.github/workflows/ci.yml`, `pkg/poller/media_test.go`, local `ffmpeg` (8.1.1) features.
- **Key findings**:
  - Pinned git submodule can be added under `third_party/ffmpeg`.
  - A minimal compilation with `--disable-everything` requires `ogg` (demuxer/muxer), `opus` (decoder/parser), `s16le` (muxer), `file`/`pipe` (protocols), and `aresample` (filter) to support both remuxing (metadata injection) and decoding (waveform extraction).
  - Both `ffmpeg` and `ffprobe` should be built because tests explicitly call `ffprobe` to verify the accuracy of the Go-based audio duration parser.
  - Prepending `bin/` to `PATH` allows local, CI, and test execution to seamlessly use the minimal compiled binaries.
  - Caching inside Docker is achieved by compiling the submodule before copying the rest of the Go code.
- **Unexplored areas**: None.

## Key Decisions Made
- Build both `ffmpeg` and `ffprobe` to satisfy test suite requirements.
- Add `--disable-x86asm` to eliminate compilation-time assembler requirements (`yasm`/`nasm`).
- Use Docker layer caching for `third_party/ffmpeg` and the build script.

## Artifact Index
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_2/handoff.md — Handoff report containing findings and instructions.
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_2/proposed_build_ffmpeg.sh — Portable build script for compilation.
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_2/proposed_Makefile — Wrapper Makefile.
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_2/proposed_Dockerfile — Updated multi-stage Dockerfile.
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_2/proposed_ci.yml.patch — Git patch for GitHub Actions CI.
- /Users/viktorashi/nerdin/pro-fm/.agents/explorer_2/proposed_justfile.patch — Git patch for the local justfile.
