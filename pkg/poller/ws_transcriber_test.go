package poller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebSocketTranscriberUsesRealtimeProtocol(t *testing.T) {
	audio := []byte{1, 2, 3}
	server := realtimeTestServer(t, func(conn *websocket.Conn, ctx context.Context) {
		events := readRealtimeEvents(t, conn, ctx)
		if got := eventType(events[0]); got != "session.update" {
			t.Errorf("first event = %q, want session.update", got)
		}
		session := events[0]["session"].(map[string]any)
		transcription := session["input_audio_transcription"].(map[string]any)
		if transcription["model"] != transcriptionModel || transcription["language"] != "ro" {
			t.Errorf("input_audio_transcription = %#v", transcription)
		}
		if value, exists := session["turn_detection"]; !exists || value != nil {
			t.Errorf("turn_detection = %#v, want null", value)
		}
		if got := eventType(events[1]); got != "input_audio_buffer.append" {
			t.Errorf("second event = %q, want input_audio_buffer.append", got)
		}
		decoded, err := base64.StdEncoding.DecodeString(events[1]["audio"].(string))
		if err != nil || string(decoded) != string(audio) {
			t.Errorf("audio = %v, %v; want %v", decoded, err, audio)
		}
		if got := eventType(events[2]); got != "input_audio_buffer.commit" {
			t.Errorf("third event = %q, want input_audio_buffer.commit", got)
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"session.updated"}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":" follow profm "}`))
	})
	defer server.Close()

	var transcript string
	transcriber := NewWebSocketTranscriber(wsURL(server.URL), func(text string) { transcript = text })
	if err := transcriber.transcribe(context.Background(), audio); err != nil {
		t.Fatal(err)
	}
	if transcript != "follow profm" {
		t.Fatalf("transcript = %q, want follow profm", transcript)
	}
}

func TestWebSocketTranscriberSurfacesServerErrors(t *testing.T) {
	server := realtimeTestServer(t, func(conn *websocket.Conn, ctx context.Context) {
		readRealtimeEvents(t, conn, ctx)
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"error","error":{"message":"model unavailable"}}`))
	})
	defer server.Close()

	err := NewWebSocketTranscriber(wsURL(server.URL), nil).transcribe(context.Background(), []byte{1})
	if err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("error = %v, want model unavailable", err)
	}
}

func TestWebSocketTranscriberRetriesPendingAudio(t *testing.T) {
	var connections atomic.Int32
	payloads := make(chan string, 2)
	server := realtimeTestServer(t, func(conn *websocket.Conn, ctx context.Context) {
		events := readRealtimeEvents(t, conn, ctx)
		payloads <- events[1]["audio"].(string)
		if connections.Add(1) == 1 {
			_ = conn.Close(websocket.StatusInternalError, "retry")
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"follow profm"}`))
	})
	defer server.Close()

	transcripts := make(chan string, 1)
	transcriber := NewWebSocketTranscriber(wsURL(server.URL), func(text string) { transcripts <- text })
	transcriber.retryDelay = time.Millisecond
	ctx := t.Context()
	transcriber.Start(ctx)
	transcriber.audio <- make([]byte, transcriptionSessionBytes)
	close(transcriber.audio)

	select {
	case <-transcripts:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transcript after reconnect")
	}
	first, second := <-payloads, <-payloads
	if first != second {
		t.Fatal("pending audio changed across reconnect")
	}
}

func TestWebSocketTranscriberRetriesTimedOutRequest(t *testing.T) {
	var connections atomic.Int32
	server := realtimeTestServer(t, func(conn *websocket.Conn, ctx context.Context) {
		readRealtimeEvents(t, conn, ctx)
		if connections.Add(1) == 1 {
			time.Sleep(100 * time.Millisecond)
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"follow profm"}`))
	})
	defer server.Close()

	transcripts := make(chan string, 1)
	transcriber := NewWebSocketTranscriber(wsURL(server.URL), func(text string) { transcripts <- text })
	transcriber.retryDelay = time.Millisecond
	transcriber.requestTimeout = 10 * time.Millisecond
	transcriber.Start(context.Background())
	transcriber.audio <- make([]byte, transcriptionSessionBytes)
	close(transcriber.audio)

	select {
	case <-transcripts:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transcript after request timeout")
	}
}

func TestWebSocketTranscriberCommitsPartialClosedStream(t *testing.T) {
	received := make(chan []byte, 1)
	server := realtimeTestServer(t, func(conn *websocket.Conn, ctx context.Context) {
		events := readRealtimeEvents(t, conn, ctx)
		audio, _ := base64.StdEncoding.DecodeString(events[1]["audio"].(string))
		received <- audio
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"partial"}`))
	})
	defer server.Close()

	transcripts := make(chan string, 1)
	transcriber := NewWebSocketTranscriber(wsURL(server.URL), func(text string) { transcripts <- text })
	transcriber.Start(context.Background())
	transcriber.audio <- []byte{7, 8, 9}
	close(transcriber.audio)

	select {
	case got := <-received:
		if string(got) != string([]byte{7, 8, 9}) {
			t.Fatalf("partial audio = %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for partial commit")
	}
	select {
	case <-transcriber.Done():
	case <-time.After(time.Second):
		t.Fatal("transcriber did not finish after the audio stream closed")
	}
}

func TestWebSocketTranscriberHonorsContextCancellation(t *testing.T) {
	server := realtimeTestServer(t, func(conn *websocket.Conn, ctx context.Context) {
		readRealtimeEvents(t, conn, ctx)
		<-ctx.Done()
	})
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewWebSocketTranscriber(wsURL(server.URL), nil).transcribe(ctx, []byte{1})
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("transcriber ignored context cancellation")
	}
}

func TestTranscriptionWebSocketURL(t *testing.T) {
	got, err := url.Parse(transcriptionWebSocketURL("https://whisper.internal/v1/audio/transcriptions?old=value"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Scheme != "wss" || got.Path != "/v1/realtime" {
		t.Fatalf("URL = %s", got)
	}
	if q := got.Query(); q.Get("model") != transcriptionModel || q.Get("intent") != "transcription" || q.Get("language") != "ro" {
		t.Fatalf("query = %v", got.Query())
	}
}

func realtimeTestServer(t *testing.T, handler func(*websocket.Conn, context.Context)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		conn.SetReadLimit(1 << 20)
		handler(conn, r.Context())
	}))
}

func readRealtimeEvents(t *testing.T, conn *websocket.Conn, ctx context.Context) []map[string]any {
	t.Helper()
	events := make([]map[string]any, 3)
	for i := range events {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Error(err)
			return events
		}
		if err := json.Unmarshal(data, &events[i]); err != nil {
			t.Error(err)
			return events
		}
	}
	return events
}

func eventType(event map[string]any) string {
	value, _ := event["type"].(string)
	return value
}

func wsURL(rawURL string) string {
	return "ws" + strings.TrimPrefix(rawURL, "http")
}
