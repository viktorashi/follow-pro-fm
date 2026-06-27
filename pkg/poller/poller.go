package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/lithammer/dedent"
)

const (
	MaxDailyMatches      = 6
	followProFMKeyword   = "follow profm"
	dashcamAfterDuration = 4 * time.Minute
	fingerprintTailBytes = 768 * 1024
)

var bucharestLocation = loadBucharestLocation()

func loadBucharestLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		// Fallback keeps the campaign aligned to Romanian standard time if tzdata
		// is unexpectedly unavailable.
		return time.FixedZone("Europe/Bucharest", 2*60*60)
	}
	return loc
}

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
	if shouldBypassCampaignTimeChecks() {
		return true
	}

	now = now.In(bucharestLocation)

	// Global Rule 1: Monday to Friday only
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false
	}

	// Global Rule 2: Between 07:00 and 20:00 (up to 19:59:59)
	if now.Hour() < 7 || now.Hour() >= 20 {
		return false
	}

	layout := "02-01-2006"
	start, err1 := time.ParseInLocation(layout, c.StartDate, bucharestLocation)
	end, err2 := time.ParseInLocation(layout, c.EndDate, bucharestLocation)

	if err1 != nil || err2 != nil {
		return false
	}

	// End date includes the entire day
	end = end.Add(24*time.Hour - time.Second)

	return now.After(start) && now.Before(end)
}

func shouldBypassCampaignTimeChecks() bool {
	return os.Getenv("BYPASS_CAMPAIGN_TIME_CHECKS") == "true"
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
	SignaturesDir      string
	AudioBuffer        *CircularAudioBuffer
	DBMgr              *DBManager
	BaseURL            string

	matchesToday int
	lastCheckDay int

	ignoredTriggerMu sync.Mutex
	ignoredTrigger   fingerprintTrigger
	sendMu           sync.Mutex
}

type fingerprintTrigger struct {
	signatureName string
	artist        string
	title         string
}

const (
	triggerSourceMetadata    = "metadata"
	triggerSourceFingerprint = "fingerprint"
)

func normalizeTriggerValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func triggerValuesMatch(left, right string) bool {
	left = normalizeTriggerValue(left)
	right = normalizeTriggerValue(right)
	if left == "" || right == "" {
		return false
	}
	return strings.Contains(left, right) || strings.Contains(right, left)
}

func parseFingerprintTrigger(signatureName string) fingerprintTrigger {
	base := strings.TrimSuffix(filepath.Base(signatureName), filepath.Ext(signatureName))
	parts := strings.SplitN(base, " - ", 2)
	trigger := fingerprintTrigger{signatureName: signatureName, artist: base, title: "Unknown"}
	if len(parts) == 2 {
		trigger.artist = parts[0]
		trigger.title = parts[1]
	}
	return trigger
}

func isUnknownSongValue(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "" || strings.HasPrefix(value, "unknown")
}

func (p *Poller) resolveFingerprintSong(trigger fingerprintTrigger) SongInfo {
	song := SongInfo{Artist: trigger.artist, Title: trigger.title}

	nowPlaying, err := p.getNowPlaying()
	if err != nil {
		return song
	}
	if !isUnknownSongValue(nowPlaying.Artist) {
		song.Artist = nowPlaying.Artist
	}
	if !isUnknownSongValue(nowPlaying.Title) {
		song.Title = nowPlaying.Title
	}
	return song
}

func (p *Poller) saveUnreviewedChunkForReview(song SongInfo, data []byte) {
	filename := fmt.Sprintf("%s - %s - %d.mp3", song.Artist, song.Title, time.Now().Unix())
	saved, matchedName, err := SaveUnreviewedChunkIfDistinct(
		data,
		filepath.Join(p.SignaturesDir, "unreviewed"),
		filepath.Join(p.SignaturesDir, "canonical"),
		filename,
	)
	if err != nil {
		log.Printf("   ⚠️ Failed to save unreviewed chunk %q: %v", filename, err)
		return
	}
	if !saved {
		log.Printf("   [SIGNATURE REVIEW] Skipped saving %q because it matches canonical signature %q", filename, matchedName)
		return
	}

	log.Printf("   [SIGNATURE REVIEW] Saved unreviewed chunk %q for manual review", filename)
	_ = p.Alerter.AlertInfo(AlertEvent{
		Title:       "Intro Chunk Needs Review",
		Message:     fmt.Sprintf("Saved new unreviewed intro chunk for manual review\nArtist: %s\nPiesa: %s\nFile: %s", song.Artist, song.Title, filename),
		ActionLabel: "View Dashboard",
		ActionURL:   p.BaseURL,
	})
}

func (p *Poller) consumeIgnoredMetadataTrigger(song SongInfo) bool {
	p.ignoredTriggerMu.Lock()
	defer p.ignoredTriggerMu.Unlock()

	if p.ignoredTrigger.signatureName == "" {
		return false
	}

	ignored := triggerValuesMatch(p.ignoredTrigger.artist, song.Artist) || triggerValuesMatch(p.ignoredTrigger.title, song.Title)
	if ignored {
		p.ignoredTrigger = fingerprintTrigger{}
	}
	return ignored
}

func (p *Poller) matchingCampaignArtist(now time.Time, song SongInfo) (string, bool) {
	titleLower := strings.ToLower(song.Title)
	artistLower := strings.ToLower(song.Artist)

	for _, campaign := range p.ActiveCampaigns {
		if !campaign.IsActive(now) {
			continue
		}

		campaignArtistLower := strings.ToLower(campaign.Artist)
		if strings.Contains(artistLower, campaignArtistLower) || strings.Contains(titleLower, followProFMKeyword) {
			return campaign.Artist, true
		}
	}

	return "", false
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

	if p.AudioBuffer != nil {
		p.AudioBuffer.Start()
		go p.fingerprintLoop()
	}

	shouldPoll := p.prepareStartState()

	// Use a cron-like Ticker instead of an infinite sleep loop
	ticker := time.NewTicker(p.PollInterval)
	defer ticker.Stop()

	// Trigger immediately on start
	if shouldPoll {
		p.checkSong(&currentSong, time.Now())
	}

	var isSleeping bool

	// Cron-like polling
	for {
		<-ticker.C
		now := time.Now()

		if p.StateMgr != nil && p.StateMgr.Get().KillSwitchActive {
			p.StateMgr.Update(func(s *AppState) {
				s.Status = StatusKilled
			})
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
					s.WhatsAppConnected = false
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

func (p *Poller) prepareStartState() bool {
	if p.StateMgr == nil {
		return true
	}

	if p.StateMgr.Get().KillSwitchActive {
		p.StateMgr.Update(func(s *AppState) {
			s.Status = StatusKilled
		})
		return false
	}

	p.StateMgr.Update(func(s *AppState) {
		s.Status = StatusPolling
	})
	return true
}

func (p *Poller) checkSong(currentSong *SongInfo, now time.Time) {
	p.resetDailyMatchesIfNeeded(now)

	song, err := p.getNowPlaying()
	if err != nil {
		log.Printf("Error fetching data: %v\n", err)
		return
	}

	if song != *currentSong {
		log.Printf("[%s] %s - %s", now.Format("15:04:05"), song.Artist, song.Title)

		currentRadioLogID := int64(0)
		if p.DBMgr != nil {
			radioLogID, err := p.DBMgr.LogRadioSong(context.Background(), song.Artist, song.Title, now)
			if err != nil {
				log.Printf("   ⚠️ DB Log Error: %v\n", err)
			} else {
				currentRadioLogID = radioLogID
			}
		}

		// Abort immediately if the bot has been permanently killed
		if p.StateMgr != nil && p.StateMgr.Get().KillSwitchActive {
			log.Printf("   ⛔️ KILL SWITCH ACTIVE! Ignoring all campaign matches for '%s'.", song.Artist)
			return
		}

		// Only check campaigns if we haven't hit the daily limit of matches
		if p.matchesToday < MaxDailyMatches {
			campaignArtist, matchesCampaign := p.matchingCampaignArtist(now, song)
			if matchesCampaign {
				if p.consumeIgnoredMetadataTrigger(song) {
					log.Printf("   [INFO] Ignoring metadata trigger for '%s - %s' because it was already triggered by fingerprint.", song.Artist, song.Title)
				} else if !p.wasSongPlayedRecently(song.Artist, song.Title) && p.canSendCampaignArtist(campaignArtist, currentRadioLogID) {
					matchIndex, selected := p.claimScheduledMatch(now, song.Artist, song.Title)
					if selected {
						p.doTriggerVoiceNote(triggerSourceMetadata, campaignArtist, song.Artist, song.Title, now, matchIndex, currentRadioLogID)

						if p.StateMgr != nil && p.StateMgr.Get().GatheringSignatures && p.AudioBuffer != nil {
							// Metadata-only detections extend the preserved pre-roll by 4 minutes.
							p.AudioBuffer.Trigger(dashcamAfterDuration, func(data []byte) {
								p.saveUnreviewedChunkForReview(song, data)
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

func (p *Poller) resetDailyMatchesIfNeeded(now time.Time) {
	if now.YearDay() != p.lastCheckDay {
		p.matchesToday = 0
		p.lastCheckDay = now.YearDay()
	}
}

func (p *Poller) wasSongPlayedRecently(artist, title string) bool {
	if p.DBMgr == nil {
		return false
	}

	played, err := p.DBMgr.WasSongInLastNPlays(context.Background(), artist, title, 2)
	if err != nil {
		log.Printf("   ⚠️ DB Check Error: %v\n", err)
		return false
	}
	if played {
		log.Printf("   [INFO] Song '%s - %s' played within the last 2 songs. Skipping duplicate.", artist, title)
	}
	return played
}

func (p *Poller) claimScheduledMatch(now time.Time, artist, title string) (int, bool) {
	p.resetDailyMatchesIfNeeded(now)
	if p.matchesToday >= MaxDailyMatches {
		log.Printf("   [INFO] Daily limit of %d matches reached. Ignoring further campaign matches for today.", MaxDailyMatches)
		return 0, false
	}

	p.matchesToday++
	matchIndex := p.matchesToday

	selected, err := IsMatchSelectedToday(p.DBMgr, now, matchIndex)
	if err != nil {
		log.Printf("   ⚠️ Failed to evaluate RNG schedule for %s (%s - %s): %v", now.Format("2006-01-02"), artist, title, err)
		return matchIndex, false
	}
	if !selected {
		log.Printf("   [RNG] Match #%d for '%s - %s' is not scheduled today. Skipping send.", matchIndex, artist, title)
		return matchIndex, false
	}

	return matchIndex, true
}

func (p *Poller) canSendCampaignArtist(campaignArtist string, currentRadioLogID int64) bool {
	if p.DBMgr == nil {
		return true
	}

	if currentRadioLogID == 0 {
		latestRadioLogID, err := p.DBMgr.GetLatestRadioLogID(context.Background())
		if err != nil {
			log.Printf("   ⚠️ Failed to load latest radio log id for %q: %v", campaignArtist, err)
			return false
		}
		currentRadioLogID = latestRadioLogID + 1
	}

	allowed, err := p.DBMgr.CanSendCampaignArtist(context.Background(), campaignArtist, currentRadioLogID)
	if err != nil {
		log.Printf("   ⚠️ Failed to enforce campaign resend gate for %q: %v", campaignArtist, err)
		return false
	}
	if !allowed {
		log.Printf("   [INFO] Skipping resend for campaign artist %q until a different artist is logged in between.", campaignArtist)
	}
	return allowed
}

func (p *Poller) doTriggerVoiceNote(triggerSource, campaignArtist, artist, title string, now time.Time, matchIndex int, currentRadioLogID int64) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	if !p.canSendCampaignArtist(campaignArtist, currentRadioLogID) {
		return
	}

	msg := dedent.Dedent(fmt.Sprintf(`
							🎉 [VEZI BAA ca se aude piesa]
							Trigger: %s
							Artistu: %s

							Piesa: %s

							(Match-ul %d/%d de azi)
								`,
		triggerSource, artist, title, matchIndex, MaxDailyMatches))
	log.Println("   " + msg)

	p.StateMgr.Update(func(s *AppState) {
		s.Status = StatusCampaignTriggered
	})

	var audioFile string
	var audioHash string
	var chosenSender string
	for _, conn := range p.StateMgr.Get().Connections {
		// Try to find a connected sender that has audios
		dir := GetAudioDirForPhone(conn.Phone, p.AudiosDir)
		f, hash, err := GetRandomAvailableAudio(dir, func(contentHash string) (bool, error) {
			if p.DBMgr == nil {
				return false, nil
			}
			return p.DBMgr.IsAudioHashUsed(context.Background(), contentHash)
		})
		if err == nil {
			chosenSender = conn.Phone
			audioFile = f
			audioHash = hash
			break
		}
	}

	if audioFile == "" {
		p.StateMgr.Update(func(s *AppState) {
			s.Status = StatusAudioExhausted
			s.LastError = "No unused audios available!"
		})
		log.Printf("   ❌ NO UNUSED AUDIO FOUND FOR %s!", artist)
		_ = p.Alerter.AlertCritical(AlertEvent{
			Title:       "AUDIO POOL EXHAUSTED",
			Message:     "Cannot send voice note for " + artist + "\nNo unused audio files found in " + p.AudiosDir,
			ActionLabel: "View Dashboard",
			ActionURL:   p.BaseURL,
		})
		return
	}

	p.StateMgr.Update(func(s *AppState) {
		s.Status = StatusSendingAudio
	})

	log.Printf("   Sending WhatsApp voice note using: %s (trigger=%s)", audioFile, triggerSource)
	err := p.SendVoiceNote(chosenSender, p.TargetPhone, audioFile)
	if err != nil {
		log.Printf("   ❌ Error sending voice note: %v\n", err)
		p.StateMgr.Update(func(s *AppState) {
			s.Status = StatusError
			s.LastError = fmt.Sprintf("Voice note failed: %v", err)
		})
		_ = p.Alerter.AlertCritical(AlertEvent{
			Title:       "Voice Note Failed",
			Message:     fmt.Sprintf("Could not send voice note to %s\nTrigger: %s\nArtist: %s\nPiesa: %s\nEroare: %v", p.TargetPhone, triggerSource, artist, title, err),
			ActionLabel: "View Dashboard",
			ActionURL:   p.BaseURL,
		})
	} else {
		if p.DBMgr != nil {
			if err := p.DBMgr.RecordSuccessfulSend(context.Background(), campaignArtist, artist, title, audioHash, currentRadioLogID, now); err != nil {
				log.Printf("   ❌ Voice note sent but persistence update failed: %v\n", err)
				p.StateMgr.Update(func(s *AppState) {
					s.Status = StatusError
					s.LastError = fmt.Sprintf("Voice note sent but persistence failed: %v", err)
				})
				return
			}
		}
		if err := MarkAudioUsedByHash(p.AudiosDir, audioFile, audioHash); err != nil {
			log.Printf("   ⚠️ Voice note sent but audio file rotation failed: %v\n", err)
		}
		msg := dedent.Dedent(fmt.Sprintf(`
			S-a trimis vocalu pe Wapp la nr: %s
			Trigger: %s
			Artist: %s
			Piesa: %s
			Fisieru audio trimis: %s
			`, p.TargetPhone, triggerSource, artist, title, audioFile))
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

func (p *Poller) fingerprintLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	canonicalDir := filepath.Join(p.SignaturesDir, "canonical")

	for {
		<-ticker.C
		now := time.Now()
		if p.StateMgr != nil && p.StateMgr.Get().KillSwitchActive {
			continue
		}
		if !p.hasActiveCampaign(now) {
			continue
		}
		p.resetDailyMatchesIfNeeded(now)
		if p.matchesToday >= MaxDailyMatches {
			continue
		}
		sigs, err := GetCanonicalSignatures(canonicalDir)
		if err != nil || len(sigs) == 0 {
			continue
		}
		buf := p.AudioBuffer.ReadCurrentBuffer()
		if len(buf) == 0 {
			continue
		}
		if len(buf) > fingerprintTailBytes {
			buf = buf[len(buf)-fingerprintTailBytes:]
		}

		matched, name, err := findMatchingCanonicalSignature(buf, defaultFingerprintFormat, canonicalDir)
		if err != nil {
			log.Printf("   ⚠️ Fingerprint matching failed: %v", err)
			continue
		}
		if !matched {
			continue
		}

		trigger := parseFingerprintTrigger(name)
		song := p.resolveFingerprintSong(trigger)
		campaignArtist, matchesCampaign := p.matchingCampaignArtist(now, song)
		if !matchesCampaign {
			continue
		}

		p.ignoredTriggerMu.Lock()
		if p.ignoredTrigger.signatureName != name {
			p.ignoredTrigger = trigger
			p.ignoredTriggerMu.Unlock()

			log.Printf("   [FINGERPRINT MATCH] Matched signature: %s -> %s - %s", name, song.Artist, song.Title)
			if p.wasSongPlayedRecently(song.Artist, song.Title) {
				continue
			}

			currentRadioLogID := int64(0)
			if p.DBMgr != nil {
				latestRadioLogID, err := p.DBMgr.GetLatestRadioLogID(context.Background())
				if err != nil {
					log.Printf("   ⚠️ Failed to load latest radio log id for fingerprint match %q: %v", name, err)
					continue
				}
				currentRadioLogID = latestRadioLogID + 1
			}

			if !p.canSendCampaignArtist(campaignArtist, currentRadioLogID) {
				continue
			}

			matchIndex, selected := p.claimScheduledMatch(now, song.Artist, song.Title)
			if !selected {
				continue
			}

			p.doTriggerVoiceNote(triggerSourceFingerprint, campaignArtist, song.Artist, song.Title, now, matchIndex, currentRadioLogID)
		} else {
			p.ignoredTriggerMu.Unlock()
		}
	}
}
