# Run 14 recovery — provisional contract

The goal is to make the saved unfinished work recoverable without pretending discovery finished, repeating playlist additions, or spending an unapproved amount of quota. For example, an approved small recovery slice can finish some videos and retain the remainder; run 14 still records its original discovery failure, not a successful run caused by later processing.

**Limit:** this is a specification proposal for tested recovery software, not approval to implement a new bulk capability, repair the live database, restart the service, or start playlist actions. No accepted BR is amended. The reported 19,615 pending videos and run status are incident evidence supplied by the caller, not independently refreshed observations.

**User-owned choices:** whether to authorize a recovery capability and later live activation; original-run continuation versus new-run evaluation; batch size and request budget; saved backlog first versus additional discovery. Existing snapshot semantics constrain those choices: a resumed original run keeps its original snapshot, and a fresh receiving run captures its own configuration. Replacing run 14's snapshot is not an available policy shortcut.

## Inventory

- [OC-001 — Controlled recovery handoff](oc-001-controlled-orphaned-work-recovery.md) — provisional, conditional contract and minimal read-only precondition check.
- [Change log](log.md)

## Status and dependencies

Counts: zero new BRs, zero QSs, one provisional OC. Accepted preservation, idempotency, quota, authority and truthful-history obligations remain binding; the new incident-specific flow and its strict authorized-slice bound remain proposed. No unconditional progress or completion guarantee is made before authority, provider availability and retained inputs are established.

The [architect's incident recommendation](../../architecture/run14-recovery/decisions/adr-001-run14-evidence-gated-recovery.md) proposes a retention handoff and existing explicit queue retry, not a public bulk admission path. Architect owns realization and uncertain-provider-attempt reconciliation; product owner owns scope/configuration/spend choices; operator owns fresh environment evidence and separately authorized activation. Parent session owns forwarding these questions and coordinating any shared navigation changes.

Read-only assessment can proceed without settling those choices. Mutating implementation scope and live execution cannot be inferred from approval of scheduling in general. Date filtering and the two-job coordinator are outside this proposal.
