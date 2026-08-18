# Coverage history

Append a row after coverage changes. Always record the tested commit and use a
clean environment so local alert credentials cannot change which branches run.

## Unit suite

Command:

```sh
go test -count=1 -coverprofile=coverage.out ./pkg/poller
```

| Commit | Overall | Alerter | Audio | Fingerprint | Media | State | HTTP transcription | WS transcription |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `3af6530` | 31.1% | 23.0% | 62.0% | 13.5% | 55.2% | 70.3% | 67.7% | 80.9% |
| `e783eee` | 34.3% | 33.3% | 78.3% | 34.4% | 87.4% | 93.2% | 77.4% | 80.9% |
| `7650183` | 35.8% | 95.3% | 78.3% | 34.4% | 87.4% | 93.2% | 77.4% | 80.9% |

## No-WhatsApp E2E suite

Command:

```sh
go test -count=1 -coverprofile=coverage.out -tags=e2e ./pkg/poller
```

| Commit | Overall | Alerter | Audio | Fingerprint | Media | State | HTTP transcription | WS transcription |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `3af6530` | 53.6% | 23.0% | 67.4% | 80.2% | 85.1% | 70.3% | 67.7% | 89.7% |
| `7650183` | 56.6% | 95.3% | 83.6% | 81.6% | 88.5% | 93.2% | 77.4% | 89.7% |

The saved local HTML report generated at `3af6530` showed 72.2% for
`alerter.go` because the old E2E test loaded the real `.env` and traversed live
Telegram and email delivery branches. That result is not a safe reproducible
baseline. Commit `7650183` covers those branches with injected local fakes.
