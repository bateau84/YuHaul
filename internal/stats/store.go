package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// Store queries analytics data from the YuHaul SQLite schema.
type Store struct {
	db *sql.DB
	// routes reads the shared route_identity registry so statistics project
	// route keys through the same seam the route API and run-video decisions
	// use (ARS-004).
	routes *router.Store
}

// NewStore returns a stats store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, routes: router.NewStore(db)}
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

// ListRouteStats returns per-route statistics for a run. Public identity is
// the route's stable UUID resolved through the route_identity registry; the
// route label comes from the run's immutable ConfigVersion snapshot so a later
// rename does not rewrite what that run evaluated (ARS-001). Internal joins
// and enrichment stay keyed by the integer route id.
//
// The emitted evaluation_order is the route's order in the requested run, not
// the route row's current order: it is projected from the run's immutable
// ConfigVersion snapshot, falls back to persisted route_decision orders only
// when the snapshot has no usable order for the route, and is omitted when
// neither source supplies one consistent positive order (ARS-001). The
// current route.evaluation_order is never consulted for the field — ADR-003
// and ADR-012 store the negative -route_id sentinel there for soft-deleted
// routes and proposal-only anchors.
func (s *Store) ListRouteStats(ctx context.Context, runID int64) ([]RouteStats, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.name,
		        COUNT(rd.id) AS evaluated,
		        SUM(CASE WHEN rd.decision = 'matched' THEN 1 ELSE 0 END) AS matched,
		        SUM(CASE WHEN rd.decision = 'not_matched' THEN 1 ELSE 0 END) AS not_matched,
		        SUM(CASE WHEN rd.decision = 'skipped' THEN 1 ELSE 0 END) AS skipped
		 FROM route r
		 LEFT JOIN route_decision rd ON rd.route_id = r.id AND rd.run_id = ?
		 GROUP BY r.id, r.name
		 ORDER BY r.evaluation_order;`, runID)
	if err != nil {
		return nil, fmt.Errorf("route stats for run %d: %w", runID, err)
	}
	defer rows.Close()

	// routeKey carries the internal integer through enrichment; it is never
	// serialized, and the public RouteID is filled in after the lookups below.
	type pending struct {
		routeKey int64
		stats    RouteStats
	}
	entries := make([]pending, 0)
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.routeKey, &p.stats.RouteName,
			&p.stats.VideosEvaluated, &p.stats.VideosMatched, &p.stats.VideosNotMatched, &p.stats.VideosSkipped); err != nil {
			return nil, fmt.Errorf("scan route stats: %w", err)
		}
		entries = append(entries, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate route stats: %w", err)
	}

	for i := range entries {
		if err := s.enrichRouteStats(ctx, runID, entries[i].routeKey, &entries[i].stats); err != nil {
			return nil, err
		}
	}

	publicIDs, err := s.routes.ListRouteIdentities(ctx)
	if err != nil {
		return nil, fmt.Errorf("route stats for run %d: %w", runID, err)
	}
	labels, orders, err := s.runRouteContext(ctx, runID)
	if err != nil {
		return nil, err
	}
	decisionOrders, err := s.decisionRouteOrders(ctx, runID)
	if err != nil {
		return nil, err
	}

	out := make([]RouteStats, 0, len(entries))
	for _, e := range entries {
		publicID, err := publicRouteID(publicIDs, e.routeKey)
		if err != nil {
			return nil, err
		}
		e.stats.RouteID = publicID
		// A route outside the evaluated snapshot (for example one created
		// after the run) has no historical label; its current label is the
		// only one that exists and identity is unaffected.
		if label, ok := labels[e.routeKey]; ok {
			e.stats.RouteName = label
		}
		// Historical order: snapshot first; persisted decision orders only
		// where the snapshot has no usable order; otherwise omitted. Never
		// the current row's value, never a transformed sentinel.
		if order, ok := orders[e.routeKey]; ok {
			e.stats.EvaluationOrder = &order
		} else if order, ok := decisionOrders[e.routeKey]; ok {
			e.stats.EvaluationOrder = &order
		}
		out = append(out, e.stats)
	}
	return out, nil
}

func (s *Store) enrichRouteStats(ctx context.Context, runID, routeKey int64, rs *RouteStats) error {
	// Rejection reason breakdown.
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(rejection_reason, 'unknown'), COUNT(*)
		 FROM route_decision
		 WHERE run_id = ? AND route_id = ? AND decision = 'not_matched'
		 GROUP BY COALESCE(rejection_reason, 'unknown');`, runID, routeKey)
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
		 GROUP BY COALESCE(skip_reason, 'unknown');`, runID, routeKey)
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
		 WHERE rd.run_id = ? AND rd.route_id = ? AND rd.decision = 'matched';`, runID, routeKey)
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
	cs.Yield = safeRate(cs.TotalRouted, cs.TotalDiscovered)
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
		 ORDER BY started_at;`, from.UTC().Format(state.RunTimestampLayout), to.UTC().Format(state.RunTimestampLayout))
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

// discoveredAtLayouts are the stored representations of video.discovered_at.
// Current writes (bound time.Time values, explicit text, and the schema
// default) land as RFC3339 text, while rows written by earlier driver
// versions carry Go's String() format. Both decode to the same chronological
// instants, so ordering compares decoded times, never raw SQLite text
// (ADR-014 §6).
var discoveredAtLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

// parseDiscoveredAt decodes a stored video.discovered_at value. ok is false
// for unparseable values: unknown evidence must never fall back to imported
// fingerprint metadata (ADR-014 §3, §6).
func parseDiscoveredAt(raw string) (time.Time, bool) {
	for _, layout := range discoveredAtLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// duplicateMatchFact is one distinct-pair duplicate_match row joined with the
// durable per-video evidence on both sides: the evaluated video's native
// record and the matched fingerprint's native video record (ADR-014). Legacy
// same-ID self-match rows are excluded before facts are returned.
type duplicateMatchFact struct {
	matchedFingerprintID int64
	similarityScore      float64
	matchType            string

	// Evaluated side: the video that was matched against historical records.
	videoID          string
	videoChannelID   string
	videoChannelName string
	videoTime        time.Time
	videoTimeOK      bool

	// Reference side: the matched fingerprint's native video record. Empty
	// when the fingerprint has no native observation.
	refVideoID     string
	refChannelID   string
	refChannelName string
	refTime        time.Time
	refTimeOK      bool
}

// pairRoles are the chronology-derived ADR-014 roles for one distinct match
// pair: the earlier native observation's channel earns discovery credit, the
// later one earns relevance credit.
type pairRoles struct {
	discoveryChannelID   string
	discoveryChannelName string
	relevanceChannelID   string
	relevanceChannelName string
	discoveryVideoID     string
	relevanceVideoID     string
}

// roles returns the pair's discovery/relevance attribution. ok is false when
// the pair has no orderable chronology — no native reference record, or
// decoded discovery times that are equal or unparseable. Neither the imported
// fingerprint association nor an assumed reference role may stand in for
// missing chronology (ADR-014 §3, §5).
func (f duplicateMatchFact) roles() (pairRoles, bool) {
	if f.refVideoID == "" || !f.videoTimeOK || !f.refTimeOK {
		return pairRoles{}, false
	}
	if f.videoTime.Before(f.refTime) {
		return pairRoles{
			discoveryChannelID:   f.videoChannelID,
			discoveryChannelName: f.videoChannelName,
			relevanceChannelID:   f.refChannelID,
			relevanceChannelName: f.refChannelName,
			discoveryVideoID:     f.videoID,
			relevanceVideoID:     f.refVideoID,
		}, true
	}
	if f.refTime.Before(f.videoTime) {
		return pairRoles{
			discoveryChannelID:   f.refChannelID,
			discoveryChannelName: f.refChannelName,
			relevanceChannelID:   f.videoChannelID,
			relevanceChannelName: f.videoChannelName,
			discoveryVideoID:     f.refVideoID,
			relevanceVideoID:     f.videoID,
		}, true
	}
	return pairRoles{}, false
}

// loadDuplicateMatchFacts returns every distinct-pair duplicate_match row with
// both sides' native video evidence. Legacy same-ID self-matches are not
// duplicate events and earn neither discovery nor relevance credit
// (ADR-014 §4). Attribution is decided per pair from decoded discovery times,
// independent of which side the detector returned as the matched fingerprint.
func (s *Store) loadDuplicateMatchFacts(ctx context.Context) ([]duplicateMatchFact, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dm.matched_fingerprint_id, dm.similarity_score, dm.match_type,
		        v.video_id, v.channel_id, vc.channel_name, v.discovered_at,
		        rv.video_id, rv.channel_id, rvc.channel_name, rv.discovered_at
		 FROM duplicate_match dm
		 JOIN video v ON v.id = dm.video_id
		 LEFT JOIN channel vc ON vc.channel_id = v.channel_id
		 JOIN title_fingerprint tf ON tf.id = dm.matched_fingerprint_id
		 LEFT JOIN video rv ON rv.video_id = tf.video_id
		 LEFT JOIN channel rvc ON rvc.channel_id = rv.channel_id
		 WHERE tf.video_id <> v.video_id;`)
	if err != nil {
		return nil, fmt.Errorf("load duplicate match facts: %w", err)
	}
	defer rows.Close()

	out := make([]duplicateMatchFact, 0)
	for rows.Next() {
		var f duplicateMatchFact
		var videoChannelName, refVideoID, refChannelID, refChannelName, refDiscoveredAt sql.NullString
		var videoDiscoveredAt string
		if err := rows.Scan(&f.matchedFingerprintID, &f.similarityScore, &f.matchType,
			&f.videoID, &f.videoChannelID, &videoChannelName, &videoDiscoveredAt,
			&refVideoID, &refChannelID, &refChannelName, &refDiscoveredAt); err != nil {
			return nil, fmt.Errorf("scan duplicate match fact: %w", err)
		}
		f.videoChannelName = videoChannelName.String
		f.videoTime, f.videoTimeOK = parseDiscoveredAt(videoDiscoveredAt)
		if refVideoID.Valid {
			f.refVideoID = refVideoID.String
			f.refChannelID = refChannelID.String
			f.refChannelName = refChannelName.String
			f.refTime, f.refTimeOK = parseDiscoveredAt(refDiscoveredAt.String)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate duplicate match facts: %w", err)
	}
	return out, nil
}

// DuplicateProvenanceBreakdown counts how often a channel earned discovery
// credit — its native video observation preceded the matched pair's other
// native observation — versus relevance credit, earned by the later
// observation of the pair. Pairs without orderable native chronology and
// legacy same-ID self-matches contribute to neither (ADR-014).
func (s *Store) DuplicateProvenanceBreakdown(ctx context.Context, channelIDText string) (DuplicateProvenanceBreakdown, error) {
	var out DuplicateProvenanceBreakdown
	facts, err := s.loadDuplicateMatchFacts(ctx)
	if err != nil {
		return out, fmt.Errorf("duplicate provenance breakdown for %s: %w", channelIDText, err)
	}
	for _, f := range facts {
		roles, ok := f.roles()
		if !ok {
			continue
		}
		if roles.discoveryChannelID == channelIDText {
			out.DiscoveryCredit++
		}
		if roles.relevanceChannelID == channelIDText {
			out.RelevanceCredit++
		}
	}
	return out, nil
}

// RetentionSignalBreakdown counts user-driven versus YuHaul-routed retention
// signals for a channel.
func (s *Store) RetentionSignalBreakdown(ctx context.Context, channelIDText string) (RetentionProvenanceBreakdown, error) {
	var out RetentionProvenanceBreakdown
	rows, err := s.db.QueryContext(ctx,
		`SELECT rs.membership_source, rs.is_retention_signal
		 FROM retention_signal rs
		 JOIN video v ON v.video_id = rs.video_id
		 WHERE v.channel_id = ?;`, channelIDText)
	if err != nil {
		return out, fmt.Errorf("retention signals for %s: %w", channelIDText, err)
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		var isSignal bool
		if err := rows.Scan(&source, &isSignal); err != nil {
			return out, fmt.Errorf("scan retention signal for %s: %w", channelIDText, err)
		}
		if source == "user_driven" && isSignal {
			out.UserDriven++
		} else if source == "yuhaul_routed" {
			out.YuhaulRouted++
		}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterate retention signals for %s: %w", channelIDText, err)
	}
	return out, nil
}

const recentVideoLimit = 10

// ListRecentVideos returns the last N videos discovered for a channel, joined
// with transient metadata for title and published-at information.
func (s *Store) ListRecentVideos(ctx context.Context, channelIDText string) ([]VideoSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT v.id, v.video_id, v.channel_id, v.processing_state, v.final_outcome,
		        v.discovered_at, v.completed_at, tm.title, tm.published_at
		 FROM video v
		 LEFT JOIN transient_metadata tm ON tm.video_id = v.video_id
		 WHERE v.channel_id = ?
		 ORDER BY v.discovered_at DESC
		 LIMIT ?;`, channelIDText, recentVideoLimit)
	if err != nil {
		return nil, fmt.Errorf("recent videos for %s: %w", channelIDText, err)
	}
	defer rows.Close()

	out := make([]VideoSummary, 0)
	for rows.Next() {
		var vs VideoSummary
		var discoveredAt, completedAt, publishedAt sql.NullString
		var title sql.NullString
		if err := rows.Scan(&vs.ID, &vs.VideoID, &vs.ChannelID, &vs.ProcessingState, &vs.FinalOutcome,
			&discoveredAt, &completedAt, &title, &publishedAt); err != nil {
			return nil, fmt.Errorf("scan recent video: %w", err)
		}
		if discoveredAt.Valid {
			vs.DiscoveredAt, _ = time.Parse(time.RFC3339Nano, discoveredAt.String)
		}
		if completedAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, completedAt.String)
			vs.CompletedAt = &t
		}
		if title.Valid {
			vs.Title = title.String
		}
		if publishedAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, publishedAt.String)
			vs.PublishedAt = &t
		}
		out = append(out, vs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent videos for %s: %w", channelIDText, err)
	}
	return out, nil
}

// ListRecentVideoProvenance returns per-video duplicate and retention
// provenance for a channel's videos. Duplicate credit follows the recorded
// pair's native chronology (ADR-014): a video tagged 'discovery' was the
// earlier native observation in a distinct matched pair, and 'relevance' the
// later one; discovery takes precedence because it is the stronger credit.
// Pairs without orderable native evidence and legacy same-ID self-matches
// contribute no credit. Retention source reflects recorded playlist-
// membership provenance.
func (s *Store) ListRecentVideoProvenance(ctx context.Context, channelIDText string) ([]VideoProvenance, error) {
	type provenance struct {
		duplicateCredit *string
		retentionSource *string
	}
	byVideo := make(map[string]*provenance)
	get := func(videoID string) *provenance {
		p := byVideo[videoID]
		if p == nil {
			p = &provenance{}
			byVideo[videoID] = p
		}
		return p
	}

	facts, err := s.loadDuplicateMatchFacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("recent video duplicate provenance for %s: %w", channelIDText, err)
	}
	for _, f := range facts {
		roles, ok := f.roles()
		if !ok {
			continue
		}
		if roles.discoveryChannelID == channelIDText {
			// Discovery is the stronger credit; keep it over relevance.
			p := get(roles.discoveryVideoID)
			c := "discovery"
			p.duplicateCredit = &c
		}
		if roles.relevanceChannelID == channelIDText {
			p := get(roles.relevanceVideoID)
			if p.duplicateCredit == nil {
				c := "relevance"
				p.duplicateCredit = &c
			}
		}
	}

	retRows, err := s.db.QueryContext(ctx,
		`SELECT rs.video_id, rs.membership_source
		 FROM retention_signal rs
		 JOIN video v ON v.video_id = rs.video_id
		 WHERE v.channel_id = ?
		   AND ((rs.membership_source = 'user_driven' AND rs.is_retention_signal = 1)
		     OR rs.membership_source = 'yuhaul_routed');`, channelIDText)
	if err != nil {
		return nil, fmt.Errorf("recent video retention provenance for %s: %w", channelIDText, err)
	}
	defer retRows.Close()
	for retRows.Next() {
		var videoID, source string
		if err := retRows.Scan(&videoID, &source); err != nil {
			return nil, fmt.Errorf("scan recent video retention provenance: %w", err)
		}
		p := get(videoID)
		if p.retentionSource == nil {
			s := source
			p.retentionSource = &s
		}
	}
	if err := retRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent video retention provenance: %w", err)
	}

	out := make([]VideoProvenance, 0, len(byVideo))
	for videoID, p := range byVideo {
		out = append(out, VideoProvenance{
			VideoID:         videoID,
			DuplicateCredit: p.duplicateCredit,
			RetentionSource: p.retentionSource,
		})
	}
	return out, nil
}

// MatchedRoutesForChannel returns one entry per route identity that matched a
// video from this channel in a production run. The count is the number of
// distinct internal video records that route matched for this channel; dry-run,
// replay, and backtest decisions never contribute (BR-027 AC7, ARS-001).
// Soft-deleted routes keep their entries while production evidence exists.
// Entries are ordered by display name and then public UUID, and a route key
// without a registry mapping fails the whole read instead of degrading to an
// integer or a name-only entry.
func (s *Store) MatchedRoutesForChannel(ctx context.Context, channelIDText string) ([]MatchedRoute, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT rd.route_id, r.name, COUNT(DISTINCT rd.video_id)
		 FROM route_decision rd
		 JOIN run ON run.id = rd.run_id
		 JOIN video v ON v.id = rd.video_id
		 LEFT JOIN route r ON r.id = rd.route_id
		 WHERE v.channel_id = ? AND rd.decision = 'matched'
		   AND run.run_type IN ('scheduled','manual')
		 GROUP BY rd.route_id, r.name;`, channelIDText)
	if err != nil {
		return nil, fmt.Errorf("matched routes for %s: %w", channelIDText, err)
	}
	defer rows.Close()

	type pending struct {
		routeKey   int64
		name       sql.NullString
		matchCount int
	}
	entries := make([]pending, 0)
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.routeKey, &p.name, &p.matchCount); err != nil {
			return nil, fmt.Errorf("scan matched route for %s: %w", channelIDText, err)
		}
		entries = append(entries, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate matched routes for %s: %w", channelIDText, err)
	}

	publicIDs, err := s.routes.ListRouteIdentities(ctx)
	if err != nil {
		return nil, fmt.Errorf("matched routes for %s: %w", channelIDText, err)
	}

	out := make([]MatchedRoute, 0, len(entries))
	for _, e := range entries {
		if !e.name.Valid {
			// The decision evidence exists but its route row does not, so the
			// required display label cannot be produced honestly.
			return nil, fmt.Errorf("route %d: missing route row for matched-route label", e.routeKey)
		}
		publicID, err := publicRouteID(publicIDs, e.routeKey)
		if err != nil {
			return nil, err
		}
		out = append(out, MatchedRoute{
			RouteID:    publicID,
			Name:       e.name.String,
			MatchCount: e.matchCount,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].RouteID < out[j].RouteID
	})
	return out, nil
}

// ListDuplicateProvenance returns distinct-pair duplicate-match rows anchored
// at this channel's evaluated videos. DiscoveryCreditChannelID/Name come from
// the pair's two native video observations' chronology — the earlier
// observation's channel (ADR-014) — and stay empty when the pair has no
// orderable native evidence. The matched fingerprint's channel association is
// never substituted, and legacy same-ID self-match rows are excluded.
func (s *Store) ListDuplicateProvenance(ctx context.Context, channelIDText string) ([]DuplicateProvenance, error) {
	facts, err := s.loadDuplicateMatchFacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("duplicate provenance for %s: %w", channelIDText, err)
	}

	out := make([]DuplicateProvenance, 0)
	for _, f := range facts {
		if f.videoChannelID != channelIDText {
			continue
		}
		dp := DuplicateProvenance{
			VideoID:              f.videoID,
			ChannelID:            f.videoChannelID,
			MatchedFingerprintID: f.matchedFingerprintID,
			SimilarityScore:      f.similarityScore,
			MatchType:            f.matchType,
		}
		if roles, ok := f.roles(); ok {
			dp.DiscoveryCreditChannelID = roles.discoveryChannelID
			dp.DiscoveryCreditChannelName = roles.discoveryChannelName
		}
		out = append(out, dp)
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
// Grouping stays keyed by the internal integer route id so same-named routes
// never merge; the returned route_id is that key's stable public UUID and the
// route_name is the current persisted display label (cross-run analytics are
// not run-scoped, ARS-001).
func (s *Store) GetRouteMatchRates(ctx context.Context, from, to time.Time, granularity string) ([]RouteMatchRate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.name, run.started_at, rd.decision
		 FROM route_decision rd
		 JOIN route r ON r.id = rd.route_id
		 JOIN run ON run.id = rd.run_id
		 WHERE run.started_at >= ? AND run.started_at < ? AND run.status = 'completed'
		 ORDER BY run.started_at, r.evaluation_order;`,
		from.UTC().Format(state.RunTimestampLayout), to.UTC().Format(state.RunTimestampLayout))
	if err != nil {
		return nil, fmt.Errorf("route match rates: %w", err)
	}
	defer rows.Close()

	type key struct {
		routeID int64
		bucket  string
	}
	type group struct {
		routeKey  int64
		routeName string
		bucket    string
		evaluated int
		matched   int
	}
	groups := make(map[key]*group)
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
			g = &group{routeKey: routeID, routeName: routeName, bucket: k.bucket}
			groups[k] = g
		}
		g.evaluated++
		if decision == "matched" {
			g.matched++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate route match rates: %w", err)
	}

	publicIDs, err := s.routes.ListRouteIdentities(ctx)
	if err != nil {
		return nil, fmt.Errorf("route match rates: %w", err)
	}

	out := make([]RouteMatchRate, 0, len(groups))
	for _, g := range groups {
		publicID, err := publicRouteID(publicIDs, g.routeKey)
		if err != nil {
			return nil, err
		}
		out = append(out, RouteMatchRate{
			RouteID:   publicID,
			RouteName: g.routeName,
			Bucket:    g.bucket,
			Evaluated: g.evaluated,
			Matched:   g.matched,
			MatchRate: safeRate(g.matched, g.evaluated),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bucket != out[j].Bucket {
			return out[i].Bucket < out[j].Bucket
		}
		if out[i].RouteName != out[j].RouteName {
			return out[i].RouteName < out[j].RouteName
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

// publicRouteID resolves one internal route key to its public UUID. A key with
// no registry row is an integrity error: the endpoint fails rather than
// emitting an integer, a route name, or a partial result (ARS-004 invariant 6).
func publicRouteID(ids map[int64]string, routeKey int64) (string, error) {
	publicID, ok := ids[routeKey]
	if !ok || publicID == "" {
		return "", fmt.Errorf("route %d: missing route identity mapping", routeKey)
	}
	return publicID, nil
}

// runRouteContext returns the route labels and evaluation orders captured in
// the run's immutable ConfigVersion snapshot (OC-003), keyed by internal
// route key. It returns nil maps with no error when the run has no linked
// snapshot, the version row is missing, or the stored snapshot is
// undecodable: OC-003 treats those as incomplete configuration context, and
// the caller falls back to the current persisted label and to decision-order
// evidence — the same policy the run-video decision projection uses. A
// failure to read the snapshot row is a real error and fails the request.
//
// Only positive snapshot orders are returned as usable historical order; a
// non-positive stored value is not a historical ordinal and is not
// transformed into one.
func (s *Store) runRouteContext(ctx context.Context, runID int64) (map[int64]string, map[int64]int, error) {
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT cv.snapshot
		 FROM run
		 LEFT JOIN config_version cv ON cv.id = run.config_version_id
		 WHERE run.id = ?;`, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("run route context for run %d: %w", runID, err)
	}
	if !raw.Valid {
		return nil, nil, nil
	}

	// Only the route id/name/order triples are read here; the rest of the
	// immutable snapshot stays opaque, mirroring internal/store's snapshot
	// decode.
	var snapshot struct {
		Routes []struct {
			ID              json.RawMessage `json:"id"`
			Name            string          `json:"name"`
			EvaluationOrder int             `json:"evaluation_order"`
		} `json:"routes"`
	}
	if err := json.Unmarshal([]byte(raw.String), &snapshot); err != nil {
		// A malformed stored snapshot is OC-003 incomplete configuration
		// context, not a failed read: warn and let current persisted labels
		// and decision evidence fill in rather than failing run inspection
		// or fabricating a label or an order.
		slog.WarnContext(ctx, "run configuration snapshot unavailable; using current route names",
			"run_id", runID, "error", err)
		return nil, nil, nil
	}
	labels := make(map[int64]string, len(snapshot.Routes))
	orders := make(map[int64]int, len(snapshot.Routes))
	for _, r := range snapshot.Routes {
		var routeKey int64
		if err := json.Unmarshal(r.ID, &routeKey); err != nil {
			// A non-integer id is not a recoverable key; that route keeps the
			// current persisted label instead.
			continue
		}
		labels[routeKey] = r.Name
		if r.EvaluationOrder > 0 {
			orders[routeKey] = r.EvaluationOrder
		}
	}
	return labels, orders, nil
}

// decisionRouteOrders returns each route's evaluation order for the run from
// persisted route_decision rows, keyed by internal route key. An order is
// returned only when every persisted decision for that route in the run
// agrees on one positive integer: agreement is the evidence that this was the
// route's order when the run evaluated it. Disagreeing, zero, or negative
// values yield no order for the route — the caller omits the field rather
// than transforming internal sentinel bookkeeping into an apparent ordinal
// (ARS-001). A route with no decision rows in the run has no order here.
func (s *Store) decisionRouteOrders(ctx context.Context, runID int64) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT route_id, MIN(evaluation_order), COUNT(DISTINCT evaluation_order)
		 FROM route_decision
		 WHERE run_id = ?
		 GROUP BY route_id;`, runID)
	if err != nil {
		return nil, fmt.Errorf("decision route orders for run %d: %w", runID, err)
	}
	defer rows.Close()

	out := make(map[int64]int)
	for rows.Next() {
		var routeKey, distinct int64
		var order int
		if err := rows.Scan(&routeKey, &order, &distinct); err != nil {
			return nil, fmt.Errorf("scan decision route orders: %w", err)
		}
		if distinct == 1 && order > 0 {
			out[routeKey] = order
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate decision route orders: %w", err)
	}
	return out, nil
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
