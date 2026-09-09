---
type: anchor
title: YuHaul — YouTube Subscription Discovery & Routing
description: Personal stateful discovery and routing system for YouTube subscription content with analytics and user authority
tags: [anchor, youtube, discovery, routing, analytics, music]
---

**Status:** accepted

## Goal

Build a personal, stateful discovery and routing system for content published by subscribed YouTube channels. The system automatically discovers newly published videos, evaluates them against configurable routing rules, adds qualifying videos to configured YouTube playlists, and measures how effective subscriptions and routing rules are at discovering content the user ultimately values. Initial primary use case is music discovery with high-volume duplicate handling.

## Acceptance Criteria

1. User can securely connect the intended YouTube account
2. Application discovers relevant videos from subscribed channels without continuous live monitoring
3. User can define multiple ordered routes
4. Routes contain configurable conditions and playlist actions
5. A video can match multiple routes
6. A route can be marked final and terminate further evaluation after successful match
7. Existing exclusion behaviors supported: channels, videos, titles, ignored words/phrases, duration limits
8. Exact and configurable fuzzy historical duplicate detection supported
9. Historical titles persisted; existing corpus importable/preservable
10. Duplicate provenance retained sufficiently to distinguish discovery credit from other source relevance
11. Processing state survives across runs and application restarts
12. Repeated execution does not repeat already completed playlist actions
13. Quota-limited work deferred rather than lost or falsely rejected
14. Failed work retained in inspectable error queue with retry/removal
15. Every meaningful route decision explainable from durable evidence
16. Scheduled runs configurable by user
17. Runs can be manually initiated
18. External API consumption accounted for and visible
19. Processing quota-aware, avoids unnecessary external expenditure
20. Run-level and route-level operational statistics available
21. Source/channel usefulness statistics available
22. User-retention/value signals measured where reliable provider evidence permits
23. Statistics viewable historically over time, not only lifetime counters
24. Useful trends visualized graphically
25. Route/configuration history sufficient to interpret historical statistics
26. Dry-run evaluation without external playlist mutation
27. Historical backtesting compares routing changes against retained observations without altering external state
28. User can inspect historical runs and understand why videos accepted/rejected/deferred/failed
29. Administration interface usable on desktop and mobile
30. Temporary API/quota/authorization/runtime failures do not silently lose eligible work
31. System provides evidence-backed advice but does not autonomously unsubscribe channels or make material policy changes
32. Processing outcomes distinguish rejected, deferred, failed, and not-yet-evaluated work; absence of processing is never represented as a negative routing decision

## v1 Scope

**v1 consists of the acceptance criteria above.** The application is a single-user personal tool with web administration interface, scheduled processing, stateful routing with conditions/actions, historical duplicate detection, quota awareness, decision evidence, statistics with temporal analytics, dry-run, backtesting, and source/channel analysis.

## Not This

- Continuous/live YouTube monitoring
- Automatic subscription removal
- Autonomous modification of routing rules based on analytics
- Automatic threshold optimization
- Machine-learning-based classification
- Generalized support for arbitrary content providers
- Social/community functionality
- Sophisticated recommendation AI
- Particular charting framework requirement
- Particular database or deployment architecture requirement

## Context

- This is a rebuild of an existing application with historical data that should be preserved where possible
- Initial primary use case is music discovery (high-volume, same song published by multiple channels under slightly different titles)
- Routing model must remain general enough to support non-music subscriptions
- Previous implementation used Levenshtein-style similarity with ~97% threshold; rebuilt system must provide equivalent capability without binding to that exact algorithm
- Application treats quota exhaustion as operational state, not error
- User retains authority over all policy decisions; system automates repetitive work but does not autonomously redefine what user wants
