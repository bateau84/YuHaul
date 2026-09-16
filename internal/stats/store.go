package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/bateau84/YuHaul/internal/state"
)

// Store queries analytics data from the YuHaul SQLite schema.
type Store struct {
	db *sql.DB
}

// NewStore returns a stats store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// GetRunStats loads the computed statistics for a run.
func (s *Store) GetRunStats(ctx context.Context, runID int64) (*RunStats, error) {
	r, err := s.loadRun(ctx, runID)
	if err != nil {
		return nil, err
	}

	rejectionReasons, err := s.rejectionBreakdown(ctx, runID)
	if err != nil {
		return nil, err
	}

	quotaOps, err := s.quotaBreakdown(ctx, runID)
	if err != nil {
		return nil, err
	}

	stats := &RunStats{
		RunID:                 r.ID,
		RunType:               string(r.RunType),
		Status:                string(r.Status),
		StartedAt:             r.StartedAt,
		CompletedAt:           r.CompletedAt,
		ConfigVersionID:       r.ConfigVersionID,
		VideosDiscovered:      r.VideosDiscovered,
		VideosAccepted:        r.VideosAccepted,
		VideosRejected:        r.VideosRejected,
		VideosDeferred:        r.VideosDeferred,
		VideosFailed:          r.VideosFailed,
		VideosNotYetEvaluated: r.VideosNotYetEvaluated,
		RejectionByReason:     rejectionReasons,
		QuotaByOperation:      quotaOps,
		DurationSeconds:       r.DurationSeconds,
	}
	return stats, nil
}

func (s *Store) loadRun(ctx context.Context, runID int64) (*state.Run, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, started_at, completed_at, run_type, status, config_version_id,
		        videos_discovered, videos_accepted, videos_rejected, videos_excluded,
		        videos_deferred, videos_failed, videos_not_yet_evaluated, quota_cost,
		        duration_seconds, interrupt_reason, current_phase
		 FROM run WHERE id = ?;`, runID)
	r, err := scanRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("run %d: %w", runID, state.ErrNotFound)
		}
		return nil, fmt.Errorf("loading run %d: %w", runID, err)
	}
	return r, nil
}

func (s *Store) rejectionBreakdown(ctx context.Context, runID int64) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(rejection_reason, 'unknown'), COUNT(*)
		 FROM route_decision
		 WHERE run_id = ? AND decision = 'not_matched'
		 GROUP BY COALESCE(rejection_reason, 'unknown');`, runID)
	if err != nil {
		return nil, fmt.Errorf("rejection breakdown for run %d: %w", runID, err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var reason string
		var count int
		if err := rows.Scan(&reason, &count); err != nil {
			return nil, fmt.Errorf("scan rejection breakdown: %w", err)
		}
		out[reason] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rejection breakdown: %w", err)
	}
	return out, nil
}

func (s *Store) quotaBreakdown(ctx context.Context, runID int64) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT operation_type, COALESCE(SUM(units_consumed), 0)
		 FROM quota_ledger
		 WHERE run_id = ?
		 GROUP BY operation_type;`, runID)
	if err != nil {
		return nil, fmt.Errorf("quota breakdown for run %d: %w", runID, err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var op string
		var units int
		if err := rows.Scan(&op, &units); err != nil {
			return nil, fmt.Errorf("scan quota breakdown: %w", err)
		}
		out[op] = units
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quota breakdown: %w", err)
	}
	return out, nil
}

// ListRouteStats returns per-route statistics for a run.
func (s *Store) ListRouteStats(ctx context.Context, runID int64) ([]RouteStats, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.name, r.evaluation_order,
		        COUNT(rd.id) AS evaluated,
		        SUM(CASE WHEN rd.decision = 'matched' THEN 1 ELSE 0 END) AS matched,
		        SUM(CASE WHEN rd.decision = 'not_matched' THEN 1 ELSE 0 END) AS not_matched,
		        SUM(CASE WHEN rd.decision = 'skipped' THEN 1 ELSE 0 END) AS skipped
		 FROM route r
		 LEFT JOIN route_decision rd ON rd.route_id = r.id AND rd.run_id = ?
		 GROUP BY r.id, r.name, r.evaluation_order
		 ORDER BY r.evaluation_order;`, runID)
	if err != nil {
		return nil, fmt.Errorf("route stats for run %d: %w", runID, err)
	}
	defer rows.Close()

	out := make([]RouteStats, 0)
	for rows.Next() {
		var rs RouteStats
		if err := rows.Scan(&rs.RouteID, &rs.RouteName, &rs.EvaluationOrder,
			&rs.VideosEvaluated, &rs.VideosMatched, &rs.VideosNotMatched, &rs.VideosSkipped); err != nil {
			return nil, fmt.Errorf("scan route stats: %w", err)
		}
		out = append(out, rs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate route stats: %w", err)
	}

	for i := range out {
		if err := s.enrichRouteStats(ctx, runID, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) enrichRouteStats(ctx context.Context, runID int64, rs *RouteStats) error {
	// Rejection reason breakdown.
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(rejection_reason, 'unknown'), COUNT(*)
		 FROM route_decision
		 WHERE run_id = ? AND route_id = ? AND decision = 'not_matched'
		 GROUP BY COALESCE(rejection_reason, 'unknown');`, runID, rs.RouteID)
	if err != nil {
		return fmt.Errorf("route rejection breakdown: %w", err)
	}
	defer rows.Close()
	rs.RejectionByReason = make(map[string]int)
	for rows.Next() {
		var reason string
		var count int
		if err := rows.Scan(&reason, &count); err != nil {
			return fmt.Errorf("scan route rejection: %w", err)
		}
		rs.RejectionByReason[reason] = count
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate route rejection: %w", err)
	}

	// Skip reason breakdown.
	rows2, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(skip_reason, 'unknown'), COUNT(*)
		 FROM route_decision
		 WHERE run_id = ? AND route_id = ? AND decision = 'skipped'
		 GROUP BY COALESCE(skip_reason, 'unknown');`, runID, rs.RouteID)
	if err != nil {
		return fmt.Errorf("route skip breakdown: %w", err)
	}
	defer rows2.Close()
	rs.SkipBreakdown = make(map[string]int)
	for rows2.Next() {
		var reason string
		var count int
		if err := rows2.Scan(&reason, &count); err != nil {
			return fmt.Errorf("scan route skip: %w", err)
		}
		rs.SkipBreakdown[reason] = count
	}
	if err := rows2.Err(); err != nil {
		return fmt.Errorf("iterate route skip: %w", err)
	}

	// Action outcomes for matched decisions.
	rows3, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(SUM(CASE WHEN ea.outcome = 'succeeded' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN ea.outcome = 'failed' THEN 1 ELSE 0 END), 0),
		        COUNT(ea.id)
		 FROM route_decision rd
		 LEFT JOIN executed_action ea ON ea.route_decision_id = rd.id
		 WHERE rd.run_id = ? AND rd.route_id = ? AND rd.decision = 'matched';`, runID, rs.RouteID)
	if err != nil {
		return fmt.Errorf("route action outcomes: %w", err)
	}
	defer rows3.Close()
	if rows3.Next() {
		if err := rows3.Scan(&rs.ActionsSucceeded, &rs.ActionsFailed, &rs.ActionsExecuted); err != nil {
			return fmt.Errorf("scan route actions: %w", err)
		}
	}
	if err := rows3.Err(); err != nil {
		return fmt.Errorf("iterate route actions: %w", err)
	}
	return nil
}

// ListChannelStats returns statistics for all known channels.
func (s *Store) ListChannelStats(ctx context.Context) ([]ChannelStats, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, channel_name, total_discovered, total_routed, total_retained,
		        last_discovered_at
		 FROM channel
		 ORDER BY channel_id;`)
	if err != nil {
		return nil, fmt.Errorf("list channel stats: %w", err)
	}
	defer rows.Close()

	out := make([]ChannelStats, 0)
	for rows.Next() {
		cs, err := scanChannelStats(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channel stats: %w", err)
	}
	return out, nil
}

// GetChannelStats loads statistics for a single channel.
func (s *Store) GetChannelStats(ctx context.Context, channelIDText string) (*ChannelStats, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT channel_id, channel_name, total_discovered, total_routed, total_retained,
		        last_discovered_at
		 FROM channel WHERE channel_id = ?;`, channelIDText)
	cs, err := scanChannelStats(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("channel %s: %w", channelIDText, state.ErrNotFound)
		}
		return nil, fmt.Errorf("load channel stats %s: %w", channelIDText, err)
	}
	return &cs, nil
}

func scanChannelStats(sc scanner) (ChannelStats, error) {
	var cs ChannelStats
	var lastDiscovered sql.NullString
	err := sc.Scan(&cs.ChannelID, &cs.ChannelName, &cs.TotalDiscovered, &cs.TotalRouted,
		&cs.TotalRetained, &lastDiscovered)
	if err != nil {
		return ChannelStats{}, err
	}
	if lastDiscovered.Valid {
		t, _ := time.Parse(time.RFC3339Nano, lastDiscovered.String)
		cs.LastDiscoveredAt = &t
	}
	cs.YieldRate = safeRate(cs.TotalRouted, cs.TotalDiscovered)
	cs.UsefulnessRate = safeRate(cs.TotalRetained, cs.TotalDiscovered)
	cs.DuplicateRate = 0 // populated by service if needed
	return cs, nil
}

// IncrementChannelRouted increments total_routed for a channel.
func (s *Store) IncrementChannelRouted(ctx context.Context, channelIDText string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channel SET total_routed = total_routed + 1 WHERE channel_id = ?;`, channelIDText)
	if err != nil {
		return fmt.Errorf("increment channel routed %s: %w", channelIDText, err)
	}
	return nil
}

// GetTemporalStats returns run outcomes grouped by bucket. Bucketing is done
// in Go because run.started_at is stored as RFC3339Nano, which SQLite's
// strftime cannot parse reliably.
func (s *Store) GetTemporalStats(ctx context.Context, from, to time.Time, granularity string) ([]TemporalBucket, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT started_at, videos_discovered, videos_accepted, videos_rejected,
		        videos_deferred, videos_failed, videos_not_yet_evaluated
		 FROM run
		 WHERE started_at >= ? AND started_at < ? AND status = 'completed'
		 ORDER BY started_at;`, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("temporal stats: %w", err)
	}
	defer rows.Close()

	groups := make(map[string]*TemporalBucket)
	for rows.Next() {
		var startedAt string
		var b TemporalBucket
		if err := rows.Scan(&startedAt, &b.VideosDiscovered, &b.VideosAccepted,
			&b.VideosRejected, &b.VideosDeferred, &b.VideosFailed, &b.VideosNotYetEvaluated); err != nil {
			return nil, fmt.Errorf("scan temporal row: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return nil, fmt.Errorf("parse run time: %w", err)
		}
		bucket := bucketKey(t, granularity)
		existing, ok := groups[bucket]
		if !ok {
			b.Bucket = bucket
			groups[bucket] = &b
		} else {
			existing.VideosDiscovered += b.VideosDiscovered
			existing.VideosAccepted += b.VideosAccepted
			existing.VideosRejected += b.VideosRejected
			existing.VideosDeferred += b.VideosDeferred
			existing.VideosFailed += b.VideosFailed
			existing.VideosNotYetEvaluated += b.VideosNotYetEvaluated
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate temporal rows: %w", err)
	}

	out := make([]TemporalBucket, 0, len(groups))
	for _, b := range groups {
		out = append(out, *b)
	}
	// Buckets are ordered lexically because bucket keys use ISO 8601 prefixes.
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out, nil
}

func bucketKey(t time.Time, granularity string) string {
	switch granularity {
	case "week":
		_, week := t.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", t.Year(), week)
	case "month":
		return t.Format("2006-01")
	default:
		return t.Format("2006-01-02")
	}
}

// ListDuplicateProvenance returns duplicate provenance rows where the
// relevance-credit channel matches channelIDText.
func (s *Store) ListDuplicateProvenance(ctx context.Context, channelIDText string) ([]DuplicateProvenance, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT v.video_id, v.channel_id, dm.matched_fingerprint_id, dm.similarity_score,
		        dm.match_type, tf.channel_id, tfp.channel_name
		 FROM duplicate_match dm
		 JOIN video v ON v.id = dm.video_id
		 JOIN title_fingerprint tf ON tf.id = dm.matched_fingerprint_id
		 LEFT JOIN channel tfp ON tfp.channel_id = tf.channel_id
		 WHERE v.channel_id = ?;`, channelIDText)
	if err != nil {
		return nil, fmt.Errorf("duplicate provenance for %s: %w", channelIDText, err)
	}
	defer rows.Close()

	out := make([]DuplicateProvenance, 0)
	for rows.Next() {
		var dp DuplicateProvenance
		if err := rows.Scan(&dp.VideoID, &dp.ChannelID, &dp.MatchedFingerprintID,
			&dp.SimilarityScore, &dp.MatchType,
			&dp.DiscoveryCreditChannelID, &dp.DiscoveryCreditChannelName); err != nil {
			return nil, fmt.Errorf("scan duplicate provenance: %w", err)
		}
		out = append(out, dp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate duplicate provenance: %w", err)
	}
	return out, nil
}

// ListRetentionSignals returns retention signals for videos from a channel.
func (s *Store) ListRetentionSignals(ctx context.Context, channelIDText string) ([]RetentionSignal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT rs.id, rs.video_id, rs.playlist_id, rs.membership_source, rs.is_retention_signal, rs.detected_at
		 FROM retention_signal rs
		 JOIN video v ON v.video_id = rs.video_id
		 WHERE v.channel_id = ?
		 ORDER BY rs.detected_at DESC;`, channelIDText)
	if err != nil {
		return nil, fmt.Errorf("retention signals for %s: %w", channelIDText, err)
	}
	defer rows.Close()

	out := make([]RetentionSignal, 0)
	for rows.Next() {
		var rs RetentionSignal
		var detectedAt string
		if err := rows.Scan(&rs.ID, &rs.VideoID, &rs.PlaylistID, &rs.MembershipSource,
			&rs.IsRetentionSignal, &detectedAt); err != nil {
			return nil, fmt.Errorf("scan retention signal: %w", err)
		}
		rs.DetectedAt, _ = time.Parse(time.RFC3339Nano, detectedAt)
		out = append(out, rs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retention signals: %w", err)
	}
	return out, nil
}

// ListRetentionPlaylists returns active retention playlists.
func (s *Store) ListRetentionPlaylists(ctx context.Context) ([]RetentionPlaylist, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, playlist_id, playlist_name, designated_at, un_designated_at, is_active, last_checked_at
		 FROM retention_playlist
		 ORDER BY designated_at DESC;`)
	if err != nil {
		return nil, fmt.Errorf("list retention playlists: %w", err)
	}
	defer rows.Close()

	out := make([]RetentionPlaylist, 0)
	for rows.Next() {
		rp, err := scanRetentionPlaylist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retention playlists: %w", err)
	}
	return out, nil
}

// CreateRetentionPlaylist designates a playlist as a retention signal source.
func (s *Store) CreateRetentionPlaylist(ctx context.Context, playlistID, playlistName string) (*RetentionPlaylist, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO retention_playlist (playlist_id, playlist_name, designated_at, is_active)
		 VALUES (?, ?, ?, 1)
		 ON CONFLICT(playlist_id) DO UPDATE SET
		  playlist_name = excluded.playlist_name,
		  un_designated_at = NULL,
		  is_active = 1,
		  designated_at = excluded.designated_at;`,
		playlistID, playlistName, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("create retention playlist: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("read retention playlist id: %w", err)
	}
	return s.GetRetentionPlaylist(ctx, id)
}

// GetRetentionPlaylist loads a retention playlist by ID.
func (s *Store) GetRetentionPlaylist(ctx context.Context, id int64) (*RetentionPlaylist, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, playlist_id, playlist_name, designated_at, un_designated_at, is_active, last_checked_at
		 FROM retention_playlist WHERE id = ?;`, id)
	rp, err := scanRetentionPlaylist(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("retention playlist %d: %w", id, state.ErrNotFound)
		}
		return nil, fmt.Errorf("load retention playlist %d: %w", id, err)
	}
	return &rp, nil
}

// DeleteRetentionPlaylist soft-deletes a retention playlist designation.
func (s *Store) DeleteRetentionPlaylist(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE retention_playlist SET is_active = 0, un_designated_at = ? WHERE id = ?;`,
		time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("delete retention playlist %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("retention playlist %d: %w", id, state.ErrNotFound)
	}
	return nil
}

// RecordRetentionSignal inserts or replaces a retention signal observation.
func (s *Store) RecordRetentionSignal(ctx context.Context, videoID, playlistID, membershipSource string, isSignal bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO retention_signal (video_id, playlist_id, membership_source, is_retention_signal, detected_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(video_id, playlist_id) DO UPDATE SET
		  membership_source = excluded.membership_source,
		  is_retention_signal = excluded.is_retention_signal,
		  detected_at = excluded.detected_at;`,
		videoID, playlistID, membershipSource, isSignal, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record retention signal: %w", err)
	}
	return nil
}

// UpdateChannelRetained recomputes total_retained for a channel from retention
// signals that count as genuine user-driven retention.
func (s *Store) UpdateChannelRetained(ctx context.Context, channelIDText string) error {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT rs.video_id)
		 FROM retention_signal rs
		 JOIN video v ON v.video_id = rs.video_id
		 WHERE v.channel_id = ? AND rs.is_retention_signal = 1;`, channelIDText).Scan(&count)
	if err != nil {
		return fmt.Errorf("count retained for %s: %w", channelIDText, err)
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE channel SET total_retained = ? WHERE channel_id = ?;`, count, channelIDText)
	if err != nil {
		return fmt.Errorf("update retained for %s: %w", channelIDText, err)
	}
	return nil
}

func scanRetentionPlaylist(sc scanner) (RetentionPlaylist, error) {
	var rp RetentionPlaylist
	var undesignated, lastChecked sql.NullString
	err := sc.Scan(&rp.ID, &rp.PlaylistID, &rp.PlaylistName, &rp.DesignatedAt,
		&undesignated, &rp.IsActive, &lastChecked)
	if err != nil {
		return RetentionPlaylist{}, err
	}
	if undesignated.Valid {
		t, _ := time.Parse(time.RFC3339Nano, undesignated.String)
		rp.UnDesignatedAt = &t
	}
	if lastChecked.Valid {
		t, _ := time.Parse(time.RFC3339Nano, lastChecked.String)
		rp.LastCheckedAt = &t
	}
	return rp, nil
}

// GetRouteMatchRates returns per-route decision counts grouped by bucket.
func (s *Store) GetRouteMatchRates(ctx context.Context, from, to time.Time, granularity string) ([]RouteMatchRate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.name, run.started_at, rd.decision
		 FROM route_decision rd
		 JOIN route r ON r.id = rd.route_id
		 JOIN run ON run.id = rd.run_id
		 WHERE run.started_at >= ? AND run.started_at < ? AND run.status = 'completed'
		 ORDER BY run.started_at, r.evaluation_order;`,
		from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("route match rates: %w", err)
	}
	defer rows.Close()

	type key struct {
		routeID int64
		bucket  string
	}
	groups := make(map[key]*RouteMatchRate)
	for rows.Next() {
		var routeID int64
		var routeName, startedAt, decision string
		if err := rows.Scan(&routeID, &routeName, &startedAt, &decision); err != nil {
			return nil, fmt.Errorf("scan route match rate: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return nil, fmt.Errorf("parse run time: %w", err)
		}
		k := key{routeID: routeID, bucket: bucketKey(t, granularity)}
		g, ok := groups[k]
		if !ok {
			g = &RouteMatchRate{RouteID: routeID, RouteName: routeName, Bucket: k.bucket}
			groups[k] = g
		}
		g.Evaluated++
		if decision == "matched" {
			g.Matched++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate route match rates: %w", err)
	}

	out := make([]RouteMatchRate, 0, len(groups))
	for _, g := range groups {
		g.MatchRate = safeRate(g.Matched, g.Evaluated)
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bucket != out[j].Bucket {
			return out[i].Bucket < out[j].Bucket
		}
		return out[i].RouteID < out[j].RouteID
	})
	return out, nil
}

func safeRate(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(sc scanner) (*state.Run, error) {
	r := &state.Run{}
	var completedAt, interruptReason, currentPhase sql.NullString
	var durationSeconds sql.NullFloat64
	var configVersionID sql.NullInt64
	err := sc.Scan(&r.ID, &r.StartedAt, &completedAt, &r.RunType, &r.Status,
		&configVersionID, &r.VideosDiscovered, &r.VideosAccepted, &r.VideosRejected,
		&r.VideosExcluded, &r.VideosDeferred, &r.VideosFailed, &r.VideosNotYetEvaluated,
		&r.QuotaCost, &durationSeconds, &interruptReason, &currentPhase)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, completedAt.String)
		r.CompletedAt = &t
	}
	if configVersionID.Valid {
		r.ConfigVersionID = &configVersionID.Int64
	}
	if durationSeconds.Valid {
		r.DurationSeconds = &durationSeconds.Float64
	}
	if interruptReason.Valid {
		r.InterruptReason = &interruptReason.String
	}
	if currentPhase.Valid {
		r.CurrentPhase = &currentPhase.String
	}
	return r, nil
}

var _ scanner = (*sql.Row)(nil)
