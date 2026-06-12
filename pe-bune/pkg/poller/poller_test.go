package poller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPoller_checkSong(t *testing.T) {
	// A Wednesday at 12:00 PM (Active time for campaigns)
	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		mockArtist     string
		mockTitle      string
		currentSong    *SongInfo
		wantMatches    int
		wantVoiceCalls int
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
