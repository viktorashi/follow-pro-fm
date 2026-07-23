# ---- Stage 1: Build ----
FROM golang:1.26-alpine AS builder

# hadolint ignore=DL3018
RUN apk add --no-cache gcc g++ musl-dev make bash perl git curl cmake

WORKDIR /src

# Copy build script and submodule first to leverage layer caching
COPY scripts/build_ffmpeg.sh ./scripts/
COPY third_party/ffmpeg ./third_party/ffmpeg

# Compile minimal ffmpeg
RUN ./scripts/build_ffmpeg.sh

# Compile whisper.cpp
RUN git clone https://github.com/ggerganov/whisper.cpp.git /src/whisper.cpp && \
    cd /src/whisper.cpp && \
    git checkout master && \
    cmake -B build && \
    cmake --build build --config Release -j4

# Download whisper model
RUN cd /src/whisper.cpp/models && ./download-ggml-model.sh base

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags "-w -s" -o /pro-fm-poller ./cmd/pro-fm-poller

# ---- Stage 2: Runtime ----
FROM alpine:3.20

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /pro-fm-poller .
COPY --from=builder /src/bin/ffmpeg /src/bin/ffprobe /usr/local/bin/

# Copy whisper-cli and model
COPY --from=builder /src/whisper.cpp/build/bin/whisper-cli /usr/local/bin/whisper-cli
RUN mkdir -p /usr/local/share/whisper
COPY --from=builder /src/whisper.cpp/models/ggml-base.bin /usr/local/share/whisper/ggml-base.bin

# /data is where the persistent volume will be mounted for wapp.sqlite
RUN mkdir -p /data

COPY static ./static

ENV TZ=Europe/Bucharest

CMD ["./pro-fm-poller"]
