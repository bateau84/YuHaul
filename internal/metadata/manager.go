package metadata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/youtube"
)

// DefaultTTL is the default metadata retention window (30 days).
const DefaultTTL = 30 * 24 * time.Hour

// DefaultRefreshBatchSize is the number of video IDs requested per refresh batch.
const DefaultRefreshBatchSize = 50

// Manager refreshes active Store A records and expires stale ones.
type Manager struct {
	store     *Store
	fetcher   Fetcher
	bound     BoundFetcher
	ledger    *quota.Ledger
	logger    *slog.Logger
	ttl       time.Duration
	batchSize int
	now       func() time.Time

	// suspended is the run-long fetch suspension (ARS-005 section 6.2, B7),
	// keyed by the run in the context's quota scope.
	suspendMu sync.Mutex
	suspended map[int64]ReadFacts
}

// maxSuspendedRuns bounds suspended. Run IDs only grow, so the lowest is the
// oldest run and the first to go.
const maxSuspendedRuns = 32

// Option configures a Manager.
type Option func(*Manager)

// WithLogger sets the logger used by the manager.
func WithLogger(logger *slog.Logger) Option {
	return func(m *Manager) {
		if logger != nil {
			m.logger = logger
		}
	}
}

// WithTTL sets the retention window for refreshed metadata.
func WithTTL(ttl time.Duration) Option {
	return func(m *Manager) {
		if ttl > 0 {
			m.ttl = ttl
		}
	}
}

// WithBatchSize sets the number of video IDs passed to the fetcher at once.
func WithBatchSize(n int) Option {
	return func(m *Manager) {
		if n > 0 {
			m.batchSize = n
		}
	}
}

// WithNow sets the clock function used for time-sensitive operations.
func WithNow(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// WithBoundFetcher sets the bound, quota-accounted by-ID read used by
// FetchForEvaluation.
func WithBoundFetcher(f BoundFetcher) Option {
	return func(m *Manager) {
		m.bound = f
	}
}

// NewManager returns a lifecycle manager backed by store and ledger.
// The fetcher is used for both scheduled and on-demand refresh.
func NewManager(store *Store, fetcher Fetcher, ledger *quota.Ledger, opts ...Option) *Manager {
	m := &Manager{
		store:     store,
		fetcher:   fetcher,
		ledger:    ledger,
		logger:    slog.Default(),
		ttl:       DefaultTTL,
		batchSize: DefaultRefreshBatchSize,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// RunLifecycle is the metadata sweep. It is safe to call repeatedly (e.g., at
// the start of each processing run). In order (ARS-001 §10.4):
//
//  1. ExpireStale, in chunks of IndexWriteBatch;
//  2. ReconcileSearchIndex, even when nothing expired or ExpireStale failed;
//  3. OptimizeSearchIndex, every sweep, even when step 1 or 2 failed;
//  4. RefreshRoutedNearExpiry, only when ExpireStale succeeded. The hybrid
//     policy refreshes routed videos automatically; older/unrouted videos are
//     refreshed only on demand.
//
// Every step runs or is skipped on those conditions alone; the errors of the
// steps that ran are joined.
func (m *Manager) RunLifecycle(ctx context.Context) error {
	var errs []error

	expired, expireErr := m.ExpireStale(ctx)
	if expireErr != nil {
		errs = append(errs, expireErr)
	}
	m.logger.InfoContext(ctx, "metadata lifecycle expired stale records", "count", expired, "failed", expireErr != nil)

	db := m.store.DB()
	rec, err := ReconcileSearchIndex(ctx, db, m.now().UTC())
	if err != nil {
		errs = append(errs, fmt.Errorf("metadata: reconcile search index: %w", err))
	}
	m.logger.InfoContext(ctx, "metadata lifecycle reconciled search index",
		"orphans_removed", rec.OrphansRemoved, "purged", rec.Purged, "added", rec.Added,
		"batches", rec.Batches, "longest_hold", rec.LongestHold, "failed", err != nil)

	opt, err := OptimizeSearchIndex(ctx, db)
	if err != nil {
		errs = append(errs, fmt.Errorf("metadata: optimize search index: %w", err))
	}
	level := slog.LevelInfo
	if !opt.Complete {
		// Removed trigrams stay in the segments until a complete optimize (I2b).
		level = slog.LevelWarn
	}
	m.logger.Log(ctx, level, "metadata lifecycle optimized search index",
		"steps", opt.Steps, "complete", opt.Complete, "longest", opt.Longest,
		"total", opt.Total, "failed", err != nil)

	if expireErr == nil {
		refreshed, err := m.RefreshRoutedNearExpiry(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		m.logger.InfoContext(ctx, "metadata lifecycle refreshed routed records", "count", refreshed, "failed", err != nil)
	}
	return errors.Join(errs...)
}

// CleanupExpired deletes metadata records older than the configured TTL, at
// most IndexWriteBatch (200) per call, so one call purges one batch of search
// entries (ARS-001 §10.10). Call it again while it returns IndexWriteBatch. It
// performs only local DELETE operations and never consumes API quota.
// Returns the number of records deleted.
func (m *Manager) CleanupExpired(ctx context.Context) (int, error) {
	cutoff := m.now().UTC().Add(-m.ttl)
	return m.store.CleanupExpired(ctx, cutoff, 10000)
}

// ExpireStale marks active records past expires_at as expired, clearing raw
// metadata fields. It lists up to 10,000 IDs and expires them in chunks of at
// most IndexWriteBatch, one transaction per chunk, so no write lock is held
// for long (ARS-001 §10.10). It returns the number of records expired. If a
// chunk fails it returns the count of the chunks already committed with the
// error; those stay committed and the next sweep continues.
func (m *Manager) ExpireStale(ctx context.Context) (int, error) {
	now := m.now().UTC()
	ids, err := m.store.ListActivePastExpiry(ctx, now, 10000)
	if err != nil {
		return 0, err
	}
	expired := 0
	for len(ids) > 0 {
		n := min(IndexWriteBatch, len(ids))
		if err := m.store.Expire(ctx, ids[:n], now); err != nil {
			return expired, err
		}
		expired += n
		ids = ids[n:]
	}
	return expired, nil
}

// RefreshRoutedNearExpiry refreshes active records for routed videos whose
// expiry is within the next 7 days. It returns the number of records refreshed.
func (m *Manager) RefreshRoutedNearExpiry(ctx context.Context) (int, error) {
	const window = 7 * 24 * time.Hour
	return m.refreshCandidates(ctx, func(ctx context.Context, cutoff time.Time, limit int) ([]RefreshCandidate, error) {
		return m.store.ListActiveRoutedBefore(ctx, cutoff, limit)
	}, window)
}

// RefreshActiveNearExpiry refreshes all active records nearing expiry,
// regardless of routing status. Prefer RefreshRoutedNearExpiry for the hybrid
// policy; this is useful for tests and backfills.
func (m *Manager) RefreshActiveNearExpiry(ctx context.Context) (int, error) {
	const window = 7 * 24 * time.Hour
	return m.refreshCandidates(ctx, m.store.ListActiveBefore, window)
}

// RefreshOnDemand re-fetches metadata for a single video and restores display.
// The provider adapter records actual calls in the ledger.
func (m *Manager) RefreshOnDemand(ctx context.Context, videoID string) error {
	cost := quota.OpVideosList.UnitCost()
	ok, _, err := m.ledger.CanAfford(ctx, cost)
	if err != nil {
		return fmt.Errorf("metadata: check quota for on-demand refresh: %w", err)
	}
	if !ok {
		m.logger.WarnContext(ctx, "on-demand metadata refresh deferred: quota exhausted")
		return nil
	}

	fresh, err := m.fetcher.FetchMetadata(ctx, []string{videoID})
	if err != nil {
		return fmt.Errorf("metadata: fetch on-demand refresh for %s: %w", videoID, err)
	}
	if len(fresh) == 0 {
		return fmt.Errorf("metadata: on-demand refresh for %s returned no data", videoID)
	}

	now := m.now().UTC()
	if err := m.store.Refresh(ctx, fresh, m.ttl, now); err != nil {
		return err
	}

	return nil
}

// ReadLocalDisplay performs a local-only, bounded metadata read for run members.
func (m *Manager) ReadLocalDisplay(ctx context.Context, ids []string) (map[string]LocalDisplay, error) {
	if len(ids) > 100 {
		return nil, fmt.Errorf("metadata: local display batch exceeds 100 IDs")
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	rows, err := m.store.readLocalDisplayRows(ctx, unique)
	if err != nil {
		return nil, err
	}
	// One cutoff is captured only after the database result set has been fully consumed.
	cutoff := m.now().UTC()
	out := make(map[string]LocalDisplay, len(unique))
	for _, id := range unique {
		out[id] = LocalDisplay{Attribution: "current_local_not_necessarily_evaluated", TitleAvailability: "unknown", ChannelNameAvailability: "unknown", DurationAvailability: "unknown"}
	}
	for _, row := range rows {
		out[row.VideoID] = ProjectLocal(row, cutoff)
	}
	return out, nil
}

// ParseLocalTime parses a stored timestamp in any layout the schema has used.
// Zero and unparseable values report false.
func ParseLocalTime(value any) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return time.Time{}, false
		}
		return v.UTC(), true
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, v); err == nil && !parsed.IsZero() {
				return parsed.UTC(), true
			}
		}
	case []byte:
		return ParseLocalTime(string(v))
	}
	return time.Time{}, false
}

// GetDisplayMetadata returns active metadata for a video. If the record has
// expired, it returns [MetadataExpiredError] so the caller can degrade to the
// Store B fingerprint.
func (m *Manager) GetDisplayMetadata(ctx context.Context, videoID string) (*TransientMetadata, error) {
	meta, err := m.store.Get(ctx, videoID)
	if err != nil {
		return nil, err
	}
	now := m.now().UTC()
	if !meta.IsActive || meta.ExpiredAt != nil || meta.ExpiresAt.Before(now) {
		return nil, &MetadataExpiredError{VideoID: videoID}
	}
	return meta, nil
}

// GetDisplayTitle returns the best available human-readable title for a video.
// Active metadata is preferred. If the metadata has expired or is missing, the
// title degrades to the Store B fingerprint hash. If no fingerprint exists, a
// fixed degraded label is returned.
func (m *Manager) GetDisplayTitle(ctx context.Context, videoID string) (string, error) {
	meta, err := m.store.Get(ctx, videoID)
	if err == nil {
		now := m.now().UTC()
		if meta.IsActive && meta.ExpiredAt == nil && !meta.ExpiresAt.Before(now) && meta.Title != nil {
			return *meta.Title, nil
		}
	}

	fp, err := m.store.GetFingerprint(ctx, videoID)
	if err != nil {
		return "", err
	}
	if fp != "" {
		return fp, nil
	}
	return "[metadata expired]", nil
}

// CountStaleActive returns the number of active records whose expires_at is
// before now. It is used by the falsification test for the 30-day TTL.
func (m *Manager) CountStaleActive(ctx context.Context) (int, error) {
	now := m.now().UTC()
	var count int
	err := m.store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM transient_metadata
		WHERE is_active = 1 AND expires_at < ?;`, now).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("metadata: count stale active: %w", err)
	}
	return count, nil
}

type candidateSource func(ctx context.Context, cutoff time.Time, limit int) ([]RefreshCandidate, error)

func (m *Manager) refreshCandidates(ctx context.Context, src candidateSource, window time.Duration) (int, error) {
	now := m.now().UTC()
	cutoff := now.Add(window)

	candidates, err := src(ctx, cutoff, 10000)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	refreshed := 0
	for i := 0; i < len(candidates); i += m.batchSize {
		end := i + m.batchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[i:end]
		ids := make([]string, len(batch))
		for j, c := range batch {
			ids[j] = c.VideoID
		}

		cost := quota.OpVideosList.UnitCost()
		ok, _, err := m.ledger.CanAfford(ctx, cost)
		if err != nil {
			// A preflight read error is a local accounting fault, not a
			// refusal (ARS-005 section 6.1). It stops the sweep and, through
			// the run owner, every later provider request of the run.
			return refreshed, fmt.Errorf("metadata: check quota for refresh: %w", errors.Join(auth.ErrQuotaAccounting, err))
		}
		if !ok {
			m.logger.WarnContext(ctx, "metadata refresh deferred: quota exhausted",
				"batch_size", len(ids))
			return refreshed, nil
		}

		fresh, err := m.fetcher.FetchMetadata(ctx, ids)
		if err != nil {
			return refreshed, fmt.Errorf("metadata: fetch refresh batch: %w", err)
		}

		if err := m.store.Refresh(ctx, fresh, m.ttl, now); err != nil {
			return refreshed, err
		}

		refreshed += len(fresh)
	}
	return refreshed, nil
}

// IsMetadataExpired reports whether err is a metadata-expired error.
func IsMetadataExpired(err error) bool {
	var expired *MetadataExpiredError
	return errors.As(err, &expired)
}

// fetchChunk is the most unique IDs sent in one videos.list call.
const fetchChunk = 50

// FetchForEvaluation makes the route inputs of reqs locally current, fetching
// the rest through the bound read seam, at most 50 unique IDs per call
// (ARS-005 §6.1, Discovery ARS-001 §3).
//
// Every unique requested ID gets one status. The returned error is only a local
// classification, preflight or persistence failure; a failed read is carried
// in FetchOutcome.Read with its primary, accounting, renewal and interruption
// facts kept apart, for the run owner to dispose of. After any read failure
// other than an unusable response no further call is made and the unsent IDs
// take the failing status. A known quota refusal makes zero calls and writes
// zero rows. An unusable response does not suspend fetching.
func (m *Manager) FetchForEvaluation(ctx context.Context, binding auth.AuthorizationBinding, reqs []InputRequirement) (FetchOutcome, error) {
	out := FetchOutcome{Statuses: make(map[string]FetchStatus, len(reqs))}
	order := make([]string, 0, len(reqs))
	need := make(map[string]InputRequirement, len(reqs))
	for _, r := range reqs {
		if r.VideoID == "" {
			return out, errors.New("metadata: fetch for evaluation: empty video ID")
		}
		if prev, ok := need[r.VideoID]; ok {
			r.Title, r.ChannelName = r.Title || prev.Title, r.ChannelName || prev.ChannelName
			r.Description, r.Duration = r.Description || prev.Description, r.Duration || prev.Duration
			r.Live = r.Live || prev.Live
		} else {
			order = append(order, r.VideoID)
		}
		need[r.VideoID] = r
	}
	failAll := func(ids []string) {
		for _, id := range ids {
			if _, done := out.Statuses[id]; !done {
				out.Statuses[id] = FetchFailed
			}
		}
	}

	var missing []string
	for i := 0; i < len(order); i += fetchChunk {
		chunk := order[i:min(i+fetchChunk, len(order))]
		rows, err := m.store.readInputRows(ctx, chunk)
		if err != nil {
			failAll(order)
			return out, err
		}
		// One cutoff is taken only after the rows are fully read.
		cutoff := m.now().UTC()
		held := make(map[string]inputRow, len(rows))
		for _, row := range rows {
			held[row.VideoID] = row
		}
		for _, id := range chunk {
			if row, ok := held[id]; ok && !need[id].Live && rowSupplies(row, cutoff, need[id]) {
				out.Statuses[id] = FetchCurrent
				continue
			}
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	if m.bound == nil || m.ledger == nil {
		failAll(missing)
		return out, errors.New("metadata: fetch for evaluation: bound fetcher or quota ledger unavailable")
	}

	for i := 0; i < len(missing); i += fetchChunk {
		chunk := missing[i:min(i+fetchChunk, len(missing))]
		if out.Read != nil {
			status := readStatus(out.Read)
			for _, id := range chunk {
				out.Statuses[id] = status
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			out.Read = &ReadFacts{Err: err, Interruption: err}
			for _, id := range chunk {
				out.Statuses[id] = FetchFailed
			}
			continue
		}
		// R5: the run's effective dispatch stop precedes the minimum-unit
		// preflight and every provider call, for this and every later chunk.
		if stop, err := m.ledger.RunDispatchStop(ctx); err != nil {
			failAll(missing[i:])
			return out, fmt.Errorf("metadata: run dispatch stop for evaluation fetch: %w", errors.Join(auth.ErrQuotaAccounting, err))
		} else if stop != quota.StopNone {
			out.Stop = max(out.Stop, stop)
			status := stopStatus(stop)
			for _, id := range chunk {
				out.Statuses[id] = status
			}
			if stop == quota.StopAccountingOrIntegrity {
				failAll(missing[i:])
				return out, fmt.Errorf("metadata: run dispatch stopped for integrity: %w", auth.ErrQuotaAccounting)
			}
			continue
		}
		// A generic provider failure earlier in this run still suspends
		// fetching here. It comes after the stop, so a stop always wins.
		if facts, ok := m.runSuspension(ctx); ok {
			status := readStatus(&facts)
			for _, id := range chunk {
				out.Statuses[id] = status
			}
			continue
		}
		// Known insufficiency refuses with zero attempts. A preflight error is a
		// local fault, not a refusal. The positive answer never replaces the
		// admission inside the read.
		ok, _, err := m.ledger.CanAfford(ctx, quota.OpVideosList.UnitCost())
		if err != nil {
			failAll(missing[i:])
			return out, fmt.Errorf("metadata: quota preflight for evaluation fetch: %w", errors.Join(auth.ErrQuotaAccounting, err))
		}
		if !ok {
			out.Read = &ReadFacts{Err: auth.ErrQuotaRefused, Primary: auth.ErrQuotaRefused, QuotaRefused: true}
			for _, id := range chunk {
				out.Statuses[id] = FetchQuotaRefused
			}
			continue
		}

		res, err := m.bound.FetchMetadataBound(ctx, binding, chunk)
		if err != nil {
			facts, status, suspend := classifyRead(err)
			if facts.Admitted {
				out.Attempts++
			}
			for _, id := range chunk {
				out.Statuses[id] = status
			}
			if suspend {
				out.Read = &facts
				m.suspendRun(ctx, facts, status)
			} else {
				m.logger.WarnContext(ctx, "metadata fetch response unusable", "ids", len(chunk))
			}
			continue
		}

		out.Attempts++
		requested := make(map[string]bool, len(chunk))
		for _, id := range chunk {
			requested[id] = true
		}
		returned := make(map[string]bool, len(res.Returned))
		complete := res.Complete
		for _, id := range res.Returned {
			if !requested[id] || returned[id] {
				complete = false // Identity is not reliable; omission proves nothing.
			}
			returned[id] = true
		}
		for id, live := range res.Live {
			if requested[id] && returned[id] {
				if out.Live == nil {
					out.Live = make(map[string]LiveStatus)
				}
				out.Live[id] = live
			}
		}
		var save []TransientMetadata
		details := make(map[string]TransientMetadata, len(res.Details))
		for _, d := range res.Details {
			if !requested[d.VideoID] || !returned[d.VideoID] {
				continue
			}
			if _, dup := details[d.VideoID]; dup {
				continue
			}
			details[d.VideoID] = d
			save = append(save, d)
		}
		saved, err := m.store.UpsertFetched(ctx, save, m.ttl, m.now())
		if err != nil {
			failAll(missing[i:])
			return out, err
		}
		out.Saved += saved
		unusable := 0
		for _, id := range chunk {
			d, hasDetail := details[id]
			switch {
			case hasDetail && detailSupplies(d, need[id]):
				out.Statuses[id] = FetchFetched
			case returned[id] || !complete:
				out.Statuses[id] = FetchUnusable
				unusable++
			default:
				out.Statuses[id] = FetchAbsent
			}
		}
		if unusable > 0 {
			m.logger.WarnContext(ctx, "metadata fetch inputs unusable", "count", unusable, "complete", complete)
		}
	}
	// An accounting fault controls fail-closed propagation: the caller must see
	// it as an error, with the per-ID statuses already filled in.
	if out.Read != nil && out.Read.Accounting != nil {
		return out, &ReadFaultError{Facts: *out.Read}
	}
	return out, nil
}

// suspendRun makes a generic provider failure (a 5xx, a transport failure or a
// non-quota rate limit) suspend fetching for the rest of the run, not only the
// current call. Quota, authorization, accounting and interruption outcomes are
// not kept: the run dispatch stop and the ledger already carry them and are
// read before every chunk. A call without a run scope keeps the suspension to
// the call.
func (m *Manager) suspendRun(ctx context.Context, f ReadFacts, status FetchStatus) {
	runID, ok := quota.RunFromContext(ctx)
	if !ok || f.QuotaRefused || f.Accounting != nil || f.Interruption != nil ||
		(status != FetchFailed && status != FetchRateLimited) {
		return
	}
	m.suspendMu.Lock()
	defer m.suspendMu.Unlock()
	if m.suspended == nil {
		m.suspended = make(map[int64]ReadFacts)
	}
	if _, held := m.suspended[runID]; held {
		return
	}
	if len(m.suspended) >= maxSuspendedRuns {
		oldest := runID
		for id := range m.suspended {
			oldest = min(oldest, id)
		}
		delete(m.suspended, oldest)
	}
	m.suspended[runID] = f
}

// runSuspension returns the suspension recorded for ctx's run.
func (m *Manager) runSuspension(ctx context.Context) (ReadFacts, bool) {
	runID, ok := quota.RunFromContext(ctx)
	if !ok {
		return ReadFacts{}, false
	}
	m.suspendMu.Lock()
	defer m.suspendMu.Unlock()
	f, ok := m.suspended[runID]
	return f, ok
}

// IsAccountingFault reports whether err carries a local quota accounting or
// integrity fault: an accounting error from a bound read, a failed quota
// preflight or a run dispatch stop at the accounting level. It is the run
// owner's test for failing closed (ARS-005 section 6.1.3, writer W3). A
// provider refusal, an authorization result and a cancellation are not faults.
func IsAccountingFault(err error) bool {
	if err == nil {
		return false
	}
	var fault *ReadFaultError
	var read *youtube.ReadError
	return errors.Is(err, auth.ErrQuotaAccounting) || errors.As(err, &fault) ||
		(errors.As(err, &read) && read.Accounting != nil)
}

// ReadFaultError reports a metadata read whose local quota accounting failed.
// It carries the typed facts and unwraps to the original read error. Its text
// is fixed so no provider or store detail reaches a log through it.
type ReadFaultError struct{ Facts ReadFacts }

func (e *ReadFaultError) Error() string { return "metadata read accounting failed" }

// Unwrap exposes the original read error and the accounting fault.
func (e *ReadFaultError) Unwrap() []error { return []error{e.Facts.Err, e.Facts.Accounting} }

// rowSupplies reports whether a stored row is current at cutoff and supplies
// every input r requires. Presence is a non-NULL column, so a real empty
// description or zero duration counts.
func rowSupplies(row inputRow, cutoff time.Time, r InputRequirement) bool {
	if ProjectLocal(row.LocalRow, cutoff).SourceFetchedAt == nil {
		return false
	}
	return (!r.Title || row.Title != nil) &&
		(!r.ChannelName || row.ChannelName != nil) &&
		(!r.Description || row.Description != nil) &&
		(!r.Duration || row.DurationSeconds != nil)
}

func detailSupplies(d TransientMetadata, r InputRequirement) bool {
	return (!r.Title || d.Title != nil) &&
		(!r.ChannelName || d.ChannelName != nil) &&
		(!r.Description || d.Description != nil) &&
		(!r.Duration || d.DurationSeconds != nil)
}

// stopStatus is the status of an input that a run dispatch stop kept unsent.
func stopStatus(stop quota.DispatchStop) FetchStatus {
	switch stop {
	case quota.StopAuthorization:
		return FetchAuthorizationPending
	case quota.StopQuota:
		return FetchQuotaRefused
	default:
		return FetchFailed
	}
}

// readStatus is the status unsent inputs take after a suspending read failure.
func readStatus(f *ReadFacts) FetchStatus {
	_, status, _ := classifyRead(f.Err)
	return status
}

// classifyRead separates the typed facts of a read error and chooses the
// per-input status. suspend is false only for an unusable successful response.
// The original error stays in the facts; nothing is flattened.
func classifyRead(err error) (facts ReadFacts, status FetchStatus, suspend bool) {
	facts = ReadFacts{Err: err, Primary: err}
	var re *youtube.ReadError
	if errors.As(err, &re) {
		facts.Primary, facts.Accounting, facts.Renewal = re.Primary, re.Accounting, re.Renewal
		facts.Interruption, facts.HTTPStatus = re.Interruption, re.Status
	}
	var api *youtube.APIError
	if errors.As(facts.Primary, &api) && facts.HTTPStatus == 0 {
		facts.HTTPStatus = api.Status
	}
	quotaReason := api != nil && (api.Reason == "quotaExceeded" || api.Reason == "dailyLimitExceeded")
	facts.QuotaRefused = errors.Is(err, auth.ErrQuotaRefused) || quotaReason
	if facts.Accounting == nil && errors.Is(err, auth.ErrQuotaAccounting) {
		facts.Accounting = auth.ErrQuotaAccounting
	}

	var invalid *youtube.InvalidResponseError
	var transport *youtube.ReadTransportError
	// A send was admitted only if the provider answered, the transport failed
	// after admission, the response was unusable, or the read was interrupted.
	// Local refusals and failures before admission are not attempts.
	facts.Admitted = facts.HTTPStatus != 0 || facts.Interruption != nil ||
		errors.As(err, &transport) || errors.As(err, &invalid)
	switch {
	case facts.Accounting != nil || errors.Is(err, auth.ErrQuotaAccounting):
		return facts, FetchFailed, true
	case facts.QuotaRefused:
		return facts, FetchQuotaRefused, true
	case facts.Interruption != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return facts, FetchFailed, true
	case facts.Renewal != nil || facts.HTTPStatus == http.StatusUnauthorized ||
		(api != nil && api.Reason == "authError") ||
		errors.Is(err, auth.ErrAuthorizationChanged) || errors.Is(err, auth.ErrNotConfigured) ||
		errors.Is(err, auth.ErrRunAuthorizationStopped) ||
		errors.Is(err, auth.ErrAccessExpiredBeforeAdmission):
		return facts, FetchAuthorizationPending, true
	case facts.HTTPStatus == http.StatusTooManyRequests ||
		(api != nil && (api.Reason == "rateLimitExceeded" || api.Reason == "userRateLimitExceeded")):
		return facts, FetchRateLimited, true
	case errors.As(err, &invalid):
		return facts, FetchUnusable, false
	}
	return facts, FetchFailed, true
}
