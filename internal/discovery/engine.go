// Package discovery implements YuHaul's Phase 1 discovery engine.
//
// It lists the authenticated user's YouTube subscriptions, fetches each
// channel's recent uploads via its cached uploads playlist, filters to videos
// published since the last per-channel watermark, persists new videos as
// not_yet_evaluated, pre-fetches full metadata via videos.list, and records
// every API call's quota cost.
package discovery

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/youtube"

	"golang.org/x/oauth2"
)

// TokenProvider returns the active OAuth2 token for the YouTube account.
type TokenProvider interface {
	ActiveToken(ctx context.Context) (*oauth2.Token, error)
}

// ClientFactory creates a YouTube API client for a run. The factory receives
// the current OAuth2 token and a cost recorder so the client can report quota
// consumption for every API call.
type ClientFactory func(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) *youtube.Client

// Engine orchestrates discovery.
type Engine struct {
	auth      TokenProvider
	factory   ClientFactory
	machine   *state.Machine
	ledger    *quota.Ledger
	db        *sql.DB
	logger    *slog.Logger
	batchSize int
}

// Option configures an Engine.
type Option func(*Engine)

// WithLogger sets the structured logger. If nil, the default logger is used.
func WithLogger(l *slog.Logger) Option {
	return func(e *Engine) {
		if l != nil {
			e.logger = l
		}
	}
}

// WithClientFactory replaces the default YouTube client factory. Tests use this
// to point the client at a mock server.
func WithClientFactory(f ClientFactory) Option {
	return func(e *Engine) {
		e.factory = f
	}
}

// WithBatchSize sets the maximum number of video IDs requested per videos.list
// call. YouTube allows up to 50; the default is 50.
func WithBatchSize(n int) Option {
	return func(e *Engine) {
		if n > 0 {
			e.batchSize = n
		}
	}
}

// NewEngine constructs a discovery Engine. The caller retains ownership of db.
func NewEngine(auth TokenProvider, machine *state.Machine, ledger *quota.Ledger, db *sql.DB, opts ...Option) *Engine {
	e := &Engine{
		auth:      auth,
		factory:   defaultClientFactory,
		machine:   machine,
		ledger:    ledger,
		db:        db,
		logger:    slog.Default(),
		batchSize: 50,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func defaultClientFactory(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) *youtube.Client {
	return youtube.NewClient(ctx, token, youtube.WithRecorder(recorder))
}

// Result summarizes a discovery run.
type Result struct {
	SubscriptionsFetched int
	ChannelsFetched      int
	VideosDiscovered     int
	QuotaCost            int
	Deferred             bool
	DeferredReason       string
}

// Run discovers new videos and persists them as not_yet_evaluated. It returns
// a summary of what was found and how much quota was consumed.
//
// The engine creates a fresh YouTube client per invocation and does not keep
// any persistent connection between runs.
func (e *Engine) Run(ctx context.Context, runID int64) (*Result, error) {
	token, err := e.auth.ActiveToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("discovery: no active token: %w", err)
	}

	recorder := func(ctx context.Context, op youtube.Operation, calls int) error {
		return e.ledger.RecordCalls(ctx, sql.NullInt64{Int64: runID, Valid: true}, quota.Operation(op), calls)
	}
	client := e.factory(ctx, token, recorder)

	result := &Result{}
	runStart := time.Now().UTC()

	subChannelIDs, err := e.fetchSubscriptions(ctx, client, result)
	if err != nil {
		return nil, err
	}

	channels, err := e.fetchChannels(ctx, client, subChannelIDs, result)
	if err != nil {
		return nil, err
	}

	newVideos, err := e.fetchNewVideos(ctx, client, channels, result)
	if err != nil {
		return nil, err
	}

	if err := e.prefetchMetadata(ctx, client, newVideos, result); err != nil {
		return nil, err
	}

	if err := e.persist(ctx, runID, runStart, newVideos); err != nil {
		return nil, err
	}
	result.VideosDiscovered = len(newVideos)

	e.logger.InfoContext(ctx, "discovery complete",
		"run_id", runID,
		"subscriptions", result.SubscriptionsFetched,
		"channels", result.ChannelsFetched,
		"videos", result.VideosDiscovered,
	)

	return result, nil
}

func (e *Engine) fetchSubscriptions(ctx context.Context, client *youtube.Client, result *Result) ([]string, error) {
	if err := e.requireBudget(ctx, quota.OpSubscriptionsList, 1); err != nil {
		return nil, err
	}

	var all []string
	var pageToken string
	for {
		resp, err := client.ListSubscriptions(ctx, pageToken)
		if err != nil {
			return nil, fmt.Errorf("discovery: list subscriptions: %w", err)
		}
		for _, s := range resp.Items {
			all = append(all, s.ChannelID)
		}
		result.SubscriptionsFetched += len(resp.Items)

		pageToken = resp.NextPageToken
		if pageToken == "" {
			break
		}
		if err := e.requireBudget(ctx, quota.OpSubscriptionsList, 1); err != nil {
			e.logger.WarnContext(ctx, "discovery: insufficient quota for next subscriptions page", "error", err)
			break
		}
	}
	return all, nil
}

func (e *Engine) fetchChannels(ctx context.Context, client *youtube.Client, channelIDs []string, result *Result) ([]youtube.Channel, error) {
	var all []youtube.Channel
	for i := 0; i < len(channelIDs); i += e.batchSize {
		end := i + e.batchSize
		if end > len(channelIDs) {
			end = len(channelIDs)
		}
		if err := e.requireBudget(ctx, quota.OpChannelsList, 1); err != nil {
			return nil, err
		}
		batch, err := client.GetChannels(ctx, channelIDs[i:end])
		if err != nil {
			return nil, fmt.Errorf("discovery: get channels: %w", err)
		}
		all = append(all, batch...)
	}
	result.ChannelsFetched = len(all)
	return all, nil
}

// discoveredVideo captures the data known about a video before metadata
// pre-fetch. The metadata field is populated when videos.list succeeds.
type discoveredVideo struct {
	VideoID      string
	ChannelID    string
	ChannelName  string
	Title        string
	PublishedAt  time.Time
	ThumbnailURL string
	Metadata     *youtube.VideoDetail
}

func (e *Engine) fetchNewVideos(ctx context.Context, client *youtube.Client, channels []youtube.Channel, result *Result) ([]*discoveredVideo, error) {
	var out []*discoveredVideo
	for _, ch := range channels {
		if ch.UploadsPlaylistID == "" {
			e.logger.DebugContext(ctx, "discovery: channel has no uploads playlist", "channel_id", ch.ID)
			continue
		}
		watermark, err := e.channelWatermark(ctx, ch.ID)
		if err != nil {
			return nil, fmt.Errorf("discovery: load watermark for %s: %w", ch.ID, err)
		}

		var pageToken string
		for {
			if err := e.requireBudget(ctx, quota.OpPlaylistItemsList, 1); err != nil {
				e.logger.WarnContext(ctx, "discovery: insufficient quota for playlist items",
					"channel_id", ch.ID, "error", err)
				break
			}
			items, next, err := client.ListPlaylistItems(ctx, ch.UploadsPlaylistID, pageToken)
			if err != nil {
				return nil, fmt.Errorf("discovery: list playlist items for %s: %w", ch.ID, err)
			}

			for _, it := range items {
				if watermark != nil && !it.PublishedAt.After(*watermark) {
					continue
				}
				exists, err := e.videoExists(ctx, it.VideoID)
				if err != nil {
					return nil, err
				}
				if exists {
					continue
				}
				out = append(out, &discoveredVideo{
					VideoID:      it.VideoID,
					ChannelID:    it.ChannelID,
					ChannelName:  ch.Title,
					Title:        it.Title,
					PublishedAt:  it.PublishedAt,
					ThumbnailURL: it.ThumbnailURL,
				})
			}

			pageToken = next
			if pageToken == "" {
				break
			}
		}
	}
	return out, nil
}

func (e *Engine) prefetchMetadata(ctx context.Context, client *youtube.Client, videos []*discoveredVideo, result *Result) error {
	ids := make([]string, 0, len(videos))
	for _, v := range videos {
		ids = append(ids, v.VideoID)
	}

	for i := 0; i < len(ids); i += e.batchSize {
		end := i + e.batchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := e.requireBudget(ctx, quota.OpVideosList, 1); err != nil {
			e.logger.WarnContext(ctx, "discovery: insufficient quota for metadata fetch", "error", err)
			break
		}
		batch, err := client.GetVideos(ctx, ids[i:end])
		if err != nil {
			return fmt.Errorf("discovery: get videos metadata: %w", err)
		}
		for _, d := range batch {
			for _, v := range videos {
				if v.VideoID == d.ID {
					v.Metadata = &d
					break
				}
			}
		}
	}
	return nil
}

func (e *Engine) persist(ctx context.Context, runID int64, runStart time.Time, videos []*discoveredVideo) error {
	for _, v := range videos {
		if _, err := e.machine.DiscoverVideo(ctx, runID, v.VideoID, v.ChannelID); err != nil {
			return fmt.Errorf("discovery: persist video %s: %w", v.VideoID, err)
		}

		title := v.Title
		channelName := v.ChannelName
		description := ""
		duration := 0
		publishedAt := v.PublishedAt
		thumbnail := v.ThumbnailURL

		if v.Metadata != nil {
			title = v.Metadata.Title
			channelName = v.Metadata.ChannelTitle
			description = v.Metadata.Description
			duration = v.Metadata.DurationSeconds
			publishedAt = v.Metadata.PublishedAt
			thumbnail = v.Metadata.ThumbnailURL
		}

		if _, err := duplicate.SaveFingerprint(ctx, e.db, v.VideoID, v.ChannelID, "api_discovery", title, false, nil); err != nil {
			return fmt.Errorf("discovery: save fingerprint for %s: %w", v.VideoID, err)
		}
		if err := e.upsertTransientMetadata(ctx, v.VideoID, title, channelName, description, duration, thumbnail, publishedAt, runStart); err != nil {
			return fmt.Errorf("discovery: save metadata for %s: %w", v.VideoID, err)
		}
		if err := e.upsertChannel(ctx, v.ChannelID, channelName, runStart); err != nil {
			return fmt.Errorf("discovery: update channel %s: %w", v.ChannelID, err)
		}
	}
	return nil
}

func (e *Engine) requireBudget(ctx context.Context, op quota.Operation, calls int) error {
	ok, _, err := e.ledger.CanAfford(ctx, op.UnitCost()*calls)
	if err != nil {
		return fmt.Errorf("discovery: budget check failed: %w", err)
	}
	if !ok {
		return fmt.Errorf("discovery: insufficient quota for %s x%d", op, calls)
	}
	return nil
}

func (e *Engine) channelWatermark(ctx context.Context, channelID string) (*time.Time, error) {
	var t sql.NullTime
	err := e.db.QueryRowContext(ctx,
		`SELECT last_discovered_at FROM channel WHERE channel_id = ?;`, channelID).Scan(&t)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if t.Valid {
		return &t.Time, nil
	}
	return nil, nil
}

func (e *Engine) videoExists(ctx context.Context, videoID string) (bool, error) {
	var id int64
	err := e.db.QueryRowContext(ctx,
		`SELECT id FROM video WHERE video_id = ?;`, videoID).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) upsertTransientMetadata(ctx context.Context, videoID, title, channelName, description string,
	duration int, thumbnailURL string, publishedAt, fetchedAt time.Time) error {

	expiresAt := fetchedAt.Add(30 * 24 * time.Hour)
	_, err := e.db.ExecContext(ctx,
		`INSERT INTO transient_metadata
		 (video_id, title, channel_name, description, published_at, duration_seconds,
		  thumbnail_url, fetched_at, refreshed_at, expires_at, is_active)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(video_id) DO UPDATE SET
		  title = excluded.title,
		  channel_name = excluded.channel_name,
		  description = excluded.description,
		  published_at = excluded.published_at,
		  duration_seconds = excluded.duration_seconds,
		  thumbnail_url = excluded.thumbnail_url,
		  fetched_at = excluded.fetched_at,
		  refreshed_at = excluded.refreshed_at,
		  expires_at = excluded.expires_at,
		  is_active = excluded.is_active;`,
		videoID, title, channelName, description, publishedAt.UTC(), duration,
		thumbnailURL, fetchedAt.UTC(), fetchedAt.UTC(), expiresAt.UTC(), true,
	)
	return err
}

func (e *Engine) upsertChannel(ctx context.Context, channelID, channelName string, discoveredAt time.Time) error {
	_, err := e.db.ExecContext(ctx,
		`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered)
		 VALUES (?, ?, ?, ?, 1)
		 ON CONFLICT(channel_id) DO UPDATE SET
		  channel_name = COALESCE(excluded.channel_name, channel.channel_name),
		  last_discovered_at = excluded.last_discovered_at,
		  total_discovered = channel.total_discovered + 1;`,
		channelID, channelName, discoveredAt.UTC(), discoveredAt.UTC(),
	)
	return err
}

// compile-time interface checks.
var (
	_ TokenProvider = (*auth.Service)(nil)
)
