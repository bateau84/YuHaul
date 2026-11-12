---
type: quality-scenario
title: QS-001 — Video Search Response Time at 100,000+ Videos
description: With 150,000 known videos that all hold a title and 150,000 imported references, ID lookup answers within 1 second and each name-search page within 2 seconds (95th percentile), also while a production run is active.
tags: [quality-scenario, videos, search, performance, priority:MUST]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

When the user searches on the Videos page ([BR-038](br-038-video-search-by-title-or-id.md)) in a YuHaul installation that holds about 100,000 or more searchable videos, the system SHALL answer quickly enough for interactive use: a video ID or link within 1 second, and each page of name-search results within 2 seconds. This SHALL also hold while a production run is active.

## Acceptance criteria

The *load* is: 150,000 known videos, all 150,000 with a held title, and 150,000 imported references, on the production deployment host or one of equal capacity. The *response time* is measured from the moment the server receives the search or page request to the moment it has sent the complete response.

1. **ID lookup.** For video IDs and recognized YouTube links (BR-038 AC4 and AC6), found and not found, the 95th-percentile response time SHALL be at most 1 second.
2. **Name search.** For name searches (BR-038 AC7) whose match count ranges from 0 to 20,000, the 95th-percentile response time for the first page, and separately for a page at position 100 or later, SHALL be at most 2 seconds.
3. **During a run.** With a production run active and processing videos, the limits in AC1–2 SHALL still hold, and the run SHALL lose no work and commit no outcome that it would not commit without the searches.
4. **Correctness under load.** No answer counted toward AC1–3 may be a time-out, an error or an incomplete result. A faster answer that omits a match does not pass.

## Verification semantics

Seed the load with generated titles, including common words that match up to 20,000 titles and rare strings that match 0 or 1. Send at least 200 requests per AC, one at a time (the product has a single user), and record each response time on the server. Compute the 95th percentile for each AC separately. For AC3, repeat while a production run against a controlled YouTube provider processes at least 1,000 videos, and compare its committed outcomes with an identical run made without searches. For AC4, compare the result set of each name search with a direct scan of the held titles.

## Derived from

- [BR-038](br-038-video-search-by-title-or-id.md) AC17, and the user's 2026-11-11 assignment, which asked for a performance expectation for about 100,000+ videos. The original human conversation was not independently inspected.
- **Load, user decision of 2026-11-11:** 150,000 held titles, relayed in the requirements assignment (Loom Action b820f6ca, Plan revision 19); the original human conversation was not independently inspected. This replaces the first draft's 50,000 held titles. Only known videos can hold a title (imported references hold none, BR-038 Definitions), so the load is 150,000 known videos that all hold a title. Keeping 150,000 known videos and 150,000 imported references unchanged is this specification's reading of the decision.
- **Specification choices**, which the user may change: 1 second for ID lookup, 2 seconds per name-search page, the 95th percentile and one request at a time. The scale follows the 2026-11-06 runs, which discovered 22,067 and 62,866 videos ([BR-037](br-037-requeue-stranded-videos.md) Example). The limits are common thresholds for an interactive lookup and a list page.
- AC3 is a necessary consequence: search must be read only (BR-038 AC15), so it must not change what a run does.

## Depends on

- [BR-038](br-038-video-search-by-title-or-id.md)
