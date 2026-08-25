#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

files=()
if [ "$#" -eq 0 ]; then
	while IFS= read -r file; do
		case "$file" in
		*_templ.go) ;;
		*) files+=("$file") ;;
		esac
	done < <(git ls-files '*.go')
else
	for file in "$@"; do
		case "$file" in
		*_templ.go) ;;
		*) files+=("$file") ;;
		esac
	done
fi

if [ "${#files[@]}" -eq 0 ]; then
	exit 0
fi

export GOFLAGS="${GOFLAGS:-} -tags=e2e"
if ! diagnostics="$(go tool gopls check -severity=hint "${files[@]}" 2>&1)"; then
	printf '%s\n' "$diagnostics" >&2
	exit 1
fi
if [ -n "$diagnostics" ]; then
	printf '%s\n' "$diagnostics" >&2
	exit 1
fi
