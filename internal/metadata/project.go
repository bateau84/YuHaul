package metadata

import (
	"strings"
	"time"
)

// ProjectLocal projects one stored row at cutoff. It is the single rule for
// what a row may display: ReadLocalDisplay and the video search both use it.
func ProjectLocal(row LocalRow, cutoff time.Time) LocalDisplay {
	fetched, fetchedOK := ParseLocalTime(row.FetchedAt)
	expires, expiresOK := ParseLocalTime(row.ExpiresAt)
	_, expiredOK := ParseLocalTime(row.ExpiredAt)
	expiredMarkerPresent := row.ExpiredAt != nil
	knownExpired := expiredOK || (expiresOK && !expires.After(cutoff))
	eligibleTime := fetchedOK && expiresOK && fetched.After(time.Time{}) && !fetched.After(cutoff) && expires.After(fetched)
	eligible := row.IsActive && !expiredMarkerPresent && !knownExpired && eligibleTime
	item := LocalDisplay{Attribution: "current_local_not_necessarily_evaluated", TitleAvailability: "unknown", ChannelNameAvailability: "unknown", DurationAvailability: "unknown"}
	if eligible {
		item.SourceFetchedAt = &fetched
	}
	projectString := func(value *string) (*string, string) {
		if knownExpired {
			return nil, "known_expired"
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			return nil, "unknown"
		}
		if !eligible {
			return nil, "withheld"
		}
		return value, "current_local"
	}
	item.Title, item.TitleAvailability = projectString(row.Title)
	item.ChannelName, item.ChannelNameAvailability = projectString(row.ChannelName)
	if knownExpired {
		item.DurationAvailability = "known_expired"
	} else if row.DurationSeconds != nil {
		if !eligible {
			item.DurationAvailability = "withheld"
		} else {
			item.DurationSeconds = row.DurationSeconds
			item.DurationAvailability = "current_local"
		}
	}
	return item
}

// Held reports whether the row's title may be shown and searched at cutoff.
// It is true exactly when ProjectLocal(row, cutoff).TitleAvailability is
// "current_local".
func Held(row LocalRow, cutoff time.Time) bool {
	return ProjectLocal(row, cutoff).TitleAvailability == "current_local"
}
