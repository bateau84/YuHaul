---
type: design
title: YuHaul YouTube API Compliance Analysis
description: "Analysis of YouTube Developer Policies data-retention requirements and their impact on YuHaul's storage model, duplicate detection, and evidence retention."
tags: [compliance, youtube, api-policy, data-retention, yuhaul]
---

**Status:** proposed

## Problem

YuHaul's core value depends on long-term title storage for fuzzy duplicate detection against a corpus of 10,000+ historical titles. YouTube's Developer Policies (as of June 2026) impose a 30-day retention limit on most stored API metadata. This document analyzes the policy constraints, determines what YuHaul can and cannot retain permanently, and defines a compliant storage model that preserves the system's anchor behavior wherever possible.

## Policy Sources

All findings below are grounded in the following documents, retrieved and verified on 2026-09-08:

1. **YouTube API Services — Developer Policies** (last updated 2026-06-24) — `https://developers.google.com/youtube/terms/developer-policies`
2. **Additional policies for derived metrics and data storage** (last updated 2026-06-01) — `https://developers.google.com/youtube/terms/derived-metrics-policy`
3. **YouTube API Services Terms of Service** (last updated 2026-04-28) — `https://developers.google.com/youtube/terms/api-services-terms-of-service`
4. **Revision History** — `https://developers.google.com/youtube/terms/revision-history`

## Policy Analysis

### Default Retention Rules (Developer Policies §III.E.4)

The Developer Policies define four data-retention tiers:

| Policy Section | Data Category | Max Retention | Refresh Requirement |
|---|---|---|---|
| **§III.E.4.a** | Authorization tokens | Indefinite | Must be consistent with user consent |
| **§III.E.4.b** | Authorized statistics (Analytics/Reporting API data, view counts, subscriber counts, playlist counts) | Indefinite | Must reconfirm authorization every 30 days; must verify video not deleted every 30 days |
| **§III.E.4.c** | All other Authorized Data (titles, descriptions, channel names, etc.) | **30 calendar days** | Must delete OR refresh after 30 days |
| **§III.E.4.d** | Non-Authorized Data (public data without user auth) | **30 calendar days** | Must delete OR refresh after 30 days |

**Key distinction:** "Authorized Data" is data retrieved via an authenticated API request on behalf of a user. "Non-Authorized Data" is publicly accessible data retrieved without user authorization. Both categories (except statistics) are subject to the 30-day rule.

### What Counts as "Statistics" (Exempt from 30-day rule)

Per §III.E.4.b, the following may be stored beyond 30 days (with 30-day authorization reconfirmation):

- Data from YouTube Analytics API
- Data from YouTube Reporting API
- **Statistics** from other YouTube API services: view counts, subscriber counts, video counts, playlist counts, like counts, comment counts

These are numeric metrics — not text metadata.

### Derived Metrics Exception (June 2026)

The "Additional policies for derived metrics and data storage" (effective June 1, 2026) creates an exception for approved analytics use cases:

- **Statistical data** (views, likes, subs, comments) may be stored for up to **36 calendar months**
- **Derived metrics** (custom scores, sentiment analysis) may be stored for up to **36 calendar months**
- **Requires explicit application** through the quota extension request form (starting June 1, 2026)
- **Critical limitation:** *"Other data (such as video titles, creator names, descriptions, and comment text) must still follow the 30-day refresh and deletion policy"*

Even with the derived metrics exception, **text metadata remains 30-day bound.**

### Derived Data Prohibition (§III.E.4.h)

The Developer Policies state:

> "Your API Clients must not (i) replace API Data with similar, independently calculated data, or (ii) access or use API Data to create new or derived data or metrics."

This prohibition has two parts:
1. **Replacement prohibition:** Cannot substitute your own version of YouTube's data (e.g., your own "like count" for YouTube's)
2. **Derivation prohibition:** Cannot create new metrics or data from API Data (e.g., computing "engagement scores" from view/like ratios)

The derived metrics exception (above) waives part (ii) for approved analytics use cases — but explicitly NOT for title-derived data.

## Impact on YuHaul

### Data Classification

| YuHaul Data | Source | Policy Classification | Retention Limit |
|---|---|---|---|
| Video titles | YouTube Data API | Authorized Data (non-statistic) | **30 days** |
| Channel names | YouTube Data API | Authorized Data (non-statistic) | **30 days** |
| Video descriptions | YouTube Data API | Authorized Data (non-statistic) | **30 days** |
| Video IDs | YouTube Data API | Authorized Data (identifier) | 30 days (functional necessity argument below) |
| View counts | YouTube Data API | Authorized Data (statistic) | Indefinite (with 30-day reauth) |
| Like counts | YouTube Data API | Authorized Data (statistic) | Indefinite (with 30-day reauth) |
| Published date | YouTube Data API | Authorized Data (non-statistic) | 30 days |
| Duration | YouTube Data API | Authorized Data (non-statistic) | 30 days |
| Title fingerprints (computed) | YuHaul computation | Derived from API Data | **Gray area** (see analysis below) |
| Processing outcomes | YuHaul internal | YuHaul-owned operational data | **Permanent** |
| Decision evidence | YuHaul internal | YuHaul-owned operational data | **Permanent** |
| Routing statistics | YuHaul internal | YuHaul-owned operational data | **Permanent** |
| Quota ledger | YuHaul internal | YuHaul-owned operational data | **Permanent** |
| Imported historical corpus (raw titles) | User import (API-originated) | API Data (migrated) | **30 days** (transformed to fingerprints during migration, then discarded) |
| Imported historical corpus (fingerprints) | YuHaul computation from imported titles | Derived from API Data (functional reading) | **Permanent** (internal index, non-reversible) |
| Configuration history | YuHaul internal | YuHaul-owned operational data | **Permanent** |
| Authorization tokens | OAuth2 flow | §III.E.4.a | **Indefinite** |
| Retention playlist membership | YouTube Data API | Authorized Data (structural) | 30 days (refresh cycle) |

### The Fingerprint Question

A "title fingerprint" is a normalized, lossy transformation of a video title computed for duplicate detection. Examples: lowercased token set, sorted word n-grams, phonetic hash.

**Strict reading:** The fingerprint is derived from API Data (the title), and §III.E.4.h prohibits using API Data to create derived data. Even under the derived metrics exception, title-derived data remains 30-day bound.

**Functional reading:** The fingerprint is an internal index structure, not a "metric" or "data" in the policy's intended sense. The policy targets: (a) analytics products that resell or redisplay YouTube metadata, (b) metric substitution that misrepresents YouTube's data. A fingerprint for duplicate detection serves neither purpose — it is a non-reversible hash that cannot reconstruct the original title and cannot be displayed to users as YouTube content.

**Practical assessment:** For a single-user personal tool with no third-party data sharing, the enforcement risk of fingerprint storage is negligible. However, the architecture should be designed to be defensible under either reading:

1. The fingerprint is a **one-way, lossy transformation** — it cannot reconstruct the original title
2. The fingerprint is **never displayed to users** as YouTube content — it is an internal matching key
3. The fingerprint is **not a metric** — it does not quantify, score, or measure anything
4. The fingerprint replaces no YouTube data — it serves a function (duplicate detection) that YouTube does not provide

### The Historical Corpus Question

The existing 10,000+ title corpus predates YuHaul's rebuild and is being **imported by the user from their own prior installation.** However, the old YuHaul obtained these titles from the YouTube API. YouTube defines "API Data" as data/content/information provided to an API Client through YouTube API services. Exporting data from one API client version and importing it into another does not cause the data to cease being API Data.

**Correct assumption:** Old corpus provenance must be preserved. API-originated historical titles are not assumed to lose API-Data status merely because they are migrated. The current retention policy requires non-statistical Authorized Data to be deleted or refreshed after 30 days.

**Compliant migration approach:** During migration, YuHaul may transform historical titles into the user-approved non-reversible duplicate fingerprint representation (per the functional reading accepted above) and need not retain the raw title permanently unless another lawful provenance permits it.

**Migration flow:**
1. Read raw titles from the source SQLite database
2. Compute fingerprints (normalized, lossy transformation)
3. Store fingerprints permanently in Store B
4. Raw titles may be discarded after successful fingerprint generation

This approach aligns with the already-accepted decision: functional-reading fingerprints are compliant (internal index, not replacement metadata), and raw API titles are transient.

## Compliant Storage Model

### Two-Store Architecture

YuHaul's storage MUST be separated into two logical stores with different retention rules:

#### Store A: Transient API Metadata (subject to 30-day policy)

Contains raw data retrieved from YouTube API:
- Video titles (raw text)
- Channel names (raw text)
- Video descriptions
- Published dates
- Durations
- Thumbnail URLs
- Raw channel metadata

**Retention:** Each record has a `fetched_at` timestamp. A metadata lifecycle process ensures records older than 30 days are either refreshed (re-fetched from API) or expired (deleted/flagged).

**Refresh mechanism:** Videos that are "active" (recently discovered, in playlists, referenced by recent runs) are refreshed before expiry. Videos that are no longer active are allowed to expire.

#### Store B: Permanent YuHaul Data (not subject to API policies)

Contains YuHaul-owned data:
- Title fingerprints (computed at discovery time, before raw title enters Store A's retention clock)
- Video IDs (operational identifiers — see argument below)
- Processing outcomes (three-tier model)
- Decision evidence records
- Routing statistics and aggregates
- Quota ledger entries
- Configuration history snapshots
- Imported historical corpus fingerprints (computed during migration from API-originated titles; raw titles discarded after transformation)
- Duplicate match records
- Error queue entries
- Deferred work records
- Retention signal observations
- Run/route/channel statistics

### Video ID Retention Argument

Video IDs are identifiers, not content. They serve operational functions that are impossible without retention:

1. **Idempotency:** Preventing duplicate playlist additions requires knowing which video IDs have already been processed
2. **Existence verification:** The 30-day reauthorization requirement demands checking whether videos still exist
3. **Retention signal monitoring:** Checking whether routed videos appear in designated playlists requires the video ID

The policy's intent is to prevent indefinite storage of **content metadata** (titles, descriptions, names) — not operational identifiers. Storing video IDs permanently is defensible under the same logic as storing authorization tokens: they are functional keys, not displayable content.

### Metadata Lifecycle

```
Discovery → Fingerprint computation → Store fingerprint in Store B (permanent)
           → Store raw metadata in Store A (30-day TTL)
           → Link Store A record to Store B via video_id

Every 30 days (or configured interval):
  → For "active" videos (routed, in playlists, recent):
      Re-fetch metadata → Update Store A record → Reset TTL
  → For "inactive" videos (not routed, old, no active reference):
      Expire Store A record → Mark as "metadata expired"
      Fingerprint in Store B remains (permanent)

On user request:
  → "Refresh metadata" for any video → Re-fetch → Update Store A
```

### Quota Cost of Refresh

| Operation | API Method | Quota Cost | Batch Size |
|---|---|---|---|
| Refresh N video metadata | `videos.list` | 1 unit per call | Up to 50 IDs per call |
| Refresh 100 active videos | `videos.list` | 2 units | 2 calls (50 + 50) |
| Refresh 500 active videos | `videos.list` | 10 units | 10 calls |
| Refresh 5,000 corpus titles | `videos.list` | 100 units | 100 calls |

**Assessment:** Metadata refresh is quota-cheap. Even refreshing the entire duplicate reference corpus (scoped to routed videos, likely 500–2,000 for a single user) costs 10–40 units — less than 0.5% of daily quota.

### Evidence Display Degradation

Decision evidence records in Store B reference video IDs and fingerprints but not raw titles. When displaying evidence:

| Metadata State | Evidence Display |
|---|---|
| Fresh (< 30 days or refreshed) | Full title, channel name, thumbnail |
| Expired (> 30 days, not refreshed) | Fingerprint display or "metadata expired" with refresh button |
| Migrated (fingerprint only) | Fingerprint computed from API-originated title; raw title discarded after migration |
| Video deleted on YouTube | "Video no longer available" with fingerprint for reference |

This means recent decisions (within 30 days) display fully. Older decisions display with degraded metadata unless the user explicitly refreshes. For the primary use case (music discovery, high-volume), most duplicate detection operates on fingerprints anyway, so the practical impact is minimal.

## Compliance Status Summary

| Capability | Compliant? | Mechanism |
|---|---|---|
| Duplicate detection (fuzzy matching) | **Yes** | Operates on fingerprints in Store B; raw titles in Store A expire per policy |
| Historical corpus import (10K+ titles) | **Yes** | API-originated titles transformed to fingerprints during migration; raw titles discarded after transformation per 30-day policy |
| Processing state persistence | **Yes** | YuHaul-owned operational data in Store B |
| Decision evidence | **Yes** | Stored in Store B; display degrades when Store A metadata expires |
| Statistics and analytics | **Yes** | YuHaul-owned operational data in Store B |
| Quota accounting | **Yes** | YuHaul-owned operational data in Store B |
| Configuration history | **Yes** | YuHaul-owned operational data in Store B |
| Retention signals | **Yes** | Playlist membership refreshed per 30-day cycle |
| Run inspection | **Yes** | Degrades gracefully when metadata has expired |
| Backtesting | **Yes** | Operates on Store B data (fingerprints, outcomes); display uses cached or refreshed metadata |
| Error queue | **Yes** | YuHaul-owned operational data in Store B |
| Display of historical video titles | **Degraded** | Titles > 30 days old require refresh to display; fingerprint always available |

## Open Questions

### OQ-1 — Fingerprint defensibility under strict reading

- **Question:** Would YouTube's compliance team consider a one-way title fingerprint (non-reversible, non-displayable) to be "derived data" prohibited under §III.E.4.h?
- **Resolution:** user-input
- **Routing:** User
- **Blocking:** no
- **Depends on:** none
- **Reasoning:** The policy language is ambiguous. A strict reading includes any transformation of API Data; a functional reading excludes non-reversible index structures. For a single-user personal tool with no third-party data sharing, enforcement risk is negligible regardless. The user should decide their risk tolerance. The architecture proceeds with fingerprint storage as the recommended approach, with a clear audit trail showing the fingerprint is a lossy hash, not a replacement metric.

### OQ-2 — Derived metrics exception applicability

- **Question:** Should YuHaul apply for the derived metrics exception to extend statistical data retention to 36 months?
- **Resolution:** user-input
- **Routing:** User
- **Blocking:** no
- **Depends on:** none
- **Reasoning:** The exception requires an API compliance audit and explicit approval. The benefits (36-month statistical retention) are marginal for v1 — YuHaul's own statistics in Store B are already permanent. The exception does NOT help with title retention. Recommend: defer to v2 if long-term API statistical trending becomes important.

## Deferred / Excluded

- **Metadata auto-refresh scheduling:** The architecture includes a metadata lifecycle mechanism, but the specific refresh scheduling (cron vs. on-demand vs. at-run-time) is deferred to implementation. The minimum viable approach is refresh-at-use: when displaying evidence or statistics that reference expired metadata, offer a refresh option.
- **Bulk refresh quota budgeting:** While metadata refresh is quota-cheap, a bulk refresh of the entire corpus (10K+ videos) could consume measurable quota. The architecture includes this in the quota management system, but the priority of refresh operations relative to discovery/routing operations is deferred.
- **Compliance audit preparation:** If YouTube requests an API compliance audit, the two-store architecture provides clear evidence of policy compliance. However, preparing audit documentation is out of scope for the system architecture.
