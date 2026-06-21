package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lithammer/dedent"
)

const (
	MaxDailyMatches    = 6
	followProFMKeyword = "follow profm"
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
	if os.Getenv("BYPASS_CAMPAIGN_TIME_CHECKS") == "true" {
		return true
	}

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
	APIURL             string
	PollInterval       time.Duration
	ActiveCampaigns    []Campaign
	TargetPhone        string
	SendVoiceNote      func(senderPhone string, targetPhone string, audioPath string) error
	DisconnectWhatsApp func()
	ConnectWhatsApp    func() error
	StateMgr           *StateManager
	Alerter            Alerter
	AudiosDir          string
	DBMgr              *DBManager
	BaseURL            string

	matchesToday int
	lastCheckDay int
}

func (p *Poller) hasActiveCampaign(now time.Time) bool {
	for _, c := range p.ActiveCampaigns {
		if c.IsActive(now) {
			return true
		}
	}
	return false
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
	_ = p.Alerter.AlertInfo(AlertEvent{
		Title:       "Service Started",
		Message:     "ProFM Jaguare Poller started! Fetching Now Playing...",
		ActionLabel: "View Dashboard",
		ActionURL:   p.BaseURL,
	})
	log.Println("Fetching Now Playing from Pro FM...")
	log.Println(strings.Repeat("-", 40))

	var currentSong SongInfo

	p.StateMgr.Update(func(s *AppState) {
		s.Status = StatusPolling
	})

	// Use a cron-like Ticker instead of an infinite sleep loop
	ticker := time.NewTicker(p.PollInterval)
	defer ticker.Stop()

	// Trigger immediately on start
	p.checkSong(&currentSong, time.Now())

	var isSleeping bool

	// Cron-like polling
	for {
		<-ticker.C
		now := time.Now()

		if p.StateMgr != nil && p.StateMgr.Get().KillSwitchActive {
			continue // If killed, just sleep
		}

		if !p.hasActiveCampaign(now) {
			if !isSleeping {
				log.Println("[INFO] No active campaigns right now. Entering sleep mode (disconnecting WhatsApp and pausing ProFM polling).")
				if p.DisconnectWhatsApp != nil {
					p.DisconnectWhatsApp()
				}
				p.StateMgr.Update(func(s *AppState) {
					s.Status = StatusSleeping
				})
				isSleeping = true
			}
			continue
		}

		if isSleeping {
			log.Println("[INFO] Campaign is now active! Waking up (reconnecting WhatsApp and resuming polling).")
			connectErr := error(nil)
			if p.ConnectWhatsApp != nil {
				connectErr = p.ConnectWhatsApp()
				if connectErr != nil {
					log.Printf("[ERROR] Failed to reconnect WhatsApp: %v\n", connectErr)
				}
			}

			isSleeping = false

			// Only transition to Polling state if there wasn't a connection error.
			// If there was an error, the WhatsApp event handler likely set StatusError or StatusPairingRequired.
			if connectErr == nil {
				p.StateMgr.Update(func(s *AppState) {
					s.Status = StatusPolling
				})
			}
		}

		p.checkSong(&currentSong, now)
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
		log.Printf("[%s] %s - %s", now.Format("15:04:05"), song.Artist, song.Title)

		if p.DBMgr != nil {
			if err := p.DBMgr.LogRadioSong(context.Background(), song.Artist, song.Title, now); err != nil {
				log.Printf("   ⚠️ DB Log Error: %v\n", err)
			}
		}

		// Abort immediately if the bot has been permanently killed
		if p.StateMgr != nil && p.StateMgr.Get().KillSwitchActive {
			log.Printf("   ⛔️ KILL SWITCH ACTIVE! Ignoring all campaign matches for '%s'.", song.Artist)
			return
		}

		// Only check campaigns if we haven't hit the daily limit of matches
		if p.matchesToday < MaxDailyMatches {
			for _, campaign := range p.ActiveCampaigns {
				if campaign.IsActive(now) {
					artistMatch := strings.Contains(strings.ToLower(song.Artist), strings.ToLower(campaign.Artist))
					titleKeywordMatch := strings.Contains(strings.ToLower(song.Title), followProFMKeyword)
					if artistMatch || titleKeywordMatch {
						if p.DBMgr != nil {
							played, err := p.DBMgr.WasSongInLastNPlays(context.Background(), song.Artist, song.Title, 2)
							if err != nil {
								log.Printf("   ⚠️ DB Check Error: %v\n", err)
							} else if played {
								log.Printf("   [INFO] Song '%s - %s' played within the last 2 songs. Skipping duplicate.", song.Artist, song.Title)
								break // break out of campaign loop
							}
						}
						p.matchesToday++
						msg := dedent.Dedent(fmt.Sprintf(`
												🎉 [VEZI BAA ca se aude piesa]
												Artistu: %s

												Piesa: %s

												(Match-ul %d/%d de azi)
													`,
							song.Artist, song.Title, p.matchesToday, MaxDailyMatches))
						log.Println("   " + msg)
						if alertErr := p.Alerter.AlertInfo(AlertEvent{
							Title:       "Campaign Alert",
							Message:     msg,
							ActionLabel: "View Dashboard",
							ActionURL:   p.BaseURL,
						}); alertErr != nil {
							log.Printf("   ⚠️ Alerter warning: %v\n", alertErr)
						}

						p.StateMgr.Update(func(s *AppState) {
							s.Status = StatusCampaignTriggered
						})

						var audioFile string
						var chosenSender string
						for _, conn := range p.StateMgr.Get().Connections {
							// Try to find a connected sender that has audios
							dir := GetAudioDirForPhone(conn.Phone, p.AudiosDir)
							f, err := GetRandomAudio(dir)
							if err == nil {
								chosenSender = conn.Phone
								audioFile = f
								break
							}
						}

						if audioFile == "" {
							p.StateMgr.Update(func(s *AppState) {
								s.Status = StatusAudioExhausted
								s.LastError = "No unused audios available!"
							})
							log.Printf("   ❌ NO UNUSED AUDIO FOUND FOR %s!", song.Artist)
							_ = p.Alerter.AlertCritical(AlertEvent{
								Title:       "AUDIO POOL EXHAUSTED",
								Message:     "Cannot send voice note for " + song.Artist + "\nNo unused audio files found in " + p.AudiosDir,
								ActionLabel: "View Dashboard",
								ActionURL:   p.BaseURL,
							})
							break
						}

						p.StateMgr.Update(func(s *AppState) {
							s.Status = StatusSendingAudio
						})

						// Trigger actual submission (WhatsApp Voice note)
						log.Println("   Sending WhatsApp voice note using: " + audioFile)
						err = p.SendVoiceNote(chosenSender, p.TargetPhone, audioFile)
						if err != nil {
							log.Printf("   ❌ Error sending voice note: %v\n", err)
							p.StateMgr.Update(func(s *AppState) {
								s.Status = StatusError
								s.LastError = fmt.Sprintf("Voice note failed: %v", err)
							})
						} else {
							// Success!
							if p.DBMgr != nil {
								_ = p.DBMgr.RecordSongPlay(context.Background(), song.Artist, song.Title, now)
							}
							_ = MarkAudioUsed(audioFile)
							msg := dedent.Dedent(fmt.Sprintf(`
								S-a trimis vocalu pe Wapp la nr: %s
								Artist: %s
								Piesa: %s
								Fisieru audio trimis: %s
								`, p.TargetPhone, song.Artist, song.Title, audioFile))
							log.Println("✅", msg)
							_ = p.Alerter.AlertSuccess(AlertEvent{
								Title:       "Voice Note Sent",
								Message:     msg,
								ActionLabel: "View Dashboard",
								ActionURL:   p.BaseURL,
							})
							unused, used := GetTotalAudioStats(p.StateMgr.Get().Connections, p.AudiosDir)
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
			log.Printf("   [INFO] Daily limit of %d matches reached. Ignoring further campaign matches for today.", MaxDailyMatches)
		}

		*currentSong = song

		p.StateMgr.Update(func(s *AppState) {
			s.CurrentSong = song.Artist + " - " + song.Title
		})
	}

	// Always update audio stats on each check to keep UI fresh
	unused, used := GetTotalAudioStats(p.StateMgr.Get().Connections, p.AudiosDir)
	p.StateMgr.Update(func(s *AppState) {
		s.UnusedAudios = unused
		s.UsedAudios = used
	})
}
