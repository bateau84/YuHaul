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

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/youtube"
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

// LocalRow is the minimal transient_metadata projection used for local
// eligibility. Time columns are scanned as `any` because the schema has stored
// several layouts.
type LocalRow struct {
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

// FetchResult is one by-ID videos.list response (ARS-005 §6.1).
type FetchResult struct {
	// Details holds the items that carry usable details. A nil pointer field
	// means the response did not supply a valid value (not an empty value).
	Details []TransientMetadata
	// Returned lists the id of every raw item of the successful response,
	// including items without a usable detail.
	Returned []string
	// Complete is true only for a validated response, so an omitted ID is
	// proof of absence.
	Complete bool
	// Live has one classification per Returned id (BR-042).
	Live map[string]LiveStatus
}

// LiveStatus is the live-status classification of one returned video.
type LiveStatus = youtube.LiveStatus

// BoundFetcher reads metadata by ID through the shared bound, quota-accounted
// read seam, using the caller's authorization binding.
type BoundFetcher interface {
	FetchMetadataBound(ctx context.Context, binding auth.AuthorizationBinding, videoIDs []string) (FetchResult, error)
}

// FetchStatus is the per-input result of FetchForEvaluation. It describes the
// input, never permission to dispatch.
type FetchStatus string

const (
	FetchCurrent              FetchStatus = "current"
	FetchFetched              FetchStatus = "fetched"
	FetchUnusable             FetchStatus = "unusable"
	FetchAbsent               FetchStatus = "absent"
	FetchQuotaRefused         FetchStatus = "quota_refused"
	FetchAuthorizationPending FetchStatus = "authorization_pending"
	FetchRateLimited          FetchStatus = "rate_limited"
	FetchFailed               FetchStatus = "failed"
)

// InputRequirement names the route inputs one video needs. Presence and value
// are separate: a valid empty description or zero duration satisfies it.
type InputRequirement struct {
	VideoID                                   string
	Title, ChannelName, Description, Duration bool
	// Live forces a read: a stored row never satisfies it.
	Live bool
}

// ReadFacts preserves the typed facts of a failed metadata read for the run
// owner. None of them is rewritten into a per-video status.
type ReadFacts struct {
	// Err is the original error, unflattened, for errors.Is/As by the owner.
	Err error
	// Primary is the provider/transport/authorization cause, if any.
	Primary error
	// Accounting is a local quota/admission/accounting fault, if any.
	Accounting error
	// Renewal is a failed authorization-renewal observation, if any.
	Renewal error
	// Authorization is the vetted cause of an authorization refusal or
	// rejection (reconnect-required versus temporary), or "" when the read did
	// not fail on authorization. It never carries provider text or credentials.
	Authorization auth.AuthorizationCause
	// Interruption is a cancellation or deadline seen during the read.
	Interruption error
	// HTTPStatus is the response status, 0 when none.
	HTTPStatus int
	// Admitted is true when the failed read was an admitted send, so it counts
	// as an attempt. A refusal or failure before admission is not.
	Admitted bool
	// QuotaRefused is true for a known local pre-admission refusal or a
	// provider quotaExceeded/dailyLimitExceeded rejection.
	QuotaRefused bool
}

// FetchOutcome is the result of FetchForEvaluation.
type FetchOutcome struct {
	// Statuses has one entry per requested video.
	Statuses map[string]FetchStatus
	// Live holds the classification of each ID returned by a read in this
	// call. It is reported alongside Statuses, not merged into them.
	Live map[string]LiveStatus
	// Read is the first read failure that suspended fetching, or nil.
	Read *ReadFacts
	// Attempts is the number of admitted by-ID sends; a refusal or failure
	// before admission is not an attempt.
	Attempts int
	// Saved is the number of rows written to Store A.
	Saved int
	// Stop is the run's effective dispatch stop seen before a chunk (reader R5,
	// run-initiation ARS-005 section 6.1.3), or StopNone. The manager never
	// writes it; the run owner does (writers W1 and W3). It is separate from
	// Read: a stop is not a read failure and costs zero attempts.
	Stop quota.DispatchStop
}

// MetadataExpiredError indicates that Store A metadata has expired and the
// caller should fall back to the Store B fingerprint for display.
type MetadataExpiredError struct {
	VideoID string
}

func (e *MetadataExpiredError) Error() string {
	return "metadata expired for video " + e.VideoID
}
