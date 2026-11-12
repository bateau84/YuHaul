---
type: specification
title: ARS-001 — Video Search — HTTP API, Input Classification, Matcher, Keyset Paging, Read Queries and Tests
description: Exact contract for BR-038 and QS-001. GET /api/videos/search classifies the input (empty, too long, link, video ID, too short, name) and returns a tagged body; GET /api/videos/{videoId} returns the video view. Name search takes candidates from an FTS5 trigram index over Go-folded keys of held titles (migration 029, secure-delete, purge triggers, stable document map, reconcile at open and at the sweep), or from a full scan when the key is shorter than 3 code points, and decides every candidate in Go with NFC plus full case folding and the held rule; results use an opaque keyset cursor and total counts only re-checked matches. The view joins outcome, queue state and per-run decisions, with simulation kept apart. No YouTube call, no write by search. Includes the conformance, retention-proof and QS-001 timing tests.
tags: [architecture, specification, videos, search, api, retention, sqlite, fts5, migration, paging, unicode, performance, simulation]
timestamp: 2026-11-12T00:00:00Z
---

**Status:** accepted. This is the exact implementation contract.

**Revised 2026-11-12: option B failed its gate.** P2 measured a name-search p95 of about 2.8–3.0 s, and P1 a typed-not-found p95 of about 1.5 s, at 150,000 held titles. [ADR-001](../decisions/adr-001-held-title-search-by-request-time-scan.md) therefore now selects option C. Name-search candidates come from the FTS5 trigram index in §10, and Go re-checks every candidate. This revision changes §1.1–§1.3, §2, §6.1, §7.3, §8, §9 and the new §10.

What is implemented, as of 2026-11-12:

- **Done:** the pure code, metadata reads, `Service`, both handlers and tests T1–T26, T30 and T31 (option B scan path). A server test checks the SPA data shape against `web/src/types.ts`. The P1–P4 harness exists (commit `e4cc1f7`).
- **Not built:** §10, which includes the write-cost spike (§10.10), migration 029, the writer changes, the batched sweep, the reconcile and recreate, and tests R1–R7, D1, D2 and F-R.
- **Revised 2026-11-12 after critique (CHANGES REQUIRED, session `ses_e2e61920affeUTLBU835PmTm85`):** migration renumbered to 029; `BEFORE INSERT` purge trigger for `REPLACE`; write-cost spike, batched expiry, P3 write-side criteria and drop-and-recreate rebuild; damaged-index and rollback procedures; write-lock-first reconcile; a precise statement of what R1 proves; key version; per-stage P2 timing.
- **Not passing or not run:** the P1–P4 gate must be rerun on the indexed path. SPA test T32 has not run.

Dependencies:

- This contract realizes ADR-001.
- It depends on [run-initiation ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) for the predicate `queue.RemovedByUser` (§2.3), the indexes `deferred_item_video_status` and `error_entry_video_status` (migration 026), and the stored `unavailable_on_youtube` outcome (§1.3).
- Migration 029 follows BR-039's migrations 027 and 028 ([run-initiation ARS-006](../../run-initiation/specifications/ars-006-final-outcomes-are-never-reprocessed.md)). Search releases only after BR-039's 028.

Earlier history:

- Revised 2026-11-11 after critique: identity outcome mapping, a 150,000-held-title speed gate, index ownership and the access-log advisory.
- Reconciled 2026-11-11 (Plan revision 19) with the updated BR-038 and QS-001: the page carries the AC18 `total`, positions and page count; `page=n` jumps are added beside the cursors; scheme-less links stay unrecognized (Q1 closed); the timing seed equals QS-001's new load.

## What the two sides agree on

The browser sends whatever the user typed. The server alone decides what the text means, so tests on the real server path cover every input rule. The server answers with a `kind`, and the SPA shows the matching message or result. The server never calls YouTube and never writes.

**Example (success).** `GET /api/videos/search?q=lofi%20beats` → `200 {"kind":"results","results":{"items":[…3 items…],"total":3,"next_cursor":null,"prev_cursor":null,"page_size":50,…}}`. The SPA lists 3 videos. Inside the server:

1. The key `lofi beats` (10 code points) is sent as the FTS5 phrase `"lofi beats"`, which returns 4 candidates.
2. One of them expired at 09:00, but the sweep has not run yet, so `metadata.Held` drops it.
3. The other 3 pass `Contains`, so `total` is 3.

**Example (link).** `GET /api/videos/search?q=https%3A%2F%2Fyoutu.be%2FdQw4w9WgXcQ%3Ft%3D42` → `200 {"kind":"video","video_id":"dQw4w9WgXcQ","from_link":true}`. The SPA replaces the URL with `/videos/dQw4w9WgXcQ`, which calls `GET /api/videos/dQw4w9WgXcQ` → `200` with `title.availability: "expired"` and the full outcome and run history.

**Example (refusal).** `q=lo` → `200 {"kind":"too_short"}`. `q=https://www.youtube.com/watch?list=PL123` → `200 {"kind":"unusable_link"}`.

## Derived from

- [BR-038](../../../requirements/yuhaul/br-038-video-search-by-title-or-id.md), [QS-001](../../../requirements/yuhaul/qs-001-video-search-response-time.md) and [ADR-001](../decisions/adr-001-held-title-search-by-request-time-scan.md).
- Source read this session:
  - schema in migrations 001, 002, 014 and 015, and the migration runner and Go backfill in `internal/store/store.go`;
  - every `transient_metadata` writer: `metadata.Store.Upsert`, `Refresh`, `Expire`, `CleanupExpired`, and `discovery.Engine.upsertTransientMetadata` (`internal/discovery/engine.go` lines 484–508);
  - the sweep: `metadata.Manager.RunLifecycle` → `ExpireStale`, called once at the start of each production run (`internal/pipeline/pipeline.go` line 408); on failure it logs a warning;
  - the implemented `internal/videosearch` (`store.go`, `service.go`, `match.go`);
  - `metadata.Manager.ReadLocalDisplay` and `metadata.Store` (`Expire`, `Refresh`, `CleanupExpired`);
  - `state.runVideoInspection` and `GetVideoByExternalIDInRun` (`internal/state/store.go`);
  - `state.Store.recordRunVideoOutcome`, which accepts only `run_type IN ('manual','scheduled')`;
  - `server.writeError`, which writes `{"error": message}`;
  - the route table in `internal/server/server.go`;
  - the SPA routes in `web/src/App.tsx` (decision detail is `/runs/:id/video/:videoId`).

## Satisfies

| BR-038 / QS-001 | Where |
|---|---|
| AC1 (page, access, desktop and mobile) | §2 routes sit behind the same ingress as all `/api/*`; §8.4 SPA routes. Layout belongs to the Designer. |
| AC2 coverage notice, AC10 no-result text | §8.4: static SPA copy, shown without a request. The `results` body with no items triggers AC10's text. |
| AC3–AC9 input classes | §3 classification order and §4.1 kinds. |
| AC6 case-sensitive ID | §6.2 binary `=` on `video.video_id` and `title_fingerprint.video_id` (no `COLLATE` in the schema). |
| AC7 matching | §5. |
| AC11 order and paging, AC18 total and positions | §4.1 Page, §7. `total` is the match count from the same scan that cuts the page. |
| AC12 view fields | §4.3–§4.4, §6.3–§6.6. |
| AC13 simulation labelled | §4.4 `mode`, §6.4 `simulation_only`. Latest outcome and queue state read only production tables (§6.5–§6.6). |
| AC14 no YouTube call | §8.1 dependency rule, test T30. |
| AC15 read only | §8.2, test T31. |
| AC16 no new retention | ADR-001 constraints 1–6; §6.1 cutoff and Go re-check; §10 index lifecycle (purge triggers, secure-delete, stable key, reconcile); `Cache-Control: no-store`; §8.4 no client persistence; tests T19–T21 and R1–R7. |
| AC17, QS-001 AC1–AC4 | §10 index path; §9.2, tests P1–P4 rerun as the gate; D1 (the index path never misses a scan-path match). |

## Scope

In scope: two read endpoints, the classifier, the matcher, the cursor, the read queries, the SPA routes and data rules, and the tests.

Out of scope:

- Visual layout and copy. The Designer owns them; the `kind` values fix which message is needed.
- Matching fingerprints (excluded by BR-038).
- Any change to *when* metadata expires or is cleaned up. §10 adds index maintenance to the existing writers and the sweep, without changing their effect on `transient_metadata`.

## 1. Components

### 1.1 New package `internal/videosearch`

| File | Content |
|---|---|
| `classify.go` | `Classify(raw string) Input` (§3). Pure. |
| `match.go` | Removed: `Folder`, `NewFolder`, `Key` and `Contains` move without change to the new leaf package `internal/titlekey` (§1.2), because the metadata writers need the same `Key`. `videosearch` calls `titlekey`. |
| `cursor.go` | `encodeCursor` and `decodeCursor` (§7). Pure. |
| `store.go` | The read queries (§6), each inside a caller-supplied `*sql.Tx`. Implemented: `ScanMatches` (§6.1 scan path) and `ProjectMetadata` (§6.3 step 3). To add: the §6.1 indexed path. `ScanMatches` chooses the path by the §10.7 rule, and both paths share one filter (`Held` and `Contains`). They report title and channel availability as `held`, `expired`, `never_fetched` or `withheld`; the wire form (§4.2) maps `held` → `current_local`, `expired` → `expired`, and the other two → `unavailable`. |
| `service.go` | `Service.Search(ctx, raw string, p Paging) (SearchResult, error)` and `Service.View(ctx, videoID string) (VideoView, error)`. `Paging` carries the raw `cursor` and `page` values and whether each was sent. Each opens one `BeginTx(ctx, &sql.TxOptions{ReadOnly: true})`, runs every query in it, then rolls back. Kinds that need no lookup (`empty`, `invalid_text`, `too_long`, `unusable_link`, `too_short`) open no transaction. It takes `now func() time.Time` for tests. Errors: `ErrInvalidCursor`, `ErrInvalidPage`, `ErrInvalidVideoID`, `ErrNotFound`. The handlers (`internal/server/video_search_handlers.go`) map them to the §2 codes. |

### 1.2 Extractions (behavior-preserving)

- `metadata.LocalRow` and `metadata.ProjectLocal(row LocalRow, cutoff time.Time) LocalDisplay`: the per-row body of `ReadLocalDisplay`, moved without change. `ReadLocalDisplay` then calls it. `metadata.Held(row, cutoff) bool` is true exactly when `ProjectLocal(row, cutoff).TitleAvailability == "current_local"`.
- `metadata.ParseLocalTime`: the existing `parseLocalTime`, exported.
- New leaf package `internal/titlekey`: `Folder`, `NewFolder`, `(*Folder).Key`, `Key`, `Contains`, moved without change from `videosearch/match.go`. It imports only the standard library and `golang.org/x/text`.
- New `internal/metadata/searchindex.go` (§10.3–§10.4, §10.10): `Indexable`, `IndexTitleTx`, `ReconcileSearchIndex`, `SearchIndexReady`, `ErrSearchIndexVersion` and `IndexWriteBatch`. `metadata` gains an import of `titlekey`. `ExpireStale` calls `Expire` in batches (§10.10).
- `titlekey.Version()` (§10.6).
- New `store.RecreateSearchIndex` (§10.9), called by `store.Open` when the key version differs.
- **Outcome names are the stored values (identity).** Every outcome shown is the stored `final_outcome` text, unchanged. That includes `unavailable_on_youtube`, which revised ARS-005 stores natively in `video` and `run_video_outcome`. No error text or diag code is ever read to derive a category. A `failed` outcome whose message carries code `video_unavailable` stays `failed`.

### 1.3 Migrations

- **Search adds migration 029** (`029_title_search_index.up.sql`, §10.2). Search releases only after BR-039's migrations 027 and 028, which own 027 and 028; see §10.2.
- Migration 026 is owned by run-initiation ARS-005 (§1). It creates `deferred_item_video_status ON deferred_item(video_id, status)` and `error_entry_video_status ON error_entry(video_id, status)`, and it adds the final-outcome CHECK step that admits `unavailable_on_youtube`.
- ARS-005 §2.3 owns `queue.RemovedByUser`.

Build search on top of ARS-005's migration 026 and predicates, or in the same change. Without those indexes, §6.6 still gives the right answer but scans the queue tables.

### 1.4 Server

`internal/server/video_search_handlers.go`, registered next to the run routes:

```go
mux.HandleFunc("GET /api/videos/search", s.handleVideoSearch)
mux.HandleFunc("GET /api/videos/{videoId}", s.handleGetVideo)
```

`/api/videos/search` is a literal segment, so Go 1.22+ routing gives it priority over `{videoId}`. `search` is also not 11 characters, so it can never be a video ID.

## 2. HTTP endpoints

| Endpoint | Query or path | Success | Failure |
|---|---|---|---|
| `GET /api/videos/search` | `q` (optional, string); at most one of `cursor` (opaque) or `page` (positive integer) | `200` + §4.1 body | `400 {"error":"invalid_cursor"}` (§7.5); `400 {"error":"invalid_page"}` (§7.5); `500 {"error":"search_failed"}` |
| `GET /api/videos/{videoId}` | `videoId` path value | `200` + §4.3 body | `400 {"error":"invalid_video_id"}` when the value fails the §3 ID pattern; `404 {"error":"video_not_found"}`; `500 {"error":"video_view_failed"}` |

Rules for both:

- Header `Cache-Control: no-store` on every response.
- Only `GET`. A body is ignored. No other parameter is read.
- A cancelled request context aborts the scan. The service checks `ctx.Err()` at least every 1,000 candidate rows.
- Logs record the `kind`, the candidate path (`indexed` or `scan`, §10.7), the per-stage durations (§9.2), the candidate and match counts, and the duration. They never record `q`, a search key, a title, a channel name or a cursor.
- **Access-log advisory.** `q` travels in the URL, so Traefik or any other proxy access log may record it. `q` is text the user typed, not data from the YouTube API, so this creates no provider-data retention under AC16. The application cannot control proxy logs. The operator may drop query strings from access logs if they do not want search text kept there. A `GET` with `q` is kept because it makes `/videos?q=` linkable and Back and Forward work.

## 3. Input classification (`Classify`)

`raw` is the decoded `q` value; absent counts as `""`. Steps run in order, and the first that applies decides.

| # | Test | Result |
|---|---|---|
| 1 | `t := strings.TrimSpace(raw)`; `t == ""` | `empty` |
| 2 | `!utf8.ValidString(t)` | `invalid_text` |
| 3 | `utf8.RuneCountInString(t) > 200` | `too_long` |
| 4 | `t` matches `^[A-Za-z][A-Za-z0-9+.\-]*://` (it is a URL) | link rules below → `link_id(ID)` or `unusable_link` |
| 5 | `t` matches `^[A-Za-z0-9_-]{11}$` | `typed_id(t)` |
| 6 | `utf8.RuneCountInString(t) < 3` | `too_short` |
| 7 | otherwise | `name(t)` |

**Link rules (step 4).** Parse with `net/url.Parse(t)`. It is `link_id(ID)` only when **all** of these hold; otherwise it is `unusable_link`, including when parsing fails:

1. `strings.ToLower(u.Scheme)` is `http` or `https`.
2. `u.User == nil` and `u.Port() == ""`.
3. `h := strings.ToLower(u.Hostname())` is one of `youtube.com`, `www.youtube.com`, `m.youtube.com`, `youtu.be`.
4. Using `p := u.EscapedPath()`:
   - for `youtu.be`: `p` matches `^/([^/]+)/?$`, and the candidate is group 1;
   - for the other hosts, `p` is `/watch` or `/watch/`: `u.Query()["v"]` must have exactly one value, which is the candidate;
   - for the other hosts, `p` matches `^/shorts/([^/]+)/?$`: the candidate is group 1;
   - any other path: unusable.
5. The candidate matches `^[A-Za-z0-9_-]{11}$`.

The query string and fragment are otherwise ignored. Path matching is case-sensitive (`/Watch` is unusable). A link longer than 200 characters is `too_long` (step 3 runs first).

## 4. Response bodies

### 4.1 `GET /api/videos/search`

All bodies are JSON objects with a `kind` string.

| `kind` | Other members | Cause |
|---|---|---|
| `empty` | — | step 1 |
| `invalid_text` | — | step 2 |
| `too_long` | — | step 3 |
| `unusable_link` | — | step 4 |
| `too_short` | — | step 6 |
| `video` | `video_id` (string), `from_link` (bool) | steps 4–5, and §6.2 finds a searchable video |
| `id_not_found` | `video_id`, `from_link`, `results` (Page, present only when `from_link` is false) | steps 4–5, and §6.2 finds nothing. When typed, the same text is also name-searched (§6.1). |
| `results` | `results` (Page) | step 7 |

`Page`:

```json
{"items": [VideoSummary], "total": 130, "page_size": 50,
 "number": 2, "total_pages": 3, "first_position": 51, "last_position": 100,
 "next_cursor": "string|null", "prev_cursor": "string|null"}
```

- `total` is the number of matches found by this request's scan (§6.1, §7.3).
- `total_pages` is `max(1, ceil(total / 50))`.
- `first_position` and `last_position` are the 1-based positions of the first and last item in the full order; both are null when `items` is empty.
- `number` is `(first_position − 1) / 50 + 1`, or the requested page when `items` is empty.
- `total` = 0 means no held title contains the text (AC10); the SPA shows the AC10 message and need not show the total.
- `items` empty with `total` > 0 means the requested page is past the end, because matches changed since the search began; the SPA offers page 1.

A `cursor` or `page` is accepted only with `kind` `results` or a typed `id_not_found`. With any other kind it is `400 invalid_cursor` or `400 invalid_page`.

### 4.2 `VideoSummary` (result row and the head of the view)

```json
{
  "video_id": "dQw4w9WgXcQ",
  "record": "known | imported_reference | simulation_only",
  "title":   {"text": "string|null", "availability": "current_local | expired | unavailable"},
  "channel": {"name": "string|null", "name_availability": "current_local | expired | unavailable",
              "channel_id": "string|null"},
  "latest_outcome": LatestOutcome,
  "queue_state": QueueState
}
```

- `title.text` and `channel.name` are non-null only when their availability is `current_local`.
- Availability comes from `metadata.ProjectLocal` at the request cutoff (§6.1): `current_local` → `current_local`; `known_expired` → `expired`; `unknown` or `withheld`, or no metadata row → `unavailable`.
- `channel.channel_id` is `video.channel_id`, else `title_fingerprint.channel_id`, else null. `channel.channel_name` in table `channel` is **never** read (topic index finding F4).
- No member ever carries `fingerprint`, `fingerprint_tokens` or any text derived from them.

`LatestOutcome` (exactly one shape):

| `state` | Members | When |
|---|---|---|
| `committed` | `outcome` (the stored `video.final_outcome`: `accepted`, `rejected`, `excluded`, `deferred`, `failed` or `unavailable_on_youtube`), `run_id` (int or null), `committed_at` (RFC 3339 UTC or null) | `record = known` and `video.final_outcome` is a committed outcome (not null, not `not_yet_evaluated`; `unavailable_on_youtube` is committed, BR-037) |
| `not_yet_evaluated` | — | `record = known`, otherwise |
| `imported_reference` | — | `record = imported_reference` |
| `simulation_only` | — | `record = simulation_only` |

`QueueState` (exactly one shape). The order below is the precedence when several items are active; within one state, the newest item (highest id) wins.

| `state` | Members | Source |
|---|---|---|
| `deferred_claimed` | `reason`, `claiming_run_id` (int or null) | `deferred_item.status = 'retrying'` |
| `deferred_pending` | `reason` | `deferred_item.status = 'pending'` |
| `errors_claimed` | `category`, `claiming_run_id` | `error_entry.status = 'retrying'` |
| `errors_active` | `category` | `error_entry.status = 'active'` |
| `removed_by_user` | — | no active item, and `queue.RemovedByUser` |
| `none` | — | otherwise, and always for `imported_reference` and `simulation_only` |

`reason` is `deferral_reason`; `category` is `error_category`. Both are closed vocabularies. `error_message` is not exposed.

### 4.3 `GET /api/videos/{videoId}`

`VideoSummary` plus `"runs": [RunEntry]`. It is not paged; the list is bounded by the number of runs.

### 4.4 `RunEntry`

```json
{"run_id": 58, "run_type": "manual", "mode": "production | simulation",
 "started_at": "2026-11-10T08:00:00Z",
 "initiated_by": "run_now | queue_retry | schedule | null",
 "run_status": "completed", "outcome": "string|null",
 "decision_detail_path": "/runs/58/video/dQw4w9WgXcQ"}
```

- `started_at` is `run.started_at` as RFC 3339 UTC, parsed with `metadata.ParseLocalTime`; an unparseable value gives null. It lets the SPA show the run date.
- `initiated_by` is the stored `run.initiated_by`, unchanged. It tells a queue retry (`queue_retry`) from Run Now (`run_now`) and a scheduled run (`schedule`). It is null for runs started before migration 024 and for simulation runs; the SPA must not guess a value for null.
- `mode` is `simulation` when `run_type` is `dry_run`, `replay` or `backtest`; otherwise it is `production`.
- `outcome` is the §6.4 per-run classification, which is the stored value for production runs. Null means the production run recorded no outcome; the SPA must not show null as a negative outcome (BR-032 AC3).
- Order: `run_id` descending (newest first by creation; a resumed run keeps its id).

## 5. Matcher (BR-038 AC7)

```text
Key(s)      = NFC( fold( NFC(s) ) )
              fold = golang.org/x/text/cases.Fold() (full Unicode case folding, e.g. ß → ss)
              NFC  = golang.org/x/text/unicode/norm.NFC
Contains(T, N) = there is a byte offset i with T[i : i+len(N)] == N
                 and either i+len(N) == len(T),
                 or the rune that starts at i+len(N) is not in Unicode category M (Mn, Mc, Me)
```

- `N = Key(search text)` and `T = Key(title)`. No trimming or collapsing inside either string.
- No character has a special meaning, so `%`, `_`, `*`, `"`, `'`, `\` and repeated spaces are literal.
- The end rule stops `cafe` from matching `cafe` + U+0301 when no precomposed form exists. With precomposed `é`, NFC already makes it a different rune.
- `cases.Caser` is not safe for concurrent use. Create one per request.
- Search by a needle that is itself only combining marks is literal and allowed.

## 6. Read queries

Each request runs every query in one read-only transaction, which gives one WAL snapshot. Placeholders shown as `(…)` are bound lists of at most 50 values.

### 6.1 Name-search candidates

`N = Key(search text)`. The candidate path is chosen by the §10.7 rule:

- **indexed** when the index is ready (built under the current key version), `N` has at least 3 code points and `N` contains no U+0000;
- **scan** otherwise.

Both paths return rows with the same columns, read in the request's one read-only transaction.

**Indexed path:**

```sql
SELECT m.video_id, m.title, m.is_active, m.expired_at, m.fetched_at, m.expires_at,
       v.discovered_at, v.id IS NOT NULL AS has_video
FROM title_search s
JOIN title_search_doc d ON d.doc_id = s.rowid
JOIN transient_metadata m ON m.video_id = d.video_id
LEFT JOIN video v ON v.video_id = m.video_id
WHERE title_search MATCH :phrase
  AND m.is_active = 1
  AND m.expired_at IS NULL
  AND m.title IS NOT NULL AND m.title <> ''
  AND (v.id IS NOT NULL
       OR EXISTS (SELECT 1 FROM title_fingerprint f
                  WHERE f.video_id = m.video_id
                    AND (f.source = 'user_import' OR f.is_duplicate_reference = 1)));
```

`:phrase` is the bound text `"` + `strings.ReplaceAll(N, "\"", "\"\"")` + `"`, an FTS5 string, so every character inside is literal (§10.6). The query plan must start at `title_search` (test D2).

**Scan path:**

```sql
SELECT m.video_id, m.title, m.is_active, m.expired_at, m.fetched_at, m.expires_at,
       v.discovered_at, v.id IS NOT NULL AS has_video
FROM transient_metadata m
LEFT JOIN video v ON v.video_id = m.video_id
WHERE m.is_active = 1
  AND m.expired_at IS NULL
  AND m.title IS NOT NULL AND m.title <> ''
  AND (v.id IS NOT NULL
       OR EXISTS (SELECT 1 FROM title_fingerprint f
                  WHERE f.video_id = m.video_id
                    AND (f.source = 'user_import' OR f.is_duplicate_reference = 1)));
```

**Shared filter, the same for both paths.**

1. After the last candidate row is read, take `cutoff := now().UTC()` once.
2. A row is a match exactly when `metadata.Held(row, cutoff)` is true and `titlekey.Contains(Key(m.title), N)` is true.
3. The title is always taken from `transient_metadata.title`. The index text `title_search.k` is never read back, used to match or returned.
4. Keep only `(video_id, sortKey)` for each match and drop the title.
5. Apply §7, then project the page's IDs through §6.3 at the same `cutoff`.

Index hits that fail the filter are dropped and are not counted anywhere.

### 6.2 Exact-ID lookup

```sql
SELECT 'video' AS src FROM video WHERE video_id = :id
UNION ALL
SELECT 'fingerprint' FROM title_fingerprint
 WHERE video_id = :id AND (source = 'user_import' OR is_duplicate_reference = 1)
LIMIT 1;
```

A row is found → searchable. Both columns use the default `BINARY` collation, so matching is case-sensitive.

### 6.3 Summary projection for a set S of external IDs

1. `SELECT id, video_id, channel_id, final_outcome, completed_at FROM video WHERE video_id IN (…)`.
2. `SELECT video_id, channel_id FROM title_fingerprint WHERE video_id IN (…) AND (source = 'user_import' OR is_duplicate_reference = 1)`. The select list is fixed and never includes `fingerprint` or `fingerprint_tokens`.
3. `SELECT video_id, title, channel_name, duration_seconds, is_active, expired_at, fetched_at, expires_at FROM transient_metadata WHERE video_id IN (…)` → `metadata.ProjectLocal(row, cutoff)`. The view takes its cutoff after this read.
4. §6.4 links for the `record` decision, §6.5 latest outcome, §6.6 queue state, each for the internal IDs of step 1.

`record`:

- a `video` row exists, and it has at least one link (§6.4) and every link is a simulation run → `simulation_only`;
- a `video` row exists otherwise → `known`. This includes a video with no remaining links;
- otherwise a step-2 row exists → `imported_reference`.

### 6.4 Links and per-run outcome

Links, used for `record`:

```sql
SELECT x.vid, r.run_type FROM (
  SELECT id AS vid, discovered_in_run_id AS run_id FROM video WHERE id IN (…) AND discovered_in_run_id IS NOT NULL
  UNION SELECT video_id, run_id FROM route_decision    WHERE video_id IN (…)
  UNION SELECT video_id, run_id FROM run_video_outcome WHERE video_id IN (…)
  UNION SELECT video_id, run_id FROM deferred_item     WHERE video_id IN (…)
  UNION SELECT video_id, run_id FROM error_entry       WHERE video_id IN (…)
) x JOIN run r ON r.id = x.run_id;
```

Runs for the view (one internal id `:vid`). A run *recorded a decision* when it has a `route_decision` row for the video, or a `run_video_outcome` row with a committed outcome (topic index Q3):

```sql
WITH links AS (
  SELECT run_id FROM route_decision WHERE video_id = :vid
  UNION
  SELECT run_id FROM run_video_outcome
   WHERE video_id = :vid AND final_outcome IS NOT NULL AND final_outcome <> 'not_yet_evaluated'
), evidence AS (
  SELECT run_id,
         MAX(decision = 'matched') AS matched,
         MAX(decision = 'not_matched') AS not_matched,
         MAX(decision = 'skipped' AND skip_reason = 'exclusion') AS excluded
  FROM route_decision WHERE video_id = :vid GROUP BY run_id
)
SELECT r.id, r.run_type, r.status, r.started_at, r.initiated_by,
  CASE WHEN r.run_type IN ('dry_run','replay','backtest') THEN
    CASE WHEN e.matched = 1 THEN 'accepted' WHEN e.not_matched = 1 THEN 'rejected'
         WHEN e.excluded = 1 THEN 'excluded' ELSE 'not_yet_evaluated' END
  ELSE o.final_outcome END AS outcome
FROM links l JOIN run r ON r.id = l.run_id
LEFT JOIN evidence e ON e.run_id = r.id
LEFT JOIN run_video_outcome o ON o.run_id = r.id AND o.video_id = :vid
ORDER BY r.id DESC;
```

The `CASE` must stay identical to `state.runVideoInspection`. Move it into one shared Go constant used by both, so they cannot drift. Test T26 checks agreement with `GET /api/runs/{id}/videos/{videoId}`.

### 6.5 Latest outcome attribution (`record = known`, committed)

```sql
SELECT o.video_id, o.run_id, o.completed_at
FROM run_video_outcome o
JOIN run r ON r.id = o.run_id AND r.run_type IN ('manual','scheduled')
JOIN video v ON v.id = o.video_id
WHERE o.video_id IN (…) AND o.final_outcome = v.final_outcome
  AND o.run_id = (SELECT MAX(o2.run_id) FROM run_video_outcome o2
                  JOIN run r2 ON r2.id = o2.run_id AND r2.run_type IN ('manual','scheduled')
                  WHERE o2.video_id = o.video_id AND o2.final_outcome = v.final_outcome);
```

- `run_id` comes from this row; when there is none (data from before migration 015), it is null.
- `committed_at` is `o.completed_at`, else `video.completed_at`, else null. It is parsed with `metadata.ParseLocalTime`; an unparseable value gives null.
- `outcome` is `video.final_outcome`, unchanged.
- A simulation never supplies this row, because of the `run_type` join.

### 6.6 Queue state

```sql
SELECT 'deferred' AS q, video_id, id, status, deferral_reason AS code, retry_run_id
  FROM deferred_item WHERE video_id IN (…) AND status IN ('pending','retrying')
UNION ALL
SELECT 'errors', video_id, id, status, error_category, retry_run_id
  FROM error_entry WHERE video_id IN (…) AND status IN ('active','retrying');
```

For videos with no row, evaluate `queue.RemovedByUser`, using `subject.video_id` bound to each internal id. Queue tables hold production work only; a simulation never writes them.

## 7. Order, paging and total

### 7.1 Sort key

`sortKey = (hasDisc bool, disc time.Time, videoID string)`.

- `disc = metadata.ParseLocalTime(video.discovered_at)`.
- `hasDisc` is false when there is no `video` row or the value does not parse.

Item a comes before item b when:

1. `a.hasDisc && !b.hasDisc`; or
2. both have a discovery time and `a.disc.After(b.disc)`; or
3. their `hasDisc` and `disc` are equal and `a.videoID < b.videoID` (byte order).

Video IDs are unique, so the order is total.

### 7.2 Cursor

The cursor is base64url (no padding) of compact JSON:

```json
{"v":1,"h":"<hex of first 8 bytes of SHA-256(N)>","dir":"after|before","d":<unix nanos or null>,"id":"<videoID>"}
```

`d` is null exactly when `hasDisc` is false. The cursor names a boundary item, not an offset.

- **No cursor:** the first 50 items in order.
- **`after`:** the first 50 items strictly after the boundary key.
- **`before`:** the last 50 items strictly before the boundary key, returned in order.
- `next_cursor` (`after`, keyed by the last returned item) is present when any match follows it. `prev_cursor` (`before`, keyed by the first returned item) is present when any match precedes it.
- The boundary item need not still exist. Comparison is by key alone, so over unchanged data every match appears on exactly one page (AC11).

### 7.3 Total (BR-038 AC18)

- `total` is the length of the sorted match list of §6.1, computed in the same scan and transaction that produce the page. No second query or estimate is used.
- Over unchanged data, every request for the same search scans the same held titles at the same rule, so `total` is the same on every page. It equals the number of matches that all pages hold together, because the pages partition that one list.
- When titles stop being held between requests, a later page may show a smaller `total`. AC18 asks for stability only over unchanged data.
- `total` is a count. It holds no title, so ADR-001's retention constraints are unaffected.
- `total` counts only rows that passed the §6.1 shared filter. On the indexed path, the number of FTS hits is never used as `total`, as an estimate or as an upper bound shown to the user.

### 7.4 Page jumps

- `page=n` (n ≥ 1) returns items `[(n−1)×50, n×50)` of the sorted list. A jump is position-based: it returns whatever sits at that position at request time.
- `Next` and `Previous` use the cursors, which stay stable when matches change between requests. The SPA uses `page` only for a reload, a shared link or `Go to page 1`.
- Both are cheap here: the scan sorts every match on every request anyway (ADR-001), so a jump costs the same as a cursor page.
- A `page` greater than `total_pages` returns `items: []` with `total` and `number` = n.

### 7.5 Invalid cursor or page

The cursor is invalid when any of these hold:

- it does not decode;
- it is longer than 512 bytes;
- `v` is not 1;
- `dir` is not one of the two values;
- `id` fails the ID pattern;
- `h` differs from the current `N`;
- it is used with a kind that does not page.

The answer is `400 {"error":"invalid_cursor"}`. A `page` that is not a positive decimal integer of at most 7 digits, or `page` together with `cursor`, answers `400 {"error":"invalid_page"}`. In both cases the SPA tells the user to start the search again. The cursor holds a hash of the search text, never the text or a title.

## 8. Invariants, failure and security

1. **No YouTube or quota (AC14).** `internal/videosearch` and the two handlers may import `database/sql`, `metadata` (only `ProjectLocal`, `Held`, `ParseLocalTime`, `LocalRow` and `SearchIndexReady`), `titlekey`, `state` (the shared per-run outcome constant), `queue` (`RemovedByUser`) and `x/text`. Neither may depend on `internal/youtube`, `internal/quota`, `internal/discovery`, `internal/pipeline`, or `metadata.Manager`'s refresh and fetch methods. Test T30 enforces this.
   - **How T30 is checked.** `go list -deps` cannot be empty of these packages, because the permitted `metadata`, `state` and `queue` packages import `quota` or `youtube` for unrelated code. The test therefore checks the **direct** imports of `internal/videosearch` and of `video_search_handlers.go`, and that neither mentions `metadata.Manager`. The handlers import `diag` (§8.5), which is allowed.
2. **Read only (AC15).** Every statement runs in a `ReadOnly` transaction that is rolled back. No statement is `INSERT`, `UPDATE`, `DELETE`, `REPLACE` or DDL. Search never writes `title_search`, `title_search_doc` or `title_search_state`. Only §10.3 writers, the §10.4 reconcile and the §10.9 recreate do.
3. **Retention (AC16).** These follow ADR-001 constraints 1–6:
   - outside `transient_metadata`, a title exists only in request-scoped memory and, as `Key(title)`, in `title_search`, under the §10.8 lifecycle invariants;
   - `Cache-Control: no-store` is set on every response;
   - the SPA does not write search results or views to `localStorage`, `sessionStorage` or IndexedDB, and refetches on every navigation to `/videos` or `/videos/:videoId`. It may keep the in-memory state of the page that is currently shown.
4. **SPA routes.** `/videos?q=<text>&page=<n>` (the URL reflects the search and page, so reload, Back, Forward and a shared link restore it; a cursor is never put in the URL; reloading a `page` URL is a position-based jump, §7.4) and `/videos/:videoId`. On `kind: video`, the SPA navigates to `/videos/:videoId` with `replace`. Run links use `decision_detail_path`. A navigation entry goes in `Sidebar` and in the mobile navigation (the Designer places it in "More"; BR-029). The Designer owns the copy for every `kind`, for AC2's notice (shown on load without a request) and for AC10.
5. **Failures.** A database error answers `500` with the closed codes in §2 and is logged with `diag.Encode` (operation `video_search`; the `diag` vocabulary gains it). `SQLITE_BUSY` cannot occur for a WAL reader except during a checkpoint restart, and `_busy_timeout=5000` covers that. A client disconnect cancels the request; nothing is written, so no partial state exists.
6. **Security.** The trust boundary is unchanged: Traefik BasicAuth in front of every route (root ADR-010). `q` is never put into SQL text; only bound parameters are used. The FTS5 phrase is a bound value. Its only syntax is the surrounding quotes, and embedded quotes are doubled, so user text cannot add FTS5 operators (§10.6). The `IN (…)` lists are built from server-produced IDs, at most 50.

## 9. Verification

Use the real server path where marked:

- `server.New` on a temporary SQLite file opened with `store.Open` (not `:memory:`, so WAL applies), with the controlled YouTube provider through `YUHAUL_YOUTUBE_BASE_URL` (root ADR-001) counting requests;
- fixtures that write `transient_metadata` with raw SQL must call `metadata.ReconcileSearchIndex` after seeding. Otherwise the indexed path cannot see the seeded titles (§10.7). Server tests T10–T26 must run on the **indexed** path: each asserts the logged path, or uses keys of 3 or more code points with the index built. T3's matcher cases are repeated end to end on the indexed path, as part of D1.

### 9.1 Conformance tests

| # | Test | Covers |
|---|---|---|
| T1 | *Unit* `Classify` table: `""`, `"  "`, invalid UTF-8, 1, 2 and 3 characters (also `"ßx"`), 200 and 201 characters (multi-byte), a known ID, an 11-character ID-shaped word, text with `://` not at the start, `youtu.be/<ID>` and `www.youtube.com/watch?v=<ID>` without a scheme (→ `name`, not a link; Q1 closed) | AC3, AC5, AC8, AC9 |
| T2 | *Unit* links: `watch?v=` with extra parameters in any order and a fragment; `m.` and `www.` hosts; upper-case host and scheme; trailing `/`; `youtu.be/<ID>?t=42`; `shorts/<ID>/`; `watch?list=PL123`; `watch?v=a&v=b`; 10- and 12-character IDs; `/embed/<ID>`; `/live/<ID>`; `/Watch?v=`; `ftp://youtube.com/…`; a port; userinfo; a non-YouTube host; a 250-character valid link (`too_long`) | AC4, AC5 |
| T3 | *Unit* matcher: `LOFI`/`Lofi Beats` match; `cafe`/`Café` (NFC and NFD) no match; `café`/`CAFÉ` match; `STRASSE`/`Straße` match; `100%`/`1000` no match; `a_b`/`axb` no match; `*`, quotes and `\` literal; double space does not match a single space; a needle ending before a combining mark does not match | AC7 |
| T4 | *Unit* cursor: round trip; tampering, wrong `h`, wrong `v` or `dir`, more than 512 bytes, a bad `id` → invalid. `page`: `0`, `-1`, `x`, `12345678`, and `page` with `cursor` → invalid | §7.5 |
| T5 | *Unit* order: discovery ties broken by ID; unparseable and missing `discovered_at` sort last by ID | AC11 |
| T6 | *Unit* `metadata.Held` agrees with `ReadLocalDisplay` (`current_local`) on every row of the existing `manager_test` fixtures (marker, malformed marker, `expires_at` past but not swept, future `fetched_at`, inactive, blank title). The same table for `metadata.Indexable` (§10.3): true for each row that `Held` accepts at `now` or at any later time before expiry, and false for the others | AC16 |
| T10 | *Server* 130 matches: pages hold 50, 50 and 30 via `next_cursor`, each with `total` 130, `total_pages` 3, `number` 1–3 and positions 1–50, 51–100 and 101–130. `prev_cursor` walks back to the same pages. `page=2` and `page=3` return the same items as the cursor pages. `page=4` returns no items with `total` 130. The union equals the expected set with no repeat, in order. A search with 0 matches has `total` 0. Expiring one matching title between page 1 and page 2 gives `total` 129 on page 2 | AC10, AC11, AC18 |
| T11 | *Server* empty, too short, too long, invalid text and unusable link each give their `kind` with `200` and no database read (spy on the transaction) | AC3, AC5, AC8, AC9 |
| T12 | *Server* typed known ID → `video`; the same ID in different case → `id_not_found` with name results; an unknown typed ID that is a title substring → `id_not_found` with that title in `results`; an unknown ID from a link → `id_not_found` with no `results` | AC6 |
| T13 | *Server* imported reference by ID → `video`; the view has `record: imported_reference`, `latest_outcome.state: imported_reference`, `queue_state: none` and the fingerprint's channel ID | AC6, AC12 |
| T14 | *Server* a cursor with a different `q` → `400 invalid_cursor` | §7.5 |
| T19 | *Server* coverage flow: a title is found; run `Manager.ExpireStale` → the same search finds nothing, and the cursor of an earlier page gives no item for it; ID lookup still opens it with `title.availability: expired`. Repeat with `CleanupExpired` (→ `unavailable`), and with `expires_at` moved past without a sweep (→ not found; `expired`) | AC2, AC10, AC16 |
| T20 | *Server* a word present only in a fingerprint or in an imported reference's former title → no result; no response anywhere contains a seeded fingerprint string | AC7, AC12 |
| T21 | *Server* every response carries `Cache-Control: no-store`; a captured slog handler holds no search text, title, channel name or cursor | AC16 |
| T22 | *Server* view fields for each seeded state: committed outcome in every BR-032 category, including `unavailable_on_youtube` (native) and a `failed` outcome whose message carries code `video_unavailable` (still `failed`), with its run and time; a run entry with `unavailable_on_youtube`; `not_yet_evaluated`; legacy committed outcome with no `run_video_outcome` (`run_id: null`); channel name held, expired and never fetched; each queue state, including precedence, `claiming_run_id` and `removed_by_user` | AC12 |
| T23 | *Server* simulation: a known video with production and dry-run, replay and backtest decisions → runs labelled `simulation`; a later dry run does not change `latest_outcome` or `queue_state`. A video linked only to simulation runs → `record: simulation_only`, `latest_outcome.state: simulation_only`, `queue_state: none`, and it is still found by name and ID | AC13 |
| T24 | *Server* every `RunEntry.decision_detail_path` resolves: `GET /api/runs/{id}/videos/{videoId}` answers `200` | AC12 |
| T25 | *Server* the runs list is newest first; a production run that only discovered the video, with outcome `not_yet_evaluated`, is not listed (Q3) | AC12 |
| T26 | *Server* for every listed run, `RunEntry.outcome` equals `final_outcome` from `GET /api/runs/{id}/videos/{videoId}` | AC12, BR-032 AC4 |
| T30 | *Static* `go list -deps ./internal/videosearch` contains none of the packages forbidden in §8.1; during T10–T26 the provider counts **0** requests | AC14 |
| T31 | *Server* snapshot every table (all columns, including `transient_metadata.expires_at` and `refreshed_at`) before and after T10–T26: identical | AC15 |
| T32 | *SPA* routes `/videos` and `/videos/:videoId`; the notice is visible on load with no request; each `kind` shows its message; `kind: video` replaces the URL; `Showing {first}–{last} of {total}` uses the page fields; Next and Previous follow the cursors and write `page={number}` into the URL; reloading `?q=…&page=n` restores the page; simulation labels show; no Web Storage write (spy) | AC1–AC3, AC10, AC13, AC16, AC18 |

### 9.2 QS-001 timing tests (gated by `YUHAUL_PERF=1`; run on the deployment host or one of equal capacity)

**Load.** QS-001 now states 150,000 known videos that all hold a title, and 150,000 imported references (user decision 2026-11-11). This replaces its earlier 50,000 held titles and closes finding S1. The scan's cost grows with held titles, so this load is ADR-001's gate. The seed adds expired rows so that the candidate filter is exercised; they are not part of QS-001's load.

**Seed** (deterministic PRNG, recorded seed):

- 150,000 known `video` rows from production runs, with `discovered_at` spread over 180 days and repeated values (to exercise ties);
- 150,000 held `transient_metadata` rows, one for each of those videos: `fetched_at` spread over the last 29 days, `expires_at` = `fetched_at` + 30 days;
- 20,000 additional known videos whose metadata has expired (title NULL, `is_active = 0`, `expired_at` set), outside QS-001's load;
- 150,000 imported references (`title_fingerprint`, `source = 'user_import'`, no `video` row);
- 1–3 `route_decision` and `run_video_outcome` rows per video, across about 200 runs, 20 of them simulations;
- 5,000 deferred items and 1,000 error entries in mixed states;
- for P3 only: 5,000 of the held rows get `expires_at` in the past, before the run starts, so the run's sweep purges 5,000 index entries. This is about one day's expiry at QS-001's load (150,000 / 30), and it puts the secure-delete cost (§10.10) inside the measured run.

**After seeding.** The seed writes rows with raw SQL, so it must call `metadata.ReconcileSearchIndex` once after the seed and before any timed request. The harness reports the reconcile's duration and its inserted count, which must be 150,000, for information. This is the first-start backfill cost on that host (§10.4).

**Gate rerun (2026-11-12).** With option B, P2 and the typed-not-found part of P1 failed on a 4-core i7-10510U. P1–P4 rerun on the indexed path, on the same seed and host class, is the gate for ADR-001 option C. P3 gains write-side pass criteria (below). The harness also reports, for information only:

- the share of name requests that took the scan path, which must be 0 for the planted tokens;
- **the per-stage timing split for P2** (p50, p95 and max of each stage, separately for the first-page set and the page-100+ set). The stages are:
  1. `begin`: classification and `BeginTx`;
  2. `candidates`: the §6.1 candidate query, until its last row is read;
  3. `recheck`: `Held` and `Key`/`Contains` over the candidates;
  4. `sort`: sort and page cut (§7);
  5. `project`: §6.3–§6.6 for the page;
  6. `encode`: JSON encoding and writing the body.

  `Service` records these as durations in `Stats`, which carries counts and timings only, never text. The handler adds `encode`. The operational log may include them.

**P3 write-side measurements.** P3 runs the same production run on three copies of the seed:

- **A:** with searches;
- **B:** without searches (as before);
- **C:** without searches and without the index. Before the run, the harness drops migration 029's two tables and three triggers from that copy and removes the `title_search_state` row.

It reports:

- run wall duration for A, B and C;
- the longest single write transaction of the sweep (§10.10 batches);
- every error or log record whose chain contains SQLite result code 5 (`SQLITE_BUSY`, including `SQLITE_BUSY_SNAPSHOT`) or 6 (`SQLITE_LOCKED`).

Titles use generated words. Planted tokens match exactly 20,000, 5,000, 500, 1 and 0 held titles, all within the 150,000. The generator records the ground-truth match set for each token. Time each request on the server as the duration of `ServeHTTP` of the real mux until the complete body is written.

| # | Test | Pass |
|---|---|---|
| P1 | ≥200 ID requests, one at a time: typed found and not found, all three link forms, through `/api/videos/search`; and ≥200 `GET /api/videos/{id}` | p95 ≤ 1 s for each endpoint (QS-001 AC1) |
| P2 | ≥200 first-page name searches spread over the planted tokens; separately ≥200 requests for page ≥100, reached by following `next_cursor` on the 5,000- and 20,000-match tokens (only the page-100+ requests are timed) | p95 ≤ 2 s for each set at 150,000 held titles (AC2). **This is ADR-001's gate.** Option B failed it on 2026-11-12. Option C must pass it before release. If it still fails, follow ADR-001 "Confirmation and reconsideration"; do not weaken the re-check or `total` |
| P3 | Repeat P1–P2 while a production run against the controlled provider processes ≥1,000 videos and its sweep expires 5,000 titles (copy A). Run the same run on copies B and C (above) | All of: (a) p95 limits hold on A; (b) `run_video_outcome`, `executed_action`, `deferred_item` and `error_entry` contents match between A and B, ignoring timestamps (AC3); (c) **no `SQLITE_BUSY` or `SQLITE_LOCKED`** in A or B, in any log record or returned error, from the run or from search; (d) **run-duration budget:** B's wall duration ≤ 1.15 × C's; (e) no sweep write transaction lasts longer than 1 s. The thresholds in (d) and (e) are architecture defaults (finding O4) |
| P4 | In P2–P3, every response is `200`, and the union of all pages for each token equals the generator's ground truth exactly | No time-out, error or omission (AC4) |

## 10. Title search index (ADR-001 option C)

### 10.1 What the index does

The index is a list of candidates that is always up to date and never decides anything.

- **Adding.** The Go code that writes a held title also adds `Key(title)` to an FTS5 trigram index, in the same transaction.
- **Removing.** SQL triggers remove the entry before any insert and after any change or delete of that video's title row, whichever program does it. FTS5 `secure-delete` removes the old trigrams from the index structures as well.
- **Searching.** Search asks the index which keys contain the search key. It then re-reads each source row and decides with `metadata.Held` and `titlekey.Contains` (§6.1).
- **Repair.** A reconcile at `store.Open` and at the sweep removes entries that should not exist and adds missing ones. A rebuild drops and recreates the index; it never deletes entries one by one.

**Example.** Discovery stores `Straße der Lieder` for `abcdefghijk`.

1. Before the insert, trigger `transient_metadata_search_purge_insert` removes any old entry for that ID. There is none.
2. After the insert, `IndexTitleTx` adds document 812 with `k = "strasse der lieder"`, in the same transaction.
3. A search for `STRASSE` has key `strasse`. It matches document 812 through the phrase `"strasse"`, and the Go re-check confirms it.
4. Later, `Expire` sets `title = NULL`. Trigger `transient_metadata_search_purge_update` deletes FTS row 812 and its document row in the same statement.
5. The same search now returns `total: 0`.

### 10.2 Migration `029_title_search_index.up.sql`

This file is normative. golang-migrate runs it in one transaction.

```sql
CREATE TABLE title_search_doc (
    doc_id   INTEGER PRIMARY KEY,          -- stable FTS5 rowid; rowid alias, survives VACUUM
    video_id TEXT NOT NULL UNIQUE          -- transient_metadata.video_id; holds no title
);

CREATE VIRTUAL TABLE title_search USING fts5(
    k,                                     -- titlekey.Key(transient_metadata.title)
    tokenize = 'trigram case_sensitive 1', -- k is already folded; no tokenizer folding
    detail   = full                        -- phrase queries longer than 3 code points need positions
);
INSERT INTO title_search(title_search, rank) VALUES ('secure-delete', 1);

CREATE TABLE title_search_state (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    key_version TEXT,                      -- titlekey.Version() the entries were built with; NULL = none
    built_at    TEXT                       -- RFC 3339 UTC of the last complete reconcile; NULL = never built
);
INSERT INTO title_search_state (id, key_version, built_at) VALUES (1, NULL, NULL);

CREATE TRIGGER transient_metadata_search_purge_insert
BEFORE INSERT ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = NEW.video_id);
    DELETE FROM title_search_doc WHERE video_id = NEW.video_id;
END;

CREATE TRIGGER transient_metadata_search_purge_update
AFTER UPDATE OF video_id, title, is_active, expired_at, fetched_at, expires_at ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = OLD.video_id);
    DELETE FROM title_search_doc WHERE video_id = OLD.video_id;
END;

CREATE TRIGGER transient_metadata_search_purge_delete
AFTER DELETE ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = OLD.video_id);
    DELETE FROM title_search_doc WHERE video_id = OLD.video_id;
END;
```

Rules:

- **Why there is an insert trigger.** `INSERT OR REPLACE` and `REPLACE INTO` remove the old row with an implicit delete. With `recursive_triggers` off (the default, and YuHaul's setting), that delete fires **no** delete trigger. Without the insert trigger, a replaced title would keep its old entry. The critic's probe (`/tmp/ftsprobe`) showed this.
  - The `BEFORE INSERT` trigger fires for every insert form: plain, `OR REPLACE`, `OR IGNORE` and `… ON CONFLICT DO UPDATE` (an upsert). It purges by `NEW.video_id` before the row is written. For a new ID it deletes nothing.
  - An upsert that updates an existing row also fires the update trigger, because all six columns are in its `SET` list. Purging twice is harmless.
  - `INSERT OR IGNORE` on an existing row purges the entry but leaves the row unchanged. That is a recall gap until the next reconcile, never a retention breach. No production writer uses `OR IGNORE`.
- **No backfill in SQL.** SQL cannot compute `Key`. The backfill is the Go reconcile in `store.Open` (§10.4). The index is empty, and `key_version` and `built_at` are NULL, until that reconcile finishes.
- **No foreign key from `title_search_doc` to `transient_metadata`.** Orphans are removed by the triggers and the reconcile, so a constraint would add nothing.
- **Table type.** `title_search` must stay a regular, content-storing FTS5 table: no `content=''`, no `contentless_delete`, no external `content=`. Secure-delete needs the stored tokens. A contentless-delete table only writes a tombstone and keeps the trigrams (ADR-001, observed experiment).
- **DDL in two places.** `store.RecreateSearchIndex` (§10.9) runs the same `CREATE` statements for the two index tables, the `secure-delete` setting and the three triggers, from a Go constant. Test R6 checks that the `sqlite_master.sql` text for these five objects is identical after migration 029 and after `RecreateSearchIndex`.
- **Ordering and release (F1).**
  - BR-039 owns migrations 027 and 028 ([ARS-006](../../run-initiation/specifications/ars-006-final-outcomes-are-never-reprocessed.md) §1.1–§1.2). This migration is 029.
  - golang-migrate applies only versions above the database's current version. A database that reached 029 before 027 or 028 existed would never receive them.
  - So **search releases only after BR-039's 028**, in the same build or a later one. A build that contains 029 must also contain 027 and 028.
  - If BR-039's migrations are renumbered or dropped, renumber this one to the next free number before release.
- **Down migration.** None, as for every search migration. Rollback is described in §10.9.
- **Rebuilds of `transient_metadata`.** A later migration that rebuilds the table (rename, copy, drop) drops these triggers. It must create them again in the same file, and test R6 fails until it does.

### 10.3 Writer contract

In `internal/metadata/searchindex.go`:

```go
// Indexable reports whether row may have an index entry when written at now:
// it is held now, or it becomes held at its own fetched_at (a fetch time
// later than now) and stays held until expires_at.
//   Indexable(row, now) == fetchedOK && Held(row, latest(now, fetched))
// where fetched, fetchedOK = ParseLocalTime(row.FetchedAt).
func Indexable(row LocalRow, now time.Time) bool

// IndexTitleTx makes the index entry of videoID match the row as stored in tx:
// 1. purge any entry for videoID (the trigger statements, run directly);
// 2. read the row: SELECT video_id, title, is_active, expired_at, fetched_at,
//    expires_at FROM transient_metadata WHERE video_id = ?;
// 3. if a row exists and Indexable(row, now): INSERT INTO title_search_doc(video_id)
//    VALUES (?), then INSERT INTO title_search(rowid, k) VALUES (doc_id, f.Key(*row.Title)).
// It must be called in the same transaction, after the statement that wrote the row.
// An error must abort that transaction, so the title write fails with it.
func IndexTitleTx(ctx context.Context, tx *sql.Tx, f *titlekey.Folder, videoID string, now time.Time) error
```

- Step 2 reads the stored row instead of trusting the writer's values. Two cases depend on this:
  - an `UPDATE` that matched no row adds nothing;
  - an upsert that keeps an earlier `expired_at` is not indexed. Discovery's `ON CONFLICT` leaves `expired_at` unchanged (finding F7).
- `f` is created once per writer transaction. A `cases.Caser` is not safe for concurrent use (§5).

Every production writer of `transient_metadata`:

| Writer | Change |
|---|---|
| `discovery.Engine.upsertTransientMetadata` (raw `INSERT … ON CONFLICT DO UPDATE`, inside the discovery `tx`) | After the `ExecContext`, call `metadata.IndexTitleTx(ctx, tx, f, videoID, fetchedAt)`. `discovery` gains an import of `metadata` (no cycle: `metadata` imports only `quota` and `titlekey`). |
| `metadata.Store.Refresh` (`UPDATE` per item, inside its `tx`) | After each `UPDATE`, call `IndexTitleTx(ctx, tx, f, m.VideoID, fetchedAt)`. |
| `metadata.Store.Upsert` (currently a bare `db.ExecContext`, no production caller) | Run the upsert and `IndexTitleTx(…, m.VideoID, time.Now().UTC())` in one transaction. |
| `metadata.Store.Expire` | The update trigger purges the entry. `Expire` itself is unchanged; its caller `ExpireStale` now calls it in batches (§10.10). |
| `metadata.Store.CleanupExpired` | The delete trigger purges the entry. Its `limit` is capped at `IndexWriteBatch` rows per call (§10.10). It still has no production caller. |
| Any new writer | It must call `IndexTitleTx` in the same transaction if it can make a row indexable, and it must be registered in test R7. Purges need nothing, because the triggers handle them. |

### 10.4 Reconcile (backfill and repair)

```go
// ReconcileSearchIndex repairs the index in batches. Each batch is one
// write transaction that takes the write lock at its start (§10.5), reads its
// work under that lock, applies at most IndexWriteBatch changes, and commits:
//  1. FTS rowids with no title_search_doc row → DELETE FROM title_search WHERE rowid = ?;
//  2. title_search_doc rows with no FTS row → delete them (step 4 re-adds if indexable);
//  3. title_search_doc rows whose transient_metadata row is missing or not Indexable(row, now)
//     → purge (the §10.2 trigger statements by video_id);
//  4. transient_metadata rows with is_active = 1, expired_at IS NULL, title IS NOT NULL
//     and no title_search_doc row, keeping those Indexable(row, now) → add as in IndexTitleTx step 3.
// Batches repeat until one finds less than a full batch of work across steps 1–4. The last
// batch then sets title_search_state.key_version = titlekey.Version() and built_at = now
// (RFC 3339 UTC). It returns counts only.
func ReconcileSearchIndex(ctx context.Context, db *sql.DB, now time.Time) (ReconcileStats, error)

type ReconcileStats struct{ OrphansRemoved, Purged, Added, Batches int }

// SearchIndexReady reports, read in tx, that title_search_state.built_at IS NOT NULL
// and key_version = titlekey.Version().
func SearchIndexReady(ctx context.Context, tx *sql.Tx) (bool, error)
```

- Each batch reads its work under the write lock and changes only what it read, so it never acts on data that another writer changed in the meantime.
- If a batch fails, the batches before it stay committed. Each committed batch leaves the index valid under I1–I2. The next reconcile continues the work.
- `ReconcileSearchIndex` refuses to run (`ErrSearchIndexVersion`) when `key_version` is not NULL and differs from `titlekey.Version()`. That case needs a rebuild (§10.9).

Callers:

| When | Where | On error |
|---|---|---|
| **Startup backfill and repair** | `store.Open`, after `BackfillRouteIdentities`, before returning: if `key_version` is not NULL and differs from `titlekey.Version()`, call `RecreateSearchIndex` (§10.9) first; then `ReconcileSearchIndex`. `cmd/server` calls `Open` before the scheduler and the listener, so the index is complete before any request. At first start after 029, or after a recreate, this is the backfill: it folds and inserts every indexable title. | Startup aborts, like the route backfill. Recovery is in §10.9. |
| **Sweep** | `metadata.Manager.RunLifecycle`, in this order: (1) `ExpireStale`, whose `Expire` batches purge through the trigger; (2) `ReconcileSearchIndex(ctx, m.store.DB(), m.now().UTC())`, which runs even if `ExpireStale` failed or expired 0 rows; (3) `RefreshRoutedNearExpiry`, which runs only if `ExpireStale` succeeded, as today. Errors are combined with `errors.Join`. | The pipeline already logs a warning and continues (`pipeline.go` line 408). Search stays correct: retention is enforced by the triggers and the re-check. Only a recall gap from a non-Go writer can last until the next reconcile. |

The sweep's reconcile is the "purge on the sweep":

- it removes the entry of every row that is no longer indexable at the sweep's `now`;
- that includes rows past `expires_at` whose text timestamp layout made `ListActivePastExpiry`'s SQL comparison miss them.

### 10.5 Write-lock-first transactions (F5)

`ReconcileSearchIndex` and `RecreateSearchIndex` read and then write. In WAL mode, a `BEGIN DEFERRED` transaction that reads first and writes later can fail. If another connection commits between its first read and its first write, the upgrade to a write lock fails with `SQLITE_BUSY_SNAPSHOT`, and `_busy_timeout` does not retry it.

So each of their transactions takes the write lock at its start, with `BEGIN IMMEDIATE` semantics:

- Take a dedicated connection: `conn, _ := db.Conn(ctx)`.
- Run `conn.ExecContext(ctx, "BEGIN IMMEDIATE")`, then the batch, then `COMMIT`. On any error, run `ROLLBACK`. Release the connection afterwards.
- `BEGIN IMMEDIATE` waits up to `_busy_timeout` (5 s) for another writer to finish. It then holds the write lock for the whole batch, which is short because of §10.10.
- **Not** the modernc DSN option `_txlock=immediate`. It is connection-pool-wide, so it would also make every read-only search transaction take the write lock (`internal/store/store.go` opens a single pool).

The other writers keep their existing transactions:

- **`Expire` and `Refresh`** write before they read: their first statement is an `UPDATE`. Their lock upgrade happens at that first statement, where `_busy_timeout` applies.
- **Discovery** runs `IndexTitleTx` after its own write in the same transaction, so it already holds the write lock.

### 10.6 Tokenizer and query rules

- **Tokenizer.** `trigram case_sensitive 1`, with `remove_diacritics` left at its default of 0. Every run of 3 consecutive code points in `k`, whitespace and punctuation included, is a term, and no character is changed. `k` is already `Key(title)`, so the index uses AC7's full case folding, never FTS5's one-to-one folding.
- **Query.** The search key `N = Key(text)` is bound as one FTS5 string: `"` + `N` with each `"` doubled + `"`. Inside an FTS5 string every character is literal. `*`, `-`, `:`, `^`, `(`, `AND`, `NEAR` and the rest have no meaning there. This was observed for `*`, `(`, `-`, `:`, `^`, doubled quotes and a double space.
- **Length.** A name search needs at least 3 code points of raw text (§3 step 6). The indexed path needs at least 3 code points of `N`:
  - 3 code points: the phrase is one trigram term;
  - n > 3: the phrase is n − 2 trigram terms at consecutive positions, so it matches exactly the keys that contain `N` as a contiguous substring;
  - fewer than 3 code points: FTS5 matches nothing, so the scan path is used (§10.7). This can happen when NFC composes the typed text, for example `e` + U+0301 + `x` → `éx`. Full folding can make `N` longer (`ß` → `ss`), which is harmless.
- **Key version.** `titlekey.Version()` returns `"<rev>/<unicode>"`:
  - `<rev>` is a constant in `titlekey`, starting at 1, raised whenever `Key`'s code changes;
  - `<unicode>` is `norm.Version`, the Unicode version of the bundled `golang.org/x/text`. Upgrading `x/text` can change NFC and case folding, and so change `Key`.

  Entries built with another version may miss true matches, so search uses the index only when the stored `key_version` equals `Version()` (§10.7). `store.Open` rebuilds when they differ (§10.4).
- **No miss (QS-001 AC4).** Take a row that is a true match at cutoff `c`: `Held(row, c)` and `Contains(Key(title), N)`. Its last write `w` is visible in the snapshot. That write was followed by `IndexTitleTx` or a reconcile at clock time `t_w ≤ c`. `Held(row, c)` with `c ≥ t_w` implies `Indexable(row, t_w)`, so an entry with `k = Key(title)` exists under the current key version (I1, I3). `Contains` implies that `N` is a substring of `k`, so the phrase matches. The prefilter is therefore a superset of the matches. Limits of this proof:
  - It assumes the wall clock does not step backwards between the write and the search.
  - It assumes every write since the last reconcile came from a §10.3 writer.

  When either fails, the result is a recall gap until the next reconcile, never a wrong result.
- **Hits are not matches.** The phrase match ignores the combining-mark end rule, and it ignores the held rule at the cutoff. `Contains` and `Held` apply them (§6.1).

### 10.7 Path selection

Inside the request's read-only transaction:

```text
indexed := SearchIndexReady(tx) && utf8.RuneCountInString(N) >= 3 && !strings.ContainsRune(N, 0)
```

- `SearchIndexReady` requires `built_at` to be set and `key_version` to equal `titlekey.Version()`.
- When `indexed` is true, the §6.1 indexed query runs; otherwise the scan query runs. Both feed the same filter, sort, page and `total` (§6.1, §7).
- U+0000 goes to the scan path because FTS5 handling of NUL inside a query string is unverified. The scan is always correct.
- The path is logged as `indexed` or `scan`, never with the text.
- A typed ID that is not found (§4.1 `id_not_found`, AC6) has an 11-code-point key, so it always takes the indexed path when the index is ready.
- An error from the indexed query, for example a damaged index, answers `500 search_failed` and is logged with `diag`. It does not silently fall back to the scan, because that would hide the damage that also blocks writes (§10.9).

### 10.8 Invariants

| # | Invariant | Enforced by |
|---|---|---|
| I1 | **Bounded to the source.** At every commit, each `title_search` row `x` has a `title_search_doc` row `(x, v)`, and a `transient_metadata` row `v` exists. That row has not been inserted over, replaced, or changed in `video_id`, `title`, `is_active`, `expired_at`, `fetched_at` or `expires_at` since the entry was added. So no entry survives `Expire`, a title change, a replace or a delete, whatever the writer. | §10.2 triggers; reconcile steps 1–3 |
| I2 | **Removed from the index structures.** A purged entry's trigrams are removed from the live segment content of `title_search_data`, and its `title_search_content` and `title_search_docsize` rows are deleted, in the same transaction. This is a statement about what SQL can read in those tables, not about bytes left in freed pages or the WAL (§10.11, ADR-001 O2). | `secure-delete = 1` on a regular FTS5 table |
| I3 | **Completeness.** After each §10.3 writer commits, and after each complete reconcile, every row that is `Indexable` at that writer's or reconcile's `now` has exactly one entry, with `k = Key(title)` under `key_version`. | §10.3, §10.4 |
| I4 | **Only held, unexpired titles.** An entry is added only for an `Indexable` row. It is removed at the first of: an insert, change or delete of the source row, or the next reconcile after the row stops being indexable. Between `expires_at` and that point, the entry exists only while the source title also still exists, and it is never returned. | I1, §10.4, §6.1 filter |
| I5 | **The index never decides.** Results and `total` come only from rows that pass `Held(row, cutoff)` and `Contains(Key(m.title), N)`. `k` is never read back, matched against or returned. | §6.1 |
| I6 | **Stable key.** FTS5 rowids are `title_search_doc.doc_id` values, an `INTEGER PRIMARY KEY` that `VACUUM` does not renumber. | §10.2 |
| I7 | **Search does not write.** Only §10.3 writers, `ReconcileSearchIndex`, `RecreateSearchIndex` and migration 029 write the three tables. | §8.2, T31 |
| I8 | **One key version.** Search uses the index only when every entry was built under the current `titlekey.Version()`. A version change empties the index by drop-and-recreate before any entry is added under the new version. | §10.4, §10.7, §10.9 |

### 10.9 Rebuild, damage, compatibility and rollback

**Rebuild is drop-and-recreate.** `store.RecreateSearchIndex(ctx, db)` runs one `BEGIN IMMEDIATE` transaction (§10.5) that:

1. drops the three triggers;
2. runs `DROP TABLE title_search` (FTS5 drops its shadow tables with it) and `DROP TABLE title_search_doc`;
3. runs the §10.2 `CREATE` statements again (two tables, `secure-delete`, three triggers) from the Go constant;
4. sets `key_version = NULL` and `built_at = NULL`.

The reconcile that follows is the backfill. Rules:

- **Never rebuild by `DELETE FROM title_search`.** With secure-delete, each deleted entry costs about as much as a purge, which the critic measured at about 3 ms in a dense index: about 7.5 minutes for 150,000 entries, holding the write lock.
- **Never use FTS5 `'rebuild'` as a repair.** It rebuilds from `title_search_content`, not from `transient_metadata`, so it cannot fix a gap or a wrong key.
- **The trigger for a rebuild** is `store.Open` seeing a `key_version` that is set and differs from `titlekey.Version()`, or an operator who resets it (below).
- **Leftovers.** Dropping the tables frees their pages. Freed-page bytes follow §10.11.

**A damaged index blocks writes to the source table (F4).** Every insert, update or delete on `transient_metadata` fires a trigger that writes `title_search`. If the FTS5 structure is damaged, those statements fail. Then:

- discovery's transaction fails, so the run fails or skips discovery, depending on existing error handling;
- `Expire` fails, so titles stay held longer, although search still hides them after `expires_at` through the re-check;
- `Refresh` fails.

Search on the indexed path answers `500` (§10.7). This is a new availability dependency of the metadata lifecycle on the index, and it is accepted. The alternative, writes that bypass the index, would break I1.

- **Detect.** Writer errors that name `title_search`, or the operator runs `INSERT INTO title_search(title_search) VALUES('integrity-check')`.
- **Recover (drop-and-recreate).**
  1. Stop the server.
  2. Run `sqlite3 <db> "UPDATE title_search_state SET key_version = 'reset', built_at = NULL"`. This touches no FTS5 table and fires no trigger.
  3. Start the server. `store.Open` sees a mismatching `key_version`, runs `RecreateSearchIndex` and then the backfill.
- **If recreate fails** (for example, `DROP TABLE title_search` itself fails because its shadow tables are damaged), restore a backup. `[unverified]` which kinds of damage stop an FTS5 `DROP`.

**First start after 029.** `store.Open` reconciles, which is the backfill. Until it finishes, nothing is served. P1–P4 report its duration on the 150,000 seed.

**Rollback (F7).** There is no down migration.

- **An older binary on a database at version 29 does not start.** golang-migrate finds no migration file for version 29 in its source and `m.Up()` returns an error, so `store.Open` fails. `[unverified]`: the exact error text; test F-R below pins the behavior.
- Rolling back therefore means one of:
  1. **Restore a backup** taken before 029 was applied. Data written since then is lost.
  2. **Reset by hand**, with the server stopped, in one `sqlite3` transaction:

     ```sql
     DROP TRIGGER transient_metadata_search_purge_insert;
     DROP TRIGGER transient_metadata_search_purge_update;
     DROP TRIGGER transient_metadata_search_purge_delete;
     DROP TABLE title_search;
     DROP TABLE title_search_doc;
     DROP TABLE title_search_state;
     UPDATE schema_migrations SET version = 28, dirty = 0;
     ```

     After this, the database is at BR-039's 028. Rolling back further follows ARS-006's release note. Re-upgrading later reapplies 029 and runs the backfill.
- Test F-R: open a database at 029 with a migration source that ends at 028. `store.Open` must fail. After the hand reset above, it must succeed, and `transient_metadata` is unchanged.

**Tests and tools that call `ApplyMigrations` without `Open`.** They get an index that is not ready, so search uses the scan path. That is correct but slow, and it is logged as `scan`.

### 10.10 Write cost and batching (F3)

The critic measured about 3 ms per secure-delete purge in a dense test index of 150,000 near-identical titles. Real titles differ, and the cost is not yet known for them. So:

- **Batch constant.** `metadata.IndexWriteBatch = 200`. It is the most rows that one transaction may purge or add in:
  - `ExpireStale`;
  - `CleanupExpired`;
  - each reconcile batch.

  At 3 ms per row that is about 0.6 s of write lock per transaction, below P3's 1 s limit. The spike (task 1 of the implementation plan) may change the value, and the change is recorded here.
- **`ExpireStale`.** It still lists up to 10,000 IDs. It now calls `Expire` once per chunk of at most `IndexWriteBatch` IDs, each in its own transaction. If a chunk fails, it returns the number expired so far, joined with the error. The earlier chunks stay committed, and the next sweep continues.
- **`CleanupExpired`.** `limit` is clamped to `IndexWriteBatch`.
- **Discovery** purges only rows it overwrites and adds one entry per held title, inside its existing transaction. The spike measures this cost. P3's run-duration budget bounds it.

**Spike (first implementation task, throwaway code, results recorded in this section and the log).** The spike runs on the gate host class.

- **Data:** realistic titles. Use the P1–P4 generator, adjusted so that title lengths and character mix follow a sample of real held titles: lengths of about 20–100 code points, mixed scripts and emoji. Record only the length and script distributions; no real title is copied into the repository. Use 150,000 indexed entries.
- **Compared:** `secure-delete` = 1 and = 0.
- **Measured:**
  - per-row purge cost (p50 and p95) for `Expire` batches of 200;
  - the add cost per discovery insert;
  - reconcile backfill time;
  - the database and WAL size after backfill.
- **Pass:** with `secure-delete = 1`, both of these hold:
  - an `Expire` batch of `IndexWriteBatch` rows commits in ≤ 1 s at p95;
  - expiring 5,000 rows takes ≤ 30 s in total.

  Otherwise, lower `IndexWriteBatch` until the batch limit holds. If even 50 rows per batch exceed 1 s, or 5,000 rows exceed 30 s, secure-delete is too costly. Then the options below go to a decision.

**Spike result (2026-11-12): FAIL with `secure-delete = 1`.** Decision O5 is now open (ADR-001 O5). `IndexWriteBatch` stays 200 until O5 is decided.

Setup. Throwaway code outside the repository, not committed. Bundled `modernc.org/sqlite` v1.58.0, WAL, the production DSN. The schema is the §10.2 DDL and the real `transient_metadata` columns. Host: i7-10510U (4 cores, 8 threads), 14 GB RAM. The host was shared with other Go build and test jobs (load average 1.6–4.7), so wall times are noisy and best read as upper bounds. Data: 150,000 rows, each indexed under `lower(title)`, an approximation of `Key`, because `titlekey` is internal.
- **Titles.** Generated, not copied. Vocabulary of about 150,000 pseudo-words with Zipf frequencies, 20–100 code points (p5/p50/p95 = 21/57/94). Scripts: Latin 75%, Cyrillic 8%, Han 6%, kana 4%, Greek 3%, Arabic 3%. About 20% end with an emoji. They also contain digits, separators such as ` - ` and ` | `, and mixed-in Latin words. This sample is not drawn from real held titles.
- **Runs.** Expiry runs `Expire`'s `UPDATE` (clears title and sets `expired_at` and `is_active`) on random rows, so the update trigger purges each entry. Each rep starts from a fresh copy of the backfilled database. "tmpfs" is `/tmp`, so commits cost no fsync. "Disk" is ext4 on LVM, the realistic case.

| Measure | `secure-delete = 1` | `secure-delete = 0` |
|---|---|---|
| Backfill, 150,000 rows, batches of 200 | 9.3 s tmpfs; 22.9 s disk | 10.1 s tmpfs; 21.7 s disk |
| Database after backfill | 140.9 MB (88.1 MB without the index, so the index adds 52.9 MB) | same |
| WAL after backfill / after `wal_checkpoint(TRUNCATE)` | 5.8 MB / 0 MB | same |
| Purge cost per row, p50 / p95 | 4.8 / 11.3 ms (tmpfs, quiet); 6.6 / 16.7 ms (disk) | 0.15 / 0.5 ms (tmpfs); 0.20 / 0.67 ms (disk) |
| Add, `INSERT` + doc + FTS, per row, p50 / p95 | 0.13 / 0.48 ms (tmpfs); 0.17 / 0.62 ms (disk) | 0.12 / 0.48 ms; 0.18 / 0.63 ms |
| 200-row `Expire` transaction, p50 / p95 / max | 1.10 / **1.25** / 1.43 s (tmpfs, 3 reps); 1.78 / **2.45** / 3.51 s (disk, 5 reps) | 48 / 214 / 303 ms (tmpfs); 129 / 479 / 735 ms (disk) |
| Expiring 5,000 rows | 26.8, 27.4, 29.3 s (tmpfs); **37.6–51.6 s** (disk, 5 reps) | 2.5 s (tmpfs); 5.0–5.6 s (disk) |
| WAL peak during expiry | 28.7 MB | 11.9 MB |

Batch size against `secure-delete = 1`, on the shared host (tmpfs rows are the first run; the other rows were run later under heavier load):

| Rows per transaction | Transaction p95 | 5,000 rows in total |
|---|---|---|
| 200 | 1.25 s tmpfs; 2.45 s disk | 27–29 s tmpfs; 38–52 s disk |
| 150 | 1.44 s (tmpfs, loaded) | 33–50 s (tmpfs, loaded) |
| 100 | 1.06 s disk; 1.73 s tmpfs (loaded) | 45–47 s disk |
| 50 | 0.86 s disk; 0.78 s tmpfs (loaded) | 64–79 s disk |

Reading against the pass rule:
- **Per-transaction limit (≤ 1 s at p95).** Not met at 200 rows. Only 50 rows per transaction meets it on every run, at about 0.8 s.
- **Total limit (5,000 rows in ≤ 30 s).** Not met on disk at any batch size, in any rep. It is met on tmpfs only at 200 rows, by 0.7–3 s, and only on the quietest run. Smaller batches do not help: the per-row cost rises as batches shrink (about 5 ms at 200, about 10 ms at 50), so 50-row batches take 52–79 s.
- **Cause.** Secure-delete costs about 5 ms of CPU per purged row, against about 0.15 ms with it off. That is about 30× more than a plain delete. 5,000 purges therefore need about 25–30 s of write-lock time even with no I/O wait, which is the whole budget.
- **Comparison with the critic's figure.** The critic measured about 3 ms per purge in a dense index. Realistic titles measure 5–7 ms, so they are not cheaper here.
- **Confidence.** The numbers are noisy, but the disk failure is not a close call, and the tmpfs pass is not robust. A rerun on a quiet gate host could move the totals, so it should confirm the decision, not reopen it.

Consequences:
- The §10.10 pass rule is not met, so the "otherwise lower `IndexWriteBatch`" step cannot rescue it: the 5,000-row limit fails at every size, and 50 rows per transaction already takes 52–79 s in total. Secure-delete is too costly as specified, and S1–S4 below go to a decision (O5).
- `secure-delete = 0` passes by a wide margin (2.5–5.6 s for 5,000 rows, 200-row p95 ≤ 0.48 s). Backfill, size and add cost do not depend on the setting.
- Not measured: S1's `optimize` and S2's drop-and-recreate (the backfill time above is S2's rebuild cost, 9–23 s with search on the scan path meanwhile). S3 and S4 are decisions, not measurements. Measure S1 before choosing it.

**Alternatives if secure-delete is too costly.** These are recorded for a decision, not chosen. Each one changes when a removed title's trigrams leave the index structures, so the compliance owner decides together with the user (ADR-001 O5).

| # | Option | What changes | Cost |
|---|---|---|---|
| S1 | `secure-delete = 0`, plus `INSERT INTO title_search(title_search) VALUES('optimize')` at the end of each sweep | Purges are logical at once: the trigger and the re-check still hide the title. The old trigrams stay in segment b-trees, behind tombstones, until the `optimize` at the end of the same sweep. A title cleared outside the sweep, by `Refresh` or discovery, keeps its old trigrams until the next sweep. | `optimize` rewrites the whole index: one long write transaction per sweep, to be measured |
| S2 | `secure-delete = 0`, plus a drop-and-recreate (§10.9) at the end of each sweep | Same window as S1. No tombstones at all after the sweep. | A full backfill each sweep; the index is not ready during the rebuild, so search uses the scan |
| S3 | Keep secure-delete with a smaller batch, and run the sweep only when no run is writing | No change to retention | A longer sweep; needs a scheduling change in the pipeline |
| S4 | Return to option B (scan) and ask the requirements owner to relax QS-001 | No index | Fails QS-001 as it stands |

### 10.11 Retention proof and index tests

All of these tests use `store.Open` on a temporary file, with the bundled modernc SQLite.

**What R1 proves, and what it does not (F6).** After a purge, R1 shows four things for a marker title:

1. no FTS5 query (`MATCH`) can find it;
2. FTS5's own term list (`fts5vocab`) lists none of its trigrams;
3. its rows in `title_search_content`, `title_search_docsize` and `title_search_doc` are gone;
4. no live `title_search_data` block, read through SQL, contains the bytes of any marker trigram as a whole.

R1 does **not** prove that the bytes are gone from the database file. They can remain in:

- freed pages and in-page free space;
- WAL frames until checkpoint and overwrite;
- the OS page cache and backups.

The byte probe (point 4) also has a limit. FTS5 stores terms with prefix compression, so a trigram can be stored split. A probe for whole trigrams can therefore miss a remaining entry. That is why R1 has a negative control: an index with `secure-delete = 0` must make the same probe find the marker. If the control finds nothing, the probe is not sensitive enough and R1 fails as inconclusive. Point 2 is the primary proof; point 4 adds evidence.

Whether to also use the connection-wide `PRAGMA secure_delete` is a compliance decision (ADR-001 O2). It zeroes freed content for every table, including the title that `Expire` clears in `transient_metadata`. It costs extra writes on every delete and overwrite in the database, and it does not clear WAL frames before checkpoint.

| # | Test | Proves |
|---|---|---|
| R1 | **Purge from the index structures.** Index a title containing a marker made of trigrams that appear in no other seeded title, among 1,000 realistic titles. Then, separately, remove it in each of these ways: (a) `Manager.ExpireStale` after `expires_at`; (b) `CleanupExpired`; (c) `Refresh` to a different title; (d) a discovery re-upsert with a different title; (e) raw SQL `UPDATE transient_metadata SET title = NULL`; (f) raw SQL `DELETE`; (g) raw `INSERT OR REPLACE` with a different title; (h) `RecreateSearchIndex`. After each, points 1–4 above hold. **Negative control**, in the same test: repeat (e) on a scratch FTS5 table with the same tokenizer and `secure-delete = 0`, and on a contentless-delete one. Point 4's probe must find at least one marker trigram there; if it finds none, the test fails as inconclusive. | AC16, I1, I2, within the limits stated above |
| R2 | **Trigger coverage (non-Go writers).** For an indexed row, each of these removes its `title_search_doc` and FTS rows: raw-SQL updates of `video_id`, `title`, `is_active`, `expired_at`, `fetched_at` and `expires_at`; a raw `DELETE`; `INSERT OR REPLACE INTO transient_metadata …`; `REPLACE INTO transient_metadata …`; and an `INSERT … ON CONFLICT(video_id) DO UPDATE` on an existing row. `INSERT OR IGNORE` on an existing row removes them as well (the documented recall gap, repaired by the next reconcile). An update of an unrelated column, such as `duration_seconds`, keeps them. The test runs with `PRAGMA recursive_triggers` both off and on. | I1 |
| R3 | **Writer adds.** Discovery upsert, `Refresh` and `Upsert` each leave exactly one entry with `k = titlekey.Key(title)`. No entry is added for: a blank or whitespace-only title; `is_active = 0`; `expired_at` set, including a discovery re-upsert of a row with `expired_at` set; `expires_at` not after `fetched_at`; a `Refresh` that matches no row. A `Refresh` with a future `fetched_at` inside its TTL is indexed. A failing `IndexTitleTx` (closed table, injected) rolls back the title write. | I3, §10.3 |
| R4 | **Reconcile.** Seed by raw SQL: an indexable row with no entry; a held entry; an entry whose row passed `expires_at` without a sweep; an entry whose row was deleted while the triggers were dropped (test-only); an FTS row with no document; a document with no FTS row. Run one reconcile with `IndexWriteBatch` forced to 2. It leaves exactly the indexable rows indexed, with the expected counts and more than one batch, and it sets `key_version` and `built_at`. A second reconcile reports 0, 0, 0. **Concurrency:** while a reconcile runs, a second connection commits discovery upserts and `Expire` batches in a loop. No call returns `SQLITE_BUSY`, and at the end I1 and I3 hold. **Version:** with a stored `key_version` of `0/x`, `ReconcileSearchIndex` returns `ErrSearchIndexVersion`; `store.Open` rebuilds, and afterwards `key_version` equals `titlekey.Version()`. | §10.4, §10.5, I3, I4, I8 |
| R5 | **Sweep.** `RunLifecycle` with 450 expired rows and `IndexWriteBatch` = 200 runs 3 `Expire` transactions and purges all 450 entries. With no expired rows, its reconcile still purges an entry whose row's `expires_at` is in a layout that `ListActivePastExpiry` misses. With `ExpireStale` failing (injected), the reconcile still runs and the error is returned joined. | "purge on the sweep", §10.10 |
| R6 | **Schema.** After `ApplyMigrations` to head: `title_search` is a regular FTS5 table, so its `sqlite_master.sql` has `trigram case_sensitive 1` and no `content=` or `contentless_delete`. `title_search_config` has `secure-delete` = 1. All three triggers exist; the update trigger lists the six columns. `title_search_doc.doc_id` is an `INTEGER PRIMARY KEY`. `title_search_state` has one row. The `sqlite_master.sql` text of the two tables and three triggers is identical after `RecreateSearchIndex`. After `VACUUM`, every document still maps to the same `video_id`, and D1 still holds. | I2, I6, §10.2 DDL rule; guards against a migration that rebuilds the table |
| R7 | **Writer census (static).** Scan the non-test Go files with case-insensitive patterns, with any whitespace or newline between words: `insert\s+(or\s+(replace\|ignore\|abort\|fail\|rollback)\s+)?into\s+transient_metadata\b`, `replace\s+into\s+transient_metadata\b`, `update\s+(or\s+(replace\|ignore\|abort\|fail\|rollback)\s+)?transient_metadata\b` and `delete\s+from\s+transient_metadata\b`. The matching functions are exactly: `metadata.Store.Upsert`, `Refresh`, `Expire` and `CleanupExpired` (`internal/metadata/store.go`), `discovery.Engine.upsertTransientMetadata` (`internal/discovery/engine.go`) and the reconcile's own reads, which are not writes. Each insert or update writer except `Expire` calls `IndexTitleTx`. A new writer fails the test until it is added here. Limit: SQL assembled at run time from fragments is not seen. Review must catch it; the triggers still guarantee purges. | §10.3 completeness |
| D1 | **Indexed equals scan (differential).** For a corpus with `ß`, NFC and NFD accents, standalone combining marks, `ﬃ`, Greek final sigma, Turkish `İ`, emoji, quotes, `%`, `_`, `*`, `\`, double spaces and U+0000, plus held, expired, past-expiry-not-swept, withheld and future-fetched rows: for every T3 case and for ≥500 needles (substrings of titles, mutated substrings and random strings of 3–12 code points), the match list and `total` from the indexed path equal those from the scan path. T10–T12 and T19 are repeated with the indexed path asserted. | AC4, AC7, AC18, §10.6 |
| D2 | **Path and plan.** Unit test of the §10.7 rule: not built → scan; `key_version` different → scan; a 2-code-point `N` from 3 typed characters → scan; `N` with U+0000 → scan; otherwise → indexed. `EXPLAIN QUERY PLAN` of the indexed query starts with `SCAN title_search VIRTUAL TABLE INDEX` and an `M` constraint. | §6.1, §10.7 |
| F-R | **Rollback behavior.** As stated in §10.9. | §10.9 |

T19–T21 and T31 still apply unchanged. T31's table snapshot includes the three new tables, and search must leave them identical.

## 11. Open questions

See the [topic index](../index.md). None block this contract.

- Q3 changes only test T25 and the `links` CTE.
- ADR-001 O1–O5 and findings F7–F9 and O4 are tracked in the topic index.
