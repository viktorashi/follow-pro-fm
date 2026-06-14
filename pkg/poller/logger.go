package poller

import (
	"bytes"
	"io"
	"os"
	"sync"
)

const LogBufferSize = 100

// SSELogWriter wraps an io.Writer (usually os.Stdout) and broadcasts written lines to SSE.
type SSELogWriter struct {
	mu          sync.Mutex
	dest        io.Writer
	broadcaster *SSEBroadcaster
	buffer      [][]byte // Circular buffer of recent log lines
	bufIndex    int
	hasLogsSubs bool // True if there's at least one subscriber listening to logs
	subsCount   int
}

func NewSSELogWriter(dest io.Writer, broadcaster *SSEBroadcaster) *SSELogWriter {
	if dest == nil {
		dest = os.Stdout
	}
	return &SSELogWriter{
		dest:        dest,
		broadcaster: broadcaster,
		buffer:      make([][]byte, 0, LogBufferSize),
	}
}

func (w *SSELogWriter) AddSubscriber() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.subsCount++
	w.hasLogsSubs = w.subsCount > 0
}

func (w *SSELogWriter) RemoveSubscriber() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.subsCount--
	w.hasLogsSubs = w.subsCount > 0
}

// GetRecentLogs returns the recent logs in chronological order
func (w *SSELogWriter) GetRecentLogs() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.buffer) == 0 {
		return nil
	}

	result := make([][]byte, len(w.buffer))
	for i := 0; i < len(w.buffer); i++ {
		idx := (w.bufIndex + i) % len(w.buffer)
		result[i] = w.buffer[idx]
	}
	return result
}

func (w *SSELogWriter) Write(p []byte) (n int, err error) {
	// 1. Write to actual destination (terminal)
	n, err = w.dest.Write(p)

	// 2. Process for SSE
	w.mu.Lock()
	hasSubs := w.hasLogsSubs
	if !hasSubs {
		w.mu.Unlock()
		return n, err
	}

	// Split into lines
	lines := bytes.Split(p, []byte("\n"))
	for i, line := range lines {
		// Ignore the last empty line from a trailing newline
		if i == len(lines)-1 && len(line) == 0 {
			continue
		}

		// Copy line to avoid retaining large backing array
		lineCopy := make([]byte, len(line))
		copy(lineCopy, line)

		// Add to circular buffer
		if len(w.buffer) < LogBufferSize {
			w.buffer = append(w.buffer, lineCopy)
		} else {
			w.buffer[w.bufIndex] = lineCopy
			w.bufIndex = (w.bufIndex + 1) % LogBufferSize
		}

		// Broadcast
		w.broadcaster.Broadcast("log", lineCopy)
	}
	w.mu.Unlock()

	return n, err
}
