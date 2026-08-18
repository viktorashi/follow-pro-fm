package poller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebSocketTranscriberSendsPCMAndReceivesTranscript(t *testing.T) {
	receivedAudio := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		_, audio, err := conn.Read(r.Context())
		if err != nil {
			t.Error(err)
			return
		}
		receivedAudio <- audio
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"text":"follow profm"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transcripts := make(chan string, 1)
	transcriber := NewWebSocketTranscriber("ws"+strings.TrimPrefix(server.URL, "http"), func(text string) {
		transcripts <- text
	})
	transcriber.Start(ctx)
	transcriber.Audio() <- []byte{1, 2, 3}

	select {
	case audio := <-receivedAudio:
		if got, want := string(audio), string([]byte{1, 2, 3}); got != want {
			t.Fatalf("audio = %v, want %v", audio, []byte{1, 2, 3})
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PCM")
	}
	select {
	case transcript := <-transcripts:
		if got, want := transcript, "follow profm"; got != want {
			t.Fatalf("transcript = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transcript")
	}
}

func TestWebSocketTranscriberReconnects(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		if connections.Add(1) == 1 {
			return
		}
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"text":"follow profm"}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transcripts := make(chan string, 1)
	transcriber := NewWebSocketTranscriber("ws"+strings.TrimPrefix(server.URL, "http"), func(text string) {
		transcripts <- text
	})
	transcriber.Start(ctx)

	deadline := time.Now().Add(4 * time.Second)
	for connections.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if connections.Load() < 2 {
		t.Fatal("transcriber did not reconnect")
	}
	transcriber.Audio() <- []byte{1}
	select {
	case transcript := <-transcripts:
		if transcript != "follow profm" {
			t.Fatalf("transcript = %q, want follow profm", transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transcript after reconnect")
	}
}

func TestWebsocketTranscriptAcceptsPlainText(t *testing.T) {
	if got, want := websocketTranscript([]byte(" follow profm ")), "follow profm"; got != want {
		t.Fatalf("transcript = %q, want %q", got, want)
	}
}
