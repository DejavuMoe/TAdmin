ALTER TABLE verification_sessions ADD COLUMN source_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE verification_sessions ADD COLUMN source_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE verification_sessions ADD COLUMN language TEXT NOT NULL DEFAULT 'zh-CN';
ALTER TABLE verification_sessions ADD COLUMN generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0);
ALTER TABLE verification_sessions ADD COLUMN challenge_secret BLOB;
ALTER TABLE verification_sessions ADD COLUMN consumed_at INTEGER;

CREATE INDEX verification_sessions_user_status_idx
    ON verification_sessions (chat_id, user_id, status);
