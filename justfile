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

test-e2e-wapp:
    go test -v -tags=e2e ./pkg/...

test-e2e-nowapp:
    go test -v -tags="e2e,nowapp" ./pkg/...

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

fly-ssh:
    flyctl ssh console

# Push local data to Fly volume (explicitly EXCLUDING wapp.sqlite to prevent disconnecting real session)
push-files:
    @echo "Uploading data folder to Fly persistent volume..."
    tar -cf - --exclude='wapp.sqlite' -C data . | flyctl ssh console -C 'mkdir -p /data && tar -xf - -C /data'
    @echo "✅ Files uploaded."

fly-list-secrets:
    flyctl secrets list
