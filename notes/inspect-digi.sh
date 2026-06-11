urls=("http://edge76.rcs-rds.ro:84/profm/chillfm.mp3" "http://edge76.rcs-rds.ro:84/profm/dancefm.mp3" "http://edge76.rcs-rds.ro:84/profm/music-fm.mp3" "http://edge76.rcs-rds.ro:84/profm/profm.mp3")

for url in "${urls[@]}"; do
  ffprobe -v quiet -print_format json -show_format -show_streams $url
done
