ALTER TABLE managed_chats ADD COLUMN moderation_enabled INTEGER NOT NULL DEFAULT 1
    CHECK (moderation_enabled IN (0, 1));

ALTER TABLE violations ADD COLUMN message_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE violations ADD COLUMN target_kind TEXT NOT NULL DEFAULT 'user'
    CHECK (target_kind IN ('user', 'sender_chat'));
ALTER TABLE violations ADD COLUMN target_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE violations ADD COLUMN domains_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE violations ADD COLUMN base_score INTEGER NOT NULL DEFAULT 0 CHECK (base_score >= 0);

CREATE UNIQUE INDEX violations_message_rule_unique_idx
    ON violations (chat_id, message_id, rule_id)
    WHERE message_id <> 0;
CREATE INDEX violations_target_window_idx
    ON violations (chat_id, target_kind, target_id, created_at);

CREATE TABLE message_observations (
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    message_id INTEGER NOT NULL,
    update_id INTEGER NOT NULL,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('user', 'sender_chat')),
    target_id INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    domains_json TEXT NOT NULL DEFAULT '[]',
    has_link INTEGER NOT NULL DEFAULT 0 CHECK (has_link IN (0, 1)),
    has_telegram_link INTEGER NOT NULL DEFAULT 0 CHECK (has_telegram_link IN (0, 1)),
    has_obfuscation INTEGER NOT NULL DEFAULT 0 CHECK (has_obfuscation IN (0, 1)),
    has_media INTEGER NOT NULL DEFAULT 0 CHECK (has_media IN (0, 1)),
    is_forward INTEGER NOT NULL DEFAULT 0 CHECK (is_forward IN (0, 1)),
    is_recent INTEGER NOT NULL DEFAULT 0 CHECK (is_recent IN (0, 1)),
    mention_count INTEGER NOT NULL DEFAULT 0 CHECK (mention_count >= 0),
    message_at INTEGER NOT NULL,
    version_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, message_id)
);

CREATE INDEX message_observations_target_time_idx
    ON message_observations (chat_id, target_kind, target_id, message_at);
CREATE INDEX message_observations_hash_time_idx
    ON message_observations (chat_id, content_hash, message_at);

CREATE TABLE moderation_subjects (
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('user', 'sender_chat')),
    target_id INTEGER NOT NULL,
    last_threshold INTEGER NOT NULL DEFAULT 0 CHECK (last_threshold IN (0, 3, 5, 8, 12)),
    last_action_at INTEGER,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (chat_id, target_kind, target_id)
);

CREATE TABLE moderation_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    idempotency_key TEXT NOT NULL UNIQUE,
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    message_id INTEGER NOT NULL DEFAULT 0,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('user', 'sender_chat', 'chat')),
    target_id INTEGER NOT NULL,
    actor_user_id INTEGER,
    action_kind TEXT NOT NULL CHECK (action_kind IN (
        'delete_warn', 'delete_mute', 'delete_ban',
        'warn', 'mute', 'kick', 'ban', 'notify', 'log', 'raid_notice'
    )),
    reason TEXT NOT NULL DEFAULT '',
    score INTEGER NOT NULL DEFAULT 0 CHECK (score >= 0),
    rule_ids_json TEXT NOT NULL DEFAULT '[]',
    domains_json TEXT NOT NULL DEFAULT '[]',
    until_at INTEGER,
    result TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    completed_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX moderation_actions_claim_idx
    ON moderation_actions (status, updated_at, id);
CREATE INDEX moderation_actions_chat_time_idx
    ON moderation_actions (chat_id, created_at);

CREATE TABLE raid_states (
    chat_id INTEGER PRIMARY KEY REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    mode TEXT NOT NULL DEFAULT 'auto' CHECK (mode IN ('auto', 'forced_on', 'forced_off')),
    active INTEGER NOT NULL DEFAULT 0 CHECK (active IN (0, 1)),
    entered_at INTEGER,
    last_signal_at INTEGER,
    last_reason TEXT NOT NULL DEFAULT '',
    transition_seq INTEGER NOT NULL DEFAULT 0 CHECK (transition_seq >= 0),
    updated_at INTEGER NOT NULL
);

CREATE TABLE raid_signals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    signal_key TEXT NOT NULL UNIQUE,
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN (
        'join', 'verification_failed', 'coordinated_repeat', 'punished_recent'
    )),
    target_id INTEGER,
    content_hash TEXT,
    created_at INTEGER NOT NULL
);

CREATE INDEX raid_signals_chat_kind_time_idx
    ON raid_signals (chat_id, kind, created_at);

INSERT OR IGNORE INTO raid_states (chat_id, mode, active, updated_at)
SELECT chat_id, 'auto', 0, updated_at FROM managed_chats
;
