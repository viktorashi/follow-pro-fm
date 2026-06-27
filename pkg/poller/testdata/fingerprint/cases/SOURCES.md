`commons_boing_match`
- Source: `Boing raw.ogg` from Wikimedia Commons
- URL: `https://commons.wikimedia.org/wiki/Special:FilePath/Boing%20raw.ogg`
- Fixture prep: `stream.mp3` is a full MP3 transcode of the source; `signature.mp3` is a cropped middle slice re-encoded at a different bitrate.

`commons_boing_vs_cash`
- Stream source: `Boing raw.ogg` from Wikimedia Commons
- Stream URL: `https://commons.wikimedia.org/wiki/Special:FilePath/Boing%20raw.ogg`
- Signature source: `Cash register.ogg` from Wikimedia Commons
- Signature URL: `https://commons.wikimedia.org/wiki/Special:FilePath/Cash%20register.ogg`
- Fixture prep: `stream.mp3` is the same Boing MP3 as above; `signature.mp3` is a cropped Cash Register slice re-encoded as MP3.

`commons_boing_long_match`
- Source: derived from `commons_boing_match/stream.mp3`
- Fixture prep: `stream.mp3` is the same Boing MP3; `signature.mp3` is a much longer 2.75s middle crop re-encoded as MP3.

`commons_boing_long_vs_waveform`
- Stream source: derived from `commons_boing_match/stream.mp3`
- Signature source: derived from `pkg/poller/testdata/waveform_sample.ogg`
- Fixture prep: `stream.mp3` is the same Boing MP3; `signature.mp3` is a full MP3 transcode of `waveform_sample.ogg` used as a long negative.

`waveform_long_match`
- Source: derived from `pkg/poller/testdata/waveform_sample.ogg`
- Fixture prep: `stream.mp3` is a full MP3 transcode of `waveform_sample.ogg`; `signature.mp3` is a 3.60s interior crop re-encoded as MP3.

`commons_boing_long_nearmiss_waveform`
- Stream source: `Boing raw.ogg` from Wikimedia Commons
- Stream URL: `https://commons.wikimedia.org/wiki/Special:FilePath/Boing%20raw.ogg`
- Signature source: derived from `pkg/poller/testdata/waveform_sample.ogg`
- Fixture prep: both files are MP3 CBR `96k`; `stream.mp3` is a full Boing transcode, `signature.mp3` is a 3.60s crop from unrelated waveform content.

`waveform_long_nearmiss_boing`
- Stream source: derived from `pkg/poller/testdata/waveform_sample.ogg`
- Signature source: `Boing raw.ogg` from Wikimedia Commons
- Signature URL: `https://commons.wikimedia.org/wiki/Special:FilePath/Boing%20raw.ogg`
- Fixture prep: both files are MP3 CBR `96k`; `stream.mp3` is a full waveform transcode, `signature.mp3` is a 3.60s crop from unrelated Boing content.
