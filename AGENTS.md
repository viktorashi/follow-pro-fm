Spawn all sub-agents with gpt 5.4-mini and NEVER in fast mode!! Always slow mode. As slow as possible

Alwayss, use the ponytail skill (It might have already been injected in your context via a lifecycle hook)

Never merge / push onto main by yourself unless specifically prompted otherwise.
That's what's actually deploying to main through CI / CD
For anything you're given, make atomic, structured ordered commits. Don't leave the worktree dirty.
Don't run tests (unless you literally just wrote them) / formatting / linting / typechecks / whatever manually. Committing will trigger the git hooks so don't worry about it.

Only amend commits if they've not yet been pushed. So that you NEVER have to FORCE-PUSH.

If asked to fix a regression, maybe first try using `git bisect` to find the commit that introduced it. Then look urself.

This project depends on a custom light build of `ffmpeg` with just what we need. Never change anything of the `ffmpeg` codebase, except the build args

Please make sure the way it runs locally + the tests correctly reproduce exactly in the Dockerfile, of what's actually gonna be running in prod.

The purpose of this project is to automatically listen to songs broadcasted by PRO FM during the specific campaign dates and hours, and if a song by a particular campaign-specific artist pops up, it's supposed to randomly send one of the voice messages in `/data/audio` as a whatsapp voicenote to the configured TARGET_PHONE.

Whichever change you make, keep in mind with utmost importance, that the state of which voicenotes have been sent, or are going to be sent is 100% global, and should not be EVER broken by sending the same voicenote twice. Or spamming (even different) voicenotes over and over again over a short period of time.

Instead some other artists' songs need to be playing in between when you redetect a new song from the campaign artist.

The dates are given in main as so:

```go
 // Load campaigns in memory
 activeCampaigns := []poller.Campaign{
  {StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
  {StartDate: "20-07-2026", EndDate: "31-07-2026", Artist: "Ariana"},
  {StartDate: "10-08-2026", EndDate: "21-08-2026", Artist: "The Weeknd"},
 }
 //posibil extensibil

```

Also these rules, which you must follow strictly are in: `rulez/` in some PDF's or whatever else i end up adding in there.

In case voicenotes don't get automatically sent, don't suggest sending a test number FROM the target one, cuz i don't control it.

- Notify the user INSTANTLY whenever a contest song is playing, NO MATTER WHAT (even if daily limits are hit, or kill switch is active, or if WhatsApp fails). The alert must be immediate so the user can manually send it if needed.
- DO NOT spam alerts on WhatsApp disconnects. Rate-limit/deduplicate alerts so the user only gets notified on the FIRST failure, reducing noise.
