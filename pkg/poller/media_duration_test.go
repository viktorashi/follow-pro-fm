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
