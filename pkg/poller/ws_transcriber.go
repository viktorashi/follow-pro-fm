package poller

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// WebSocketTranscriber maintains a live PCM transcription connection. Its
// bounded input intentionally prefers current radio audio after reconnects.
type WebSocketTranscriber struct {
	url          string
	audio        chan []byte
	onTranscript func(string)

	mu        sync.RWMutex
	connected bool
}

func NewWebSocketTranscriber(url string, onTranscript func(string)) *WebSocketTranscriber {
	return &WebSocketTranscriber{
		url:          url,
		audio:        make(chan []byte, 64),
		onTranscript: onTranscript,
	}
}

func (t *WebSocketTranscriber) Audio() chan<- []byte { return t.audio }

func (t *WebSocketTranscriber) IsConnected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connected
}

func (t *WebSocketTranscriber) setConnected(connected bool) {
	t.mu.Lock()
	t.connected = connected
	t.mu.Unlock()
}

func (t *WebSocketTranscriber) Start(ctx context.Context) {
	go func() {
		for ctx.Err() == nil {
			_ = t.run(ctx)

			if ctx.Err() == nil {
				time.Sleep(2 * time.Second)
			}
		}
	}()
}

func (t *WebSocketTranscriber) run(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, t.url, nil)
	if err != nil {
		log.Printf("   ⚠️ Streaming transcription connect: %v", err)
		return err
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	t.setConnected(true)
	defer t.setConnected(false)

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if transcript := websocketTranscript(data); transcript != "" && t.onTranscript != nil {
				t.onTranscript(transcript)
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-readDone:
			return nil
		case audio := <-t.audio:
			if err := conn.Write(ctx, websocket.MessageBinary, audio); err != nil {
				return nil
			}
		}
	}
}

func websocketTranscript(data []byte) string {
	var result struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &result) == nil && result.Text != "" {
		return strings.TrimSpace(result.Text)
	}
	return strings.TrimSpace(string(data))
}
