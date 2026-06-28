# Action Plan - ProFM Go Enhancements

This document maps out the strategy to design, build, test, and verify the enhancements requested in ORIGINAL_REQUEST.md.

## Execution Strategy

We will use the **Project Pattern** (Dual Track):
1. **E2E Testing Track**: Spawns E2E Testing Orchestrator to define the E2E test infra, feature inventory, test cases (Tiers 1-4), and produce `TEST_READY.md`.
2. **Implementation Track**: Decomposes work into sequential and parallel milestones, implementing each and executing E2E tests once `TEST_READY.md` is available.
3. **Audit Gating**: Run Forensic Auditor (`teamwork_preview_auditor`) on each milestone gate to ensure absolute implementation integrity.
4. **Adversarial Hardening**: Run Tier 5 (Challenger-driven) hardening for code coverage and bug hunting once Tiers 1-4 pass.

## Milestones

| Milestone | Scope / Features | Dependencies | Strategy |
|---|---|---|---|
| M0: E2E Test Suite | Create test runner, feature coverage (Tier 1), boundaries (Tier 2), cross-features (Tier 3), real workloads (Tier 4) | None | Spawns E2E Testing Orchestrator |
| M1: WhatsApp & ffmpeg | R1: git submodule ffmpeg, build script, dockerfile, creation_time, waveform extraction, unit tests | None | Spawns Sub-Orchestrator for M1 |
| M2: Multi-WhatsApp | R2: multi whatsmeow client sessions, DB isolation, QR pairing, per-phone directories, non-recursive lookup | M1 | Spawns Sub-Orchestrator for M2 |
| M3: Buffer & Fingerprinting | R3 & R4: Dashcam recording, signature gathering/dashboard, cropping endpoints, fingerprint match detection, metadata de-duplication, test fixtures | M2 (for client routing), M1 | Spawns Sub-Orchestrator for M3 |
| M4: RNG Selection | R5: campaign-days scheduler, DB persistence, backfill logic, view/edit endpoints, campaign constraints | M3 | Spawns Sub-Orchestrator for M4 |
| M5: Dashboard Upload | R6: form/endpoint for phone audio upload | M3 | Spawns Sub-Orchestrator for M5 |

## Coordination and Workspaces
- All coordination files are located under `.agents/` folder.
- Sub-orchestrators and workers will create directories under `.agents/` following naming conventions.
- Standard Project Pattern: Explorer -> Worker -> Reviewer loop per milestone.
