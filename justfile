default:
    just -l

format:
    go fmt ./...

generate:
    templ generate

build: generate
    CGO_ENABLED=1 go build -o ./pro-fm-poller ./cmd/pro-fm-poller/main.go 

air-run:
    air --build.cmd "CGO_ENABLED=1 go build -o ./pro-fm-poller ./cmd/pro-fm-poller/main.go" --build.entrypoint "./pro-fm-poller"

alias fmt := format
alias f := format

setup-dev:
    git submodule update --init --recursive third_party/ffmpeg
    prek install -f
    go mod tidy

lint:
    ./scripts/golangci-lint-shim.sh run

build-ffmpeg:
    ./scripts/build_ffmpeg.sh

test: build-ffmpeg generate
    PATH="./bin:$PATH" go test ./pkg/...

smoke-live-mock: build
    ./scripts/smoke_live_mock.sh

test-cover: build-ffmpeg generate
    PATH="./bin:$PATH" go test -coverprofile=coverage.out ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out

test-cover-e2e-nowapp: build-ffmpeg generate
    PATH="./bin:$PATH" go test -v -coverprofile=coverage.out -tags="e2e,nowapp" ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out

test-cover-e2e-all: build-ffmpeg generate
    PATH="./bin:$PATH" go test -v -coverprofile=coverage.out -tags=e2e ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out

test-transcriptions:
    go test -run TestWhisperContainerTranscription ./pkg/poller/...

# ---- Docker ----

run:
    ENVIRONMENT=dev docker compose up --build

# ---- Fly.io Deployment ----

# Mai bine da-i commit si push pe main, sincer avem acolo tot CI/CD-u
# BUT BE SURE YOU ACTUALLY HAVE STAGED EVERYTHING. It's pretty load heavy if you do small pushes, instead commit a lot locally and run tests or whatnot, then push once after the feature seems ready.
#
# Also, before starting to make changes to dev branch (which is reccomended) please makes sure you're all up-to-date with main
# deploy:
#     @echo "Running tests first..."
#     go test -count=1 ./pkg/...
#     @echo "Tests passed. Deploying to Fly.io..."
#     flyctl deploy --remote-only
#     @echo "Deployment complete! Checking running machines..."
#     flyctl machine list
#

fly-ssh:
    flyctl ssh console

# Push audios to production.
# Audio files in local 'used/' directories will be published to the root of their respective remote pool.
# Production code will automatically hash and skip any audios that have already been used in prod.
# Usage:
#   just push-audios                        (pushes local 'data/audios' to remote '/data/audios')
#   just push-files                         (alias for push-audios)
#   just push-audios ./my_audios            (pushes to canonical sender '/data/audios/')
#   just push-audios ./my_audios 40771234567 (pushes to '/data/audios/40771234567/')
#   just push-audios ./my_audios /           (pushes to canonical sender '/data/audios/')
push-audios LOCAL_DIR="data/audios" PHONE="":
    #!/usr/bin/env bash
    set -e
    LOCAL="{{ LOCAL_DIR }}"
    PHONE="{{ PHONE }}"

    # If the user accidentally specifies 'data', forcefully correct it to 'data/audios'
    if [ "$LOCAL" = "data" ]; then
        LOCAL="data/audios"
    fi

    if [ ! -d "$LOCAL" ]; then
        echo "❌ Local directory '$LOCAL' does not exist."
        exit 1
    fi

    if [ "$LOCAL" = "data/audios" ]; then
        TARGET="/data/audios"
        echo "Uploading local 'data/audios' folder to Fly persistent volume..."
    else
        if [ -z "$PHONE" ] || [ "$PHONE" = "/" ]; then
            TARGET="/data/audios"
            echo "Pushing audios from $LOCAL to canonical sender at $TARGET..."
        else
            PHONE=$(echo "$PHONE" | sed 's/+//g' | sed 's/ //g')
            TARGET="/data/audios/$PHONE"
            echo "Pushing audios from $LOCAL to phone $PHONE at $TARGET..."
        fi
    fi

    TMP_STAGING=$(mktemp -d)
    trap 'rm -rf "$TMP_STAGING"' EXIT

    # Copy files into temporary staging directory
    cp -R "$LOCAL/." "$TMP_STAGING/"

    # Publish files inside any local 'used/' directories to the root of their respective parent pool
    find "$TMP_STAGING" -type d -name "used" | while read -r used_dir; do
        parent_dir=$(dirname "$used_dir")
        find "$used_dir" -maxdepth 1 -type f \( -name "*.ogg" -o -name "*.mp3" -o -name "*.wav" \) | while read -r audio_file; do
            filename=$(basename "$audio_file")
            mv -f "$audio_file" "$parent_dir/$filename"
        done
        rm -rf "$used_dir"
    done

    flyctl ssh console -C "mkdir -p $TARGET"
    # STRICTLY forbid any database files from ever being uploaded
    env COPYFILE_DISABLE=1 tar -cf - --exclude='*.sqlite*' --exclude='*.db' --exclude='._*' -C "$TMP_STAGING" . | flyctl ssh console -C "tar -xf - -C $TARGET"
    echo "✅ Audios uploaded (locally used files published to root pool)."

alias push-files := push-audios

# Clean up accidentally uploaded macOS ._ metadata files from the Fly persistent volume
fly-cleanup-mac-files:
    @echo "Removing all ._* Apple metadata files from the Fly volume..."
    flyctl ssh console -C "sh -c 'find /data -name \"._*\" -type f -delete'"
    @echo "✅ Cleanup complete."

# List all files inside the Fly.io persistent volume
fly-list-files:
    flyctl ssh console -C 'find /data -maxdepth 4'

# Read the contents of a specific file inside the Fly.io persistent volume (usage: just fly-cat <filepath>)
fly-cat filepath:
    flyctl ssh console -C 'cat /data/{{ filepath }}'

# Pull all files from Fly.io persistent volume to local data directory (excluding wapp.sqlite)
fly-pull-files:
    @echo "Downloading files from Fly persistent volume to local 'data' directory..."
    flyctl ssh console -C 'tar -cf - --exclude="wapp.sqlite" -C /data .' | tar -xf - -C data
    @echo "✅ Files downloaded."

# Pull the wapp.sqlite database file from Fly.io persistent volume
fly-pull-db:
    @echo "Downloading wapp.sqlite from Fly persistent volume..."
    flyctl ssh console -C 'tar -cf - -C /data wapp.sqlite' | tar -xf - -C data
    @echo "✅ Database downloaded."

# Pull the entire contents of the Fly.io persistent volume to a versioned sub-directory in downloaded-from-fly/
fly-pull-all:
    @NEXT_NUM=1; \
     while [ -d "downloaded-from-fly/$NEXT_NUM" ]; do \
         NEXT_NUM=$((NEXT_NUM + 1)); \
     done; \
     echo "Downloading all files and databases from Fly persistent volume to downloaded-from-fly/$NEXT_NUM..."; \
     mkdir -p "downloaded-from-fly/$NEXT_NUM"; \
     flyctl ssh console -C 'tar -cf - -C /data .' | tar -xf - -C "downloaded-from-fly/$NEXT_NUM"
    @echo "✅ All files and databases downloaded."

fly-list-secrets:
    flyctl secrets list

# Show comprehensive status of Fly.io application, machines, and volumes
fly-status:
    flyctl status
    @echo "\n=== Running Machines ==="
    flyctl machine list
    @echo "\n=== Persistent Volumes ==="
    flyctl volumes list

fly-logs:
    fly logs 

# List all Fly.io volumes for this application
fly-list-volumes:
    flyctl volumes list

# Spin up a temporary Ubuntu Machine with a volume attached for inspection (usage: just run-temp-fly-vm <volume_id>)
run-temp-fly-vm volume_id="vol_rkg633w5y5p0z3o4":
    flyctl machine run ubuntu:latest --app pro-fm-poller --volume {{ volume_id }}:/data --shell

deploy:
    fly deploy --ha=false

deploy-whisper:
    cd whisper-server && fly deploy #stateless so ok

restart-whisper:
    fly apps restart pro-fm-whisper
