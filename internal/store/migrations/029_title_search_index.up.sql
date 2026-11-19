CREATE TABLE title_search_doc (
    doc_id INTEGER PRIMARY KEY,
    video_id TEXT NOT NULL UNIQUE
);

CREATE VIRTUAL TABLE title_search USING fts5(
    k,
    tokenize = 'trigram case_sensitive 1',
    detail = full
);

CREATE TABLE title_search_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    key_version TEXT,
    built_at TEXT
);
INSERT INTO title_search_state (id, key_version, built_at) VALUES (1, NULL, NULL);

CREATE TRIGGER transient_metadata_search_purge_insert
BEFORE INSERT ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = NEW.video_id);
    DELETE FROM title_search_doc WHERE video_id = NEW.video_id;
END;

CREATE TRIGGER transient_metadata_search_purge_update
AFTER UPDATE OF video_id, title, is_active, expired_at, fetched_at, expires_at ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id IN (OLD.video_id, NEW.video_id));
    DELETE FROM title_search_doc WHERE video_id IN (OLD.video_id, NEW.video_id);
END;

CREATE TRIGGER transient_metadata_search_purge_delete
AFTER DELETE ON transient_metadata
BEGIN
    DELETE FROM title_search
     WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = OLD.video_id);
    DELETE FROM title_search_doc WHERE video_id = OLD.video_id;
END;
