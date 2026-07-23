package poller

import (
	"context"
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
		_, _ = w.Write([]byte(`{"text":"follow profm"}`))
	}))
	defer server.Close()

	text, err := NewHTTPTranscriber(server.URL)(context.Background(), []byte("radio"))
	if err != nil || text != "follow profm" {
		t.Fatalf("transcribe = (%q, %v)", text, err)
	}
}
