---
type: specification
title: ARS-001 — Video Search — HTTP API, Input Classification, Matcher, Keyset Paging, Read Queries and Tests
description: Exact contract for BR-038 and QS-001. GET /api/videos/search classifies the input (empty, too long, link, video ID, too short, name) and returns a tagged body; GET /api/videos/{videoId} returns the video view. Name search scans held titles in one read-only transaction and matches in Go with NFC plus full case folding; results use an opaque keyset cursor. The view joins outcome, queue state and per-run decisions, with simulation kept apart. No YouTube call, no write, no title copy. Includes the conformance and QS-001 timing tests.
tags: [architecture, specification, videos, search, api, retention, sqlite, paging, unicode, performance, simulation]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted — exact implementation contract. The pure code, metadata reads, `Service`, both handlers and tests T1–T26, T30 and T31 are implemented; the SPA data shape is checked against `web/src/types.ts` by a server test. The QS-001 timing tests P1–P4 (§9.2) and SPA test T32 are not yet run. It realizes [ADR-001](../decisions/adr-001-held-title-search-by-request-time-scan.md). Search adds no migration and reads existing tables only. It depends on [run-initiation ARS-005](../../run-initiation/specifications/ars-005-requeue-stranded-videos.md) for the predicate `queue.RemovedByUser` (§2.3), the indexes `deferred_item_video_status` and `error_entry_video_status` (migration 026), and the stored `unavailable_on_youtube` outcome. See §1.3. Revised 2026-11-11 after critique: identity outcome mapping, a 150,000-held-title speed gate, index ownership and the access-log advisory. Reconciled 2026-11-11 (Plan revision 19) with the updated BR-038 and QS-001: the page carries the AC18 `total`, positions and page count; `page=n` jumps are added beside the cursors; scheme-less links stay unrecognized (Q1 closed); the timing seed equals QS-001's new load.

## What the two sides agree on

The browser sends whatever the user typed. The server alone decides what the text means, so tests on the real server path cover every input rule. The server answers with a `kind`, and the SPA shows the matching message or result. The server never calls YouTube and never writes.

**Example (success).** `GET /api/videos/search?q=lofi%20beats` → `200 {"kind":"results","results":{"items":[…3 items…],"next_cursor":null,"prev_cursor":null,"page_size":50}}`. The SPA lists 3 videos.

**Example (link).** `GET /api/videos/search?q=https%3A%2F%2Fyoutu.be%2FdQw4w9WgXcQ%3Ft%3D42` → `200 {"kind":"video","video_id":"dQw4w9WgXcQ","from_link":true}`. The SPA replaces the URL with `/videos/dQw4w9WgXcQ`, which calls `GET /api/videos/dQw4w9WgXcQ` → `200` with `title.availability: "expired"` and the full outcome and run history.

**Example (refusal).** `q=lo` → `200 {"kind":"too_short"}`. `q=https://www.youtube.com/watch?list=PL123` → `200 {"kind":"unusable_link"}`.

## Derived from

- [BR-038](../../../requirements/yuhaul/br-038-video-search-by-title-or-id.md), [QS-001](../../../requirements/yuhaul/qs-001-video-search-response-time.md) and [ADR-001](../decisions/adr-001-held-title-search-by-request-time-scan.md).
- Source read this session:
  - schema in migrations 001, 002, 014 and 015;
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
| AC16 no new retention | ADR-001 constraints 1–2; §6.1 cutoff; `Cache-Control: no-store`; §8.4 no client persistence; tests T19–T21. |
| AC17, QS-001 AC1–AC4 | §9.2, tests P1–P4. |

## Scope

In scope: two read endpoints, the classifier, the matcher, the cursor, the read queries, the SPA routes and data rules, and the tests.

Out of scope:

- Visual layout and copy. The Designer owns them; the `kind` values fix which message is needed.
- Matching fingerprints (excluded by BR-038).
- Any change to metadata lifecycle.

## 1. Components

### 1.1 New package `internal/videosearch`

| File | Content |
|---|---|
| `classify.go` | `Classify(raw string) Input` (§3). Pure. |
| `match.go` | `Key(s string) string` and `Contains(titleKey, needleKey string) bool` (§5). Pure. |
| `cursor.go` | `encodeCursor` and `decodeCursor` (§7). Pure. |
| `store.go` | The read queries (§6), each inside a caller-supplied `*sql.Tx`. Implemented: `ScanMatches` (§6.1) and `ProjectMetadata` (§6.3 step 3). They report title and channel availability as `held`, `expired`, `never_fetched` or `withheld`; the wire form (§4.2) maps `held` → `current_local`, `expired` → `expired`, and the other two → `unavailable`. |
| `service.go` | `Service.Search(ctx, raw string, p Paging) (SearchResult, error)` and `Service.View(ctx, videoID string) (VideoView, error)`. `Paging` carries the raw `cursor` and `page` values and whether each was sent. Each opens one `BeginTx(ctx, &sql.TxOptions{ReadOnly: true})`, runs every query in it, then rolls back. Kinds that need no lookup (`empty`, `invalid_text`, `too_long`, `unusable_link`, `too_short`) open no transaction. It takes `now func() time.Time` for tests. Errors: `ErrInvalidCursor`, `ErrInvalidPage`, `ErrInvalidVideoID`, `ErrNotFound`. The handlers (`internal/server/video_search_handlers.go`) map them to the §2 codes. |

### 1.2 Extractions (behavior-preserving)

- `metadata.LocalRow` and `metadata.ProjectLocal(row LocalRow, cutoff time.Time) LocalDisplay`: the per-row body of `ReadLocalDisplay`, moved without change. `ReadLocalDisplay` then calls it. `metadata.Held(row, cutoff) bool` is true exactly when `ProjectLocal(row, cutoff).TitleAvailability == "current_local"`.
- `metadata.ParseLocalTime`: the existing `parseLocalTime`, exported.
- **Outcome names are the stored values (identity).** Every outcome shown is the stored `final_outcome` text, unchanged. That includes `unavailable_on_youtube`, which revised ARS-005 stores natively in `video` and `run_video_outcome`. No error text or diag code is ever read to derive a category. A `failed` outcome whose message carries code `video_unavailable` stays `failed`.

### 1.3 Owned by run-initiation ARS-005

Search adds **no migration**.

- Migration 026 (ARS-005 §1) owns `deferred_item_video_status ON deferred_item(video_id, status)` and `error_entry_video_status ON error_entry(video_id, status)`. It also owns the final-outcome CHECK step that admits `unavailable_on_youtube`.
- ARS-005 §2.3 owns `queue.RemovedByUser`.

Search is implemented after ARS-005's migration 026 and predicates, or in the same change. Without the indexes, §6.6 is still correct but scans the queue tables.

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
- Logs record the `kind`, the candidate and match counts, and the duration. They never record `q`, a title, a channel name or a cursor.
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

After the last row is read, take `cutoff := now().UTC()` once. A row is a match when `metadata.Held(row, cutoff)` and `Contains(Key(title), N)`. Keep only `(video_id, sortKey)` for each match; drop the title. Then apply §7. Project the page's IDs through §6.3 at the same `cutoff`.

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

1. **No YouTube or quota (AC14).** `internal/videosearch` and the two handlers may import `database/sql`, `metadata` (only `ProjectLocal`, `Held`, `ParseLocalTime`, `LocalRow`), `state` (the shared per-run outcome constant), `queue` (`RemovedByUser`) and `x/text`. Neither may depend on `internal/youtube`, `internal/quota`, `internal/discovery`, `internal/pipeline`, or `metadata.Manager`'s refresh and fetch methods. Test T30 enforces this.
   - **How T30 is checked.** `go list -deps` cannot be empty of these packages, because the permitted `metadata`, `state` and `queue` packages import `quota` or `youtube` for unrelated code. The test therefore checks the **direct** imports of `internal/videosearch` and of `video_search_handlers.go`, and that neither mentions `metadata.Manager`. The handlers import `diag` (§8.5), which is allowed.
2. **Read only (AC15).** Every statement runs in a `ReadOnly` transaction that is rolled back. No statement is `INSERT`, `UPDATE`, `DELETE`, `REPLACE` or DDL.
3. **Retention (AC16).** These follow ADR-001 constraints 1–2:
   - titles exist only in request-scoped memory;
   - `Cache-Control: no-store` is set on every response;
   - the SPA does not write search results or views to `localStorage`, `sessionStorage` or IndexedDB, and refetches on every navigation to `/videos` or `/videos/:videoId`. It may keep the in-memory state of the page that is currently shown.
4. **SPA routes.** `/videos?q=<text>&page=<n>` (the URL reflects the search and page, so reload, Back, Forward and a shared link restore it; a cursor is never put in the URL; reloading a `page` URL is a position-based jump, §7.4) and `/videos/:videoId`. On `kind: video`, the SPA navigates to `/videos/:videoId` with `replace`. Run links use `decision_detail_path`. A navigation entry goes in `Sidebar` and in the mobile navigation (the Designer places it in "More"; BR-029). The Designer owns the copy for every `kind`, for AC2's notice (shown on load without a request) and for AC10.
5. **Failures.** A database error answers `500` with the closed codes in §2 and is logged with `diag.Encode` (operation `video_search`; the `diag` vocabulary gains it). `SQLITE_BUSY` cannot occur for a WAL reader except during a checkpoint restart, and `_busy_timeout=5000` covers that. A client disconnect cancels the request; nothing is written, so no partial state exists.
6. **Security.** The trust boundary is unchanged: Traefik BasicAuth in front of every route (root ADR-010). `q` is never put into SQL text; only bound parameters are used. The `IN (…)` lists are built from server-produced IDs, at most 50.

## 9. Verification

Use the real server path where marked: `server.New` on a migrated temporary SQLite file (not `:memory:`, so WAL applies), with the controlled YouTube provider through `YUHAUL_YOUTUBE_BASE_URL` (root ADR-001) counting requests.

### 9.1 Conformance tests

| # | Test | Covers |
|---|---|---|
| T1 | *Unit* `Classify` table: `""`, `"  "`, invalid UTF-8, 1, 2 and 3 characters (also `"ßx"`), 200 and 201 characters (multi-byte), a known ID, an 11-character ID-shaped word, text with `://` not at the start, `youtu.be/<ID>` and `www.youtube.com/watch?v=<ID>` without a scheme (→ `name`, not a link; Q1 closed) | AC3, AC5, AC8, AC9 |
| T2 | *Unit* links: `watch?v=` with extra parameters in any order and a fragment; `m.` and `www.` hosts; upper-case host and scheme; trailing `/`; `youtu.be/<ID>?t=42`; `shorts/<ID>/`; `watch?list=PL123`; `watch?v=a&v=b`; 10- and 12-character IDs; `/embed/<ID>`; `/live/<ID>`; `/Watch?v=`; `ftp://youtube.com/…`; a port; userinfo; a non-YouTube host; a 250-character valid link (`too_long`) | AC4, AC5 |
| T3 | *Unit* matcher: `LOFI`/`Lofi Beats` match; `cafe`/`Café` (NFC and NFD) no match; `café`/`CAFÉ` match; `STRASSE`/`Straße` match; `100%`/`1000` no match; `a_b`/`axb` no match; `*`, quotes and `\` literal; double space does not match a single space; a needle ending before a combining mark does not match | AC7 |
| T4 | *Unit* cursor: round trip; tampering, wrong `h`, wrong `v` or `dir`, more than 512 bytes, a bad `id` → invalid. `page`: `0`, `-1`, `x`, `12345678`, and `page` with `cursor` → invalid | §7.5 |
| T5 | *Unit* order: discovery ties broken by ID; unparseable and missing `discovered_at` sort last by ID | AC11 |
| T6 | *Unit* `metadata.Held` agrees with `ReadLocalDisplay` (`current_local`) on every row of the existing `manager_test` fixtures (marker, malformed marker, `expires_at` past but not swept, future `fetched_at`, inactive, blank title) | AC16 |
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
- 5,000 deferred items and 1,000 error entries in mixed states.

Titles use generated words. Planted tokens match exactly 20,000, 5,000, 500, 1 and 0 held titles, all within the 150,000. The generator records the ground-truth match set for each token. Time each request on the server as the duration of `ServeHTTP` of the real mux until the complete body is written.

| # | Test | Pass |
|---|---|---|
| P1 | ≥200 ID requests, one at a time: typed found and not found, all three link forms, through `/api/videos/search`; and ≥200 `GET /api/videos/{id}` | p95 ≤ 1 s for each endpoint (QS-001 AC1) |
| P2 | ≥200 first-page name searches spread over the planted tokens; separately ≥200 requests for page ≥100, reached by following `next_cursor` on the 5,000- and 20,000-match tokens (only the page-100+ requests are timed) | p95 ≤ 2 s for each set at 150,000 held titles (AC2). **This is ADR-001's gate:** if P2 fails, the FTS5 fallback (ADR-001 option C) applies before release. The test also reports the p95 for information |
| P3 | Repeat P1–P2 while a production run against the controlled provider processes ≥1,000 videos. Run the same run on an identical seed copy without searches | p95 limits hold; `run_video_outcome`, `executed_action`, `deferred_item` and `error_entry` contents match between the two copies, ignoring timestamps (AC3) |
| P4 | In P2–P3, every response is `200`, and the union of all pages for each token equals the generator's ground truth exactly | No time-out, error or omission (AC4) |

## 10. Open questions

See the [topic index](../index.md). None block this contract; Q3 changes only test T25 and the `links` CTE.
