---
type: decision
title: ADR-001 — YouTube Provider Seam via Base URL Override
description: Thread the existing youtube.WithBaseURL option through the shipped binary behind a YUHAUL_YOUTUBE_BASE_URL environment variable so Product Acceptance can run the assembled artifact against a fake YouTube endpoint without modifying the binary.
tags: [architecture, decision, yuhaul, youtube, provider-seam, product-acceptance]
---

**Status:** proposed

## Depends on

- [YuHaul System Architecture](yuhaul-system-architecture.md)
- [Anchor — YouTube Subscription Discovery & Routing](../anchors/youtube-discovery-routing.md)

## Satisfies

- [OC-001 — YouTube API Quota and Failure Semantics](../requirements/yuhaul/oc-001-youtube-api-quota-and-failure-semantics.md) — the seam makes the assembled-product quota/failure path observable without a live YouTube account.
- [BR-002 — Subscription-Based Discovery Without Live Monitoring](../requirements/yuhaul/br-002-subscription-discovery-without-live-monitoring.md) — the seam lets PA-001 exercise the discovery phase against a fake provider on the shipped binary.

## Problem

The shipped binary has no provider seam for the YouTube Data API v3. The three production client-construction sites (`internal/discovery/engine.go:95`, `internal/pipeline/executor.go:89`, `cmd/server/main.go:469`) each call `youtube.NewClient(ctx, token, ...)` with the hardcoded production base URL `https://www.googleapis.com/youtube/v3`. `youtube.WithBaseURL` exists (`internal/youtube/client.go:54-60`) but is referenced only from unit/e2e test factories, never from `cmd/server/main.go`. Consequently the only positive PA-001 evidence path is a reviewer-authored in-module harness overlay; the assembled artifact cannot run the discovery journey against a fake YouTube. This is plan-vs-implementation drift (`plan.md:51` declares a YouTube sandbox substitution) and it makes every future PA re-run re-derive a bespoke overlay.

## Decision drivers

- **Product acceptance must run the assembled product-owned path** (`§Verification and Composition Obligations`: "YouTube Data API v3 interaction" is production-owned and may be substituted only at the external boundary).
- **Narrow seam, not a wide one.** PA needs to redirect the HTTP base URL; it must still use the real OAuth token source and the real quota recorder, otherwise the proof is blind to the exact auth/quota wiring.
- **No binary modification at PA time.** The seam must be operator-configurable (env var), not a recompile.
- **Minimal blast radius.** The client already exposes `WithBaseURL`; this is plumbing, not new surface area.

## Options considered

### Option A — Base-URL override seam (chosen)

Add `YUHAUL_YOUTUBE_BASE_URL` (empty = production default) read in `cmd/server/main.go`, and thread `youtube.WithBaseURL` into all three client-construction sites.

- **Mechanism:** env var → `appConfig.YouTubeBaseURL` → per-package `WithBaseURL`/`WithYouTubeBaseURL` options → `youtube.WithBaseURL` at construction. Empty value applies no option, preserving the hardcoded default.
- **Benefits:** reuses an existing, tested client option; keeps auth (token source) and quota (recorder) on the real path; single env var is trivial to document and gate; blast radius is three construction sites.
- **Costs:** three sites must be touched; a fourth future construction site must remember to thread the base URL.
- **Failure modes:** a PA pointed at a fake server that does not implement the full surface (`subscriptions`, `channels`, `playlistItems`, `videos`, `playlistItems.insert`) fails fast with an API 404 — visible, not silent.

### Option B — Full client factory override

Expose a `WithClientFactory`/`WithYouTubeFactory` seam in `main.go` so tests inject a wholly custom `*youtube.Client` (or a client-like interface).

- **Mechanism:** pass a factory closure at wiring time that swaps the whole HTTP client.
- **Benefits:** maximum flexibility — can also replace token source, recorder, transport, retries.
- **Costs:** each package already carries its own nominal factory type (`discovery.ClientFactory`, `pipeline.YouTubeClientFactory`); a full override invites PA to bypass the real token/recorder path and thus *weaken* the proof the seam exists to strengthen. Heavier than the finding requires.
- **Failure modes:** a PA that swaps the recorder silently loses the quota-cost assertion (the exact CRITICAL fixed in the prior wave).

## Decision

In the context of a pre-merge MAJOR where the shipped binary cannot execute the PA journey, facing the choice between a narrow base-URL seam and a full client-factory override, we decided **for Option A** (thread the existing `youtube.WithBaseURL` behind `YUHAUL_YOUTUBE_BASE_URL`) and **against** a full factory override, to achieve a minimal, operator-configurable seam that preserves the real OAuth token source and quota recorder, accepting a three-site plumbing change.

The base URL remains the *only* substitutable dimension of the client. OAuth token acquisition, quota recording, and the HTTP transport stay on the production-owned path.

## Realization

- `cmd/server/main.go`
  - Add `YouTubeBaseURL string` to `appConfig`.
  - `loadConfig()`: `cfg.YouTubeBaseURL = envDefault("YUHAUL_YOUTUBE_BASE_URL", "")`.
  - Wire: `discovery.NewEngine(authSvc, machine, ledger, db, discovery.WithLogger(logger), discovery.WithBaseURL(cfg.YouTubeBaseURL))`.
  - Wire: `pipeline.NewExecutor(db, ledger, authSvc, pipeline.WithExecutorLogger(logger), pipeline.WithYouTubeBaseURL(cfg.YouTubeBaseURL))`.
  - Wire: `&metadataFetcher{auth: authSvc, baseURL: cfg.YouTubeBaseURL}`.
- `internal/discovery/engine.go`
  - Add `baseURL string` field to `Engine`; add `WithBaseURL(string) Option`.
  - `defaultClientFactory` appends `youtube.WithBaseURL(e.baseURL)` when `e.baseURL != ""`.
- `internal/pipeline/executor.go`
  - Add `baseURL string` field to `Executor`; add `WithYouTubeBaseURL(string) ExecutorOption`.
  - `defaultYouTubeFactory` appends `youtube.WithBaseURL(e.baseURL)` when `e.baseURL != ""`.
- `cmd/server/main.go` `metadataFetcher`
  - Add `baseURL string` field; in `FetchMetadata`, construct with `youtube.WithBaseURL(f.baseURL)` when non-empty.
- `internal/youtube/client.go`
  - No functional change. Update the `WithBaseURL` doc comment to state it is a supported runtime/PA seam, not test-only.

## Consequences

- **Positive:** PA-001/PA-003 can run against the assembled binary pointed at a fake YouTube; the quota recorder and token source remain real, so the accepted quota-cost and discovery proofs stay honest; the drift between `plan.md:51` and the artifact closes.
- **Negative:** three construction sites now carry the base URL; a future fourth site must be added deliberately. The seam is URL-level, so the fake provider must implement the real API surface (a 404 on an unimplemented method is the failure signal).
- **Neutral:** empty env var behavior is byte-for-byte identical to today (no `WithBaseURL` option applied).

## Confirmation

- Product Acceptance re-run of PA-001 with `YUHAUL_YOUTUBE_BASE_URL` pointed at a fake YouTube serving recorded responses: discovery populates videos, `run.quota_cost > 0` is observed through the real quota ledger, and no binary modification is required.
- Unit test: `discovery.WithBaseURL` and `pipeline.WithYouTubeBaseURL` produce a client whose requests hit the override URL (verify via `httptest.Server` request path).

## Open questions

- None.
