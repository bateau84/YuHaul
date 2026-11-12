package videosearch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// Service errors the server maps to HTTP codes (ARS-001 §2).
var (
	ErrInvalidVideoID = errors.New("invalid video id")
	ErrNotFound       = errors.New("video not found")
)

// Service answers search and view requests from the local database only. It
// never calls YouTube and never writes (ARS-001 §8).
type Service struct {
	db  *sql.DB
	now func() time.Time
}

// NewService returns a Service. A nil now uses time.Now.
func NewService(db *sql.DB, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, now: now}
}

// Paging carries the raw cursor and page query values (ARS-001 §2). The Has
// flags distinguish an absent parameter from an empty one.
type Paging struct {
	Cursor    string
	HasCursor bool
	Page      string
	HasPage   bool
}

// ---- Wire types (ARS-001 §4) -----------------------------------------------

// Page is one page of name-search results.
type Page struct {
	Items         []Summary `json:"items"`
	Total         int       `json:"total"`
	PageSize      int       `json:"page_size"`
	Number        int       `json:"number"`
	TotalPages    int       `json:"total_pages"`
	FirstPosition *int      `json:"first_position"`
	LastPosition  *int      `json:"last_position"`
	NextCursor    *string   `json:"next_cursor"`
	PrevCursor    *string   `json:"prev_cursor"`
}

// SearchResult is the body of GET /api/videos/search.
type SearchResult struct {
	Kind     string `json:"kind"`
	VideoID  string `json:"video_id,omitempty"`
	FromLink *bool  `json:"from_link,omitempty"`
	Results  *Page  `json:"results,omitempty"`

	// Stats feed the operational log. They hold counts only.
	Stats Stats `json:"-"`
}

// Stats are the counts a request logs (never text).
type Stats struct{ Candidates, Matches int }

// TitleInfo is a title and whether it may be shown.
type TitleInfo struct {
	Text         *string `json:"text"`
	Availability string  `json:"availability"`
}

// ChannelInfo is the channel name (shown only when held) and ID.
type ChannelInfo struct {
	Name             *string `json:"name"`
	NameAvailability string  `json:"name_availability"`
	ChannelID        *string `json:"channel_id"`
}

// Summary is a result row and the head of the view.
type Summary struct {
	VideoID       string      `json:"video_id"`
	Record        string      `json:"record"`
	Title         TitleInfo   `json:"title"`
	Channel       ChannelInfo `json:"channel"`
	LatestOutcome any         `json:"latest_outcome"`
	QueueState    any         `json:"queue_state"`
}

// The Latest-outcome and queue-state members. Each has exactly one JSON shape.
type (
	stateOnly struct {
		State string `json:"state"`
	}
	committedOutcome struct {
		State       string  `json:"state"`
		Outcome     string  `json:"outcome"`
		RunID       *int64  `json:"run_id"`
		CommittedAt *string `json:"committed_at"`
	}
	deferredClaimedState struct {
		State         string `json:"state"`
		Reason        string `json:"reason"`
		ClaimingRunID *int64 `json:"claiming_run_id"`
	}
	deferredPendingState struct {
		State  string `json:"state"`
		Reason string `json:"reason"`
	}
	errorsClaimedState struct {
		State         string `json:"state"`
		Category      string `json:"category"`
		ClaimingRunID *int64 `json:"claiming_run_id"`
	}
	errorsActiveState struct {
		State    string `json:"state"`
		Category string `json:"category"`
	}
)

// RunEntry is one run that recorded a decision or outcome for the video.
type RunEntry struct {
	RunID             int64   `json:"run_id"`
	RunType           string  `json:"run_type"`
	Mode              string  `json:"mode"`
	StartedAt         *string `json:"started_at"`
	InitiatedBy       *string `json:"initiated_by"`
	RunStatus         string  `json:"run_status"`
	Outcome           *string `json:"outcome"`
	DecisionDetailURL string  `json:"decision_detail_path"`
}

// VideoView is the body of GET /api/videos/{videoId}.
type VideoView struct {
	Summary
	Runs []RunEntry `json:"runs"`
}

// wireAvailability maps the store's availability to the wire (ARS-001 §1.1).
func wireAvailability(a Availability) string {
	switch a {
	case Held:
		return "current_local"
	case Expired:
		return "expired"
	default:
		return "unavailable"
	}
}

// ---- Search ----------------------------------------------------------------

func (s *Service) begin(ctx context.Context) (*sql.Tx, error) {
	return s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
}

// pagingErr reports the 400 error for paging parameters sent with a request
// that cannot page.
func pagingErr(p Paging) error {
	switch {
	case p.HasPage:
		return ErrInvalidPage
	case p.HasCursor:
		return ErrInvalidCursor
	}
	return nil
}

// Search classifies raw and answers it (ARS-001 §3, §4.1, §6, §7). Inputs that
// need no lookup are answered without opening a transaction.
func (s *Service) Search(ctx context.Context, raw string, p Paging) (SearchResult, error) {
	in := Classify(raw)
	fromLink := in.Kind == KindLinkID
	switch in.Kind {
	case KindEmpty, KindInvalidText, KindTooLong, KindUnusableLink, KindTooShort:
		if err := pagingErr(p); err != nil {
			return SearchResult{}, err
		}
		return SearchResult{Kind: string(in.Kind)}, nil
	case KindLinkID:
		// A link never pages: found gives `video`, not found has no results.
		if err := pagingErr(p); err != nil {
			return SearchResult{}, err
		}
	}

	tx, err := s.begin(ctx)
	if err != nil {
		return SearchResult{}, fmt.Errorf("videosearch: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only; rollback is the end of the snapshot

	if in.Kind == KindLinkID || in.Kind == KindTypedID {
		found, err := exactIDExists(ctx, tx, in.VideoID)
		if err != nil {
			return SearchResult{}, err
		}
		if found {
			if err := pagingErr(p); err != nil {
				return SearchResult{}, err
			}
			return SearchResult{Kind: "video", VideoID: in.VideoID, FromLink: &fromLink}, nil
		}
		if fromLink {
			return SearchResult{Kind: "id_not_found", VideoID: in.VideoID, FromLink: &fromLink}, nil
		}
	}

	page, stats, err := s.namePage(ctx, tx, in.Text, p)
	if err != nil {
		return SearchResult{}, err
	}
	res := SearchResult{Kind: "results", Results: page, Stats: stats}
	if in.Kind == KindTypedID {
		res.Kind, res.VideoID, res.FromLink = "id_not_found", in.VideoID, &fromLink
	}
	return res, nil
}

// exactIDExists is ARS-001 §6.2.
func exactIDExists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var src string
	err := tx.QueryRowContext(ctx, `SELECT 'video' AS src FROM video WHERE video_id = ?
UNION ALL
SELECT 'fingerprint' FROM title_fingerprint
 WHERE video_id = ? AND (source = 'user_import' OR is_duplicate_reference = 1)
LIMIT 1`, id, id).Scan(&src)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("videosearch: exact id lookup: %w", err)
	}
	return true, nil
}

// namePage runs the §6.1 scan and cuts the requested page from the sorted
// matches (§7).
func (s *Service) namePage(ctx context.Context, tx *sql.Tx, text string, p Paging) (*Page, Stats, error) {
	folder := NewFolder()
	needle := folder.Key(text)

	// Validate paging against this needle before any database read cost is
	// spent on a request that will be refused.
	var (
		boundary  Boundary
		hasCursor bool
		pageNo    int
	)
	switch {
	case p.HasCursor && p.HasPage:
		return nil, Stats{}, ErrInvalidPage
	case p.HasPage:
		n, err := ParsePage(p.Page)
		if err != nil {
			return nil, Stats{}, err
		}
		pageNo = n
	case p.HasCursor:
		b, err := decodeCursor(p.Cursor, needle)
		if err != nil {
			return nil, Stats{}, err
		}
		boundary, hasCursor = b, true
	}

	matches, cutoff, candidates, err := scanMatches(ctx, tx, folder, needle, s.now)
	if err != nil {
		return nil, Stats{}, err
	}
	keys := make([]SortKey, len(matches))
	for i, m := range matches {
		keys[i] = m.Key
	}
	SortKeys(keys)
	total := len(keys)

	var (
		w         Window
		requested = 1
	)
	switch {
	case hasCursor:
		w = CursorWindow(keys, boundary)
	case pageNo > 0:
		w, requested = PageWindow(total, pageNo), pageNo
	default:
		w = PageWindow(total, 1)
	}
	info := NewPageInfo(total, w, requested)

	ids := make([]string, 0, w.End-w.Start)
	for _, k := range keys[w.Start:w.End] {
		ids = append(ids, k.VideoID)
	}
	items, err := summaries(ctx, tx, ids, cutoff)
	if err != nil {
		return nil, Stats{}, err
	}

	page := &Page{
		Items: items, Total: total, PageSize: PageSize,
		Number: info.Number, TotalPages: info.TotalPages,
	}
	if info.LastPosition > 0 {
		first, last := info.FirstPosition, info.LastPosition
		page.FirstPosition, page.LastPosition = &first, &last
		if info.HasNext {
			c := encodeCursor(needle, Boundary{Dir: After, Key: keys[w.End-1]})
			page.NextCursor = &c
		}
		if info.HasPrev {
			c := encodeCursor(needle, Boundary{Dir: Before, Key: keys[w.Start]})
			page.PrevCursor = &c
		}
	}
	return page, Stats{Candidates: candidates, Matches: total}, nil
}

// ---- View ------------------------------------------------------------------

// View answers GET /api/videos/{videoId} (ARS-001 §4.3, §6.4).
func (s *Service) View(ctx context.Context, videoID string) (VideoView, error) {
	if !ValidVideoID(videoID) {
		return VideoView{}, ErrInvalidVideoID
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return VideoView{}, fmt.Errorf("videosearch: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only snapshot

	found, err := exactIDExists(ctx, tx, videoID)
	if err != nil {
		return VideoView{}, err
	}
	if !found {
		return VideoView{}, ErrNotFound
	}
	md, _, err := ProjectMetadataAfterRead(ctx, tx, []string{videoID}, s.now)
	if err != nil {
		return VideoView{}, err
	}
	items, err := summariesWithMetadata(ctx, tx, []string{videoID}, md)
	if err != nil {
		return VideoView{}, err
	}
	if len(items) != 1 {
		return VideoView{}, ErrNotFound
	}
	view := VideoView{Summary: items[0], Runs: []RunEntry{}}

	var vid sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM video WHERE video_id = ?`, videoID).Scan(&vid); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return VideoView{}, fmt.Errorf("videosearch: video id: %w", err)
	}
	if !vid.Valid { // imported reference: no runs
		return view, nil
	}
	runs, err := runEntries(ctx, tx, vid.Int64, videoID)
	if err != nil {
		return VideoView{}, err
	}
	view.Runs = runs
	return view, nil
}

// runsSQL is ARS-001 §6.4 (runs for the view). The outcome CASE is the shared
// state.RunVideoOutcomeCase.
var runsSQL = `WITH links AS (
  SELECT run_id FROM route_decision WHERE video_id = ?1
  UNION
  SELECT run_id FROM run_video_outcome
   -- every committed outcome lists the run, including unavailable_on_youtube
   WHERE video_id = ?1 AND final_outcome IS NOT NULL AND final_outcome <> 'not_yet_evaluated'
), evidence AS (
  SELECT run_id,
         MAX(decision = 'matched') AS matched,
         MAX(decision = 'not_matched') AS not_matched,
         MAX(decision = 'skipped' AND skip_reason = 'exclusion') AS excluded
  FROM route_decision WHERE video_id = ?1 GROUP BY run_id
)
SELECT r.id, r.run_type, r.status, r.started_at, r.initiated_by,
  ` + state.RunVideoOutcomeCase + ` AS outcome
FROM links l JOIN run r ON r.id = l.run_id
LEFT JOIN evidence e ON e.run_id = r.id
LEFT JOIN run_video_outcome o ON o.run_id = r.id AND o.video_id = ?1
ORDER BY r.id DESC`

func isSimulation(runType string) bool {
	return runType == "dry_run" || runType == "replay" || runType == "backtest"
}

func runEntries(ctx context.Context, tx *sql.Tx, vid int64, videoID string) ([]RunEntry, error) {
	rows, err := tx.QueryContext(ctx, runsSQL, vid)
	if err != nil {
		return nil, fmt.Errorf("videosearch: runs: %w", err)
	}
	defer rows.Close()
	out := []RunEntry{}
	for rows.Next() {
		var (
			e                  RunEntry
			started            any
			initiated, outcome sql.NullString
		)
		if err := rows.Scan(&e.RunID, &e.RunType, &e.RunStatus, &started, &initiated, &outcome); err != nil {
			return nil, fmt.Errorf("videosearch: scan run: %w", err)
		}
		e.Mode = "production"
		if isSimulation(e.RunType) {
			e.Mode = "simulation"
		}
		e.StartedAt = rfc3339(started)
		if initiated.Valid {
			e.InitiatedBy = &initiated.String
		}
		if outcome.Valid {
			e.Outcome = &outcome.String
		}
		e.DecisionDetailURL = fmt.Sprintf("/runs/%d/video/%s", e.RunID, videoID)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("videosearch: iterate runs: %w", err)
	}
	return out, nil
}

// rfc3339 renders a stored time as RFC 3339 UTC; unparseable or empty is nil.
func rfc3339(v any) *string {
	if v == nil {
		return nil
	}
	t, ok := metadata.ParseLocalTime(v)
	if !ok {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// ---- Summary projection (ARS-001 §6.3) -------------------------------------

func inList(n int) string { return "(?" + strings.Repeat(",?", n-1) + ")" }

func anyArgs[T any](xs []T) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// summaries projects ids (at most 50) into Summary rows in the given order.
func summaries(ctx context.Context, tx *sql.Tx, ids []string, cutoff time.Time) ([]Summary, error) {
	md, err := ProjectMetadata(ctx, tx, ids, cutoff)
	if err != nil {
		return nil, err
	}
	return summariesWithMetadata(ctx, tx, ids, md)
}

type videoRow struct {
	internalID  int64
	channelID   sql.NullString
	outcome     sql.NullString
	completedAt any
}

func summariesWithMetadata(ctx context.Context, tx *sql.Tx, ids []string, md map[string]Metadata) ([]Summary, error) {
	if len(ids) == 0 {
		return []Summary{}, nil
	}
	args := anyArgs(ids)
	list := inList(len(ids))

	// 1. video rows.
	videos := map[string]videoRow{}
	internal := []int64{}
	rows, err := tx.QueryContext(ctx, `SELECT id, video_id, channel_id, final_outcome, completed_at FROM video WHERE video_id IN `+list, args...)
	if err != nil {
		return nil, fmt.Errorf("videosearch: read videos: %w", err)
	}
	for rows.Next() {
		var (
			v  videoRow
			id string
		)
		if err := rows.Scan(&v.internalID, &id, &v.channelID, &v.outcome, &v.completedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("videosearch: scan video: %w", err)
		}
		videos[id] = v
		internal = append(internal, v.internalID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("videosearch: iterate videos: %w", err)
	}
	rows.Close()

	// 2. imported-reference rows. The select list never includes fingerprint
	// or fingerprint_tokens.
	fpChannel := map[string]sql.NullString{}
	rows, err = tx.QueryContext(ctx, `SELECT video_id, channel_id FROM title_fingerprint
WHERE video_id IN `+list+` AND (source = 'user_import' OR is_duplicate_reference = 1) ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("videosearch: read references: %w", err)
	}
	for rows.Next() {
		var (
			id string
			ch sql.NullString
		)
		if err := rows.Scan(&id, &ch); err != nil {
			rows.Close()
			return nil, fmt.Errorf("videosearch: scan reference: %w", err)
		}
		if cur, seen := fpChannel[id]; !seen || (!cur.Valid && ch.Valid) {
			fpChannel[id] = ch
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("videosearch: iterate references: %w", err)
	}
	rows.Close()

	// 4. links, latest outcome, queue state for the known videos.
	simOnly, err := simulationOnly(ctx, tx, internal)
	if err != nil {
		return nil, err
	}
	latest, err := latestRuns(ctx, tx, internal)
	if err != nil {
		return nil, err
	}
	queues, err := queueStates(ctx, tx, internal)
	if err != nil {
		return nil, err
	}

	out := make([]Summary, 0, len(ids))
	for _, id := range ids {
		v, known := videos[id]
		fp, imported := fpChannel[id]
		if !known && !imported {
			continue // not searchable (cannot happen inside one snapshot)
		}
		m := md[id]
		sum := Summary{
			VideoID: id,
			Title:   TitleInfo{Text: m.Title, Availability: wireAvailability(m.TitleAvailability)},
			Channel: ChannelInfo{Name: m.ChannelName, NameAvailability: wireAvailability(m.ChannelAvailability)},
		}
		if m.TitleAvailability != Held {
			sum.Title.Text = nil
		}
		if m.ChannelAvailability != Held {
			sum.Channel.Name = nil
		}
		switch {
		case known && v.channelID.Valid:
			sum.Channel.ChannelID = &v.channelID.String
		case imported && fp.Valid:
			sum.Channel.ChannelID = &fp.String
		}
		switch {
		case known && simOnly[v.internalID]:
			sum.Record = "simulation_only"
			sum.LatestOutcome, sum.QueueState = stateOnly{"simulation_only"}, stateOnly{"none"}
		case known:
			sum.Record = "known"
			sum.LatestOutcome = latestOutcome(v, latest)
			sum.QueueState = queues[v.internalID]
		default:
			sum.Record = "imported_reference"
			sum.LatestOutcome, sum.QueueState = stateOnly{"imported_reference"}, stateOnly{"none"}
		}
		out = append(out, sum)
	}
	return out, nil
}

func latestOutcome(v videoRow, latest map[int64]latestRun) any {
	if !v.outcome.Valid || v.outcome.String == "" || v.outcome.String == "not_yet_evaluated" {
		return stateOnly{"not_yet_evaluated"}
	}
	c := committedOutcome{State: "committed", Outcome: v.outcome.String}
	var at any = v.completedAt
	if l, ok := latest[v.internalID]; ok {
		id := l.runID
		c.RunID = &id
		if l.completedAt != nil {
			at = l.completedAt
		}
	}
	c.CommittedAt = rfc3339(at)
	return c
}

// simulationOnly is ARS-001 §6.4 (links): a video is simulation-only when it
// has at least one link and every link is a simulation run.
func simulationOnly(ctx context.Context, tx *sql.Tx, internal []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(internal) == 0 {
		return out, nil
	}
	list := inList(len(internal))
	args := make([]any, 0, 5*len(internal))
	for range 5 {
		args = append(args, anyArgs(internal)...)
	}
	rows, err := tx.QueryContext(ctx, `SELECT x.vid, r.run_type FROM (
  SELECT id AS vid, discovered_in_run_id AS run_id FROM video WHERE id IN `+list+` AND discovered_in_run_id IS NOT NULL
  UNION SELECT video_id, run_id FROM route_decision    WHERE video_id IN `+list+`
  UNION SELECT video_id, run_id FROM run_video_outcome WHERE video_id IN `+list+`
  UNION SELECT video_id, run_id FROM deferred_item     WHERE video_id IN `+list+`
  UNION SELECT video_id, run_id FROM error_entry       WHERE video_id IN `+list+`
) x JOIN run r ON r.id = x.run_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("videosearch: links: %w", err)
	}
	defer rows.Close()
	linked, production := map[int64]bool{}, map[int64]bool{}
	for rows.Next() {
		var (
			vid int64
			rt  string
		)
		if err := rows.Scan(&vid, &rt); err != nil {
			return nil, fmt.Errorf("videosearch: scan link: %w", err)
		}
		linked[vid] = true
		if !isSimulation(rt) {
			production[vid] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("videosearch: iterate links: %w", err)
	}
	for vid := range linked {
		out[vid] = !production[vid]
	}
	return out, nil
}

type latestRun struct {
	runID       int64
	completedAt any
}

// latestRuns is ARS-001 §6.5: the newest production run whose stored outcome
// equals the video's committed outcome. A simulation never supplies it.
func latestRuns(ctx context.Context, tx *sql.Tx, internal []int64) (map[int64]latestRun, error) {
	out := map[int64]latestRun{}
	if len(internal) == 0 {
		return out, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.video_id, o.run_id, o.completed_at
FROM run_video_outcome o
JOIN run r ON r.id = o.run_id AND r.run_type IN ('manual','scheduled')
JOIN video v ON v.id = o.video_id
WHERE o.video_id IN `+inList(len(internal))+` AND o.final_outcome = v.final_outcome
  AND o.run_id = (SELECT MAX(o2.run_id) FROM run_video_outcome o2
                  JOIN run r2 ON r2.id = o2.run_id AND r2.run_type IN ('manual','scheduled')
                  WHERE o2.video_id = o.video_id AND o2.final_outcome = v.final_outcome)`, anyArgs(internal)...)
	if err != nil {
		return nil, fmt.Errorf("videosearch: latest outcome: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			vid int64
			l   latestRun
		)
		if err := rows.Scan(&vid, &l.runID, &l.completedAt); err != nil {
			return nil, fmt.Errorf("videosearch: scan latest outcome: %w", err)
		}
		out[vid] = l
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("videosearch: iterate latest outcome: %w", err)
	}
	return out, nil
}

// queueStates is ARS-001 §6.6. Precedence: deferred_claimed, deferred_pending,
// errors_claimed, errors_active, removed_by_user, none; within one state the
// newest item (highest id) wins. A simulation never writes the queue tables.
func queueStates(ctx context.Context, tx *sql.Tx, internal []int64) (map[int64]any, error) {
	out := map[int64]any{}
	if len(internal) == 0 {
		return out, nil
	}
	list := inList(len(internal))
	args := append(anyArgs(internal), anyArgs(internal)...)
	rows, err := tx.QueryContext(ctx, `SELECT 'deferred' AS q, video_id, id, status, deferral_reason AS code, retry_run_id
  FROM deferred_item WHERE video_id IN `+list+` AND status IN ('pending','retrying')
UNION ALL
SELECT 'errors', video_id, id, status, error_category, retry_run_id
  FROM error_entry WHERE video_id IN `+list+` AND status IN ('active','retrying')`, args...)
	if err != nil {
		return nil, fmt.Errorf("videosearch: queue state: %w", err)
	}
	type item struct {
		rank  int
		id    int64
		state any
	}
	best := map[int64]item{}
	for rows.Next() {
		var (
			q, status string
			vid, id   int64
			code      sql.NullString
			run       sql.NullInt64
		)
		if err := rows.Scan(&q, &vid, &id, &status, &code, &run); err != nil {
			rows.Close()
			return nil, fmt.Errorf("videosearch: scan queue state: %w", err)
		}
		var claim *int64
		if run.Valid {
			claim = &run.Int64
		}
		var it item
		switch {
		case q == "deferred" && status == "retrying":
			it = item{0, id, deferredClaimedState{"deferred_claimed", code.String, claim}}
		case q == "deferred":
			it = item{1, id, deferredPendingState{"deferred_pending", code.String}}
		case status == "retrying":
			it = item{2, id, errorsClaimedState{"errors_claimed", code.String, claim}}
		default:
			it = item{3, id, errorsActiveState{"errors_active", code.String}}
		}
		if cur, ok := best[vid]; !ok || it.rank < cur.rank || (it.rank == cur.rank && it.id > cur.id) {
			best[vid] = it
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("videosearch: iterate queue state: %w", err)
	}
	rows.Close()

	var rest []int64
	for _, id := range internal {
		if it, ok := best[id]; ok {
			out[id] = it.state
		} else {
			rest = append(rest, id)
		}
	}
	if len(rest) > 0 {
		values := strings.TrimSuffix(strings.Repeat("(?),", len(rest)), ",")
		rows, err := tx.QueryContext(ctx, `WITH subject(video_id) AS (VALUES `+values+`)
SELECT subject.video_id FROM subject WHERE `+queue.RemovedByUser, anyArgs(rest)...)
		if err != nil {
			return nil, fmt.Errorf("videosearch: removed by user: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, fmt.Errorf("videosearch: scan removed by user: %w", err)
			}
			out[id] = stateOnly{"removed_by_user"}
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("videosearch: iterate removed by user: %w", err)
		}
	}
	for _, id := range internal {
		if _, ok := out[id]; !ok {
			out[id] = stateOnly{"none"}
		}
	}
	return out, nil
}
