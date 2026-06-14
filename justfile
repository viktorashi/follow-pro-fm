default:
    just -l

format:
    go fmt ./...

alias fmt := format
alias f := format

setup-dev:
    prek install -f
    go mod tidy

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

run:
    docker compose up --build

# ---- Fly.io Deployment ----

deploy:
    @echo "Running tests first..."
    go test ./pkg/...
    @echo "Tests passed. Deploying to Fly.io..."
    flyctl deploy --remote-only

# Push local WhatsApp session and audios to Fly volume (one-time after pairing)
push-files:
    @echo "Uploading data/wapp.sqlite to Fly persistent volume..."
    flyctl ssh sftp shell <<< "put data/wapp.sqlite /data/wapp.sqlite"
    flyctl ssh sftp shell <<< "put data/audios /data/audios"
    @echo "✅ Session and audios uploaded. Restart with: flyctl apps restart"
