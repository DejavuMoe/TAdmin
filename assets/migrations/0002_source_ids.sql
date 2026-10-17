ALTER TABLE preview_sources RENAME TO preview_sources_old;

-- Never reuse a deleted source's id while an old fetch may still be running.
CREATE TABLE preview_sources (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    url           TEXT    NOT NULL UNIQUE,
    kind          TEXT    NOT NULL,
    title         TEXT    NOT NULL DEFAULT '',
    enabled       INTEGER NOT NULL DEFAULT 1,
    etag          TEXT    NOT NULL DEFAULT '',
    last_modified TEXT    NOT NULL DEFAULT '',
    checked_at    INTEGER NOT NULL DEFAULT 0,
    next_check    INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT    NOT NULL DEFAULT '',
    url_count     INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);
INSERT INTO preview_sources SELECT * FROM preview_sources_old;
DROP TABLE preview_sources_old;
UPDATE preview_urls SET source_id = 0
WHERE source_id <> 0 AND NOT EXISTS (SELECT 1 FROM preview_sources WHERE id = source_id);
