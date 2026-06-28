#!/bin/sh

set -e

# Must be kept in sync with the version in .github/workflows/ci.yml
version="v2.12.2"

bin_dir="$(cd "$(dirname "$0")/.." && pwd)/bin"
if [ -x "${bin_dir}/golangci-lint" ]; then
  exec "${bin_dir}/golangci-lint" "$@"
fi

go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$version" "$@"
