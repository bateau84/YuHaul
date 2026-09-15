# YuHaul

**YuHaul is a personal YouTube subscription discovery, filtering, routing, and analytics system.**

It periodically inspects content published by channels you subscribe to, evaluates discovered videos against configurable routes, and sends qualifying videos to the appropriate playlists.

YuHaul was originally built for music discovery: subscribe to channels that publish music, let YuHaul collect their uploads, reject things you do not want, deduplicate songs that appear across multiple channels, and put the remaining discoveries into playlists for later listening.

The routing model is intentionally more general than music, however. A route can target any kind of subscribed content.

## North Star

> **Find more of what you'll actually keep.**

YuHaul should maximize useful discoveries while minimizing noise, wasted attention, wasted API quota, and unexplained decisions.

It automates repetitive discovery work, but the user remains in control of subscriptions, routing policy, thresholds, and other meaningful decisions.

---

## How it works

At a high level:

```text
YouTube account
      │
      ▼
Subscriptions
      │
      ▼
Discovered videos
      │
      ▼
Ordered routes
      │
      ├── Filters / conditions
      │
      ├── Duplicate detection
      │
      └── Actions
      │
      ▼
Playlists
```

Every routing decision is recorded so YuHaul can explain what happened and produce meaningful historical statistics.

---

## Routes

YuHaul evaluates discovered videos against user-defined routes.

A route contains conditions and an action, such as adding a matching video to a playlist.

Examples of filtering conditions include:

* ignored channels
* ignored video IDs
* ignored titles
* ignored words or phrases
* minimum video duration
* maximum video duration
* exact duplicate detection
* fuzzy duplicate detection against previously seen titles

Routes are ordered.

A video may match multiple routes and therefore be added to multiple playlists.

A route can also be marked **final**. When a video successfully matches a final route, no later routes are evaluated for that video.

This makes it possible to create flows such as:

```text
Fireship videos
    ↓
Programming playlist
    ↓
FINAL

Everything else
    ↓
Music filters
    ↓
TBL playlist
```

---

## Duplicate detection

Music discovery produces a particular problem: several channels may upload effectively the same song using slightly different titles.

For example:

```text
Artist - Track
Artist - Track (Official Audio)
ARTIST – TRACK
Artist - Track [Extended Mix]
```

YuHaul therefore maintains a durable historical title corpus and supports fuzzy title comparison in addition to exact matching.

Duplicate decisions retain their provenance.

If several subscribed channels publish substantially the same track, YuHaul can retain information about all of those sources even if only the first copy is routed.

This allows statistics to distinguish between:

* **discovery credit** — which channel introduced the content first
* **relevance credit** — which other channels also published content that proved useful

---

## Scheduled processing

YuHaul is intentionally not a live-monitoring system.

It runs periodically according to a user-defined schedule.

Each run discovers new content and processes all configured routes.

Work that cannot be completed because API quota is exhausted is deferred and processed during a later run.

Temporary operational limitations must not cause eligible content to disappear.

---

## Quota awareness

YouTube API capacity is a finite resource.

YuHaul treats quota as an operational budget rather than simply waiting for requests to fail.

Where possible, it tracks:

* quota consumed
* quota remaining
* quota usage per run
* quota usage by operation type
* expensive operations
* deferred work caused by insufficient quota

Routing should perform cheap and locally decidable work before expensive remote operations.

YuHaul should reuse known metadata, cache durable information, batch compatible operations where possible, and avoid performing external mutations until a video has satisfied its configured filters.

---

## Processing and error handling

YuHaul distinguishes between different processing outcomes.

A video may be:

* pending
* evaluated
* accepted
* rejected
* deferred
* successfully processed
* failed

**Not processed is never equivalent to rejected.**

Quota-limited work is placed into a deferred queue.

Unexpected failures are placed into an error queue where they can be inspected, retried, or deliberately removed by the user.

Completed actions must be idempotent so scheduled runs do not repeatedly add the same video to the same playlist.

---

## Explainability

YuHaul should always be able to answer:

> Why was this video added?

and:

> Why wasn't this video added?

For each video and route, decision evidence should make it possible to inspect:

* whether the route was evaluated
* whether it matched
* which condition accepted or rejected it
* duplicate similarity information
* the historical title it matched against
* whether a final route stopped further processing
* which action was attempted
* whether that action succeeded, failed, or was deferred

Logs and statistics should be derived from durable decision history rather than being the only surviving record.

---

## Analytics

Analytics are a core part of YuHaul.

YuHaul should measure both what its automation does and whether its discoveries actually prove useful.

Statistics should be available by:

* run
* route
* playlist
* source/channel
* time period
* lifetime

Useful measurements include:

* videos discovered
* videos evaluated
* videos accepted
* videos rejected
* rejection reasons
* videos deferred
* errors
* playlist additions
* quota expenditure
* source yield
* source usefulness

### Source usefulness

A channel that generates many candidate videos is not necessarily valuable.

YuHaul therefore distinguishes between:

**Yield**

How often content from a channel survives automatic routing.

and:

**Usefulness**

How often routed content later produces evidence that the user actually wanted it.

One useful signal is whether a routed video is subsequently added to one of the user's own listening or rotation playlists.

This makes questions such as these possible:

> Which channels consistently give me music I keep?

> Which channels create lots of candidates but almost nothing useful?

> Has this channel become less useful over the last year?

> Which subscriptions generate the most useful discoveries per API quota spent?

---

## Historical trends

Lifetime totals can hide important changes.

YuHaul preserves historical data so metrics can be visualized over time.

Graphs should make it possible to distinguish:

* temporary dips
* seasonal patterns
* long-term deterioration
* improving sources
* inactive sources
* routing changes
* changes in discovery volume
* changes in usefulness

Historical statistics should also retain enough configuration provenance to distinguish a change in source quality from a change in routing rules.

---

## Dry-run and backtesting

YuHaul should make routing changes testable before they affect the user's YouTube account.

### Dry-run

Evaluate current or proposed routing rules without performing playlist mutations.

### Historical backtesting

Apply proposed route configurations against previously observed content.

Examples:

> What would have happened if duplicate similarity were 95% instead of 97%?

> Would adding this ignored word have rejected songs I later kept?

> What would I have lost if this channel had been ignored for the last year?

> How much API quota would this rule have saved?

Historical simulations must remain clearly separate from actual routing history.

---

## Recommendations

YuHaul may use its statistics to provide evidence-backed recommendations.

For example:

> This channel has produced 183 routed tracks during the last year and none were subsequently retained.

YuHaul may advise that the subscription is worth reviewing.

It must **not automatically unsubscribe from channels or autonomously modify routing policy**.

Likewise, future versions may suggest channels that appear likely to be useful, but subscribing remains a user decision.

---

## Administration

YuHaul is administered through a responsive web interface designed to work properly on both desktop and mobile devices.

The interface is expected to expose areas such as:

* account status
* routes
* filters
* route ordering
* schedules
* playlist destinations
* ignore lists
* run history
* decision history
* deferred work
* errors
* quota usage
* channel analytics
* statistics
* graphs
* dry-runs
* historical backtests

---

## Principles

YuHaul follows a few fundamental principles:

### Evidence over assumptions

Keep enough factual history to explain decisions and statistics later.

### Automation without loss of control

Automate repetitive work aggressively, but leave policy decisions with the user.

### Efficient by design

Treat external API quota as a constrained resource.

### Explain every decision

A filtered or routed video should never become an unexplained historical outcome.

### Preserve history

Historical observations are useful not only for duplicate detection, but also for analytics, trend analysis, and backtesting.

### Measure actual value

Routing volume is not success.

Useful discovery is.

---

## Deployment

YuHaul ships as a single container. The container serves the admin SPA and the
REST API on one port and stores all durable state in a SQLite database file
that lives on a persistent volume.

Kubernetes is the supported deployment target. A plain `docker run` equivalent
still works, but the repository now ships Kubernetes manifests instead of a
Docker Compose file.

### Quick start with Kubernetes

1. Build the container image and make it available to your cluster:

   ```bash
   docker build -t yuhaul:latest .
   # Push the image to a registry your cluster can reach, or load it into
   # your local cluster (e.g. `kind load docker-image yuhaul:latest`).
   ```

2. Create the OAuth secret from `k8s/secret.example.yaml`:

   ```bash
   cp k8s/secret.example.yaml k8s/secret.yaml
   # Edit k8s/secret.yaml and replace every CHANGEME value.
   kubectl apply -f k8s/secret.yaml
   ```

   The redirect URL must be registered in Google Cloud Console exactly as:

   ```text
   https://yuhaul.bateau.cloud/api/auth/callback
   ```

3. Apply the remaining manifests:

   ```bash
   kubectl apply -f k8s/pvc.yaml
   kubectl apply -f k8s/deployment.yaml
   kubectl apply -f k8s/service.yaml
   kubectl apply -f k8s/ingress.yaml
   ```

4. Point `yuhaul.bateau.cloud` at your ingress controller and ensure the
   controller terminates TLS for that hostname.

The SQLite database is stored on the `yuhaul-data` PVC and persists across
pod restarts, rescheduling, and image updates.

### Kubernetes manifests

| File | Purpose |
|---|---|
| `k8s/pvc.yaml` | PersistentVolumeClaim for the SQLite database file |
| `k8s/deployment.yaml` | Single-replica Deployment with the YuHaul container |
| `k8s/service.yaml` | ClusterIP service exposing port 80 to the ingress |
| `k8s/ingress.yaml` | Ingress rule for `yuhaul.bateau.cloud` |
| `k8s/secret.example.yaml` | Template for the OAuth credentials Secret |

### Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `YUHAUL_DB_PATH` | no | `/data/yuhaul.db` | Path to the SQLite database file |
| `YUHAUL_HTTP_ADDR` | no | `:8080` | HTTP listen address |
| `YUHAUL_WEB_ROOT` | no | `/app/web` | Directory containing the built SPA |
| `YUHAUL_DAILY_QUOTA` | no | `10000` | YouTube Data API daily quota limit |
| `YUHAUL_YOUTUBE_BASE_URL` | no | — | Override the YouTube Data API v3 base URL for Product Acceptance / sandboxing |
| `YUHAUL_OAUTH_CLIENT_ID` | yes | — | Google OAuth2 client ID |
| `YUHAUL_OAUTH_CLIENT_SECRET` | yes | — | Google OAuth2 client secret |
| `YUHAUL_OAUTH_REDIRECT_URL` | yes | — | Absolute OAuth callback URL |
| `YUHAUL_ENCRYPTION_KEY` | yes | — | 32-byte AES-256-GCM key (raw or base64) |

### OAuth callback hostname and client-secret provisioning

The YouTube OAuth2 flow requires a registered redirect URI. The value of
`YUHAUL_OAUTH_REDIRECT_URL` must match one of the **Authorized redirect URIs**
configured in the Google Cloud Console exactly, including scheme, hostname, and
path. The application exposes the callback handler at `/api/auth/callback`.

For the Kubernetes ingress hostname `yuhaul.bateau.cloud`, set:

```text
https://yuhaul.bateau.cloud/api/auth/callback
```

Provision the client secret by editing `k8s/secret.example.yaml` (copied to
`k8s/secret.yaml`) and applying it with `kubectl apply`. The secret is mounted
into the container as environment variables, never logged, and used only to
exchange the authorization code for an access token during the OAuth flow.

### Docker run (without Kubernetes)

```bash
docker run -d \
  --name yuhaul \
  -p 8080:8080 \
  -v yuhaul-data:/data \
  -e YUHAUL_OAUTH_CLIENT_ID \
  -e YUHAUL_OAUTH_CLIENT_SECRET \
  -e YUHAUL_OAUTH_REDIRECT_URL=https://yuhaul.bateau.cloud/api/auth/callback \
  -e YUHAUL_ENCRYPTION_KEY \
  yuhaul:latest
```

The SPA is built into the container image during `docker build`; no external
volume for static assets is required.

---

## Status

YuHaul is currently being rebuilt from an earlier working application.

The initial development focus is establishing the deterministic discovery and routing core, durable processing state, duplicate detection, quota-aware execution, explainability, and the historical evidence required for meaningful analytics.

Implementation architecture and technology choices will be documented separately as the design progresses.

---

## The loop

```text
DISCOVER
   ↓
EVALUATE
   ↓
EXPLAIN
   ↓
ROUTE
   ↓
MEASURE
   ↓
LEARN
   ↓
USER DECIDES
```

**YuHaul — haul in the good stuff.**

