#!/usr/bin/env zsh
set -euo pipefail
setopt null_glob

for cmd in ffmpeg SetFile touch xattr mktemp; do
  command -v "$cmd" >/dev/null 2>&1 || {
    echo "Missing required command: $cmd" >&2
    exit 1
  }
done

files=( *.ogg *.OGG *.Ogg )
files=( "${(@on)files}" )

if (( ${#files[@]} == 0 )); then
  echo "No .ogg files found in the current directory." >&2
  exit 1
fi

rm -f -- .DS_Store

# Stage names first so renaming cannot collide with existing target names.
staged=()
i=1
for f in "${files[@]}"; do
  stage=".rename-stage-${$}-${i}.ogg"
  mv -- "$f" "$stage"
  staged+=( "$stage" )
  ((i++))
done

i=1
for f in "${staged[@]}"; do
  final="WhatsApp Ptt ${i}.ogg"
  mv -- "$f" "$final"
  ((i++))
done

for f in "WhatsApp Ptt "*.ogg; do
  [[ -f "$f" ]] || continue

  xattr -c -- "$f" 2>/dev/null || true

  tmp=$(mktemp /private/tmp/wappmeta.XXXXXX.ogg)
  ffmpeg -y -nostdin -loglevel error -i "$f" \
    -map_metadata -1 \
    -map_chapters -1 \
    -c copy \
    -bitexact \
    "$tmp"

  mv -f -- "$tmp" "$f"
  xattr -c -- "$f" 2>/dev/null || true

  touch -t 197001010000 -- "$f"
  SetFile -d '01/02/1970 12:00:00 AM' "$f"
done

rm -f -- .DS_Store

echo "Processed ${#files[@]} .ogg files in $(pwd)"
