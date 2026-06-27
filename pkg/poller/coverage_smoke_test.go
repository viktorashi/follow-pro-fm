package poller

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
)

func TestRenderDashboardTemplates(t *testing.T) {
	state := NewStateManager().Get()
	state.CurrentSong = "BTS - Dynamite"
	state.UnusedAudios = 3
	state.UsedAudios = 7
	state.Connections = []WAConnectionState{
		{Phone: CanonicalSenderPhone, Status: StatusPairingRequired, WhatsAppConnected: true, QRCodeData: "data:image/png;base64,Zm9v"},
	}

	chunks := []ReviewChunk{{
		Name:    "BTS - Butter.mp3",
		Size:    128,
		ModTime: "2026-06-27 12:00:00",
		PlayURL: "/api/unreviewed/file?name=BTS+-+Butter.mp3",
	}}
	schedules := []ScheduleEntry{{Date: "2026-06-23", TargetMatches: []int{1, 3, 5}}}
	files := []FileInfo{{Name: "audio.ogg", Path: "audios/audio.ogg", Size: 42, ModTime: "2026-06-27 12:00:00"}}
	logs := []RadioLog{{ID: 1, PlayedDatetime: "2026-06-27 12:00:00", Artist: "BTS", Title: "Dynamite"}}

	components := map[string]templ.Component{
		"Layout":          Layout("T"),
		"Login":           Login(),
		"Dashboard":       Dashboard(state, chunks, []string{CanonicalSenderPhone}, schedules),
		"StatusComponent": StatusComponent(state),
		"SongComponent":   SongComponent(state.CurrentSong),
		"AudioStats":      AudioStatsComponent(state.UnusedAudios, state.UsedAudios),
		"QRComponent":     QRComponent(state.Connections),
		"LogsPage":        LogsPage(),
		"DataViewer":      DataViewer(files),
		"RadioLogsPage":   RadioLogsPage(logs),
	}

	for name, component := range components {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := component.Render(context.Background(), &buf); err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if buf.Len() == 0 {
				t.Fatal("expected rendered output")
			}
		})
	}
}

func TestRenderDashboardTemplatesBodies(t *testing.T) {
	state := NewStateManager().Get()
	state.CurrentSong = "BTS - Dynamite"
	state.Connections = []WAConnectionState{{Phone: CanonicalSenderPhone, Status: StatusConnected, WhatsAppConnected: true}}

	cases := []struct {
		name   string
		render func(*bytes.Buffer) error
		want   string
	}{
		{
			name: "Dashboard",
			render: func(buf *bytes.Buffer) error {
				return Dashboard(state, nil, []string{CanonicalSenderPhone}, []ScheduleEntry{{Date: "2026-06-23", TargetMatches: []int{1, 3}}}).Render(context.Background(), buf)
			},
			want: "Daily RNG Schedule",
		},
		{
			name: "DataViewer",
			render: func(buf *bytes.Buffer) error {
				return DataViewer([]FileInfo{{Name: "audio.ogg", Path: "audios/audio.ogg", Size: 42, ModTime: "2026-06-27 12:00:00"}}).Render(context.Background(), buf)
			},
			want: "audio.ogg",
		},
		{
			name: "RadioLogsPage",
			render: func(buf *bytes.Buffer) error {
				return RadioLogsPage([]RadioLog{{ID: 1, PlayedDatetime: "2026-06-27 12:00:00", Artist: "BTS", Title: "Dynamite"}}).Render(context.Background(), buf)
			},
			want: "Dynamite",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.render(&buf); err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Fatalf("rendered body missing %q", tc.want)
			}
		})
	}
}

func TestGetRandomAudioAndFormatScheduleTargets(t *testing.T) {
	dir := t.TempDir()
	if _, err := GetRandomAudio(dir); err == nil {
		t.Fatal("expected empty audio pool error")
	}

	path := filepath.Join(dir, "sample.ogg")
	if err := os.WriteFile(path, []byte("ogg"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := GetRandomAudio(dir)
	if err != nil {
		t.Fatalf("GetRandomAudio() error = %v", err)
	}
	if got != path {
		t.Fatalf("GetRandomAudio() = %q, want %q", got, path)
	}
	if got := formatScheduleTargets([]int{1, 3, 5}); got != "1, 3, 5" {
		t.Fatalf("formatScheduleTargets() = %q", got)
	}
}

func TestGetAudioDurationFallbacks(t *testing.T) {
	wavPath := filepath.Join(t.TempDir(), "tiny.wav")
	wav := []byte{
		'R', 'I', 'F', 'F', 40, 0, 0, 0,
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ', 16, 0, 0, 0,
		1, 0,
		1, 0,
		0x40, 0x1f, 0x00, 0x00,
		0x80, 0x3e, 0x00, 0x00,
		2, 0,
		16, 0,
		'd', 'a', 't', 'a', 4, 0, 0, 0,
		0, 0, 0, 0,
	}
	if err := os.WriteFile(wavPath, wav, 0o644); err != nil {
		t.Fatalf("WriteFile(wav) error = %v", err)
	}

	if got, err := GetAudioDuration(wavPath); err != nil || got <= 0 {
		t.Fatalf("GetAudioDuration(wav) = %v, %v", got, err)
	}

	otherPath := filepath.Join(t.TempDir(), "sample.bin")
	if err := os.WriteFile(otherPath, bytes.Repeat([]byte("a"), 5000), 0o644); err != nil {
		t.Fatalf("WriteFile(bin) error = %v", err)
	}
	if got, err := GetAudioDuration(otherPath); err != nil || got != 2*time.Second {
		t.Fatalf("GetAudioDuration(bin) = %v, %v, want 2s", got, err)
	}
}

func TestSSEEventMarshalAndBufferRead(t *testing.T) {
	ev := &SSEEvent{Event: "status", Data: []byte("<div>ok</div>")}
	raw := string(ev.Marshal())
	if !strings.Contains(raw, "event: status") || !strings.Contains(raw, "data: <div>ok</div>") {
		t.Fatalf("Marshal() = %q", raw)
	}

	buf := &CircularAudioBuffer{buffer: make([]byte, 8)}
	buf.writeBytes([]byte("abcdefgh"))
	if got := string(buf.ReadCurrentBuffer()); got != "abcdefgh" {
		t.Fatalf("ReadCurrentBuffer() = %q", got)
	}
}

func TestHandleUnreviewedCropAndHelpers(t *testing.T) {
	dataDir := t.TempDir()
	unreviewedDir := filepath.Join(dataDir, "signatures", "unreviewed")
	if err := os.MkdirAll(unreviewedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	filename := "intro.mp3"
	if err := os.WriteFile(filepath.Join(unreviewedDir, filename), []byte("abcdefgh"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	server := &TelemetryServer{dataDir: dataDir}

	var body bytes.Buffer
	writer := multipartNewWriter(t, &body, map[string]string{
		"filename":    filename,
		"start_bytes": "2",
		"end_bytes":   "6",
	})

	e := newTestEcho()
	req := httptest.NewRequest(http.MethodPost, "/unreviewed/crop", &body)
	req.Header.Set("Content-Type", writer)
	rec := httptest.NewRecorder()

	if err := server.handleUnreviewedCrop(e.NewContext(req, rec)); err != nil {
		t.Fatalf("handleUnreviewedCrop() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	canonical, err := os.ReadFile(filepath.Join(dataDir, "signatures", "canonical", filename))
	if err != nil {
		t.Fatalf("ReadFile(canonical) error = %v", err)
	}
	if string(canonical) != "cdef" {
		t.Fatalf("canonical contents = %q, want %q", string(canonical), "cdef")
	}

	if mime := audioMimeType("clip.ogg"); !strings.Contains(mime, "ogg") {
		t.Fatalf("audioMimeType(ogg) = %q", mime)
	}
	if mime := audioMimeType("clip.weird"); mime != "application/octet-stream" {
		t.Fatalf("audioMimeType(weird) = %q", mime)
	}
	if !isSafeFilename("ok.mp3") || isSafeFilename("../bad.mp3") {
		t.Fatal("isSafeFilename() guard failed")
	}
}

func TestListScheduleEntriesAndDashboardPhones(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	if err := dbMgr.SetDailySchedule(nilContext(), "2026-06-24", "[2,4]"); err != nil {
		t.Fatalf("SetDailySchedule() error = %v", err)
	}
	if err := dbMgr.SetDailySchedule(nilContext(), "2026-06-23", "[1,3]"); err != nil {
		t.Fatalf("SetDailySchedule() error = %v", err)
	}

	server := &TelemetryServer{dbMgr: dbMgr}
	entries, err := server.listScheduleEntries(nilContext())
	if err != nil {
		t.Fatalf("listScheduleEntries() error = %v", err)
	}
	if len(entries) != 2 || entries[0].Date != "2026-06-23" || entries[1].Date != "2026-06-24" {
		t.Fatalf("entries = %+v", entries)
	}

	phones := dashboardUploadPhones([]WAConnectionState{
		{Phone: "+40111222333"},
		{Phone: "40111222333"},
		{Phone: CanonicalSenderPhone},
	})
	if len(phones) != 2 || phones[0] != CanonicalSenderPhone || phones[1] != "+40111222333" {
		t.Fatalf("dashboardUploadPhones() = %+v", phones)
	}
	if !isKnownDashboardPhone("40111222333", []WAConnectionState{{Phone: "+40111222333"}}) {
		t.Fatal("expected normalized dashboard phone match")
	}
}

func multipartNewWriter(t *testing.T, body *bytes.Buffer, fields map[string]string) string {
	t.Helper()
	w := multipart.NewWriter(body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("WriteField(%s) error = %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return w.FormDataContentType()
}

func newTestEcho() *echo.Echo {
	return echo.New()
}

func nilContext() context.Context {
	return context.Background()
}
