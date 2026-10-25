// Package duplicate implements YuHaul's fingerprint normalization and duplicate
// detection engine.
//
// Detection runs in two passes against the duplicate-reference corpus:
//  1. Exact lookup: an eligible TitleFingerprint row whose canonical
//     fingerprint equals the evaluated title's fingerprint and whose video_id
//     differs from the evaluated video.
//  2. Fuzzy lookup: an FTS5 query against fingerprint_tokens, followed by a
//     normalized-token-set Jaccard score against the same corpus, again
//     excluding the evaluated video's own row.
//
// A reference carrying the same video identifier as the evaluated video is the
// video itself, not a historical original: it is never returned as a duplicate
// (BR-008 AC8, OC-004), so no self-match can be recorded as a duplicate event
// or earn discovery credit. Matching across distinct video identifiers is
// unchanged: exact matches are threshold-independent, fuzzy matches follow the
// configured threshold.
//
// The duplicate-reference corpus is scoped to TitleFingerprint rows where
// is_duplicate_reference=true. That flag is matching eligibility, not only
// "routed to a playlist": a native api_discovery row becomes eligible when it
// is routed, while a valid user_import row is eligible on import because the
// prior-system corpus is historical matching data (OC-004, ADR-013). Eligibility
// is monotonic across same-video_id upserts, and it is neither route completion
// nor discovery-credit evidence.
package duplicate
