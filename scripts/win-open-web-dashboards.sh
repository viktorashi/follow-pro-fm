#!/usr/bin/env bash

#Toat astea care ne zic chestii depsre aplicatie
urls=(
  "https://pro-fm-poller.fly.dev"
  "https://fly.io/apps/pro-fm-poller"
  "https://app.codecov.io/gh/viktorashi/follow-pro-fm"
  "https://github.com/viktorashi/follow-pro-fm/actions/workflows/ci.yml"
  "https://app.sendgrid.com/email_logs"
)

for url in "${urls[@]}"; do
  explorer.exe "$url"
done
