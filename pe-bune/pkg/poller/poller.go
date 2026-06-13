package poller

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type EPGData struct {
	Data struct {
		Epg struct {
			Title    string `json:"playerExtendedSongTitle"`
			Subtitle string `json:"playerExtendedSongSubtitle"`
		} `json:"epg"`
	} `json:"data"`
}

type SongInfo struct {
	Artist string
	Title  string
}

// Campaign represents a multi-week date period where a specific artist is targeted
type Campaign struct {
	StartDate string // Format: "02-01-2006"
	EndDate   string // Format: "02-01-2006"
	Artist    string
}

// IsActive checks if the current time falls within the campaign date period
func (c Campaign) IsActive(now time.Time) bool {
	// Global Rule 1: Monday to Friday only
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false
	}

	// Global Rule 2: Between 07:00 and 20:00 (up to 19:59:59)
	if now.Hour() < 7 || now.Hour() >= 20 {
		return false
	}

	layout := "02-01-2006"
	start, err1 := time.ParseInLocation(layout, c.StartDate, now.Location())
	end, err2 := time.ParseInLocation(layout, c.EndDate, now.Location())

	if err1 != nil || err2 != nil {
		return false
	}

	// End date includes the entire day
	end = end.Add(24*time.Hour - time.Second)

	return now.After(start) && now.Before(end)
}

type Poller struct {
	APIURL          string
	PollInterval    time.Duration
	ActiveCampaigns []Campaign
	TargetPhone     string
	SendVoiceNote   func(phone string, audioPath string) error
	StateMgr        *StateManager
	Alerter         Alerter
	AudiosDir       string

	matchesToday int
	lastCheckDay int
}

func (p *Poller) getNowPlaying() (SongInfo, error) {
	req, err := http.NewRequest("GET", p.APIURL, nil)
	if err != nil {
		return SongInfo{}, err
	}

	// Be a good citizen with the user agent
	req.Header.Set("User-Agent", "ProFMNowPlayingGoClient/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return SongInfo{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return SongInfo{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SongInfo{}, err
	}

	var data EPGData
	if err := json.Unmarshal(body, &data); err != nil {
		return SongInfo{}, err
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

	return SongInfo{Artist: artist, Title: song}, nil
}

func (p *Poller) Start() {
	_ = p.Alerter.AlertInfo("ProFM Jaguare Poller started! Fetching Now Playing...")
	fmt.Println("Fetching Now Playing from Pro FM...")
	fmt.Println(strings.Repeat("-", 40))

	var currentSong SongInfo

	p.StateMgr.Update(func(s *AppState) {
		s.Status = StatusPolling
	})

	// Use a cron-like Ticker instead of an infinite sleep loop
	ticker := time.NewTicker(p.PollInterval)
	defer ticker.Stop()

	// Trigger immediately on start
	p.checkSong(&currentSong, time.Now())

	// Cron-like polling
	for {
		<-ticker.C
		p.checkSong(&currentSong, time.Now())
	}
}

func (p *Poller) checkSong(currentSong *SongInfo, now time.Time) {
	// Reset daily matches counter on a new day
	if now.YearDay() != p.lastCheckDay {
		p.matchesToday = 0
		p.lastCheckDay = now.YearDay()
	}

	song, err := p.getNowPlaying()
	if err != nil {
		log.Printf("Error fetching data: %v\n", err)
		return
	}

	if song != *currentSong {
		fmt.Printf("[%s] %s - %s\n", now.Format("15:04:05"), song.Artist, song.Title)

		// Only check campaigns if we haven't hit the daily limit of 6
		if p.matchesToday < 6 {
			for _, campaign := range p.ActiveCampaigns {
				if campaign.IsActive(now) {
					if strings.Contains(strings.ToLower(song.Artist), strings.ToLower(campaign.Artist)) {
						p.matchesToday++
						msg := fmt.Sprintf("🎉 [CAMPAIGN ALERT] %s is playing! (Match %d/6 for today)", song.Artist, p.matchesToday)
						fmt.Println("   " + msg)
						_ = p.Alerter.AlertInfo(msg)

						p.StateMgr.Update(func(s *AppState) {
							s.Status = StatusCampaignTriggered
						})

						audioFile, err := GetRandomAudio(p.AudiosDir)
						if err != nil {
							p.StateMgr.Update(func(s *AppState) {
								s.Status = StatusAudioExhausted
								s.LastError = "No unused audios available!"
							})
							_ = p.Alerter.AlertCritical("AUDIO POOL EXHAUSTED! Cannot send voice note for " + song.Artist)
							break
						}

						p.StateMgr.Update(func(s *AppState) {
							s.Status = StatusSendingAudio
						})

						// Trigger actual submission (WhatsApp Voice note)
						fmt.Println("   Sending WhatsApp voice note using: " + audioFile)
						err = p.SendVoiceNote(p.TargetPhone, audioFile)
						if err != nil {
							log.Printf("   ❌ Error sending voice note: %v\n", err)
							p.StateMgr.Update(func(s *AppState) {
								s.Status = StatusError
								s.LastError = fmt.Sprintf("Voice note failed: %v", err)
							})
						} else {
							// Success!
							_ = MarkAudioUsed(audioFile)
							_ = p.Alerter.AlertSuccess("Voice note sent successfully for " + song.Artist)
							unused, used := GetAudioStats(p.AudiosDir)
							p.StateMgr.Update(func(s *AppState) {
								s.Status = StatusPolling
								s.LastError = ""
								s.LastVoiceNoteSentAt = time.Now()
								s.UnusedAudios = unused
								s.UsedAudios = used
							})
						}
					}
				}
			}
		} else {
			fmt.Println("   [INFO] Daily limit of 6 matches reached. Ignoring further campaign matches for today.")
		}

		*currentSong = song
		
		p.StateMgr.Update(func(s *AppState) {
			s.CurrentSong = song.Artist + " - " + song.Title
		})
	}

	// Always update audio stats on each check to keep UI fresh
	unused, used := GetAudioStats(p.AudiosDir)
	p.StateMgr.Update(func(s *AppState) {
		s.UnusedAudios = unused
		s.UsedAudios = used
	})
}
