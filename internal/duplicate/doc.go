// Package duplicate implements YuHaul's fingerprint normalization and duplicate
// detection engine.
//
// Detection runs in two passes:
//  1. Exact lookup: a TitleFingerprint row with the same video_id and
//     is_duplicate_reference=true.
//  2. Fuzzy lookup: an FTS5 query against fingerprint_tokens, followed by a
//     normalized-token-set Jaccard score against the duplicate-reference corpus.
//
// The duplicate-reference corpus is scoped to TitleFingerprint rows where
// is_duplicate_reference=true — i.e., videos that were routed to a playlist.
package duplicate
