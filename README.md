tre sa facem cuvma sa citim ce ne da profmu

## Development

[Mise](https://mise.jdx.dev/) is the single entrypoint for the development
toolchain and project workflows. Install Mise itself, clone with submodules,
then run:

```sh
mise trust
mise run setup
```

`mise.toml` pins tools shared by local development and automation.
`mise.dev.toml` adds workstation-only tools and is selected by `.miserc.toml`.
CI selects `mise.ci.toml` with `MISE_ENV=ci`. Production does not depend on
Mise: the application and Whisper Dockerfiles remain the runtime contract.

Useful commands:

```sh
mise tasks                 # list every workflow
mise run dev               # live reload
mise run test              # package tests
mise run ci                # the same verification pipeline as GitHub Actions
mise run run               # Docker Compose development stack
mise run fly:status        # production status
```

Mise provisions all portable CLIs. `mise run doctor` checks the system-level
prerequisites it cannot own: Git, Docker with Compose, a C compiler, Make, and
Perl. Go-native tools such as templ and gopls are pinned in `go.mod` and invoked
through `go tool`, so they never depend on an untracked global installation.

## Constitutional Rules
1. **Instant Notification Regardless of Send Outcome:** Even if automatic voicenotes fail, daily limits are hit, or the kill-switch is active, the user MUST get an instant notification that a contest song is playing. This allows for manual intervention with no noise.
2. **Reduce Annoyance/Spam:** If WhatsApp disconnects, deduplicate and rate-limit alerts so the user doesn't get repeatedly spammed. Send the alert only on the first disconnection.
