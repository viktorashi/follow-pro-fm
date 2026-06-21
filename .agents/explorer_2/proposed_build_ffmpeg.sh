#!/bin/sh
# scripts/build_ffmpeg.sh
# Compiles a minimal ffmpeg/ffprobe binary from the git submodule.
# This script is DRY and works locally, in tests, and inside the Docker builder container.

set -eu

# Setup paths
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# If running directly or from within scripts/
if [ "$(basename "$SCRIPT_DIR")" = "scripts" ]; then
  PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
else
  PROJECT_ROOT="$SCRIPT_DIR"
fi

SUBMODULE_DIR="$PROJECT_ROOT/third_party/ffmpeg"
OUTPUT_DIR="$PROJECT_ROOT/bin"
FFMPEG_BIN="$OUTPUT_DIR/ffmpeg"
FFPROBE_BIN="$OUTPUT_DIR/ffprobe"

FORCE_REBUILD=false
if [ "${1:-}" = "--force" ]; then
  FORCE_REBUILD=true
fi

# 1. Skip if already compiled and we are not forcing a rebuild
if [ -f "$FFMPEG_BIN" ] && [ -f "$FFPROBE_BIN" ] && [ "$FORCE_REBUILD" = false ]; then
  echo "✅ Minimal ffmpeg and ffprobe already exist in $OUTPUT_DIR. Skipping build."
  exit 0
fi

echo "🚀 Starting minimal FFmpeg / FFprobe build..."

# 2. Check if submodule is initialized (relevant for local development)
if [ ! -f "$SUBMODULE_DIR/configure" ]; then
  echo "⚠️ Submodule not initialized. Initializing third_party/ffmpeg..."
  git submodule update --init --recursive "$SUBMODULE_DIR"
fi

# 3. Enter submodule directory
cd "$SUBMODULE_DIR"

# 4. Clean previous build if forcing
if [ "$FORCE_REBUILD" = true ]; then
  echo "🧹 Cleaning previous build artifacts..."
  make distclean || true
fi

# 5. Configure FFmpeg with minimal features
# - --disable-everything: turn off all components by default
# - --enable-ffmpeg, --enable-ffprobe: build both programs
# - --enable-protocol=file,pipe: file & pipe protocols for stdin/stdout and local files
# - --enable-demuxer=ogg: demux OGG files containing Opus
# - --enable-muxer=ogg,s16le: OGG muxer for metadata-injected audio, s16le for PCM
# - --enable-parser=opus: parse Opus packets
# - --enable-decoder=opus: decode Opus packets to PCM for waveform extraction
# - --enable-filter=aresample: resample audio (e.g. channel downmix, sample rate change)
# - --disable-x86asm: disable yasm/nasm dependency to ensure clean compilation on bare Alpine/macOS
# - --disable-network, --disable-autodetect, --disable-hwaccels: ensure security, no dependencies, speed
echo "🔧 Configuring minimal FFmpeg..."
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

# 6. Determine core count for parallel make
if [ "$(uname)" = "Darwin" ]; then
  CORES=$(sysctl -n hw.ncpu)
else
  CORES=$(nproc 2>/dev/null || echo 1)
fi

echo "🔨 Compiling FFmpeg and FFprobe on $CORES cores..."
make -j"$CORES"

# 7. Copy binaries to target directory
mkdir -p "$OUTPUT_DIR"
cp ffmpeg "$FFMPEG_BIN"
cp ffprobe "$FFPROBE_BIN"

echo "✅ Successfully built minimal FFmpeg and FFprobe at $OUTPUT_DIR"
ls -lh "$FFMPEG_BIN" "$FFPROBE_BIN"
