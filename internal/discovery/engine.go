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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/titlekey"
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
	baseURL   string
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

// WithClientFactory replaces the legacy Run client factory. RunBound always
// constructs its own fenced client and ignores this option.
func WithClientFactory(f ClientFactory) Option {
	return func(e *Engine) {
		e.factory = f
	}
}

// WithBaseURL overrides the default production YouTube Data API base URL for
// this engine. Empty value keeps the production default.
func WithBaseURL(u string) Option {
	return func(e *Engine) {
		e.baseURL = u
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
		machine:   machine,
		ledger:    ledger,
		db:        db,
		logger:    slog.Default(),
		batchSize: 50,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.factory == nil {
		e.factory = e.defaultClientFactory
	}
	return e
}

func (e *Engine) defaultClientFactory(ctx context.Context, token *oauth2.Token, recorder youtube.CostRecorder) *youtube.Client {
	opts := []youtube.Option{youtube.WithRecorder(recorder)}
	if e.baseURL != "" {
		opts = append(opts, youtube.WithBaseURL(e.baseURL))
	}
	return youtube.NewClient(ctx, token, opts...)
}

// Result summarizes a discovery run.
type Result struct {
	SubscriptionsFetched int
	ChannelsFetched      int
	VideosDiscovered     int
	QuotaCost            int
	Deferred             bool
	DeferredReason       string

	// SourcesAttempted counts channels for which at least one uploads
	// request was made. SourcesCompleted counts those whose pagination
	// finished (including channels with no new uploads). SourcesFailedLocal
	// counts those skipped under the source-local rule (see isSourceLocal).
	SourcesAttempted   int
	SourcesCompleted   int
	SourcesFailedLocal int
	// ScanStopped is set when the uploads scan stopped early because of a
	// budget or admission refusal.
	ScanStopped bool
}

// ErrNoSourceScanned ends a run in which every attempted uploads source failed
// locally and nothing stopped the scan (BR-002 AC8). It carries the registered
// diagnostic code no_source_scanned.
var ErrNoSourceScanned = diag.WithCode(errors.New("discovery: no channel was scanned successfully"), "no_source_scanned")

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
	// The unguarded client cannot use the by-ID read, so this legacy path (no
	// production caller; the pipeline uses RunBound whenever it has a binding)
	// cannot learn true field presence.
	return e.runWithClient(ctx, runID, client, false, false)
}

// runWithClient runs one discovery. byID selects the guarded by-ID metadata
// read; isolateSources enables D1 source isolation. They are independent:
// neither is derived from the other.
func (e *Engine) runWithClient(ctx context.Context, runID int64, client *youtube.Client, byID, isolateSources bool) (*Result, error) {

	result := &Result{}
	runStart := time.Now().UTC()

	subChannelIDs, err := e.fetchSubscriptions(ctx, client, result)
	if err != nil {
		return e.preScanOutcome(ctx, runID, result, quota.OpSubscriptionsList, err)
	}

	channels, err := e.fetchChannels(ctx, client, subChannelIDs, result)
	if err != nil {
		return e.preScanOutcome(ctx, runID, result, quota.OpChannelsList, err)
	}

	newVideos, err := e.fetchNewVideos(ctx, runID, client, channels, result, isolateSources)
	if err != nil {
		return nil, err
	}
	if result.SourcesAttempted >= 1 && result.SourcesCompleted == 0 &&
		result.SourcesFailedLocal == result.SourcesAttempted && !result.ScanStopped {
		// A canceled run is interrupted, not "nothing scanned".
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("discovery: %w", err)
		}
		return nil, fmt.Errorf("discovery: %w", ErrNoSourceScanned)
	}

	if err := e.prefetchMetadata(ctx, client, newVideos, result, byID); err != nil {
		return nil, err
	}

	if err := e.persist(ctx, runID, runStart, newVideos); err != nil {
		return nil, err
	}
	result.VideosDiscovered = len(newVideos)

	e.logDiscoveryComplete(ctx, runID, result)

	return result, nil
}

func (e *Engine) logDiscoveryComplete(ctx context.Context, runID int64, result *Result) {
	e.logger.InfoContext(ctx, "discovery complete",
		"run_id", runID,
		"subscriptions", result.SubscriptionsFetched,
		"channels", result.ChannelsFetched,
		"videos", result.VideosDiscovered,
		"scan_outcome", scanOutcomeName(result),
	)
}

func scanOutcomeName(r *Result) string {
	if r.ScanStopped {
		return string(state.ScanOutcomeCutShort)
	}
	return string(state.ScanOutcomeCompleted)
}

// preScanQuotaDeferredReason is the fixed text for a quota stop before the
// channel scan. It carries no provider data.
const preScanQuotaDeferredReason = "discovery stopped: the daily YouTube quota was used up"

// preScanOutcome decides how discovery ends when subscriptions or channels
// could not be listed. A quota stop is not an error: the scan outcome is
// cut_short and discovery returns no videos (ARS-001 section 4.2). Any other
// error is returned unchanged.
//
// The terminal status for this case (completed + cut_short) is the dispatcher's
// assumption, pending the user's decision (ARS-001 Q-1). To change it, change
// only this function's return value and the Run Detail condition.
func (e *Engine) preScanOutcome(ctx context.Context, runID int64, result *Result, op quota.Operation, err error) (*Result, error) {
	if !quotaStop(err) {
		return nil, err
	}
	if serr := e.machine.SetDiscoveryScanOutcome(ctx, runID, state.ScanOutcomeCutShort); serr != nil {
		return nil, fmt.Errorf("discovery: %w", serr)
	}
	result.ScanStopped = true
	result.Deferred = true
	result.DeferredReason = preScanQuotaDeferredReason
	refusal := "provider"
	if errors.Is(err, auth.ErrQuotaRefused) {
		refusal = "admission"
	}
	e.logger.WarnContext(ctx, "discovery skipped: daily quota used up",
		"run_id", runID, "operation", string(op), "refusal", refusal)
	e.logDiscoveryComplete(ctx, runID, result)
	return result, nil
}

// quotaStop reports whether err means the daily quota is used up: a local
// admission or budget refusal, or an admitted provider error whose reason is
// quotaExceeded or dailyLimitExceeded (the single reasons youtube maps to the
// quota category). It uses typed fields only, never error text. A budget-check
// storage fault is not a quota stop.
func quotaStop(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, auth.ErrQuotaRefused) {
		return true
	}
	var api *youtube.APIError
	return errors.As(err, &api) && (api.Reason == "quotaExceeded" || api.Reason == "dailyLimitExceeded")
}

// RunBound performs production discovery using the caller's invocation binding.
// The binding is shared with subsequent production calls and is never renewed
// into a different authorization identity.
func (e *Engine) RunBound(ctx context.Context, runID int64, binding auth.AuthorizationBinding) (*Result, error) {
	resolver, ok := e.auth.(interface {
		Resolve(context.Context, auth.AuthorizationBinding) (*oauth2.Token, error)
	})
	if !ok {
		return nil, fmt.Errorf("discovery: authorization resolver unavailable")
	}
	resolve := func(ctx context.Context) (*oauth2.Token, error) { return resolver.Resolve(ctx, binding) }
	{
		dispatcher, dispatchOK := e.auth.(interface {
			DispatchWithQuota(context.Context, auth.AuthorizationBinding, *http.Request, http.RoundTripper, *quota.Ledger, quota.Operation) (*http.Response, auth.AdmissionReceipt, *quota.Hold, error)
			ObserveAccessRejection(context.Context, auth.AuthorizationBinding, auth.AdmissionReceipt) error
		})
		if !dispatchOK {
			return nil, fmt.Errorf("discovery: authorization dispatcher unavailable")
		}
		opts := []youtube.Option{
			youtube.WithQuotaReads(),
			youtube.WithRequestDispatcher(func(ctx context.Context, req *http.Request, transport http.RoundTripper) (*http.Response, youtube.RequestAdmission, error) {
				op, ok := youtube.ReadOperation(ctx)
				if !ok {
					return nil, youtube.RequestAdmission{}, fmt.Errorf("discovery: youtube read operation unavailable")
				}
				resp, receipt, hold, err := dispatcher.DispatchWithQuota(ctx, binding, req, transport, e.ledger, quota.Operation(op))
				admission := youtube.RequestAdmission{AuthorizationID: receipt.AuthorizationID, CredentialRevision: receipt.CredentialRevision, AdmittedAt: receipt.AdmittedAt}
				if hold != nil {
					admission.Finalize = func(ctx context.Context, category string, status int) error {
						return hold.Finalize(ctx, sql.NullInt64{Int64: runID, Valid: true}, quota.OutcomeCategory(category), sql.NullInt64{Int64: int64(status), Valid: status != 0})
					}
				}
				return resp, admission, err
			}),
			youtube.WithRejectionObserver(func(ctx context.Context, admission youtube.RequestAdmission) error {
				return dispatcher.ObserveAccessRejection(ctx, binding, auth.AdmissionReceipt{AuthorizationID: admission.AuthorizationID, CredentialRevision: admission.CredentialRevision, AdmittedAt: admission.AdmittedAt})
			}),
		}
		if e.baseURL != "" {
			opts = append(opts, youtube.WithBaseURL(e.baseURL))
		}
		client := youtube.NewAuthorizedClient(ctx, resolve, opts...)
		return e.runWithClient(ctx, runID, client, true, true)
	}
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
		// A refusal on any page ends discovery: the next call would be
		// refused too, and a partial subscription list is not a scan.
		if err := e.requireBudget(ctx, quota.OpSubscriptionsList, 1); err != nil {
			return nil, err
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
	VideoID   string
	ChannelID string
	// SourceChannelID is the scanned channel whose uploads list returned this
	// video. The floor advances only when it equals ChannelID.
	SourceChannelID     string
	ChannelName         string
	Title               string
	PublishedAt         time.Time
	ThumbnailURL        string
	Metadata            *youtube.VideoDetail
	EnumerationComplete bool
}

// isSourceLocal reports whether a ListPlaylistItems error is a complete 404
// whose every provider reason is playlistNotFound (ADR-001 D1, as corrected by
// review). It classifies only by typed fields, never by error text.
//
// The guarded client returns a bare *APIError for a clean, accounted 404. A
// *ReadError exists only when accounting, renewal or interruption failed, so
// any ReadError is not source-local and fails the run as it always has.
func isSourceLocal(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var re *youtube.ReadError
	if errors.As(err, &re) {
		return false
	}
	var api *youtube.APIError
	if !errors.As(err, &api) || api.Status != http.StatusNotFound || !api.Complete || len(api.Reasons) == 0 {
		return false
	}
	for _, r := range api.Reasons {
		if r != "playlistNotFound" {
			return false
		}
	}
	return true
}

// recordSourceFailure commits the durable incident row for channelID in its own
// short transaction (ARS-002 section 2), with no network I/O inside it.
func (e *Engine) recordSourceFailure(ctx context.Context, runID int64, channelID string) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin source failure transaction: %w", err)
	}
	if err := state.RecordUploadsSourceFailureTx(ctx, tx, runID, channelID, time.Now()); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit source failure: %w", err)
	}
	return nil
}

// logSourceSkipped records the one WARN per skipped source (ADR-001 D7), after
// its incident row has committed. It never logs err, the uploads playlist ID, a page token, the channel title or
// a URL.
func (e *Engine) logSourceSkipped(ctx context.Context, runID int64, ch youtube.Channel, err error, pagesDiscarded int) {
	attrs := []any{
		"run_id", runID,
		"diagnostic", diag.Encode(err, "discovery", "playlist_items_list"),
		"pages_discarded", pagesDiscarded,
	}
	// The validated channel ID may appear in this one server-log record (G3,
	// accepted). Only a valid ID is ever isolated, so only a valid ID is logged.
	attrs = append(attrs, "source_channel_id", ch.ID)
	var ref int64
	if qerr := e.db.QueryRowContext(ctx, `SELECT id FROM channel WHERE channel_id = ?;`, ch.ID).Scan(&ref); qerr == nil {
		attrs = append(attrs, "source_channel_ref", ref)
	}
	e.logger.WarnContext(ctx, "discovery: uploads source skipped", attrs...)
}

// fetchNewVideos scans each channel's uploads. isolate enables source-local
// skipping and is set only on the guarded RunBound path: the legacy unguarded
// Run never isolates a source.
func (e *Engine) fetchNewVideos(ctx context.Context, runID int64, client *youtube.Client, channels []youtube.Channel, result *Result, isolate bool) ([]*discoveredVideo, error) {
	var out []*discoveredVideo
	// A video listed twice (in one playlist page or by two channels) is
	// discovered once; persisting it twice would violate video.video_id. It
	// holds only items merged from completed or quota-stopped channels, so a
	// skipped channel never hides a video another channel lists.
	// The budget check exits only the page loop; later channels are refused
	// before any request, so they are never "attempted".
	listed := make(map[string]bool)
	// Written first, so every non-normal ending (error, stop, crash) leaves
	// cut_short with no per-error path (ARS-002 section 4.5).
	if err := e.machine.SetDiscoveryScanOutcome(ctx, runID, state.ScanOutcomeCutShort); err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	// A resumed run repeats discovery. A source already recorded as failed in
	// this run is not asked again and counts as attempted and failed locally.
	var recorded map[string]bool
	if isolate {
		var err error
		if recorded, err = state.PermanentSourceFailureChannels(ctx, e.db, runID); err != nil {
			return nil, fmt.Errorf("discovery: %w", err)
		}
	}
	for _, ch := range channels {
		if ch.UploadsPlaylistID == "" {
			e.logger.DebugContext(ctx, "discovery: channel has no uploads playlist")
			continue
		}
		if recorded[ch.ID] {
			result.SourcesAttempted++
			result.SourcesFailedLocal++
			continue
		}
		watermark, err := e.channelWatermark(ctx, ch.ID)
		if err != nil {
			return nil, fmt.Errorf("discovery: load watermark for %s: %w", ch.ID, err)
		}

		// The channel's items stay in a local buffer until its pagination
		// ends, so a source-local failure leaves no page behind.
		var buf []*discoveredVideo
		bufSeen := make(map[string]bool)
		var pageToken string
		complete, attempted, skipped, pages := false, false, false, 0
		for {
			if err := e.requireBudget(ctx, quota.OpPlaylistItemsList, 1); err != nil {
				if !quotaStop(err) {
					return nil, err
				}
				e.logger.WarnContext(ctx, "discovery: insufficient quota for playlist items",
					"diagnostic", diag.Encode(err, "discovery", "playlist_items_list"))
				result.ScanStopped = true
				break
			}
			if !attempted {
				attempted = true
				result.SourcesAttempted++
			}
			items, next, err := client.ListPlaylistItems(ctx, ch.UploadsPlaylistID, pageToken)
			if err != nil {
				if quotaStop(err) {
					result.ScanStopped = true
					break
				}
				// A skip that cannot be recorded must not happen: an invalid
				// channel ID is not isolated, and a failed insert fails the
				// run (ARS-002 section 2). The reason is fixed by
				// isSourceLocal and is in diag's provider-reason set.
				if isolate && isSourceLocal(ctx, err) && state.ValidSourceChannelID(ch.ID) {
					if rerr := e.recordSourceFailure(ctx, runID, ch.ID); rerr != nil {
						return nil, fmt.Errorf("discovery: record source failure for %s: %w", ch.ID, rerr)
					}
					result.SourcesFailedLocal++
					e.logSourceSkipped(ctx, runID, ch, err, pages)
					skipped = true
					break
				}
				return nil, fmt.Errorf("discovery: list playlist items for %s: %w", ch.ID, err)
			}
			pages++

			for _, it := range items {
				if watermark != nil && !it.PublishedAt.After(*watermark) {
					continue
				}
				exists, err := e.videoExists(ctx, it.VideoID)
				if err != nil {
					return nil, err
				}
				if exists || bufSeen[it.VideoID] {
					continue
				}
				bufSeen[it.VideoID] = true
				buf = append(buf, &discoveredVideo{
					VideoID:         it.VideoID,
					ChannelID:       it.ChannelID,
					SourceChannelID: ch.ID,
					ChannelName:     ch.Title,
					Title:           it.Title,
					PublishedAt:     it.PublishedAt,
					ThumbnailURL:    it.ThumbnailURL,
				})
			}

			pageToken = next
			if pageToken == "" {
				complete = true
				result.SourcesCompleted++
				break
			}
		}
		if skipped {
			continue
		}
		// A run-time timestamp is a safe watermark only after the uploads
		// playlist was fully enumerated, and only for the scanned channel's
		// own videos. Otherwise older unseen pages could be filtered forever
		// on the next run.
		for _, v := range buf {
			if listed[v.VideoID] {
				continue
			}
			listed[v.VideoID] = true
			v.EnumerationComplete = complete && v.ChannelID == v.SourceChannelID
			out = append(out, v)
		}
	}
	// The loop visited every in-scope channel with no budget or refusal break.
	if !result.ScanStopped {
		if err := e.machine.SetDiscoveryScanOutcome(ctx, runID, state.ScanOutcomeCompleted); err != nil {
			return nil, fmt.Errorf("discovery: %w", err)
		}
	}
	return out, nil
}

// prefetchMetadata attaches videos.list details to the discovered videos. A
// video whose batch was refused, skipped or unusable keeps no details, so its
// description and duration are stored as not supplied. With byID the read keeps
// true field presence (a real empty description or P0D duration stays a real
// value). Without it (the legacy unguarded client) GetVideos cannot tell an
// omitted field from an empty one, so only a non-empty string or non-zero
// duration counts as supplied.
func (e *Engine) prefetchMetadata(ctx context.Context, client *youtube.Client, videos []*discoveredVideo, result *Result, byID bool) error {
	ids := make([]string, 0, len(videos))
	seen := make(map[string]bool, len(videos))
	for _, v := range videos {
		if !seen[v.VideoID] {
			seen[v.VideoID] = true
			ids = append(ids, v.VideoID)
		}
	}
	batchSize := e.batchSize
	if byID && batchSize > 50 {
		batchSize = 50
	}

	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := e.requireBudget(ctx, quota.OpVideosList, 1); err != nil {
			e.logger.WarnContext(ctx, "discovery: insufficient quota for metadata fetch", "diagnostic", diag.Encode(err, "discovery", "videos_list"))
			break
		}
		var batch []youtube.VideoDetail
		var err error
		if byID {
			batch, _, err = client.GetVideosWithReturnedIDs(ctx, ids[i:end])
		} else {
			batch, err = client.GetVideos(ctx, ids[i:end])
			for j := range batch {
				d := &batch[j]
				d.Present = youtube.VideoDetailPresence{
					Title: d.Title != "", ChannelTitle: d.ChannelTitle != "",
					Description: d.Description != "", Duration: d.DurationSeconds != 0,
				}
			}
		}
		if err != nil {
			var readErr *youtube.ReadError
			var invalid *youtube.InvalidResponseError
			switch {
			case errors.Is(err, auth.ErrQuotaRefused):
				return nil
			case byID && !errors.As(err, &readErr) && errors.As(err, &invalid):
				// An unusable response means "not supplied" for this batch only.
				e.logger.WarnContext(ctx, "discovery: metadata response unusable", "count", end-i)
				continue
			}
			return fmt.Errorf("discovery: get videos metadata: %w", err)
		}
		byVideo := make(map[string]youtube.VideoDetail, len(batch))
		for _, d := range batch {
			byVideo[d.ID] = d
		}
		for _, v := range videos {
			if d, ok := byVideo[v.VideoID]; ok {
				d := d
				v.Metadata = &d
			}
		}
	}
	return nil
}

func (e *Engine) persist(ctx context.Context, runID int64, runStart time.Time, videos []*discoveredVideo) error {
	for _, v := range videos {
		tx, err := e.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("discovery: begin transaction: %w", err)
		}

		if _, err := e.machine.DiscoverVideoTx(ctx, tx, runID, v.VideoID, v.ChannelID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("discovery: persist video %s: %w", v.VideoID, err)
		}

		// The playlist snippet is real provider data, so it supplies title,
		// channel name, thumbnail and published time when there are no details
		// or the details lack that field. Description and duration come only
		// from details whose Present flag is set; otherwise they are NULL.
		title := v.Title
		channelName := v.ChannelName
		publishedAt := v.PublishedAt
		thumbnail := v.ThumbnailURL
		var description *string
		var duration *int

		if d := v.Metadata; d != nil {
			if d.Present.Title {
				title = d.Title
			}
			if d.Present.ChannelTitle {
				channelName = d.ChannelTitle
			}
			if d.Present.Description {
				description = &d.Description
			}
			if d.Present.Duration {
				duration = &d.DurationSeconds
			}
			publishedAt = d.PublishedAt
			if d.ThumbnailURL != "" {
				thumbnail = d.ThumbnailURL
			}
		}

		if _, err := duplicate.SaveFingerprint(ctx, tx, v.VideoID, v.ChannelID, "api_discovery", title, false, nil); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("discovery: save fingerprint for %s: %w", v.VideoID, err)
		}
		if err := e.upsertTransientMetadata(ctx, tx, v.VideoID, title, channelName, description, duration, thumbnail, publishedAt, runStart); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("discovery: save metadata for %s: %w", v.VideoID, err)
		}
		if err := e.upsertChannel(ctx, tx, v.ChannelID, channelName, runStart, v.EnumerationComplete); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("discovery: update channel %s: %w", v.ChannelID, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("discovery: commit video %s: %w", v.VideoID, err)
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
		return fmt.Errorf("discovery: insufficient quota for %s x%d: %w", op, calls, auth.ErrQuotaRefused)
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

// upsertTransientMetadata stores the Store A row. A nil description or duration
// is written as NULL ("not validly supplied"); a non-nil one is a real value,
// including "" and 0. Blank title, channel name and thumbnail are NULL too.
// It also rewrites the video's title search entry in tx.
func (e *Engine) upsertTransientMetadata(ctx context.Context, tx *sql.Tx, videoID, title, channelName string, description *string,
	duration *int, thumbnailURL string, publishedAt, fetchedAt time.Time) error {

	nullString := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	var descriptionArg, durationArg any
	if description != nil {
		descriptionArg = *description
	}
	if duration != nil {
		durationArg = *duration
	}
	expiresAt := fetchedAt.Add(30 * 24 * time.Hour)
	_, err := tx.ExecContext(ctx,
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
		videoID, nullString(title), nullString(channelName), descriptionArg, publishedAt.UTC(), durationArg,
		nullString(thumbnailURL), fetchedAt.UTC(), fetchedAt.UTC(), expiresAt.UTC(), true,
	)
	if err != nil {
		return err
	}
	// Same transaction (ARS-001 §10.3): the index entry follows the stored row,
	// so a kept expired_at or a blank title leaves no entry.
	return metadata.IndexTitleTx(ctx, tx, titlekey.NewFolder(), videoID, fetchedAt.UTC())
}

func (e *Engine) upsertChannel(ctx context.Context, tx *sql.Tx, channelID, channelName string, discoveredAt time.Time, enumerationComplete bool) error {
	var watermark any
	if enumerationComplete {
		watermark = discoveredAt.UTC()
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered)
		 VALUES (?, ?, ?, ?, 1)
		 ON CONFLICT(channel_id) DO UPDATE SET
		  channel_name = COALESCE(excluded.channel_name, channel.channel_name),
		  last_discovered_at = COALESCE(excluded.last_discovered_at, channel.last_discovered_at),
		  total_discovered = channel.total_discovered + 1;`,
		channelID, channelName, discoveredAt.UTC(), watermark,
	)
	return err
}

// compile-time interface checks.
var (
	_ TokenProvider = (*auth.Service)(nil)
)
