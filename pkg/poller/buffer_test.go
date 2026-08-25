package poller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCircularAudioBuffer_TriggerPreservesWrappedPrerollAndExtendsFutureBytes(t *testing.T) {
	cab := NewCircularAudioBuffer("", 5)
	cab.writeBytes([]byte("abcdef"))

	done := make(chan []byte, 1)
	cab.Trigger(0, func(data []byte) {
		done <- append([]byte(nil), data...)
	})
	cab.writeBytes([]byte("gh"))

	select {
	case captured := <-done:
		if got, want := string(captured), "bcdefgh"; got != want {
			t.Fatalf("captured = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for trigger callback")
	}
}

func TestCircularAudioBuffer_Trigger(t *testing.T) {
	// 1. Create a dummy HTTP server that streams bytes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		for i := range 20 {
			_, _ = w.Write(bytes.Repeat([]byte{byte(i)}, 100))
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()

	// 2. Init buffer
	// Size: 500 bytes (will wrap around because 20 * 100 = 2000 bytes streamed)
	cab := NewCircularAudioBuffer(server.URL, 500)
	cab.Start()
	defer cab.Stop()

	// 3. Let it fill up partially
	time.Sleep(50 * time.Millisecond)

	// 4. Trigger!
	done := make(chan struct{})
	var captured []byte
	cab.Trigger(100*time.Millisecond, func(data []byte) {
		captured = append([]byte(nil), data...)
		close(done)
	})

	// 5. Wait for it to finish
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for trigger callback")
	}

	// 6. Check
	if len(captured) < 500 {
		t.Errorf("expected captured to include at least the 500 bytes buffer, got %d", len(captured))
	}
}

func TestCircularAudioBuffer_SubscribeGetsIndependentChunk(t *testing.T) {
	cab := NewCircularAudioBuffer("", 8)
	chunks, unsubscribe := cab.Subscribe(1)
	defer unsubscribe()

	input := []byte("radio")
	cab.writeBytes(input)
	input[0] = 'x'

	select {
	case chunk := <-chunks:
		if got, want := string(chunk), "radio"; got != want {
			t.Fatalf("chunk = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for listener chunk")
	}
}
