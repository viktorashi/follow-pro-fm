package poller

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const transcriptionSessionBytes = 9 * 16000 * 2

var errTranscriptionAudioClosed = errors.New("transcription audio closed")

// StartStreamingTranscription connects any MP3 chunk source to the production
// ffmpeg and Whisper WebSocket pipeline.
func StartStreamingTranscription(ctx context.Context, mp3 <-chan []byte, rawURL string, onTranscript func(string)) *WebSocketTranscriber {
	transcriber := NewWebSocketTranscriber(transcriptionWebSocketURL(rawURL), onTranscript)
	transcriber.Start(ctx)
	NewPCMConverter(mp3, transcriber.Audio()).Start(ctx)
	return transcriber
}

func transcriptionWebSocketURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	q := u.Query()
	q.Set("vad_filter", "true")
	u.RawQuery = q.Encode()
	return u.String()
}

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
			err := t.run(ctx)
			if errors.Is(err, errTranscriptionAudioClosed) {
				return
			}

			if ctx.Err() == nil && err != nil {
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

	sent := 0
	for sent < transcriptionSessionBytes {
		select {
		case <-ctx.Done():
			return nil
		case <-readDone:
			return nil
		case audio, ok := <-t.audio:
			if !ok {
				select {
				case <-ctx.Done():
					return nil
				case <-readDone:
					return errTranscriptionAudioClosed
				}
			}
			if err := conn.Write(ctx, websocket.MessageBinary, audio); err != nil {
				return err
			}
			sent += len(audio)
		}
	}

	// Let Whisper's one-second inactivity timeout flush the current text. Audio
	// keeps buffering for the next connection instead of growing one huge ASR job.
	select {
	case <-ctx.Done():
		return nil
	case <-readDone:
		return nil
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
