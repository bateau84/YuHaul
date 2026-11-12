---
type: requirement
title: BR-038 — Video Search by Title or Video ID
description: The user can find any video YuHaul knows by its video ID or a YouTube link, and by part of its title while the title is still held, with a total match count; search reads only local data and never calls YouTube.
tags: [requirement, videos, search, inspection, retention, priority:MUST]
timestamp: 2026-11-11T00:00:00Z
---

**Status:** accepted
**Priority:** MUST

## Statement

The system SHALL let the user search, from a Videos page in the administration interface, for a video that YuHaul knows. A video ID, or a YouTube link that contains one, SHALL take the user straight to that video. Any other text SHALL find the videos whose currently held title contains that text, ignoring letter case. Each result SHALL show what YuHaul knows about the video: its title, channel, latest production outcome, queue state and the runs that decided it, each linked to its decision detail. Search SHALL read only data YuHaul already holds. It SHALL make no YouTube call and SHALL NOT keep any title longer than the existing retention rules allow.

## Example

The user remembers a video called "Lofi Beats to Study To (Live Remix)". They type `lofi beats`. The page says that 3 videos match and lists them, newest discovery first. The second one shows: title "Lofi Beats to Study To (Live Remix)", labelled current/local; channel "ChillHop"; latest outcome *rejected* in run 58; queue state *none*; and links to its decisions in run 58 and in simulation run 61, which is labelled *simulation*.

The user also wants a video from three months ago. Its title expired 30 days after it was last fetched, so `lofi` does not find it. The page has always said that name search covers only videos whose titles are still held. The user pastes `https://youtu.be/dQw4w9WgXcQ?t=42`. The page opens that video. Its title shows as "unavailable", its channel ID is shown, and its outcome and runs are complete. YouTube received no request.

Typing `lo` shows "Type at least 3 characters, or a video ID". Pasting `https://www.youtube.com/watch?list=PL123` shows "This link does not contain a video ID".

## Definitions

- A *known video* has the meaning in [BR-037](br-037-requeue-stranded-videos.md): a video that production discovery has saved.
- An *imported reference* is a record added by a corpus import ([BR-009](br-009-historical-title-persistence-and-corpus-import.md), [OC-004](oc-004-imported-history-as-duplicate-reference.md)) whose video ID is not a known video. YuHaul holds its video ID, but not its title.
- A *searchable video* is a known video or an imported reference.
- A *held title* is a video's title in the local metadata that has not expired under the existing retention rules (30 days without refresh). A title whose metadata has expired, been deleted or was never fetched is not held. A title fingerprint, normalized title or duplicate-detection token is never a held title.
- A *video ID* is 11 characters, each a letter `A–Z` or `a–z`, a digit, `-` or `_`. Video IDs are case-sensitive: `dQw4w9WgXcQ` and `DQW4W9WGXCQ` are different IDs.
- A *recognized YouTube link* is a URL with scheme `http` or `https`, host `youtube.com`, `www.youtube.com`, `m.youtube.com` or `youtu.be`, in one of these forms:
  - `youtube.com/watch?v=<ID>` (other query parameters, in any order, are allowed);
  - `youtu.be/<ID>`;
  - `youtube.com/shorts/<ID>`.

  A trailing `/`, query string or `#` fragment after the ID is allowed.

  Text without a scheme, such as `youtu.be/dQw4w9WgXcQ` or `www.youtube.com/watch?v=dQw4w9WgXcQ`, is not a URL and is not a recognized YouTube link (user decision, 2026-11-11). It is ordinary search text and is handled by AC7–9.
- The *search text* is the input with leading and trailing whitespace removed.
- A *committed outcome* has the meaning in [BR-037](br-037-requeue-stranded-videos.md); outcome names follow [BR-032](br-032-processing-outcome-distinction.md).

## Acceptance criteria

### The page

1. **Where.** The administration interface SHALL have a Videos page with a search box, available with the same access as the rest of the administration interface. It SHALL be usable on desktop and mobile ([BR-029](br-029-responsive-administration-interface.md)).
2. **Coverage notice.** The Videos page SHALL say, visibly and without user action, that name search finds only videos whose titles YuHaul still holds, that titles are held for a limited time (30 days without refresh), and that searching by video ID or YouTube link always works.

### Classifying the input

3. **Empty.** When the search text is empty, the system SHALL perform no search, SHALL show no error, and SHALL invite the user to type a title or a video ID.
4. **Recognized link.** When the search text is a recognized YouTube link, the system SHALL extract its video ID and continue as in AC6.
5. **Unusable link.** When the search text is a URL but not a recognized YouTube link, or is a recognized form whose ID part is missing or is not a video ID, the system SHALL perform no search and SHALL say that the link does not contain a video ID YuHaul can use. It SHALL NOT fall back to name search on the URL text.
6. **Video ID.** When the search text is a video ID, or comes from AC4:
   - if a searchable video has exactly that ID, the system SHALL open that video's view (AC12) directly, without a result list;
   - otherwise, the system SHALL say that YuHaul knows no video with that ID. For search text typed as an ID (not from a link), it SHALL also run name search on the same text (AC7) and show any results under that message.

   A video ID SHALL be found whether or not its title is held.
7. **Name search.** Any other search text of at least 3 characters SHALL be a name search. It SHALL find every searchable video with a held title that contains the search text as a contiguous substring, ignoring letter case (Unicode case folding; accents are not removed: `cafe` does not match `Café`). Every character SHALL be matched literally, including `%`, `_`, `*`, quotes and repeated spaces.
8. **Too short.** Search text of 1 or 2 characters that is not a video ID SHALL NOT be searched. The system SHALL say that at least 3 characters, or a video ID, are needed.
9. **Too long.** Search text longer than 200 characters SHALL NOT be searched. The system SHALL say that the input is too long. No other input SHALL cause an unexplained error.

### Results

10. **No result.** A name search with no match SHALL say that no held title contains the text, and SHALL repeat that videos whose titles are no longer held can still be found by video ID.
11. **Result list.** Name-search results SHALL be ordered by most recent production discovery first; ties, and videos with no production discovery, SHALL be ordered by video ID. Results SHALL be shown in pages of at most 50. The user SHALL be able to move to the next and previous page. For data that does not change between page requests, the pages together SHALL contain every match exactly once. Each result SHALL show the fields in AC12 except the run list, and SHALL open that video's view.
12. **Video view.** The view of one video SHALL show:
    - **video ID**, always;
    - **title**: the held title, labelled as current/local information ([OC-005](oc-005-decision-evidence-attribution-and-availability.md) Guarantee 2), or "unavailable" when no title is held. It may say "expired" only when expiry is the known cause. A fingerprint or normalized title SHALL never be shown as a title;
    - **channel**: the held channel name, labelled current/local, when held; otherwise the channel ID when YuHaul holds it; otherwise "unavailable";
    - **latest outcome**: the most recently committed production outcome, the run that committed it and when; or *not yet evaluated* for a known video with no committed outcome ([BR-032](br-032-processing-outcome-distinction.md) AC2–3). An imported reference SHALL say that it is an imported duplicate reference that YuHaul has never processed;
    - **queue state**: the video's active queue item, if any, as pending or claimed in the deferred queue (with its reason and, when claimed, the claiming run) or active or claimed in the Errors queue; otherwise *removed by the user* when its most recent queue item was removed by the user; otherwise *none*;
    - **runs**: every run, production or simulation, that recorded a decision for the video, newest first. Each SHALL show the run ID, its mode and its outcome for the video, and SHALL link to that run's decision detail for the video ([BR-015](br-015-explainable-route-decisions.md), [BR-028](br-028-historical-run-inspection.md)).
13. **Simulation labelled.** Every run, outcome or decision that comes from a simulation (dry run or backtest, [BR-026](br-026-dry-run-evaluation.md), [BR-027](br-027-historical-backtesting.md)) SHALL be labelled as simulation wherever search shows it. A simulation result SHALL never be shown as the latest outcome or as a queue state. A video that appears only in simulation data SHALL be labelled simulation-only and SHALL show no production outcome.

### What search must not do

14. **No YouTube call.** Searching, paging, opening a video view and following its links SHALL make no YouTube request of any kind, and SHALL NOT trigger a metadata refresh.
15. **Read only.** Search SHALL NOT change any video, title, metadata expiry time, outcome, decision, queue item, run, send guard or quota count.
16. **No new retention.** Search SHALL NOT keep, copy or extend the lifetime of any title or channel name beyond the existing retention rules. When a title stops being held, a later search, page request or video view SHALL no longer find the video by that title or show that title.

### Speed

17. **Fast at scale.** Search SHALL meet [QS-001](qs-001-video-search-response-time.md) with about 100,000 or more searchable videos.

### Total count (user decision, 2026-11-11)

18. **Total shown.** Every page of name-search results SHALL show the total number of videos that match the search, together with the positions of the results on that page, for example "51–100 of 130". For data that does not change between page requests, the total SHALL be the same on every page and SHALL equal the number of matches that AC11's pages contain together. A name search with no match shows the AC10 message; it need not also show a total of 0.

## Derived from

- **Source.** The user's request of 2026-11-11, "I need to be able to search for a video, either by name or video id", relayed in the specification assignment, with proposed defaults the user may change. The original human conversation was not independently inspected.
  - Statement, AC6–7, AC12: search by name or by video ID.
  - Proposed defaults adopted: a Videos page with a search box (AC1); coverage of production-discovered videos and corpus-imported records, which are distinct (Definitions); an exact video ID or a pasted `watch?v=`, `youtu.be/` or `shorts/` link goes straight to the video (AC4, AC6); case-insensitive substring name search (AC7); results show title, channel, latest outcome, queue state and links to each run's decision detail (AC11–12); paged results (AC11).
  - Constraints stated in the assignment: titles only from held, unexpired metadata or other already retained title sources (Definitions, AC7, AC16); the page states the coverage limit (AC2); no YouTube calls (AC14); simulation data labelled (AC13); empty, too-short and invalid inputs handled (AC3, AC5, AC8–9); a performance expectation for about 100,000 videos (AC17, QS-001).
- **Which title sources may be searched (source check, 2026-11-11).** The assignment allowed "other already-retained title sources such as the duplicate-detection corpus or fingerprints" if the retention rules permit. They do not provide a searchable title:
  - The accepted [compliance analysis](../../architecture/yuhaul-youtube-compliance.md) and [system architecture](../../architecture/yuhaul-system-architecture.md) keep raw titles and channel names only in transient metadata, deleted or refreshed after 30 days. Imported corpus titles are turned into fingerprints and the raw titles are discarded. Source agrees: expiry clears the raw title (`internal/store/migrations/002_metadata_lifecycle.up.sql`, `internal/metadata/manager.go` `ExpireStale`), and corpus import stores no raw title (`internal/store/migrations/013_corpus_import_metadata.up.sql`). This is evidence, not the source of meaning.
  - Fingerprints are kept permanently only under the compliance analysis's functional reading: a lossy internal matching key that is "never displayed to users". [BR-009](br-009-historical-title-persistence-and-corpus-import.md) says its durable record serves duplicate detection, and whether it fits the expiry model is still open. [OC-005](oc-005-decision-evidence-attribution-and-availability.md) Guarantee 10 says retaining a provider value elsewhere does not exempt it from policy, and the architecture forbids showing a fingerprint as a title.
  - So the only held title is current, unexpired local metadata. Matching a name against fingerprints is excluded (AC7, AC12) and left as a user-owned question; see the index.
- **Necessary consequences.**
  - AC2 and AC10: name search cannot find a video whose title is no longer held, so the user must be told, or "no result" would be read as "YuHaul never saw it". The assignment requires the notice.
  - AC12's title label and "unavailable": [BR-015](br-015-explainable-route-decisions.md) AC7 and OC-005 Guarantees 2 and 9 already require this for inspection.
  - AC12's *not yet evaluated*: [BR-032](br-032-processing-outcome-distinction.md) AC2–3.
  - AC15–16: a search that refreshed, copied or extended titles would create new retention, which the assignment forbids.
  - AC6 case sensitivity: YouTube video IDs are case-sensitive, so a case-insensitive ID match could open the wrong video.
- **Specification choices**, within the assignment. The user may change any of them:
  - minimum of 3 characters for name search (AC8) and maximum of 200 (AC9);
  - 50 results per page, newest discovery first, ties by video ID (AC11);
  - an 11-character input that is not a known ID also runs name search (AC6), because the same text can be a title word;
  - only the three link forms named by the user are recognized; other YouTube links, such as `/embed/` or `/live/`, are reported as unusable (AC5);
  - Unicode case folding without accent removal (AC7);
  - queue-state categories reuse [BR-013](br-013-quota-limited-work-deferral.md), [BR-014](br-014-failed-work-retention-and-retry.md) and BR-037's *removed by the user* (AC12);
  - imported references are found by ID only, because YuHaul holds no title for them (Definitions, AC6, AC12);
  - priority MUST, like the neighbouring inspection requirements. The assignment did not state a priority.
- **User decisions of 2026-11-11, after review of the first draft, the admin interface design and the [video-search architecture](../../architecture/video-search/index.md)**, relayed in the requirements assignment (Loom Action b820f6ca, Plan revision 19). The original human conversation was not independently inspected.
  - *Links without a scheme* (Definitions): not recognized; unchanged from the first draft. This answers the architecture's question Q1. Such text is name-searched like any other text and usually finds nothing.
  - *Total count* (AC18): search shows the total number of matches. This answers the architecture's question Q2.
  - *Mobile navigation:* on mobile, the Videos page is reached through the "More" menu, as the admin interface design proposed. This confirms a design choice. It adds no acceptance criterion, because navigation placement belongs to the design; AC1's desktop and mobile obligation is unchanged.
  - *QS-001 load:* 150,000 held titles (see [QS-001](qs-001-video-search-response-time.md)).
- **Simulation-only videos (AC13).** A 2026-11-11 source check (BR-037, Derived from) found that dry runs evaluate saved production videos and do not save new ones. AC13 still covers any simulation-only video, so that the rule holds if one exists.

## Verified by

Use the real server path with migrated SQLite and a controlled YouTube provider that counts requests.

- **Seed data:** known videos with held titles, with expired metadata (title cleared) and with metadata never fetched; imported references; at least one video with only simulation decisions, if the data model allows it; videos in each queue state of AC12; videos with committed outcomes in each BR-032 category and with none; video IDs that differ only in letter case.
- **Input classes (AC3–9):** empty and whitespace-only; 1 and 2 characters; exactly 3; 200 and 201 characters; a known ID; the same ID in different letter case; an unknown 11-character ID that is also a title substring; each recognized link form with extra parameters, a fragment, `m.` and `www.` hosts and a trailing `/`; `youtube.com/watch?list=…` with no `v`; `youtu.be/` with a 10-character ID; `youtube.com/embed/<ID>`; `youtu.be/<ID>` and `www.youtube.com/watch?v=<ID>` without a scheme (name search, no video opened); a non-YouTube URL; text containing `%`, `_`, `*` and quotes. Each gives the outcome its AC states, and none gives an unexplained error.
- **Coverage (AC2, AC7, AC10, AC16):** a title substring finds the video while its metadata is held. Expire that metadata: the same search no longer finds it, the no-result message repeats the ID route, and search by its ID still opens it with title "unavailable". The notice is visible on page load. Search for a word that appears only in an imported reference's former title, or only in a fingerprint: no result.
- **Case and literals (AC6–7):** `LOFI` finds "Lofi Beats"; `cafe` does not find "Café"; `100%` does not match "1000"; a lower-cased ID does not open the upper-cased video.
- **Video view (AC12–13):** for each seeded state, check every field against the stored data. Simulation runs are labelled, never shown as latest outcome or queue state. No fingerprint text appears anywhere. Each run link opens that run's decision detail for the video.
- **Paging and total (AC11, AC18):** with 130 matches, pages hold 50, 50 and 30; the union is the 130 matches with no repeats, in the stated order. Each page shows a total of 130 and its own positions (1–50, 51–100, 101–130).
- **No call, no change (AC14–15):** run all of the above while the provider counts zero requests. Snapshot the database before and after: only unrelated rows may differ, and no metadata expiry time moves.
- **Speed (AC17):** as in QS-001.

## Depends on

- [BR-009](br-009-historical-title-persistence-and-corpus-import.md) and [OC-004](oc-004-imported-history-as-duplicate-reference.md) (imported references; durable titles serve duplicate detection only)
- [BR-015](br-015-explainable-route-decisions.md), [BR-028](br-028-historical-run-inspection.md) and [OC-005](oc-005-decision-evidence-attribution-and-availability.md) (decision detail; current/local and "unavailable" labels)
- [BR-032](br-032-processing-outcome-distinction.md) (outcome names; not yet evaluated)
- [BR-013](br-013-quota-limited-work-deferral.md), [BR-014](br-014-failed-work-retention-and-retry.md) and [BR-037](br-037-requeue-stranded-videos.md) (queue states; *known video*; *removed by the user*)
- [BR-026](br-026-dry-run-evaluation.md) and [BR-027](br-027-historical-backtesting.md) (simulation data)
- [BR-029](br-029-responsive-administration-interface.md) (desktop and mobile)
- [QS-001](qs-001-video-search-response-time.md) (speed)
