CREATE TABLE playlist_send_guard (
    video_id INTEGER NOT NULL REFERENCES video(id) ON DELETE NO ACTION,
    target_playlist_id TEXT NOT NULL CHECK (target_playlist_id <> ''),
    action_type TEXT NOT NULL CHECK (action_type = 'playlist_insert'),
    run_id INTEGER NOT NULL REFERENCES run(id) ON DELETE NO ACTION,
    route_decision_id INTEGER NOT NULL REFERENCES route_decision(id) ON DELETE NO ACTION,
    authorization_id TEXT NOT NULL CHECK (authorization_id <> ''),
    prepared_at TIMESTAMP NOT NULL,
    PRIMARY KEY (video_id, target_playlist_id, action_type)
);
