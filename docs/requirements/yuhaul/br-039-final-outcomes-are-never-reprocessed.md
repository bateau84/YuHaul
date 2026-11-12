---
type: requirement
title: BR-039 — Final Outcomes Are Never Reprocessed or Overwritten
description: Only videos without a final outcome are ever routed again; a final outcome (accepted, rejected, excluded, unavailable on YouTube) never changes in either direction; a retry of a partly done video finishes only its missing actions under the original decision and never reverses a recorded success.
tags: [requirement, outcomes, lifecycle, retry, deferred-queue, error-queue, send-guard, idempotency, priority:MUST]
timestamp: 2026-11-12T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

Once a production run commits a final outcome for a video, the system SHALL NOT route that video again in production, by any means, and SHALL NOT change that outcome to any other outcome. Only videos without a final outcome may be evaluated again. When a video without a final outcome already has at least one recorded successful playlist addition, a later run or retry SHALL complete only the additions that are still missing, under the decision that produced the recorded success. It SHALL NOT decide the video's routes again, and it SHALL NOT remove, undo or contradict a recorded success.

## Example

Video V matches two routes in run 40, with targets "Lofi" and "Study". The addition to "Lofi" succeeds; the addition to "Study" is deferred because quota runs out. V has no final outcome; its outcome is deferred. The user then edits the routes so that V would match nothing. Run 41 claims V's deferred item. It does not evaluate V's routes again. It adds V to "Study" only, as run 40 decided, and V's outcome becomes accepted. The "Lofi" success stays recorded.

Video W was rejected in run 12. Later, a stale deferred item for W is claimed by run 50, whose routes would accept W. Run 50 does not evaluate W, sends nothing for it, and W stays rejected. The item stops being active and its history is kept. The same holds the other way round: an accepted video never becomes rejected, deferred, failed or not yet evaluated.

## Definitions

- A *final outcome* is one of these committed outcomes ([BR-032](br-032-processing-outcome-distinction.md) AC1): **accepted**, **rejected**, **excluded**, **unavailable on YouTube**.
- **Deferred**, **failed** and **not yet evaluated** are *not final*. A video whose only committed outcomes are deferred or failed, or that has none, is a *video without a final outcome*. The user's word "unfinished" in the 2026-11-12 decision means exactly this.
  - This is wider than *unfinished* in [BR-037](br-037-requeue-stranded-videos.md), which also excludes videos with a committed deferred or failed outcome. Every BR-037 *unfinished* or *stranded* video is a video without a final outcome. BR-037's definition is unchanged.
- To *route* a video is to evaluate its exclusions and route conditions, or to send a playlist addition for it, in production. Simulations ([BR-026](br-026-dry-run-evaluation.md), [BR-027](br-027-historical-backtesting.md)) do not route in this sense: they never commit a production outcome and never send.
- A *recorded success* is a playlist addition for a video and a target playlist that is recorded as completed ([BR-012](br-012-idempotent-playlist-actions.md) AC1).
- A video is *partly done* when it has no final outcome and has at least one recorded success.
- The *original decision* of a partly done video is the production route decision under which its recorded successes were made. Its *missing additions* are the additions that decision required and that have no recorded success.

## Acceptance criteria

### A final outcome is never reprocessed

1. **Nothing routes a final video.** For a video with a final outcome, none of these SHALL route it: a deferred-queue claim, an Errors-queue retry (automatic or by the user), a stop or discard handoff ([BR-035](br-035-stop-active-run.md), [BR-017](br-017-manual-run-initiation.md)), the re-queue action ([BR-037](br-037-requeue-stranded-videos.md)), a resumed run, startup recovery, or any production run, scheduled, manual or queue retry. No YouTube call SHALL be made for it on that account.
2. **A final outcome never changes, in either direction.** No operation SHALL replace a video's final outcome with another outcome, final or not. In particular it SHALL NOT be set to not yet evaluated, deferred or failed, and accepted and rejected SHALL NOT replace each other. The run that committed it SHALL keep showing it, and the video's latest production outcome ([BR-038](br-038-video-search-by-title-or-id.md)) SHALL stay that outcome.
3. **A later run records nothing over it.** A run that holds or claims a queue item for a video that already has a final outcome SHALL NOT commit any outcome for that video and SHALL NOT count it in its outcome counts.
4. **Its queue items are closed, not left active.** When a queue item belongs to a video that already has a final outcome, the system SHALL make the item no longer active when it is claimed or retried, keep the item's history, and record that it was not processed because the video already had a final outcome. A user-started Errors-queue retry of such a video SHALL tell the user so, and change nothing else. Such an item SHALL NOT stop a run from starting or claiming other items ([BR-035](br-035-stop-active-run.md) AC15).
5. **The re-queue action never queues a final video.** This already follows from BR-037's *stranded* definition and AC18, and holds unchanged.

### Videos without a final outcome

6. **They may be evaluated again.** A video without a final outcome that is not partly done MAY be evaluated again by the run that processes it, under that run's configuration, as [BR-035](br-035-stop-active-run.md) AC14 and [BR-037](br-037-requeue-stranded-videos.md) AC11 say. Its earlier decisions stay as history. This requirement does not oblige a re-evaluation; it only limits which videos can have one.
7. **A partly done video finishes under its original decision.** When a run or retry processes a partly done video, it SHALL:
   - not evaluate the video's exclusions or routes again, whatever the current configuration says;
   - attempt only the missing additions of the original decision, each under the existing send safety rules ([BR-012](br-012-idempotent-playlist-actions.md), [OC-006](oc-006-refresh-authorization-and-call-safety.md)); an addition those rules refuse SHALL NOT be sent;
   - not send again any addition that already has a recorded success.
8. **A recorded success is never reversed.** No run, retry, handoff, re-queue or user action described in the requirements SHALL remove a recorded success, mark it as failed, uncertain or not done, remove the video from that playlist, or commit an outcome for the video that states the addition did not happen.
9. **Outcome after finishing.** When every missing addition of a partly done video has a recorded success, the video's outcome SHALL be accepted, which is final. If a missing addition is deferred or fails, the video stays without a final outcome and is retained under the existing rules ([BR-013](br-013-quota-limited-work-deferral.md), [BR-014](br-014-failed-work-retention-and-retry.md), [BR-030](br-030-temporary-failure-work-preservation.md)), still as partly done, so AC7 applies to its next retry.
10. **Original decision unreadable.** If the original decision of a partly done video cannot be identified or read, the system SHALL NOT decide the video's routes again and SHALL NOT send any addition for it. The video SHALL stay without a final outcome, retained with a reason that the user can see, and SHALL NOT be lost ([BR-030](br-030-temporary-failure-work-preservation.md) AC5).

### Relation to existing rules

11. **The send guard still applies, and is not the only protection.** The per-addition send safety rules ([BR-012](br-012-idempotent-playlist-actions.md), [OC-006](oc-006-refresh-authorization-and-call-safety.md)) keep refusing a repeated or possibly sent addition. They protect one addition to one playlist. AC1–3 additionally protect the video's outcome and routing as a whole: a final video is not routed even where no send guard would refuse an addition, for example a rejected video, which has no addition to guard.
12. **Simulations are unaffected and change nothing.** Dry runs and backtests MAY evaluate videos that have a final outcome, as [BR-026](br-026-dry-run-evaluation.md) and [BR-027](br-027-historical-backtesting.md) allow. They SHALL NOT change any production outcome, queue item or recorded success.

## Derived from

- **Statement, definitions, AC1–3, AC6–9, AC11:** the user's decision of 2026-11-12, relayed in the requirements assignment; the original human conversation was not independently inspected. The assignment says: only unfinished videos are ever re-evaluated, and a final outcome is never overwritten; a video with a committed final outcome (accepted, rejected, excluded, unavailable on YouTube) is never routed again by any queue claim, retry, handoff, re-queue or run; its outcome is never changed, in either direction ("this also goes the other way around"); a partly done video, for example two targets where one succeeded and one was deferred, is finished by a retry that completes only the missing actions under the original decision, does not re-decide routes and never removes or reverses a recorded success.
- **Which outcomes are final.** Accepted, rejected, excluded and unavailable on YouTube are the outcomes named final in the assignment. Unavailable on YouTube is already final in [BR-032](br-032-processing-outcome-distinction.md) AC1 and [BR-037](br-037-requeue-stranded-videos.md) AC18. Deferred and failed are not final: [BR-013](br-013-quota-limited-work-deferral.md) and [BR-014](br-014-failed-work-retention-and-retry.md) keep them for retry, and [OC-002](oc-002-route-evaluation-outcome-semantics.md) Guarantee 8 gives "deferred in run N, accepted in run N+1" as an allowed change.
- **Necessary consequences:**
  - AC3: if a later run committed its own outcome for a final video, the video's latest outcome and run counts would contradict AC2.
  - AC4: without it, a queue item for a final video would either route the video (contradicting AC1) or stay active for ever and be claimed again by every run.
  - AC10: AC7 forbids re-deciding, and BR-030 AC5 forbids losing the video; holding it with a visible reason is the only behavior that keeps both.
  - AC12: BR-026 and BR-027 are accepted and evaluate stored production videos; the user's rule concerns production routing and outcomes.
- **Specification choices:**
  - The *original decision* is the decision that produced the recorded success, so that any further additions agree with what was already done.
  - AC9: finishing every missing addition gives accepted, which is what [OC-002](oc-002-route-evaluation-outcome-semantics.md) Guarantee 2 calls a matched video whose additions were executed.
  - AC4's closing record and the message to the user, so the user can see why an item ended without processing.
  - Priority MUST, matching BR-032 and the run-control requirements; the assignment named no priority.
- **Evidence, not source.** A debugger reported, and a source read on 2026-11-12 confirmed, that a deferred-queue claim that no other run owns sets the video's `processing_state` and `final_outcome` to `not_yet_evaluated` and clears `completed_at` (`internal/queue/retry.go`, the `UPDATE video SET processing_state = 'not_yet_evaluated', final_outcome = 'not_yet_evaluated' …` statement near line 230). The receiving run then routes the video under its own configuration. For a video with a final outcome, or a partly done video, this conflicts with AC1–2 and AC7. This finding exposed the gap; it is not a source of product meaning.

## Verified by

Use the real server path with migrated SQLite and a controlled YouTube provider that counts requests.

- **Final videos are not routed (AC1–4):** seed one video for each final outcome (accepted, rejected, excluded, unavailable on YouTube), each with a pending deferred item, and a further accepted and rejected video each with an active Errors-queue entry. Change the routes so every seed would get a different outcome. Start a new scheduled run, a new Run Now run, a queue-retry run and a user Errors-queue retry; also run the stop handoff, the re-queue action and startup recovery with a resumable run that lists these videos. For each: the provider records no request for these videos; every outcome is unchanged, including in the committing run and as the latest outcome; no later run commits or counts an outcome for them; each item is no longer active, its history is kept and the reason is recorded; the user retry reports that the video already has a final outcome; the runs still start and claim other items.
- **Both directions (AC2):** for an accepted video, configuration that would reject it; for a rejected video, configuration that would accept it. Neither outcome changes.
- **Partly done (AC7–9):** seed the Example's video V with a recorded success to "Lofi" and a deferred addition to "Study", then change routes so V would match nothing. The next run sends exactly one addition, to "Study"; no route is evaluated; "Lofi" is not sent again and its success stays recorded; V becomes accepted. Repeat with the "Study" addition failing permanently: V is failed, in the Errors queue, "Lofi" stays recorded, and a user retry again sends only "Study".
- **Send safety still applies (AC7, AC11):** give the missing addition a held send guard: it is not sent and the video stays without a final outcome.
- **Unreadable decision (AC10):** remove the original decision's readable configuration: nothing is sent, no route is evaluated, the video stays retained with a visible reason.
- **Not partly done (AC6):** a deferred video with a decision and no recorded success is evaluated under the receiving run's configuration, as BR-035 AC14 and BR-037 AC11 require.
- **Simulation (AC12):** a dry run and a backtest over the final videos report their simulated results; every production outcome, queue item and recorded success is unchanged.
- **Races:** race a queue claim of a video against another run committing its final outcome, at least 100 times. Every result matches one serial order: either the claim routes the video before the outcome is committed, or the video is never routed after it. The final outcome is never replaced.

## Depends on

- [BR-032](br-032-processing-outcome-distinction.md) and [OC-002](oc-002-route-evaluation-outcome-semantics.md) (outcome categories; which are final)
- [BR-012](br-012-idempotent-playlist-actions.md) and [OC-006](oc-006-refresh-authorization-and-call-safety.md) (recorded successes, send guards and the veto)
- [BR-013](br-013-quota-limited-work-deferral.md), [BR-014](br-014-failed-work-retention-and-retry.md), [BR-030](br-030-temporary-failure-work-preservation.md) (retention of videos without a final outcome)
- [BR-035](br-035-stop-active-run.md) AC14–15, [BR-017](br-017-manual-run-initiation.md), [BR-037](br-037-requeue-stranded-videos.md) AC11 and AC18 (claims, handoffs, resume and re-queue that this requirement limits)
- [BR-026](br-026-dry-run-evaluation.md), [BR-027](br-027-historical-backtesting.md) (simulations, AC12)
