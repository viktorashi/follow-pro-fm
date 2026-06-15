default:
    just -l

format:
    go fmt ./...

build:
    CGO_ENABLED=1 go build -o ./pro-fm-poller ./cmd/pro-fm-poller/main.go 

air-run:
    air --build.cmd "CGO_ENABLED=1 go build -o ./pro-fm-poller ./cmd/pro-fm-poller/main.go" --build.entrypoint "./pro-fm-poller"

alias fmt := format
alias f := format

setup-dev:
    prek install -f
    go mod tidy

lint:
    ./scripts/golangci-lint-shim.sh run

test:
    go test -count=1 ./pkg/...

test-cover:
    go test -count=1 -coverprofile=coverage.out ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out

test-cover-e2e-nowapp:
    go test -count=1 -v -coverprofile=coverage.out -tags="e2e,nowapp" ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out

test-cover-e2e-all:
    go test -count=1 -v -coverprofile=coverage.out -tags=e2e ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out

# ---- Docker ----

run:
    docker compose up --build

# ---- Fly.io Deployment ----

deploy:
    @echo "Running tests first..."
    go test -count=1 ./pkg/...
    @echo "Tests passed. Deploying to Fly.io..."
    flyctl deploy --remote-only
    @echo "Deployment complete! Checking running machines..."
    flyctl machine list

fly-ssh:
    flyctl ssh console

# Push local data to Fly volume (explicitly EXCLUDING wapp.sqlite to prevent disconnecting real session)
# and preventing the re-upload of already used audio files
push-files:
    @echo "Uploading data folder to Fly persistent volume..."
    env COPYFILE_DISABLE=1 tar -cf - --exclude='wapp.sqlite' --exclude='._*' -C data . | flyctl ssh console -C 'sh -c '\''mkdir -p /tmp/px && tar -xf - -C /tmp/px && if [ -d /tmp/px/audios ]; then for f in /tmp/px/audios/*; do [ -e "$f" ] || continue; name="${f##*/}"; if [ -f "/data/audios/used/$name" ]; then echo "Skipping already used file: $name"; rm -f "$f"; fi; done; fi && tar -cf - -C /tmp/px . | tar -xf - -C /data && rm -rf /tmp/px'\'''
    @echo "✅ Files uploaded."

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
    fly logs -a pro-fm-poller

# List all Fly.io volumes for this application
fly-list-volumes:
    flyctl volumes list

# Spin up a temporary Ubuntu Machine with a volume attached for inspection (usage: just run-temp-fly-vm <volume_id>)
run-temp-fly-vm volume_id="vol_rkg633w5y5p0z3o4":
    flyctl machine run ubuntu:latest --app pro-fm-poller --volume {{ volume_id }}:/data --shell
