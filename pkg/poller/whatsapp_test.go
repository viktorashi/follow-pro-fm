package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

func TestSendVoiceNote_Success(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "send_voice_note_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	dbPath := filepath.Join(tempDir, "wapp_40700000001.sqlite")
	messagesJsonPath := filepath.Join(tempDir, "mock_sent_messages.json")

	// Set env vars
	t.Setenv("MOCK_WHATSAPP", "true")
	t.Setenv("MOCK_SENT_MESSAGES_PATH", messagesJsonPath)

	stateMgr := NewStateManager()
	client, err := InitWhatsApp("+40700000001", dbPath, stateMgr, nil, "")
	if err != nil {
		t.Fatalf("failed to init WhatsApp: %v", err)
	}

	mockClient, ok := client.(*MockWhatsAppClient)
	if !ok {
		t.Fatalf("expected MockWhatsAppClient, got %T", client)
	}

	// Wait a moment for async InitWhatsApp/Connect
	time.Sleep(100 * time.Millisecond)

	mockClient.mu.Lock()
	mockClient.loggedIn = false
	mockClient.mu.Unlock()

	// Since we forced the mock into a logged-out state, client is connected but not logged in.
	// SendVoiceNote should fail.
	testAudio := filepath.Join("testdata", "waveform_sample.ogg")
	if _, err := os.Stat(testAudio); err != nil {
		t.Skipf("skipping test because test audio is not available at %s: %v", testAudio, err)
	}

	err = SendVoiceNote(client, "+40770661491", testAudio)
	if err == nil {
		t.Error("Expected error because client is not logged in, but got nil")
	}

	// Pair the client
	mockClient.SimulatePairing()

	// Wait a moment for pairing state to settle
	time.Sleep(100 * time.Millisecond)

	if !client.IsConnected() || !client.IsLoggedIn() {
		t.Fatalf("client should be connected and logged in after pairing")
	}

	// Send voice note
	sendStart := time.Now().UTC()
	err = SendVoiceNote(client, "+40 770-661-491", testAudio)
	sendEnd := time.Now().UTC()
	if err != nil {
		t.Fatalf("expected SendVoiceNote to succeed, got error: %v", err)
	}

	// Verify that the message was logged to mock sent messages JSON
	recordsData, err := os.ReadFile(messagesJsonPath)
	if err != nil {
		t.Fatalf("failed to read mock sent messages JSON: %v", err)
	}

	var records []MockSentMessage
	if err := json.Unmarshal(recordsData, &records); err != nil {
		t.Fatalf("failed to unmarshal mock records: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 sent record, got %d", len(records))
	}

	record := records[0]
	if record.Phone != "40770661491" {
		t.Errorf("expected normalized phone 40770661491, got %s", record.Phone)
	}

	if len(record.Waveform) != 64 {
		t.Errorf("expected 64-byte waveform, got %d bytes", len(record.Waveform))
	}
	if !bytes.Equal(record.Waveform, expectedWaveformSample()) {
		t.Errorf("unexpected waveform sent: got %v", record.Waveform)
	}

	ffprobePath, err := ffprobeBinaryPath()
	if err != nil {
		t.Skip("ffprobe not installed, skipping uploaded artifact metadata proof")
	}

	mockClient.mu.Lock()
	uploadedAudio := append([]byte(nil), mockClient.uploadedAudio...)
	mockClient.mu.Unlock()
	if len(uploadedAudio) == 0 {
		t.Fatal("expected mock upload to capture remuxed send artifact")
	}

	originalAudio, err := os.ReadFile(testAudio)
	if err != nil {
		t.Fatalf("failed to read original test audio: %v", err)
	}
	if bytes.Equal(uploadedAudio, originalAudio) {
		t.Fatal("expected uploaded artifact to differ from original audio after remux")
	}

	uploadedPath := filepath.Join(tempDir, "uploaded.ogg")
	if err := os.WriteFile(uploadedPath, uploadedAudio, 0644); err != nil {
		t.Fatalf("failed to persist uploaded artifact: %v", err)
	}

	cmd := exec.Command(ffprobePath, "-v", "error", "-show_entries", "stream_tags=creation_time", "-of", "default=noprint_wrappers=1:nokey=1", uploadedPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffprobe failed for uploaded artifact: %v", err)
	}

	creationTimeStr := strings.TrimSpace(out.String())
	if creationTimeStr == "" {
		t.Fatal("expected uploaded artifact creation_time metadata")
	}

	creationTime, err := time.Parse(time.RFC3339, creationTimeStr)
	if err != nil {
		t.Fatalf("failed to parse creation_time %q: %v", creationTimeStr, err)
	}

	sendWindowStart := sendStart.Add(-1 * time.Second)
	sendWindowEnd := sendEnd.Add(1 * time.Second)
	if creationTime.Before(sendWindowStart) || creationTime.After(sendWindowEnd) {
		t.Fatalf("creation_time %s outside expected send window [%s, %s]", creationTime, sendWindowStart, sendWindowEnd)
	}
}

type failMockClient struct {
	*MockWhatsAppClient
	failOnUpload bool
	failOnSend   bool
}

func (f *failMockClient) Upload(ctx context.Context, data []byte, mediaType whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
	if f.failOnUpload {
		return whatsmeow.UploadResponse{}, fmt.Errorf("simulated upload error")
	}
	return f.MockWhatsAppClient.Upload(ctx, data, mediaType)
}

func (f *failMockClient) SendMessage(ctx context.Context, to types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	if f.failOnSend {
		return whatsmeow.SendResponse{}, fmt.Errorf("simulated send error")
	}
	return f.MockWhatsAppClient.SendMessage(ctx, to, message, extra...)
}

func TestSendVoiceNote_Errors(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "send_voice_note_errors_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	dbPath := filepath.Join(tempDir, "wapp_40700000001.sqlite")
	testAudio := filepath.Join("testdata", "waveform_sample.ogg")
	if _, err := os.Stat(testAudio); err != nil {
		t.Skipf("skipping test because test audio is not available at %s", testAudio)
	}

	t.Setenv("MOCK_WHATSAPP", "true")

	// 1. Test upload failure (with retries)
	client := &failMockClient{
		MockWhatsAppClient: &MockWhatsAppClient{
			dbPath:    dbPath,
			connected: true,
			loggedIn:  true,
		},
		failOnUpload: true,
	}

	err = SendVoiceNote(client, "40770661491", testAudio)
	if err == nil {
		t.Error("expected error due to upload failure, got nil")
	}

	// 2. Test send message failure (with retries)
	client = &failMockClient{
		MockWhatsAppClient: &MockWhatsAppClient{
			dbPath:    dbPath,
			connected: true,
			loggedIn:  true,
		},
		failOnSend: true,
	}

	err = SendVoiceNote(client, "40770661491", testAudio)
	if err == nil {
		t.Error("expected error due to send message failure, got nil")
	}
}
