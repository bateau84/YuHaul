---
type: design-spec
title: YuHaul Admin Interface Design Specification
description: "Human-centered design specification for the YuHaul web administration interface — a responsive, device-aware operational dashboard for managing YouTube subscription routing, inspecting processing decisions, and analyzing content discovery performance."
tags: [design-spec, web-ui, admin-dashboard, youtube]
status: accepted
timestamp: 2026-11-07T00:00:00Z
---

## Design Spec: YuHaul Admin Interface

### Experience Context

**Launch-scope reconciliation (2026-10-25, user decision):** US-011/US-012 remain accepted future-target design. Their per-video comparison of replay/backtest results with an earlier production outcome from the same YuHaul is deferred from first-use launch, not rejected or completed. Comparison-dependent passages below are retained as future targets, not launch acceptance checks. The baseline is an earlier same-YuHaul production outcome when available, never the old cron application's corpus or outcomes; imported titles support duplicate detection, not production-outcome comparison. This deferral does not weaken mode-aware simulation inspection, hypothetical/non-executed action labeling, or the actual subscription → discovery → routing → YouTube playlist flow. It does not defer replay/backtest generally or establish any implementation as complete.

**Problem Statement:**
A person who subscribes to many YouTube channels (primarily for music discovery) cannot efficiently discover which new videos are worth watching. Manually checking subscriptions is time-consuming; the same song appears across many channels under slightly different titles; and without historical tracking, there is no way to know whether a subscription or routing rule is actually producing valuable content. The user needs a system that automates the repetitive parts of content discovery — finding new videos, matching them to playlists, detecting duplicates — while keeping the user in full control of policy decisions and providing enough evidence to understand and trust every automated action.

**Experience Requirement:**
Enable the user to trust that their YouTube discovery system is operating correctly, configure routing rules with confidence, investigate why specific videos were accepted or rejected, and identify trends in channel quality — all without consulting documentation, switching tools, or second-guessing the system's behavior.

**Scenario 1 — Morning check-in (90 seconds):**
The user opens YuHaul on their phone while having coffee. They see the Overview: last run completed 6 hours ago, 42 videos processed, 3 accepted, 12 deferred (quota), 0 errors. They tap into Runs to see the latest run's details — the 3 accepted videos went to their "New Discoveries" playlist as expected. They glance at the error queue: empty. They close the app.

**Scenario 2 — Route tuning after noticing a gap (5 minutes):**
The user notices their "Ambient" route has accepted 0 videos in the last 2 weeks. On desktop, they navigate to Routes, edit the Ambient route's conditions, and lower the duration threshold from 10 minutes to 5 minutes. Before saving, they run a dry-run with the unsaved draft conditions. The dry-run shows 8 currently pending videos would now match. They save the route. They later check Analytics to see if acceptance rates improve.

**Scenario 3 — Investigating a specific rejection (3 minutes):**
The user remembers a specific channel that should have matched but didn't. They navigate to Analytics > Channels, find the channel, and see its recent videos. They click a specific video that was rejected — the Decision Detail view shows the evidence trail: "Route 'Music Discovery' condition 'title contains [genre keyword]' — NOT MATCHED. Route 'Ambient' condition 'duration > 10min' — NOT MATCHED. Result: rejected (no route matched)." They realize their route conditions need adjustment.

**Scenario 4 — Error triage and deferred work (2 minutes):**
The user checks the Errors view. One item needs account reconnection because stored renewal authorization was rejected, not merely because an access token expired. They open Settings > Account and reconnect. The failed work remains inspectable; reconnection does not prove it succeeded or make an uncertain playlist action safe to repeat. They also see the Deferred tab: 2 items deferred due to quota exhaustion, eligible for a later scheduled or manual run when quota is available. Routine access expiry instead renews automatically during production work; a temporary renewal failure explains that access is unavailable without claiming revoked consent.

**Scenario 5 — Backtest before committing a change (10 minutes):**
**Status:** Future-target comparison illustration; comparison against earlier same-YuHaul production outcomes is deferred from first-use launch. The scenario text is retained.

The user wants to add a new route for a niche genre. Before committing, they open Backtest, apply the proposed route configuration to 30 days of historical observations, and see: the new route would have caught 23 videos, 15 of which overlap with existing routes (already caught), 8 are new catches. They're satisfied and create the route.

**Scenario 6 — Channel advisory review (3 minutes):**
The user notices a "Low usefulness" advisory on the Analytics page for a channel they subscribed to months ago. The advisory notes: "This channel has produced 45 discoveries in the last 90 days, but only 2 were retained in designated rotation playlists. Consider whether this subscription still serves your interests." The user reviews the channel's recent videos, agrees, and unsubscribes directly from YouTube.

### Workflow Map

Derived from the 32 acceptance criteria. Each workflow names the surfaces and endpoints that serve it, and identifies capability gaps.

| # | Workflow | Surfaces / Endpoints | ACs Served | Capability Gap |
|---|----------|---------------------|------------|----------------|
| 1 | **Account Connection and Renewal** — OAuth connection, automatic production renewal, truthful recovery status | Settings > Account, Overview account summary, production callers; account endpoints follow Architecture | AC 1; BR-034, OC-006 | Status/renewal realization and assembled-path verification remain outstanding; endpoint names here are not new contracts |
| 2 | **Discovery & Processing** — Scheduled or manual run discovers videos, evaluates routes, executes actions | Overview (trigger), Runs (trigger + inspect), `POST /api/runs`, `GET /api/runs/:id`, `GET /api/runs/:id/stream` (SSE hints) | AC 2, 11, 12, 13, 14, 30, 32 | Existing durable facts support phase/status feedback; no overall completion denominator |
| 3 | **Route Management** — Create, edit, delete, reorder, enable/disable routes with conditions and actions | Routes (list + editor), `GET/POST/PUT/DELETE /api/routes`, `PUT /api/routes/reorder` | AC 3, 4, 5, 6 | None |
| 4 | **Exclusion Management** — Per-route exclusion rules (channels, videos, titles, words, duration) | Route Editor > Exclusions region | AC 7 | None |
| 5 | **Duplicate Detection** — Exact and fuzzy duplicate detection, corpus import | Settings > Data, Settings > Duplicate Threshold | AC 8, 9, 10 | None |
| 6 | **Run Scheduling** — Configurable cron schedule + manual runs | Settings > Schedule, Overview (next run), `GET/PUT /api/settings` | AC 16, 17 | None |
| 7 | **Quota Management** — Quota visibility, quota-aware processing, deferred work | Overview (quota bar), Analytics (quota ledger), Errors (deferred tab) | AC 18, 19, 30 | None |
| 8 | **Run Inspection** — Decision evidence, run detail, video outcome detail | Runs (list + detail), `GET /api/runs/:id/videos/:videoId` | AC 15, 28 | None |
| 9 | **Analytics & Statistics** — Run/route statistics, channel analysis, retention signals | Analytics (dashboard + channels tab) | AC 20, 21, 22 | None |
| 10 | **Temporal Analytics** — Historical statistics, trend visualization, config-history attribution | Analytics (trends), History (config versions) | AC 23, 24, 25 | None |
| 11 | **Dry-Run & Backtest** — Dry-run evaluation, historical backtesting, replay | Backtest (dual-mode), `POST /api/runs/dry-run`, `POST /api/backtest`, `POST /api/runs/replay` | AC 26, 27 | None |
| 12 | **Configuration History** — Version snapshots, diffs, linked runs | History (list + detail), `GET /api/config-versions` | AC 25 | None |
| 13 | **Error & Deferred Queue** — Inspectable error queue with retry/remove, deferred work queue | Errors (tabs + actions), `GET/POST/DELETE /api/errors` | AC 14, 30 | None |
| 14 | **Responsive UI** — Desktop sidebar + mobile bottom nav, card layout for tables | All views | AC 29 | None |
| 15 | **User Authority** — Evidence-backed advisories, no autonomous policy changes | Analytics > Channels (advisory cards), `GET /api/analytics/channels/:id` | AC 31 | None |
| 16 | **Outcome Distinction** — Distinguish rejected, deferred, failed, not-yet-evaluated | Decision Detail (three-layer model), Run Detail (summary badges) | AC 32 | None |

**Joint views (workflows that share a surface):**
- **Run Detail** serves workflows 2 (processing), 8 (inspection), 7 (quota cost), and 16 (outcome distinction)
- **Route Editor** serves workflows 3 (management) and 4 (exclusions)
- **Analytics Dashboard** serves workflows 9 (statistics), 10 (temporal), and 15 (advisories)
- **Settings** serves workflows 1 (account), 5 (duplicate threshold), 6 (schedule), and data import

**Workflow-map qualification:** The original endpoint mapping is intended-design coverage, not proof of implemented capability. Earlier same-YuHaul production-outcome comparison in workflow 11 is a retained future target deferred from first-use launch; see the Simulation Inspection capability boundary below. No old-application comparison baseline is implied.

---

### User Stories

#### Account & Onboarding

**US-001: Connect YouTube Account**
- **Actor:** YuHaul administrator (the single user)
- **Context:** First-time setup, or reconnection because renewal authorization is missing or confirmed rejected. Routine access expiry with available renewal is not a reconnection task.
- **Trigger:** User navigates to Settings > Account and clicks "Connect YouTube Account" or the existing "Reconnect" action when required
- **Desired outcome:** YouTube account is authenticated via OAuth2; YuHaul can discover subscriptions and manage playlists
- **Observable success:** Account status confirms usable authorization for the intended account. Scheduled/manual production work renews expired access automatically, including after restart and between calls; failure preserves work and distinguishes temporary unavailability from reconnection required. Connection success is not a promise that a run or playlist action has succeeded.
- **Source:** AC 1; [BR-034](/docs/requirements/yuhaul/br-034-automatic-youtube-token-refresh.md), [OC-006](/docs/requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md)

**US-002: Import Historical Corpus**
- **Actor:** YuHaul administrator migrating from the previous application
- **Context:** Existing corpus of 10,000+ titles in the prior application's SQLite database must be preserved for duplicate detection
- **Trigger:** User navigates to Settings > Data and uploads a SQLite database file
- **Desired outcome:** Historical titles are imported into the duplicate detection corpus; titles with video identifiers already present are identified and skipped; corpus integrity is preserved
- **Observable success:** Import summary shows, for this import's source rows, how many were newly inserted, how many video identifiers were already present in the destination (skipped), and how many failed; total corpus size reports the actual destination row count after import. Duplicate detection works identically for imported and native titles.
- **Source:** AC 9

#### Route Configuration

**US-003: Create a New Route**
- **Actor:** YuHaul administrator defining content routing rules
- **Context:** User wants to route videos matching specific criteria to a YouTube playlist
- **Trigger:** User navigates to Routes and clicks "New Route"
- **Desired outcome:** A new route is created with a name, conditions (field + operator + value), exclusions, and a playlist action; the route is added to the evaluation order
- **Observable success:** New route appears in the route list at the correct position; conditions are saved; route is enabled by default
- **Source:** AC 3, 4

**US-004: Configure Route Exclusions**
- **Actor:** YuHaul administrator refining route behavior
- **Context:** User wants to exclude specific channels, videos, titles, words, or durations from a route without affecting other routes
- **Trigger:** User opens a route editor and adds exclusion rules
- **Desired outcome:** Exclusion rules are saved with the route; excluded videos are skipped by this route but remain eligible for other routes
- **Observable success:** Exclusion list shows added entries; excluded videos do not match this route in subsequent runs; other routes still evaluate excluded videos
- **Source:** AC 7

**US-005: Set Route as Final**
- **Actor:** YuHaul administrator controlling evaluation flow
- **Context:** User wants a route to terminate further evaluation after a successful match (e.g., a "catch-all" route)
- **Trigger:** User toggles the "Final" flag on a route in the editor
- **Desired outcome:** After this route matches a video, subsequent routes in evaluation order are skipped for that video
- **Observable success:** Route shows "Final" badge; in Decision Detail, videos matched by this route show subsequent routes as "skipped (final route matched)"
- **Source:** AC 6

**US-006: Reorder Routes**
- **Actor:** YuHaul administrator adjusting evaluation priority
- **Context:** Route evaluation is order-dependent; user needs to change the sequence
- **Trigger:** User drags a route to a new position, or uses Move Up/Down buttons
- **Desired outcome:** Route evaluation order updates; a ConfigVersion snapshot is created
- **Observable success:** Route list reflects new order; configuration history shows the reorder event
- **Source:** AC 3

#### Duplicate Detection

**US-007: Adjust Fuzzy Match Threshold**
- **Actor:** YuHaul administrator tuning duplicate sensitivity
- **Context:** Fuzzy matching threshold controls how similar titles must be to count as duplicates
- **Trigger:** User adjusts the threshold slider in Settings > Duplicate Detection
- **Desired outcome:** New threshold is persisted and applied to all subsequent duplicate detection
- **Observable success:** Slider shows current value; toast confirms save; subsequent runs use the new threshold
- **Source:** AC 8

**US-008: Inspect Duplicate Provenance**
- **Actor:** YuHaul administrator understanding discovery credit
- **Context:** When the same content appears from multiple channels, the user needs to know which channel was the original discoverer
- **Trigger:** User navigates to Analytics > Channels > [channel] > [video]
- **Desired outcome:** Video detail shows whether this channel holds discovery credit (first source) or relevance credit (also published by others)
- **Observable success:** Per-video provenance badge shows "First discovered" or "Also published by [other channels]"; channel-level summary shows discovery credit vs relevance credit counts
- **Source:** AC 10

#### Processing & Runs

**US-009: Start a Processing Run**
- **Actor:** YuHaul administrator triggering content discovery
- **Context:** User wants to process pending videos now rather than waiting for the next scheduled run
- **Trigger:** User clicks "Run Now" in the header or Overview
- **Desired outcome:** A processing run starts; the user can follow its recorded phase and saved status, then inspect its outcomes without mistaking API spending for playlist success.
- **Observable success:** The acknowledged run opens in Run Detail; recorded phase/status remain visible through discovery even before selected inputs are known. A saved terminal status replaces active progress, and recorded video/action outcomes explain the result. Start acknowledgement, Accepted, and quota usage are not playlist-insertion confirmations.
- **Source:** AC 2, 17

**US-010: Run a Dry-Run with Draft Configuration**
- **Actor:** YuHaul administrator previewing changes before committing
- **Context:** User has unsaved route edits and wants to see what would happen
- **Trigger:** User clicks "Dry Run" and confirms the dialog
- **Desired outcome:** System processes pending videos against draft (unsaved) route configuration without mutating playlists, duplicate corpus, or statistics
- **Observable success:** Dry-run results show "Would have" language for actions; production state is unchanged; user can save the draft configuration if satisfied
- **Source:** AC 26

**US-011: Backtest Proposed Configuration**
**Status:** Accepted future target; comparison with earlier same-YuHaul production outcomes, including comparison-derived new/lost/net results, is deferred from first-use launch. Story text retained unchanged.

- **Actor:** YuHaul administrator evaluating routing changes against history
- **Context:** User wants to know what a configuration change would have caught or lost over the past 30 days
- **Trigger:** User navigates to Backtest, modifies proposed routes, selects date range, clicks "Run Backtest"
- **Desired outcome:** System replays retained historical observations against proposed configuration; results show new catches, lost catches, and net change
- **Observable success:** Comparison view shows actual vs proposed outcomes; user can inspect individual video differences; user can apply or discard the proposed configuration
- **Source:** AC 27

**US-012: Replay History with a Specific Configuration**
**Status:** Accepted future target; per-video alongside-original comparison means an earlier same-YuHaul production outcome when available and is deferred from first-use launch. Distinct non-production replay labeling remains in scope. Story text retained unchanged.

- **Actor:** YuHaul administrator re-evaluating history
- **Context:** User wants to see what a historical configuration would have produced on the same observations
- **Trigger:** User selects a configuration version and date range in Backtest > Replay mode
- **Desired outcome:** System reprocesses retained observations through the selected configuration; replay outcomes are labeled distinctly from production outcomes
- **Observable success:** Replay results show each video's replay outcome alongside its original production outcome (if available); replay is clearly marked as non-production
- **Source:** AC 27

#### Inspection & Explainability

**US-013: Inspect Why a Video Was Rejected**
- **Actor:** YuHaul administrator debugging routing gaps
- **Context:** User expected a video to match but it didn't
- **Trigger:** User navigates to Runs > [run] > [video] or Analytics > Channels > [channel] > [video]
- **Desired outcome:** Decision Detail shows the complete evidence trail: every route evaluated, every condition result (matched/not matched), and the final outcome
- **Observable success:** Evidence trail is readable top-to-bottom in evaluation order; each condition shows its result with a visual indicator; outcome is explained in plain language
- **Source:** AC 15, 28

**US-014: Understand Processing Outcome States**
- **Actor:** YuHaul administrator interpreting run results
- **Context:** Videos can be in multiple outcome states; the user needs to distinguish them
- **Trigger:** User views Run Detail or Decision Detail
- **Desired outcome:** The three-layer outcome model is visible: processing state, per-route decisions, and per-action execution; summary badges use distinct visual treatment for each state
- **Observable success:** Accepted, rejected, deferred, failed, and not-yet-evaluated are all visually distinct; not-yet-evaluated never appears as a negative routing decision; the user can tell at a glance what happened
- **Source:** AC 32

#### Analytics & Channel Quality

**US-015: View Trend Analytics**
- **Actor:** YuHaul administrator identifying patterns over time
- **Context:** User wants to see how routing performance changes over weeks or months
- **Trigger:** User navigates to Analytics > Trends
- **Desired outcome:** Temporal charts show acceptance rate, videos processed, route match rates, and quota consumption over the selected time range
- **Observable success:** Charts render with accurate data; time range selector works; charts have accessible text alternatives; data is interpretable without documentation
- **Source:** AC 23, 24

**US-016: Evaluate Channel Usefulness**
- **Actor:** YuHaul administrator assessing subscription value
- **Context:** User wants to know which channels produce content they actually keep
- **Trigger:** User navigates to Analytics > Channels
- **Desired outcome:** Channel list shows usefulness metrics (discovered, routed, retained, usefulness rate); channel detail shows retention signal provenance (user-driven vs YuHaul-routed)
- **Observable success:** Channel usefulness is quantified; retention provenance distinguishes genuine user retention from system-routed membership; advisory badges appear for low-usefulness channels
- **Source:** AC 21, 22, 31

**US-017: Review Channel Advisory**
- **Actor:** YuHaul administrator acting on system suggestions
- **Context:** System has identified a channel with low usefulness
- **Trigger:** User clicks a channel with an advisory badge
- **Desired outcome:** Advisory shows evidence-backed suggestion with supporting metrics; user can take action, dismiss, or ignore
- **Observable success:** Advisory text is specific and evidence-based; "Take action" opens the relevant external action (e.g., YouTube unsubscribe page); dismiss/ignore are distinct actions; no autonomous policy changes occur
- **Source:** AC 31

#### Quota & Error Management

**US-018: Monitor Quota Consumption**
- **Actor:** YuHaul administrator managing API budget
- **Context:** YouTube quota is limited (10,000 units/day); user needs visibility
- **Trigger:** User views Overview or Analytics
- **Desired outcome:** Quota consumption is visible at a glance (current usage, remaining, reset time); quota ledger breaks down consumption by operation type
- **Observable success:** Overview shows quota bar with percentage; Analytics shows detailed ledger; when quota is exhausted, deferred work is clearly indicated
- **Source:** AC 18, 19

**US-019: Triage Failed Processing**
- **Actor:** YuHaul administrator resolving processing failures
- **Context:** Videos failed during processing and need investigation
- **Trigger:** User navigates to Errors
- **Desired outcome:** Error queue shows failed items with error type, reason, and timestamp; user can retry or dismiss each item
- **Observable success:** Retry re-attempts processing; dismiss removes from active queue while preserving history; error details are specific enough to diagnose
- **Source:** AC 14

**US-020: Review Deferred Work**
- **Actor:** YuHaul administrator checking quota-deferred items
- **Context:** Some videos were deferred due to quota exhaustion
- **Trigger:** User navigates to Errors > Deferred tab
- **Desired outcome:** Deferred items show video, channel, deferral reason, and next-eligibility indicator; user can retry when quota allows or dismiss
- **Observable success:** Deferred items are clearly distinguished from errors; next-eligibility shows when the item will be retried; manual retry is available if quota permits
- **Source:** AC 13, 30

#### Configuration History

**US-021: Review Configuration Changes**
- **Actor:** YuHaul administrator interpreting historical statistics
- **Context:** Route changes affect statistics; user needs to correlate changes with outcomes
- **Trigger:** User navigates to History
- **Desired outcome:** Chronological list of configuration changes with timestamps, summaries, and linked runs; user can diff any two versions
- **Observable success:** Each version shows what changed, when, and which runs executed under it; diff view highlights additions/removals/modifications; historical statistics are interpretable in configuration context
- **Source:** AC 25

#### Responsive Access

**US-022: Use on Mobile**
- **Actor:** YuHaul administrator checking system status on the go
- **Context:** User wants to check runs, errors, and analytics from their phone
- **Trigger:** User opens YuHaul on a mobile device
- **Desired outcome:** Interface adapts to mobile viewport; primary navigation is accessible via bottom bar; data tables convert to cards; forms are usable with touch
- **Observable success:** All core functionality is reachable; touch targets are adequate (44×44px minimum); text is readable without zooming; no horizontal scrolling on primary content
- **Source:** AC 29

---

### Scenarios

#### SC-001: Morning Coffee Check-in (Mobile, 90 seconds)

**Context:** The user is having coffee, phone in hand, before starting their day. YuHaul last ran 6 hours ago during the overnight scheduled window.

**Goal:** Confirm the system is healthy and see if anything interesting was discovered overnight.

**Task sequence:**
1. User opens YuHaul in mobile browser — Overview loads
2. User sees: last run 6h ago, 42 videos processed, 3 accepted, 12 deferred (quota), 0 errors. Quota bar: 8,400/10,000 used today (84%)
3. User taps "Runs" in bottom nav — run list shows latest run at top with "3 accepted" badge
4. User taps the latest run — Run Detail shows the 3 accepted videos: title, channel, target playlist
5. User taps "Errors" in bottom nav — error queue is empty, deferred tab shows 12 items from quota exhaustion
6. User closes the browser tab

**Outcome:** User confirms system is operating normally. Total time: ~90 seconds. No action needed.

**Edge condition:** If the error queue were not empty, the user would tap into the error detail to assess urgency. The design must make error detail accessible in ≤2 taps from the Errors list.

---

#### SC-002: Investigating a Specific Rejection (Desktop, 3 minutes)

**Context:** The user remembers a video from a specific channel that should have matched their "New Discoveries" route but didn't appear in the playlist.

**Goal:** Understand exactly why the video was rejected and decide whether to adjust routes.

**Task sequence:**
1. User navigates to Analytics > Channels
2. User searches or scrolls to find the channel — sees it has 12 discoveries, 8 routed, 2 retained
3. User clicks the channel — channel detail shows recent videos
4. User finds the specific video — it shows "Rejected" badge
5. User clicks the video — Decision Detail opens
6. Evidence trail shows:
   - Route 1 "Music Discovery": condition "title contains [genre keyword]" — NOT MATCHED (red X). The video's title uses a different keyword.
   - Route 2 "Ambient": condition "duration > 10min" — NOT MATCHED (red X). Video is 4 minutes.
   - Result: rejected (no route matched)
7. User realizes the genre keyword in their route is too narrow
8. User navigates to Routes > Music Discovery and adds an alternative keyword condition
9. User saves the route

**Outcome:** User understands the rejection reason and adjusts routing. The evidence trail made the cause unambiguous.

**Edge condition:** If the video were deferred (not rejected), the user would see "Deferred: quota exhausted" instead of the condition trail. The design must distinguish "not evaluated" from "evaluated and rejected."

---

#### SC-003: Setting Up a New Genre Route (Desktop, 5 minutes)

**Context:** The user has been manually listening to a new genre (lo-fi hip-hop) and wants to automate discovery of it.

**Goal:** Create a route that catches lo-fi hip-hop videos and adds them to a dedicated playlist.

**Task sequence:**
1. User navigates to Routes — sees existing routes in evaluation order
2. User clicks "New Route" — Route Editor opens
3. User enters name: "Lo-Fi Discoveries"
4. User adds condition: title contains "lo-fi" AND condition: duration_seconds >= 120
5. User adds exclusion: channel name "Spam Channel" (known repetitive uploader)
6. User configures action: add to playlist "Lo-Fi Beats" (selected from YouTube playlists)
7. User sets evaluation order: drags route to position 3 (after "Music Discovery" and "Ambient")
8. User toggles "Final" off (default — other routes should still evaluate)
9. User clicks "Save" — route is created
10. User clicks "Dry Run" — confirms the dialog
11. Dry-run processes 42 pending videos against draft configuration (including new route)
12. Results show: 5 videos would match the new route, 3 overlap with existing routes, 2 are new catches
13. User is satisfied — the route is already saved

**Outcome:** New route created, dry-run validated it catches expected content, no conflicts with existing routes.

**Edge condition:** If the dry-run showed 0 new catches, the user would revisit conditions before saving. The dry-run must clearly show which videos matched which routes to make this assessment possible.

---

#### SC-004: Importing Historical Corpus (Desktop, 10 minutes)

**Context:** The user is migrating from the old YuHaul application and has a SQLite database file containing 12,000 historical video titles with their video identifiers.

**Goal:** Import the historical corpus so duplicate detection works from day one.

**Task sequence:**
1. User navigates to Settings > Data
2. User sees "Historical Corpus Import" section
3. User clicks "Import" — file picker appears
4. User selects the SQLite database file
5. System validates the database schema (expects one table with `video_id` and `title` columns, optional `channel_id`) and shows preview: "12,000 titles found in source database. 847 video identifiers already exist in corpus (will be skipped). First 10 sample titles shown for confirmation."
6. User confirms import
7. Progress indicator shows: "Importing: 3,200 of 12,000 titles (27%) — ~180 titles/sec — estimated 49 seconds remaining"
8. Import completes: toast "Imported 11,153 new historical titles (847 already present, 0 errors). Total corpus: 12,000 titles."
   - Note: "inserted" (11,153), "already present" (847), and "errors" (0) reconcile the 12,000 source rows from this import. "Total corpus" (12,000) is the actual destination row count after import. This example assumes the destination held only the 847 already-present rows before import; when other destination rows exist, total may exceed inserted + already-present.

**Outcome:** Historical corpus imported, already-present video identifiers skipped, processing errors reported, corpus integrity preserved.

**Edge condition:** If the database schema is invalid (no table with both `video_id` and `title` columns, or multiple tables match), the system shows a clear error: "Expected SQLite database with exactly one table containing video_id and title columns" without losing the user's context. If the user cancels mid-import, already-committed titles are preserved (Append mode).

---

#### SC-005: Backtest Before Adding a Route (Desktop, 10 minutes)

**Status:** Retained future-target comparison scenario supporting US-011/US-012. Comparison-dependent results and checks are deferred from first-use launch; the historical baseline means earlier same-YuHaul production outcomes, not the old cron application. Scenario text retained unchanged.

**Context:** The user wants to add a route for "Jazz Covers" but isn't sure if it would catch meaningful content given their current subscriptions.

**Goal:** Evaluate the proposed route against 30 days of historical observations before committing.

**Task sequence:**
1. User navigates to Backtest
2. User selects "Hypothetical Backtest" mode
3. User adds a proposed route in the draft editor: condition "title contains 'jazz' OR title contains 'cover'"
4. User selects date range: last 30 days
5. User clicks "Run Backtest" — SSE stream shows progress
6. Backtest completes — results view shows:
   - New catches: 23 videos (would have matched under proposed config but didn't historically)
   - Lost catches: 0 (no existing route would be disrupted)
   - Net change: +23
7. User inspects individual new catches — sees titles, channels, and which routes they would match
8. User clicks "Apply" — the proposed route configuration is saved as a new route
9. User navigates to Routes — new "Jazz Covers" route appears in the list

**Outcome:** User validated the route against history before committing, gaining confidence in the configuration change.

**Edge condition:** If the backtest showed lost catches, the user would need to evaluate whether the trade-off is acceptable. The comparison view must make lost catches as visible as new catches.

---

#### SC-006: Quota Exhaustion During a Run (Desktop, observed passively)

**Context:** A scheduled run starts with 2,100 quota units remaining. The run discovers 85 new videos.

**Goal:** Process as many videos as possible without losing eligible work.

**Task sequence:**
1. Run starts — Overview shows "Run in progress" with a link to its saved status and recorded phase; SSE may prompt earlier status reads
2. At video 34, quota is exhausted
3. Run transitions remaining 51 videos to "deferred" state
4. Run completes with summary: 34 processed (22 accepted, 10 rejected, 2 failed), 51 deferred
5. Overview shows: quota bar at 10,000/10,000 (exhausted), deferred count: 51
6. Errors > Deferred tab shows 51 items with "Quota exhausted" reason and next-eligibility: "Eligible when quota resets (estimated 6 hours)"
7. User sees no errors — this is operational state, not failure
8. Next scheduled run picks up deferred items when quota is available

**Outcome:** No eligible work was lost. Deferred items are preserved and will be retried automatically.

**Edge condition:** If a video failed (not deferred), it would appear in the Error tab with a specific error type (e.g., "YouTube API error: video not found"). The design must clearly distinguish "deferred because quota" from "failed because error."

---

#### SC-007: Automatic Access Renewal and Account Recovery (Desktop or Mobile)

**Context:** A scheduled or manually initiated production run needs YouTube access after access-token expiry, including after application restart or between calls during a run. The account was previously authorized.

**Goal:** Continue authorized work without unnecessary reconnection, or understand the actual recovery needed without losing work or repeating an uncertain playlist change.

**Task sequence — available renewal:**
1. Production work encounters expired access, or the provider rejects the access used. The system attempts available renewal before demanding reconnection.
2. If the user inspects account status while renewal is unresolved, it shows "Renewing YouTube access", not usable-access success or "Disconnected — token expired". Ordinary successful background renewal needs no modal or user action.
3. After renewal and its durable save succeed, account status shows "Connected" for the intended account. Eligible work continues subject to operation-safety checks; the UI reads actual run/action evidence rather than inferring success from renewal.

**Observable success:** Scheduled and manual production entry paths continue with renewed access without interactive authorization solely for expiry. Repeat after restart and expiry between calls. Later calls use recovered authorization; work and action outcomes remain truthful.

**Failure/recovery branches:**
- **Missing or confirmed rejected renewal authorization:** Overview and Settings show "Reconnect required" with a safe explanation. The user follows the existing Reconnect OAuth flow, returns to Settings > Account and sees the confirmed result. Cancellation or failure leaves recovery needed and failed work inspectable; it never claims Connected. Successful reconnection restores account access, not historical run/action success.
- **Temporary provider/network failure:** Show "YouTube access temporarily unavailable" and explain that renewal could not complete. Do not claim consent was revoked or require reconnecting. The user can inspect the affected run/work and return later; later production work resolves authorization again. No new retry schedule, countdown or manual refresh control is promised.
- **Renewal save failure:** Show "Could not save renewed YouTube access" with "Access renewal is unavailable. Try again later." Do not claim durable renewal success or rejected consent. Reconnect becomes necessary only on actual evidence that renewal authorization is missing/rejected.
- **Uncertain playlist action:** Even after refresh or reconnect succeeds, retain the unresolved action and its explicit unknown-application explanation. “Playlist Action Confirmation — Authorization Pending and Unknown Application” below defines the exact run/decision wording, including guard-only and unavailable evidence. Renewal, reconnection and clicking Retry are not evidence that repeating it is safe. Existing retry/idempotency checks govern continuation; absent safe-replay evidence, work remains unresolved rather than silently repeated or marked successful.
- **Newer user authority:** If the user disconnects or replaces the account while renewal is in flight, a late result does not restore the old account or show its success. Already issued external calls are not promised cancellable.

**Outcome:** Either access recovers without unnecessary interaction, or the user sees the supported reason and recovery path. No pending work is silently discarded; failed runs remain failed, and a retained error is cleared only by its actual resolution or the user's explicit existing removal action, not by account renewal alone.

**Source:** US-001, [BR-034](/docs/requirements/yuhaul/br-034-automatic-youtube-token-refresh.md), [OC-006](/docs/requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md). These are design checks, not executed acceptance evidence.

---

### Behavioral Specification

#### Views

**View: Overview** — Quick status dashboard; user job: determine system health in under 30 seconds.
- **Regions:**
  - Status bar region: last run time, run count, next scheduled run, account connection status
  - Summary cards region: key metrics at a glance (videos processed, accepted, deferred, errors)
  - Quick actions region: "Run Now", "Dry Run", "View Errors" buttons
  - Activity feed region: recent run history (last 5 runs as compact rows)
  - Quota region: quota bar with units used/remaining, estimated reset time
- **Components:** Status card, metric card, quick action button, activity row, quota bar

**View: Route List** — Manage all routing rules; user job: see all routes, their order, and status.
- **Regions:**
  - Toolbar region: "New Route" button
  - Route list region: sortable list of routes with drag-to-reorder
- **Components:** Route row (name, conditions summary, enabled/disabled toggle, final badge, match count, drag handle, move up/down buttons), empty state
- **Route row details:**
  - Name: route name, clickable to open editor
  - Conditions summary: compact text summarizing conditions (e.g., "title contains 'lo-fi' | duration ≥ 2min"). Truncate with ellipsis if longer than 60 characters. If no conditions, show "No conditions" in muted text.
  - Enabled toggle: see Toggle component below
  - Final badge: small badge "Final" if route is marked final; hidden if not final
  - Match count: number of videos matched by this route (lifetime or period, depending on context)
  - Drag handle: grip icon for drag reordering
  - Move Up/Down buttons: arrow icons for keyboard-accessible reordering

**View: Route Editor** — Create or edit a single route; user job: define conditions, exclusions, and actions for a routing rule.
- **Regions:**
  - Header region: route name (editable), description (editable)
  - Conditions region: flat condition list with AND/OR toggle at top
  - Exclusions region: route-scoped exclusion rules (channel, video, title, word/phrase, duration limits)
  - Actions region: action configuration (v1: playlist insertion only)
  - Settings region: evaluation order, final flag, enabled toggle
  - Footer region: Save, Cancel, Delete (if editing existing)
- **Components:** Condition row (field selector, operator selector, value input, remove button), condition list (add condition, AND/OR toggle), exclusion section (channel exclusion list, video identity exclusion list, title exclusion list, word/phrase exclusion list, duration min/max inputs, add/remove for each list), action row (type selector: "Add to playlist" — v1 only, parameter fields), save/cancel buttons
- **Exclusion semantics:** Exclusions are route-scoped — a video excluded from this route is not evaluated by this route but remains eligible for other routes. Exclusion lists support add/remove of individual entries. Duration limits are optional min/max fields (empty = no limit on that side). All exclusion state is persisted with the route.
- **Layout:** Single-column layout at all viewport widths. Conditions, exclusions, and actions are vertically stacked.

**Route Editor — Create vs Edit mode:**
- **Create mode:** URL pattern `/routes/new`. Page title: "New Route". All fields start empty/default. Footer shows "Save" and "Cancel" (no Delete). Evaluation order: new route appended to end of list.
- **Edit mode:** URL pattern `/routes/:id`. Page title: "Edit Route [name]". All fields populated from saved route data. Footer shows "Save", "Cancel", and "Delete". Delete button is destructive-styled (red).
- **Distinguishing the mode:** The URL determines mode. If the route ID does not exist, show a "Route not found" error and navigate back to Route list.

**Route Editor — Default values on create:**
- Name: empty string
- Description: empty string
- Conditions: empty list (no conditions — user must add at least one before saving)
- Exclusions: empty lists (all exclusion lists empty)
- Actions: empty list (no actions — user must add at least one before saving)
- Final: false
- Enabled: true
- Evaluation order: last position (appended after all existing routes)

**Route Editor — Loading state on edit:**
- On navigating to `/routes/:id`: show skeleton loader (placeholder rows for name, conditions, actions) while fetching route data from `GET /api/routes/:id`
- On fetch success: populate all fields, transition to ready state
- On fetch error (404): show "Route not found" message with "Back to Routes" link
- On fetch error (network): show "Failed to load route" error with "Retry" button

**Route Editor — Condition composition UI:**
- **Condition row layout:** Horizontal row with three controls: field selector (dropdown), operator selector (dropdown), value input (text or number), remove button (×).
- **Field options (v1):**
  - `title` — video title text
  - `channel_name` — channel name text
  - `duration_seconds` — video duration in seconds
  - `description` — video description text
- **Operator options per field:**
  - `title`, `channel_name`, `description` (text fields): `contains`, `equals`, `starts_with`, `ends_with`, `matches_regex`
  - `duration_seconds` (numeric field): `equals`, `greater_than`, `less_than`, `between`
- **Value input behavior:**
  - Text fields: text input with placeholder hint (e.g., "e.g., lo-fi")
  - Numeric fields: number input with min=0
  - `between` operator: shows two number inputs (min, max) with "to" separator
  - `matches_regex`: monospace font input with regex syntax hint
- **Add condition button:** Appears below the last condition row. Label: "+ Add condition". Click appends a new empty condition row.
- **AND/OR toggle:** Single toggle at the top of the conditions region. Label: "Match [ALL / ANY] conditions". Default: ALL. When ALL: all conditions must be true for the route to match. When ANY: at least one condition must be true.
- **Remove condition:** Click × button on a condition row. Removes the row immediately (undo via "Discard changes" on the editor). If last condition is removed, conditions list becomes empty.

**Route Editor — Action configuration UI:**
- **Action row layout:** Horizontal row with: action type selector (dropdown), action-specific parameters, remove button (×).
- **Action type options (v1):**
  - `add_to_playlist` — Add matching video to a YouTube playlist
- **`add_to_playlist` parameters:**
  - Playlist selector: dropdown populated from user's YouTube playlists (fetched via `GET /api/playlists`). Shows playlist title. If playlists fail to load, shows error state with "Retry" link.
  - Playlist selector loading state: skeleton dropdown with "Loading playlists..." text.
  - Playlist selector empty state: "No playlists found. Create a playlist on YouTube first." with link to YouTube.
- **Add action button:** Appears below the last action row. Label: "+ Add action". Click appends a new action row with type defaulting to `add_to_playlist`.
- **Remove action:** Click × button on an action row. Removes the row immediately (undo via "Discard changes"). If last action is removed, actions list becomes empty.

**Route Editor — Validation rules:**
- **Name:** Required. Must be non-empty after trimming whitespace. Maximum 100 characters. Must be unique across all routes (checked on save).
- **Conditions:** At least one condition required before saving. Each condition requires: field selected, operator selected, value non-empty (except `between` which requires both min and max).
- **Actions:** At least one action required before saving. Each action requires: type selected, all type-specific parameters filled (for `add_to_playlist`: playlist selected).
- **Validation timing:** Validate on save attempt. Show inline error messages below each invalid field. Scroll to first error if multiple errors exist.
- **Validation error messages:**
  - Name empty: "Route name is required"
  - Name too long: "Route name must be 100 characters or fewer"
  - Name not unique: "A route with this name already exists"
  - No conditions: "Add at least one condition"
  - Condition missing field: "Select a field"
  - Condition missing operator: "Select an operator"
  - Condition missing value: "Enter a value"
  - No actions: "Add at least one action"
  - Action missing playlist: "Select a playlist"

**Route Editor — Save behavior:**
- **Create:** `POST /api/routes` with route payload. On success: toast "Route created", navigate to Route list (or stay on editor with new URL `/routes/:id`). On error (validation): show inline errors. On error (network): toast "Failed to save route. Check your connection." and re-enable Save button.
- **Update:** `PUT /api/routes/:id` with route payload. On success: toast "Route saved", stay on editor. On error (validation): show inline errors. On error (network): toast "Failed to save route. Check your connection." and re-enable Save button.
- **Cancel:** If no changes made, navigate back immediately. If changes made, show confirmation dialog: "Discard unsaved changes?" with "Discard" and "Keep editing" options.

**Route Editor — Delete behavior:**
- **Delete button:** Destructive-styled (red) button in footer, only visible in edit mode. Label: "Delete".
- **Delete confirmation dialog:** On click, show modal dialog:
  - Title: "Delete route?"
  - Body: "Delete route '[name]'? This cannot be undone. Videos that would have matched this route will no longer be routed by it."
  - Actions: "Cancel" (secondary) and "Delete" (destructive, red)
  - Focus: moves to "Cancel" button on open (safe default)
- **Delete execution:** On confirm, `DELETE /api/routes/:id`. On success: toast "Route deleted", navigate to Route list. On error: toast "Failed to delete route. Please try again.", dialog closes.
- **Evaluation order after delete:** Remaining routes maintain their relative order. No re-indexing occurs — the gap in positions is transparent to the user.

**Route Editor — is_final toggle:**
- Toggle control in Settings region. Label: "Final route" with description: "When enabled, no further routes are evaluated after this route matches."
- Visual indicator on Route list: "Final" badge (distinct color/icon) next to route name.
- No restriction on multiple final routes — the user may set multiple routes as final. The first final route that matches terminates evaluation; later final routes are still evaluated if no earlier final matched.

**Route Editor — Evaluation order display:**
- In Settings region, show current position: "Evaluation order: #N of M".
- Provide "Move up" and "Move down" buttons to adjust position (same as Route list drag, but within the editor).
- Moving a route in the editor also updates the Route list order immediately (persisted on save).

**View: Run List** — Browse processing history; user job: find a specific run or scan recent activity.
- **Regions:**
  - Filter region: date range, run type (manual/scheduled/dry-run/replay/backtest), status filter (including “Stopping” and “Stopped by user”, see “Stop an Active Run”)
  - Run list region: chronological list of runs
- **Components:** Run row (timestamp, type badge, duration, video count, outcome summary, quota cost, status), empty state, filter controls
- **Filter behavior:** Filters apply to the runs returned by `GET /api/runs`. The date range matches the run start date as a UTC calendar day, inclusive of both endpoints; run type and status match the run's `run_type` and `status` values. Filters combine — a run must satisfy every active filter. `GET /api/runs` accepts no query parameters (ARS-001), so filtering happens in the client over the fetched list; server-side filtering would require extending that API contract.
- **Filter-empty state:** When filters are active and no run matches, the run list region shows "No runs match the current filters." instead of the unfiltered empty state. Filter controls remain usable.

**View: Run Detail** — Inspect a single run; user job: understand what happened in this run and why.
- **Regions:**
  - Header region: run metadata (timestamp, type, duration, configuration version with link to Configuration History)
  - Summary region: outcome counts (accepted/rejected/deferred/failed), quota cost summary
  - Video outcomes region: filterable list of videos processed in this run
- **Components:** Outcome summary cards, video outcome row (title, channel, outcome badge, route match info, expand action), filter by outcome type, quota cost breakdown, configuration version link
- **Mode-aware interpretation:** Run and video inspection follow “Simulation Inspection — Requested-Run Results” below; production lifecycle state is not a simulation result.

#### Playlist Action Confirmation — Authorization Pending and Unknown Application

**Accepted amendment (2026-11-07):** Refines Run Detail and Decision Detail under [BR-034](/docs/requirements/yuhaul/br-034-automatic-youtube-token-refresh.md), [OC-006](/docs/requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md), and [Authorization ARS-001 — Evidence, retention and public uncertainty](/docs/architecture/youtube-authorization/specifications/ars-001-authorization-state-dispatch-and-send-guard.md). Reuses US-001/SC-007 and the existing run/decision inspection journey. This supplies precise presentation of accepted states, not a new retry, reconciliation or historical-repair policy. It qualifies generic production `Failed`/`Not attempted` action labels elsewhere in this document when the explicit evidence below is present. Run status and recorded video categories remain unchanged.

**Walkthrough:** The administrator opens a failed run and sees a video marked `Playlist outcome unknown`. Its decision detail identifies the captured playlist and explains that YuHaul cannot confirm whether the change was applied. Account access may now be Connected, but the warning remains. A different action deferred before sending says `Waiting for YouTube access` and `Not attempted in this run`. Opening Settings helps the person understand current access; it does not replay either action or change the earlier run's outcome.

**Human goal and scenario checks:** The administrator needs to distinguish “not sent because access was unavailable” from “may have applied; do not repeat”, including after restart or reconnection. SC-007's uncertainty branch is checked by opening the same run/video before and after account recovery: the original warning and attempt evidence stay truthful. Its pre-send branch is checked with authorization unavailable before admission: a deferred action has no attempt time and does not claim a provider failure. Its interrupted branch is checked with only a saved unresolved guard and no action row: inspection still explains unknown application without inventing a send. These extend the existing scenario; no duplicate story IDs/files are needed.

**Separate three meanings:**

- **Run lifecycle:** Keep stored Running, Completed, Failed or Interrupted and existing counts. Completion does not certify every playlist result. Do not rewrite terminal run history after account recovery, or invent an `unknown` run/video outcome category or filter.
- **Action confirmation in this run:** Explain the saved action/guard facts using the table below. A failed confirmation is not proof the playlist stayed unchanged.
- **Current account access:** Settings/Overview use SC-007's canonical status. A historical `authorization_pending` reason is not proof that access is still unavailable. Show `View account status` linking to existing Settings > Account, not a guessed Reconnect button in run history. Only actual current status selects Reconnect in Settings.

**Exact production action presentation:** Apply consistently in both per-route Actions and Layer 3 — Action outcomes. Only admitted, application-owned evidence selects these explanations; do not classify by raw error prose, HTTP status alone or missing timestamp.

| Evidence supplied by the accepted contracts | Main action label | Persistent explanation / time |
|---|---|---|
| `deferred` with `authorization_pending` | `Waiting for YouTube access` | `Not attempted in this run. YouTube access was unavailable; the work was retained.` No attempt time. This does not mean renewal is currently running or promise automatic completion. |
| `failed` with admitted `mutation_application_uncertain` reason/diagnostic | `Playlist outcome unknown` | `YuHaul could not confirm whether this playlist action was applied. Repeating it could add the video again, so it remains blocked.` Show `Attempted {time}` only from an actual recorded attempt time. The stored failed outcome describes failure to confirm, not certain non-application. |
| `not_attempted` with `mutation_application_uncertain` | `Not attempted — unresolved playlist action` | `This run did not repeat the playlist action because an earlier application is unresolved. Whether the earlier action was applied is unknown.` No attempt time for this row. Do not assign an earlier run/time unless actually supplied. |
| Requested-run `unresolved_mutations` entry without an action row or matching attempt evidence | `Playlist outcome unknown` | `YuHaul cannot confirm whether this playlist action was sent or applied. Repeating it could add the video again, so it remains blocked.` If shown, `Prepared {time} — not an attempt time` uses only the supplied preparation time. Never fabricate a failed/not-attempted action row or increment attempts. |
| Unsupported/withheld action reason or diagnostic | Keep only independently established outcome facts | `Action explanation unavailable.` Never display the raw value or infer authorization/unknown application from it. A separately valid unresolved entry still produces the warning above. |

Known success and other existing conclusive dispositions retain their established labels. If a reason was withheld for safety, preserve the existing safe-withholding explanation rather than echoing it. Explanations come from registered reason/code semantics, not parsing a printable error string in the browser. Safe bounded diagnostic facts may be secondary detail; raw provider bodies, prose, URLs, tokens, authorization IDs/revisions and original error chains are absent from visible text, tooltips and accessible names.

**Run Detail placement:** Keep the recorded video-outcome badge and outcome filters. Add the text `Playlist outcome unknown` immediately alongside/below the affected video's result when that requested-run row supplies an unresolved entry or an explicit uncertainty reason. For `authorization_pending`, add `Waiting for YouTube access` beside the recorded Deferred result. These are explanatory notices, not replacements for the recorded category. They remain visible on mobile, not tooltip-only. The existing video link opens Decision Detail for the same run/video. Do not compute a run-wide unresolved total from one filtered/paginated page, change accepted/failed counts, or claim that unshown videos have no uncertainty.

**Decision Detail placement and hierarchy:** After Layer 1's recorded outcome and before route evidence, show a persistent H2 `Unresolved playlist actions` when valid requested-run uncertainty exists. Begin with `Restoring YouTube access does not confirm whether these playlist actions were applied.` List each supplied exact target with visible label `Playlist {target_playlist_id}` and the applicable explanation above. Use existing captured playlist names only when actually available; never fetch YouTube membership to fill this region. It remains visible even when Layer 3 says `No action outcomes recorded for this video.` Add `An unresolved playlist action is recorded above; no action outcome was recorded.` in that case. This is not a new action result. Preserve any independently recorded attempt row/time; a later blocked re-entry must not visually erase it.

For guard entries and action evidence about the same target, one target notice can refer to the associated action row. Do not sum them as two attempts or imply an extra send. A receiving run's blocked row explains the earlier unresolved application, but must not borrow the original run's guard or attempt history. If no requested-run uncertainty evidence is supplied, do not show the warning merely because global processing state is failed or the account was unavailable.

**Unavailable versus unknown application:** A positively supplied unresolved entry means application is unknown. Missing legacy action/guard evidence means `Playlist confirmation details unavailable for this record`, not “not sent”, “safe to retry”, or a diagnosed uncertain send. An empty/missing unresolved array does not establish historical non-application, another run's guard absence or safe retry. Keep existing missing-action copy and do not require an all-clear badge. If an explanation is withheld, say unavailable, not “never recorded” unless that is known. Mandatory list/detail read failure uses the existing failed-fetch state; it cannot become an empty successful guard/action list.

**Actions, continuity and recovery:**

- Run → video link → Decision Detail preserves requested-run identity. Direct links, new tabs and reload use saved run mode/evidence, never navigation-only flags. Back to run returns to that run; retain existing filter/page continuity where supported. This inspection performs local reads only, not token renewal, YouTube reads, evaluation or mutation.
- `View account status` navigates to existing Settings > Account. The user can return using browser Back or the saved run link. Renewal/reconnection shows its actual current result there; neither navigation nor OAuth success restarts a run, clears a warning, resolves a guard or promises safe replay.
- Add no `Retry playlist action`, `Clear uncertainty`, reconciliation or recovery control to run/decision views. Existing queue Retry/Remove/Dismiss semantics remain unchanged: none clears a guard or establishes non-application. When describing known uncertainty, say `Retry does not resolve this playlist uncertainty.` Never advertise Retry as a way to repeat a blocked target; removal does not mean an issued request was cancelled. Do not use a production run as a harmless confirmation check.
- Read retry acknowledges loading and suppresses duplicate reads, then shows returned evidence or the existing read failure. It never retries the playlist operation. Keep last-known warnings during a failed background refresh with the existing stale indicator; do not replace them with an all-clear. Initial loading has no success/non-application claim. Unsupported/malformed uncertainty information is unavailable, not guessed or rendered verbatim. Existing not-found/back navigation remains available.
- Saved terminal history stays fixed. Account recovery can change current status, but only supported operation evidence can change a live nonterminal confirmation. There is no promised eventual automatic resolution or new timing/retry policy in this design.

**Accessibility and surface checks:** Target WCAG 2.2 AA. Warning meaning is persistent text, not color alone (SC 1.4.1); headings and per-target text have a meaningful reading order: recorded outcome → uncertainty explanation → account link → route/action evidence (SC 1.3.1/1.3.2). Unknown application is a warning, not a green success check or indefinite spinner. Keep existing design tokens; normal text contrast is at least 4.5:1 (SC 1.4.3). Target IDs and explanations wrap at 320 CSS pixels with no lost text (SC 1.4.10). All links/read-retry controls are keyboard operable with visible, unobscured focus (SC 2.1.1/2.4.7/2.4.11). Route changes focus the destination H1; background reads leave focus in place. Announce a newly changed uncertainty/pending status politely once, not on every identical poll (SC 4.1.3). Verify by keyboard, screen reader and narrow viewport against the checks below; this amendment does not assert implemented conformance.

**Design decision:** Retain established run/category structure and add explicit target-level confirmation notices. Reject using generic `Failed` alone because it implies conclusive non-application; reject merging this with `Waiting for YouTube access` because renewed access cannot settle an issued mutation; reject a retry/reconciliation workflow because no such recovery capability is accepted. This is scoped refinement of the accepted design, not a new visual direction.

**Evidence and downstream checks (not executed browser acceptance):** Source inspection found that `web/src/types.ts` and `web/src/api/run-videos.ts` omit `unresolved_mutations`, `web/src/lib/outcome.ts` has no labels for these two reasons and falls back to raw reason strings, and `DecisionDetailView.tsx` uses generic outcome badges. These are material presentation/projection gaps for implementation owners, not resolved by this document. Architecture owns safe diagnostic registrations and API facts under the linked ARS; implementation owns carrying them through list/detail and the real browser. No new product policy or user decision is needed. Semantic lookup was degraded; targeted source reads were used. Runtime, rendered hierarchy and assistive-technology behavior remain unverified.

Check the shipped browser with real registered API projections for: pre-send authorization deferral; admitted uncertain failure with attempt time; guard-only uncertainty after restart with no action row/time/count; same-run blocked re-entry retaining earlier attempt evidence; fresh receiving-run blocked action without borrowed history; reconnect/refresh success with uncertainty unchanged; missing legacy evidence and unknown/withheld reasons; failed/stale reads; direct-link/reload; filtering/paging without false global totals; and simultaneous distinct successful, deferred and unresolved targets. Confirm run/video counts remain recorded facts, no raw provider marker reaches any rendered/accessibility surface, no inspection/retry-status action sends a playlist request, and simulation mode retains hypothetical labels without projecting current production guards.

#### Run Now — Recorded Progress and Closure

**Status and scope (2026-11-05):** Accepted amendment for the user's requested production-run feedback, especially discovery with unknown selected count and zero counters. Reuses US-009; no new run mode, telemetry, processing policy, or requirement is introduced. This section refines Overview, Run Detail, start feedback, and DD-13. It does not settle Backtest/Replay date filters or discovery/processing coordinator semantics.

**Authority and dependencies:** [BR-017 — Manual Run Initiation](/docs/requirements/yuhaul/br-017-manual-run-initiation.md), [BR-020 — Run-Level and Route-Level Statistics](/docs/requirements/yuhaul/br-020-run-level-and-route-level-statistics.md), [BR-028 — Historical Run Inspection](/docs/requirements/yuhaul/br-028-historical-run-inspection.md), and the accepted [durable run diagnostics design](/docs/architecture/run-outcome-diagnostics/design/ard-001-durable-run-diagnostics-and-input-visibility.md). Saved run status/phase and run-scoped evidence remain authoritative; SSE is best-effort feedback, not completion authority.

**Walkthrough / scenario:** A first-time administrator starts Run Now from Overview. Once acknowledged, Run Detail opens for that run. It says “Running — Discovering videos”, “Selected inputs: Unknown”, and “Videos with recorded route decisions: 0”. Quota may rise while these counts stay unchanged; the page explains that discovery has no known completion total. The person leaves the tab, returns, and sees freshly read saved status. If updates cannot be read, the last known values are labeled stale, not completed. When a saved terminal status is read, active progress ends and the recorded outcomes remain inspectable. Observable success is knowing whether this identified run is running, finished, failed, interrupted, or currently unconfirmable without starting another run or guessing from quota.

**Start and navigation:**

- Keep the existing production confirmation and explanation. On confirmation, immediately show “Starting run…” and prevent repeated submission while the start request is pending.
- Acknowledgement with `run_id` opens `/runs/{id}` and immediately reads that run. Initial copy: “Run {id} started. Checking progress…”. This is not completion; the first read may already be terminal. Move focus to the destination heading, not a toast. Browser Back returns to Overview; direct links/reload reconstruct the same run from saved state.
- If acceptance cannot be confirmed, show “Could not confirm whether the run started. Check Runs before starting another run.” with “View runs”. Do not automatically repeat the start request. An explicit rejection can use its safe, specific message instead.
- While a production run is known to be running, keep Run Now unavailable with adjacent “Run in progress” and a “View run” link (a stopping run shows “Run {id} is stopping” instead; see “Stop an Active Run”). Server concurrency protection remains authoritative; a stale browser is not concurrency protection. Do not disable unrelated navigation.

**Placement and meaning:**

- On Run Detail, put the persistent progress/status region immediately below the heading and before result cards: run identity/status, recorded phase, selected count, decision-bearing count, then status-read freshness or recovery text. Keep existing video → decision → action navigation.
- On Overview, put the latest run's identity, type, saved status, and “View run” link above the metric cards; include the recorded phase while running. Refresh that status while running and refresh dashboard totals after observing terminal status. Associate last-run metrics with the identified run; preserve simulation qualifications when the latest run is a simulation.
- Keep the quota meter separate, labeled “Quota today”, with persistent explanation: “YouTube API quota used today—not run progress or playlist additions.” Its real quota denominator is not a processing denominator.
- Show `current_phase` from saved reads. Map `discovery` → “Discovering videos”; `exclusion` → “Checking exclusions”; `duplicate_detection` → “Checking duplicates”; `routing` → “Evaluating routes”; `action_execution` → “Processing playlist actions”. Do not display unknown raw phase strings. A phase indicates the recorded checkpoint, not verified success of every preceding item.

**Observable states and exact copy:**

| Saved/read state | Presentation and available recovery |
|---|---|
| `running`, recognized phase | “Running — {phase label}”. No overall progress bar, percent, ETA, heartbeat, page count, or elapsed-time-based stall claim. |
| `running`, missing/unrecognized phase | “Running — current phase unavailable.” Continue reading status; do not infer discovery or another phase. |
| Discovery, selected count null/absent | “Selected inputs: Unknown”. Adjacent: “Discovery is in progress. The input count is not available yet; there is no known completion total.” Zero decision/counter values do not mean no videos were found. |
| Known selected count | “Selected inputs: {n}”. Zero is displayed only for a captured empty selection. Unknown outside discovery remains “Unknown”, with “The selected input count was not captured or is not available.” |
| Decision-bearing count available | “Videos with recorded route decisions: {n}”. Adjacent: “Counts reflect saved evidence, not overall completion.” If absent, show “Unavailable”, not zero. Never present this as “{n} of {selected} complete”; decisions can be partial, exclusion-related, or absent for legitimate paths. Equal counts are not completion proof. |
| `completed` | “Run completed.” Remove active progress. Show recorded outcome totals, including deferred/failed outcomes when present. Adjacent: “Processing has ended. Inspect video action outcomes for recorded playlist results.” Completion is not universal playlist success. |
| `completed`, selected count exactly zero | Add “No inputs selected for this run.” Do not say “No videos exist” or “Nothing matched”. |
| Terminal, no video outcome rows, selected count unknown or positive | “No video outcomes recorded for this run.” Do not infer an empty input set or rejection. With an active outcome filter, retain the existing filter-specific empty state instead. |
| `failed` / `interrupted` | “Run failed.” / “Run interrupted.” Show recognized phase as “Last recorded phase: {phase label}” and the API-vetted reason, or existing safe-reason-unavailable/missing-reason explanation. Keep recorded counts/evidence inspectable even when Errors is empty. Do not promise automatic recovery or unchanged YouTube state. Run Detail has no Resume button: continuing a run happens only through Run Now. A run that is not executing has no Stop control either; “Stop run {id}” exists only while a run is active (see “Stop an Active Run”). An interrupted manual run stopped by its time limit gets the extra explanation in “Run Now — Resume After a Time Limit”; every other failed or interrupted run keeps this row's copy and has no resume promise. |
| Initial status read fails | “Could not load run status. Completion is unknown.” Offer “Retry status” and “Back to runs”; never leave an indefinite loading or success state. |
| Background status read fails | Retain values as “Last known status”. Show “Could not refresh status. This run may still be processing.” and “Retry status”. Do not portray it as confirmed active work or turn a read failure into run failure. |
| SSE unavailable, saved reads succeed | “Live updates unavailable. Checking saved status periodically.” No repeated error toast or run-failed styling. |

While running, qualify existing discovered/outcome summary cards: “Recorded totals; may be incomplete until the run finishes.” These counters are not promised to increment during discovery or each action. Accepted remains distinct from a recorded successful playlist action. Preserve simulation explanations; if shared progress presentation is reused for simulations, terminal success says “Simulation evaluation completed”, never playlist success.

**Refresh, failure, and interruption:** Use the existing five-second saved-status refresh while running; SSE hints can prompt an earlier read. A terminal event only triggers a read; only its saved status ends active progress. Fresh reads replace stale values, including a terminal result reached while disconnected. On return to a visible tab, read status again; background timer throttling is not a run interruption. Label browser receipt time “Status checked at {local time}”, not “Last activity”; an unchanged phase/count is not heartbeat evidence. Keep filters/page selection and focus through background reads. Stop active refresh presentation at saved `completed`, `failed`, `interrupted` or stopped by user. A run that is stopping is still active and keeps refreshing. Do not auto-restart work.

**Accessibility and responsive checks:** Target WCAG 2.2 AA. Use a named progress region and a concise polite status announcement for start, phase changes, terminal state, and loss/recovery of status reads (SC 4.1.3). Do not announce each poll, freshness timestamp, or changing count. Background updates never move focus; heading focus follows explicit navigation; retry controls remain keyboard accessible with visible, unobscured focus (SC 2.1.1, 2.4.3, 2.4.7, 2.4.11). Status/connection meaning is explicit text, not a colored dot alone (SC 1.4.1). Verify text/control contrast in supported themes (SC 1.4.3, 1.4.11), target sizes (SC 2.5.8), and single-column reflow without clipped copy/actions at 320 CSS px and usable text at 200% zoom (SC 1.4.10, 1.4.4). Any activity animation is supplementary; reduced motion retains the same text with no animation. Verify with keyboard and a screen reader that a lost connection is announced once and retry/recovery does not steal focus.

**Acceptance observations:** Exercise US-009 with delayed discovery (selected unknown, zero counters, rising quota); an acknowledged run already terminal at first read; captured-zero completion versus unknown-count/no-outcome completion; partial decisions followed by failure with an empty Errors queue; completed processing containing deferred/failed outcomes; dropped SSE with working saved reads; failed saved reads followed by background-tab return/recovery; and direct-link/reload plus keyboard/screen-reader/mobile inspection. In each case the same run remains identifiable, no duplicate start is automatic, and no unknown count or missing evidence becomes a success/rejection/percentage claim.

**Decision and evidence limits:** Keep the already intended automatic Run Detail navigation and text-first persistent progress. An Overview-only panel duplicates inspection; a modal traps attention; percentage/ETA presentation lacks completion facts. Existing status, phase, nullable selected count, decision-bearing count, and safe run/action evidence suffice for this amendment. No new backend facts are requested. Source inspection on 2026-11-05 found Overview's toast-only start, Run Detail's SSE-only active phase, late-finalized counters, and a named-event/payload mismatch between server SSE and its browser consumer. These are implementation follow-ups, not new protocol decisions or executed browser proof. The user's report of a discovery run with rising quota motivates the design but does not prove that run's health. No live-run diagnosis, implementation conformance, accessibility certification, or date-filter decision is claimed.

#### Run Now — Resume After a Time Limit

**Status and scope (2026-11-10):** Amendment for [BR-017](/docs/requirements/yuhaul/br-017-manual-run-initiation.md) AC7–AC12, which needs a visible UI. It supersedes any earlier statement here that an interrupted run cannot be continued, for time-limit-interrupted manual runs only. It extends “Run Now — Recorded Progress and Closure”, reusing US-009. It adds no capability, button, or telemetry.

**Implementation status:** implemented in commit a016744 (`a01674440a54175ee9c1e0395c7093f4dd8b1b61`); browser and screen-reader verification pending. The run API now exposes `initiated_by` (`run_now`, `schedule`, `queue_retry` or null) and `interrupt_cause` (`time_limit`, `shutdown` or null).

**Walkthrough:** Run Now started run 30, which stopped at its time limit and shows “Run interrupted”. Later the administrator presses Run Now and confirms. The dialog already said an earlier stopped run would be continued. Run Detail for run 30 opens, with focus on its heading, and says “Run 30 resumed. It continues from where it stopped. No new run was started.” Status then follows the normal progress rules. Had run 30 been unusable, Overview would instead keep the person in place and say run 30 cannot be resumed safely, name the reason, and link to it. Observable success: the person can tell whether the press resumed an existing run, started a new one, or did nothing, and which run it concerns.

**What the API gives the UI.** `POST /api/runs`:

| Response | Meaning |
|---|---|
| 202 `admission: "started"` | New run `run_id` created. |
| 202 `admission: "resumed"` | Existing run `run_id` continues; no new run, no new snapshot. |
| 409 `code: "production_run_active"` | Another production run is executing; nothing started or resumed. |
| 409 `code: "resume_unavailable"` with `run_id`, `reason` | The oldest time-limit-interrupted run cannot be continued safely; no new run was started; nothing changed. |
| 503 | Startup recovery in progress, or shutdown; nothing started or resumed. The two have no machine code. |
| other error / no response | Outcome not confirmed. |

The UI branches on `status`, `code` and `admission`, never on message text. `reason` is a closed set; render only the mapped copy below, never the raw value. A missing or unknown `admission` on a 202 is treated as accepted without claiming new or resumed.

**Exact copy after acceptance.** Navigate to `/runs/{run_id}`; focus the Run Detail heading.

| Case | Status-region first line (persistent until the first saved status read replaces it) | Further text |
|---|---|---|
| `started` | “Run {id} started. Checking progress…” (unchanged) | none |
| `resumed` | “Run {id} resumed. It continues from where it stopped. No new run was started. Checking progress…” | “Playlist additions already recorded as successful are not repeated. An addition whose earlier outcome could not be confirmed stays marked as unconfirmed.” |
| 202, unknown `admission` | “Run {id} accepted. Checking progress…” | none |

The resumption line comes from navigation state of this press only. The server does not expose a saved “was resumed” fact, so after reload or direct link the run shows ordinary saved status and phase. Do not claim any persistent resumed marker, resume count or resume time. While the page stays open, the line stays visible and is not removed when status changes or the run finishes; this was the implementer's judgement call, as the design did not set a lifetime. It is cleared from history state after first read, so it never returns on reload.

A resumed run keeps its id and original settings. Run Detail shows “Running — {phase label}” from the saved checkpoint, and the normal selected/decision count rules apply. Counts reflect saved evidence and may already be non-zero at the first read. If the run reaches its time limit again, it is an ordinary interrupted run and the “Interrupted run” text below applies again.

**Exact copy for refusals.** Show a persistent inline message directly under the Overview header, not a toast (toasts time out, which would lose a safety-relevant message). It stays until the person dismisses it, presses Run Now again, or leaves Overview. Use `role="alert"`, give it a text prefix, and do not move focus. Re-enable Run Now afterwards. Every refusal says nothing was started or changed, except the unconfirmed cases.

| Case | Message | Links / action |
|---|---|---|
| `production_run_active` | “Run Now did nothing. A production run is already running. Nothing was started or resumed.” | “View run” link when Overview knows the active run; otherwise “View runs”. Tell them to wait for it to finish, then press Run Now again. |
| `resume_unavailable` | “Run Now did nothing. Run {id} stopped at its time limit but cannot be resumed safely. No new run was started and nothing was changed. {Reason sentence}” | Link “View run {id}”. Then, depending on `reason`: for `snapshot_unavailable`, `snapshot_unreadable`, `snapshot_inadmissible`, `checkpoint_unrecognized` and `terminal_timestamp_contradiction`: “While run {id} is the oldest run stopped this way, Run Now will keep refusing. To unblock Run Now, you can discard run {id}. The schedule is not affected.” and the button “Discard run {id}” (see “Run Now — Discarding a Run That Cannot Be Resumed”). For `recheck_at_write` use only the sentence in the reason table, with no refusal-will-continue claim and no button. For an unknown reason: “While run {id} is the oldest run stopped this way, Run Now may keep refusing. Open the run to see whether it can be discarded. The schedule is not affected.” with no button. Dry Run stays available. |
| 503 | “Run Now did nothing. YuHaul can't accept a run right now. It may be starting up or shutting down. Nothing was started or resumed. Try again in a minute.” | “View runs” link. |
| Other server error, or no response | “Could not confirm whether a run started or resumed. Check Runs before pressing Run Now again.” | “View runs”. No automatic retry (existing start-uncertainty rule, which this reuses). |

`resume_unavailable` reason sentences (the `{Reason sentence}` above):

| `reason` | Sentence |
|---|---|
| `snapshot_unavailable` | “Its saved settings are missing.” |
| `snapshot_unreadable` | “Its saved settings cannot be read.” |
| `snapshot_inadmissible` | “Its saved settings cannot be used safely.” |
| `checkpoint_unrecognized` | “Its saved progress point is not recognized.” |
| `terminal_timestamp_contradiction` | “Its saved record is contradictory: it has a completion time but is marked interrupted.” |
| `recheck_at_write` | “Its saved state changed while Run Now was checking it. Check the run, then press Run Now again if that is still what you want.” |
| anything else | “The reason is not available in this version of the interface.” |

No refusal copy promises that the run can be fixed, that YouTube state is unchanged, or that waiting will resolve a `resume_unavailable` refusal. Discarding removes the block; it does not repair the run. “Nothing was changed” is only used where the server contract guarantees a refusal changes nothing.

**On Run Detail — interrupted run.** Applies only when the saved run is a manual Run Now run, `interrupted`, and the API reports that the interruption cause is the time limit. If the API does not report the cause (`interrupt_cause` null or absent), use the generic “Run interrupted.” row and make no resume promise.

- Heading status: “Run interrupted — time limit reached.” Keep “Last recorded phase: {phase label}” and the recorded counts.
- Explanation: “Work done up to this point is saved. Pressing Run Now continues the oldest run stopped this way. This run continues only if it is the oldest and can be resumed safely.” Add a link “Go to Overview” to where Run Now is. Do not add a Resume button.
- When the run read says this run is the discardable run (BR-017 AC22), replace the explanation above with the unusable-run text and the Discard control defined in “Run Now — Discarding a Run That Cannot Be Resumed”. This applies to all five unusable reasons.
- Do not say it “will resume”, “resumes next”, or “resumes automatically”. The UI cannot know whether it is the oldest or whether a guarded target blocks it.
- A run interrupted by shutdown, process loss, or any other cause, a scheduled run, a legacy run, a failed run, and a simulation do not get this text.

**Accessibility.** Target WCAG 2.2 AA. Refusal and uncertain messages are inline text with a visible “Run Now did nothing.” or equivalent prefix, not color or an icon alone (SC 1.4.1). `role="alert"` announces the refusal once without stealing focus (SC 4.1.3); the resumed line on Run Detail is part of the existing polite progress region, announced once. The run link is keyboard operable with visible, unobscured focus (SC 2.1.1, 2.4.7, 2.4.11), and its text names the run (“View run 30”, SC 2.4.4). The message wraps at 320 CSS px with no clipped text (SC 1.4.10). The message persists, so there is no time limit on reading it (SC 2.2.1). Run Now is not disabled by a refusal; during the pending request it follows the existing “Starting run…” rule.

**Acceptance observations:** With a real server, press Run Now with: a time-limit-interrupted run (resumed, same id, named, not a new run, Run Detail heading focused); two such runs (oldest named, the other unchanged); an active run (`production_run_active` copy and link); an unusable candidate for each closed reason plus an unknown reason (mapped sentence, no raw value, link to the run, no new run); startup recovery or shutdown (503 copy); a dropped connection (uncertain copy, no auto repeat). On Run Detail, check the interrupted copy appears only for a time-limit-interrupted manual run, reload drops the resumed line but not the status, and screen-reader and 320 px checks pass.

**Decision and capability gaps.** Rejected: a toast (disappears, no link), an Overview-only banner on success (the run page is where progress lives), and a Resume button on Run Detail (a press would act on the oldest candidate, not necessarily the one on screen, so a per-run button would mislead). Gaps, not resolved here: (1) the run API may not expose the interruption cause, which the Run Detail interrupted text needs (closed in a016744: the run API exposes `initiated_by` and `interrupt_cause`); (2) there is no saved “resumed” marker, so the resumption line is transient; (3) a candidate that stays unusable blocks all new runs through Run Now and the product offers the person no way to clear it, which is a recovery capability the requirements owner would need to decide; (4) 503 has no code to separate startup recovery from shutdown. Gap (1) is closed; gaps (2) and (4) remain. Gap (3) is answered in design by BR-017 AC13–21 and “Run Now — Discarding a Run That Cannot Be Resumed” below, pending implementation and the API facts listed there. Implementation is in a016744 (Overview refusal as persistent alert, Run Detail line and time-limit text); accessibility and browser behavior are unverified.

#### Run Now — Discarding a Run That Cannot Be Resumed

**Status and scope (2026-11-10, amended 2026-11-10 after the stop-run decisions):** Design for [BR-017](/docs/requirements/yuhaul/br-017-manual-run-initiation.md) AC13–AC22. Not implemented. It answers capability gap (3) above, extends “Run Now — Resume After a Time Limit”, and reuses US-009. It adds one control, one confirmation dialog and one acknowledgement. It adds no Resume or bulk action. Ending an active run is a different action: see “Stop an Active Run”.

**Walkthrough:** The administrator presses Run Now. Overview says run 30 stopped at its time limit, its saved settings are missing, and Run Now will keep refusing. Beside the link “View run 30” is the button “Discard run 30”. The administrator presses it. A dialog states that run 30 will be marked failed and never resumed, that its unfinished videos go to the deferred queue for the next run, and that nothing is sent to YouTube. They choose “Discard run 30”. The page then says run 30 is failed and its unfinished videos are in the deferred queue for the next run. The next Run Now press resumes the next stopped run, or starts a new run that processes the deferred videos. The same control is on run 30's own page, so the administrator can also discard it without first being refused. Observable success: the person can tell which run was discarded, that it will never run again, where its videos went, and that YouTube was not touched.

**Naming rule (two actions once shared the word “Discard”).** The deferred queue's button was renamed “Dismiss” on 2026-11-10. It removes one queue item and keeps the existing dialog “Dismiss this deferred item?”. The run action discards a whole run. To keep them apart:

- The run action is always labeled with the run: “Discard run {id}”. The bare word “Discard” is never used for it, in the button, dialog, toast or announcement.
- It never appears in the Errors view or inside a queue row. “Dismiss” is the only verb for a queue item, and “Discard” is the only verb for a run.
- The run dialog says “deferred queue” for the videos. It never says “dismiss”, and never “Error Queue”.
- The route editor's “Discard changes” and “Discard unsaved changes?” are unrelated and unchanged.

**Where the control appears.** Two places, one label. Both are driven by the discardable fact (AC22), not by the reason.

1. *Run Now refusal on Overview.* Inside the `resume_unavailable` message, next to “View run {id}”, for all five unusable reasons: `snapshot_unavailable`, `snapshot_unreadable`, `snapshot_inadmissible`, `checkpoint_unrecognized` and `terminal_timestamp_contradiction`. [BR-017](/docs/requirements/yuhaul/br-017-manual-run-initiation.md) AC11 says every press refused for an unusable run names a run the user can discard. For `recheck_at_write` and unknown reasons, show no button, only “View run {id}”: the run's own page shows the control if the run is discardable at that moment.
2. *Run Detail.* Whenever the run read says this run is the discardable run (AC22). This covers all five reasons. Place it in the interrupted-run explanation, replacing the sentence “This run continues only if it is the oldest and can be resumed safely.” Added text: “Run {id} cannot be resumed. {Reason sentence} Run Now will keep refusing until run {id} is discarded.” then the button “Discard run {id}”. Use the reason sentence for the five reasons from the `resume_unavailable` table when the read gives the cause. When it does not, use: “Its saved data cannot be used to continue it safely.” If the read does not say the run is discardable, or says it is not, show no control and no refusal promise.

The button is a normal secondary button with a text label. It is not shown in run lists. At most one run shows it at a time. A stale page can show it for a run that is no longer discardable; the server rechecks when the discard is carried out (AC13) and the person then sees a refusal. The UI never infers discardability from run age, status or interruption cause.

**Confirmation dialog.** Opens when the button is pressed. Nothing is sent until the person confirms. A modal dialog, not a toast or inline toggle.

- Title: “Discard run {id}?”
- Opening line: “Run {id} stopped at its time limit and cannot be resumed. {Reason sentence}”
- Facts, as a list, in this order:
  - “Run {id} will be marked failed. It will never be resumed.”
  - “Its unfinished videos will be put in the deferred queue. The next new run, from Run Now or the schedule, will process them automatically.”
  - “Nothing is sent to YouTube. Playlist additions already recorded stay recorded. An addition whose outcome could not be confirmed stays marked as unconfirmed and still blocks a repeat.”
  - “This cannot be undone.”
- Closing line: “Discarding does not start a run. After this, Run Now continues the next stopped run, or starts a new run if none is left. The deferred videos wait until a new run starts. The schedule is not affected.”
- Buttons: “Discard run {id}” (confirm, destructive styling plus text) and “Keep run {id}” (cancel).
- Behavior: initial focus is on “Keep run {id}”. Escape, “Keep run {id}”, and clicking outside the dialog all close it and change nothing. Focus returns to the “Discard run {id}” button. The confirm button is never the default action of Enter.
- While the request is pending: confirm button reads “Discarding run {id}…”, both buttons are disabled, the dialog stays open and is marked busy. A second press cannot be sent.

**Acknowledgement after a successful discard.** A persistent inline message, not a toast, with a “Discarded:” text prefix, at the top of the page where the person acted (below the Overview header, or at the top of Run Detail above the status region). It replaces the refusal message when on Overview.

“Discarded: run {id} is now marked failed and will not be resumed. Its unfinished videos are in the deferred queue, and the next new run will process them automatically. Nothing was sent to YouTube. Press Run Now to continue with the next stopped run, or to start a new run.”

Links: “View deferred queue” (the Deferred tab of Errors) and, when not already on it, “View run {id}”. Announce once with `role="status"`. Because the discard control disappears, move focus to this message (programmatic focus, no scroll jump past it). It stays until dismissed, until Run Now is pressed, or until the person leaves the page. On Run Detail the page re-reads the run: its status changes to “Run failed — discarded by user.” (see the comparison in “Mark a Legacy Run as Ended”) and shows the saved reason. The saved reason is server-written and API-vetted; show it as the existing failed-run row does, and never raw text. Run history and Run Detail show that reason so a discarded run can be told from one that failed alone (AC16). Run Now stays available throughout and is never started or resumed by the discard.

**Refusals.** Each says the run was not discarded and nothing was changed, as AC20 guarantees, except the unconfirmed case. The UI branches on status and machine code, never message text. The dialog stays open for refusals the person may retry; for the others it closes and the message appears as a persistent inline `role="alert"` message with the prefix “Discard did nothing.”, no focus move, and the page re-reads the run so the control disappears when no longer valid. A refusal never starts or resumes any run.

| Case | Dialog | Message |
|---|---|---|
| Run does not exist | closes | “Discard did nothing. Run {id} was not found. Nothing was changed.” Link “View runs”. |
| Run already `failed` or `completed`, including already discarded | closes | “Discard did nothing. Run {id} has already ended, so it was not discarded again. Nothing was changed.” Link “View run {id}”. |
| Not the discardable run | closes | “Discard did nothing. Run {id} cannot be discarded now. Only the run that is blocking Run Now can be discarded, and run {id} is not that run. Its state may have changed. Nothing was changed.” Links “View run {id}” and “Go to Overview”. |
| Production run active | stays open | “Discard did nothing. A production run is already running. Run {id} was not discarded and nothing was changed. Wait for it to finish, then try again.” “View run” link when known, else “View runs”. |
| 503: startup recovery or shutdown | stays open | “Discard did nothing. YuHaul can't discard a run right now. It may be starting up or shutting down. Nothing was changed. Try again in a minute.” “View runs”. |
| Save failed; run stays interrupted | stays open | “Discard did nothing. Run {id} could not be saved as discarded, so it is still interrupted and nothing was changed. You can try again.” |
| Other error, or no response | closes | “Could not confirm whether run {id} was discarded. Open the run to check its status before trying again.” Link “View run {id}”. No automatic retry. The saved state is either fully discarded or unchanged, never a mixture (AC15). |

Where the dialog stays open, the message appears inside it with `role="alert"` and the buttons re-enable. A repeated or double-submitted discard is refused as “already ended”, and the person sees the one result.

**Accessibility.** Target WCAG 2.2 AA. The dialog is modal with an accessible name (“Discard run {id}?”) and description (the opening line and facts); focus is trapped, moves in on open, and returns to the trigger on cancel (SC 2.4.3, 2.1.2 no trap because Escape exits). Every control's visible label contains the run id, so voice and screen-reader users hear which run (SC 2.5.3, 2.4.6). The destructive button is marked by its text, not color alone (SC 1.4.1). Status messages use `role="status"`; errors use `role="alert"` without stealing focus (SC 4.1.3); the one intended focus move is to the acknowledgement after the trigger is removed. Buttons and links meet target size (SC 2.5.8), focus is visible and unobscured (SC 2.4.7, 2.4.11), and the dialog and messages reflow at 320 CSS px with no clipped copy (SC 1.4.10). Messages persist, so reading is not time-limited (SC 2.2.1). Reduced motion needs no animation.

**Acceptance observations:** With a real server and an unusable oldest run: the control appears in the refusal and on Run Detail with matching labels; the dialog names the run and states the four facts; cancelling by each route changes nothing and returns focus; confirming shows the pending state, then the acknowledgement; Run Detail shows “Run failed.” with the reason and the Deferred tab lists the run's videos as pending and eligible now. Press Run Now afterwards and see the next run resumed or a new run started that processes them. Check the control on Run Detail for each of the five reasons, every refusal row, a double submit, a dropped connection, a usable or younger unusable run (no control), and keyboard, screen-reader and 320 px passes. “Discard run {id}” and “Dismiss” are never confusable in label, dialog or announcement.

**Capability gaps and limits.** (1) AC22 gives Run Detail the discardable fact. The 409 `resume_unavailable` response still gives only `run_id` and `reason`, so the Overview button follows the five-reason rule above; if the response also carries the discardable fact, use that instead. (2) AC22 says whether a run is discardable, not why. If the run read omits the cause, Run Detail uses the generic sentence above. (3) No discard endpoint, error codes or reason text exist yet; the table above names cases by meaning (AC20) and the architect defines the codes. (4) The count of unfinished videos is not known to the UI, so the dialog does not state a number; add one only if the API supplies it. (5) The 503 case still cannot separate startup recovery from shutdown. No code, browser or accessibility verification is claimed.

**Deferred rows handed over by a discard or a stop.** Each such video has one pending deferred item with no waiting time. On the Deferred tab, show its reason as “Waiting for the next run. Run {id} was discarded.” or “Waiting for the next run. Run {id} was stopped by user.”, and its next-eligibility indicator as “Eligible for the next run”. Use the API-vetted reason, never raw text; if the reason is unavailable, use “Waiting for the next run.” These rows keep the existing “Dismiss” and manual retry controls. Dismissing one keeps the existing copy “It will not be retried”, which is true: the video then waits for nothing.

#### Stop an Active Run

**Status and scope (2026-11-10):** Design for [BR-035](/docs/requirements/yuhaul/br-035-stop-active-run.md) AC1–AC21. Not implemented. It reuses US-009 and extends “Run Now — Recorded Progress and Closure”. It adds one control, one confirmation dialog, one acknowledgement, a “Stopping” state and a “Stopped by user” final status. It adds no Pause, Resume or Undo.

**Walkthrough:** Scheduled run 41 is adding videos to playlists. The administrator opens run 41 and presses “Stop run 41”. A dialog says run 41 will stop and never be resumed, that it will show as “Stopped by user” and not as failed, that its unfinished videos go to the deferred queue for the next run, and that nothing more is sent to YouTube. They choose “Stop run 41”. The page says run 41 is stopping, and the status reads “Stopping” rather than “Running”. A short time later the status reads “Stopped by user”, and the page says its unfinished videos are in the deferred queue. Run Now becomes available again. An addition that was already sent without an answer still shows as unconfirmed. Observable success: the person can tell that the stop was accepted, that the run is not yet finished stopping, when it has settled, and that a stop is not a failure.

**Words used.** Control: “Stop run {id}”. In progress: “Stopping”. Final: “Stopped by user”. The verb “stop” is only for an active run. It is never used for an interrupted run, for the time limit, or for shutdown; those keep “interrupted”. “Stopped by user” is never shown as “failed”, “cancelled” or “aborted”.

**Where the control appears.** On every surface that presents an active run, one button per run per view, labeled with the run's id:

- Run Detail: in the persistent status region, after the status line and counts.
- Overview: in the latest-run panel when that run is active, next to “Run in progress” and “View run”. A run shown in that panel does not repeat the button in the activity feed.
- Run List and Overview activity feed: in the row of an active run.

It applies to any active run: manual, scheduled, queue retry, a run being resumed by Run Now or by startup recovery, and dry run, replay and backtest. It is shown only while the saved status says the run is running and the run read says `executing` is true (the server is actually executing it; an absent `executing` shows no Stop). It is not shown for an interrupted run that is not executing (including a discardable run), for a completed, failed or stopped run, or for any run whose `endable` fact is true (a stuck legacy run that is marked running but is not executing; see “Mark a Legacy Run as Ended”). A run with `endable` true is never presented as running normally, never gets “Run in progress”, and does not keep Run Now unavailable. The UI does not decide whether a run is really executing; the server decides at the moment of the press (AC18) and the person sees a refusal if the state has moved on.

**Confirmation dialog — production run** (manual, scheduled, queue retry, or a run being resumed). Modal, opened by the button. Nothing is sent until the person confirms.

- Title: “Stop run {id}?”
- Opening line: “Run {id} is {run kind} and is running now.” where {run kind} is “a Run Now run”, “a scheduled run”, “a queue retry run” or “a run being resumed”.
- Facts, as a list, in this order:
  - “Run {id} will stop and will never be resumed. It will be shown as “Stopped by user”, not as failed.”
  - “Its unfinished videos will be put in the deferred queue. The next new run, from Run Now or the schedule, will process them automatically.”
  - “No new request is sent to YouTube after you confirm. A playlist addition that was already sent and has no answer yet will be shown as unconfirmed and will not be sent again automatically.”
  - “Results saved so far are kept. This cannot be undone.”
- Closing line: “Stopping can take a moment while the run saves its work.”
- Buttons: “Stop run {id}” (confirm, destructive styling plus text) and “Keep run {id} running” (cancel).

**Confirmation dialog — simulation** (dry run, replay or backtest). Same structure and behavior.

- Title: “Stop run {id}?”
- Opening line: “Run {id} is {a dry run | a replay | a backtest} and is running now. It makes no changes to your playlists.”
- Facts:
  - “Run {id} will stop and will never be resumed. It will be shown as “Stopped by user”.”
  - “Results saved so far stay visible and are marked incomplete.”
  - “Nothing is added to the deferred queue, and no queue item or playlist is changed.”
  - “No new request is sent to YouTube after you confirm. This cannot be undone.”
- Buttons: “Stop run {id}” and “Keep run {id} running”.

**Dialog behavior (both).** Initial focus is on “Keep run {id} running”. Escape, that button and clicking outside the dialog close it and change nothing; focus returns to the “Stop run {id}” button. Enter on the confirm button is never a default. While the request is pending, the confirm button reads “Stopping run {id}…”, both buttons are disabled, the dialog stays open and is marked busy, and a second press cannot be sent.

**Acknowledgement.** When the server accepts the stop, close the dialog and show a persistent inline `role="status"` message with the prefix “Stopping:”, at the top of the page where the person acted (below the Overview header, or at the top of Run Detail above the status region). Move focus to this message, because the button is replaced.

“Stopping: run {id} is stopping and will never be resumed. It is not stopped yet. The status will change to “Stopped by user” when it has finished saving.”

Link “View run {id}” when not already on it. The message stays until dismissed or the page is left. It does not claim that videos are already in the deferred queue; that is said only after the run settles.

If the server says the run is already stopping or already stopped (a repeated or double stop), it is not a refusal. Show, with the same prefix and no alert role: “Stopping: run {id} is already stopping.” or “Run {id} is already stopped by user.” Both link “View run {id}”.

**The “Stopping” state.** From acceptance until the run settles:

- Status line on Run Detail: “Stopping — {phase label}. Run {id} has been told to stop and is saving its work.” Never “Running”, “Completed” or “Failed”.
- The Stop button is replaced by plain text “Stopping run {id}…”. No other run control appears.
- Run List row, Overview latest-run panel and activity feed: status “Stopping”. Overview Run Now area: “Run {id} is stopping” with a “View run” link; Run Now stays unavailable until the saved status reaches a final state.
- Keep the five-second saved read; a saved final state replaces this state. No percent, ETA or time-based claim, and no “stuck” claim.
- If saved reads fail, show the existing “Could not refresh status…” copy with “Last known status: Stopping”. Do not claim the run has stopped.

**Final status: “Stopped by user”.**

- Badge and run list status text: “Stopped by user”, in a neutral status style with a stop-square icon and the text. It is not the failed, completed or interrupted style and is not shown in an error color. Add a token only if none of the existing neutral status styles fits.
- Run List: the status filter gains “Stopping” and “Stopped by user”, kept separate from “Failed”, “Completed” and “Interrupted”. Overview totals and run statistics never count a stopped run as failed or completed.
- Run Detail status line, production run: “Run stopped by user. Last recorded phase: {phase label}.” followed by “Run {id} will not be resumed. Its unfinished videos are in the deferred queue, and the next new run will process them automatically.” with the link “View deferred queue”. Recorded counts and evidence stay inspectable.
- Run Detail status line, simulation: “Run stopped by user. Last recorded phase: {phase label}. Results are incomplete because the run was stopped.” No deferred-queue sentence.
- A stopped simulation with no result rows shows “No results were recorded before the run was stopped.” It does not use the completed-dry-run selector explanation, and never says nothing matched.
- A playlist addition that was sent with no answer keeps the existing “Playlist outcome unknown” presentation. Stopping does not add, hide or reclassify it.
- Deferred rows handed over by the stop follow “Deferred rows handed over by a discard or a stop” above.

**Refusals.** Prefix “Stop did nothing.” Each says nothing was changed and no YouTube call was made, as AC18 guarantees. The UI branches on status and machine code, never message text. Where the dialog closes, the message appears as a persistent inline `role="alert"` on the page, with no focus move, and the page re-reads the run so the button disappears if no longer valid.

| Case | Dialog | Message and links |
|---|---|---|
| Run does not exist | closes | “Stop did nothing. Run {id} was not found. Nothing was changed.” “View runs”. |
| Run already ended (`completed` or `failed`, or ended by itself first) | closes | “Stop did nothing. Run {id} has already ended, so there is nothing to stop. Nothing was changed.” “View run {id}”. |
| Run is not active (interrupted and not executing; startup recovery has not yet started resuming it) | closes | “Stop did nothing. Run {id} is not running, so there is nothing to stop. Nothing was changed.” “View run {id}”. If the run read says it is the discardable run, add: “It cannot be resumed. You can discard it from its run page.” |
| Shutdown has begun | stays open | “Stop did nothing. YuHaul is shutting down, so run {id} was not stopped. Nothing was changed. The shutdown will interrupt the run, and you can stop it again once it is running again.” “View runs”. |
| 503 without a shutdown code | stays open | “Stop did nothing. YuHaul can't accept a stop right now. It may be shutting down. Nothing was changed. Try again in a minute.” “View runs”. |
| Other error, or no response | closes | “Could not confirm whether run {id} was stopped. Open the run to check its status. If it is still running, you can press Stop again; stopping twice has the same result as stopping once.” “View run {id}”. No automatic retry. |

In the “stays open” rows the message appears inside the dialog with `role="alert"` and the buttons re-enable. A run that ends by itself at the same moment as the press gets one of the first three rows, or the stop takes effect; the page never shows a mixture (AC21). The refusal never starts, resumes or changes any other run.

**Accessibility.** Target WCAG 2.2 AA. The dialog is modal with an accessible name (“Stop run {id}?”) and description (opening line and facts); focus is trapped, moves in on open, and returns to the trigger on cancel; Escape exits (SC 2.4.3, 2.1.2). Every control's visible label contains the run id (“Stop run 41”), so more than one stop button on a list is distinguishable (SC 2.5.3, 2.4.6). The destructive button is marked by its text, not color alone, and “Stopped by user” is text plus icon, not color alone (SC 1.4.1). “Stopping” and the final state are announced politely once each (`role="status"`, SC 4.1.3); polls, counts and timestamps are not announced. Refusals use `role="alert"` without stealing focus; the one intended focus move is to the acknowledgement after the trigger is replaced. Buttons and links meet target size (SC 2.5.8), including inside compact list rows. Focus is visible and unobscured (SC 2.4.7, 2.4.11). Dialog and messages reflow at 320 CSS px with no clipped copy and no hidden Stop button in list rows (SC 1.4.10). Messages persist (SC 2.2.1). Reduced motion needs no animation.

**Acceptance observations:** With a real server, stop each run kind (Run Now, scheduled, queue retry, resumed, dry run, replay, backtest) from Run Detail and, where shown, from Overview and the Run List. Check: the dialog differs for production and simulation; cancelling by each route changes nothing and returns focus; confirming shows the pending state, the acknowledgement and then “Stopping” until the saved status changes to “Stopped by user”; the final status is not styled or counted as failed or completed; the status filter finds it; a production stop says the unfinished videos are in the deferred queue and the Deferred tab shows them as eligible now; a stopped simulation says its results are incomplete and the Deferred tab is unchanged; an addition sent without an answer still shows as unconfirmed; and the schedule is unaffected. Check every refusal row, a double press, a dropped connection, a stop pressed as the run ends, a stop during startup recovery, and keyboard, screen-reader and 320 px passes. Run Now is unavailable while the run is stopping and available after it has stopped.

**Capability gaps and limits.** (1) No stop endpoint, statuses or error codes exist yet. “Stopping” and “Stopped by user” are design labels for states the run API must expose; the architect defines the values. The UI branches on them, never on message text. (2) The UI cannot tell “already stopping” and “already stopped” from a refusal unless the response distinguishes them (AC20). (3) The response cannot yet separate shutdown from other 503 causes; the fallback row covers it. (4) The UI does not know how many videos were handed over, so no count is shown. (5) The run read does not yet say whether an addition is unconfirmed because of a stop; the existing per-video evidence presentation is used. (6) Whether the Overview activity feed and Run List rows are tall enough for a safe Stop button at 320 px is untested; if not, keep the button on Run Detail and Overview only and show a “View run {id}” link in rows. No code, browser or accessibility verification is claimed.

#### Mark a Legacy Run as Ended

**Status and scope (2026-11-11):** Design for [BR-036](/docs/requirements/yuhaul/br-036-mark-legacy-run-as-ended.md) AC1–AC12. Implemented 2026-11-11 (see [ARS-004 §9](/docs/architecture/run-initiation/specifications/ars-004-mark-legacy-run-as-ended.md#9-implementation-record-and-deviations)); browser and accessibility verification pending. It reuses US-009. It adds one control on Run Detail, one confirmation dialog, one acknowledgement, and a display for a run ended this way. It adds nothing to Overview, run lists or queues.

**Walkthrough:** The administrator opens run 14. It is saved as running, but nothing is executing it, and it was created by an older version of YuHaul. Run Detail says so, says it cannot be resumed and will not end by itself, and offers “Mark run 14 as ended”. A dialog says run 14 will be marked failed, its unfinished videos will not be processed, and nothing else changes. They confirm. The page says run 14 is marked failed, and its status reads “Run failed — marked as ended by user (legacy run)”, with its saved results unchanged. Observable success: the person can tell that this run was ended by them, that it did not fail by itself and was not discarded or stopped, and that its videos were left unprocessed.

**The three run-ending actions use different words.** They apply to different runs and do different things to the unfinished videos. Never use one action's label, verb or final status for another.

| | Stop | Discard | Mark as ended |
|---|---|---|---|
| For | A run that is executing now | A time-limit run that cannot be resumed | A legacy run stuck as running or interrupted |
| Button | “Stop run {id}” | “Discard run {id}” | “Mark run {id} as ended” |
| Final status line | “Run stopped by user.” | “Run failed — discarded by user.” | “Run failed — marked as ended by user (legacy run).” |
| Unfinished videos | Deferred queue, next new run processes them | Deferred queue, next new run processes them | Not processed, not added to any queue |
| Counted as | Stopped, not failed | Failed | Failed |

The word “ended” is only used for this action's button and status. “End run”, “Stop” and “Discard” are never used for it.

**Where the control appears.** On Run Detail only, in the status region after the status line, and only when the run read says `endable` is true (BR-036 AC12). It is not shown on Overview, in run lists, or for any run whose `endable` is false or missing. The UI never infers it from status, run age or version. A stale page can show it after the run stopped being endable; the server rechecks when the action is carried out (AC2) and the person sees a refusal.

**What Run Detail shows for an endable run.** It replaces the normal progress and interrupted text for that run. Show no “Running — {phase label}”, no progress claim, no Stop button and no “Run Now continues this run” text.

- Saved status running: status line “Marked as running — nothing is executing it.”
- Saved status interrupted: status line “Run interrupted.”
- Then, for both: “Run {id} was created by an older version of YuHaul and cannot be resumed. It will not end by itself. Last recorded phase: {phase label}. Work saved so far is kept.” Keep the recorded counts and evidence inspectable.
- Then: “You can mark it as ended. That marks it failed and leaves its unfinished videos unprocessed.” and the button “Mark run {id} as ended”.

**Confirmation dialog.** Modal, opened by the button. Nothing is sent until the person confirms.

- Title: “Mark run {id} as ended?”
- Opening line: “Run {id} was created by an older version of YuHaul. Its saved status is {running | interrupted}, but nothing is executing it and it cannot be resumed.”
- Facts, as a list, in this order:
  - “Run {id} will be marked failed. It will never be resumed.”
  - “Its unfinished videos will not be processed. Unlike stopping or discarding a run, this does not add them to the deferred queue or the Error Queue.”
  - “Nothing else changes. Its saved results, playlist records, send guards and queue items stay as they are. A playlist addition whose outcome could not be confirmed stays marked as unconfirmed. Nothing is sent to YouTube.”
  - “This cannot be undone.”
- Closing line: “This does not start or resume any run. The schedule is not affected.”
- Buttons: “Mark run {id} as ended” (confirm, destructive styling plus text) and “Keep run {id} as it is” (cancel).
- Behavior: initial focus is on “Keep run {id} as it is”. Escape, that button and clicking outside close the dialog and change nothing; focus returns to the “Mark run {id} as ended” button. Enter is never a default on the confirm button. While pending, the confirm button reads “Marking run {id} as ended…”, both buttons are disabled, the dialog is marked busy, and a second press cannot be sent.

**Acknowledgement.** On success, close the dialog and show a persistent inline `role="status"` message with the prefix “Marked as ended:” at the top of Run Detail, above the status region. Move focus to it, because the button disappears.

“Marked as ended: run {id} is now marked failed. You ended it as a legacy run, and it will not be resumed. Its unfinished videos were not processed and were not added to any queue. Nothing was sent to YouTube.”

Link “View runs”. It stays until dismissed or the page is left. The page then re-reads the run and shows the display below.

**Run Detail display of a run ended this way.** The saved status is failed. When the run read carries the machine reason for this action (a stand-in for “ended by user (legacy run)”; the UI branches on that value, never on message text):

- Status line: “Run failed — marked as ended by user (legacy run).”
- Explanation: “You marked this run as ended. It did not fail by itself. Its unfinished videos were not processed and were not added to any queue. Its saved results, queue items and playlist records were not changed. Nothing was sent to YouTube.”
- Keep “Last recorded phase: {phase label}” and the recorded counts and evidence inspectable. An unconfirmed playlist addition keeps its existing “Playlist outcome unknown” presentation.
- No “Mark as ended”, Stop, Discard or Resume control. No deferred-queue sentence.
- A run list row for it shows status “Failed” and, below it, “Ended by user (legacy run)”. It counts as failed.

For comparison, a discarded run reads “Run failed — discarded by user.” with its API-vetted reason and the deferred-queue sentence from the Discard acknowledgement. A run that failed by itself keeps the existing “Run failed.” row with its API-vetted reason and no “by user” wording. If the run read does not carry a machine reason that identifies the cause, show “Run failed.” and the API-vetted reason as text, and make no claim about who ended it.

**Refusals.** Prefix “Mark as ended did nothing.” Each says nothing was changed and no YouTube call was made, as AC9 guarantees, except the unconfirmed case. The UI branches on status and machine code, never message text. Where the dialog closes, the message appears as a persistent inline `role="alert"` message at the top of Run Detail, with no focus move, and the page re-reads the run so the button disappears if no longer valid. A refusal never starts, resumes or changes any run.

| Case | Dialog | Message and links |
|---|---|---|
| Run does not exist | closes | “Mark as ended did nothing. Run {id} was not found. Nothing was changed.” “View runs”. |
| Run is not a legacy run (created by the current version) | closes | “Mark as ended did nothing. Run {id} is not an older-version run, so it cannot be marked as ended. Nothing was changed.” “View run {id}”. |
| Run is a dry run, replay or backtest | closes | “Mark as ended did nothing. Run {id} is {a dry run | a replay | a backtest}, so it cannot be marked as ended. Nothing was changed.” “View run {id}”. |
| Run already ended (`completed`, `failed`, stopped by user, discarded, or already marked as ended) | closes | “Mark as ended did nothing. Run {id} has already ended. Nothing was changed.” “View run {id}”. |
| Run is executing now | closes | “Mark as ended did nothing. Run {id} is running now, so it cannot be marked as ended. Nothing was changed. To end a running run, use “Stop run {id}”.” “View run {id}”. |
| A production run is active | stays open | “Mark as ended did nothing. A production run is running, so run {id} was not marked as ended. Nothing was changed. Wait for it to finish or stop it, then try again.” “View runs”. |
| Startup recovery in progress | stays open | “Mark as ended did nothing. YuHaul is starting up and recovering runs, so run {id} was not marked as ended. Nothing was changed. Try again in a minute.” |
| Shutdown has begun | stays open | “Mark as ended did nothing. YuHaul is shutting down, so run {id} was not marked as ended. Nothing was changed. Try again after it has started again.” |
| 503 without a code for the two cases above | stays open | “Mark as ended did nothing. YuHaul can't mark a run as ended right now. It may be starting up or shutting down. Nothing was changed. Try again in a minute.” |
| Save failed; run keeps its earlier status | stays open | “Mark as ended did nothing. Run {id} could not be marked as ended, so it keeps its earlier status ({running | interrupted}). Nothing was changed. You can try again.” |
| Other error, or no response | closes | “Could not confirm whether run {id} was marked as ended. Open the run to check its status. If it is not marked failed, you can try again; doing it twice has the same result as doing it once.” “View run {id}”. No automatic retry. |

Where the dialog stays open, the message appears inside it with `role="alert"` and the buttons re-enable. A double submit or a repeat in a stale tab is answered “has already ended” and leaves the one result (AC10).

**Accessibility.** Target WCAG 2.2 AA. The dialog is modal with an accessible name (“Mark run {id} as ended?”) and description (the opening line and facts); focus is trapped, moves in on open and returns to the trigger on cancel; Escape exits (SC 2.4.3, 2.1.2). Every control's visible label contains the run id (SC 2.5.3, 2.4.6). The destructive button and the status “Run failed — marked as ended by user (legacy run).” are conveyed by text, not color alone (SC 1.4.1). Status messages use `role="status"` and errors `role="alert"` without stealing focus (SC 4.1.3); the one intended focus move is to the acknowledgement after the trigger disappears. Targets, focus visibility and 320 CSS px reflow follow the Discard and Stop sections (SC 2.5.8, 2.4.7, 2.4.11, 1.4.10). Messages persist (SC 2.2.1).

**Acceptance observations:** With a real server and a run-14-shaped version-0 run saved as running, and a second saved as interrupted: Run Detail shows the legacy text and the button for each, with no “Running” claim, no Stop button and no resume promise; a run with `endable` false (a current-version run, a simulation, any run while a production run is active) shows no button. The dialog names the run, says it will be marked failed and that unfinished videos will not be processed; cancelling by each route changes nothing and returns focus. Confirming shows the pending state, then the acknowledgement, then the ended display, with videos, queues and unconfirmed additions unchanged. Compare the final status of an ended, a discarded, a stopped and a self-failed run: all four read differently. Check every refusal row, a double submit, a dropped connection, a production run started between showing and confirming, and keyboard, screen-reader and 320 px passes. Run Now is available afterwards and never resumes the ended run.

**Capability gaps and limits.** (1) No endpoint, error codes or machine reason for this action exist yet. The refusal table names cases by meaning (AC9); the architect defines the codes. The status line depends on a machine reason that identifies “ended by user (legacy run)” and, for the comparison, “discarded by user”; without it the UI shows only “Run failed.” and the vetted reason. (2) The 503 causes are not separable today; the fallback row covers them. (3) `endable` is specified on run reads (AC12); until it exists the control is not shown. (4) The UI cannot show a count of unprocessed videos, so none is stated. (5) Overview and run lists use the same read, so they must also stop treating an `endable` run as running; this is stated in “Stop an Active Run” but not browser-checked. No code, browser or accessibility verification is claimed.

**View: Decision Detail** — Inspect a single video's routing outcome; user job: understand exactly why this video was accepted, rejected, deferred, or failed — across all three layers of the outcome model.
- **Regions:**
  - Video info region: title, channel, duration, publish date, thumbnail
  - Processing outcome region: video/run-level lifecycle state badge (discovered/evaluating/evaluated/acted/completed/deferred/not-yet-evaluated)
  - Route decisions region: per-route evaluation results (accepted/rejected/skipped — skipped includes final-route-skip, exclusion-skip, and disabled-route-skip)
  - Action execution region: per-action results (succeeded/failed/deferred)
  - Evidence trail region: step-by-step route evaluation showing conditions matched/not-matched
- **Components:** Video metadata, processing outcome badge, route decision row (route name, decision, icon, skip reason if skipped), action execution row (action type, target, result), condition evaluation row (route name, condition, result, icon)

#### Run Video Recognition — Current Local Context

**Amendment scope (2026-11-06):** Improve recognition in the Run Detail video list and Decision Detail using permitted local title, source-channel display name and duration. This is the bounded independent read slice in [OC-005](../requirements/yuhaul/oc-005-decision-evidence-attribution-and-availability.md) and the provisional [decision-evidence architecture](../architecture/decision-evidence/design/ard-001-run-attributed-evidence-and-metadata-lifecycles.md), not full historical evidence delivery. Reuse US-013, US-014 and SC-002; the user's Run15 feedback shows that IDs and generic exclusion badges alone do not meet their recognition/explanation goal. No new story capability, subscription-name capability, metadata storage, date meaning, or retention permission is introduced. The full historical comparison/reproduction promise remains blocked, not waived by truthful unavailable copy.

**Walkthrough:** An operator opens a backtest's video list. Where permitted metadata exists locally now, the row shows a recognizable title and source channel, explicitly marked as current/local information. Opening detail preserves the same run/video and hypothetical outcome. The operator sees that local information separately from recorded route evidence. A legacy exclusion says its specific rule and compared input are unavailable; the current title never becomes an explanation of what the historical run compared. If metadata has expired, labeled identifiers remain usable without exposing the expired values or calling YouTube.

**Information hierarchy and exact copy:**

- Run Detail retains its existing outcome filters, pagination, mode-qualified heading and outcome column. Show the title as the video link, `Source channel` as the source label/column, and the recorded outcome independently. IDs are secondary reference text, never unlabeled title/name substitutes. Do not add a wall of topic pills or fetch detail per row to manufacture recognition.
- Place this persistent explanation directly above the video list: `Video information is current/local display context, not necessarily the information evaluated by this run.` The title/source columns belong to this explanation, not to the recorded evidence region. Each row with available display context also shows `Current/local information` beside/below its recognition text so the attribution survives mobile cards and partial views. When any row has route lines, add directly beneath it the list-level sentence defined in “Compact route lines on the Run Detail list”.
- In Decision Detail, preserve the existing page H1, run identity and Back to run link. The video-info card precedes the outcome and route evidence. Its title is the available display title or `Video title unavailable`. Show `Source channel: {name}` or `Source channel name unavailable`. Below the card heading, show `Current/local information — not necessarily the information evaluated by this run.` Label duration `Duration`; show a known zero as `0 seconds`, never as absent. Other known durations use the existing duration format.
- Show `Video ID: {id}` and `Channel ID: {id}` as secondary labeled text. In the list, keep the video ID visible when the title is unavailable, so identical unavailable-title links remain distinguishable; show channel ID when the source name is unavailable. In detail both IDs remain available regardless of recognition metadata. Preserve the accepted muted short UUID beside each route name; this amendment does not remove route disambiguation.
- Where a field has a trustworthy observation/fetch time, label it `Observed at {time}` alongside that field or a clearly associated shared timestamp. Do not label it as run-time input or use run start/discovery time as a substitute. Do not invent a timestamp when unknown. Apply availability independently: missing title does not hide an available duration or source name.
- Only title, source-channel display name and duration are added by this slice. Existing publication/thumbnail design is not a request to expose expired rows, load external images, or add those fields here. Do not infer a separate subscription name from channel identity. Current/local metadata may change between list and detail reads without changing the recorded decision.

**Per-field availability:** Use the following text only when supported by the inspection response. The UI does not diagnose absence from a null field, missing database row or empty string. No fingerprint/hash is a title; no fallback to a channel-name store with unestablished provenance/freshness is permitted.

| Field state | Title | Source-channel name | Duration |
|---|---|---|---|
| Available and eligible now | Display title | Display name | Known duration, including zero |
| Unavailable, cause unknown | `Video title unavailable` | `Source channel name unavailable` | `Duration unavailable` |
| Known expired | `Video title unavailable — local metadata expired` | `Source channel name unavailable — local metadata expired` | `Duration unavailable — local metadata expired` |
| Known not captured | `Video title not recorded` | `Source channel name not recorded` | `Duration not recorded` |
| Known withheld | `Video title unavailable for display` | `Source channel name unavailable for display` | `Duration unavailable for display` |
| Context read failed | Keep any independently available fields; identify the affected context with `Could not load local video information.` and `Retry local information`. Do not turn this into expired/not-recorded or hide successfully loaded decisions. | Same context failure | Same context failure |

Withhold expired/inactive fields at their release deadline, even if cleanup has not removed their stored values. No stale-value preview, raw sensitive reason, provider refresh control or YouTube call is introduced. `Retry local information` rereads local retained context for the same run/video only; no evaluation or mutation. If the response cannot separate context failure from a failed decision read, show the truthful decision-read failure rather than invent partial success. Exact API shape belongs to Architecture.

**Exclusion evidence:** The later “Causal Exclusion — Run List and Decision Detail” amendment below replaces the generic-only presentation for captured causes. Keep the overall outcome separate from route evidence. Generic-only historical rows remain incomplete; do not claim an absence was never recorded unless the evidence supports it.

An exclusion describes supporting route evidence, not the cause of every route or the whole video's category. Disabled and final-route skips retain their separate reasons; none is a failed condition or an execution failure. Never recompute a historical match from current metadata, configuration, or a snapshot rule list.

**Simulation and missing actions:** Retain the accepted hypothetical labels and persistent simulation explanation on both views. Recognition context does not imply fresh discovery or evaluation completion. Simulated actions remain `Not attempted`; no action record remains `No simulated actions recorded for this video.` Do not infer a playlist target, successful insertion, run failure, or complete route coverage from missing actions or a generic exclusion. Keep the existing production outcome/attempt distinctions unchanged.

**Read recovery and continuity:** Preserve the requested run/video on direct link, refresh, retry and Back. Missing run mode retains the existing safe context gate, never production-default interpretation. A conclusive missing decision shows `No decision record found for this video in this run.` A failed decision read ends loading with `Could not load decision evidence. This loading error does not determine the run's result.` Offer `Retry decision evidence` and `Back to run`. Retry only reads local retained information; late responses from another run/video never replace the selected record. Back restores the originating list filter/page where available. These recovery states express OC-005, not a new run lifecycle or restart capability.

**Accessibility/responsive checks:** Target WCAG 2.2 AA. Keep attribution, missing-field reasons and hypothetical qualifiers visible ordinary text, not hover-only content (SC 1.3.1, 1.4.1). At 320 CSS px and 200% text zoom, use the existing stacked video cards and single-column detail; titles, IDs, source names and unavailable copy wrap without clipping or page-level horizontal scrolling (SC 1.4.4, 1.4.10). Normal and muted text meet 4.5:1 contrast; controls/focus indicators meet applicable 3:1 non-text contrast (SC 1.4.3, 1.4.11). Title links and retry/Back controls have visible, unobscured keyboard focus and meaningful names; unavailable-title links include the video ID in their accessible name and visible secondary text (SC 2.1.1, 2.4.3, 2.4.7, 2.4.11, 4.1.2). Use at least 24 CSS px targets or applicable spacing exceptions, preferably 44 px for touch (SC 2.5.8). Navigation focuses H1; local-context retries/background reads do not steal focus. Announce context read failure/recovery once, politely, not every field or poll (SC 4.1.3). Keep the same reading order and labeled metadata relationships for screen readers.

**Downstream observations:** Check list → detail with available title/source/duration, partial fields, zero duration, unknown-cause absence, known expiry exactly at the deadline, known not-captured/withheld, and a context read failure/recovery with usable decisions. Change local metadata between reads: attribution remains current/local and historical cause remains unchanged. A Run15-shaped generic exclusion remains explicitly incomplete even beside a useful title; one excluded route plus a matching route retains the recorded overall category. Direct links and failed/not-found reads terminate truthfully; retry preserves identity and makes no YouTube call or new evaluation. Verify mobile, keyboard and screen-reader recognition without relying on pills, color or unlabeled IDs. These are intended checks, not observed implementation acceptance.

**Open authority boundary:** Full historical-input reproduction remains unresolved under OC-005/BR-015 and the architecture's retention boundary. The independently specified causal-reference explanation below does not resolve that boundary. Compliance/research and Architect own establishing a permitted realization; if it cannot meet the accepted promise, the product owner decides any precise scope amendment. This does not certify existing raw audit release/storage as safe or complete.

#### Causal Exclusion — Run List and Decision Detail

**Scope and sources (2026-11-07):** This amendment refines existing US-013, US-014 and SC-002, not a new workflow. It applies to production and simulation decisions under [BR-015 AC10–13](../requirements/yuhaul/br-015-explainable-route-decisions.md), [OC-005](../requirements/yuhaul/oc-005-decision-evidence-attribution-and-availability.md), and [decision-evidence ARS-002](../architecture/decision-evidence/specifications/ars-002-causal-exclusion-capture-and-inspection.md). The user's accepted exception for old generic-only records does not excuse missing capture in newly evaluated decisions. No retention permission or new external refresh is implied.

**Walkthrough:** An operator sees a rejected video in a run. Its list row separately says that route A did not match and route B was skipped by exclusion #3 in that run's configuration; the specific rule and comparison are visible without opening detail. In detail, route B says its recorded match prevented its conditions from being evaluated. The configured phrase appears only if permitted for display, and the unavailable compared input is labeled separately. If an old row has only a generic skip, both views say `Rule unavailable` and explain that the record is incomplete. A later rename or a current title does not rewrite this explanation. Simulation stays hypothetical.

**Placement and attribution:** Retain the video-level recorded outcome and mode labels separately. For every presented route decision on the Run Detail list row, show one compact route line directly with that video (see “Compact route lines on the Run Detail list” below), not only in a tooltip or detail. Show all such route causes, including when multiple routes exclude the same video; do not imply complete route coverage from a partial response. The full record is one control away (the route's “Details” disclosure) and in Decision Detail. In Decision Detail place the same information inside the corresponding route card after `Skipped — excluded from this route.` and before conditions/actions. Preserve other routes' distinct results. In Decision Detail and in the list's expanded route details show route UUID alongside its label (the list's compact line shows the short ID instead): `Route: {name} (ID: {uuid})` for `run_snapshot`; `Route: {name} (current/local name; ID: {uuid})` for `current_local`; `Route name unavailable (ID: {uuid})` for `unavailable` or unrecognized attribution. A current name is recognition only, not a historical name. If the UUID is unavailable due to a failed mandatory read, show the existing safe read failure, not an invented identity.

**Captured, supported cause:** For `available`, display `Recorded causal exclusion: configuration {config_version_id}, exclusion #{index + 1}.` The ordinal is the occurrence in that run's complete configuration, not a position in today's editor. Follow with `Matched rule: {comparison sentence}.` and `Conditions were not evaluated for this route because this exclusion matched.` Decision Detail and the list's expanded route details show all three facts as ordinary text. The list's compact line carries only the rule, in the vocabulary below. Render only released configured values as text, never HTML. Do not disclose or reconstruct a withheld value through another field.

| Type / comparison | Comparison sentence; `{value}` is the original permitted configured value |
|---|---|
| channel / exact | `Channel ID exactly equals configured channel ID “{value}”` |
| video / exact | `Video ID exactly equals configured video ID “{value}”` |
| title / trimmed_equal_fold | `Title equals configured title “{value}”, ignoring surrounding whitespace and case` |
| word / lowercase_substring | `Title contains configured substring “{value}”, ignoring case` |
| duration_min / strict_less_than | `Duration is strictly less than configured minimum {value} seconds` |
| duration_max / strict_greater_than | `Duration is strictly greater than configured maximum {value} seconds` |

For `configured_value_status: withheld`, replace the value with `value unavailable for display`, keeping the field, comparison and occurrence; add `Configured value unavailable for display.` For `unavailable`, replace it with `value unavailable` and add `Configured value unavailable.` Do not say the rule was not captured. Do not substitute blank or zero. After the comparison in Decision Detail and in each route's expanded details show `Compared input unavailable in this rule-reference record.` On the list, the one list-level sentence below covers it. This means the projection provides no historical input; it does not assert expiry, emptiness or a known reason. Do not substitute current/local metadata, another route's audit or current rules. The recorded match explains the branch, not independent reproduction.

**Incomplete and incompatible states:** Preserve the route's recorded skip and the video's recorded overall outcome in every case. For `not_recorded`, including old generic-only rows and a missing causal object from an older response, show `Route exclusion recorded; rule unavailable. This explanation is incomplete.` Do not assert the cause of absence or infer a rule from a single candidate. For `definition_unavailable`, show `Route exclusion recorded; the captured rule definition is unavailable. This explanation is incomplete.` For `unsupported`, show `Route exclusion recorded; the captured rule cannot be interpreted by this version. This explanation is incomplete.` For `invalid_reference`, show `Route exclusion recorded; the captured rule reference is invalid. This explanation is incomplete.` For an unknown status or comparison use the unsupported wording, never echo unknown raw values. Where a valid configuration ID and nonnegative occurrence are supplied for an unresolved reference, show `Captured reference: configuration {id}, exclusion #{index + 1}.` Show only supported scalars and never imply a usable definition or match. All these states also show `Compared input unavailable in this rule-reference record.` in Decision Detail and expanded details (the list uses its one list-level sentence). The first two unavailable situations must not be collapsed: absence of a captured rule and an unresolvable captured definition are different. No actual query/read failure becomes an unavailable-rule success.

**Compact route lines on the Run Detail list (2026-11-11 amendment).** A user report showed that the full explanation takes about eight lines per route, which hides which rule was rejected and why. The list now shows one compact line per route. Decision Detail is unchanged and keeps the full explanation. This changes presentation only; every truthfulness rule above still holds, moved to the expanded details and one list-level sentence.

*Walkthrough:* A rejected video's row shows its recorded outcome, then “Route results” with one line per route, in evaluation order: “New Listens (a3f5b2c1) — excluded: duration < 75 s” and “Fireship (6f1105be) — no match: needs channel name = “Fireship””. The person sees at once which route and which rule. Pressing “Details” on a line opens that route's full record in place: the full route ID, configuration and exclusion number, the full comparison sentence, and the compared-input note. Observable success: each route's outcome and key reason are readable without opening anything, and nothing is dropped, only moved.

*Line format.* One list item per route: `{route label} — {outcome}` or `{route label} — {outcome}: {reason}`. A line may wrap at narrow widths but never holds more than one route. The outcome is text, with an icon that is never its only signal.

*Route label.* The route name followed by the muted short ID, the first 8 hex characters of the route UUID, as in the accepted route-identity rule. The short ID is always shown, so same-named routes stay distinct.

| Attribution | Compact label |
|---|---|
| `run_snapshot` | `{name} ({id8})` |
| `current_local` | `{name} (current name, {id8})` |
| `unavailable` or unrecognized | `Route name unavailable ({id8})` |

*Group heading.* Above the lines: `Route results` for production runs, `Simulated route results` for dry run, replay and backtest. Simulated lines are otherwise worded the same; the heading and the existing hypothetical outcome and persistent simulation explanation carry the simulation labelling. No line says a playlist was changed. In the expanded details of a simulated route, the first line is `Simulated result. No playlist was changed.`

*Outcomes and reasons.*

| Recorded result | Compact line (example) |
|---|---|
| Matched | `Music Discovery (b7e9d4f2) — matched` |
| Did not match, failed condition available | `Fireship (6f1105be) — no match: needs channel name = “Fireship”` |
| Did not match, no failed condition on the list read | `Fireship (6f1105be) — no match` |
| Excluded, captured rule | `New Listens (a3f5b2c1) — excluded: duration < 75 s` |
| Skipped, earlier final route matched | `Ambient (c1d8e3a9) — skipped: an earlier final route matched` |
| Skipped, route disabled | `Old Route (d4e5f6a7) — skipped: route disabled` |

*Short rule vocabulary for exclusions* (`{v}` is the permitted configured value; text values appear in curly quotes, numbers and durations do not). The compact form is a summary; the full comparison sentence stays in the details. Screen readers get the spoken form, never the symbol (see Accessibility).

| Type / comparison | Compact | Spoken |
|---|---|---|
| channel / exact | `channel ID = “{v}”` | channel ID equals {v} |
| video / exact | `video ID = “{v}”` | video ID equals {v} |
| title / trimmed_equal_fold | `title = “{v}” (any case)` | title equals {v}, any case |
| word / lowercase_substring | `title contains “{v}” (any case)` | title contains {v}, any case |
| duration_min / strict_less_than | `duration < {v} s` | duration less than {v} seconds |
| duration_max / strict_greater_than | `duration > {v} s` | duration greater than {v} seconds |

*Short vocabulary for route conditions* (fields `title`, `channel_name`, `description`, `channel_id`, `duration_seconds`; operators as in the Route Editor). A failed condition is shown as what the route needs, because the list does not hold the video's compared value and “not equal” would claim one. No case or whitespace semantics are stated; the details show the operator and value as recorded.

| Field type / operator | Compact | Spoken |
|---|---|---|
| text / contains | `{field} contains “{v}”` | {field} contains {v} |
| text / equals | `{field} = “{v}”` | {field} equals {v} |
| text / starts_with | `{field} starts with “{v}”` | {field} starts with {v} |
| text / ends_with | `{field} ends with “{v}”` | {field} ends with {v} |
| text / matches_regex | `{field} matches pattern {v}` | {field} matches pattern {v} |
| duration_seconds / equals | `duration = {v} s` | duration equals {v} seconds |
| duration_seconds / greater_than | `duration > {v} s` | duration greater than {v} seconds |
| duration_seconds / less_than | `duration < {v} s` | duration less than {v} seconds |
| duration_seconds / between | `duration {min}–{max} s` | duration between {min} and {max} seconds |

`{field}` is `title`, `channel name`, `description` or `channel ID`. The line is `no match: needs {condition}`. If several conditions failed under AND, show the first in recorded order and add ` (+{n} more)`. If the route uses OR, show `no match: none of {k} conditions matched` and leave the conditions to the details. Values longer than 40 characters are cut to 40 with `…` inside the quotes and shown in full in the details; a cut value is never presented as complete.

*Truthfulness states, kept short.* Each replaces the rule part of the line, and the route's recorded outcome stays in front.

| State | Compact line (example) |
|---|---|
| Legacy, `not_recorded` | `Fireship (6f1105be) — excluded: rule unavailable (record incomplete)` |
| `definition_unavailable` | `Fireship (6f1105be) — excluded: rule definition unavailable (record incomplete)` |
| `unsupported`, unknown status or comparison | `Fireship (6f1105be) — excluded: rule not readable by this version (record incomplete)` |
| `invalid_reference` | `Fireship (6f1105be) — excluded: rule reference invalid (record incomplete)` |
| Value `withheld`, exclusion | `New Listens (a3f5b2c1) — excluded: duration less than (value unavailable for display)` |
| Value `unavailable`, exclusion | `New Listens (a3f5b2c1) — excluded: duration less than (value unavailable)` |
| Value `withheld` or `unavailable`, condition | `Fireship (6f1105be) — no match: needs channel name equals (value unavailable for display)` |
| Route name unavailable | `Route name unavailable (6f1105be) — excluded: duration < 75 s` |

Use the spoken wording as the text when a value is withheld or unavailable, so no symbol stands next to a missing value. Never echo an unknown raw value. Never write that a rule was not captured when it was captured but cannot be read, and the two unavailable states keep their separate wording. A failed read of the list ends in the existing error and retry state, not in one of these lines.

*List-level sentence.* Directly under the existing video-information sentence above the list, shown once and only when at least one row has route lines: `Route results show the recorded rule, not the video value that was compared. Use Details for the full record.` This takes the place of repeating “Compared input unavailable in this rule-reference record.” on every route.

*Expanded details.* Each route line ends with a “Details” disclosure button. Its visible text is `Details`. Its accessible name is `Details for route {name} ({id8})`, or `Details for route name unavailable ({id8})`; it has `aria-expanded` and `aria-controls`. Opening it shows, in this order and as ordinary text, only the parts that apply:

- `Simulated result. No playlist was changed.` (simulations only)
- `Route: {name} (ID: {uuid})`, or the `current_local` or `unavailable` form defined above. For `current_local`, add `This is the current route name, not necessarily the name when this run was evaluated.`
- For a captured exclusion: `Recorded causal exclusion: configuration {config_version_id}, exclusion #{index + 1}.` then `Matched rule: {comparison sentence}.` then `Conditions were not evaluated for this route because this exclusion matched.` then `Compared input unavailable in this rule-reference record.`
- For an incomplete state: the full accepted sentence from “Incomplete and incompatible states”, `Captured reference: configuration {id}, exclusion #{index + 1}.` where valid, and the compared-input sentence.
- For a no-match line whose conditions are on the list read: each condition as `{field} {operator} {value} — not met`. Otherwise: `Condition details are in the full decision detail.`
- A link `Open full decision detail` to this video's Decision Detail.

Opening or closing does not move focus, does not refetch, and does not change other lines. State is not kept across reload. Closed is the default.

*Many routes.* Show the first 5 route lines in recorded evaluation order. If more exist, show the button `Show {n} more route results` (accessible name `Show {n} more route results for {video title}`), which reveals the rest in the same order. The count keeps the list from implying that coverage is complete. Never reorder lines by outcome.

*Accessibility.* WCAG 2.2 AA. The group is a labelled list (`Route results` or `Simulated route results`); each route is one list item. Compact symbols (`=`, `<`, `>`, `–`) are hidden from assistive technology and replaced by the spoken wording (SC 1.3.1). Outcome is text plus icon, never color alone (SC 1.4.1). The “Details” button meets target size, has visible unobscured focus and is keyboard operable (SC 2.5.8, 2.4.7, 2.4.11, 2.1.1); its visible label is contained in its accessible name (SC 2.5.3). Wrapping at 320 CSS px and 200% zoom loses no text (SC 1.4.10, 1.4.4). Expanding does not steal focus; the expanded region is announced by `aria-expanded`, not a live region.

*Decision Detail.* Unchanged: each route card keeps `Skipped — excluded from this route.`, then the full causal block, with the full UUID and the compared-input sentence. The list's compact vocabulary is not used there.

*Gaps and decisions for others.* (1) Compact “no match” reasons need the list read to carry a short failed-condition summary. *Implemented (2026-11-11):* each not-matched route in the run-videos list may carry `failed_condition` with the first failed condition's `field`, `operator` and configured `value`, the route's `composition` (`AND`/`OR`), its `condition_count`, and `further_failed` (failed conditions after the first). These come only from the run's immutable configuration snapshot; the condition audit contributes only which conditions failed, read for the whole page in one query. The video's compared value and the stored expected value are never read or sent. Decision Detail does not carry it. When the run has no usable snapshot, the route is not in it, or the condition is outside the known vocabulary, the field is absent and the line reads `— no match` with the details pointing to Decision Detail. The list does not fetch detail per row. The details list only the first failed condition (the list read holds no more); the architect still owns whether this shape is final, and the same release rules as for exclusion values apply. (2) The brief's examples omit the short route ID and phrase the no-match reason as “channel name ≠ Fireship”. I kept the short ID because the accepted route-identity rule requires it beside every route name where identity matters, and I used “needs … =” because “≠” would assert the video's actual value, which the list does not hold, including when that value is missing. (3) Moving “Compared input unavailable…” off every list route onto a list-level sentence and the expanded details is a presentation choice; the Specifier should confirm it satisfies BR-015/OC-005, since earlier text asked for it on each surface. (4) Condition-operator case and whitespace semantics are not stated here because they are not in the accepted evidence; the details show the operator and value as recorded. No code, browser or accessibility verification is claimed.

**Read and accessibility behavior:** Existing list/detail loading, failed-read retry, not-found, local-context partial failure and Back behavior remain as in this specification. Retry rereads the same run/video locally, without evaluating, mutation or provider refresh; stale responses cannot replace the selected run/video. A missing run mode stays unresolved, never assumed production. Apply WCAG 2.2 AA: route, skip, comparison, attribution and limitations are real text in reading order, not color or hover alone (SC 1.3.1, 1.4.1); at 320 CSS px and 200% zoom, each cause wraps without loss (SC 1.4.4, 1.4.10). List links and retry controls remain keyboard reachable with visible, unobscured focus (SC 2.1.1, 2.4.7, 2.4.11); entering detail focuses its heading and inline updates do not steal focus. Verify with keyboard, screen reader and viewport checks; this design is not an accessibility certification.

**Implementation checks and limits:** Exercise list and detail for each supported type, both duration equality boundaries and substring semantics; duplicate/changed/reordered rules must still identify the first recorded occurrence. Exercise multiple excluded routes, a nonmatching route plus an exclusion, a separate matched route, both production and each simulation, missing/withheld values, unresolved/unsupported/invalid references, old generic-only reuse and a new decision for the same video. Confirm no action or playlist target is inferred for an excluded route. A failed mandatory read ends in the existing error/retry state, not a legacy placeholder. Prove the composed producer → durable record → API → real-client list/detail path and restart; shaped fixtures alone do not prove capture. Positive release of configured values requires the separate compliance/safety decision identified by ARS-002; until then withholding preserves the partial cause but does not prove permitted-value acceptance. Full historical reproduction remains unresolved outside the accepted legacy exception.

#### Simulation Inspection — Requested-Run Results

**Amendment scope:** Correct the interpretation and wording of existing Run Detail and Decision Detail, not a new simulation workflow or visual direction. Applies to `backtest`, `replay`, and `dry_run`; `manual` and `scheduled` remain actual production runs. Existing US-011, US-012 and SC-005 supply the stories/scenario; no duplicate story artifacts are added.

**Walkthrough:** A person opens a completed backtest to see whether a proposed rule would accept a video. They see “Backtest run 2” and a persistent simulation explanation before the results. Choosing Accepted shows “Hypothetical accepted” even if that video's production state is still `evaluated`. Opening the video preserves the backtest context and shows the matching rule and “Not attempted” simulated action. A second video already completed in production can correctly show “Hypothetical rejected”. Neither view implies a playlist insertion occurred.

**Human-experience authority:** [BR-027 AC6](/docs/requirements/yuhaul/br-027-historical-backtesting.md), US-012 above, and [ARS-005, Recovery and inspection membership / Counting and projection](/docs/architecture/duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md). This section refines the two view descriptions above for non-production inspection; it does not alter production outcome or execution semantics.

**Exact mode/context copy:**

| Requested run type | Run Detail H1 | Decision Detail H1 | Persistent explanation on both views, immediately after header |
|---|---|---|---|
| `backtest` | `Backtest run {id}` | `Backtest decision detail` | `Simulation only. Results are hypothetical; no playlist actions were executed by this run.` |
| `replay` | `Replay run {id}` | `Replay decision detail` | Same simulation explanation. |
| `dry_run` | `Dry-run {id}` | `Dry-run decision detail` | Same simulation explanation. |
| `manual`, `scheduled` | Existing `Run {id}` | Existing `Decision Detail` | No simulation explanation. Preserve real production state and results. |

Simulation Decision Detail also shows `Run {id}` beside/below the H1 so a direct link identifies the inspected run. Explanation is persistent ordinary text, not a dismissible warning or hover-only tooltip. “No playlist actions” does not claim dry-run/replay make no read requests. Keep Backtest's separate no-external-call constraint intact.

**Simulation regions and labels:**

- Put summary cards under H2 `Hypothetical results`. Use `Evaluated videos` instead of `Discovered`: this existing count represents distinct videos with decisions, not fresh discovery or complete selected-corpus coverage. Keep outcome card labels `Accepted`, `Rejected`, `Deferred`, `Failed` within this explicitly hypothetical region; no new metrics are added. A run's completion means the simulation finished, not that playlist actions succeeded.
- Video-list H2: `Hypothetical video outcomes`; filter label and outcome column/mobile data label: `Hypothetical outcome`. Keep `All outcomes` and the existing category option labels/values. Filter, totals, row badges and detail outcome all describe the same requested run.
- Decision Detail H2: `Layer 1 — Hypothetical outcome`; H2 `Layer 2 — Route decisions` stays unchanged. Hide the production `Processing state: …` line and production deferred/error/completion annotations in simulation result regions. Do not relabel the production lifecycle state as a simulation state.
- Per-route action subsection: `Simulated actions`. Layer 3 H2: `Layer 3 — Simulated action outcomes`. Under that H2 show `These actions were not executed. Accepted means the video matched this simulation's routing rules, not that a playlist was changed.` Keep actual route/condition results (`Matched`, `Did not match`, `Skipped`) and recorded targets unchanged.
- Simulation action badge `not_attempted`: `Not attempted`, with its existing neutral treatment; never `Succeeded`, `Executed` or `Would succeed`. Empty Layer 3: `No simulated actions recorded for this video.` An empty action set is not proof of rejection.
- Simulation list empty/loading: use the state-aware copy in “Empty Simulation — Recorded Selection” below. Filter-empty copy and Clear outcome filter remain unchanged. Fetch-error wording: `Failed to load hypothetical video outcomes. Check your connection and try again.` Do not show production results as a fallback after a failed simulation fetch.
- Production per-route action subsection becomes neutral `Actions`, replacing `Actions executed` even when rows are failed/deferred/not attempted. Keep H2 `Layer 3 — Action outcomes`, real row badges (`Succeeded`, `Failed`, `Deferred`, `Not attempted`), and production outcome/lifecycle explanation. Production empty Layer 3: `No action outcomes recorded for this video.` No fabricated prior success or already-applied evidence is introduced.

##### Empty Simulation — Recorded Selection

**Bounded amendment (2026-11-06):** A person opens a completed dry-run with Selected 0 and Evaluated videos 0. Instead of being told to expect future results, they see that this run selected no videos and why dry-run inputs differ from discovery. This corrects the accepted empty copy, not the selector. Existing US-011 and the simulation-inspection scenario remain the design basis; no new workflow or story is added.

**Placement and precedence:** Use the existing empty region under H2 `Hypothetical video outcomes`. A request in progress is not an empty result. Before the first successful outcome response, show `Loading hypothetical video outcomes…`, not any recorded-empty claim. A fetch failure retains the existing error and retry control. After a successful fetch, an active filter with no matches retains `No videos match the current filters.` and Clear outcome filter. Only an unfiltered, successfully loaded empty list uses the following copy:

| Stored run state and counts | Exact empty-region copy |
|---|---|
| `dry_run`, `completed`, selected explicitly 0 and evaluated explicitly 0 | `No videos were selected for this dry run.` Follow with a separate paragraph: `Dry runs evaluate stored videos awaiting their first evaluation. They do not discover new videos or re-evaluate previously processed videos.` |
| Any simulation, `running` | `No hypothetical video outcomes recorded yet.` Keep the existing run-progress indication. |
| Any simulation, `completed`, except the explicit dry-run zero/zero case above | `No hypothetical video outcomes were recorded for this run.` |
| Any simulation, `failed` or `interrupted`, regardless of counts | `No hypothetical video outcomes were recorded for this run.` Keep the stored failure/interruption status and its existing explanation visible. |

Missing/null selected counts remain `Unknown`, never zero. Missing evaluated counts do not qualify for the zero/zero explanation. Selected/evaluated counts alone do not override a loading, failed-fetch, filtered, or nonempty outcome list. An empty terminal result does not imply playlist success, rejection, absence of stored videos, or a current pending-work count. The explanation describes the existing dry-run selector, not why deployed run 13 selected zero; its deployment and persisted evidence have not been verified by this amendment. It does not apply the dry-run selector description to backtest or replay.

**Recovery and scope:** Add no pending-workload link: the inspected UI has no supported destination for the not-yet-evaluated input set. Errors/deferred work is not that set. Do not offer a production run as a harmless way to populate a dry run; it can change playlists. Keep existing navigation, simulation disclaimer, filters and retry behavior. No discovery, reprocessing, date-selection, coordinator, or two-job behavior is introduced.

**Accessibility and checks:** Keep both explanatory paragraphs as persistent text in reading order within the outcome region, not a tooltip or color-only signal (WCAG 2.2 AA, SC 1.3.1 and 1.4.1). Loading-to-empty updates leave keyboard focus in place; a changed result status is announced politely once, not repeatedly for unchanged polling responses (SC 4.1.3). Check the explicit completed dry-run zero/zero case, null/missing selected and evaluated counts, nonzero selected with an empty list, running zero/zero, failed/interrupted zero/zero, initial loading, fetch failure, filter-empty, and nonempty rows. Confirm only the explicit completed dry-run zero/zero case gets the selector explanation; all terminal cases omit `yet`. Check reading order and status announcements with a screen reader and reflow at 320 CSS pixels without clipping (SC 1.4.10). These are downstream checks, not claims of implemented or deployed conformance.

**Badge interpretation/precedence:** Establish the requested run's mode before interpreting a result. In simulation mode, use the requested-run `final_outcome` projection exclusively; the unchanged production `processing_state` never overrides it. Use the same mapping on list and detail:

| Simulation `final_outcome` | Visible badge text | Semantic treatment |
|---|---|---|
| `accepted` | `Hypothetical accepted` | Accepted category; no execution-success claim |
| `rejected` | `Hypothetical rejected` | Rejected category |
| `excluded` | `Hypothetical excluded` | Excluded category |
| `deferred` | `Deferred` | Deferred evaluation, not predicted action execution |
| `failed` | `Failed` | Failed evaluation, not predicted action failure |
| `not_yet_evaluated` | `Not evaluated` | No result; never infer rejection |

For Deferred/Failed in simulations, visible adjacent explanation is respectively `Simulation evaluation was deferred.` / `Simulation evaluation failed.` Missing/unsupported result uses neutral `Outcome unavailable`, not an inferred result from production state. This is a presentation fallback, not a new stored outcome. No new precedence for mixed deferred/failed/matched evidence is defined here. For actual `manual`/`scheduled` runs preserve genuine lifecycle interpretation: discovered/evaluating → `In progress`; not_yet_evaluated → `Not evaluated`; evaluated with rejected/excluded → that category; completed → `Accepted`; acted/failed → `Failed`; deferred → `Deferred`. Do not globally replace production badge derivation with simulation precedence.

**Mode loading and continuity:** Decision Detail needs the requested run's type as well as its existing detail response; the existing run lookup supplies that context. A direct URL/new tab/refresh cannot depend on navigation-only state. Until mode is known, show `Loading run context…` without an interpreted badge or production action title. If mode lookup fails or is unsupported, show `Run context unavailable. Results cannot be interpreted safely.` with `Retry` and the existing `Back to run` link; retain the run/video location. Retry reloads context and only then presents mode-qualified results. Do not momentarily render a production badge while context loads. Normal route navigation moves focus to the new H1; background context/result refresh does not steal focus. Back returns to the same run; no simulation-to-production promotion or mutation control is added.

**Capability boundary:** Source inspection confirms run mode is available from the run lookup and requested-run hypothetical `final_outcome` is available from list/detail. The inspected detail response has no separately identified original production outcome. US-011/US-012's comparison with earlier same-YuHaul production outcomes remains an accepted future-target capability limitation for the API/architecture owner, explicitly deferred by the user from first-use launch rather than a launch blocker. Do not derive an “original outcome” from `processing_state`, use the projected hypothetical outcome twice, substitute old cron application data, or claim current production state is the actual outcome of an earlier run. This correction does not certify the broader comparison journey as complete; the simulation differentiation and inspection behavior in this section remain in scope.

**Accessibility and representative browser checks (verification targets, not observed passes):**

- WCAG 2.2 AA, SC 1.3.1/1.3.2/1.4.1: mode explanation precedes result regions in visual and screen-reader reading order; headings and table/mobile labels express hypothetical meaning in text. Badge text is exposed once, decorative icons are hidden, and simulation meaning never depends on color. Verify accessible tree/reading order on both views, including direct video links.
- SC 1.4.3/1.4.10: badge/explanation text meets 4.5:1 contrast and wraps without clipping at 320 CSS px and 200% zoom. Retain the complete “Hypothetical” qualifier and action target; never truncate away the distinction.
- SC 2.1.1/2.4.3/2.4.7/2.4.11/4.1.2: keyboard users can select the visibly labeled outcome filter, activate a video link and Retry/Back controls with visible unobscured focus; static mode/badges add no tab stops. Check H1 focus on navigation and stable focus on background refresh. A failed context load announces its visible error once; successful asynchronous context/result load announces the mode-qualified result region politely (SC 4.1.3), not on every polling tick.
- Real replay **and** backtest: fixture A has production `evaluated` and requested-run accepted; fixture B has production `completed` and requested-run rejected. Accepted filter shows only A with `Hypothetical accepted`; rejected filter shows only B with `Hypothetical rejected`. Detail and run counts agree; no production `Processing state` line appears as simulation evidence. Also check globally excluded result and no-evaluation fallback without manufacturing rejection.
- On both fixture details, see the persistent simulation explanation, `Simulated actions` / `Simulated action outcomes`, `Not attempted`, and correct target, with no `Actions executed` claim. Check an empty action set uses the exact simulation empty copy. Repeat a dry-run inspection for consistent mode isolation.
- Open a completed actual production run and a real deferred/failed/in-progress production run: genuine state/result labels remain unchanged, action subsections say `Actions`, recorded successes remain `Succeeded`, and no hypothetical qualifier or simulation disclaimer appears. Context failure/retry, direct links, refresh and browser Back never change run identity or expose unqualified stale results.

**Decision:** A bounded wording/precedence amendment wins over (a) globally preferring `final_outcome`, which erases genuine production lifecycle meaning, and (b) a disclaimer alone, which leaves false badges and execution headings intact. No new layout or brand direction is needed. Evidence: reviewer browser artifact `/tmp/opencode/followup-authority/browser-result.json` plus inspected RunDetailView, DecisionDetailView, outcome helper and response types. This design amendment is not a browser conformance approval; production code and post-fix browser/assistive-technology verification remain outstanding.

**View: Analytics Dashboard** — Temporal trend visualization; user job: identify patterns and changes over time.
- **Regions:**
  - Time range selector region: preset ranges (7d, 30d, 90d, all) + custom date picker
  - Trend charts region: acceptance rate, videos processed, route match rates over time
  - Quota ledger region: API consumption breakdown
  - Summary statistics region: lifetime and period aggregates
- **Components:** Date range picker, line chart, bar chart, statistic card, sparkline, quota ledger table

**View: Channel Analysis** — Per-channel performance and advisories; user job: evaluate which channels produce valuable content and act on system suggestions.
- **Regions:**
  - Channel list region: sortable table of channels with usefulness metrics and advisory badges
  - Channel detail region (on selection): deep-dive into one channel
  - Duplicate provenance region (within channel detail): discovery credit vs. relevance credit breakdown
  - Retention signal provenance region (within channel detail): user-driven vs. YuHaul-routed membership breakdown
  - Advisory region (within channel detail): system-generated suggestions with evidence
- **Components:** Channel row (name, discovered count, routed count, retained count, usefulness rate, time since last useful discovery, advisory badge), channel detail (video list, usefulness chart, route match breakdown, duplicate provenance breakdown, retention signal provenance breakdown, advisory card with take-action/dismiss/ignore actions)

**View: Configuration History** — Route and configuration version history; user job: understand what changed, when, and which configuration governed each historical run.
- **Regions:**
  - History list region: chronological list of configuration changes
  - Change detail region (on selection): full effective route configuration at that point in time, linked runs
- **Components:** History entry (timestamp, change summary, configuration version number, linked run count), change detail panel (full route configuration snapshot, list of runs that executed under this configuration, diff view comparing to previous version)
- **Diff view:** Side-by-side or inline comparison showing what changed between two configuration versions (added/removed/modified routes, conditions, exclusions, actions). Highlight additions in green, removals in red, modifications in amber.
- **Linked runs:** Each configuration version shows which runs executed under it. Clicking a run navigates to Run Detail.

**View: Error Queue and Deferred Work** — Triage processing failures and review deferred items; user job: resolve failures and understand deferred work.
- **Regions:**
  - Tab region: "Errors" tab, "Deferred" tab
  - Error list region: failed items with reasons
  - Error detail region (on selection): full error context
  - Deferred list region: quota-deferred items with next-eligibility indicator
  - Deferred detail region (on selection): deferral context and manual retry option
- **Components:** Tab selector, error row (video title, channel, error type, timestamp, retry button, remove button), error detail panel, deferred row (video title, channel, deferral reason, next-eligibility indicator, manual retry button, “Dismiss” button), deferred detail panel

**View: Backtest** — Compare proposed routing against historical observations, or replay/reprocess historical videos; user job: evaluate a configuration change before committing, or re-evaluate history with different settings.
**Status qualification:** Comparison-specific regions/components below (actual-versus-proposed video comparison and comparison-derived new/lost/net cards) are retained future-target design, deferred from first-use launch with US-011/US-012. Requested-run hypothetical results and non-production labeling remain governed by Simulation Inspection above; the view description is not evidence that earlier production comparison is implemented.

- **Regions:**
  - Mode selector region: "Hypothetical Backtest" tab, "Replay/Reprocess" tab
  - Configuration editor region (hypothetical): proposed route changes (draft, not saved)
  - Configuration selector region (replay): select current configuration or a historical version
  - Date range selector region: which historical period to test or replay
  - Results region: comparison view
- **Components:** Mode tab selector, route editor (same as Route Editor but in draft mode for hypothetical mode), configuration version selector (for replay mode), date picker, comparison cards (new catches, lost catches, net change), replay results list (reprocessed outcomes), video comparison list

**View: Settings** — System configuration; user job: configure scheduling, quota limits, account, data management, and retention playlists.
- **Regions:**
  - Schedule region: cron-like schedule configuration
  - Quota region: API quota limits, current usage, and quota ledger
  - Account region: usable access, renewal and recovery status; connect/reconnect when appropriate (see Account Access — Automatic Renewal and Safe Recovery)
  - Retention playlists region: designate which playlists represent the user's actual keep signal
  - Duplicate detection region: fuzzy matching threshold configuration
  - Data region: historical corpus import, data management
  - Danger zone region: disconnect account
- **Components:** Schedule form, quota display with ledger breakdown, account status card, retention playlist selector (multi-select from user's YouTube playlists), threshold slider with numeric display and explanation, import controls, danger action buttons

#### Account Access — Automatic Renewal and Safe Recovery

**Accepted reconciliation (2026-11-07):** [BR-034](/docs/requirements/yuhaul/br-034-automatic-youtube-token-refresh.md) and [OC-006](/docs/requirements/yuhaul/oc-006-refresh-authorization-and-call-safety.md) supersede expiry-only reconnection in the opening journey, US-001 and SC-007. This is a bounded amendment to the existing account card and Overview summary, not a new visual direction or a claim of implemented renewal.

**Walkthrough:** A morning run renews expired access without asking the administrator to sign in again. If renewal is temporarily unavailable, the account card explains that condition and the run retains its actual failure/work evidence. Only missing or confirmed rejected renewal authorization calls for Reconnect. Returning from OAuth restores access when confirmed; it does not restart a failed run or resolve an uncertain playlist action.

**Presentation and state transitions:** Use SC-007's state names, safe explanations and recovery branches consistently in Settings and Overview. Account identity, when available, is context, never proof of usable access. Expired/rejected access with only an unvalidated refresh credential is not Connected. Pending renewal shows pending only while actually unresolved; a failure leaves that state for unavailable or reconnect-required. Confirmed rejection supersedes stale Connected information, and later successful recovery supersedes earlier failure. User disconnect/account replacement supersedes late old-account results.

An initial status read shows "Loading account status"; a failed status read shows "Could not load account status" with "Retry account status". That button rereads status, not a promised provider renewal, run restart or action replay. During the read, acknowledge "Loading account status" and prevent duplicate reads; on completion show the returned state or keep the read failure. Previously shown identity may remain labeled as last known, never as current access confirmation. Do not turn unknown/read failure into revoked consent, Disconnected or Connected. First-time/no-account state retains "Connect YouTube Account"; intentional disconnect remains distinct from renewal failure. Do not silently open OAuth. Reconnect is a deliberate existing flow, with an opening acknowledgment and duplicate-launch prevention until it returns or fails; cancellation/failure restores that action with a safe explanation and preserves work.

**Safety and scope:** Render only application-owned safe reasons. No tokens, refresh credentials, raw provider responses or raw error chains appear in text, tooltips, links or accessibility names. No new token-expiry timer, proactive status-triggered provider call, refresh button, retry budget, simulation provider call or historical run replay is introduced. Account recovery is independent of operation completion; retain actual run/action evidence and SC-007's uncertainty warning wherever the affected work is inspected. Architecture owns status/API realization, not a UI inference from expiry or credential presence.

**Accessibility and responsive checks:** Target WCAG 2.2 AA. Under SC 1.4.1/1.3.1, status, reason and recovery action appear together in text, not color or avatar alone. Under SC 4.1.3, announce meaningful pending/result changes politely once without repeating unchanged polling results or stealing focus. Under SC 2.1.1/2.4.3/2.4.7/2.4.11, keyboard users can reach Connect, Reconnect and Retry account status in reading order with visible unobscured focus; inline status updates preserve focus, OAuth return restores the Account context with focus on its heading, and a cancelled/failed launch restores focus to its trigger. Under SC 1.4.3/1.4.4/1.4.10, text meets 4.5:1 contrast and status/recovery copy wraps at 320 CSS px and 200% text resize. Verify the rendered interface, keyboard return path and screen-reader announcements; this documentation change does not establish implementation conformance.

**Decision and outstanding evidence:** Keep the existing account surface and distinguish renewal pending/unavailable from reconnect-required. A binary Connected/Disconnected interpretation is rejected because it invents revocation or usability; forcing OAuth on every expiry contradicts BR-034. No new story IDs or separate files are needed: US-001/SC-007 now cover the accepted capability. The supplied expiry-only account-status and Run20 401 observations identify a reconciliation concern, not proof of refresh-grant rejection, restart token loss or Run19's cause. Architecture/implementation owners still owe supported status facts and the assembled production-path checks in BR-034/OC-006, including temporary/save failure, restart, overlapping renewal, disconnect/replacement and uncertain mutation. No user-owned ambiguity blocks this bounded design.

#### Fixed Duplicate Continuation — Settings and Recorded History

**Accepted amendment (2026-10-25):** This bounded clarification follows the user's “Continue, yes”, [BR-008 AC9–AC11](/docs/requirements/yuhaul/br-008-exact-and-fuzzy-duplicate-detection.md), [ADR-017](/docs/architecture/duplicate-continuation/decisions/adr-017-fixed-duplicate-continuation.md), and [ARS-005](/docs/architecture/duplicate-continuation/specifications/ars-005-duplicate-continuation-compatibility-results.md). It is intended design, not a claim that the UI or evaluator has been updated.

**Walkthrough:** An administrator opens Settings to tune fuzzy matching. The existing threshold remains editable. Nearby text explains that a duplicate match does not stop eligible routing. When the administrator inspects an older version containing `skip`, History shows that recorded value unchanged and explains that today's evaluation continues regardless. Neither screen offers a handling choice; neither a match nor a historical string proves that a playlist action occurred.

**Settings — exact copy and placement:** In the existing **Duplicate detection** card, retain the threshold label, slider, numeric value, and the existing exact/fuzzy/self-match explanation verbatim. Immediately after that explanation, add one ordinary paragraph:

> Duplicate matches do not stop routing. Videos continue through eligible routes and playlist actions; exclusions, route conditions and order, final-route rules, quota and failure handling, and completed-action checks still apply.

This is fixed explanatory text, not a field, badge requiring interaction, disabled selector, toggle, or save action. Do not say “default”, offer `skip`/`flag`, or echo a legacy settings value as effective policy. Current Settings readback is `continue` under ARS-005. Existing loading/error/retry behavior and threshold interaction remain unchanged by this amendment. Keep Data/import copy and counts unchanged: skipping an already-present video ID during import is not suppressing routing after a distinct-video duplicate match. No new imported-reference or discovery-credit wording is introduced.

**History version detail — exact copy and placement:** In **Summary**, immediately after **Duplicate threshold**, display a read-only text row:

> Duplicate handling (recorded legacy value): `{recorded value}`

Show the returned recorded string without translating `skip`, `flag`, or arbitrary strings to `continue`, including in a restored historical copy. For an empty string or absent field represented as empty by the API, show `Empty or not recorded` rather than a blank value or an invented policy. This display fallback does not change stored data or distinguish absence from empty when the API cannot. For nonempty strings, preserve characters (including whitespace); render as text, not markup.

Directly below this row, show:

> This is a recorded configuration value, not a current handling choice. Current evaluation continues after duplicate matches regardless of this value, including replay and backtest. It does not establish what actions occurred in earlier runs; inspect their recorded outcomes and actions.

Use this label and explanation for every displayed value, including `continue`: “legacy” identifies the retired field, not a claim about the age of a version. Preserve existing version descriptions, IDs, dates, thresholds, route snapshots, and past outcome/action evidence. Do not rewrite a historical description mentioning skip. No new history-list column is needed.

**History diff — exact copy and placement:** Within **Settings changed**, special-case only the `duplicate_behavior` row's presentation. Replace its bare technical key with **Duplicate handling (recorded legacy values)**. Display both unmodified values with explicit **Previous:** and **This version:** labels; apply the same empty/absent fallback above. Beneath that row, show the same explanatory paragraph used in Summary. Show this row only when the returned diff contains it; do not fabricate a change when recorded values are equal. Other diff rows and the no-changes state remain unchanged. A `skip → continue` comparison describes recorded values, not a proven transition in past execution behavior.

**Accessibility and responsive presentation:** These additions are selectable, wrapping text in the existing reading order, not tooltips or live alerts. Under WCAG 2.2 AA SC 1.3.1/1.3.2, each historical label, value, and explanation is read together in that order; verify by DOM/accessibility-tree inspection. Under SC 1.4.1, previous/this-version meaning is explicit in text, never only red/green or an arrow; verify without color. Under SC 1.4.3/1.4.4/1.4.10, explanation text meets 4.5:1 contrast and remains available at 200% text resize and 320 CSS px width, with long recorded strings wrapping; verify in the browser. Static text adds no tab stops or focus movement. Existing keyboard threshold and History navigation remain intact. No implementation accessibility approval is claimed here.

**Design decision:** Inline explanation is the smallest correction to the accepted threshold-only Settings design and generic History diff. A disabled selector is rejected because it implies another available mode; hiding or normalizing historical strings is rejected because it conceals evidence. No new flow or separate story/scenario artifact is needed: this refines US-007 (threshold tuning) and the existing configuration-history inspection journey without adding capability.

**Downstream checks (not executed by this documentation change):**

1. Settings with active `continue` shows the exact paragraph and editable threshold, with no handling control or skip/default claim. Threshold changes and current loading/error/retry paths still work. Data/import already-present/skipped counts and copy are unchanged.
2. Open versions containing `skip`, `flag`, `continue`, an arbitrary string, empty, and absent behavior. Confirm the row, fallback, explanation, unchanged threshold/routes/descriptions, and unchanged underlying recorded history. A copied old version still displays its recorded value.
3. Diff `skip → continue`, `flag → skip`, and empty → `continue`. Confirm both recorded values, explicit previous/this-version labels, and explanation; equal-value comparisons add no handling row. Unrelated settings diffs remain unchanged.
4. Keyboard and screen-reader inspection finds no fake handling control; reading order includes the explanation. Check contrast, no-color interpretation, text resize, narrow-width wrapping, and long-string safety against the accessibility conditions above.
5. The assembled evaluator/API continuation and historical immutability proofs belong to ARS-005; UI copy or fixtures alone do not establish them. Do not claim simulation actions succeeded or past actions occurred from a duplicate flag or handling string.

**Known boundary:** Source inspection found Settings already has no handling selector; its missing clarification and History's bare `duplicate_behavior` diff are the bounded corrections. History Summary currently omits this value, but the snapshot supplies it; no new API capability is needed. The existing History “Restore” action/toast promises more than ARS-005's copy-only implementation establishes. Complete restore semantics remain with the requirements/implementation owners; this amendment does not approve active restoration or redesign that separate journey. No user-owned ambiguity blocks the copy and labeling above.

#### Three-Layer Video Outcome Model

A video in YuHaul has three independent outcome layers. The summary badge on video outcome rows shows the processing outcome; the full detail is available in Decision Detail.

**Layer 1: Video/Run Processing State** — the overall lifecycle stage for this video in this run.
- `discovered`: video was discovered from a subscription but routing has not begun
- `evaluating`: routing is in progress (conditions being evaluated)
- `evaluated`: all applicable routes have been evaluated; actions pending
- `acted`: actions have been executed (playlist insertions attempted)
- `completed`: all actions succeeded; video processing is finished
- `deferred`: processing stopped due to quota exhaustion (work is saved for later)
- `not-yet-evaluated`: routing for this video has not begun or has not completed because execution did not reach it (e.g., processing was interrupted before reaching this video, or quota exhaustion prevented evaluation). This does NOT include videos that were evaluated but skipped subsequent routes because an earlier final route matched.

**Layer 2: Per-Route Decision State** — for each route evaluated against this video.
- `accepted`: route conditions were satisfied and route matched
- `rejected`: route conditions were not satisfied
- `skipped`: route was not evaluated — either because a prior final route already matched this video, or because the video was excluded from this route by an exclusion rule, or because the route is disabled. Note: when a final route matches, the video IS evaluated at Layer 1; subsequent routes are skipped at Layer 2 only.

**Layer 3: Per-Action Execution State** — for each action triggered by a matched route.
- `succeeded`: action completed successfully (e.g., playlist insertion confirmed)
- `failed`: action attempted but failed (e.g., YouTube API error during playlist insertion)
- `deferred`: action was not reached due to quota exhaustion or upstream failure

**Summary badge rules:** The processing outcome badge is derived from the three layers:
- If processing state is `completed` → badge shows "Accepted" (all actions succeeded)
- If processing state is `acted` and any action failed → badge shows "Failed"
- If processing state is `evaluated` and no route matched → badge shows "Rejected"
- If processing state is `deferred` → badge shows "Deferred"
- If processing state is `not-yet-evaluated` → badge shows "Not evaluated"
- If processing state is `discovered` or `evaluating` → badge shows "In progress" (transient)

#### Quota Ledger

The quota ledger provides visibility into API consumption, fulfilling AC 18.

**Displayed in Analytics Dashboard and Run Detail:**

| Metric | Description |
|--------|-------------|
| Metadata reads | YouTube Data API quota units consumed reading video/channel metadata |
| Playlist additions | Units consumed adding videos to playlists |
| Discovery calls | Units consumed discovering new videos from subscriptions |
| Total this run | Sum of all quota units for the current/selected run |
| Total today | Sum of all quota units consumed today (resets per provider-documented schedule) |
| Estimated remaining | Quota remaining in current bucket (if known) or "Unknown" |
| Reset/bucket info | When the quota bucket resets per provider documentation, bucket size if known |
| Per-run cost | In Run Detail: quota cost breakdown for that specific run |

**Quota state on Overview:** Shows a compact summary: "X/Y units used today (Z%)" with a visual bar. If quota is exhausted, the bar turns the deferred color (amber) and shows "Quota exhausted — work deferred."

#### Advisory System

Advisories are system-generated suggestions based on channel and route performance data. The system advises; the user decides. No advisory auto-acts.

**Advisory types (v1):**
- **Low usefulness:** Channel has many discoveries but few retentions in designated rotation playlists. The advisory references retention in playlists the user has explicitly designated as retention/rotation targets — not just any playlist membership. Suggested action: consider unsubscribing.
- **Declining yield:** Channel's acceptance rate has dropped significantly over time. Suggested action: review route conditions for this channel's content.
- **High duplicate rate:** Channel's discoveries are mostly duplicates of content from other channels. Suggested action: evaluate whether this channel adds unique value.

**Advisory display:** Advisory badges appear on channel rows in Channel Analysis. Clicking a channel shows the advisory detail with supporting evidence (metrics, trend). User can take action (opens the specific action described, e.g., YouTube unsubscribe page), dismiss (acknowledge, don't act), or ignore (hide for 30 days).

**Advisory authority model:** Advisories never auto-unsubscribe or auto-modify routes. Taking action on an advisory opens the specific action described (e.g., "open YouTube to unsubscribe" or "highlight the route condition for review"). The user retains full authority over all policy decisions (per Anchor AC 31).

#### States and Interactions

**Route row states:**
- default: name, description snippet, enabled toggle, drag handle
- hover: subtle background shift, action buttons (edit, delete) become visible
- dragging: reduced opacity, drop indicator line appears between rows
- disabled: muted text, toggle shows "off" position, route is skipped during evaluation

**Route row interaction — click:**
- System: navigates to Route Editor for this route
- Focus: moves to route name field in editor

**Route row interaction — drag handle:**
- System: initiates drag reordering, shows drop indicator
- On drop: updates evaluation order, saves automatically
- On Escape: cancels drag, returns to original position

**Route row interaction — Move Up/Down buttons:**
- System: moves route one position up or down in evaluation order, saves automatically
- Keyboard: accessible via Tab, activated with Enter/Space
- Required alternative to drag per WCAG 2.2 SC 2.5.7

**Run row states:**
- default: timestamp, type badge, duration, outcome summary, quota cost
- hover: background shift, clickable
- expanded (in detail view): shows video outcome list inline

**Video outcome row states:**
- default: title, channel, outcome badge (color + icon + text)
- hover: background shift, clickable to decision detail
- completed (accepted): green badge with check icon
- evaluated (rejected): red badge with X icon
- deferred: yellow badge with clock icon
- acted (failed): red badge with alert icon
- not-yet-evaluated: gray badge with dash icon (only for videos where execution did not reach routing)
- discovered/evaluating: blue badge with spinner icon (transient states)

**Video outcome row interaction — click:**
- System: navigates to Decision Detail view
- Focus: moves to processing outcome badge in decision detail

**Condition row states:**
- default: field selector, operator selector, value input, remove button
- active (being edited): highlighted border, focus on current field
- error: red border on invalid field, error message below

**Condition row interaction — remove button click:**
- System: removes condition from list, re-renders remaining conditions
- No confirmation needed (undo available via "Discard changes" on the route editor)

**Exclusion list entry states:**
- default: entry text with remove (×) button
- hover: subtle background shift, remove button becomes more prominent
- error: red border if entry is invalid (e.g., empty string, malformed duration)

**Exclusion list interaction — add entry:**
- System: input field with "Add" button; on submit, entry validated and added to list
- Validation: channel name non-empty, title non-empty, word/phrase non-empty, duration values numeric and min ≤ max
- On validation error: inline error message below input, entry not added

**Exclusion list interaction — remove entry click:**
- System: removes entry from list, re-renders remaining entries
- No confirmation needed (undo available via "Discard changes" on the route editor)

**Duration limit interaction:**
- System: two optional number inputs (min seconds, max seconds)
- Validation: values must be numeric ≥ 0; if both set, min ≤ max
- Empty fields mean no limit on that side; both empty means no duration exclusion

**Save button states:**
- default: enabled, primary style
- loading: spinner, disabled, label changes to "Saving..."
- success: checkmark icon, label "Saved", transitions back to default after 2s
- error: re-enables, error toast appears

**Error queue item interaction — retry click:**
- System: transitions item to "Retrying" state (spinner replaces retry button)
- On success: item dismissed from active queue, toast "Video reprocessed successfully"
- On failure: item stays, error message updated, retry button re-enables

**Error queue item interaction — remove click:**
- System: shows confirmation dialog "Dismiss this item from the error queue? The historical record will be preserved."
  - On confirm: item dismissed from active queue, toast "Item dismissed from error queue"
  - On cancel: dialog closes, item unchanged

**Deferred item interaction — manual retry:**
- System: checks if quota is available
  - If quota available: initiates retry, transitions to "Retrying" state
  - If quota unavailable: shows toast "Quota still exhausted — item will be eligible for the next scheduled or manual run"

**Deferred item interaction — dismiss (button label “Dismiss”, renamed from “Discard” on 2026-11-10 so it is not confused with “Discard run {id}”; its accessible name is “Dismiss deferred item: {video title}”, falling back to the video ID):**
- System: shows confirmation dialog "Dismiss this deferred item? It will not be retried, but the historical record will be preserved."
  - On confirm: item dismissed from active queue, toast "Deferred item dismissed"
  - On cancel: dialog closes, item unchanged

**Advisory interaction — take action:**
- System: opens the specific action described in the advisory (e.g., opens YouTube unsubscribe page, highlights route condition for review)
- Logs the action for reference

**Advisory interaction — dismiss:**
- System: marks advisory as acknowledged, removes badge from channel row
- Advisory does not reappear for this specific finding

**Advisory interaction — ignore:**
- System: hides advisory badge for this channel for 30 days
- Advisory reappears after 30 days if conditions persist

**Manual run trigger interaction:**
- System: "Run" button in header always visible
- On click: dropdown with "Run Now" and "Dry Run" options
- "Run Now" on click: confirmation dialog "Start a processing run now?" with the added sentence “If an earlier Run Now run stopped at its time limit, this continues the oldest such run instead of starting a new one.”; on confirm, acknowledge the pending request, then navigate the acknowledged run ID to Run Detail. Start, resume and refusal feedback follow “Run Now — Resume After a Time Limit”, not a toast. Follow “Run Now — Recorded Progress and Closure”: saved reads supply phase/status and terminal confirmation; SSE is optional faster feedback, not completion authority.
- "Dry Run" on click: confirmation dialog (see SC-003), on confirm: starts dry-run, subscribes to SSE stream, shows live progress, navigates to dry-run results

**Route delete button states:**
- default: destructive style (red text/border), enabled
- hover: darker red background
- disabled: gray, cursor not-allowed (during delete API call)

**Route delete confirmation dialog:**
- Modal opens with focus on "Cancel" button
- "Delete" button is destructive-styled (red)
- Escape key closes dialog, returns focus to Delete button
- Clicking outside dialog closes it (same as Cancel)

**Route list after delete:**
- Deleted route removed from list
- Remaining routes maintain relative order
- Focus moves to the route row that was below the deleted route (or the last row if deleted route was last)
- Toast confirms deletion

**Condition validation error states:**
- Condition row: red border on invalid field
- Error message below the row in caption style (red, 12px)
- First error field receives focus on save attempt

**Action validation error states:**
- Action row: red border on invalid parameter
- Error message below the row in caption style (red, 12px)
- Playlist selector: red border if no playlist selected when save attempted

**Empty states:**
- Route list empty: "No routes yet. Create your first route to start routing videos." + "New Route" button
- Run list empty: "No runs yet. Start a processing run to see results here." + "Run Now" button
- Run list filtered empty: "No runs match the current filters." (shown only when filters are active and the fetch succeeded)
- Error queue empty: "No errors. All processing is running smoothly." + checkmark icon
- Deferred queue empty: "No deferred work. All pending items have been processed." + checkmark icon
- Analytics empty: "Not enough data yet. Analytics will appear after a few processing runs."
- Channel advisories empty: "No advisories. All channels are performing within expected ranges."
- Configuration history empty: "No configuration changes yet. History will appear as you create and modify routes."

#### Transitions

**Route list → Route Editor:** Content slides in from right (push navigation). Duration: 200ms. Reduced-motion: instant swap, no animation.

**Run list → Run Detail:** Content slides in from right. Duration: 200ms. Reduced-motion: instant swap.

**Run Detail → Decision Detail:** Content slides in from right (deeper navigation). Duration: 200ms. Reduced-motion: instant swap.

**Save button loading → success:** Spinner replaced by checkmark with scale-up. Duration: 200ms. Reduced-motion: instant replacement.

**Error queue item removal:** Item fades out and collapses height. Duration: 300ms. Reduced-motion: instant removal.

**Toast notification appearance:** Slides up from bottom-right, fades in. Duration: 200ms. Reduced-motion: instant appearance.

**Toast notification dismissal:** Fades out and slides down. Duration: 150ms. Reduced-motion: instant disappearance.

#### Focus Behavior

**Route list view load:** Focus moves to first route row in the list (or "New Route" button if list is empty).

**Route Editor load:** Focus moves to route name field.

**Run list view load:** Focus moves to first run row in the list.

**Run Detail view load:** Focus moves to run summary region heading.

**Decision Detail view load:** Focus moves to processing outcome badge.

**SPA route change:** Focus moves to the new view's heading (h1) or primary content region. Screen reader announces page change via aria-live region.

**Modal/dialog open:** Focus moves to first focusable element inside modal (typically close button or primary action).

**Modal/dialog close:** Focus returns to the element that triggered the modal.

#### Layout Constraint

**Sidebar navigation:** Must be the first landmark on desktop. On mobile, it collapses to a bottom navigation bar with 5 items (Overview, Routes, Runs, Analytics, Errors) plus a "More" overflow for Settings and History.

**Main content area:** Must be scrollable independently of sidebar on desktop. On mobile, full-width.

**Route editor:** Conditions and actions are vertically stacked in a single-column layout at all viewport widths. This eliminates the contradiction between vertical stacking (narrow viewports) and two-column (desktop) — a single layout works everywhere.

**Video outcome list in Run Detail:** On mobile, each video row converts to a card layout with stacked fields (title, channel, outcome badge, route match summary). On desktop, full table columns are visible.

**Decision evidence trail:** Must be vertically sequential (top to bottom = evaluation order). Never side-by-side — this is a sequential evaluation process.

**Charts in Analytics:** Must have minimum width of 300px to remain readable. On viewports narrower than 300px, charts stack vertically and allow horizontal scroll.

**Form labels:** Must appear above inputs (not beside) to support text reflow at narrow viewports.

**Primary action buttons (Save, Run, etc.):** Must be the last focusable element in their region (keyboard users reach them after configuring everything above).

#### Responsive Behavior

**Viewport ≥ 1024px (desktop):**
- Sidebar navigation: persistent, collapsible, 240px wide
- Content area: fills remaining width
- Tables: full column visibility
- Charts: full width, side-by-side when appropriate
- Route editor: single-column layout (vertically stacked conditions and actions)

**Viewport 768px–1023px (tablet):**
- Sidebar navigation: persistent, collapsible, 200px wide
- Content area: fills remaining width
- Tables: may reduce to essential columns
- Charts: full width, stacked vertically
- Route editor: single-column layout

**Viewport < 768px (mobile):**
- Sidebar navigation: collapses to bottom navigation bar (5 primary items + "More" overflow)
- Content area: full width, single column
- Tables: convert to card layout (each row becomes a card with stacked fields)
- Charts: full width, stacked vertically, allow horizontal scroll if minimum width not met
- Route editor: single-column, full width
- Action buttons: move to bottom of screen (sticky footer) for thumb reach

**Viewport < 320px (very small mobile):**
- Warning banner: "Screen is very small. Some features may be limited. Consider using landscape orientation."
- All above mobile behaviors apply
- Charts: hide axis labels, show data points only (tap to see values)

### Accessibility Requirements

**Component/interaction: All navigation items**
- Standard: WCAG 2.2 SC 2.4.1 Bypass Blocks (A), SC 4.1.2 Name, Role, Value (A)
- Requirement: Each navigation item must have an accessible name matching its visible label. The sidebar must be a `<nav>` landmark with `aria-label="Main navigation"`. A skip link ("Skip to main content") must be the first focusable element on the page.
- Verification: Automated: axe-core confirms landmark and label presence. Manual: screen reader announces navigation landmark and each item by name.

**Component/interaction: Route enable/disable toggle**
- Standard: WCAG 2.2 SC 4.1.2 Name, Role, Value (A), SC 1.4.1 Use of Color (A)
- Requirement: Toggle must use a semantic `<button>` or `<input type="checkbox">` with `aria-checked` state. Toggle state must be communicated by text ("Enabled"/"Disabled") AND icon (checkmark/X) AND color (green/red), not color alone. Accessible name: "Enable route [route name]" or "Disable route [route name]" depending on current state.
- Verification: Automated: axe-core confirms aria-checked or role="switch". Manual: screen reader announces "Enable route Music Discovery, switch, on/off".

**Component/interaction: Video outcome badges**
- Standard: WCAG 2.2 SC 1.4.1 Use of Color (A), SC 1.1.1 Non-text Content (A)
- Requirement: Each outcome badge must use: (1) color (green/red/yellow/gray/blue), (2) icon (check/X/clock/alert/dash/spinner), AND (3) text label ("Accepted"/"Rejected"/"Deferred"/"Failed"/"Not evaluated"/"In progress"). Never rely on color alone. Each badge must have an accessible name combining the outcome text.
- Verification: Manual: disable color (CSS filter or grayscale) and confirm outcomes remain distinguishable by icon and text.

**Component/interaction: Drag-to-reorder routes**
- Standard: WCAG 2.2 SC 2.5.7 Dragging Movements (AA)
- Requirement: Every drag interaction must have a single-pointer alternative. Provide explicit "Move up" and "Move down" buttons alongside the drag handle. The buttons must be keyboard-accessible and have accessible names "Move [route name] up" and "Move [route name] down."
- Verification: Manual: test with keyboard only — confirm route reordering works without drag.

**Component/interaction: Charts in Analytics**
- Standard: WCAG 2.2 SC 1.1.1 Non-text Content (A)
- Requirement: Every chart must have a text alternative that conveys the same data. Options: (1) an accessible data table adjacent to or behind the chart (toggle view), or (2) a `aria-describedby` element summarizing key trends. Chart data points must be accessible via keyboard (tab through data points with tooltip showing value).
- Verification: Manual: activate screen reader, navigate to chart area, confirm data is readable.

**Component/interaction: Route delete confirmation dialog**
- Standard: WCAG 2.2 SC 2.4.3 Focus Order (A), ARIA APG Dialog (Modal) pattern
- Requirement: On open, focus moves to "Cancel" button (safe default). Focus is trapped within dialog. Escape closes dialog and returns focus to Delete button. Dialog must have `role="alertdialog"` (confirmation) and `aria-modal="true"`. Destructive action ("Delete") must be clearly labeled and visually distinct from cancel.
- Verification: Manual: open dialog with keyboard, tab through buttons, confirm focus trap, close with Escape, confirm focus returns to Delete button.

**Component/interaction: Condition row field/operator selectors**
- Standard: WCAG 2.2 SC 4.1.2 Name, Role, Value (A), SC 3.3.2 Labels or Instructions (A)
- Requirement: Each selector must be a `<select>` or custom dropdown with `aria-label` (e.g., "Condition field", "Condition operator"). Value input must have an associated `<label>` or `aria-label`. Error messages must be linked via `aria-describedby`.
- Verification: Automated: axe-core confirms label association. Manual: screen reader announces each selector and input by name.

**Component/interaction: Playlist selector**
- Standard: WCAG 2.2 SC 4.1.2 Name, Role, Value (A), SC 3.3.2 Labels or Instructions (A)
- Requirement: Playlist selector must have `aria-label="Select target playlist"`. Loading state must use `aria-busy="true"` and announce "Loading playlists" via `aria-live="polite"`. Empty state must be announced via `aria-live="polite"`. Error state must use `role="alert"`.
- Verification: Manual: trigger loading state, confirm screen reader announces loading. Trigger error, confirm error is announced.

**Component/interaction: Route name input**
- Standard: WCAG 2.2 SC 3.3.1 Error Identification (A), SC 3.3.2 Labels or Instructions (A)
- Requirement: Name input must have visible label "Route name". Required state must be indicated (aria-required="true"). Validation errors must appear below input, linked via `aria-describedby`, and announced via `aria-live="polite"`. Character count must be visible: "X/100 characters".
- Verification: Manual: leave name empty, trigger save, confirm error message is announced and linked to input.

**Component/interaction: AND/OR toggle**
- Standard: WCAG 2.2 SC 4.1.2 Name, Role, Value (A)
- Requirement: Toggle must use `role="switch"` with `aria-checked` state. Accessible name: "Match all conditions" or "Match any conditions" depending on state. Toggle must be keyboard-accessible (Tab + Enter/Space).
- Verification: Manual: screen reader announces toggle state. Keyboard-only toggle works.

**Component/interaction: Modal dialogs (confirm, error detail, etc.)**
- Standard: ARIA APG Dialog (Modal) pattern
- Requirement: On modal open, focus moves to the modal's close button or primary action. Focus is trapped within the modal (Tab and Shift+Tab cycle). Escape key closes the modal. On close, focus returns to the trigger element. Modal must have `role="dialog"` and `aria-modal="true"`.
- Verification: Manual: open modal with keyboard, tab through all elements, confirm focus trap, close with Escape, confirm focus returns to trigger.

**Component/interaction: Date range picker**
- Standard: WCAG 2.2 SC 3.3.2 Labels or Instructions (A), SC 2.5.8 Target Size Minimum (AA)
- Requirement: Date inputs must have visible labels ("From date", "To date"). Touch targets must be minimum 44×44px. Date format must be stated: "Format: YYYY-MM-DD". Keyboard: Tab between date fields, Enter to confirm range.
- Verification: Measure touch targets in browser DevTools. Confirm labels are visible and associated.

**Component/interaction: Toast notifications**
- Standard: WCAG 2.2 SC 4.1.3 Status Messages (AA)
- Requirement: Toasts must use `role="status"` and `aria-live="polite"` for non-urgent messages (success toasts) and `role="alert"` and `aria-live="assertive"` for error toasts. Toasts must auto-dismiss after 5 seconds minimum (allow user to extend). Toast must have a visible close button.
- Verification: Automated: axe-core confirms role and aria-live. Manual: screen reader announces toast content without focus moving to it.

**Component/interaction: Recorded run progress indicator**
- Standard: WCAG 2.2 SC 4.1.3 Status Messages (AA)
- Requirement: Follow “Run Now — Recorded Progress and Closure”. Use `role="status"` and `aria-live="polite"` for concise state-transition announcements, not every poll/count/timestamp. Explicit phase and status text remains available without animation. Connection loss/recovery is announced once without moving focus; lost SSE alone is not run failure. No fabricated completion fraction.
- Verification: With a screen reader, start a delayed discovery run, lose SSE and then saved-status reads, and restore connectivity. Confirm phase, unknown counts, stale state, recovery, and saved terminal status are understandable without repeated announcements or stolen focus.

**Component/interaction: Focus indicators**
- Standard: WCAG 2.2 SC 2.4.7 Focus Visible (AA), SC 2.4.11 Focus Not Obscured (AA)
- Requirement: Every focusable element must have a visible focus indicator with minimum 3:1 contrast ratio against adjacent colors. Focus ring must be a 2px outline (not just color change). Sticky headers must not obscure focused elements when tabbing down the page.
- Verification: Manual: Tab through entire interface with keyboard, confirm focus indicator is visible on every element.

**Component/interaction: Table data (route list, run list, channel list)**
- Standard: WCAG 2.2 SC 1.3.1 Info and Relationships (A)
- Requirement: Tables must use semantic `<table>` elements with `<th>` for column headers. Sortable columns must indicate sort state (ascending/descending) via `aria-sort`. Row hover/selection must not be the only way to access row actions — action buttons must be visible or keyboard-reachable.
- Verification: Automated: axe-core confirms table structure. Manual: screen reader reads column headers and row data.

**Component/interaction: Advisory actions (take action/dismiss/ignore)**
- Standard: WCAG 2.2 SC 3.2.2 On Input (A), SC 4.1.3 Status Messages (AA)
- Requirement: Advisory action buttons must have clear accessible labels describing their effect ("Take action: open YouTube to unsubscribe", "Dismiss advisory", "Ignore advisory for 30 days"). Action results must be communicated via `role="status"` toast — not by modifying content the user is currently focused on.
- Verification: Manual: activate screen reader, navigate to advisory actions, confirm each button's accessible name describes its consequence. Confirm toast announces result after action.

### Visual Semantics

**Color meaning (semantic tokens, not hex values):**

| Token | Purpose | Usage |
|-------|---------|-------|
| `color.status.accepted` | Positive outcome | Green-tinted: completed (accepted) video badges, positive trend arrows, success states |
| `color.status.rejected` | Negative outcome | Red-tinted: evaluated (rejected) video badges, error states, destructive actions |
| `color.status.deferred` | Pending/waiting | Yellow/amber-tinted: deferred video badges, quota warnings, pending states |
| `color.status.failed` | System failure | Red-tinted (distinct from rejected via icon/label): acted (failed) video badges, error queue items |
| `color.status.not-evaluated` | Not processed | Gray-tinted: not-yet-evaluated video badges (videos where execution did not reach routing) |
| `color.status.in-progress` | Processing active | Blue-tinted: discovered/evaluating video badges (transient states during active processing) |
| `color.surface.primary` | Main background | Light or dark surface depending on system preference |
| `color.surface.elevated` | Cards, panels, modals | Slightly lighter/darker than primary to create depth hierarchy |
| `color.text.primary` | Headings, primary content | High contrast against surface |
| `color.text.secondary` | Metadata, labels, timestamps | Reduced contrast (4.5:1 minimum), muted |
| `color.interactive.primary` | Primary actions | Saturated color for primary buttons (Save, Run Now) |
| `color.interactive.destructive` | Destructive actions | Red-tinted for delete, remove, disconnect |
| `color.border.default` | Structural borders | Low contrast, 1px, used for table borders, input borders |
| `color.border.focus` | Focus indicators | High contrast (3:1 minimum), visible on keyboard focus |
| `color.advisory.suggestion` | Advisory badge | Blue-tinted: advisory indicators on channel rows |

**Typography hierarchy:**

| Level | Use | Weight | Size |
|-------|-----|--------|------|
| Display | Page titles (route name, channel name) | Bold | 24–28px |
| Heading | Section headings (Conditions, Actions, Evidence) | Semibold | 18–20px |
| Subheading | Group labels, card titles | Medium | 14–16px |
| Body | Primary content, descriptions, form labels | Regular | 14–16px |
| Caption | Metadata, timestamps, helper text | Regular | 12–13px |
| Monospace | Code, file paths, error messages, data values | Regular | 13–14px |

**Iconography:**
- Use universal icons: checkmark (accepted/success), X (rejected/error), clock (deferred/pending), alert triangle (warning), magnifying glass (search — if retained as enhancement), plus (create), trash (delete), chevron (navigation), drag handle (reorder), play (run), stop (stop run), dash (not evaluated), info (advisory), spinner (in-progress/transient)
- Every icon-only button must have an `aria-label`
- Icons must have minimum 16×16px touch target; interactive icons minimum 24×24px

**Spacing rhythm:**
- 4px base unit
- Tight spacing (4–8px): within table rows, between form label and input
- Medium spacing (12–16px): between related content groups, card padding
- Large spacing (24–32px): between sections, view margins
- Extra large spacing (48px): between major page regions

**Data density approach:**
- Tables use tight vertical rhythm (8px row padding) for scanability
- Cards use medium spacing (16px padding) for readability
- Charts use large spacing (24px margins) for visual breathing room
- Mobile converts tables to cards with stacked fields — maintains density while adapting to narrow viewport

### Design Decisions

**DD-1: Task-based navigation over entity-based navigation**
- Decision: Navigation is organized around user tasks (Overview, Routes, Runs, Analytics, Errors, Settings) rather than system entities (Videos, Channels, Playlists, Conditions, Actions).
- Rationale: Users approach YuHaul with goals ("check if system is working", "tune a route", "investigate a rejection"), not with entity queries ("show me all conditions"). Task-based grouping matches mental model and reduces navigation steps for common workflows.
- Alternatives considered: Entity-based navigation (Videos, Channels, Routes, Statistics) — rejected because it fragments related information (a run's video outcomes span entities) and doesn't match user task patterns.
- Trade-off accepted: Some information appears in multiple contexts (a video appears in run detail AND channel analysis). This is intentional polyhierarchy, not duplication.

**DD-2: Sidebar navigation on desktop, bottom bar on mobile**
- Decision: Desktop uses persistent sidebar (240px, collapsible). Mobile uses bottom navigation bar with 5 primary items + overflow.
- Rationale: Sidebar provides persistent wayfinding and is standard for dashboard apps. Bottom bar is thumb-accessible on mobile and matches iOS/Android platform conventions.
- Alternatives considered: Top navigation bar — rejected because it competes with search and manual run trigger in the header. Hamburger menu on desktop — rejected because it hides navigation and reduces discoverability for a tool used frequently.
- Trade-off accepted: Bottom bar limits visible navigation to 5 items. Settings is in overflow. Acceptable because Settings is accessed infrequently.

**DD-3: Theme follows system preference, no manual toggle in v1**
- Decision: The interface uses `prefers-color-scheme` CSS media query to follow the user's operating system theme. No manual dark/light toggle in v1.
- Rationale: A single-user personal tool inherits the user's system preference automatically. Adding a toggle requires persisting the choice (localStorage or backend), generating two complete token sets, and testing both themes — non-trivial implementation cost for a feature the OS already provides.
- Alternatives considered: Manual toggle with localStorage persistence — possible as a future enhancement but adds implementation complexity without proportional user benefit for a single-user tool.
- Trade-off accepted: User cannot override OS preference within the app. Acceptable because OS-level theme switching is immediate and system-wide.

**DD-4: SPA with URL reflecting all meaningful state**
- Decision: Single-page application where every navigable state has a URL. Back button works. New tab opens same view. Links are shareable.
- Rationale: Users bookmark frequently accessed views (specific route editor, channel analysis page). Users share links to specific runs or channels. SPA provides smoother transitions between related views.
- Alternatives considered: MPA (multi-page app) — rejected because the interface has many cross-cutting views where SPA's client-side routing provides smoother transitions. Hash-based routing — rejected because URLs are less readable and less shareable.
- Trade-off accepted: SPA requires explicit focus management on route changes and careful state management to avoid stale data.

**DD-5: Evidence trail as vertical sequence, not tree**
- Decision: Decision evidence is presented as a top-to-bottom evaluation sequence (Route 1 → Route 2 → Route 3), not as a tree or branching visualization.
- Rationale: Routes are evaluated in strict order. The sequential presentation matches the actual evaluation process. A tree would imply parallel evaluation, which is inaccurate. Vertical sequence is also simpler to implement responsively.
- Alternatives considered: Tree visualization — rejected because it implies parallel evaluation. Flowchart — rejected because the evaluation is linear, not branching.
- Trade-off accepted: For runs with many routes, the evidence trail can be long. Mitigate with collapsible route sections and a summary at the top.

**DD-6: Progressive disclosure over dense-at-once**
- Decision: Key information is shown at summary level first, with details available on demand (click to expand, click to navigate).
- Rationale: Users typically scan before investigating. Showing full evidence for every video in every run would overwhelm. Progressive disclosure reduces cognitive load while keeping full information accessible.
- Alternatives considered: Dense tables showing everything at once — rejected because it creates information overload on initial view and makes scanning slower.
- Trade-off accepted: Users must click to see full details. This adds a step for users who already know what they're looking for. Mitigate with direct linking (URL includes video ID) and keyboard shortcuts (arrow keys to navigate between videos in a run).

**DD-7: Flat condition list for v1 route editor**
- Decision: Route conditions use a flat list of conditions with a single AND/OR toggle at the top. No nested condition groups in v1.
- Rationale: The anchor specifies "configurable conditions" without requiring nesting. For v1, conditions are typically 2–4 simple rules (title contains X, duration > Y, channel equals Z). Flat list with AND/OR covers the common case with minimal UI complexity.
- Alternatives considered: Nested condition groups with AND/OR at group level — deferred to v2 if user needs arise. Visual query builder — rejected as over-engineered for v1.
- Trade-off accepted: Users cannot express complex boolean logic like "(A AND B) OR (C AND D)" in v1.

**DD-7b: Route-scoped exclusions as a separate region in Route Editor**
- Decision: Exclusions are a dedicated region in the Route Editor, visually distinct from conditions. Each exclusion type (channel, video, title, word/phrase, duration) is a separate list with add/remove controls. Exclusions apply per-route — a video excluded from Route A remains eligible for Route B.
- Rationale: Exclusions are a different semantic construct from conditions. Conditions define what a route accepts; exclusions define what a route explicitly rejects before condition evaluation. Separating them makes the route's behavior legible.
- Alternatives considered: Exclusions as a special condition type (e.g., "title NOT contains") — rejected because exclusions run before condition evaluation and have different semantics. Global exclusions — rejected because the requirement specifies per-route scoping.
- Trade-off accepted: More UI elements in the Route Editor. Acceptable because the single-column layout accommodates it and the separation makes route behavior clearer.

**DD-8: Mobile tables convert to cards**
- Decision: On viewports < 768px, data tables convert to card layout where each row becomes a stacked card with essential fields visible.
- Rationale: Cards preserve scanability on narrow viewports by presenting each item as a discrete unit with stacked fields. This is more mobile-native than column reduction and avoids horizontal scrolling on touch devices.
- Alternatives considered: Reduced columns with expand — deferred; cards are the default. Horizontal scroll — rejected because horizontal scrolling on mobile is poor UX.
- Trade-off accepted: Cards show fewer items per viewport than a table. Acceptable because mobile use is typically scanning rather than deep analysis.

**DD-9: Three-layer video outcome model**
- Decision: Video outcomes are modeled as three independent layers: processing state, per-route decisions, and per-action execution. Summary badges derive from the layers but are not the authoritative model.
- Rationale: A single outcome badge cannot represent the full reality of routing. A video can be accepted by one route and fail on another, or be accepted by a route whose action fails. The three layers make this visible and debuggable.
- Alternatives considered: Single outcome badge only — rejected because it hides actionable information. Separate views per layer — rejected because it fragments information.
- Trade-off accepted: More complex data model. Acceptable because the complexity reflects reality and the UI presents it progressively.

**DD-10: Dry-run, backtest, and replay as distinct operations**
- Decision: Dry-run processes current pending workload with real routing semantics but no external mutations. Backtest replays retained historical observations against hypothetical configuration. Replay reprocesses retained historical observations through a selected configuration. All three protect production state.
- Rationale: These serve different user needs with different inputs, configuration sources, outputs, and protection requirements. Conflating them creates confusion.
- Alternatives considered: Unified "test mode" — rejected because the operations have genuinely different semantics.
- Trade-off accepted: Three separate UIs/modes instead of one. Acceptable because the workflows are genuinely different.

**DD-11: Global duplicate threshold in Settings, not per-route**
- Decision: The fuzzy matching threshold for duplicate detection is a single global setting in Settings, not a per-route configuration.
- Rationale: Duplicate detection operates against a shared corpus. A per-route threshold would create inconsistency — the same video pair could be a duplicate under Route A but not under Route B. A global threshold ensures consistent detection.
- Alternatives considered: Per-route threshold — rejected because duplicate detection is a corpus-level operation.
- Trade-off accepted: User cannot tune duplicate sensitivity per genre. Acceptable because a single threshold covers the common case.

**DD-12: Retention provenance as progressive disclosure, not a filter**
- Decision: Retention signal provenance (user-driven vs. YuHaul-routed) is shown as a summary line and per-video badges in the channel detail view, not as a filter on the channel list.
- Rationale: The channel list needs to remain scannable — the retained count is the primary signal. Provenance breakdown is investigative detail needed during channel evaluation, not during scanning.
- Alternatives considered: Filter by provenance on channel list — rejected because it adds UI complexity to the primary scan view.
- Trade-off accepted: User must click into a channel to see provenance breakdown. Acceptable because provenance inspection is investigative, not daily scanning.

**DD-13: Saved run status with optional SSE feedback**
- Decision: Reconcile the original SSE-only design with the accepted durable diagnostic boundary. Run Detail reads saved phase/status on entry and periodically while running; `/api/runs/:id/stream` may prompt faster reads. Only a saved terminal status establishes closure. See “Run Now — Recorded Progress and Closure” for visible states.
- Rationale: Runs may take minutes; discovery can have unknown selected inputs and unchanged counters. The person needs truthful continuity after reload or lost events, not a fabricated completion total.
- Alternatives considered: SSE-only/no persistent feedback loses recoverability; percentage/ETA has no defensible denominator; a modal interrupts other work. Keep automatic navigation to existing Run Detail rather than build a second monitoring surface.
- Trade-off accepted: Saved reads have modest latency; optional SSE improves responsiveness without becoming a second lifecycle authority. This is an experience dependency on the existing architecture, not a new transport/schema decision.

### Open Design Questions

**OQ-1: Chart library selection**
- Option A: Lightweight charting library (e.g., Chart.js, uPlot) — smaller bundle, simpler API, sufficient for line/bar charts
- Option B: Full-featured library (e.g., ECharts, Recharts) — more chart types, interactive features, larger bundle
- Option C: Pure SVG/D3 approach — maximum flexibility, highest implementation cost
- Deciding criterion: The anchor says "no particular charting framework requirement." For v1, line charts (trends) and bar charts (comparisons) are sufficient. Recommend Option A for simplicity, but this is an architecture decision.
- Owner: Architect (after design spec is accepted)
- Blocking: No — Coder can start with placeholder chart components and swap library later

**OQ-2: Quota bucket awareness**
- Option A: Display raw quota numbers (units used / units remaining) — precise but may be confusing if bucket details are unknown
- Option B: Display relative status (green/amber/red bar) with raw numbers on hover/tap — simpler at a glance, detailed on demand
- Option C: Display bucket-specific information (reset time, bucket size) alongside raw numbers — most informative but requires YouTube API quota endpoint availability
- Deciding criterion: How much quota information does the YouTube API actually expose? If bucket details are available, Option C. If only raw consumption is available, Option A or B.
- Owner: Architect (after API investigation)
- Default: Option A (raw numbers) for v1, with Option B as enhancement
- Blocking: No — design specifies what to display, implementation determines what's available

---

*This spec is a behavioral contract. It specifies what the system must do, for whom, under what conditions — expressed in terms each downstream agent can verify. Visual appearance decisions are separated in the Visual Semantics section. The behavioral specification is independently implementable without visual assets.*
