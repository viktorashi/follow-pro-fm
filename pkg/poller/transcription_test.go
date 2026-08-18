package poller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHTTPTranscriberPostsAudioAndReadsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Fatal(err)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		if data, _ := io.ReadAll(file); string(data) != "radio" {
			t.Fatalf("audio = %q, want radio", data)
		}
		for field, want := range map[string]string{
			"model":              transcriptionModel,
			"language":           "ro",
			"without_timestamps": "true",
		} {
			if got := r.FormValue(field); got != want {
				t.Errorf("%s = %q, want %q", field, got, want)
			}
		}
		_, _ = w.Write([]byte(`{"text":"follow profm"}`))
	}))
	defer server.Close()

	text, err := NewHTTPTranscriber(server.URL)(context.Background(), []byte("radio"))
	if err != nil || text != "follow profm" {
		t.Fatalf("transcribe = (%q, %v)", text, err)
	}
}

func TestNewHTTPTranscriberErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "server status", handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusBadGateway) }},
		{name: "invalid json", handler: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()
			if _, err := NewHTTPTranscriber(server.URL)(context.Background(), []byte("radio")); err == nil {
				t.Fatal("transcription succeeded unexpectedly")
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewHTTPTranscriber("http://127.0.0.1:1")(ctx, []byte("radio"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled transcription error = %v", err)
	}
}
