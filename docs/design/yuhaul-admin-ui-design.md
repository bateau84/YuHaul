---
type: design-spec
title: YuHaul Admin Interface Design Specification
description: "Human-centered design specification for the YuHaul web administration interface — a responsive, device-aware operational dashboard for managing YouTube subscription routing, inspecting processing decisions, and analyzing content discovery performance."
tags: [design-spec, web-ui, admin-dashboard, youtube]
status: draft
---

## Design Spec: YuHaul Admin Interface

### Experience Context

- **Experience requirement:** Enable a single user to understand at a glance whether their YouTube discovery system is operating correctly, configure routing rules with confidence, investigate why specific videos were accepted or rejected, and identify trends in channel quality — all without consulting documentation or switching tools.

- **Scenario 1 — Morning check-in:** The user opens YuHaul on their phone while having coffee. They see the Overview: last run completed 6 hours ago, 42 videos processed, 3 accepted, 12 deferred (quota), 0 errors. They tap into Runs to see the latest run's details — the 3 accepted videos went to their "New Discoveries" playlist as expected. They glance at the error queue: empty. They close the app. Total time: 90 seconds.

- **Scenario 2 — Route tuning:** The user notices their "Ambient" route has accepted 0 videos in the last 2 weeks. On desktop, they navigate to Routes, edit the Ambient route's conditions, and lower the duration threshold from 10 minutes to 5 minutes. They run a dry-run to see what would happen with the current pending workload using the unsaved draft conditions. The dry-run shows 8 currently pending videos would now match under the adjusted conditions. They save the route. They later check Analytics to see if acceptance rates improve. Time: 5 minutes.

- **Scenario 3 — Investigating a reject:** The user remembers a specific channel that should have matched but didn't. They navigate to Analytics > Channels, find the channel, and see its recent videos. They click a specific video that was rejected — the Decision Detail view shows the evidence: "Route 'Music Discovery' condition 'title contains [genre keyword]' — NOT MATCHED. Route 'Ambient' condition 'duration > 10min' — NOT MATCHED. Result: rejected (no route matched)." They realize their route conditions need adjustment. Time: 3 minutes.

- **Scenario 4 — Error triage and deferred work:** The user checks the Errors view. The error queue shows 1 item: authentication token expired (needs manual re-authentication). They also see the Deferred tab: 2 items deferred due to quota exhaustion. The deferred items will be eligible for the next scheduled or manual run once sufficient quota is available. The user re-authenticates, then removes the resolved auth error from the queue. Time: 2 minutes.

- **Scenario 5 — Backtest before change:** The user wants to add a new route for a niche genre. Before committing, they open Backtest, apply the proposed route configuration to 30 days of historical observations, and see: the new route would have caught 23 videos, 15 of which overlap with existing routes (already caught), 8 are new catches. They're satisfied and create the route. Time: 10 minutes.

- **Scenario 6 — Channel advisory review:** The user notices a "Low usefulness" advisory on the Analytics page for a channel they subscribed to months ago. The advisory notes: "This channel has produced 45 discoveries in the last 90 days, but only 2 were retained in designated rotation playlists. Consider whether this subscription still serves your interests." The user reviews the channel's recent videos, agrees, and unsubscribes directly from YouTube. Time: 3 minutes.

### Information Architecture

**Primary grouping principle:** Task-based — navigation is organized around what the user wants to do, not what the system manages internally.

**Top-level sections:**

| Section | Purpose | What it contains |
|---------|---------|-----------------|
| **Overview** | Quick status check, entry to everything | Run schedule status, last run summary, quota state, error count, quick actions |
| **Routes** | Define and manage routing rules | Route list with ordering, create/edit/delete, enable/disable, route conditions/exclusions/actions editor |
| **Runs** | Inspect processing history | Run list (filterable), run detail with video outcomes, decision evidence, dry-run results |
| **Analytics** | Understand trends and channel quality | Temporal charts, run/route statistics, channel analysis with advisories, source performance, duplicate provenance |
| **Errors** | Triage failures and deferred work | Error queue with retry/remove, deferred work queue with next-eligibility status |
| **History** | Configuration version history | Route/config change history with timestamps, diffs, linked runs |
| **Settings** | System configuration | Schedule config, quota limits, account status, retention playlist designation, data import/export |

**Navigation model:** Sidebar navigation on desktop (persistent, collapsible on mobile). Top-level sections are always visible. Within Routes, Runs, and Analytics, secondary navigation uses tabs or sub-views.

**URL structure (SPA with pushState):**
- `/` — Overview
- `/routes` — Route list
- `/routes/:id` — Route detail/edit
- `/routes/new` — Create route
- `/runs` — Run history list
- `/runs/:id` — Run detail with video outcomes
- `/runs/:id/video/:videoId` — Decision detail for a specific video in a run
- `/analytics` — Analytics dashboard (default: trends)
- `/analytics/channels` — Channel analysis
- `/analytics/channels/:channelId` — Single channel deep-dive
- `/errors` — Error queue and deferred work
- `/backtest` — Backtest interface
- `/settings` — System settings
- `/history` — Configuration history
- `/history/:versionId` — Configuration version detail

### User/Task Flows

**Flow 1: Create a new route**

```
1. User navigates to Routes → sees route list
2. User clicks "New Route" → Route Editor view opens
3. User enters route name (required), description (optional)
4. User configures conditions (flat list with AND/OR toggle):
   a. Select field (title, duration, channel, etc.)
   b. Select operator (contains, equals, greater than, etc.)
   c. Enter value
   d. Logical connective (AND/OR between conditions — top-level toggle)
5. User configures exclusions (route-scoped, optional):
   a. Channel exclusion: enter channel name → "Add" → entry appears in excluded channels list
   b. Video identity exclusion: enter video ID or title → "Add" → entry appears in excluded videos list
   c. Title exclusion: enter exact title string → "Add" → entry appears in excluded titles list (exact match after trim and Unicode NFC normalization)
   d. Word/phrase exclusion: enter word or phrase → "Add" → entry appears in excluded words list (title-contains match)
   e. Duration limits: optional min and/or max in seconds (empty = no limit)
   → Excluded videos skip this route but remain eligible for other routes
6. User configures actions (what happens on match):
   a. Select action type (add to playlist — v1 only supports playlist insertion)
   b. Configure action parameters (playlist selection)
7. User sets route evaluation order (drag to reorder, or use position input)
8. User optionally marks route as "final" (stops further evaluation after match)
9. User optionally enables/disables route (default: enabled)
10. User clicks "Save" → route is created, list updates
    → Error path: validation fails → inline errors on invalid fields, focus moves to first error
    → Success path: navigate to route list, new route visible, toast confirmation
```

**Flow 2: Inspect a run decision**

```
1. User navigates to Runs → sees run list sorted by date descending
2. User clicks a run → Run Detail view opens
3. Run Detail shows: run timestamp, duration, videos processed, outcomes summary (accepted/rejected/deferred/failed counts), quota cost summary
4. User clicks a video outcome row → Decision Detail panel opens (or navigates to detail view)
5. Decision Detail shows:
   - Video title, channel, duration, publish date, thumbnail
   - Three-layer outcome model (see Behavioral Specification for full model):
     a. Video/run processing state: discovered, evaluating, evaluated, acted, completed, deferred, not-yet-evaluated
     b. Per-route decision state: for each route evaluated, accepted/rejected/skipped
     c. Per-action execution state: succeeded, failed, deferred
   - Evidence trail: which routes were evaluated, in order
     - For each route: which conditions matched (green check) or didn't match (red X)
     - Which actions would have/were executed
   - If accepted: which playlist it was added to (with link to YouTube playlist)
   - If deferred: reason (quota limit, etc.)
   - If failed: error type and message
6. User can navigate to next/previous video in the same run (keyboard arrows or nav buttons)
7. User presses Back or clicks Run list to return
```

**Flow 3: Run a dry-run**

```
1. User navigates to Routes (or anywhere with manual run trigger)
2. User clicks "Run" button → dropdown appears: "Run Now" | "Dry Run"
3. User selects "Dry Run" → confirmation dialog appears
4. Confirmation shows: "This will process current pending videos against current routes
   (including any unsaved draft conditions) without adding anything to playlists.
   Production state (duplicate corpus, idempotency records, statistics) is not affected."
5. User confirms → Run begins, frontend subscribes to SSE stream (`/api/runs/:id/stream`), progress indicator shown with real-time updates
    → Error path: SSE connection failure → auto-reconnect (EventSource built-in), retry button if stream cannot establish
6. Run completes → SSE stream closes, user navigates to Runs list, latest entry marked as "Dry Run"
7. User clicks the dry-run → sees same outcome detail as a real run, but with "Would have"
   language for actions
```

**Flow 4: View channel analytics**

```
1. User navigates to Analytics → default view shows temporal trend charts
2. User clicks "Channels" tab → channel list appears with usefulness metrics
3. Channel list shows: channel name, discovered count, routed count, retained count (in designated rotation playlists), usefulness rate, time since last useful discovery
4. User clicks a channel → channel detail view opens
5. Channel detail shows:
   - Channel name, subscription date (if known), total videos
   - Usefulness metrics: discovered, routed, retained (in designated rotation playlists), usefulness rate, yield
   - Retention signal provenance summary: user-driven count vs. YuHaul-routed count (with clear labeling that YuHaul-routed membership does not count as retention)
   - Usefulness trend over time (chart)
   - Recent videos with their outcomes and retention provenance badges (User-added / YuHaul-routed)
   - Which routes matched this channel's content
   - Advisory section (if applicable): system-generated suggestions about channel value
6. User can filter by date range using date picker
7. User clicks a video → Decision Detail (same as Flow 2, step 5)
```

**Flow 5: Manage errors and deferred work**

```
1. User navigates to Errors → sees two tabs: "Errors" and "Deferred"
2. Error tab shows: video title, channel, error type, failure reason, timestamp
3. User clicks an error → detail panel shows full error context
4. User clicks "Retry" → system re-attempts processing
   → On success: item dismissed from active queue, toast confirmation
   → On failure: error updated with new failure reason, stays in queue
5. User clicks "Remove" → item dismissed from active queue (historical record preserved)
6. Deferred tab shows: video title, channel, deferral reason, next-eligible indicator
7. Deferred items remain queued and become eligible for the next scheduled or manual run when sufficient quota is available
8. User can manually "Retry Now" on a deferred item (if quota allows)
9. User can "Discard" a deferred item — dismissed from active queue, historical record preserved
```

**Flow 5b: Review configuration history**

```
1. User navigates to History (or accesses from a run's metadata link)
2. Configuration History view shows: chronological list of all configuration changes
3. Each entry shows: timestamp, change summary (e.g., "Added channel exclusion to Route 'Ambient'"), configuration version number, count of runs executed under this version
4. User clicks an entry → change detail panel opens
5. Change detail shows:
   a. Full effective route configuration snapshot at that point in time
   b. List of runs that executed under this configuration version (clickable → Run Detail)
   c. Diff view: comparison to previous version (what was added/removed/modified)
6. User can click a run link → navigates to Run Detail
7. User can compare any two versions by selecting them and clicking "Compare"
```

**Flow 6: Run a backtest or replay**

```
1. User navigates to Backtest
2. Backtest interface shows mode selector: "Hypothetical Backtest" | "Replay/Reprocess"
3. If Hypothetical Backtest:
   a. Current route configuration shown as read-only preview
   b. User can modify routes in a "proposed configuration" editor (changes are draft, not saved)
   c. User selects date range for historical observations
    d. User clicks "Run Backtest" → frontend subscribes to SSE stream, progress indicator shown with real-time updates
   e. Backtest completes → results view shows:
      - Comparison: actual historical outcomes vs. proposed configuration outcomes
      - New catches (videos that would have matched under proposed config but didn't)
      - Lost catches (videos that matched historically but wouldn't under proposed config)
      - Net change summary
   f. User can inspect individual video outcomes in the comparison
   g. User can "Apply" the proposed configuration (creates/updates routes) or "Discard"
4. If Replay/Reprocess:
   a. User selects configuration version (current or historical from Configuration History)
   b. User selects date range for historical observations
    c. User clicks "Run Replay" → frontend subscribes to SSE stream, progress indicator shown with real-time updates
   d. Replay completes → results view shows:
      - Each video's replay outcome (accepted/rejected/deferred/failed) under the selected configuration
      - Comparison to original production outcome (if available): "Originally accepted, now rejected" etc.
      - Outcomes are labeled as "Replay" — distinct from production outcomes
   e. User can inspect individual video outcomes in the replay results
   f. User can "Apply Configuration" to make this historical config current, or "Discard"
```

**Flow 7: Import historical corpus**

```
1. User navigates to Settings → Data section
2. User sees "Historical Corpus Import" option
3. User clicks "Import" → file picker appears (accepts CSV or JSON)
4. User selects file → system validates format and shows preview:
   - File format detected (CSV or JSON)
   - N titles found
   - Date range of entries (earliest to latest publish date, if available)
   - Sample of first 10 titles for format confirmation
   - Duplicate check: M titles already exist in corpus
5. User sees mode selection:
   - "Append" (default): add new titles to existing corpus
   - "Replace": overwrite existing corpus entirely (with confirmation)
6. User confirms import → system stages and validates all titles before committing
7. Progress indicator shown with:
   - Percentage complete (X of N titles processed)
   - Processing rate (titles/second)
   - Estimated time remaining
   - Cancel button (Append mode: stops import, preserves already-imported titles; Replace mode: cancels before commit, leaves previous corpus intact)
8. On completion: summary toast "Imported N new historical titles (M duplicates skipped). Total corpus: T titles."
9. User can also "Export" current corpus for backup
```

**Flow 8: Review channel advisories**

```
1. User navigates to Analytics → Channels
2. Advisory badges appear next to channels with system-generated suggestions
3. User clicks a channel with advisory → channel detail shows advisory section
4. Advisory shows: suggestion text, supporting evidence (metrics), suggested action
5. User can "Take action" (system opens the specific action described, e.g., YouTube unsubscribe page), "Dismiss" (acknowledge but don't act),
   or "Ignore" (hide advisory for this channel for 30 days)
6. Advisory decisions are logged for reference
```

### Behavioral Specification

#### Views

**View: Overview** — Quick status dashboard; user job: determine system health in under 30 seconds.
- **Regions:**
  - Status bar region: last run time, run count, next scheduled run, account connection status
  - Summary cards region: key metrics at a glance (videos processed, accepted, deferred, errors)
  - Quick actions region: "Run Now", "Dry Run", "View Errors" buttons
  - Activity feed region: recent run history (last 5 runs as compact rows)
- **Components:** Status card, metric card, quick action button, activity row

**View: Route List** — Manage all routing rules; user job: see all routes, their order, and status.
- **Regions:**
  - Toolbar region: "New Route" button
  - Route list region: sortable list of routes with drag-to-reorder
- **Components:** Route row (name, description snippet, enabled/disabled toggle, final badge, match count, drag handle), empty state

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
- **Layout:** Single-column layout at all viewport widths. Conditions, exclusions, and actions are vertically stacked. This avoids the complexity of responsive column reflow and ensures the editor works identically on mobile and desktop.

**View: Run List** — Browse processing history; user job: find a specific run or scan recent activity.
- **Regions:**
  - Filter region: date range, run type (manual/scheduled/dry-run), status filter
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
  - Quota ledger region: API consumption breakdown (see Quota Ledger below)
  - Summary statistics region: lifetime and period aggregates
- **Components:** Date range picker, line chart, bar chart, statistic card, sparkline, quota ledger table

**View: Channel Analysis** — Per-channel performance and advisories; user job: evaluate which channels produce valuable content and act on system suggestions.
- **Regions:**
  - Channel list region: sortable table of channels with usefulness metrics and advisory badges
  - Channel detail region (on selection): deep-dive into one channel
  - Duplicate provenance region (within channel detail): discovery credit vs. relevance credit breakdown
  - Retention signal provenance region (within channel detail): user-driven vs. YuHaul-routed membership breakdown
  - Advisory region (within channel detail): system-generated suggestions with evidence
- **Components:** Channel row (name, discovered count, routed count, retained count in designated rotation playlists, usefulness rate, time since last useful discovery, advisory badge), channel detail (video list, usefulness chart, route match breakdown, duplicate provenance breakdown, retention signal provenance breakdown, advisory card with take-action/dismiss/ignore actions)
- **Duplicate provenance display:** Within the channel detail view, a section titled "Discovery & Relevance Credit" shows:
  - **Discovery credit:** videos where this channel was the first source to publish the content (discovery = this channel "won" the race). Count and percentage of channel's discoveries.
  - **Relevance credit:** videos where this channel published content that was already discovered from another channel (relevance = this channel confirmed relevance of existing content). Count and percentage.
  - Per-video provenance note: for each video in the channel's video list, a small badge or note indicates whether this channel holds discovery credit ("First discovered") or relevance credit ("Also published by [other channels]"). This surfaces which channels are primary discoverers vs. redundant publishers.

- **Retention signal provenance display:** Within the channel detail view, a section titled "Retention Signal Provenance" shows:
  - **User-driven retention:** videos where this channel's content was added to a designated retention/rotation playlist by the user or an external process (not by YuHaul routing). These are genuine retention signals — the user chose to keep this content. Count and percentage of channel's retained videos.
  - **YuHaul-routed membership:** videos where this channel's content was added to a designated retention/rotation playlist by YuHaul's own routing action. These do NOT count as retention signals — the system placed them, not the user. Count and percentage, labeled clearly as "not counted as retention."
  - **Provenance summary line:** displayed prominently in the channel detail header area, e.g., "Retained: 12 (8 user-driven, 4 YuHaul-routed — not counted as retention)." This mirrors the existing duplicate provenance pattern and ensures the user can distinguish genuine keep signals from self-referential routing artifacts at a glance.
  - Per-video provenance note: for each video in the channel's video list that appears in a designated retention playlist, a small badge indicates provenance — "User-added" (genuine retention signal) or "YuHaul-routed" (not a retention signal). This mirrors the existing duplicate provenance pattern (discovery credit vs. relevance credit).

**View: Configuration History** — Route and configuration version history; user job: understand what changed, when, and which configuration governed each historical run.
- **Regions:**
  - History list region: chronological list of configuration changes
  - Change detail region (on selection): full effective route configuration at that point in time, linked runs
- **Components:** History entry (timestamp, change summary, configuration version number, linked run count), change detail panel (full route configuration snapshot, list of runs that executed under this configuration, "diff view" comparing to previous version)
- **Diff view:** Side-by-side or inline comparison showing what changed between two configuration versions (added/removed/modified routes, conditions, exclusions, actions). Highlight additions in green, removals in red, modifications in amber.
- **Linked runs:** Each configuration version shows which runs executed under it. Clicking a run navigates to Run Detail. This fulfills AC 25 (route/configuration history sufficient to interpret historical statistics) and the configuration-history requirement ACs 2–4.

**View: Error Queue and Deferred Work** — Triage processing failures and review deferred items; user job: resolve failures and understand deferred work.
- **Regions:**
  - Tab region: "Errors" tab, "Deferred" tab
  - Error list region: failed items with reasons
  - Error detail region (on selection): full error context
  - Deferred list region: quota-deferred items with next-eligibility indicator
  - Deferred detail region (on selection): deferral context and manual retry option
- **Components:** Tab selector, error row (video title, channel, error type, timestamp, retry button, remove button), error detail panel, deferred row (video title, channel, deferral reason, next-eligible indicator, manual retry button, discard button), deferred detail panel

**View: Backtest** — Compare proposed routing against historical observations, or replay/reprocess historical videos through current or proposed configuration; user job: evaluate a configuration change before committing, or re-evaluate history with different settings.
- **Regions:**
  - Mode selector region: "Hypothetical Backtest" tab, "Replay/Reprocess" tab
  - Configuration editor region (hypothetical): proposed route changes (draft, not saved)
  - Configuration selector region (replay): select current configuration or a historical version
  - Date range selector region: which historical period to test or replay
  - Results region: comparison view
- **Components:** Mode tab selector, route editor (same as Route Editor but in draft mode for hypothetical mode), configuration version selector (for replay mode), date picker, comparison cards (new catches, lost catches, net change), replay results list (reprocessed outcomes), video comparison list
- **Hypothetical backtest:** User proposes route changes, runs backtest against historical observations, sees what would have changed. Production state is not altered. Results show comparison: actual historical outcomes vs. proposed configuration outcomes.
- **Replay/reprocess:** User selects a configuration version (current or historical) and date range. System re-processes retained historical observations through that configuration, producing replay outcomes recorded distinctly from production outcomes. The user can see whether a video's outcome was produced by a production run, a replay, or a backtest. This fulfills the historical-reevaluation requirement (AC 2–5).
- **Protection:** Backtest and replay must not alter production duplicate corpus, idempotency records, statistics, or playlist state. Backtest operates on a read-only copy of historical observations. Replay produces replay-labeled results, not production results.

**View: Settings** — System configuration; user job: configure scheduling, quota limits, account, data management, and retention playlists.
- **Regions:**
  - Schedule region: cron-like schedule configuration
  - Quota region: API quota limits, current usage, and quota ledger
  - Account region: YouTube account connection status, re-authenticate option
  - Retention playlists region: designate which playlists represent the user's actual keep signal (rotation, favorites, curated)
  - Duplicate detection region: fuzzy matching threshold configuration
  - Data region: historical corpus import/export, data management
  - Danger zone region: disconnect account
- **Components:** Schedule form, quota display with ledger breakdown, account status card, retention playlist selector (multi-select from user's YouTube playlists with "Add" to designate), threshold slider with numeric display and explanation, import/export controls, danger action buttons
- **Retention playlist semantics:** Only playlists explicitly designated by the user count as retention signals. Membership caused by YuHaul routing actions does not itself indicate retention — the user must designate a playlist as a retention/rotation target. The advisory system and channel usefulness metrics reference these designated playlists when calculating retention rates.
- **Duplicate detection threshold:** A numeric input or slider (range 50%–100%, default 80%) controlling the fuzzy matching threshold for title similarity. Below the slider, helper text explains: "Titles with similarity above this threshold are considered fuzzy duplicates. Higher values require closer matches. Exact identifier matches are always detected regardless of this setting." The threshold is global — it applies to all duplicate detection across the system. Current value is displayed and persisted on change. No separate "Apply" needed — value saves on change with a brief toast confirmation.

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

The quota ledger provides visibility into API consumption, fulfilling AC 18 ("External API consumption accounted for and visible").

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
- not-yet-evaluated: gray badge with dash icon (only for videos where execution did not reach routing — never for videos skipped by a final route match or exclusion)
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
- "Dry Run" on click: confirmation dialog (see Flow 3), on confirm: starts dry-run, subscribes to SSE stream, shows live progress, navigates to dry-run results

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

**Video outcome list in Run Detail:** On mobile, each video row converts to a card layout with stacked fields (title, channel, outcome badge, route match summary). On desktop, full table columns are visible. Cards preserve scanability while adapting to narrow viewports.

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
- Rationale: A single-user personal tool inherits the user's system preference automatically. Adding a toggle requires persisting the choice (localStorage or backend), generating two complete token sets, and testing both themes — non-trivial implementation cost for a feature the OS already provides. The system preference approach is zero-configuration and always correct.
- Alternatives considered: Manual toggle with localStorage persistence — possible as a future enhancement but adds implementation complexity without proportional user benefit for a single-user tool. Dark-mode-only — rejected because some users prefer light mode and the OS preference approach supports both.
- Trade-off accepted: User cannot override OS preference within the app. Acceptable because OS-level theme switching is immediate and system-wide.

**DD-4: SPA with URL reflecting all meaningful state**
- Decision: Single-page application where every navigable state has a URL. Back button works. New tab opens same view. Links are shareable.
- Rationale: Users bookmark frequently accessed views (specific route editor, channel analysis page). Users share links to specific runs or channels. SPA provides smoother transitions between related views.
- Alternatives considered: MPA (multi-page app) — rejected because the interface has many cross-cutting views (search results, decision detail from multiple entry points) where SPA's client-side routing provides smoother transitions. Hash-based routing — rejected because URLs are less readable and less shareable.
- Trade-off accepted: SPA requires explicit focus management on route changes (handled in Focus Behavior section) and careful state management to avoid stale data.

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
- Rationale: The anchor specifies "configurable conditions" without requiring nesting. For v1, conditions are typically 2–4 simple rules (title contains X, duration > Y, channel equals Z). Flat list with AND/OR covers the common case with minimal UI complexity. Nested groups add significant UI complexity (indentation, group management, mixed operators) for a use case that may not arise in practice.
- Alternatives considered: Nested condition groups with AND/OR at group level — deferred to v2 if user needs arise. Visual query builder — rejected as over-engineered for v1.
- Trade-off accepted: Users cannot express complex boolean logic like "(A AND B) OR (C AND D)" in v1. If this need arises, it becomes a v2 enhancement informed by real usage.

**DD-7b: Route-scoped exclusions as a separate region in Route Editor**
- Decision: Exclusions are a dedicated region in the Route Editor, visually distinct from conditions. Each exclusion type (channel, video, title, word/phrase, duration) is a separate list with add/remove controls. Exclusions apply per-route — a video excluded from Route A remains eligible for Route B.
- Rationale: Exclusions are a different semantic construct from conditions. Conditions define what a route accepts; exclusions define what a route explicitly rejects before condition evaluation. Separating them makes the route's behavior legible: "evaluate conditions, but skip these first." Per-route scoping matches the exclusion-behaviors requirement (AC 6: excluded video remains eligible for other routes).
- Alternatives considered: Exclusions as a special condition type (e.g., "title NOT contains") — rejected because exclusions run before condition evaluation and have different semantics (exclusion prevents evaluation entirely; condition mismatch just means no match). Global exclusions — rejected because the requirement specifies per-route scoping.
- Trade-off accepted: More UI elements in the Route Editor. Acceptable because the single-column layout accommodates it and the separation makes route behavior clearer.

**DD-8: Mobile tables convert to cards**
- Decision: On viewports < 768px, data tables convert to card layout where each row becomes a stacked card with essential fields visible.
- Rationale: Cards preserve scanability on narrow viewports by presenting each item as a discrete unit with stacked fields. This is more mobile-native than column reduction and avoids the poor UX of horizontal scrolling on touch devices.
- Alternatives considered: Reduced columns with expand — deferred as an enhancement; cards are the default because they work without interaction. Horizontal scroll — rejected because horizontal scrolling on mobile is poor UX.
- Trade-off accepted: Cards show fewer items per viewport than a table. Acceptable because mobile use is typically scanning (checking status) rather than deep analysis.

**DD-9: Three-layer video outcome model**
- Decision: Video outcomes are modeled as three independent layers: processing state, per-route decisions, and per-action execution. Summary badges derive from the layers but are not the authoritative model.
- Rationale: A single outcome badge cannot represent the full reality of routing. A video can be accepted by one route and fail on another, or be accepted by a route whose action fails. The three layers make this visible and debuggable.
- Alternatives considered: Single outcome badge only — rejected because it hides actionable information (e.g., "accepted" when one action failed). Separate views per layer — rejected because it fragments information; layers are presented together in Decision Detail.
- Trade-off accepted: More complex data model. Acceptable because the complexity reflects reality and the UI presents it progressively (summary badge → full detail on click).

**DD-10: Dry-run, backtest, and replay as distinct operations**
- Decision: Dry-run processes current pending workload with real routing semantics but no external mutations. Backtest replays retained historical observations against hypothetical configuration. Replay reprocesses retained historical observations through a selected (current or historical) configuration. All three protect production state.
- Rationale: These serve different user needs. Dry-run answers "what would happen if I ran now?" Backtest answers "what would have happened if my routes were different?" Replay answers "what would this specific historical configuration have produced?" Conflating them creates confusion about what state is affected and what outcomes are production vs. hypothetical.
- Alternatives considered: Unified "test mode" — rejected because the operations have different inputs (current pending vs. historical observations), different configuration sources (draft proposed vs. selected historical), different outputs (current outcomes vs. comparison vs. replay results), and different protection requirements.
- Trade-off accepted: Three separate UIs/modes instead of one. Acceptable because the workflows are genuinely different and users need to understand which they're running and whether outcomes are production, replay, or backtest.

**DD-11: Global duplicate threshold in Settings, not per-route**
- Decision: The fuzzy matching threshold for duplicate detection is a single global setting in Settings, not a per-route configuration.
- Rationale: Duplicate detection operates against the duplicate-reference corpus (videos routed to playlists), which is shared across all routes. A per-route threshold would create inconsistency — the same video pair could be a duplicate under Route A but not under Route B, depending on which route processed them. A global threshold ensures consistent duplicate detection regardless of which route matched. The requirement (AC 3) states "adjustable by the user" without implying per-route scope.
- Alternatives considered: Per-route threshold — rejected because duplicate detection is a corpus-level operation, not a route-level operation. The corpus is shared; the threshold should be too.
- Trade-off accepted: User cannot tune duplicate sensitivity per genre or content type. Acceptable because a single threshold covers the common case, and the user can adjust it globally when their content mix changes.

**DD-13: SSE for real-time run progress transport**
- Decision: Run progress is delivered via Server-Sent Events (SSE) through a REST endpoint (`/api/runs/:id/stream`). The frontend subscribes and receives real-time events for processing state transitions, per-video outcomes, and quota updates.
- Rationale: Runs may take minutes. Users need immediate feedback on progress — polling adds latency and unnecessary load. SSE is unidirectional (server→client only), which matches the progress use case. It has native browser support (`EventSource`), auto-reconnects on connection loss, and works over standard HTTP with no special server requirements. WebSocket was considered but adds bidirectional complexity that isn't needed here.
- Alternatives considered: Polling (adds 1–5s latency, wastes requests on fast runs), no real-time progress (poor UX for multi-minute runs), WebSocket (over-engineered for unidirectional progress).
- Trade-off accepted: SSE requires server-side streaming support, but this is well-supported by Go's `http.Flusher` interface and standard SSE libraries. The architect has already designed the endpoint.

**DD-12: Retention provenance as progressive disclosure, not a filter**
- Decision: Retention signal provenance (user-driven vs. YuHaul-routed) is shown as a summary line and per-video badges in the channel detail view, not as a filter on the channel list.
- Rationale: The channel list needs to remain scannable — the retained count is the primary signal for usefulness assessment. Provenance breakdown is investigative detail needed when the user is evaluating a specific channel's value. Showing it at list level would clutter the scan path. The summary line in channel detail ("12 retained: 8 user-driven, 4 YuHaul-routed") gives the user the answer immediately; per-video badges let them inspect individual cases.
- Alternatives considered: Filter by provenance on channel list — rejected because it adds UI complexity to the primary scan view for a use case that arises during investigation, not scanning. Separate provenance tab — rejected because it fragments information that belongs in context with the channel's other metrics.
- Trade-off accepted: User must click into a channel to see provenance breakdown. Acceptable because provenance inspection is an investigative action, not a daily scan action.

### Open Design Questions

**OQ-1: Chart library selection**
- Option A: Lightweight charting library (e.g., Chart.js, uPlot) — smaller bundle, simpler API, sufficient for line/bar charts
- Option B: Full-featured library (e.g., ECharts, Recharts) — more chart types, interactive features, larger bundle
- Option C: Pure SVG/D3 approach — maximum flexibility, highest implementation cost
- Deciding criterion: The anchor says "no particular charting framework requirement." Implementation complexity vs. feature needs. For v1, line charts (trends) and bar charts (comparisons) are sufficient. Recommend Option A for simplicity, but this is an architecture decision.
- Owner: Architect (after design spec is accepted)
- Blocking: No — Coder can start with placeholder chart components and swap library later

**OQ-2: Real-time run progress display** — RESOLVED
- **Resolution:** SSE (Server-Sent Events) via REST + SSE with `/api/runs/:id/stream` endpoint.
- Architect has designed this as the transport. The frontend subscribes to the SSE stream and receives real-time run progress events (processing state, per-video outcomes as they complete, quota consumption updates).
- Rationale: SSE is simpler than WebSocket (unidirectional server→client is sufficient for progress), has native browser support with `EventSource`, auto-reconnects on connection loss, and works over standard HTTP. No polling overhead, no connection management complexity.
- Impact on design: Progress indicator in run views (Run Detail, dry-run, backtest, replay) is driven by SSE events, not polling. The UX stays the same — user sees a progress indicator — but the transport is instant and efficient.

**OQ-3: Quota bucket awareness**
- Option A: Display raw quota numbers (units used / units remaining) — precise but may be confusing if bucket details are unknown
- Option B: Display relative status (green/amber/red bar) with raw numbers on hover/tap — simpler at a glance, detailed on demand
- Option C: Display bucket-specific information (reset time, bucket size) alongside raw numbers — most informative but requires YouTube API quota endpoint availability
- Deciding criterion: How much quota information does the YouTube API actually expose? If bucket details are available, Option C. If only raw consumption is available, Option A or B.
- Owner: Architect (after API investigation)
- Default: Option A (raw numbers) for v1, with Option B as enhancement
- Blocking: No — design specifies what to display, implementation determines what's available

---

*This spec is a behavioral contract. It specifies what the system must do, for whom, under what conditions — expressed in terms each downstream agent can verify. Visual appearance decisions are separated in the Visual Semantics section. The behavioral specification is independently implementable without visual assets.*
