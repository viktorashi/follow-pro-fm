# ---- Stage 1: Build ----
FROM golang:1.26.4-alpine AS builder

# hadolint ignore=DL3018
RUN apk add --no-cache gcc g++ musl-dev make bash perl git curl cmake

WORKDIR /src

# Copy the shared ffmpeg task and submodule first to leverage layer caching
COPY mise-tasks/ffmpeg/build ./mise-tasks/ffmpeg/build
COPY third_party/ffmpeg ./third_party/ffmpeg

# Compile minimal ffmpeg
RUN ./mise-tasks/ffmpeg/build



COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go tool templ generate && \
    CGO_ENABLED=1 go build -trimpath -ldflags "-w -s" -o /pro-fm-poller ./cmd/pro-fm-poller

# ---- Stage 2: Runtime ----
FROM alpine:3.20

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /pro-fm-poller .
COPY --from=builder /src/bin/ffmpeg /src/bin/ffprobe /usr/local/bin/



# /data is where the persistent app and per-sender session databases are mounted.
RUN mkdir -p /data

COPY static ./static

ENV TZ=Europe/Bucharest

CMD ["./pro-fm-poller"]
