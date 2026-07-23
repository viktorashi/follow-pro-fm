package main

import (
	"context"
	"fmt"
	"os"

	"github.com/coder/websocket"
)

func main() {
	wsURL := os.Getenv("TRANSCRIPTION_WS_URL")
	if wsURL == "" {
		wsURL = "ws://localhost:8000/v1/audio/transcriptions"
	}
	_, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println("Success!")
	}
}
