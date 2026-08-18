package poller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const (
	transcriptionModel        = "Systran/faster-whisper-base"
	transcriptionSessionBytes = 9 * 24000 * 2
)

// StartStreamingTranscription connects any MP3 chunk source to the production
// ffmpeg and Speaches Realtime WebSocket pipeline.
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
	u.Path = "/v1/realtime"
	q := u.Query()
	q.Set("model", transcriptionModel)
	q.Set("intent", "transcription")
	q.Set("language", "ro")
	u.RawQuery = q.Encode()
	return u.String()
}

// WebSocketTranscriber keeps at most one unacknowledged audio buffer plus the
// bounded input channel, and resends that buffer after a reconnect.
type WebSocketTranscriber struct {
	url            string
	audio          chan []byte
	done           chan struct{}
	onTranscript   func(string)
	retryDelay     time.Duration
	requestTimeout time.Duration
}

func NewWebSocketTranscriber(url string, onTranscript func(string)) *WebSocketTranscriber {
	return &WebSocketTranscriber{
		url:            url,
		audio:          make(chan []byte, 64),
		done:           make(chan struct{}),
		onTranscript:   onTranscript,
		retryDelay:     2 * time.Second,
		requestTimeout: 2 * time.Minute,
	}
}

func (t *WebSocketTranscriber) Audio() chan<- []byte  { return t.audio }
func (t *WebSocketTranscriber) Done() <-chan struct{} { return t.done }

func (t *WebSocketTranscriber) Start(ctx context.Context) {
	go func() {
		defer close(t.done)
		var buffered, pending []byte
		audioClosed := false
		for ctx.Err() == nil {
			for len(pending) == 0 && len(buffered) < transcriptionSessionBytes && !audioClosed {
				select {
				case <-ctx.Done():
					return
				case chunk, ok := <-t.audio:
					if !ok {
						audioClosed = true
						break
					}
					buffered = append(buffered, chunk...)
				}
			}
			if len(pending) == 0 {
				if len(buffered) == 0 {
					return
				}
				n := min(len(buffered), transcriptionSessionBytes)
				pending = append([]byte(nil), buffered[:n]...)
				buffered = buffered[n:]
			}

			requestCtx, cancel := context.WithTimeout(ctx, t.requestTimeout)
			err := t.transcribe(requestCtx, pending)
			cancel()
			if err == nil {
				pending = nil
				continue
			} else if ctx.Err() == nil {
				log.Printf("   ⚠️ Streaming transcription: %v", err)
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(t.retryDelay):
			}
		}
	}()
}

func (t *WebSocketTranscriber) transcribe(ctx context.Context, audio []byte) error {
	conn, _, err := websocket.Dial(ctx, t.url, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	for _, event := range []any{
		map[string]any{
			"type": "session.update",
			"session": map[string]any{
				"input_audio_transcription": map[string]string{"model": transcriptionModel, "language": "ro"},
				"turn_detection":            nil,
			},
		},
		map[string]string{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(audio)},
		map[string]string{"type": "input_audio_buffer.commit"},
	} {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return err
		}
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var event struct {
			Type       string `json:"type"`
			Transcript string `json:"transcript"`
			Error      struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			continue
		}
		switch event.Type {
		case "conversation.item.input_audio_transcription.completed":
			if transcript := strings.TrimSpace(event.Transcript); transcript != "" && t.onTranscript != nil {
				t.onTranscript(transcript)
			}
			return nil
		case "error":
			if event.Error.Message == "" {
				event.Error.Message = string(data)
			}
			return errors.New(event.Error.Message)
		}
	}
}
