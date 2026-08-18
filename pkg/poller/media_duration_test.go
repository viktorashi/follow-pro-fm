package poller

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetOggDuration(t *testing.T) {
	valid := make([]byte, 14)
	copy(valid, "OggS")
	binary.LittleEndian.PutUint64(valid[6:14], 2*48000)

	tests := []struct {
		name    string
		data    []byte
		want    time.Duration
		wantErr bool
	}{
		{name: "valid", data: valid, want: 2 * time.Second},
		{name: "empty", wantErr: true},
		{name: "missing magic", data: []byte("not an ogg"), wantErr: true},
		{name: "truncated header", data: []byte("prefix OggS"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sample.ogg")
			if err := os.WriteFile(path, tt.data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := getOggDuration(path)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("getOggDuration() = %v, %v; want %v, error=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestGetWavDuration(t *testing.T) {
	valid := []byte{
		'R', 'I', 'F', 'F', 40, 0, 0, 0, 'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ', 16, 0, 0, 0,
		1, 0, 1, 0, 0x40, 0x1f, 0, 0, 0x80, 0x3e, 0, 0, 2, 0, 16, 0,
		'd', 'a', 't', 'a', 0x80, 0x3e, 0, 0,
	}
	valid = append(valid, make([]byte, 16000)...)
	tests := []struct {
		name    string
		data    []byte
		want    time.Duration
		wantErr bool
	}{
		{name: "one second", data: valid, want: time.Second},
		{name: "short header", data: []byte("RIFF"), wantErr: true},
		{name: "invalid header", data: []byte("NOPE0000WAVE"), wantErr: true},
		{name: "missing chunks", data: []byte("RIFF0000WAVE"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sample.wav")
			if err := os.WriteFile(path, tt.data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := getWavDuration(path)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("getWavDuration() = %v, %v; want %v, error=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
