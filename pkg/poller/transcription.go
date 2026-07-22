package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
)

// NewHTTPTranscriber adapts a speech-to-text endpoint that accepts an `audio`
// multipart field and returns JSON with a `text` field.
func NewHTTPTranscriber(url string) func(context.Context, []byte) (string, error) {
	return func(ctx context.Context, audio []byte) (string, error) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("audio", "stream.mp3")
		if err != nil {
			return "", err
		}
		if _, err := part.Write(audio); err != nil {
			return "", err
		}
		if err := writer.Close(); err != nil {
			return "", err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return "", fmt.Errorf("transcription endpoint returned %s", resp.Status)
		}
		var result struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}
		return result.Text, nil
	}
}
