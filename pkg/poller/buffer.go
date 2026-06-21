package poller

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"
)

// CircularAudioBuffer maintains a rolling buffer of an audio stream.
type CircularAudioBuffer struct {
	mu           sync.Mutex
	buffer       []byte
	writeIdx     int
	totalWritten int64
	isRecording  bool
	recordBuf    *bytes.Buffer
	recordEnd    time.Time
	saveCallback func(data []byte)
	streamURL    string
	cancel       chan struct{}
}

// NewCircularAudioBuffer creates a new circular buffer.
// For a 128kbps stream, 3 minutes is ~2.88MB. 8MB covers >8 minutes.
func NewCircularAudioBuffer(streamURL string, sizeBytes int) *CircularAudioBuffer {
	return &CircularAudioBuffer{
		buffer:    make([]byte, sizeBytes),
		streamURL: streamURL,
		cancel:    make(chan struct{}),
	}
}

func (cab *CircularAudioBuffer) Start() {
	go cab.loop()
}

func (cab *CircularAudioBuffer) Stop() {
	close(cab.cancel)
}

func (cab *CircularAudioBuffer) loop() {
	for {
		select {
		case <-cab.cancel:
			return
		default:
		}

		resp, err := http.Get(cab.streamURL)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		buf := make([]byte, 8192)
		for {
			select {
			case <-cab.cancel:
				_ = resp.Body.Close()
				return
			default:
			}
			n, err := resp.Body.Read(buf)
			if n > 0 {
				cab.writeBytes(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					time.Sleep(1 * time.Second) // Small backoff on network error
				}
				_ = resp.Body.Close()
				break
			}
		}
	}
}

func (cab *CircularAudioBuffer) writeBytes(data []byte) {
	cab.mu.Lock()
	defer cab.mu.Unlock()

	size := len(cab.buffer)
	for _, b := range data {
		cab.buffer[cab.writeIdx] = b
		cab.writeIdx = (cab.writeIdx + 1) % size
		cab.totalWritten++
	}

	if cab.isRecording {
		cab.recordBuf.Write(data)
		if time.Now().After(cab.recordEnd) {
			cab.isRecording = false
			finalData := cab.recordBuf.Bytes()
			cab.recordBuf = nil
			go cab.saveCallback(finalData)
		}
	}
}

// Trigger captures the current buffer contents and continues recording for futureDuration.
// It calls callback exactly once when the recording duration is finished.
func (cab *CircularAudioBuffer) Trigger(futureDuration time.Duration, callback func([]byte)) {
	cab.mu.Lock()
	defer cab.mu.Unlock()

	if cab.isRecording {
		// Already recording; we could extend the time, but for simplicity ignore.
		return
	}

	cab.isRecording = true
	cab.recordBuf = new(bytes.Buffer)
	cab.saveCallback = callback
	cab.recordEnd = time.Now().Add(futureDuration)

	size := len(cab.buffer)
	if cab.totalWritten < int64(size) {
		// Buffer not fully wrapped yet
		cab.recordBuf.Write(cab.buffer[:cab.writeIdx])
	} else {
		// Buffer fully wrapped
		cab.recordBuf.Write(cab.buffer[cab.writeIdx:])
		cab.recordBuf.Write(cab.buffer[:cab.writeIdx])
	}
}
