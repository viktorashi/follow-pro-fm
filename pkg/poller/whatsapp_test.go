package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	dbPath := filepath.Join(tempDir, "mock_wapp.sqlite")
	messagesJsonPath := filepath.Join(tempDir, "mock_sent_messages.json")

	// Set env vars
	t.Setenv("MOCK_WHATSAPP", "true")
	t.Setenv("MOCK_SENT_MESSAGES_PATH", messagesJsonPath)

	stateMgr := NewStateManager()
	client, err := InitWhatsApp("+40734788254", dbPath, stateMgr, nil, "")
	if err != nil {
		t.Fatalf("failed to init WhatsApp: %v", err)
	}

	mockClient, ok := client.(*MockWhatsAppClient)
	if !ok {
		t.Fatalf("expected MockWhatsAppClient, got %T", client)
	}

	// Wait a moment for async InitWhatsApp/Connect
	time.Sleep(100 * time.Millisecond)

	// Since we haven't paired yet, client is connected but not logged in.
	// SendVoiceNote should fail.
	testAudio := filepath.Join("..", "..", "data", "audios", "WhatsApp Ptt 1.ogg")
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
	err = SendVoiceNote(client, "+40 770-661-491", testAudio)
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

	// Peak value check (excluding completely zero waveforms, which this ogg shouldn't be)
	peak := byte(0)
	for _, val := range record.Waveform {
		if val > peak {
			peak = val
		}
	}
	if peak != 255 {
		t.Errorf("expected peak of 255 in sent waveform, got %d", peak)
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

	dbPath := filepath.Join(tempDir, "mock_wapp.sqlite")
	testAudio := filepath.Join("..", "..", "data", "audios", "WhatsApp Ptt 1.ogg")
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
