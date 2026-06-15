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

fly-ssh:
    flyctl ssh console

# Push local data to Fly volume (explicitly EXCLUDING wapp.sqlite to prevent disconnecting real session)
push-files:
    @echo "Uploading data folder to Fly persistent volume..."
    tar -cf - --exclude='wapp.sqlite' -C data . | flyctl ssh console -C 'mkdir -p /data && tar -xf - -C /data'
    @echo "✅ Files uploaded."

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

fly-list-secrets:
    flyctl secrets list

# Show status of Fly.io application and its machines
fly-status:
    flyctl status

# List all Fly.io volumes for this application
fly-list-volumes:
    flyctl volumes list
