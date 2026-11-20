package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/bateau84/YuHaul/internal/state"
)

// withoutUnroutableVideos is the single main-loop input filter of BR-039
// (ARS-006 §5). It keeps only the members the production run may route
// (state.RoutableByRun): a final video, a video whose production success sits
// under another run's decision, and a video held by a queue row this run does
// not own never reach exclusion, duplicate detection, routing or action
// execution. Order is preserved. Simulations never call it.
func (p *Pipeline) withoutUnroutableVideos(ctx context.Context, runID int64, videos []*state.Video) ([]*state.Video, error) {
	if len(videos) == 0 {
		return videos, nil
	}
	ids := make([]int64, len(videos))
	for i, v := range videos {
		ids[i] = v.ID
	}
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("encode member ids: %w", err)
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT v.id, (`+state.RoutableByRun+`
		  AND NOT EXISTS (SELECT 1 FROM run_video_outcome o
		    WHERE o.run_id=:run AND o.video_id=v.id
		      AND v.discovered_in_run_id=:run AND o.final_outcome='deferred')), `+state.FinalVideo+`
		 FROM video v WHERE v.id IN (SELECT value FROM json_each(:ids))`,
		sql.Named("ids", string(idsJSON)), sql.Named("run", runID))
	if err != nil {
		return nil, fmt.Errorf("select routable members: %w", err)
	}
	type verdict struct{ routable, final bool }
	verdicts := make(map[int64]verdict, len(videos))
	for rows.Next() {
		var id int64
		var v verdict
		if err := rows.Scan(&id, &v.routable, &v.final); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan routable member: %w", err)
		}
		verdicts[id] = v
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close routable members: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate routable members: %w", err)
	}

	kept := make([]*state.Video, 0, len(videos))
	var final, held int
	for _, v := range videos {
		verdict, ok := verdicts[v.ID]
		switch {
		case !ok:
			return nil, fmt.Errorf("member video %d does not exist", v.ID)
		case verdict.routable:
			kept = append(kept, v)
		case verdict.final:
			final++
			p.logger.DebugContext(ctx, "run member dropped: video final", "run_id", runID, "video_id", v.ID)
		default:
			held++
			p.logger.DebugContext(ctx, "run member dropped: video held elsewhere", "run_id", runID, "video_id", v.ID)
		}
	}
	if final+held > 0 {
		p.logger.InfoContext(ctx, "run members dropped by routable filter",
			"run_id", runID, "final", final, "held_elsewhere", held, "kept", len(kept))
	}
	return kept, nil
}
