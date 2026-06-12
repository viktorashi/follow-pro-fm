package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const apiUrl = "https://api.profm.ro/api/v1/radios/article/2918?appVersion=1.0.0&platform=android"

type EPGData struct {
	Data struct {
		Epg struct {
			Title    string `json:"playerExtendedSongTitle"`
			Subtitle string `json:"playerExtendedSongSubtitle"`
		} `json:"epg"`
	} `json:"data"`
}

func getNowPlaying() (string, error) {
	req, err := http.NewRequest("GET", apiUrl, nil)
	if err != nil {
		return "", err
	}

	// Be a good citizen with the user agent
	req.Header.Set("User-Agent", "ProFMNowPlayingGoClient/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var data EPGData
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}

	artist := data.Data.Epg.Title
	if artist == "" {
		artist = "Unknown Artist"
	}

	song := data.Data.Epg.Subtitle
	if song == "" {
		song = "Unknown Song"
	}

	// Clean up "2000 - LASA-MA PAPA LA MARE"
	if strings.Contains(song, " - ") {
		parts := strings.SplitN(song, " - ", 2)
		// Check if the first part is purely digits (a year)
		isYear := true
		for _, ch := range parts[0] {
			if ch < '0' || ch > '9' {
				isYear = false
				break
			}
		}
		if isYear {
			song = parts[1]
		}
	}

	return fmt.Sprintf("%s - %s", artist, song), nil
}

func main() {
	fmt.Println("Fetching Now Playing from Pro FM...")
	fmt.Println(strings.Repeat("-", 40))

	var currentSong string

	for {
		song, err := getNowPlaying()
		if err != nil {
			log.Printf("Error fetching data: %v\n", err)
		} else if song != currentSong {
			fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), song)
			currentSong = song
		}

		time.Sleep(10 * time.Second)
	}
}
