package poller

import (
	"context"
	"io"
	"log"
	"os/exec"
)

// PCMConverter turns the MP3 radio stream into the PCM format accepted by the
// Whisper WebSocket endpoint.
type PCMConverter struct {
	input  <-chan []byte
	output chan<- []byte
}

func NewPCMConverter(input <-chan []byte, output chan<- []byte) *PCMConverter {
	return &PCMConverter{input: input, output: output}
}

func (c *PCMConverter) Start(ctx context.Context) {
	go c.run(ctx)
}

func (c *PCMConverter) run(ctx context.Context) {
	ffmpegPath, err := ffmpegBinaryPath()
	if err != nil {
		log.Printf("   ⚠️ PCM converter ffmpeg: %v", err)
		return
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, "-nostdin", "-loglevel", "error", "-i", "pipe:0", "-ac", "1", "-ar", "16000", "-f", "s16le", "pipe:1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("   ⚠️ PCM converter stdin: %v", err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("   ⚠️ PCM converter stdout: %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("   ⚠️ PCM converter start: %v", err)
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for chunk := range c.input {
			if _, err := stdin.Write(chunk); err != nil {
				return
			}
		}
		_ = stdin.Close()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			select {
			case c.output <- chunk:
			default:
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("   ⚠️ PCM converter read: %v", err)
			}
			break
		}
	}
	<-done
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		log.Printf("   ⚠️ PCM converter stopped: %v", err)
	}
}
