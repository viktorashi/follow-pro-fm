# Implementation Plan: WhatsApp Voice Note Sending & Minimal ffmpeg

We need to implement the requirements for Milestone 1. Please execute the following tasks on the checked-out branch `feature/m1-whatsapp-ffmpeg`. Commit atomically for each logical chunk.

## Task 1: Pinned Git Submodule for ffmpeg
- We have checked out the tag `n6.1.1` in the submodule directory `third_party/ffmpeg`.
- Register the submodule in `.gitmodules` if not already staged. Commit the submodule entry pinned to commit `e38092ef9395d7049f871ef4d5411eb410e283e0` (which is tag `n6.1.1`).

## Task 2: DRY Compile Script & Makefile wrapper
- Create `scripts/build_ffmpeg.sh` to compile a minimal ffmpeg/ffprobe binary.
- It must run `./configure` inside `third_party/ffmpeg` with the following minimal features:
  ```bash
  ./configure \
    --disable-everything \
    --disable-doc \
    --disable-ffplay \
    --enable-ffmpeg \
    --enable-ffprobe \
    --enable-protocol=file \
    --enable-protocol=pipe \
    --enable-demuxer=ogg \
    --enable-muxer=ogg \
    --enable-muxer=s16le \
    --enable-parser=opus \
    --enable-decoder=opus \
    --enable-filter=aresample \
    --disable-network \
    --disable-autodetect \
    --disable-hwaccels \
    --disable-x86asm \
    --disable-shared \
    --enable-static
  ```
- Copy the output binaries `ffmpeg` and `ffprobe` to a root-level `./bin` folder.
- Ensure the script is DRY, handles parallel make (`-j`), and skips rebuild if binaries already exist (unless run with `--force`).
- Create a `Makefile` at the root wrapper to easily invoke the build script.

## Task 3: Update `justfile` and `.github/workflows/ci.yml`
- In `justfile`:
  - Add a target `build-ffmpeg` that runs `scripts/build_ffmpeg.sh`.
  - Update `test` (and other test commands) to depend on `build-ffmpeg` and prefix `PATH` with `bin/` so that the compiled minimal binaries are used instead of system ones.
- In `.github/workflows/ci.yml`:
  - Add submodule checkouts (`submodules: recursive`).
  - Run the build script before testing.
  - Prepend `bin/` to the PATH for the test steps.

## Task 4: Dockerfile Integration
- Update `Dockerfile` to compile the minimal ffmpeg during image build and place it in `/usr/local/bin` in the final Alpine runtime stage.
- Leverage Docker layer caching: copy the `third_party/ffmpeg` submodule and the build script first and compile them before copying the rest of the application source code.

## Task 5: Fix creation_time metadata timestamp in WhatsApp sending
- Currently in `pkg/poller/whatsapp.go`, the metadata injection happens *before* the estimated recording sleep. This causes a ~15-35s time discrepancy.
- Move the duration extraction, sleep, and chat presence commands *before* the ffmpeg metadata injection and upload.
- Specifically, the workflow should be:
  1. Extract duration to calculate `estimatedSeconds`.
  2. Send "recording audio" presence and sleep for `estimatedSeconds`.
  3. Send "paused" presence.
  4. Generate the exact send timestamp (`time.Now().UTC().Format(time.RFC3339)`).
  5. Run ffmpeg to inject this timestamp into the OGG metadata creation_time.
  6. Read the newly modified file and upload it.
  7. Populate `waE2E.AudioMessage` fields (including `MediaKeyTimestamp` set to `time.Now().Unix()`).
  8. Send the message.
- Update `patch_whatsapp.sh` to match this logic as well.

## Task 6: Extract real waveform data for waE2E.AudioMessage
- In `pkg/poller/whatsapp.go`, implement a function `ExtractWaveform(audioPath string) ([]byte, error)`:
  - Run `ffmpeg -i <audioPath> -f s16le -ac 1 -ar 8000 -` to decode to mono 16-bit PCM and stream it.
  - Divide the samples into 64 equal-sized buckets.
  - Find the absolute peak amplitude in each bucket.
  - Scale/normalize each bucket's peak to the range `[0, 255]` relative to the global max peak.
  - If the global max peak is 0 or error, return a 64-byte zero slice.
- Populate the `Waveform` field of the `waE2E.AudioMessage` with this 64-byte slice.

## Task 7: Unit Tests for Waveform Extraction
- Write unit tests in `pkg/poller/media_test.go` or a new test file that run `ExtractWaveform` on the test audio files in `data/audios/` and verify:
  - The waveform is non-empty.
  - Its length is exactly 64 bytes.
  - The peak value in the waveform is exactly 255.
  - The logic handles error conditions gracefully.

## MANDATORY INTEGRITY WARNING
> DO NOT CHEAT. All implementations must be genuine. DO NOT
> hardcode test results, create dummy/facade implementations, or
> circumvent the intended task. A Forensic Auditor will independently
> verify your work. Integrity violations WILL be detected and your
> work WILL be rejected.
