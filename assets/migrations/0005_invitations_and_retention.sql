CREATE TABLE invite_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    public_id TEXT NOT NULL UNIQUE,
    create_operation_key TEXT NOT NULL UNIQUE,
    revoke_operation_key TEXT UNIQUE,
    chat_id INTEGER NOT NULL REFERENCES managed_chats(chat_id) ON DELETE CASCADE,
    display_name TEXT NOT NULL,
    telegram_name TEXT NOT NULL,
    creator_user_id INTEGER NOT NULL,
    revoked_by_user_id INTEGER,
    status TEXT NOT NULL CHECK (status IN (
        'pending', 'active', 'revocation_pending', 'revoked', 'failed', 'indeterminate'
    )),
    encrypted_link BLOB NOT NULL DEFAULT X'',
    link_nonce BLOB NOT NULL DEFAULT X'',
    link_fingerprint BLOB NOT NULL DEFAULT X'',
    action_kind TEXT NOT NULL CHECK (action_kind IN ('create', 'revoke', 'none')),
    action_state TEXT NOT NULL CHECK (action_state IN ('pending', 'processing', 'completed', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_result TEXT NOT NULL DEFAULT '',
    revoked_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX invite_links_fingerprint_unique_idx
    ON invite_links (link_fingerprint)
    WHERE length(link_fingerprint) > 0;

CREATE INDEX invite_links_chat_status_idx
    ON invite_links (chat_id, status, updated_at);

CREATE INDEX invite_links_action_claim_idx
    ON invite_links (status, action_kind, action_state, updated_at);

CREATE INDEX verification_sessions_retention_idx
    ON verification_sessions (status, updated_at);

CREATE INDEX message_observations_retention_idx
    ON message_observations (updated_at);

CREATE INDEX trusted_users_retention_idx
    ON trusted_users (trusted_until);

CREATE INDEX member_states_retention_idx
    ON member_states (recent_until, normal_message_count);
