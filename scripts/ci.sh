#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

build_image() {
  local ready="$1" image="$2" context="$3"
  if [ "$ready" != "1" ]; then
    docker build -q -t "$image" "$context"
  fi
  docker image inspect "$image" >/dev/null
}

build_image "${WHISPER_TEST_IMAGE_READY:-}" profm-whisper-test:local ./whisper-server

if [ "${1:-}" = "whisper" ]; then
  WHISPER_TEST_IMAGE_READY=1 go test -tags=e2e -v -run '^TestWhisperContainerTranscription$' ./pkg/poller
  exit
fi

build_image "${PROFM_APP_IMAGE_READY:-}" profm-app-ci:local .
./scripts/build_ffmpeg.sh
export PATH="$repo_root/bin:$PATH"
./scripts/golangci-lint-shim.sh run

if [ ! -x bin/gotestsum ]; then
  GOBIN="$repo_root/bin" go install gotest.tools/gotestsum@v1.13.0
fi
bin/gotestsum --junitfile junit.xml -- -coverprofile=coverage.out ./pkg/...
go tool cover -func=coverage.out
WHISPER_TEST_IMAGE_READY=1 go test -tags=e2e -v -run '^TestWhisperContainerTranscription$' ./pkg/poller
