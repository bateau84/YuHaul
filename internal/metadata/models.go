// Package metadata implements YuHaul's Store A metadata lifecycle.
//
// Store A holds transient YouTube API metadata subject to a 30-day retention
// policy. The Manager refreshes active records, expires stale ones, and
// supports on-demand refresh for evidence display. Expired records degrade to
// the permanent Store B fingerprint rather than retaining raw titles.
package metadata

import (
	"context"
	"time"
)

// TransientMetadata is a single Store A record.
type TransientMetadata struct {
	VideoID         string
	Title           *string
	ChannelName     *string
	Description     *string
	PublishedAt     *time.Time
	DurationSeconds *int
	ThumbnailURL    *string
	FetchedAt       time.Time
	RefreshedAt     *time.Time
	ExpiresAt       time.Time
	ExpiredAt       *time.Time
	IsActive        bool
}

// LocalDisplay is current Store A display context, never evidence of what a run evaluated.
type LocalDisplay struct {
	Attribution             string     `json:"attribution"`
	Title                   *string    `json:"title"`
	TitleAvailability       string     `json:"title_availability"`
	ChannelName             *string    `json:"channel_name"`
	ChannelNameAvailability string     `json:"channel_name_availability"`
	DurationSeconds         *int       `json:"duration_seconds"`
	DurationAvailability    string     `json:"duration_availability"`
	SourceFetchedAt         *time.Time `json:"source_fetched_at"`
}

// localDisplayRow is the minimal row projection used for manager-owned eligibility.
type localDisplayRow struct {
	VideoID                         string
	Title, ChannelName              *string
	DurationSeconds                 *int
	FetchedAt, ExpiresAt, ExpiredAt any
	IsActive                        bool
}

// RefreshCandidate is an active record eligible for refresh.
type RefreshCandidate struct {
	VideoID   string
	FetchedAt time.Time
	ExpiresAt time.Time
}

// Fetcher retrieves fresh metadata from YouTube for one or more video IDs.
// Implementations are provided by the discovery/youtube client.
type Fetcher interface {
	// FetchMetadata returns fresh metadata for the requested video IDs.
	// Callers may batch IDs; the implementation decides the actual batch size.
	FetchMetadata(ctx context.Context, videoIDs []string) ([]TransientMetadata, error)
}

// MetadataExpiredError indicates that Store A metadata has expired and the
// caller should fall back to the Store B fingerprint for display.
type MetadataExpiredError struct {
	VideoID string
}

func (e *MetadataExpiredError) Error() string {
	return "metadata expired for video " + e.VideoID
}
