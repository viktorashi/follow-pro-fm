import urllib.request
import json
import time

def get_now_playing():
    url = "https://api.profm.ro/api/v1/radios/article/2918?appVersion=1.0.0&platform=android"
    
    try:
        req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
        with urllib.request.urlopen(req) as response:
            data = json.loads(response.read().decode('utf-8'))
            epg = data.get('data', {}).get('epg', {})
            
            artist = epg.get('playerExtendedSongTitle', 'Unknown Artist')
            song = epg.get('playerExtendedSongSubtitle', 'Unknown Song')
            
            # The subtitle sometimes includes the year, e.g. "2000 - LASA-MA PAPA LA MARE"
            # Let's clean it up slightly if needed
            if ' - ' in song:
                parts = song.split(' - ', 1)
                if parts[0].isdigit():
                    song = parts[1]
            
            return f"{artist} - {song}"
            
    except Exception as e:
        return f"Error fetching data: {e}"

if __name__ == "__main__":
    print("Fetching Now Playing from Pro FM...")
    print("-" * 40)
    current_song = ""
    
    try:
        while True:
            song = get_now_playing()
            if song != current_song:
                print(f"[{time.strftime('%H:%M:%S')}] {song}")
                current_song = song
            time.sleep(10) # Poll every 10 seconds
    except KeyboardInterrupt:
        print("\nExiting...")
