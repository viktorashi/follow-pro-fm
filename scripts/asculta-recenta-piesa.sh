curl -s "https://api.profm.ro/api/v1/radios/article/2918?appVersion=1.0.0&platform=android" | jq '.data.epg | {title: .playerExtendedSongTitle, subtitle: .playerExtendedSongSubtitle}'
