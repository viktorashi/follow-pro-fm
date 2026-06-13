package poller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
			time: time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC), // Wed
			want: true,
		},
		{
			name: "Inactive weekend (Saturday)",
			time: time.Date(2026, time.June, 20, 12, 0, 0, 0, time.UTC), // Sat
			want: false,
		},
		{
			name: "Inactive before 07:00",
			time: time.Date(2026, time.June, 17, 6, 59, 59, 0, time.UTC), // Wed
			want: false,
		},
		{
			name: "Inactive after 20:00",
			time: time.Date(2026, time.June, 17, 20, 0, 1, 0, time.UTC), // Wed
			want: false,
		},
		{
			name: "Inactive before StartDate",
			time: time.Date(2026, time.June, 12, 12, 0, 0, 0, time.UTC), // Friday
			want: false,
		},
		{
			name: "Inactive after EndDate",
			time: time.Date(2026, time.June, 29, 12, 0, 0, 0, time.UTC), // Monday
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

func TestCampaign_IsActive_BadDates(t *testing.T) {
	c := Campaign{
		StartDate: "bad-date",
		EndDate:   "26-06-2026",
	}
	if got := c.IsActive(time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)); got != false {
		t.Errorf("IsActive with bad dates = %v, want false", got)
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
				_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"BTS","playerExtendedSongSubtitle":"Dynamite"}}}`))
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

			poller := &Poller{ApiURL: server.URL}
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
	poller := &Poller{ApiURL: "http://127.0.0.1:0"}
	_, err := poller.getNowPlaying()
	if err == nil {
		t.Error("Expected error for bad connection")
	}

	// Test NewRequest error (e.g., bad URL scheme)
	poller = &Poller{ApiURL: string([]byte{0x7f})}
	_, err = poller.getNowPlaying()
	t.Logf("err for \\x7f: %v", err)
	if err == nil {
		t.Error("Expected error for bad URL")
	}
}

func TestPoller_checkSong(t *testing.T) {
	// A Wednesday at 12:00 PM (Active time for campaigns)
	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)

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
			name:           "Same song playing again, should not trigger",
			mockArtist:     "BTS",
			mockTitle:      "Dynamite",
			currentSong:    &SongInfo{Artist: "BTS", Title: "Dynamite"},
			wantMatches:    0,
			wantVoiceCalls: 0,
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
			poller := &Poller{
				ApiURL:       server.URL,
				PollInterval: 1 * time.Millisecond,
				ActiveCampaigns: []Campaign{
					{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
				},
				TargetPhone: "+40762631673",
				SendVoiceNote: func(phone string, audioPath string) error {
					voiceCalls++
					if tt.simulateVoiceError {
						return fmt.Errorf("simulated network error sending audio")
					}
					return nil
				},
			}

			poller.checkSong(tt.currentSong, activeTime)

			if poller.matchesToday != tt.wantMatches {
				t.Errorf("matchesToday = %v, want %v", poller.matchesToday, tt.wantMatches)
			}
			if voiceCalls != tt.wantVoiceCalls {
				t.Errorf("voiceCalls = %v, want %v", voiceCalls, tt.wantVoiceCalls)
			}
		})
	}
}

func TestPoller_checkSong_DailyLimit(t *testing.T) {
	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"epg":{"playerExtendedSongTitle":"BTS","playerExtendedSongSubtitle":"Dynamite"}}}`))
	}))
	defer server.Close()

	voiceCalls := 0
	poller := &Poller{
		ApiURL:       server.URL,
		PollInterval: 1 * time.Millisecond,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
		matchesToday: 6,
		lastCheckDay: activeTime.YearDay(), // Prevent matchesToday from being reset
		TargetPhone:  "+40762631673",
		SendVoiceNote: func(phone string, audioPath string) error {
			voiceCalls++
			return nil
		},
	}

	currentSong := &SongInfo{}
	poller.checkSong(currentSong, activeTime)

	if voiceCalls != 0 {
		t.Errorf("Expected 0 voice calls due to daily limit, got %d", voiceCalls)
	}
	if poller.matchesToday != 6 {
		t.Errorf("matchesToday should remain 6, got %d", poller.matchesToday)
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
