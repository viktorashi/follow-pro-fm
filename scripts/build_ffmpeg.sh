#!/usr/bin/env bash
set -euo pipefail

# Get absolute path to the root directory
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${ROOT_DIR}/bin"
FFMPEG_SUBMODULE="${ROOT_DIR}/third_party/ffmpeg"

FORCE=false
for arg in "$@"; do
  if [ "$arg" = "--force" ] || [ "$arg" = "-f" ]; then
    FORCE=true
  fi
done

if [ "$FORCE" = false ] && [ -f "${BIN_DIR}/ffmpeg" ] && [ -f "${BIN_DIR}/ffprobe" ]; then
  echo "ffmpeg and ffprobe binaries already exist in ${BIN_DIR}. Skipping build."
  echo "Use --force or -f to force rebuilding."
  exit 0
fi

echo "Building minimal ffmpeg and ffprobe..."

# Detect core count
if command -v nproc >/dev/null 2>&1; then
  CORES=$(nproc)
elif command -v sysctl >/dev/null 2>&1; then
  CORES=$(sysctl -n hw.ncpu)
else
  CORES=2
fi

echo "Using ${CORES} CPU cores for compilation."

# Move to ffmpeg submodule directory
cd "${FFMPEG_SUBMODULE}"

# Clean if forcing or if prior build artifacts exist in the source tree.
if [ "$FORCE" = true ] || [ -f "ffbuild/config.mak" ] || [ -f "config.mak" ] || [ -f "fftools/objpool.o" ]; then
  if [ "$FORCE" = true ]; then
    echo "Forced build. Cleaning submodule directory..."
  else
    echo "Detected existing ffmpeg build artifacts. Cleaning submodule directory..."
  fi
  make distclean || true
fi

# Run configure
echo "Configuring ffmpeg..."
./configure \
  --disable-everything \
  --disable-doc \
  --disable-debug \
  --disable-ffplay \
  --disable-avdevice \
  --disable-postproc \
  --disable-swscale \
  --enable-ffmpeg \
  --enable-ffprobe \
  --enable-protocol=file \
  --enable-protocol=pipe \
  --enable-demuxer=mp3 \
  --enable-demuxer=ogg \
  --enable-demuxer=pcm_s16le \
  --enable-muxer=ogg \
  --enable-muxer=pcm_s16le \
  --enable-encoder=pcm_s16le \
  --enable-encoder=opus \
  --enable-decoder=mp3float \
  --enable-parser=opus \
  --enable-decoder=opus \
  --enable-filter=aresample \
  --disable-network \
  --disable-autodetect \
  --disable-hwaccels \
  --disable-x86asm \
  --disable-shared \
  --enable-static

# Compile
echo "Compiling ffmpeg & ffprobe..."
make -j "${CORES}"

# Make sure bin directory exists
mkdir -p "${BIN_DIR}"

# Copy output binaries
cp -f ffmpeg ffprobe "${BIN_DIR}/"

echo "Done! Binaries copied to ${BIN_DIR}."
