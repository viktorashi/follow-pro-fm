tre sa facem cuvma sa citim ce ne da profmu

## Scop

Gen a facut o companie pro-fm cum ca trebuie sa asculti "piesa de concurs" la radio, si sa trimiti un mesaj vocal, ca sa castigi o excursie + bilete de concert.

Asa ca asta asculta radioul, si trimite automat voicenote-uri (dintr-o lista de d-alea salvate) folosind [whatsmeaw](https://github.com/tulir/whatsmeow).

Are mai multe structuri care implementeaza "ContestChecker" si merge fiecare pe goroutina lui, fiecare in parte:

- verifica metadatele trimise de ei pe streamu audio.
- compara sound signateru cu cv mate de n-o inteleg folosind ffmpeg
- citeste transcriptii lol

  si e survival of the fittest intre modurile astea de checkuit

  -----

  n-o sa inteleg nimic cand citesc asta peste 75 de ani
  


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
mise run tofu:plan         # preview OpenTofu infrastructure plan
mise run tofu:apply        # apply declarative OpenTofu infrastructure
mise run fly:status        # production status
```

Mise provisions every portable CLI and shell utility used by project tasks,
including templ, gopls, Docker CLI and Compose, Clang, Make, Perl, Git, curl,
Bash, Zsh, GNU coreutils, findutils, awk, grep, sed, and tar. Go-native tools
remain pinned in `go.mod` as well, and CI verifies those versions agree with
Mise. The Docker daemon itself remains host infrastructure; tasks that use it
connect to it directly without a separate validation step.

## Constitutional Rules
1. **Instant Notification Regardless of Send Outcome:** Even if automatic voicenotes fail, daily limits are hit, or the kill-switch is active, the user MUST get an instant notification that a contest song is playing. This allows for manual intervention with no noise.
2. **Reduce Annoyance/Spam:** If WhatsApp disconnects, deduplicate and rate-limit alerts so the user doesn't get repeatedly spammed. Send the alert only on the first disconnection.
