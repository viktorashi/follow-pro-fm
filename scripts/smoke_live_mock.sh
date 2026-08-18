#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-8100}"
BASE_URL="${BASE_URL:-http://127.0.0.1:${PORT}}"
TARGET_PHONE="${TARGET_PHONE:-+40770661491}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-smoke}"
TRUSTED_EMAIL="${TRUSTED_EMAIL:-smoke@example.com}"
PROFM_API_URL="${PROFM_API_URL:-https://api.profm.ro/api/v1/radios/article/2918?appVersion=1.0.0&platform=android}"
PROFM_STREAM_URL="${PROFM_STREAM_URL:-http://edge76.rcs-rds.ro:84/profm/profm.mp3}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/profm-live-smoke.XXXXXX")"
COOKIE_JAR="${TMP_DIR}/cookies.txt"
LOG_PATH="${TMP_DIR}/app.log"
APP_PID=""

cleanup() {
  if [ -n "${APP_PID}" ] && kill -0 "${APP_PID}" 2>/dev/null; then
    kill "${APP_PID}" 2>/dev/null || true
    wait "${APP_PID}" 2>/dev/null || true
  fi
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

mkdir -p "${TMP_DIR}/audios"
printf '%s\n' "${TRUSTED_EMAIL}" > "${TMP_DIR}/trusted-emails.txt"

(
  cd "${ROOT_DIR}"
  PORT="${PORT}" \
  BASE_URL="${BASE_URL}" \
  MOCK_WHATSAPP=true \
  TARGET_PHONE="${TARGET_PHONE}" \
  APP_DB_PATH="${TMP_DIR}/app.sqlite" \
  AUDIOS_DIR="${TMP_DIR}/audios" \
  ADMIN_PASSWORD="${ADMIN_PASSWORD}" \
  PROFM_API_URL="${PROFM_API_URL}" \
  PROFM_STREAM_URL="${PROFM_STREAM_URL}" \
  ./pro-fm-poller > "${LOG_PATH}" 2>&1
) &
APP_PID=$!

echo "Smoke app pid: ${APP_PID}"
echo "Smoke state dir: ${TMP_DIR}"

for _ in $(seq 1 30); do
  if curl -fsS "${BASE_URL}/login" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

curl -fsS "${BASE_URL}/login" >/dev/null

curl -fsS -c "${COOKIE_JAR}" \
  -d "email=${TRUSTED_EMAIL}&password=${ADMIN_PASSWORD}" \
  -X POST "${BASE_URL}/login" >/dev/null

dashboard_html="$(curl -fsS -b "${COOKIE_JAR}" "${BASE_URL}/")"
echo "${dashboard_html}" | rg -q "Currently Playing"

schedule_json="$(curl -fsS -b "${COOKIE_JAR}" "${BASE_URL}/api/schedule")"
echo "${schedule_json}" | rg -q '"target_matches"'

curl -fsS -b "${COOKIE_JAR}" \
  -F "name=Smoke Sender" \
  "${BASE_URL}/api/persons" >/dev/null

curl -fsS -b "${COOKIE_JAR}" \
  -F "person_slug=smoke-sender" \
  -F "audio=@${ROOT_DIR}/pkg/poller/testdata/waveform_sample.ogg;type=audio/ogg" \
  "${BASE_URL}/api/audio/upload" >/dev/null

for _ in $(seq 1 15); do
  if rg -q '\[[0-9]{2}:[0-9]{2}:[0-9]{2}\] .+ - .+' "${LOG_PATH}"; then
    break
  fi
  sleep 1
done

rg -q '\[[0-9]{2}:[0-9]{2}:[0-9]{2}\] .+ - .+' "${LOG_PATH}"

dashboard_html="$(curl -fsS -b "${COOKIE_JAR}" "${BASE_URL}/")"
echo "${dashboard_html}" | rg -qv "Waiting for broadcast"

test -f "${TMP_DIR}/audios/smoke-sender/waveform_sample.ogg"

echo "Smoke test passed."
