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
	listeners    map[uint64]chan []byte
	nextListener uint64
}

// AudioSnapshot is one immutable view of the shared live stream.
type AudioSnapshot struct {
	Data    []byte
	Version int64
}

// NewCircularAudioBuffer creates a new circular buffer.
// For a 128kbps stream, 3 minutes is ~2.88MB. 8MB covers >8 minutes.
func NewCircularAudioBuffer(streamURL string, sizeBytes int) *CircularAudioBuffer {
	return &CircularAudioBuffer{
		buffer:    make([]byte, sizeBytes),
		streamURL: streamURL,
		cancel:    make(chan struct{}),
		listeners: make(map[uint64]chan []byte),
	}
}

// Subscribe receives best-effort copies of incoming stream chunks. A slow
// consumer never blocks the radio buffer; it can reconnect from fresh audio.
func (cab *CircularAudioBuffer) Subscribe(queueSize int) (<-chan []byte, func()) {
	if queueSize < 1 {
		queueSize = 1
	}
	listener := make(chan []byte, queueSize)

	cab.mu.Lock()
	id := cab.nextListener
	cab.nextListener++
	cab.listeners[id] = listener
	cab.mu.Unlock()

	return listener, func() {
		cab.mu.Lock()
		delete(cab.listeners, id)
		cab.mu.Unlock()
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

	for _, listener := range cab.listeners {
		chunk := append([]byte(nil), data...)
		select {
		case listener <- chunk:
		default:
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

// ReadCurrentBuffer returns a copy of the current buffer contents in chronological order.
func (cab *CircularAudioBuffer) ReadCurrentBuffer() []byte {
	return cab.Snapshot().Data
}

// Snapshot returns the stream bytes and a monotonically increasing version.
// Consumers can share the same snapshot instead of copying the rolling buffer each.
func (cab *CircularAudioBuffer) Snapshot() AudioSnapshot {
	cab.mu.Lock()
	defer cab.mu.Unlock()

	out := make([]byte, len(cab.buffer))
	if cab.totalWritten < int64(len(cab.buffer)) {
		copy(out, cab.buffer[:cab.writeIdx])
		return AudioSnapshot{Data: out[:cab.writeIdx], Version: cab.totalWritten}
	}

	copy(out, cab.buffer[cab.writeIdx:])
	copy(out[len(cab.buffer)-cab.writeIdx:], cab.buffer[:cab.writeIdx])
	return AudioSnapshot{Data: out, Version: cab.totalWritten}
}

// TimedItem represents a value attached to a specific timestamp.
type TimedItem[T any] struct {
	Timestamp time.Time `json:"timestamp"`
	Value     T         `json:"value"`
}

// TimeSeriesBuffer is a generic ring-like buffer for discrete events (like transcripts or metadata).
// It retains items up to a specific max age to match the audio ring buffer's time window.
type TimeSeriesBuffer[T any] struct {
	mu     sync.Mutex
	buffer []TimedItem[T]
	maxAge time.Duration
}

// NewTimeSeriesBuffer creates a new buffer that trims items older than maxAge.
func NewTimeSeriesBuffer[T any](maxAge time.Duration) *TimeSeriesBuffer[T] {
	return &TimeSeriesBuffer[T]{
		maxAge: maxAge,
	}
}

// Append adds a new item at the given timestamp and trims old items.
func (b *TimeSeriesBuffer[T]) Append(item T, timestamp time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buffer = append(b.buffer, TimedItem[T]{
		Timestamp: timestamp,
		Value:     item,
	})

	// Trim old items relative to the latest timestamp added
	cutoff := timestamp.Add(-b.maxAge)
	trimIdx := 0
	for i, v := range b.buffer {
		if v.Timestamp.After(cutoff) || v.Timestamp.Equal(cutoff) {
			trimIdx = i
			break
		}
	}
	if trimIdx > 0 {
		n := copy(b.buffer, b.buffer[trimIdx:])
		// Clear remainder for GC
		for i := n; i < len(b.buffer); i++ {
			var zero T
			b.buffer[i] = TimedItem[T]{Value: zero}
		}
		b.buffer = b.buffer[:n]
	}
}

// GetWindow returns a copy of all items that fall within [start, end].
func (b *TimeSeriesBuffer[T]) GetWindow(start, end time.Time) []TimedItem[T] {
	b.mu.Lock()
	defer b.mu.Unlock()

	var result []TimedItem[T]
	for _, v := range b.buffer {
		if (v.Timestamp.Equal(start) || v.Timestamp.After(start)) &&
			(v.Timestamp.Equal(end) || v.Timestamp.Before(end)) {
			result = append(result, v)
		}
	}
	return result
}
