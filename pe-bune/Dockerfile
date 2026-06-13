# ---- Stage 1: Build ----
FROM golang:1.22-alpine AS builder

# hadolint ignore=DL3018
RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags "-w -s" -o /pro-fm-poller ./cmd/pro-fm-poller

# ---- Stage 2: Runtime ----
FROM alpine:3.20

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /pro-fm-poller .
COPY audios/ ./audios/

# /data is where the persistent volume will be mounted for wapp.sqlite
RUN mkdir -p /data

ENV TZ=Europe/Bucharest

CMD ["./pro-fm-poller"]
