CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value BLOB NOT NULL
);

CREATE TABLE chats (
    chat_id        INTEGER PRIMARY KEY,
    title          TEXT    NOT NULL DEFAULT '',
    username       TEXT    NOT NULL DEFAULT '',
    status         TEXT    NOT NULL,
    linked_chat_id INTEGER NOT NULL DEFAULT 0,
    added_by       INTEGER NOT NULL DEFAULT 0,
    settings       TEXT    NOT NULL DEFAULT '{}',
    lockdown_until INTEGER NOT NULL DEFAULT 0,
    problems       TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE gates (
    id            INTEGER PRIMARY KEY,
    chat_id       INTEGER NOT NULL,
    user_id       INTEGER NOT NULL,
    source        TEXT    NOT NULL,
    state         TEXT    NOT NULL,
    outcome       TEXT    NOT NULL DEFAULT '',
    delivery      TEXT    NOT NULL,
    kind          TEXT    NOT NULL,
    seed          BLOB    NOT NULL,
    gen           INTEGER NOT NULL DEFAULT 1,
    picks         TEXT    NOT NULL DEFAULT '',
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL,
    deadline      INTEGER NOT NULL,
    next_at       INTEGER NOT NULL,
    steps         INTEGER NOT NULL DEFAULT 0,
    retries       INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT    NOT NULL DEFAULT '',
    token         TEXT    NOT NULL UNIQUE,
    user_name     TEXT    NOT NULL DEFAULT '',
    user_username TEXT    NOT NULL DEFAULT '',
    lang          TEXT    NOT NULL DEFAULT '',
    user_chat_id  INTEGER NOT NULL DEFAULT 0,
    query_id      TEXT    NOT NULL DEFAULT '',
    prev_perms    TEXT    NOT NULL DEFAULT '',
    prev_until    INTEGER NOT NULL DEFAULT 0,
    prompt_msg    INTEGER NOT NULL DEFAULT 0,
    captcha_chat  INTEGER NOT NULL DEFAULT 0,
    captcha_msg   INTEGER NOT NULL DEFAULT 0,
    eph_msg       INTEGER NOT NULL DEFAULT 0,
    review_chat   INTEGER NOT NULL DEFAULT 0,
    review_msg    INTEGER NOT NULL DEFAULT 0,
    join_msg      INTEGER NOT NULL DEFAULT 0,
    risk          REAL    NOT NULL DEFAULT -1,
    risk_reason   TEXT    NOT NULL DEFAULT '',
    flags         INTEGER NOT NULL DEFAULT 0,
    actor_id      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    closed_at     INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX gates_open ON gates(chat_id, user_id) WHERE state <> 'closed';
CREATE INDEX gates_due ON gates(next_at) WHERE state <> 'closed';
CREATE INDEX gates_history ON gates(chat_id, user_id, closed_at) WHERE state = 'closed';

CREATE TABLE events (
    id       INTEGER PRIMARY KEY,
    at       INTEGER NOT NULL,
    chat_id  INTEGER NOT NULL,
    kind     TEXT    NOT NULL,
    user_id  INTEGER NOT NULL DEFAULT 0,
    actor_id INTEGER NOT NULL DEFAULT 0,
    value    INTEGER NOT NULL DEFAULT 0,
    data     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX events_chat_at ON events(chat_id, at);
CREATE INDEX events_at ON events(at);

CREATE TABLE deletions (
    chat_id    INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    user_id    INTEGER NOT NULL DEFAULT 0,
    due        INTEGER NOT NULL,
    PRIMARY KEY (chat_id, message_id, user_id)
);
CREATE INDEX deletions_due ON deletions(due);

CREATE TABLE channel_pins (
    chat_id    INTEGER NOT NULL,
    message_id INTEGER NOT NULL,
    channel_id INTEGER NOT NULL,
    at         INTEGER NOT NULL,
    PRIMARY KEY (chat_id, message_id)
);

CREATE TABLE preview_sources (
    id            INTEGER PRIMARY KEY,
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

CREATE TABLE preview_urls (
    url          TEXT PRIMARY KEY,
    source_id    INTEGER NOT NULL DEFAULT 0,
    lastmod      INTEGER NOT NULL DEFAULT 0,
    seen_at      INTEGER NOT NULL,
    state        TEXT    NOT NULL DEFAULT 'idle',
    priority     INTEGER NOT NULL DEFAULT 0,
    queued_at    INTEGER NOT NULL DEFAULT 0,
    refreshed_at INTEGER NOT NULL DEFAULT 0,
    attempts     INTEGER NOT NULL DEFAULT 0,
    result       TEXT    NOT NULL DEFAULT '',
    reply        TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX preview_queue ON preview_urls(priority DESC, queued_at) WHERE state = 'queued';
CREATE INDEX preview_by_source ON preview_urls(source_id);
