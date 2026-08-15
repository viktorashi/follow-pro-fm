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
	"unicode"

	"github.com/lithammer/dedent"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
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

// DefaultActiveCampaigns defines the active campaign dates, artists, and phrases.
var DefaultActiveCampaigns = []Campaign{
	{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS", Phrases: []string{"follow profm"}},
	{StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana", Phrases: []string{"follow profm"}},
	{StartDate: "10-08-2026", EndDate: "21-08-2026", Artist: "The Weeknd", Phrases: []string{"follow profm"}},
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

	matchesMu    sync.Mutex
	matchesToday int
	lastCheckDay int

	sendMu    sync.Mutex
	checkerMu sync.Mutex
	checker   *ContestCheckCoordinator
	captureMu sync.Mutex
	capture   *contestCapture
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

type metadataContestChecker struct {
	poller      *Poller
	coordinator *ContestCheckCoordinator
	currentSong *SongInfo
}

type fingerprintContestChecker struct {
	poller      *Poller
	coordinator *ContestCheckCoordinator
}

type transcriptionContestChecker struct {
	poller      *Poller
	coordinator *ContestCheckCoordinator
	lastVersion int64
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

var diacriticsTransformer = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

func removeDiacritics(s string) string {
	result, _, err := transform.String(diacriticsTransformer, s)
	if err != nil {
		return s
	}
	return result
}

func normalizeTriggerValue(value string) string {
	value = removeDiacritics(value)
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func (p *Poller) contestCheckCoordinator() *ContestCheckCoordinator {
	p.checkerMu.Lock()
	defer p.checkerMu.Unlock()
	if p.checker == nil {
		p.checker = NewContestCheckCoordinator(p.ContestCheckCooldown)
	}
	return p.checker
}

func hasExistingSignatureForSong(dir string, prefix string) (bool, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, ""
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			return true, entry.Name()
		}
	}
	return false, ""
}

func (p *Poller) saveUnreviewedChunkForReview(song SongInfo, data []byte, transcript string) {
	recordedAt := time.Now()
	filename := fmt.Sprintf("%s - %s - %d.mp3", song.Artist, song.Title, recordedAt.Unix())
	unreviewedDir := filepath.Join(p.SignaturesDir, BucketUnreviewed)
	canonicalDir := filepath.Join(p.SignaturesDir, BucketCanonical)

	prefix := fmt.Sprintf("%s - %s - ", song.Artist, song.Title)
	if exists, name := hasExistingSignatureForSong(unreviewedDir, prefix); exists {
		log.Printf("   [SIGNATURE REVIEW] Skipped saving %q because we already have an unreviewed signature for this song: %q", filename, name)
		return
	}
	if exists, name := hasExistingSignatureForSong(canonicalDir, prefix); exists {
		log.Printf("   [SIGNATURE REVIEW] Skipped saving %q because we already have a canonical signature for this song: %q", filename, name)
		return
	}

	var allowed map[string]struct{}
	matchedName := ""
	if p.DBMgr != nil {
		loaded, err := allowedCanonicalSignatureNames(context.Background(), p.DBMgr, p.ActiveCampaigns, canonicalDir, recordedAt)
		if err != nil {
			log.Printf("   ⚠️ Failed to load campaign-bound canonical signatures: %v", err)
		} else {
			allowed = loaded
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
							_ = p.DBMgr.UpdateSignatureTranscript(context.Background(), BucketUnreviewed, f, t)
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
							_ = p.DBMgr.UpdateSignatureTranscript(context.Background(), BucketUnreviewed, f, t)
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
		_ = p.DBMgr.UpsertSignatureFile(context.Background(), BucketUnreviewed, filename, recordedAt, campaignArtist, transcript)
	}
	log.Printf("   [SIGNATURE REVIEW] Saved unreviewed chunk %q for manual review", filename)
	_ = p.Alerter.AlertInfo(AlertEvent{
		Title:       "Intro Chunk Needs Review",
		Message:     fmt.Sprintf("Saved new unreviewed intro chunk for manual review\nArtist: %s\nPiesa: %s\nFile: %s", song.Artist, song.Title, filename),
		ActionLabel: "View Dashboard",
		ActionURL:   p.BaseURL,
	})
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

func (c *metadataContestChecker) Check(now time.Time) {
	p := c.poller
	coordinator := c.coordinator
	currentSong := c.currentSong
	if !coordinator.CanCheck(now) {
		return
	}

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

		campaignArtist, matchesCampaign := p.matchingCampaignArtist(now, song)
		if matchesCampaign {
			if p.Alerter != nil {
				msg := fmt.Sprintf("🎵 Contest Song Detected!\nArtist: %s\nTitle: %s\n\n(This is an instant notification; automatic send rules apply independently.)", song.Artist, song.Title)
				_ = p.Alerter.AlertSuccess(AlertEvent{Title: "Contest Song Playing", Message: msg})
			}
		}

		// Abort immediately if the bot has been permanently killed
		if p.StateMgr != nil && p.StateMgr.Get().KillSwitchActive {
			log.Printf("   ⛔️ KILL SWITCH ACTIVE! Ignoring all campaign matches for '%s'.", song.Artist)
			return
		}

		if matchesCampaign {
			if p.hasReachedDailyLimit(now) {
				log.Printf("   [INFO] Daily limit of %d matches reached. Ignoring further campaign matches for today.", MaxDailyMatches)
			} else {
				if !p.wasSongPlayedRecently(song.Artist, song.Title) && coordinator.Claim(now, triggerSourceMetadata) {
					p.evaluateAndTriggerCampaign(now, campaignArtist, triggerSourceMetadata, currentRadioLogID, fmt.Sprintf("%q", campaignArtist), func(matchIndex int, currentRadioLogID int64) {
						if p.StateMgr != nil && p.StateMgr.Get().GatheringSignatures && p.AudioBuffer != nil {
							// Metadata-only detections extend the preserved pre-roll by 4 minutes.
							p.AudioBuffer.Trigger(dashcamAfterDuration, func(data []byte) {
								tag := contestTag{Source: triggerSourceMetadata, CampaignArtist: campaignArtist}
								p.saveCapturedChunkForReview(&contestCapture{Audio: AudioSnapshot{Data: data}, CapturedAt: now, Metadata: song, Tags: []contestTag{tag}}, tag)
							})
						}
						p.tagCapture(capture, contestTag{Source: triggerSourceMetadata, CampaignArtist: campaignArtist})
					})
				}
			}
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
		s.UnusedAudios = totalUnused
		s.UsedAudios = totalUsed
	})
}

func (p *Poller) hasReachedDailyLimit(now time.Time) bool {
	p.matchesMu.Lock()
	defer p.matchesMu.Unlock()
	if now.YearDay() != p.lastCheckDay {
		p.matchesToday = 0
		p.lastCheckDay = now.YearDay()
	}
	return p.matchesToday >= MaxDailyMatches
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

func (p *Poller) claimScheduledMatch(now time.Time, campaignArtist string) (int, bool) {
	p.matchesMu.Lock()
	if now.YearDay() != p.lastCheckDay {
		p.matchesToday = 0
		p.lastCheckDay = now.YearDay()
	}
	if p.matchesToday >= MaxDailyMatches {
		p.matchesMu.Unlock()
		log.Printf("   [INFO] Daily limit of %d matches reached. Ignoring further campaign matches for today.", MaxDailyMatches)
		return 0, false
	}

	p.matchesToday++
	matchIndex := p.matchesToday
	p.matchesMu.Unlock()

	selected, err := IsMatchSelectedToday(p.DBMgr, now, matchIndex)
	if err != nil {
		log.Printf("   ⚠️ Failed to evaluate RNG schedule for %s (Campaign: %s): %v", now.Format("2006-01-02"), campaignArtist, err)
		return matchIndex, false
	}
	if !selected {
		log.Printf("   [RNG] Match #%d for campaign %q is not scheduled today. Skipping send.", matchIndex, campaignArtist)
		return matchIndex, false
	}

	return matchIndex, true
}

func (p *Poller) checkCampaignResendGate(campaignArtist string, currentRadioLogID int64, logLabel string) (bool, int64) {
	if p.DBMgr == nil {
		return true, currentRadioLogID
	}

	if currentRadioLogID == 0 {
		latestRadioLogID, err := p.DBMgr.GetLatestRadioLogID(context.Background())
		if err != nil {
			log.Printf("   ⚠️ Failed to load latest radio log id for %s: %v", logLabel, err)
			return false, 0
		}
		currentRadioLogID = latestRadioLogID + 1
	}

	allowed, err := p.DBMgr.CanSendCampaignArtist(context.Background(), campaignArtist, currentRadioLogID)
	if err != nil {
		log.Printf("   ⚠️ Failed to enforce campaign resend gate for %q: %v", campaignArtist, err)
		return false, 0
	}
	if !allowed {
		log.Printf("   [INFO] Skipping resend for campaign artist %q until a different artist is logged in between.", campaignArtist)
	}
	return allowed, currentRadioLogID
}

func (p *Poller) evaluateAndTriggerCampaign(now time.Time, campaignArtist, triggerSource string, currentRadioLogID int64, logLabel string, onSuccess func(matchIndex int, currentRadioLogID int64)) {
	allowed, currentRadioLogID := p.checkCampaignResendGate(campaignArtist, currentRadioLogID, logLabel)
	if !allowed {
		return
	}

	matchIndex, selected := p.claimScheduledMatch(now, campaignArtist)
	if selected {
		p.doTriggerVoiceNote(triggerSource, campaignArtist, now, matchIndex, currentRadioLogID)
		if onSuccess != nil {
			onSuccess(matchIndex, currentRadioLogID)
		}
	}
}

func (p *Poller) doTriggerVoiceNote(triggerSource, campaignArtist string, now time.Time, matchIndex int, currentRadioLogID int64) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	// We assume that before calling doTriggerVoiceNote, we've already done checkCampaignResendGate
	// But it might be called from somewhere else, let's keep it safe.
	allowed, currentRadioLogID := p.checkCampaignResendGate(campaignArtist, currentRadioLogID, "voice note trigger")
	if !allowed {
		return
	}

	msg := dedent.Dedent(fmt.Sprintf(`
							🎉 [VEZI BAA ca se aude piesa]
							Trigger: %s
							Campanie: %s

							(Match-ul %d/%d de azi)
								`,
		triggerSource, campaignArtist, matchIndex, MaxDailyMatches))
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
		log.Printf("   ❌ NO UNUSED AUDIO FOUND FOR %s!", campaignArtist)
		_ = p.Alerter.AlertCritical(AlertEvent{
			Title:       "AUDIO POOL EXHAUSTED",
			Message:     "Cannot send voice note for " + campaignArtist + "\nNo unused audio files found in " + p.AudiosDir,
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

			isTimelocked := strings.Contains(err.Error(), "463") || strings.Contains(err.Error(), "ReachoutTimelocked")
			if isTimelocked {
				p.StateMgr.UpdateConnection(job.Phone, func(s *WAConnectionState) {
					s.Status = StatusError
				})
				p.StateMgr.Update(func(s *AppState) {
					s.LastError = fmt.Sprintf("Sender %s timelocked by WhatsApp (Error 463). New sender phone required.", job.Phone)
				})
			}

			_ = p.Alerter.AlertCritical(AlertEvent{
				Title:       "Voice Note Failed",
				Message:     fmt.Sprintf("Could not send voice note to %s from %s\nTrigger: %s\nCampanie: %s\nEroare: %v", p.TargetPhone, job.Phone, triggerSource, campaignArtist, err),
				ActionLabel: "View Dashboard",
				ActionURL:   p.BaseURL,
			})
		} else {
			if p.DBMgr != nil {
				if err := p.DBMgr.RecordSuccessfulSend(context.Background(), campaignArtist, campaignArtist, triggerSource, job.AudioHash, currentRadioLogID, now); err != nil {
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
			Campanie: %s
			Fisiere audio trimise:
			%s
			`, len(sentFiles), p.TargetPhone, triggerSource, campaignArtist, filesStr))
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

func (c *fingerprintContestChecker) Check(now time.Time) {
	p := c.poller
	coordinator := c.coordinator
	if !coordinator.CanCheck(now) {
		return
	}
	if !p.canRunContestChecker(now) {
		return
	}
	if p.hasReachedDailyLimit(now) {
		return
	}
	canonicalDir := filepath.Join(p.SignaturesDir, BucketCanonical)
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

	campaignArtist, err := signatureCampaignArtist(context.Background(), p.DBMgr, p.ActiveCampaigns, BucketCanonical, name, canonicalDir)
	if err != nil {
		log.Printf("   ⚠️ Failed to resolve campaign owner for signature %q: %v", name, err)
		return
	}

	log.Printf("   [FINGERPRINT MATCH] Matched signature: %s", name)

	if p.Alerter != nil {
		msg := fmt.Sprintf("🎵 Contest Audio Signature Detected!\nSignature Name: %s\nCampaign: %s\n\n(This is an instant notification; automatic send rules apply independently.)", name, campaignArtist)
		_ = p.Alerter.AlertSuccess(AlertEvent{Title: "Contest Song Playing", Message: msg})
	}

	p.evaluateAndTriggerCampaign(now, campaignArtist, triggerSourceFingerprint, 0, fmt.Sprintf("fingerprint match %q", name), func(matchIndex int, currentRadioLogID int64) {
		p.tagCapture(capture, contestTag{Source: triggerSourceFingerprint, CampaignArtist: campaignArtist, SignatureName: name})
		if p.StateMgr != nil && p.StateMgr.Get().GatheringSignatures {
			p.saveCapturedChunkForReview(capture, contestTag{Source: triggerSourceFingerprint, CampaignArtist: campaignArtist, SignatureName: name})
		}
	})
}

func (c *transcriptionContestChecker) Check(now time.Time) {
	p := c.poller
	coordinator := c.coordinator
	checker := c
	if p.Transcribe == nil || (p.StreamingTranscriptionActive != nil && p.StreamingTranscriptionActive()) || !coordinator.CanCheck(now) || !p.canRunContestChecker(now) {
		return
	}
	if p.hasReachedDailyLimit(now) {
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
	p.handleTranscriptWithCoordinator(now, p.contestCheckCoordinator(), p.captureContestAudio(now, SongInfo{}), transcript)
}

func (p *Poller) handleTranscriptWithCoordinator(now time.Time, coordinator *ContestCheckCoordinator, capture *contestCapture, transcript string) {
	if capture == nil || !coordinator.CanCheck(now) || p.hasReachedDailyLimit(now) {
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

	if p.Alerter != nil {
		msg := fmt.Sprintf("🎵 Contest Phrase Detected (via Transcription)!\nMatched Phrase: %s\nCampaign: %s\nTranscript: \"%s\"\n\n(This is an instant notification; automatic send rules apply independently.)", phrase, campaignArtist, transcript)
		_ = p.Alerter.AlertSuccess(AlertEvent{Title: "Contest Song Playing", Message: msg})
	}
	p.evaluateAndTriggerCampaign(now, campaignArtist, triggerSourceTranscription, 0, fmt.Sprintf("transcription match %q", phrase), func(matchIndex int, currentRadioLogID int64) {
		log.Printf("   [TRANSCRIPTION MATCH] %q matched campaign phrase %q", transcript, phrase)
		if p.StateMgr != nil && p.StateMgr.Get().GatheringSignatures {
			p.saveCapturedChunkForReview(capture, tag)
		}
	})
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
