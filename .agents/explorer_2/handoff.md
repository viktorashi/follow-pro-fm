# Handoff Report: Minimal FFmpeg & Git Submodule Integration

This report documents how to add, configure, compile, and integrate a minimal FFmpeg/FFprobe build into the ProFM radio monitor application.

---

## 1. Observation

During my investigation of the repository, I observed the following:

- **Submodule Status**: There is currently no git submodule or `.gitmodules` file configured in the repository (verified using `git submodule status`).
- **Binary Usage in Application**: `pkg/poller/whatsapp.go` runs an external `ffmpeg` process to copy codec streams and update creation time metadata:
  ```go
  cmd := exec.Command("ffmpeg", "-y", "-i", audioPath, "-c", "copy", "-metadata", "creation_time="+now, tmpPath)
  ```
- **Binary Usage in Tests**: `pkg/poller/media_test.go` runs an external `ffprobe` process to check durations of Ogg files:
  ```go
  cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
  ```
- **Dockerfile structure**: The root `Dockerfile` uses a two-stage Alpine-based build.
  - Stage 1 (builder) uses `golang:1.26-alpine` and only installs `gcc` and `musl-dev`.
  - Stage 2 (runtime) uses `alpine:3.20` and only installs `ca-certificates` and `tzdata`. It has no `ffmpeg` or `ffprobe` binaries.
- **Local environment**: The host has `ffmpeg` version 8.1.1 installed locally, which supports standard protocols, decoders, and filters including the native `opus` decoder, `ogg` demuxer/muxer, `s16le` muxer, and `aresample` filter.

---

## 2. Logic Chain

To satisfy all requirements (git submodule, minimal build, DRY compilation script, Docker integration, and test compatibility):

1. **Git Submodule**: Since the build needs to be 100% reproducible and isolated from upstream releases, we should add the upstream FFmpeg repository as a git submodule in the `third_party/ffmpeg` directory. We pin it to a stable release tag's commit hash (e.g., FFmpeg `n6.1.1` tag commit `3236e7a270bf1390610332851cf574eb6a5f78b3`).
2. **Minimal Configuration Flags**:
   - To inject metadata during remuxing (`-c copy`), we need the `ogg` demuxer and muxer, the `opus` parser, and the `file` protocol.
   - To extract audio waveforms, we must decode the Opus audio packets to PCM. Using the native `opus` decoder and `s16le` (signed 16-bit little-endian) muxer avoids external dependencies like `libopus` and allows Go to read raw binary PCM streams directly via stdout pipes.
   - Resampling or downmixing to mono (e.g., `-ac 1 -ar 8000`) is handled by the `aresample` filter.
   - We must also compile `ffprobe` because the test suite `pkg/poller/media_test.go` calls `ffprobe` directly.
   - Passing `--disable-x86asm` removes the compilation dependency on an assembler (`yasm` or `nasm`), ensuring the compilation works out-of-the-box on bare Alpine/macOS environments.
   - Passing `--disable-network` and `--disable-autodetect` prevents the builder from attempting to link against host system libraries, making the build highly deterministic and lightweight.
3. **DRY Build Script**: A shell script `scripts/build_ffmpeg.sh` can be executed locally, during tests, and inside Docker. Prepending the output directory `./bin` to `PATH` makes the binaries available to the Go application and tests.
4. **Dockerfile Caching**: To prevent compiling FFmpeg from scratch on every Go source code change, the Dockerfile should copy the `third_party/ffmpeg` submodule and run the compilation script *before* copying the rest of the application source code.
5. **CI/CD Integration**: The `.github/workflows/ci.yml` needs to clone submodules and run the compilation script before testing.

---

## 3. Caveats

- **Submodule checkout**: Developers cloning the repository for the first time must run `git submodule update --init --recursive` to fetch the FFmpeg source. If they do not, the build script will automatically attempt to run it, but this requires `git` to be installed on the host.
- **Docker context**: Since `.git` is ignored in `.dockerignore`, the submodule files must be fully checked out on the host prior to running `docker build`. The build context will copy these files as a plain directory.
- **macOS static linking**: On macOS, fully static linking of system frameworks is not supported by Apple. However, FFmpeg's `--enable-static --disable-shared` flags compile FFmpeg's internal libraries statically and link dynamically only with the basic macOS system libraries, which is perfectly safe and standard.

---

## 4. Conclusion

The proposed implementation integrates a pinned FFmpeg submodule, builds it using a DRY script, and incorporates it into both the containerized runtime and test suites.

All proposed files and patches have been written to the explorer directory `/Users/viktorashi/nerdin/pro-fm/.agents/explorer_2/`:
- `proposed_build_ffmpeg.sh`: The core compilation script.
- `proposed_Makefile`: A clean Makefile wrapping the script.
- `proposed_Dockerfile`: The updated Dockerfile with compiler caching.
- `proposed_justfile.patch`: Integration of the build step into local tests.
- `proposed_ci.yml.patch`: Integration of the submodule and build step into CI.

### Summary of Configure Flags
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

---

## 5. Verification Method

Once the implementer applies the changes, the integration can be verified as follows:

1. **Submodule Verification**:
   Verify that `third_party/ffmpeg` is pinned to commit `3236e7a270bf1390610332851cf574eb6a5f78b3`:
   ```bash
   git submodule status third_party/ffmpeg
   ```
2. **Local Build Verification**:
   Run the compilation:
   ```bash
   just build-ffmpeg
   ```
   Confirm that `bin/ffmpeg` and `bin/ffprobe` exist, are small (~2 MB), and work:
   ```bash
   ./bin/ffmpeg -version
   ./bin/ffprobe -version
   ```
3. **Test Verification**:
   Run tests using the compiled binaries:
   ```bash
   just test
   ```
   Verify that the test suite passes, indicating `ffprobe` successfully measured audio duration.
4. **Docker Image Verification**:
   Build the Docker container and inspect the image:
   ```bash
   docker build -t pro-fm-test .
   docker run --rm -it pro-fm-test which ffmpeg ffprobe
   docker run --rm -it pro-fm-test ffmpeg -version
   ```
   This confirms that the minimal binaries are present and functional in the final runtime container.
