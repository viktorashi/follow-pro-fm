package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingAlerter struct {
	infoEvents     []AlertEvent
	successEvents  []AlertEvent
	criticalEvents []AlertEvent
}

func (a *recordingAlerter) AlertCritical(event AlertEvent) error {
	a.criticalEvents = append(a.criticalEvents, event)
	return nil
}

func (a *recordingAlerter) AlertInfo(event AlertEvent) error {
	a.infoEvents = append(a.infoEvents, event)
	return nil
}

func (a *recordingAlerter) AlertSuccess(event AlertEvent) error {
	a.successEvents = append(a.successEvents, event)
	return nil
}

func bucharestTime(year int, month time.Month, day, hour, min, sec int) time.Time {
	return time.Date(year, month, day, hour, min, sec, 0, bucharestLocation)
}

func TestCampaign_IsActive(t *testing.T) {
	c := Campaign{
		StartDate: "15-06-2026",
		EndDate:   "26-06-2026",
		Artist:    "BTS",
	}

	tests := []struct {
		name string
		time time.Time
		want bool
	}{
		{
			name: "Active within window (Wednesday 12:00)",
			time: bucharestTime(2026, time.June, 17, 12, 0, 0), // Wed
			want: true,
		},
		{
			name: "Inactive weekend (Saturday)",
			time: bucharestTime(2026, time.June, 20, 12, 0, 0), // Sat
			want: false,
		},
		{
			name: "Inactive before 07:00",
			time: bucharestTime(2026, time.June, 17, 6, 59, 59), // Wed
			want: false,
		},
		{
			name: "Inactive after 20:00",
			time: bucharestTime(2026, time.June, 17, 20, 0, 1), // Wed
			want: false,
		},
		{
			name: "Inactive before StartDate",
			time: bucharestTime(2026, time.June, 12, 12, 0, 0), // Friday
			want: false,
		},
		{
			name: "Inactive after EndDate",
			time: bucharestTime(2026, time.June, 29, 12, 0, 0), // Monday
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.IsActive(tt.time); got != tt.want {
				t.Errorf("IsActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPoller_hasActiveCampaign(t *testing.T) {
	p := &Poller{
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
	}

	activeTime := bucharestTime(2026, time.June, 17, 12, 0, 0) // Wed
	if !p.hasActiveCampaign(activeTime) {
		t.Errorf("Expected hasActiveCampaign to be true for Wed 12:00")
	}

	inactiveTime := bucharestTime(2026, time.June, 20, 12, 0, 0) // Sat
	if p.hasActiveCampaign(inactiveTime) {
		t.Errorf("Expected hasActiveCampaign to be false for Saturday")
	}
}

func TestCampaign_IsActive_BadDates(t *testing.T) {
	c := Campaign{
		StartDate: "bad-date",
		EndDate:   "26-06-2026",
	}
	if got := c.IsActive(bucharestTime(2026, time.June, 17, 12, 0, 0)); got != false {
		t.Errorf("IsActive with bad dates = %v, want false", got)
	}
}

func TestCampaign_IsActive_BypassCampaignTimeChecks(t *testing.T) {
	t.Setenv("BYPASS_CAMPAIGN_TIME_CHECKS", "true")

	c := Campaign{
		StartDate: "15-06-2026",
		EndDate:   "26-06-2026",
		Artist:    "BTS",
	}

	if got := c.IsActive(bucharestTime(2026, time.June, 29, 12, 0, 0)); got != true {
		t.Fatalf("IsActive() with bypass = %v, want true", got)
	}
}

func TestPoller_getNowPlaying(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantArtist string
		wantTitle  string
		wantErr    bool
	}{
		{
			name: "Valid Response",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"BTS","playerExtendedSongSubtitle":"2026 - Dynamite"}}}`))
			},
			wantArtist: "BTS",
			wantTitle:  "Dynamite",
			wantErr:    false,
		},
		{
			name: "Missing Fields (Unknowns)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"epg":{}}}`))
			},
			wantArtist: "Unknown Artist",
			wantTitle:  "Unknown Song",
			wantErr:    false,
		},
		{
			name: "Clean up year edge case",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"Artist","playerExtendedSongSubtitle":"2000 - LASA-MA PAPA LA MARE"}}}`))
			},
			wantArtist: "Artist",
			wantTitle:  "LASA-MA PAPA LA MARE",
			wantErr:    false,
		},
		{
			name: "Clean up year edge case 2",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"BTS","playerExtendedSongSubtitle":"2026 - 2.0"}}}`))
			},
			wantArtist: "BTS",
			wantTitle:  "2.0",
			wantErr:    false,
		},
		{
			name: "Cu apostreoafe si chestii",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"Queen","playerExtendedSongSubtitle":"1985 - '39"}}}`))
			},
			wantArtist: "Queen",
			wantTitle:  "'39",
			wantErr:    false,
		},
		{
			name: "Cu double quotes",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"David Bowie","playerExtendedSongSubtitle":"1977 - \"Heroes\""}}}`))
			},
			wantArtist: "David Bowie",
			wantTitle:  "\"Heroes\"",
			wantErr:    false,
		},
		{
			name: "Clean up string containing dash but not year",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"Artist","playerExtendedSongSubtitle":"Word - Song Title"}}}`))
			},
			wantArtist: "Artist",
			wantTitle:  "Word - Song Title",
			wantErr:    false,
		},
		{
			name: "Bad Status Code",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr: true,
		},
		{
			name: "Bad JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{bad-json`))
			},
			wantErr: true,
		},
		{
			name: "Body Read Error (Unexpected EOF)",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "100")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("short"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			poller := &Poller{APIURL: server.URL}
			got, err := poller.getNowPlaying()

			if (err != nil) != tt.wantErr {
				t.Errorf("getNowPlaying() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Artist != tt.wantArtist || got.Title != tt.wantTitle {
					t.Errorf("getNowPlaying() got = %v, want %v / %v", got, tt.wantArtist, tt.wantTitle)
				}
			}
		})
	}
}

func TestPoller_getNowPlaying_BadURL(t *testing.T) {
	// Using an invalid port that usually refuses connection
	poller := &Poller{APIURL: "http://127.0.0.1:0"}
	_, err := poller.getNowPlaying()
	if err == nil {
		t.Error("Expected error for bad connection")
	}

	// Test NewRequest error (e.g., bad URL scheme)
	poller = &Poller{APIURL: string([]byte{0x7f})}
	_, err = poller.getNowPlaying()
	t.Logf("err for \\x7f: %v", err)
	if err == nil {
		t.Error("Expected error for bad URL")
	}
}

func TestPoller_checkMetadataWithCoordinator(t *testing.T) {
	// A Wednesday at 12:00 PM (Active time for campaigns)
	activeTime := bucharestTime(2026, time.June, 17, 12, 0, 0)

	tests := []struct {
		name               string
		mockArtist         string
		mockTitle          string
		currentSong        *SongInfo
		wantMatches        int
		wantVoiceCalls     int
		simulateError      bool
		simulateVoiceError bool
	}{
		{
			name:           "Match active campaign (BTS)",
			mockArtist:     "BTS",
			mockTitle:      "Dynamite",
			currentSong:    &SongInfo{},
			wantMatches:    1,
			wantVoiceCalls: 1,
		},
		{
			name:           "No match for active campaign (Ed Sheeran)",
			mockArtist:     "Ed Sheeran",
			mockTitle:      "Shape of You",
			currentSong:    &SongInfo{},
			wantMatches:    0,
			wantVoiceCalls: 0,
		},
		{
			name:           "Match active campaign via FOLLOW PROFM title keyword",
			mockArtist:     "Ed Sheeran",
			mockTitle:      "CONCURS FOLLOW PROFM 2026 MUNCHEN - BUTTER",
			currentSong:    &SongInfo{},
			wantMatches:    1,
			wantVoiceCalls: 1,
		},
		{
			name:           "Same song playing again (should trigger if DBMgr is not tracking, but DBMgr will track now so it should skip)",
			mockArtist:     "BTS",
			mockTitle:      "Dynamite",
			currentSong:    &SongInfo{Artist: "BTS", Title: "Dynamite"}, // Handled by currentSong pointer logic
			wantMatches:    0,
			wantVoiceCalls: 0,
		},
		{
			name:           "Different song but same campaign artist (trigger)",
			mockArtist:     "BTS",
			mockTitle:      "Butter",
			currentSong:    &SongInfo{},
			wantMatches:    1,
			wantVoiceCalls: 1,
		},
		{
			name:           "API Error, should just return early",
			simulateError:  true,
			currentSong:    &SongInfo{},
			wantMatches:    0,
			wantVoiceCalls: 0,
		},
		{
			name:               "Voice note send error, logs and continues",
			mockArtist:         "BTS",
			mockTitle:          "Dynamite",
			currentSong:        &SongInfo{},
			wantMatches:        1,
			wantVoiceCalls:     1,
			simulateVoiceError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.simulateError {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				epg := EPGData{}
				epg.Data.Epg.Title = tt.mockArtist
				epg.Data.Epg.Subtitle = tt.mockTitle
				_ = json.NewEncoder(w).Encode(epg)
			}))
			defer server.Close()

			voiceCalls := 0
			dbMgr, _ := NewDBManager(":memory:")

			audiosDir := t.TempDir()
			personDir := filepath.Join(audiosDir, "test-sender")
			_ = os.MkdirAll(personDir, 0o755)
			_ = os.WriteFile(filepath.Join(personDir, "test.ogg"), []byte("fake"), 0o644)
			poller := &Poller{
				APIURL:       server.URL,
				PollInterval: 1 * time.Millisecond,
				ActiveCampaigns: []Campaign{
					{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
				},
				TargetPhone: "+40770661491",
				StateMgr:    createMockStateMgr(),
				Alerter:     NewMultiAlerter(),
				AudiosDir:   audiosDir,
				DBMgr:       dbMgr,
				SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
					if senderPhone != "+40700000001" {
						t.Fatalf("SendVoiceNote senderPhone = %q, want %q", senderPhone, "+40700000001")
					}
					if targetPhone != "+40770661491" {
						t.Fatalf("SendVoiceNote targetPhone = %q, want %q", targetPhone, "+40770661491")
					}
					if audioPath == "" {
						t.Fatal("SendVoiceNote audioPath should not be empty")
					}
					voiceCalls++
					if tt.simulateVoiceError {
						return fmt.Errorf("simulated network error sending audio")
					}
					return nil
				},
			}

			(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: tt.currentSong}).Check(activeTime)

			if poller.matchesToday != tt.wantMatches {
				t.Errorf("matchesToday = %v, want %v", poller.matchesToday, tt.wantMatches)
			}
			if voiceCalls != tt.wantVoiceCalls {
				t.Errorf("voiceCalls = %v, want %v", voiceCalls, tt.wantVoiceCalls)
			}
		})
	}
}

func TestPoller_checkMetadataWithCoordinator_Deduplication(t *testing.T) {
	activeTime := bucharestTime(2026, time.June, 17, 12, 0, 0)
	dbMgr, _ := NewDBManager(":memory:")
	audiosDir := t.TempDir()
	personDir := filepath.Join(audiosDir, "test-sender")
	_ = os.MkdirAll(personDir, 0o755)
	_ = os.WriteFile(filepath.Join(personDir, "test1.ogg"), []byte("fake-1"), 0o644)
	_ = os.WriteFile(filepath.Join(personDir, "test2.ogg"), []byte("fake-2"), 0o644)
	_ = os.WriteFile(filepath.Join(personDir, "test3.ogg"), []byte("fake-3"), 0o644)

	voiceCalls := 0
	poller := &Poller{
		APIURL:       "",
		PollInterval: 1 * time.Millisecond,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
		TargetPhone: "+40770661491",
		StateMgr:    createMockStateMgr(),
		Alerter:     NewMultiAlerter(),
		AudiosDir:   audiosDir,
		DBMgr:       dbMgr,
		SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
			if senderPhone != "+40700000001" {
				t.Fatalf("SendVoiceNote senderPhone = %q, want %q", senderPhone, "+40700000001")
			}
			if targetPhone != "+40770661491" {
				t.Fatalf("SendVoiceNote targetPhone = %q, want %q", targetPhone, "+40770661491")
			}
			if audioPath == "" {
				t.Fatal("SendVoiceNote audioPath should not be empty")
			}
			voiceCalls++
			return nil
		},
	}

	currentSong := &SongInfo{}
	// Helper to simulate a song play
	simulateSong := func(artist, title string, when time.Time) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, `{"data":{"epg":{"playerExtendedSongTitle":"%s","playerExtendedSongSubtitle":"%s"}}}`, artist, title)
		}))
		defer server.Close()
		poller.APIURL = server.URL
		(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: currentSong}).Check(when)
	}

	// 1. Play BTS - Dynamite (should trigger, voiceCalls = 1)
	simulateSong("BTS", "Dynamite", activeTime)
	if voiceCalls != 1 {
		t.Errorf("Expected 1 voice call for BTS Dynamite, got %d", voiceCalls)
	}

	// 2. Play Kamrad - BE MINE (No campaign, voiceCalls = 1)
	simulateSong("Kamrad", "BE MINE", activeTime.Add(21*time.Minute))
	if voiceCalls != 1 {
		t.Errorf("Expected 1 voice call, got %d", voiceCalls)
	}

	// 3. Play BTS - Dynamite again (It was 1 song ago, so it's in the last 2 plays, should SKIP, voiceCalls = 1)
	simulateSong("BTS", "Dynamite", activeTime.Add(42*time.Minute))
	if voiceCalls != 1 {
		t.Errorf("Expected BTS Dynamite to be deduplicated! Voice calls should still be 1, got %d", voiceCalls)
	}

	// 4. Play Ed Sheeran - Shape of You (No campaign, voiceCalls = 1)
	simulateSong("Ed Sheeran", "Shape of You", activeTime.Add(63*time.Minute))

	// 5. Play BTS - Dynamite again.
	// Now the history is:
	// Ed Sheeran - Shape of You (1 play ago)
	// BTS - Dynamite (2 plays ago)
	// It's still in the last 2 plays! Should SKIP!
	simulateSong("BTS", "Dynamite", activeTime.Add(84*time.Minute))
	if voiceCalls != 1 {
		t.Errorf("Expected BTS Dynamite to be deduplicated again (2 plays ago)! Voice calls got %d", voiceCalls)
	}

	// 6. Play another song to push BTS out of top 2
	simulateSong("The Weeknd", "Blinding Lights", activeTime.Add(105*time.Minute))
	simulateSong("Bruno Mars", "Leave the Door Open", activeTime.Add(126*time.Minute))

	// History: Bruno Mars (1), The Weeknd (2).
	// 7. Play BTS - Dynamite. Should trigger again once it is out of the
	// previous 2-song window and there is still globally unused audio left.
	simulateSong("BTS", "Dynamite", activeTime.Add(147*time.Minute))
	if voiceCalls != 2 {
		t.Errorf("Expected BTS Dynamite to trigger again since it's out of last 2 plays! Voice calls got %d", voiceCalls)
	}
}

func TestPoller_checkMetadataWithCoordinator_DailyLimit(t *testing.T) {
	activeTime := bucharestTime(2026, time.June, 17, 12, 0, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"BTS","playerExtendedSongSubtitle":"Dynamite"}}}`))
	}))
	defer server.Close()

	voiceCalls := 0
	poller := &Poller{
		APIURL:       server.URL,
		PollInterval: 1 * time.Millisecond,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
		matchesToday: 6,
		lastCheckDay: activeTime.YearDay(), // Prevent matchesToday from being reset
		TargetPhone:  "+40770661491",
		StateMgr:     createMockStateMgr(),
		Alerter:      NewMultiAlerter(),
		AudiosDir:    t.TempDir(),
		SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
			if senderPhone != "+40700000001" {
				t.Fatalf("SendVoiceNote senderPhone = %q, want %q", senderPhone, "+40700000001")
			}
			if targetPhone != "+40770661491" {
				t.Fatalf("SendVoiceNote targetPhone = %q, want %q", targetPhone, "+40770661491")
			}
			voiceCalls++
			return nil
		},
	}

	currentSong := &SongInfo{}
	(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: currentSong}).Check(activeTime)

	if voiceCalls != 0 {
		t.Errorf("Expected 0 voice calls due to daily limit, got %d", voiceCalls)
	}
	if poller.matchesToday != 6 {
		t.Errorf("matchesToday should remain 6, got %d", poller.matchesToday)
	}
}

func TestPoller_checkMetadataWithCoordinator_RequiresDifferentArtistBetweenCampaignSends(t *testing.T) {
	activeTime := bucharestTime(2026, time.June, 17, 12, 0, 0)
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	audiosDir := t.TempDir()
	personDir := filepath.Join(audiosDir, "test-sender")
	_ = os.MkdirAll(personDir, 0o755)
	if err := os.WriteFile(filepath.Join(personDir, "test1.ogg"), []byte("first"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(personDir, "test2.ogg"), []byte("second"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	voiceCalls := 0
	poller := &Poller{
		APIURL:       "",
		PollInterval: 1 * time.Millisecond,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
		TargetPhone: "+40770661491",
		StateMgr:    createMockStateMgr(),
		Alerter:     NewMultiAlerter(),
		AudiosDir:   audiosDir,
		DBMgr:       dbMgr,
		SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
			voiceCalls++
			return nil
		},
	}

	currentSong := &SongInfo{}
	simulateSong := func(artist, title string, when time.Time) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, `{"data":{"epg":{"playerExtendedSongTitle":"%s","playerExtendedSongSubtitle":"%s"}}}`, artist, title)
		}))
		defer server.Close()
		poller.APIURL = server.URL
		(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: currentSong}).Check(when)
	}

	simulateSong("BTS", "Dynamite", activeTime)
	if voiceCalls != 1 {
		t.Fatalf("Expected first BTS detection to send once, got %d", voiceCalls)
	}

	simulateSong("BTS", "Butter", activeTime.Add(21*time.Minute))
	if voiceCalls != 1 {
		t.Fatalf("Expected BTS resend without another artist in between to be blocked, got %d", voiceCalls)
	}

	simulateSong("Kamrad", "BE MINE", activeTime.Add(42*time.Minute))
	simulateSong("BTS", "Permission to Dance", activeTime.Add(63*time.Minute))
	if voiceCalls != 2 {
		t.Fatalf("Expected BTS resend after another artist to be allowed, got %d", voiceCalls)
	}
}

func TestPoller_matchingCampaignArtistRequiresActualCampaignMatch(t *testing.T) {
	activeTime := bucharestTime(2026, time.June, 17, 12, 0, 0)
	poller := &Poller{
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
	}

	campaignArtist, matched := poller.matchingCampaignArtist(activeTime, SongInfo{Artist: "Taylor Swift", Title: "Cruel Summer"})
	if matched {
		t.Fatalf("Expected unrelated fingerprint signature to be ignored, got campaign artist %q", campaignArtist)
	}

	campaignArtist, matched = poller.matchingCampaignArtist(activeTime, SongInfo{Artist: "BTS feat. Halsey", Title: "Butter"})
	if !matched || campaignArtist != "BTS" {
		t.Fatalf("Expected BTS song to match active campaign, got matched=%v campaignArtist=%q", matched, campaignArtist)
	}
}

func TestPoller_captureContestAudioSharesOneSnapshotAndMetadata(t *testing.T) {
	buffer := NewCircularAudioBuffer("", 16)
	buffer.writeBytes([]byte("live audio"))
	poller := &Poller{AudioBuffer: buffer}
	now := bucharestTime(2026, time.June, 17, 12, 0, 0)

	first := poller.captureContestAudio(now, SongInfo{})
	buffer.writeBytes([]byte(" newer"))
	second := poller.captureContestAudio(now, SongInfo{Artist: "BTS", Title: "Butter"})
	if first == nil || first != second {
		t.Fatal("expected checkers to receive the same captured audio observation")
	}
	if got, want := string(second.Audio.Data), "live audio"; got != want {
		t.Fatalf("captured audio = %q, want %q", got, want)
	}
	if second.Metadata != (SongInfo{Artist: "BTS", Title: "Butter"}) {
		t.Fatalf("metadata = %#v, want current song", second.Metadata)
	}
	if next := poller.captureContestAudio(now.Add(contestCaptureWindow), SongInfo{}); next == first {
		t.Fatal("expected the next checker window to capture fresh audio")
	}
}

func TestPoller_matchingCampaignPhraseRequiresAnActiveCampaignPhrase(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	_ = dbMgr.AddCampaignPhrase(context.Background(), "BTS", "follow profm")

	poller := &Poller{
		ActiveCampaigns: []Campaign{{
			StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS",
		}},
		DBMgr: dbMgr,
	}
	now := bucharestTime(2026, time.June, 17, 12, 0, 0)
	artist, phrase, matched := poller.matchingCampaignPhrase(now, "Acum asculta follow profm si castiga cu noi")
	if !matched || artist != "BTS" || phrase != "follow profm" {
		t.Fatalf("matchingCampaignPhrase() = (%q, %q, %v), want BTS phrase match", artist, phrase, matched)
	}
	if _, _, matched := poller.matchingCampaignPhrase(now, "unrelated radio chat"); matched {
		t.Fatal("unrelated transcription must not trigger a contest")
	}
}

func TestPoller_matchingCampaignPhraseAllowsWhisperWordDrift(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	phrase := "Urmează Ariana Grande pe ProFM! Ascultă melodia și poți câștiga super premii în direct."
	_ = dbMgr.AddCampaignPhrase(context.Background(), "Ariana", phrase)

	poller := &Poller{
		ActiveCampaigns: []Campaign{{StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana"}},
		DBMgr:           dbMgr,
	}
	now := bucharestTime(2026, time.July, 21, 12, 0, 0)
	artist, matchedPhrase, matched := poller.matchingCampaignPhrase(now, "Urmează ariana grandei pe profm, ascultă melodii ași poți că știga super premii indirect.")
	if !matched || artist != "Ariana" || normalizeTriggerValue(matchedPhrase) != normalizeTriggerValue(phrase) {
		t.Fatalf("matchingCampaignPhrase() = (%q, %q, %v), want fuzzy Ariana phrase match", artist, matchedPhrase, matched)
	}
}

func TestPoller_matchingCampaignPhraseRefreshesAfterPhraseEdits(t *testing.T) {
	dbMgr := mustNewTestDBManager(t)
	ctx := context.Background()
	if err := dbMgr.AddCampaignPhrase(ctx, "BTS", "follow profm"); err != nil {
		t.Fatal(err)
	}
	poller := &Poller{ActiveCampaigns: []Campaign{{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"}}, DBMgr: dbMgr}
	now := bucharestTime(2026, time.June, 17, 12, 0, 0)

	if _, phrase, matched := poller.matchingCampaignPhrase(now, "follow profm"); !matched || phrase != "follow profm" {
		t.Fatalf("initial phrase = (%q, %v), want follow profm match", phrase, matched)
	}
	if err := dbMgr.DeleteCampaignPhrase(ctx, "BTS", "follow profm"); err != nil {
		t.Fatal(err)
	}
	if _, _, matched := poller.matchingCampaignPhrase(now, "follow profm"); matched {
		t.Fatal("deleted phrase still matched from cache")
	}
	if err := dbMgr.AddCampaignPhrase(ctx, "BTS", "trimite mesaj"); err != nil {
		t.Fatal(err)
	}
	if _, phrase, matched := poller.matchingCampaignPhrase(now, "trimite mesaj acum"); !matched || phrase != "trimite mesaj" {
		t.Fatalf("added phrase = (%q, %v), want trimite mesaj match", phrase, matched)
	}
}

func TestPoller_transcriptionMatchesAcrossRecentTranscriptWindow(t *testing.T) {
	t.Setenv("BYPASS_CAMPAIGN_TIME_CHECKS", "true")
	dbMgr := mustNewTestDBManager(t)
	if err := dbMgr.AddCampaignPhrase(context.Background(), "The Weeknd", "ascultă hitul către barcelona trimite acum un mesaj audio pe whatsapp"); err != nil {
		t.Fatal(err)
	}
	alerter := &recordingAlerter{}
	poller := &Poller{
		ActiveCampaigns:     []Campaign{{Artist: "The Weeknd"}},
		DBMgr:               dbMgr,
		Alerter:             alerter,
		TranscriptionBuffer: NewTimeSeriesBuffer[string](10 * time.Minute),
	}
	coordinator := NewContestCheckCoordinator(time.Minute)
	now := time.Now()

	poller.handleTranscriptWithCoordinator(now, coordinator, nil, "ascultă hitul către barcelona")
	if len(alerter.successEvents) != 0 {
		t.Fatal("partial transcript triggered before phrase was complete")
	}
	poller.handleTranscriptWithCoordinator(now.Add(5*time.Second), coordinator, nil, "trimite acum un mesaj audio pe whatsapp")
	if len(alerter.successEvents) != 1 {
		t.Fatalf("AlertSuccess() calls = %d, want 1", len(alerter.successEvents))
	}
}

func TestPoller_transcriptionAlertsWhenAutomaticSendingIsBlocked(t *testing.T) {
	t.Setenv("BYPASS_CAMPAIGN_TIME_CHECKS", "true")
	dbMgr := mustNewTestDBManager(t)
	if err := dbMgr.AddCampaignPhrase(context.Background(), "BTS", "follow profm"); err != nil {
		t.Fatal(err)
	}
	stateMgr := createMockStateMgr()
	stateMgr.Update(func(s *AppState) { s.KillSwitchActive = true })
	alerter := &recordingAlerter{}
	poller := &Poller{
		ActiveCampaigns: []Campaign{{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"}},
		DBMgr:           dbMgr,
		StateMgr:        stateMgr,
		Alerter:         alerter,
		matchesToday:    MaxDailyMatches,
		lastCheckDay:    time.Now().YearDay(),
	}

	poller.HandleStreamingTranscript("asculta follow profm acum")
	if got := len(alerter.successEvents); got != 1 {
		t.Fatalf("AlertSuccess() calls = %d, want 1", got)
	}
	if got := poller.matchesToday; got != MaxDailyMatches {
		t.Fatalf("matchesToday = %d, want daily limit unchanged", got)
	}
}

func TestPoller_saveUnreviewedChunkForReviewAlertsOnceWhenSaved(t *testing.T) {
	alerter := &recordingAlerter{}
	signaturesDir := t.TempDir()
	poller := &Poller{
		Alerter:       alerter,
		SignaturesDir: signaturesDir,
		BaseURL:       "http://localhost:8080",
	}

	poller.saveUnreviewedChunkForReview(SongInfo{Artist: "BTS", Title: "Butter"}, []byte("new intro chunk"), "", []ContestTag{})

	if len(alerter.infoEvents) != 1 {
		t.Fatalf("AlertInfo() calls = %d, want 1", len(alerter.infoEvents))
	}
	if got := alerter.infoEvents[0].Title; got != "Intro Chunk Needs Review" {
		t.Fatalf("AlertInfo().Title = %q, want %q", got, "Intro Chunk Needs Review")
	}

	files, err := os.ReadDir(filepath.Join(signaturesDir, BucketUnreviewed))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("saved review chunks = %d, want 1", len(files))
	}
}

func TestPoller_saveUnreviewedChunkForReviewStoresCampaignOwnership(t *testing.T) {
	alerter := &recordingAlerter{}
	signaturesDir := t.TempDir()
	dbMgr := mustNewTestDBManager(t)
	poller := &Poller{
		Alerter:       alerter,
		SignaturesDir: signaturesDir,
		BaseURL:       "http://localhost:8080",
		DBMgr:         dbMgr,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
	}

	t.Setenv("BYPASS_CAMPAIGN_TIME_CHECKS", "true")
	poller.saveUnreviewedChunkForReview(SongInfo{Artist: "BTS", Title: "Butter"}, []byte("new intro chunk"), "", []ContestTag{})

	files, err := os.ReadDir(filepath.Join(signaturesDir, BucketUnreviewed))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("saved review chunks = %d, want 1", len(files))
	}

	meta, err := dbMgr.GetSignatureFile(context.Background(), BucketUnreviewed, files[0].Name())
	if err != nil {
		t.Fatalf("GetSignatureFile() error = %v", err)
	}
	if meta.CampaignArtist != "BTS" {
		t.Fatalf("CampaignArtist = %q, want %q", meta.CampaignArtist, "BTS")
	}
}

func TestPoller_doTriggerVoiceNote_ReportsOnlySuccessAfterSend(t *testing.T) {
	audiosDir := t.TempDir()
	personDir := filepath.Join(audiosDir, "test-sender")
	_ = os.MkdirAll(personDir, 0o755)
	if err := os.WriteFile(filepath.Join(personDir, "test.ogg"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	alerter := &recordingAlerter{}
	sendCalls := 0
	poller := &Poller{
		TargetPhone: "+40770661491",
		StateMgr:    createMockStateMgr(),
		Alerter:     alerter,
		AudiosDir:   audiosDir,
		SendVoiceNote: func(senderPhone, targetPhone, audioPath string) error {
			sendCalls++
			return nil
		},
	}

	poller.doTriggerVoiceNote(triggerSourceFingerprint, "BTS", bucharestTime(2026, time.June, 17, 12, 0, 0), 1, 0)

	if sendCalls != 1 {
		t.Fatalf("SendVoiceNote() calls = %d, want 1", sendCalls)
	}
	if len(alerter.infoEvents) != 0 {
		t.Fatalf("AlertInfo() calls = %d, want 0", len(alerter.infoEvents))
	}
	if len(alerter.successEvents) != 1 {
		t.Fatalf("AlertSuccess() calls = %d, want 1", len(alerter.successEvents))
	}
	if !strings.Contains(alerter.successEvents[0].Message, "Trigger: "+triggerSourceFingerprint) {
		t.Fatalf("AlertSuccess().Message = %q, want trigger source", alerter.successEvents[0].Message)
	}
	if len(alerter.criticalEvents) != 0 {
		t.Fatalf("AlertCritical() calls = %d, want 0", len(alerter.criticalEvents))
	}
}

func TestPoller_doTriggerVoiceNote_ReportsOnlyFailureAfterSendError(t *testing.T) {
	audiosDir := t.TempDir()
	personDir := filepath.Join(audiosDir, "test-sender")
	_ = os.MkdirAll(personDir, 0o755)
	if err := os.WriteFile(filepath.Join(personDir, "test.ogg"), []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	alerter := &recordingAlerter{}
	sendCalls := 0
	poller := &Poller{
		TargetPhone: "+40770661491",
		StateMgr:    createMockStateMgr(),
		Alerter:     alerter,
		AudiosDir:   audiosDir,
		SendVoiceNote: func(senderPhone, targetPhone, audioPath string) error {
			sendCalls++
			return fmt.Errorf("boom")
		},
	}

	poller.doTriggerVoiceNote(triggerSourceMetadata, "BTS", bucharestTime(2026, time.June, 17, 12, 0, 0), 1, 0)

	if sendCalls != 1 {
		t.Fatalf("SendVoiceNote() calls = %d, want 1", sendCalls)
	}
	if len(alerter.infoEvents) != 0 {
		t.Fatalf("AlertInfo() calls = %d, want 0", len(alerter.infoEvents))
	}
	if len(alerter.successEvents) != 0 {
		t.Fatalf("AlertSuccess() calls = %d, want 0", len(alerter.successEvents))
	}
	if len(alerter.criticalEvents) != 1 {
		t.Fatalf("AlertCritical() calls = %d, want 1", len(alerter.criticalEvents))
	}
	if !strings.Contains(alerter.criticalEvents[0].Message, "Trigger: "+triggerSourceMetadata) {
		t.Fatalf("AlertCritical().Message = %q, want trigger source", alerter.criticalEvents[0].Message)
	}
}

func TestPoller_prepareStartStateHonorsKillSwitch(t *testing.T) {
	stateMgr := createMockStateMgr()
	stateMgr.Update(func(s *AppState) {
		s.KillSwitchActive = true
		s.Status = StatusKilled
	})

	poller := &Poller{StateMgr: stateMgr}

	if shouldPoll := poller.prepareStartState(); shouldPoll {
		t.Fatal("prepareStartState() should skip immediate polling when kill switch is active")
	}

	state := stateMgr.Get()
	if state.Status != StatusKilled {
		t.Fatalf("status = %q, want %q", state.Status, StatusKilled)
	}
}

func TestPoller_prepareStartStateSetsPollingWhenActive(t *testing.T) {
	stateMgr := createMockStateMgr()
	poller := &Poller{StateMgr: stateMgr}

	if shouldPoll := poller.prepareStartState(); !shouldPoll {
		t.Fatal("prepareStartState() should allow immediate polling when kill switch is inactive")
	}

	state := stateMgr.Get()
	if state.Status != StatusPolling {
		t.Fatalf("status = %q, want %q", state.Status, StatusPolling)
	}
}

func TestNormalizePhoneNumber(t *testing.T) {
	tests := []struct {
		name  string
		phone string
		want  string
	}{
		{
			name:  "Romanian standard format",
			phone: "0762631673",
			want:  "40762631673",
		},
		{
			name:  "International format with plus",
			phone: "+40762631673",
			want:  "40762631673",
		},
		{
			name:  "Format with dashes and spaces",
			phone: "+40 762-631-673",
			want:  "40762631673",
		},
		{
			name:  "Format with brackets",
			phone: "(0762) 631 673",
			want:  "40762631673",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePhoneNumber(tt.phone); got != tt.want {
				t.Errorf("normalizePhoneNumber() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeTriggerValueDiacritics(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "Ascultă ProFM în fiecare zi pentru muzică bună și concursuri",
			want:  "asculta profm in fiecare zi pentru muzica buna si concursuri",
		},
		{
			input: "ĂÂÎȘȚ  ăâîșț  ŞŢ şţ",
			want:  "aaist aaist st st",
		},
		{
			input: "  Muzică   &  Concursuri:   Ștefan  ",
			want:  "muzica & concursuri: stefan",
		},
	}

	for _, tt := range tests {
		got := normalizeTriggerValue(tt.input)
		if got != tt.want {
			t.Errorf("normalizeTriggerValue(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}

	triggerValuesMatch := func(left, right string) bool {
		left = normalizeTriggerValue(left)
		right = normalizeTriggerValue(right)
		if left == "" || right == "" {
			return false
		}
		return strings.Contains(left, right) || strings.Contains(right, left)
	}

	if !triggerValuesMatch("Ascultă hitul către Londra", "asculta hitul catre londra") {
		t.Error("TriggerValuesMatch should match regardless of diacritics")
	}
	if !triggerValuesMatch("Muzică bună și concursuri", "muzica buna si concursuri") {
		t.Error("TriggerValuesMatch should match regardless of diacritics and special characters")
	}
}

func createMockStateMgr() *StateManager {
	sm := NewStateManager()
	personID := int64(1)
	sm.Update(func(s *AppState) {
		s.Persons = []Person{{ID: personID, Name: "Test Sender", Slug: "test-sender"}}
		s.Connections = []WAConnectionState{{
			Phone:             "+40700000001",
			WhatsAppConnected: true,
			Status:            StatusConnected,
			PersonID:          &personID,
			PersonName:        "Test Sender",
			PersonSlug:        "test-sender",
		}}
	})
	return sm
}
