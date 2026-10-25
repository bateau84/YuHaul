CREATE TABLE run_video_outcome (
    run_id INTEGER NOT NULL REFERENCES run(id),
    video_id INTEGER NOT NULL REFERENCES video(id),
    final_outcome TEXT CHECK (final_outcome IS NULL OR final_outcome IN ('accepted','rejected','excluded','deferred','failed','not_yet_evaluated')),
    deferred_reason TEXT,
    error_message TEXT,
    completed_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (run_id, video_id),
    CHECK ((final_outcome = 'deferred' AND deferred_reason IS NOT NULL AND deferred_reason <> '') OR (final_outcome IS NOT 'deferred' AND deferred_reason IS NULL)),
    CHECK ((final_outcome = 'failed' AND error_message IS NOT NULL AND error_message <> '') OR (final_outcome IS NOT 'failed' AND error_message IS NULL))
);
