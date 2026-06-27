Aight, i wanted you to figure out which of the following tasks can be parallizable, with the emphasis on making it actual work, since it hasn't now for a few days (see the logs provided in the issue) and enabling support for multiple whataspp conenctions to be able to send voicenotes FROM multiple phone numbers, onto the same target number.

<https://github.com/viktorashi/follow-pro-fm/issues/2>

all the pics are found in pics/ here

try the context thing for each of the subagent, looping and revising until they get everything up and ready

If if you are to give them sepparate worktrees make sure each one runs just setup-dev before doing anything. Anything you do never merge into main. Also don't let them push their sepparate branches.

Don't let them run e2e-tests by themselves since that would require me to be at the computer / with the phone to confirm their reception

Also, if for the audio-buffer thingy any version of ffmpeg is required, try finding out what combination of modules can be combined and compiled to get the samllest possible distro of ffmpeg that serves all of our purposes that the app requires.

Regarding one of the screenshots another model suggested:
Based on the log shown in the screenshot:

```otherone

1. Campaign Triggered: At 17:31:39 , the campaign detected that a song by the target artist "BTS" was playing ( [CAMPAIGN ALERT] BTS is playing!
   (Match 5/6 for today) ).
2. Action Taken: The system attempted to send the WhatsApp voice note /data/audios/WhatsApp Ptt 23.ogg to the target number +40771001872
   (resolved to JID <40771001872@s.whatsapp.net> ).
3. Warnings:
   • ffmpeg was not found in the system's $PATH , meaning the system failed to inject metadata (to randomize the file hash and update its
   creation time) and fell back to sending the original file.
4. Error (Failure):
   • The actual transmission failed after three retry attempts with the error: server returned error 463 ( NackCallerReachoutTimelocked ).

### What does "Error 463" ( NackCallerReachoutTimelocked ) mean?

This is a server-side protection mechanism by WhatsApp. It occurs when:

• You attempt to send a message to a "cold" number (a contact with whom you do not have any established/recent conversation history).
• The WhatsApp servers require privacy validation tokens ( tctoken / cstoken ) to approve outgoing messages to new contacts, and these are missing
or have not yet been exchanged/stored.

### Recommended Next Steps

1. Warm up the target number: Send a manual message from the target phone to the sender phone (or vice versa) to establish a mutual chat history.
   This creates the necessary conversation context and exchanges the required privacy tokens.
2. Install ffmpeg : Ensure ffmpeg is installed and available in the system $PATH so the metadata injection doesn't fail and the files get
   sent as unique voice notes.

────────────────────────────────────────────────────────────

> what woud be the most miniimal version of ffmpeg we could have / compile ourselves with the scrictly neccesary modules to do ecxaclty what is asked
> right then and there?

▸ Thought for 2s, 425 tokens
Analyzing Minimal ffmpeg

● WebSearch(ffmpeg compile minimal disable everything copy muxer demuxer) (ctrl+o to expand)

▸ Thought for 3s, 1.1k tokens
Defining the Scope
To achieve exactly what is requested (copying the audio streams from an Ogg file while changing metadata/timestamps), the minimal compilation
configuration for ffmpeg would be:

### 1. Minimal ffmpeg Configure Command

You can disable everything and only enable the file protocol, the Ogg container, and the Opus parser (as WhatsApp voice notes are Opus-in-Ogg):

    ./configure \
      --disable-everything \
      --disable-doc \
      --disable-programs \
      --disable-devices \
      --disable-avdevice \
      --disable-swresample \
      --disable-swscale \
      --disable-postproc \
      --disable-avfilter \
      --disable-network \
      --enable-ffmpeg \
      --enable-protocol=file \
      --enable-demuxer=ogg \
      --enable-muxer=ogg \
      --enable-parser=opus

• Why this works: Disabling all decoders, encoders, and filters keeps the binary extremely small (often around 1–2 MB). Since you are running with
-c copy , no audio decoding/encoding modules are needed at all; it just reads the Ogg pages ( demuxer=ogg ), parses the Opus packets ( parser=opus
), and packages them back into an Ogg container ( muxer=ogg ) via file I/O ( protocol=file ).

```

If anyhting else needs to be added, please make sure the corresponding ffmpeg compilation is extremely DRY in regards to just using it in unit-testing and locally as well as what's built for inside the container, and not deduping the logic. You could add ffmpeg as a submodule in that regard then.

```actual-issue
``- Nu se mai trimit deloc voicenoteurile lol, gen primesc  notificare ca s-a dat piesa de concurs, dar numa nimic ciuciu, ma uit pe telefon si chiar nu s-a trimis nimic, trebuie sa ma pun sa trimit eu manual.



<img width="1088" height="163" alt="Image" src="https://github.com/user-attachments/assets/30d11c9c-843c-47eb-b999-e5c833e1a247" />

<img width="1177" height="143" alt="Image" src="https://github.com/user-attachments/assets/510022b1-f3ef-4060-b278-a2ed30517174" />

chiar daca din E2E-Testing merge super bine n-are treaba


------------------------------------------------

- Dupa as vrea sa trimit de la mai multe numere catre acelasi, ca sa-mi cresc practic sanele de concurs

- O data ca nu stiu exact tot timpul cum arata si introu si outro-u
Pune un buffer circular de 3 minute de filmeaza constant, dar care incepe dupa sa continue sa filmeze, extizand bufferu inca vreo 4 miunte DACA aude o piesa pe care o cautam (de la artistu respectiv)
Gen efectiv ca la camera de dashboard de la masina care filmeaza constant, sterge ce a fost inainte, dar daca apesi pe buttonu de record ca CEVA INTERESANT S-A INTAMPLAT dupa continue sa filmeze mai lung, si dupa salveaza filmarea aia persistent.

- Nu avem metoda usoara sa uploadam audouri noi

- Putem doar o singura conexiunea de wapp o data, daca vrem sa punem mai multe numere, fiecare cu audiourile lui?

- Deocamdata nu detecteaza decat DUPA ce a inceput sa se transmita metadatele alea de "ce melodie - artist se aude?" ... Am am observat ca se trimit destul de tarziu DUPA ce s-a dat deja "startul" la concurs, si piesa se aude gen deja, cel mai bine ar fi sa facem super bun pasul 1, ca sa culegem date de sound signature-ul audio-ului de intro respectiv, si dupa sa-l folosim sa detectam ca ba VINE AUDIOU

- Nu se vad audiowaveurile alea la vocale for some reason

Gen uite aici primul a fost trimis automat, si al doilea, manual de mine chiar filmandul in momentul ala:
<img width="465" height="192" alt="Image" src="https://github.com/user-attachments/assets/e4ad4c6d-02b6-4fae-be74-60377639c026" />

poate asta ma descalifica ca shadowban or something who knows? poate pentru ca dupa ce sa da hold-down la "butonu pentru vocal" dupa aceea doar sa da instant drop in la audio fara sa se mai formeze waveformu ala.

- Nu mai trimite 1000 de alerteruri fix inainte sa tirmiti lor mesaju pe whatsapp. Prioretizeaza voicenoteu- si dabea dupa raporteaza un singur raport.


- Aaa, also sa faci niste mega Otel pe faptul daca "chiar se detecteaza bine intro-u inainte sa se vada in metadate piesa cum canta" Pentru at that point, daca ajuge 100%, nu mai trebuie deloc sa ne uitam la piesa, si doar sa ascultam audio-ul.

- Also loguri super clare de la ce ore apar piesele, si ce piese mai exact (daca ajungem sa ascultam doar intr-oul, trebiue sa mai dam query la metadate pentru asta doar atunci cand incepem sa auzim introu)

- Dupa, daca devine chiar prea bun si OP, ceva RNG, care alege pentru fiecare zi, la care dintre piesele zilei sa trimita, si sa nu trimita chiar la toate, pt ca dupa ar fi chiar prea ciudat de accurate. Asta ar fi populata pentru absolut toata perioada tuturor campaniilor, direct la startup de program, si incarcam toate zilele alea in memorie direct si stau acolo persistant, si daca cumva cand pornim prima data si ajung sa nu fie pre-poulate toate campaniile cu algerile din ce zile, atunci facem noi alt RNG si-l populam doar cu ce lipseste de acolo.`
```
