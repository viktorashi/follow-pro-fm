package main

import (
	"context"
	"fmt"
	"github.com/coder/websocket"
)

func main() {
	_, _, err := websocket.Dial(context.Background(), "ws://pro-fm-whisper.internal:8000/v1/audio/transcriptions", nil)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	} else {
		fmt.Println("Success!")
	}
}
