# Real-Device Validation Runbook — June 26, 2026

This runbook covers the remaining checks that cannot be fully proven by unit tests, Docker builds, or mock runtime smoke tests.

As of **June 26, 2026**, the currently active campaign window from the repo config is:

- `BTS`: **June 15, 2026** through **June 26, 2026**

Later configured windows are:

- `Ariana`: **July 20, 2026** through **July 31, 2026**
- `The Weeknd`: **August 10, 2026** through **August 21, 2026**

## Remaining Unproven Areas

1. Real WhatsApp delivery on actual linked devices
2. Real contest intro fingerprint triggering against the live stream
3. End-to-end behavior during an actual campaign turn with the real target number

## Preconditions

- You are on branch `dev`
- `just test` passes
- `just smoke-live-mock` passes
- The target phone has recent chat history with the sender phone
- The sender phone is another number you are comfortable linking through `whatsmeow`
- The target number is reachable and not blocked/restricted

## 1. Warm Up the Real Target Number

This is specifically to reduce the risk of the earlier WhatsApp `463` reachout timelock issue.

From the real sender phone and the real target phone:

1. Send at least one manual WhatsApp text from the sender to the target
2. Send at least one manual WhatsApp text from the target back to the sender
3. Send one manual real WhatsApp voice note from the sender to the target
4. Confirm the target phone can play it normally

Do not proceed if the target phone has never replied.

## 2. Pair the Real Sender Device

Start the app without mock mode:

```sh
PORT=8080 \
BASE_URL=http://127.0.0.1:8080 \
TARGET_PHONE=+40770661491 \
ADMIN_PASSWORD='<your-admin-password>' \
./pro-fm-poller
```

Then:

1. Open the dashboard
2. Add the real sender phone if it is not already present
3. Scan the QR code from the real WhatsApp device
4. Confirm the dashboard shows the sender as connected

Abort if:

- pairing loops repeatedly
- the sender drops back to pairing-required immediately
- the dashboard never shows connected state

## 3. Real Delivery Spot Check

This validates the real send path independently of campaign timing.

Preparation:

1. Ensure there is at least one known-good `.ogg` voice note in the sender's active audio pool
2. Keep the target phone in hand
3. Watch app logs live

Then trigger a controlled send only if you are comfortable doing so in the current environment. The key success criteria are:

1. WhatsApp send completes without `463`
2. The target phone receives a native green voice note bubble
3. The waveform is visible on the target phone
4. The message is playable
5. The used-audio rotation moves the chosen content out of the active pool
6. The DB send-state updates without allowing immediate resend for the same campaign artist

If the send fails:

- check whether the error is still `463`
- verify recent mutual chat history again
- verify the sender is truly connected and logged in
- verify the target number format resolves correctly

## 4. Fingerprint Trigger Validation During a Real Campaign Turn

This is the main remaining behavior check for the live stream.

During an active campaign day and time window:

1. Start the app with the real stream and real API
2. Keep dashboard and logs open
3. Confirm `GatheringSignatures` is enabled if you are still collecting/refining intros
4. Wait for a real campaign intro/song turn

Expected success behavior:

1. A clear log line appears showing fingerprint detection before metadata
2. The matching campaign artist is recognized
3. Only one send path is used
4. The later metadata event is ignored for that same turn
5. No duplicate voice note is sent

Expected metadata-only fallback behavior:

1. If fingerprinting misses the intro, metadata can still trigger the turn
2. A dashcam/unreviewed capture is saved
3. The capture is available through the dashboard/API review paths

Abort conditions:

- duplicate send on one campaign turn
- resend for the same campaign artist without another artist in between
- same voice note content being reused
- fingerprint trigger plus metadata trigger both sending

## 5. Post-Run Integrity Checks

After any real-device run:

1. Confirm no duplicate content hash was reused
2. Confirm no immediate resend happened for the same campaign artist
3. Confirm the target received at most one message for the observed campaign turn
4. Confirm the sender remains linked and stable
5. Check the dashboard audio counts and radio log history

Useful commands:

```sh
just test
just smoke-live-mock
docker build -t pro-fm-poller:test .
```

## 6. If You Want a Safer First Real Run

Prefer this order:

1. Real PRO FM stream + `MOCK_WHATSAPP=true`
2. Real sender pairing + no forced send yet
3. One deliberate real delivery spot check
4. One live campaign-window observation

That keeps the highest-risk variable, real WhatsApp sending, as the last step instead of the first.
