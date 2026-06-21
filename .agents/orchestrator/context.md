# Context - ProFM Go App

## Codebase Architecture
- Entry Point: `cmd/pro-fm-poller/main.go`
- Package: `pkg/poller`
  - `whatsapp.go`: Manages `whatsmeow.Client` initialization, connection, QR pairing, and message sending.
  - `poller.go`: Checks ProFM API metadata every 2 seconds, tracks state, triggers WhatsApp sends.
  - `media.go` & `audio.go`: Logic for remuxing files, managing the voice notes pool (ogg files in `/data/audios`).
  - `db.go`: SQLite Database manager for persisting application state.
  - `server.go`: Echo v5 web server, serving the admin dashboard, login, SSE, and APIs.
  - `state.go`: In-memory thread-safe state manager.
  - `sse.go`: Server-Sent Events broadcaster.
  - `alerter.go`: Sends warnings to Admin (via email/Telegram) on critical status/errors.

## Deployment Target
- Fly.io deployment with SQLite volumes for DBs and audios.
- Environment variables: `TARGET_PHONE`, `WAPP_DB_PATH`, `APP_DB_PATH`, `AUDIOS_DIR`, `BASE_URL`, `ADMIN_PASSWORD`, etc.

## Key Existing Interfaces
- `whatsmeow.Client` is used for WhatsApp.
- `stateMgr` holds the current pairing/status state.
- `dbMgr` manages the SQLite app state.
