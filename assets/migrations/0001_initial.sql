CREATE TABLE managed_chats (
    chat_id INTEGER PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    language TEXT NOT NULL DEFAULT 'zh-CN' CHECK (language IN ('zh-CN', 'en')),
    management_topic_id INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE verification_sessions (
    id TEXT PRIMARY KEY,
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    status TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX verification_sessions_chat_user_idx
    ON verification_sessions(chat_id, user_id);

CREATE TABLE trusted_users (
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    trusted_until INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE member_states (
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    joined_at INTEGER NOT NULL,
    recent_until INTEGER NOT NULL,
    normal_message_count INTEGER NOT NULL DEFAULT 0 CHECK (normal_message_count >= 0),
    PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE list_entries (
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('allow', 'block')),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, user_id, kind)
);

CREATE TABLE violations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    rule_id TEXT NOT NULL,
    score INTEGER NOT NULL CHECK (score >= 0),
    content_hash TEXT,
    created_at INTEGER NOT NULL
);

CREATE INDEX violations_chat_user_created_idx
    ON violations(chat_id, user_id, created_at);

CREATE TABLE review_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    verification_session_id TEXT REFERENCES verification_sessions(id) ON DELETE SET NULL,
    status TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX review_items_chat_status_idx
    ON review_items(chat_id, status);

CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER REFERENCES managed_chats(chat_id) ON DELETE SET NULL,
    actor_user_id INTEGER,
    target_user_id INTEGER,
    event_type TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL
);

CREATE INDEX audit_events_chat_created_idx
    ON audit_events(chat_id, created_at);
