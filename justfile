default:
    just -l
build:
    go build -o pro-fm-poller ./cmd/pro-fm-poller

run: build
    ./pro-fm-poller

format:
    go fmt ./...

alias fmt := format
alias f := format

test:
    go test ./pkg/...

test-e2e:
    go test -v -tags=e2e ./pkg/...

lint:
    ./scripts/golangci-lint-shim.sh run

test-cover:
    go test -coverprofile=coverage.out ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out

test-cover-e2e:
    go test -v -coverprofile=coverage.out -tags=e2e ./pkg/...
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out

# ---- Docker ----

docker-build:
    docker build -t pro-fm-poller .

docker-run: docker-build
    docker run --rm -v "$(pwd)/tests/e2e.sqlite:/data/wapp.sqlite" -e WAPP_DB_PATH=/data/wapp.sqlite pro-fm-poller

# ---- Fly.io Deployment ----

deploy:
    @echo "Running tests first..."
    go test ./pkg/...
    @echo "Tests passed. Deploying to Fly.io..."
    flyctl deploy --remote-only

# Push local WhatsApp session and audios to Fly volume (one-time after pairing)
push-files:
    @echo "Uploading tests/e2e.sqlite to Fly persistent volume..."
    flyctl ssh sftp shell <<< "put tests/e2e.sqlite /data/wapp.sqlite"
    flyctl ssh sftp shell <<< "put audios /data/audios"
    @echo "✅ Session uploaded. Restart with: flyctl apps restart"
