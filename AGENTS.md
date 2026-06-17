Never merge / push onto main by yourself unless specifically prompted otherwise.
That's what's actually deploying to main through CI / CD
If you're given a long multitude of tasks to do, make atomic, structured ordered commits for each of those.

The purpose of this project is to automatically listen to songs broadcasted by PRO FM during the specific campaign dates and hours, and if a song by a particular campaign-specific artist pops up, it's supposed to randomly send one of the voice messages in `/data/audio` as a whatsapp voicenote to the configured TARGET_PHONE.

Whichever change you make, keep in mind with utmost iportance, that the state of which voicenotes have been sent, or are going to be sent is 100% global, and should not be EVER broken by sending the same voicenote twice. Or spamming (even different) voicenotes over and over again over a short period of time.

Instead some other artists' songs need to be playing in between when you redetect a new song from the campaign artist.

The dates are given in main as so:

```go
 // Load campaigns in memory
 activeCampaigns := []poller.Campaign{
  {StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
  {StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana"},
  {StartDate: "10-08-2026", EndDate: "21-08-2026", Artist: "The Weeknd"},
 }

```

Also these rules, which you must follow strictly are in: `rulez/` in some PDF's or whatever else i end up adding in there.

<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan
<!-- SPECKIT END -->
