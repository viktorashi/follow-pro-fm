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
	"slices"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/lithammer/dedent"
)

const (
	MaxDailyMatches      = 6
	followProFMKeyword   = "follow profm"
	dashcamAfterDuration = 2 * time.Minute
	fingerprintTailBytes = 768 * 1024
	contestCaptureWindow = 2 * time.Second
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
	Phrases   []string // Spoken contest phrases accepted for this campaign.
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
	APIURL                       string
	PollInterval                 time.Duration
	ActiveCampaigns              []Campaign
	TargetPhone                  string
	SendVoiceNote                func(senderPhone string, targetPhone string, audioPath string) error
	DisconnectWhatsApp           func()
	ConnectWhatsApp              func() error
	StateMgr                     *StateManager
	Alerter                      Alerter
	AudiosDir                    string
	SignaturesDir                string
	AudioBuffer                  *CircularAudioBuffer
	DBMgr                        *DBManager
	BaseURL                      string
	ContestCheckCooldown         time.Duration
	Transcribe                   func(context.Context, []byte) (string, error)
	StreamingTranscriptionActive func() bool

	matchesToday int
	lastCheckDay int

	ignoredTriggerMu sync.Mutex
	ignoredTrigger   fingerprintTrigger
	sendMu           sync.Mutex
	checkerMu        sync.Mutex
	checker          *ContestCheckCoordinator
	captureMu        sync.Mutex
	capture          *contestCapture
}

// contestCapture is the one shared observation of a live audio window. A tag
// can only exist on this captured evidence, and every tag is consumed by a
// checker before it can affect sending or signature review.
type contestCapture struct {
	Audio      AudioSnapshot
	CapturedAt time.Time
	Metadata   SongInfo
	Tags       []contestTag
	Transcript string
}

type contestTag struct {
	Source         string
	CampaignArtist string
	Phrase         string
	SignatureName  string
}

type fingerprintTrigger struct {
	signatureName string
	artist        string
	title         string
}

type metadataContestChecker struct {
	poller      *Poller
	coordinator *ContestCheckCoordinator
	currentSong *SongInfo
}

func (c *metadataContestChecker) Check(now time.Time) {
	c.poller.checkSongWithCoordinator(c.currentSong, now, c.coordinator)
}

type fingerprintContestChecker struct {
	poller      *Poller
	coordinator *ContestCheckCoordinator
}

func (c *fingerprintContestChecker) Check(now time.Time) {
	c.poller.checkFingerprintWithCoordinator(now, c.coordinator)
}

type transcriptionContestChecker struct {
	poller      *Poller
	coordinator *ContestCheckCoordinator
	lastVersion int64
}

func (c *transcriptionContestChecker) Check(now time.Time) {
	c.poller.checkTranscriptionWithCoordinator(now, c.coordinator, c)
}

var (
	_ ContestChecker = (*metadataContestChecker)(nil)
	_ ContestChecker = (*fingerprintContestChecker)(nil)
	_ ContestChecker = (*transcriptionContestChecker)(nil)
)

const (
	triggerSourceMetadata      = "metadata"
	triggerSourceFingerprint   = "fingerprint"
	triggerSourceTranscription = "transcription"
)

func (p *Poller) captureContestAudio(now time.Time, metadata SongInfo) *contestCapture {
	if p.AudioBuffer == nil {
		return nil
	}
	snapshot := p.AudioBuffer.Snapshot()
	if len(snapshot.Data) == 0 {
		return nil
	}
	p.captureMu.Lock()
	defer p.captureMu.Unlock()
	if p.capture == nil || now.Sub(p.capture.CapturedAt) >= contestCaptureWindow {
		p.capture = &contestCapture{Audio: snapshot, CapturedAt: now, Metadata: metadata}
	} else if metadata != (SongInfo{}) {
		p.capture.Metadata = metadata
	}
	return p.capture
}

func (p *Poller) tagCapture(capture *contestCapture, tag contestTag) {
	if capture == nil {
		return
	}
	p.captureMu.Lock()
	defer p.captureMu.Unlock()
	if p.capture == capture {
		p.capture.Tags = append(p.capture.Tags, tag)
	}
}

func captureHasTag(capture *contestCapture, tag contestTag) bool {
	return slices.ContainsFunc(capture.Tags, func(candidate contestTag) bool {
		return candidate == tag
	})
}

func normalizeTriggerValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

// CanCheckContest lets every checker avoid repeated work during a claimed window.
func (p *Poller) CanCheckContest(now time.Time) bool {
	return p.contestCheckCoordinator().CanCheck(now)
}

// ClaimContestWindow lets the first checker that finds a campaign candidate
// put all checkers to sleep for the shared cooldown.
func (p *Poller) ClaimContestWindow(now time.Time, source string) bool {
	return p.contestCheckCoordinator().Claim(now, source)
}

func (p *Poller) contestCheckCoordinator() *ContestCheckCoordinator {
	p.checkerMu.Lock()
	defer p.checkerMu.Unlock()
	if p.checker == nil {
		p.checker = NewContestCheckCoordinator(p.ContestCheckCooldown)
	}
	return p.checker
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

func (p *Poller) saveUnreviewedChunkForReview(song SongInfo, data []byte, transcript string) {
	recordedAt := time.Now()
	filename := fmt.Sprintf("%s - %s - %d.mp3", song.Artist, song.Title, recordedAt.Unix())
	unreviewedDir := filepath.Join(p.SignaturesDir, "unreviewed")
	canonicalDir := filepath.Join(p.SignaturesDir, "canonical")

	var allowed map[string]struct{}
	matchedName := ""
	if p.DBMgr != nil {
		loaded, err := allowedCanonicalSignatureNames(context.Background(), p.DBMgr, p.ActiveCampaigns, canonicalDir, recordedAt)
		if err != nil {
			log.Printf("   ⚠️ Failed to load campaign-bound canonical signatures: %v", err)
		} else {
			allowed = loaded
			// User requested: don't capture "unreviewed" chunks for songs that we've already reviewed (as per metadata songname)
			prefix := fmt.Sprintf("%s - %s", song.Artist, song.Title)
			for canonicalFilename := range allowed {
				if strings.HasPrefix(canonicalFilename, prefix) {
					log.Printf("   [SIGNATURE REVIEW] Skipped saving %q because we already have a canonical signature for this song: %q", filename, canonicalFilename)
					return
				}
			}

			match, name, err := findMatchingCanonicalSignatureInSet(data, defaultFingerprintFormat, canonicalDir, allowed)
			if err == nil && match {
				matchedName = name
			}
		}
	}

	if matchedName == "" {
		if allowed == nil {
			saved, name, err := SaveUnreviewedChunkIfDistinct(data, unreviewedDir, canonicalDir, filename)
			if err != nil {
				log.Printf("   ⚠️ Failed to save unreviewed chunk %q: %v", filename, err)
				return
			}
			if saved {
				p.reportSavedUnreviewedChunk(song, filename, recordedAt, transcript)
				if transcript == "" && p.Transcribe != nil {
					go func(audioData []byte, f string) {
						ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
						defer cancel()
						if t, err := p.Transcribe(ctx, audioData); err == nil && t != "" && p.DBMgr != nil {
							_ = p.DBMgr.UpdateSignatureTranscript(context.Background(), "unreviewed", f, t)
						}
					}(data, filename)
				}
				return
			}
			matchedName = name
		} else {
			match, name, err := findMatchingCanonicalSignatureInSet(data, defaultFingerprintFormat, canonicalDir, allowed)
			if err != nil {
				log.Printf("   ⚠️ Failed to compare unreviewed chunk %q against campaign signatures: %v", filename, err)
				return
			}
			if !match {
				if err := SaveUnreviewedChunk(data, unreviewedDir, filename); err != nil {
					log.Printf("   ⚠️ Failed to save unreviewed chunk %q: %v", filename, err)
					return
				}
				p.reportSavedUnreviewedChunk(song, filename, recordedAt, transcript)
				if transcript == "" && p.Transcribe != nil {
					go func(audioData []byte, f string) {
						ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
						defer cancel()
						if t, err := p.Transcribe(ctx, audioData); err == nil && t != "" && p.DBMgr != nil {
							_ = p.DBMgr.UpdateSignatureTranscript(context.Background(), "unreviewed", f, t)
						}
					}(data, filename)
				}
				return
			}
			matchedName = name
		}
	}

	log.Printf("   [SIGNATURE REVIEW] Skipped saving %q because it matches canonical signature %q", filename, matchedName)
}

func (p *Poller) saveCapturedChunkForReview(capture *contestCapture, tag contestTag) {
	if capture == nil || !captureHasTag(capture, tag) {
		return
	}
	song := capture.Metadata
	if song == (SongInfo{}) {
		song = SongInfo{Artist: tag.CampaignArtist, Title: tag.Phrase}
	}
	p.saveUnreviewedChunkForReview(song, capture.Audio.Data, capture.Transcript)
}

func (p *Poller) reportSavedUnreviewedChunk(song SongInfo, filename string, recordedAt time.Time, transcript string) {
	if campaignArtist, ok := campaignArtistForTime(p.ActiveCampaigns, recordedAt); ok && p.DBMgr != nil {
		_ = p.DBMgr.UpsertSignatureFile(context.Background(), "unreviewed", filename, recordedAt, campaignArtist, transcript)
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
	return slices.ContainsFunc(p.ActiveCampaigns, func(c Campaign) bool {
		return c.IsActive(now)
	})
}

func (p *Poller) canRunContestChecker(now time.Time) bool {
	return p.hasActiveCampaign(now) && (p.StateMgr == nil || !p.StateMgr.Get().KillSwitchActive)
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
		Title:       "Inceput aplicatia!",
		Message:     "ProFM Poller started! Ascultam ce joacaa lol...",
		ActionLabel: "Vezi dashboardu",
		ActionURL:   p.BaseURL,
	})
	log.Println("Ascultam ce joacaa lol...")
	log.Println(strings.Repeat("-", 40))

	var currentSong SongInfo
	coordinator := p.contestCheckCoordinator()
	// Both checkers receive this exact coordinator. Its mutex makes Claim()
	// atomic, so only one checker can enter the shared WhatsApp send window.
	var metadataChecker ContestChecker = &metadataContestChecker{
		poller:      p,
		coordinator: coordinator,
		currentSong: &currentSong,
	}

	var fingerprintChecker ContestChecker
	var transcriptionChecker ContestChecker
	if p.AudioBuffer != nil {
		fingerprintChecker = &fingerprintContestChecker{
			poller:      p,
			coordinator: coordinator,
		}
		p.AudioBuffer.Start()
		if p.Transcribe != nil {
			transcriptionChecker = &transcriptionContestChecker{poller: p, coordinator: coordinator}
		}
	}

	shouldPoll := p.prepareStartState()
	go p.runMetadataChecker(metadataChecker, shouldPoll)
	if fingerprintChecker != nil {
		go runPeriodicChecker(p, fingerprintChecker)
	}
	if transcriptionChecker != nil {
		go runPeriodicChecker(p, transcriptionChecker)
	}

	select {}
}

func (p *Poller) runMetadataChecker(checker ContestChecker, checkImmediately bool) {
	// This worker owns the application sleep/wake transition. Both checker
	// workers skip their detector work outside an active campaign window.
	ticker := time.NewTicker(p.PollInterval)
	defer ticker.Stop()

	if checkImmediately {
		now := time.Now()
		if p.canRunContestChecker(now) {
			checker.Check(now)
		}
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

		checker.Check(now)
	}
}

func (p *Poller) prepareStartState() bool {
	if p.StateMgr == nil {
		return true
	}

	stats := GetAudioStatsPerPhone(p.StateMgr.Get().Connections, p.AudiosDir)
	p.StateMgr.Update(func(s *AppState) {
		var totalUnused, totalUsed int
		for i, conn := range s.Connections {
			phoneStats := stats[conn.Phone]
			s.Connections[i].UnusedAudios = phoneStats.Unused
			s.Connections[i].UsedAudios = phoneStats.Used
			totalUnused += phoneStats.Unused
			totalUsed += phoneStats.Used
		}
		// Also add canonical phone stats if not in connections
		canonicalStats := stats[CanonicalSenderPhone]
		canonicalInConns := false
		for _, conn := range s.Connections {
			if conn.Phone == CanonicalSenderPhone {
				canonicalInConns = true
				break
			}
		}
		if !canonicalInConns {
			totalUnused += canonicalStats.Unused
			totalUsed += canonicalStats.Used
		}

		s.UnusedAudios = totalUnused
		s.UsedAudios = totalUsed
	})

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
	p.checkSongWithCoordinator(currentSong, now, p.contestCheckCoordinator())
}

func (p *Poller) checkSongWithCoordinator(currentSong *SongInfo, now time.Time, coordinator *ContestCheckCoordinator) {
	if !coordinator.CanCheck(now) {
		return
	}
	p.resetDailyMatchesIfNeeded(now)

	song, err := p.getNowPlaying()
	if err != nil {
		log.Printf("Error fetching data: %v\n", err)
		return
	}

	if song != *currentSong {
		log.Printf("[%s] %s - %s", now.Format("15:04:05"), song.Artist, song.Title)

		*currentSong = song
		p.StateMgr.Update(func(s *AppState) {
			s.CurrentSong = song.Artist + " - " + song.Title
		})

		capture := p.captureContestAudio(now, song)
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
				if !coordinator.Claim(now, triggerSourceMetadata) {
					return
				}
				if p.consumeIgnoredMetadataTrigger(song) {
					log.Printf("   [INFO] Ignoring metadata trigger for '%s - %s' because it was already triggered by fingerprint.", song.Artist, song.Title)
				} else if !p.wasSongPlayedRecently(song.Artist, song.Title) && p.canSendCampaignArtist(campaignArtist, currentRadioLogID) {
					matchIndex, selected := p.claimScheduledMatch(now, song.Artist, song.Title)
					if selected {
						p.doTriggerVoiceNote(triggerSourceMetadata, campaignArtist, song.Artist, song.Title, now, matchIndex, currentRadioLogID)

						if p.StateMgr != nil && p.StateMgr.Get().GatheringSignatures && p.AudioBuffer != nil {
							// Metadata-only detections extend the preserved pre-roll by 4 minutes.
							p.AudioBuffer.Trigger(dashcamAfterDuration, func(data []byte) {
								tag := contestTag{Source: triggerSourceMetadata, CampaignArtist: campaignArtist}
								p.saveCapturedChunkForReview(&contestCapture{Audio: AudioSnapshot{Data: data}, CapturedAt: now, Metadata: song, Tags: []contestTag{tag}}, tag)
							})
						}
						p.tagCapture(capture, contestTag{Source: triggerSourceMetadata, CampaignArtist: campaignArtist})
					}
				}
			}
		} else {
			log.Printf("   [INFO] Daily limit of %d matches reached. Ignoring further campaign matches for today.", MaxDailyMatches)
		}
	}

	// Always update audio stats on each check to keep UI fresh
	stats := GetAudioStatsPerPhone(p.StateMgr.Get().Connections, p.AudiosDir)
	p.StateMgr.Update(func(s *AppState) {
		var totalUnused, totalUsed int
		for i, conn := range s.Connections {
			phoneStats := stats[conn.Phone]
			s.Connections[i].UnusedAudios = phoneStats.Unused
			s.Connections[i].UsedAudios = phoneStats.Used
			totalUnused += phoneStats.Unused
			totalUsed += phoneStats.Used
		}

		canonicalStats := stats[CanonicalSenderPhone]
		canonicalInConns := false
		for _, conn := range s.Connections {
			if conn.Phone == CanonicalSenderPhone {
				canonicalInConns = true
				break
			}
		}
		if !canonicalInConns {
			totalUnused += canonicalStats.Unused
			totalUsed += canonicalStats.Used
		}

		s.UnusedAudios = totalUnused
		s.UsedAudios = totalUsed
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

	type SendJob struct {
		Phone     string
		AudioFile string
		AudioHash string
	}
	var jobs []SendJob

	for _, conn := range p.StateMgr.Get().Connections {
		dir := GetAudioDirForPhone(conn.Phone, p.AudiosDir)
		f, hash, err := GetRandomAvailableAudio(dir, func(contentHash string) (bool, error) {
			if p.DBMgr == nil {
				return false, nil
			}
			return p.DBMgr.IsAudioHashUsed(context.Background(), contentHash)
		})
		if err == nil {
			jobs = append(jobs, SendJob{Phone: conn.Phone, AudioFile: f, AudioHash: hash})
		}
	}

	if len(jobs) == 0 {
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

	var sentFiles []string
	var lastErr error
	for _, job := range jobs {
		log.Printf("   Sending WhatsApp voice note using: %s (trigger=%s) from sender %s", job.AudioFile, triggerSource, job.Phone)
		err := p.SendVoiceNote(job.Phone, p.TargetPhone, job.AudioFile)
		if err != nil {
			log.Printf("   ❌ Error sending voice note from %s: %v\n", job.Phone, err)
			lastErr = err
			_ = p.Alerter.AlertCritical(AlertEvent{
				Title:       "Voice Note Failed",
				Message:     fmt.Sprintf("Could not send voice note to %s from %s\nTrigger: %s\nArtist: %s\nPiesa: %s\nEroare: %v", p.TargetPhone, job.Phone, triggerSource, artist, title, err),
				ActionLabel: "View Dashboard",
				ActionURL:   p.BaseURL,
			})
		} else {
			if p.DBMgr != nil {
				if err := p.DBMgr.RecordSuccessfulSend(context.Background(), campaignArtist, artist, title, job.AudioHash, currentRadioLogID, now); err != nil {
					log.Printf("   ❌ Voice note sent from %s but persistence update failed: %v\n", job.Phone, err)
					lastErr = err
					continue
				}
			}
			if err := MarkAudioUsedByHash(p.AudiosDir, job.AudioFile, job.AudioHash); err != nil {
				log.Printf("   ⚠️ Voice note sent but audio file rotation failed: %v\n", err)
			}
			sentFiles = append(sentFiles, job.AudioFile)
		}
	}

	if len(sentFiles) > 0 {
		filesStr := strings.Join(sentFiles, "\n\t\t\t")
		msg := dedent.Dedent(fmt.Sprintf(`
			S-au trimis %d vocal(uri) pe Wapp la nr: %s
			Trigger: %s
			Artist: %s
			Piesa: %s
			Fisiere audio trimise:
			%s
			`, len(sentFiles), p.TargetPhone, triggerSource, artist, title, filesStr))
		log.Println("✅", msg)
		_ = p.Alerter.AlertSuccess(AlertEvent{
			Title:       "Voice Note(s) Sent",
			Message:     msg,
			ActionLabel: "View Dashboard",
			ActionURL:   p.BaseURL,
		})
	}

	stats := GetAudioStatsPerPhone(p.StateMgr.Get().Connections, p.AudiosDir)
	p.StateMgr.Update(func(s *AppState) {
		s.Status = StatusPolling
		if lastErr != nil {
			s.LastError = fmt.Sprintf("Some errors occurred: %v", lastErr)
		} else {
			s.LastError = ""
		}
		if len(sentFiles) > 0 {
			s.LastVoiceNoteSentAt = time.Now()
		}

		var totalUnused, totalUsed int
		for i, conn := range s.Connections {
			phoneStats := stats[conn.Phone]
			s.Connections[i].UnusedAudios = phoneStats.Unused
			s.Connections[i].UsedAudios = phoneStats.Used
			totalUnused += phoneStats.Unused
			totalUsed += phoneStats.Used
		}
		canonicalStats := stats[CanonicalSenderPhone]
		canonicalInConns := false
		for _, conn := range s.Connections {
			if conn.Phone == CanonicalSenderPhone {
				canonicalInConns = true
				break
			}
		}
		if !canonicalInConns {
			totalUnused += canonicalStats.Unused
			totalUsed += canonicalStats.Used
		}
		s.UnusedAudios = totalUnused
		s.UsedAudios = totalUsed
	})
}

func runPeriodicChecker(p *Poller, checker ContestChecker) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C
		now := time.Now()
		if p.canRunContestChecker(now) {
			checker.Check(now)
		}
	}
}

func (p *Poller) checkFingerprintWithCoordinator(now time.Time, coordinator *ContestCheckCoordinator) {
	if !coordinator.CanCheck(now) {
		return
	}
	if !p.canRunContestChecker(now) {
		return
	}
	p.resetDailyMatchesIfNeeded(now)
	if p.matchesToday >= MaxDailyMatches {
		return
	}
	canonicalDir := filepath.Join(p.SignaturesDir, "canonical")
	allowedNames, err := allowedCanonicalSignatureNames(context.Background(), p.DBMgr, p.ActiveCampaigns, canonicalDir, now)
	if err != nil {
		log.Printf("   ⚠️ Failed to load campaign-bound signatures: %v", err)
		return
	}
	if len(allowedNames) == 0 {
		return
	}
	capture := p.captureContestAudio(now, SongInfo{})
	if capture == nil {
		return
	}
	buf := capture.Audio.Data
	if len(buf) > fingerprintTailBytes {
		buf = buf[len(buf)-fingerprintTailBytes:]
	}

	matched, name, err := findMatchingCanonicalSignatureInSet(buf, defaultFingerprintFormat, canonicalDir, allowedNames)
	if err != nil {
		log.Printf("   ⚠️ Fingerprint matching failed: %v", err)
		return
	}
	if !matched || !coordinator.Claim(now, triggerSourceFingerprint) {
		return
	}

	campaignArtist, err := signatureCampaignArtist(context.Background(), p.DBMgr, p.ActiveCampaigns, "canonical", name, canonicalDir)
	if err != nil {
		log.Printf("   ⚠️ Failed to resolve campaign owner for signature %q: %v", name, err)
		return
	}

	trigger := fingerprintTrigger{signatureName: name, artist: campaignArtist, title: "Unknown"}
	song := p.resolveFingerprintSong(trigger)
	p.tagCapture(capture, contestTag{Source: triggerSourceFingerprint, CampaignArtist: campaignArtist, SignatureName: name})

	p.ignoredTriggerMu.Lock()
	if p.ignoredTrigger.signatureName == name {
		p.ignoredTriggerMu.Unlock()
		return
	}
	p.ignoredTrigger = trigger
	p.ignoredTriggerMu.Unlock()

	log.Printf("   [FINGERPRINT MATCH] Matched signature: %s -> %s - %s", name, song.Artist, song.Title)
	if p.wasSongPlayedRecently(song.Artist, song.Title) {
		return
	}

	currentRadioLogID := int64(0)
	if p.DBMgr != nil {
		latestRadioLogID, err := p.DBMgr.GetLatestRadioLogID(context.Background())
		if err != nil {
			log.Printf("   ⚠️ Failed to load latest radio log id for fingerprint match %q: %v", name, err)
			return
		}
		currentRadioLogID = latestRadioLogID + 1
	}

	if !p.canSendCampaignArtist(campaignArtist, currentRadioLogID) {
		return
	}

	matchIndex, selected := p.claimScheduledMatch(now, song.Artist, song.Title)
	if selected {
		p.doTriggerVoiceNote(triggerSourceFingerprint, campaignArtist, song.Artist, song.Title, now, matchIndex, currentRadioLogID)
		if p.StateMgr != nil && p.StateMgr.Get().GatheringSignatures {
			p.saveCapturedChunkForReview(capture, contestTag{Source: triggerSourceFingerprint, CampaignArtist: campaignArtist, SignatureName: name})
		}
	}
}

func (p *Poller) checkTranscriptionWithCoordinator(now time.Time, coordinator *ContestCheckCoordinator, checker *transcriptionContestChecker) {
	if p.Transcribe == nil || (p.StreamingTranscriptionActive != nil && p.StreamingTranscriptionActive()) || !coordinator.CanCheck(now) || !p.canRunContestChecker(now) {
		return
	}
	p.resetDailyMatchesIfNeeded(now)
	if p.matchesToday >= MaxDailyMatches {
		return
	}
	capture := p.captureContestAudio(now, SongInfo{})
	if capture == nil || capture.Audio.Version == checker.lastVersion {
		return
	}
	checker.lastVersion = capture.Audio.Version

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	audioData := capture.Audio.Data
	const bytesPerSec = 16000
	if len(audioData) > 60*bytesPerSec {
		audioData = audioData[len(audioData)-60*bytesPerSec:]
	}

	transcript, err := p.Transcribe(ctx, audioData)
	if err != nil {
		log.Printf("   ⚠️ Transcription failed: %v", err)
		return
	}
	p.handleTranscriptWithCoordinator(now, coordinator, capture, transcript)
}

// HandleStreamingTranscript applies a live transcription through the same
// coordinator and persistence safeguards as the batch fallback.
func (p *Poller) HandleStreamingTranscript(transcript string) {
	now := time.Now()
	if !p.canRunContestChecker(now) {
		return
	}
	p.resetDailyMatchesIfNeeded(now)
	p.handleTranscriptWithCoordinator(now, p.contestCheckCoordinator(), p.captureContestAudio(now, SongInfo{}), transcript)
}

func (p *Poller) handleTranscriptWithCoordinator(now time.Time, coordinator *ContestCheckCoordinator, capture *contestCapture, transcript string) {
	if capture == nil || !coordinator.CanCheck(now) || p.matchesToday >= MaxDailyMatches {
		return
	}
	p.captureMu.Lock()
	capture.Transcript = transcript
	p.captureMu.Unlock()

	campaignArtist, phrase, matched := p.matchingCampaignPhrase(now, transcript)
	if !matched || !coordinator.Claim(now, triggerSourceTranscription) {
		return
	}
	tag := contestTag{Source: triggerSourceTranscription, CampaignArtist: campaignArtist, Phrase: phrase}
	p.tagCapture(capture, tag)
	song := capture.Metadata
	if song == (SongInfo{}) {
		song = p.resolveFingerprintSong(fingerprintTrigger{artist: campaignArtist, title: phrase})
	}
	if p.wasSongPlayedRecently(song.Artist, song.Title) {
		return
	}
	currentRadioLogID := int64(0)
	if p.DBMgr != nil {
		latestRadioLogID, err := p.DBMgr.GetLatestRadioLogID(context.Background())
		if err != nil {
			log.Printf("   ⚠️ Failed to load latest radio log id for transcription match: %v", err)
			return
		}
		currentRadioLogID = latestRadioLogID + 1
	}
	if !p.canSendCampaignArtist(campaignArtist, currentRadioLogID) {
		return
	}
	matchIndex, selected := p.claimScheduledMatch(now, song.Artist, song.Title)
	if !selected {
		return
	}
	log.Printf("   [TRANSCRIPTION MATCH] %q matched campaign phrase %q", transcript, phrase)
	p.doTriggerVoiceNote(triggerSourceTranscription, campaignArtist, song.Artist, song.Title, now, matchIndex, currentRadioLogID)
	if p.StateMgr != nil && p.StateMgr.Get().GatheringSignatures {
		p.saveCapturedChunkForReview(capture, tag)
	}
}

func (p *Poller) matchingCampaignPhrase(now time.Time, transcript string) (string, string, bool) {
	transcript = normalizeTriggerValue(transcript)
	if transcript == "" {
		return "", "", false
	}
	for _, campaign := range p.ActiveCampaigns {
		if !campaign.IsActive(now) {
			continue
		}
		phrases := append([]string(nil), campaign.Phrases...)
		if p.DBMgr != nil {
			dbPhrases, _ := p.DBMgr.GetCampaignPhrases(context.Background(), campaign.Artist)
			phrases = append(phrases, dbPhrases...)
		}
		for _, phrase := range phrases {
			if normalized := normalizeTriggerValue(phrase); normalized != "" && strings.Contains(transcript, normalized) {
				return campaign.Artist, phrase, true
			}
		}
	}
	return "", "", false
}
