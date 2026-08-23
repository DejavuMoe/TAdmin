ALTER TABLE managed_chats ADD COLUMN verification_timeout_seconds INTEGER NOT NULL DEFAULT 180 CHECK (verification_timeout_seconds BETWEEN 30 AND 900);
ALTER TABLE managed_chats ADD COLUMN reentry_trust_seconds INTEGER NOT NULL DEFAULT 2592000 CHECK (reentry_trust_seconds BETWEEN 0 AND 31536000);

CREATE TABLE join_cases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_key TEXT NOT NULL UNIQUE,
    update_id INTEGER NOT NULL,
    path TEXT NOT NULL CHECK (path IN ('guard', 'join_request', 'direct')),
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL,
    user_chat_id INTEGER NOT NULL DEFAULT 0,
    source_ref TEXT NOT NULL,
    invite_source TEXT NOT NULL DEFAULT '',
    user_first_name TEXT NOT NULL DEFAULT '',
    user_last_name TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    verification_session_id TEXT UNIQUE REFERENCES verification_sessions(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('initializing', 'verifying', 'review_pending', 'resolved', 'cancelled')),
    reason TEXT NOT NULL DEFAULT '',
    action_kind TEXT NOT NULL DEFAULT '',
    action_state TEXT NOT NULL DEFAULT 'none' CHECK (action_state IN ('none', 'pending', 'processing', 'completed', 'failed')),
    action_attempts INTEGER NOT NULL DEFAULT 0 CHECK (action_attempts >= 0),
    joined_at INTEGER,
    resolved_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX join_cases_status_action_idx
    ON join_cases (status, action_state, updated_at);
CREATE INDEX join_cases_chat_user_idx
    ON join_cases (chat_id, user_id, updated_at);

ALTER TABLE review_items ADD COLUMN join_case_id INTEGER REFERENCES join_cases(id) ON DELETE CASCADE;
ALTER TABLE review_items ADD COLUMN public_id TEXT NOT NULL DEFAULT '';
ALTER TABLE review_items ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE review_items ADD COLUMN deadline_at INTEGER;
ALTER TABLE review_items ADD COLUMN management_topic_id INTEGER;
ALTER TABLE review_items ADD COLUMN management_message_id INTEGER;
ALTER TABLE review_items ADD COLUMN card_state TEXT NOT NULL DEFAULT 'pending' CHECK (card_state IN ('pending', 'processing', 'completed', 'failed'));
ALTER TABLE review_items ADD COLUMN decision TEXT NOT NULL DEFAULT '';
ALTER TABLE review_items ADD COLUMN actor_user_id INTEGER;
ALTER TABLE review_items ADD COLUMN decided_at INTEGER;

CREATE UNIQUE INDEX review_items_join_case_unique_idx
    ON review_items (join_case_id)
    WHERE join_case_id IS NOT NULL;
CREATE UNIQUE INDEX review_items_public_id_unique_idx
    ON review_items (public_id)
    WHERE public_id <> '';
CREATE INDEX review_items_deadline_idx
    ON review_items (status, deadline_at);
