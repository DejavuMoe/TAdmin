ALTER TABLE managed_chats ADD COLUMN management_chat_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE join_cases ADD COLUMN deadline_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE review_items ADD COLUMN management_chat_id INTEGER NOT NULL DEFAULT 0;
UPDATE managed_chats SET moderation_enabled = 0, reentry_trust_seconds = 0, management_topic_id = NULL;
UPDATE join_cases SET deadline_at = created_at + COALESCE((SELECT verification_timeout_seconds FROM managed_chats WHERE managed_chats.chat_id = join_cases.chat_id), 180);
CREATE TABLE bot_state (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
ALTER TABLE join_cases ADD COLUMN member_event_consumed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE join_cases ADD COLUMN prior_permissions TEXT NOT NULL DEFAULT '';
ALTER TABLE join_cases ADD COLUMN prior_until INTEGER NOT NULL DEFAULT 0;
