---
type: design-spec
title: YuHaul Admin Interface Design Specification
description: "Human-centered design specification for the YuHaul web administration interface — a responsive, device-aware operational dashboard for managing YouTube subscription routing, inspecting processing decisions, and analyzing content discovery performance."
tags: [design-spec, web-ui, admin-dashboard, youtube]
status: accepted
---

## Design Spec: YuHaul Admin Interface

### Experience Context

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
The user checks the Errors view. The error queue shows 1 item: authentication token expired (needs manual re-authentication). They also see the Deferred tab: 2 items deferred due to quota exhaustion. The deferred items will be eligible for the next scheduled or manual run once sufficient quota is available. The user re-authenticates, then removes the resolved auth error from the queue.

**Scenario 5 — Backtest before committing a change (10 minutes):**
The user wants to add a new route for a niche genre. Before committing, they open Backtest, apply the proposed route configuration to 30 days of historical observations, and see: the new route would have caught 23 videos, 15 of which overlap with existing routes (already caught), 8 are new catches. They're satisfied and create the route.

**Scenario 6 — Channel advisory review (3 minutes):**
The user notices a "Low usefulness" advisory on the Analytics page for a channel they subscribed to months ago. The advisory notes: "This channel has produced 45 discoveries in the last 90 days, but only 2 were retained in designated rotation playlists. Consider whether this subscription still serves your interests." The user reviews the channel's recent videos, agrees, and unsubscribes directly from YouTube.

### Workflow Map

Derived from the 32 acceptance criteria. Each workflow names the surfaces and endpoints that serve it, and identifies capability gaps.

| # | Workflow | Surfaces / Endpoints | ACs Served | Capability Gap |
|---|----------|---------------------|------------|----------------|
| 1 | **Account Connection** — OAuth flow to connect YouTube account | Settings > Account, `POST /api/settings/account/connect` | AC 1 | None |
| 2 | **Discovery & Processing** — Scheduled or manual run discovers videos, evaluates routes, executes actions | Overview (trigger), Runs (trigger + inspect), `POST /api/runs`, `GET /api/runs/:id/progress` (SSE) | AC 2, 11, 12, 13, 14, 30, 32 | None |
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

**No capability gaps identified.** All 32 ACs map to existing architecture endpoints and entity model.

---

### User Stories

#### Account & Onboarding

**US-001: Connect YouTube Account**
- **Actor:** YuHaul administrator (the single user)
- **Context:** First-time setup or re-authentication after token expiry
- **Trigger:** User navigates to Settings > Account and clicks "Connect YouTube Account"
- **Desired outcome:** YouTube account is authenticated via OAuth2; YuHaul can discover subscriptions and manage playlists
- **Observable success:** Account status shows "Connected" with channel name and avatar; next run will discover subscriptions
- **Source:** AC 1

**US-002: Import Historical Corpus**
- **Actor:** YuHaul administrator migrating from the previous application
- **Context:** Existing corpus of 10,000+ titles must be preserved for duplicate detection
- **Trigger:** User navigates to Settings > Data and uploads a CSV or JSON file
- **Desired outcome:** Historical titles are imported into the duplicate detection corpus; duplicates with existing entries are identified and skipped; corpus integrity is preserved
- **Observable success:** Import summary shows count of new titles added, duplicates skipped, and total corpus size; duplicate detection works identically for imported and native titles
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
- **Desired outcome:** A processing run starts; the user sees real-time progress via SSE stream; run completes with outcomes
- **Observable success:** Progress indicator shows current state (discovering, evaluating, acting); run appears in Runs list with outcome summary; accepted videos are in target playlists on YouTube
- **Source:** AC 2, 17

**US-010: Run a Dry-Run with Draft Configuration**
- **Actor:** YuHaul administrator previewing changes before committing
- **Context:** User has unsaved route edits and wants to see what would happen
- **Trigger:** User clicks "Dry Run" and confirms the dialog
- **Desired outcome:** System processes pending videos against draft (unsaved) route configuration without mutating playlists, duplicate corpus, or statistics
- **Observable success:** Dry-run results show "Would have" language for actions; production state is unchanged; user can save the draft configuration if satisfied
- **Source:** AC 26

**US-011: Backtest Proposed Configuration**
- **Actor:** YuHaul administrator evaluating routing changes against history
- **Context:** User wants to know what a configuration change would have caught or lost over the past 30 days
- **Trigger:** User navigates to Backtest, modifies proposed routes, selects date range, clicks "Run Backtest"
- **Desired outcome:** System replays retained historical observations against proposed configuration; results show new catches, lost catches, and net change
- **Observable success:** Comparison view shows actual vs proposed outcomes; user can inspect individual video differences; user can apply or discard the proposed configuration
- **Source:** AC 27

**US-012: Replay History with a Specific Configuration**
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
- **Desired outcome:** Deferred items show video, channel, deferral reason, and next-eligibility indicator; user can retry when quota allows or discard
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

**Context:** The user is migrating from the old YuHaul application and has a CSV file with 12,000 historical video titles.

**Goal:** Import the historical corpus so duplicate detection works from day one.

**Task sequence:**
1. User navigates to Settings > Data
2. User sees "Historical Corpus Import" section
3. User clicks "Import" — file picker appears
4. User selects the CSV file
5. System validates format and shows preview: "12,000 titles found. Date range: 2020-01-15 to 2026-09-01. First 10 titles shown for confirmation. 847 titles already exist in corpus (duplicates to skip)."
6. User selects "Append" mode (default)
7. User confirms import — progress indicator shows: "Importing: 3,200 of 12,000 titles (27%) — ~180 titles/sec — estimated 49 seconds remaining"
8. Import completes: toast "Imported 11,153 new historical titles (847 duplicates skipped). Total corpus: 11,153 titles."
9. User clicks "Export" to verify the corpus can be backed up

**Outcome:** Historical corpus imported, duplicates identified and skipped, corpus integrity preserved.

**Edge condition:** If the file format is invalid, the system must show a clear error ("Expected CSV with title column, or JSON array of objects with 'title' field") without losing the user's context. If the user cancels mid-import in Append mode, already-imported titles are preserved.

---

#### SC-005: Backtest Before Adding a Route (Desktop, 10 minutes)

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
1. Run starts — Overview shows "Run in progress" with SSE-driven progress
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

#### SC-007: Re-authentication After Token Expiry (Desktop, 2 minutes)

**Context:** The user's YouTube OAuth token has expired (tokens typically last hours to days).

**Goal:** Re-authenticate without losing any processing state.

**Task sequence:**
1. User opens YuHaul — Overview shows "Account: Disconnected" warning
2. Next scheduled run would fail — user sees an auth error in the Error queue
3. User navigates to Settings > Account
4. Account status shows "Disconnected — token expired"
5. User clicks "Reconnect" — OAuth flow opens in new tab
6. User authorizes on YouTube — redirected back to YuHaul
7. Account status updates to "Connected — [channel name]"
8. User navigates to Errors — the auth error is still in the queue
9. User clicks "Retry" on the auth error — system re-processes the video with fresh token
10. On success: error is dismissed from active queue

**Outcome:** Account reconnected, failed items retried, processing can resume.

**Edge condition:** If the retry fails again (e.g., video was removed from YouTube), the error updates with the new reason and stays in the queue. The user can dismiss it manually.

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
- **Components:** Route row (name, description snippet, enabled/disabled toggle, final badge, match count, drag handle, move up/down buttons), empty state

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

**View: Run List** — Browse processing history; user job: find a specific run or scan recent activity.
- **Regions:**
  - Filter region: date range, run type (manual/scheduled/dry-run/replay/backtest), status filter
  - Run list region: chronological list of runs
- **Components:** Run row (timestamp, type badge, duration, video count, outcome summary, quota cost, status), empty state, filter controls

**View: Run Detail** — Inspect a single run; user job: understand what happened in this run and why.
- **Regions:**
  - Header region: run metadata (timestamp, type, duration, configuration version with link to Configuration History)
  - Summary region: outcome counts (accepted/rejected/deferred/failed), quota cost summary
  - Video outcomes region: filterable list of videos processed in this run
- **Components:** Outcome summary cards, video outcome row (title, channel, outcome badge, route match info, expand action), filter by outcome type, quota cost breakdown, configuration version link

**View: Decision Detail** — Inspect a single video's routing outcome; user job: understand exactly why this video was accepted, rejected, deferred, or failed — across all three layers of the outcome model.
- **Regions:**
  - Video info region: title, channel, duration, publish date, thumbnail
  - Processing outcome region: video/run-level lifecycle state badge (discovered/evaluating/evaluated/acted/completed/deferred/not-yet-evaluated)
  - Route decisions region: per-route evaluation results (accepted/rejected/skipped — skipped includes final-route-skip, exclusion-skip, and disabled-route-skip)
  - Action execution region: per-action results (succeeded/failed/deferred)
  - Evidence trail region: step-by-step route evaluation showing conditions matched/not-matched
- **Components:** Video metadata, processing outcome badge, route decision row (route name, decision, icon, skip reason if skipped), action execution row (action type, target, result), condition evaluation row (route name, condition, result, icon)

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
- **Components:** Tab selector, error row (video title, channel, error type, timestamp, retry button, remove button), error detail panel, deferred row (video title, channel, deferral reason, next-eligibility indicator, manual retry button, discard button), deferred detail panel

**View: Backtest** — Compare proposed routing against historical observations, or replay/reprocess historical videos; user job: evaluate a configuration change before committing, or re-evaluate history with different settings.
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
  - Account region: YouTube account connection status, re-authenticate option
  - Retention playlists region: designate which playlists represent the user's actual keep signal
  - Duplicate detection region: fuzzy matching threshold configuration
  - Data region: historical corpus import/export, data management
  - Danger zone region: disconnect account
- **Components:** Schedule form, quota display with ledger breakdown, account status card, retention playlist selector (multi-select from user's YouTube playlists), threshold slider with numeric display and explanation, import/export controls, danger action buttons

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

**Deferred item interaction — discard:**
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
- "Run Now" on click: confirmation dialog "Start a processing run now?", on confirm: starts run, frontend subscribes to SSE stream (`/api/runs/:id/stream`), navigates to Run Detail for the new run with live progress
- "Dry Run" on click: confirmation dialog (see SC-003), on confirm: starts dry-run, subscribes to SSE stream, shows live progress, navigates to dry-run results

**Empty states:**
- Route list empty: "No routes yet. Create your first route to start routing videos." + "New Route" button
- Run list empty: "No runs yet. Start a processing run to see results here." + "Run Now" button
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

**Component/interaction: Run progress indicator (SSE-driven)**
- Standard: WCAG 2.2 SC 4.1.3 Status Messages (AA)
- Requirement: The run progress indicator must use `role="status"` and `aria-live="polite"` so screen readers announce progress updates without interrupting focus. Progress must include text describing current state (e.g., "Processing: 12 of 42 videos", "Evaluating route conditions", "Actions in progress") — not just a visual bar or spinner. On SSE connection loss, an error message must appear using `role="alert"` and `aria-live="assertive"`.
- Verification: Manual: activate screen reader, trigger a run, confirm progress updates are announced. Disconnect network mid-run, confirm error is announced.

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

**DD-13: SSE for real-time run progress transport**
- Decision: Run progress is delivered via Server-Sent Events (SSE) through a REST endpoint (`/api/runs/:id/stream`). The frontend subscribes and receives real-time events for processing state transitions, per-video outcomes, and quota updates.
- Rationale: Runs may take minutes. Users need immediate feedback. SSE is unidirectional (server→client), has native browser support (`EventSource`), auto-reconnects, and works over standard HTTP. WebSocket was considered but adds unnecessary bidirectional complexity.
- Alternatives considered: Polling (adds latency, wastes requests), no real-time progress (poor UX), WebSocket (over-engineered).
- Trade-off accepted: SSE requires server-side streaming support, but this is well-supported by Go's `http.Flusher` interface.

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
