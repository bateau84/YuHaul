// Package videosearch implements video search per ARS-001: input
// classification, title matching, and keyset paging. This file, match.go and
// cursor.go are pure and do not touch the database.
package videosearch

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Kind is the outcome of classifying the raw search text (ARS-001 §3).
type Kind string

const (
	KindEmpty        Kind = "empty"
	KindInvalidText  Kind = "invalid_text"
	KindTooLong      Kind = "too_long"
	KindUnusableLink Kind = "unusable_link"
	KindTooShort     Kind = "too_short"
	KindLinkID       Kind = "link_id"
	KindTypedID      Kind = "typed_id"
	KindName         Kind = "name"
)

// maxTextRunes is the longest accepted search text.
const maxTextRunes = 200

// Input is the classification of one search request.
type Input struct {
	Kind Kind
	// VideoID is set for KindLinkID and KindTypedID.
	VideoID string
	// Text is the trimmed text. It is set for KindName and KindTypedID; a typed
	// ID that is not found is also name-searched (ARS-001 §4.1).
	Text string
}

var (
	schemeRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://`)
	videoIDRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	shortLinkP = regexp.MustCompile(`^/([^/]+)/?$`)
	shortsP    = regexp.MustCompile(`^/shorts/([^/]+)/?$`)
)

// ValidVideoID reports whether s has the shape of a YouTube video ID.
func ValidVideoID(s string) bool { return videoIDRe.MatchString(s) }

// Classify decides what raw means. Steps run in the order of ARS-001 §3 and
// the first that applies decides.
func Classify(raw string) Input {
	t := strings.TrimSpace(raw)
	switch {
	case t == "":
		return Input{Kind: KindEmpty}
	case !utf8.ValidString(t):
		return Input{Kind: KindInvalidText}
	case utf8.RuneCountInString(t) > maxTextRunes:
		return Input{Kind: KindTooLong}
	case schemeRe.MatchString(t):
		if id, ok := linkVideoID(t); ok {
			return Input{Kind: KindLinkID, VideoID: id}
		}
		return Input{Kind: KindUnusableLink}
	case videoIDRe.MatchString(t):
		return Input{Kind: KindTypedID, VideoID: t, Text: t}
	case utf8.RuneCountInString(t) < 3:
		return Input{Kind: KindTooShort}
	default:
		return Input{Kind: KindName, Text: t}
	}
}

// linkVideoID applies the link rules of ARS-001 §3 to t, which has a scheme.
func linkVideoID(t string) (string, bool) {
	u, err := url.Parse(t)
	if err != nil {
		return "", false
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return "", false
	}
	if u.User != nil || u.Port() != "" {
		return "", false
	}
	p := u.EscapedPath()
	var candidate string
	switch strings.ToLower(u.Hostname()) {
	case "youtu.be":
		m := shortLinkP.FindStringSubmatch(p)
		if m == nil {
			return "", false
		}
		candidate = m[1]
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		if p == "/watch" || p == "/watch/" {
			v := u.Query()["v"]
			if len(v) != 1 {
				return "", false
			}
			candidate = v[0]
		} else if m := shortsP.FindStringSubmatch(p); m != nil {
			candidate = m[1]
		} else {
			return "", false
		}
	default:
		return "", false
	}
	if !videoIDRe.MatchString(candidate) {
		return "", false
	}
	return candidate, true
}
