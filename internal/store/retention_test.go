package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupRetentionBoundariesBatchesAndProtectedState(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChatSetup(ctx, ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 77,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	old24 := now.Add(-24 * time.Hour).Unix()
	inside24 := old24 + 1
	old30 := now.Add(-30 * 24 * time.Hour).Unix()
	inside30 := old30 + 1
	created := now.Add(-40 * 24 * time.Hour).Unix()

	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO verification_sessions(id, chat_id, user_id, status, attempts, expires_at, created_at, updated_at)
		VALUES
		 ('v-old', -1001, 1, 'expired', 0, ?, ?, ?),
		 ('v-inside', -1001, 2, 'expired', 0, ?, ?, ?),
		 ('v-active', -1001, 3, 'challenge_issued', 0, ?, ?, ?),
		 ('v-referenced', -1001, 4, 'expired', 0, ?, ?, ?)
	`, old24, created, old24, inside24, created, inside24,
		now.Add(time.Hour).Unix(), created, old24, old24, created, old24); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO join_cases(
			id, source_key, update_id, path, chat_id, user_id, user_chat_id, source_ref,
			verification_session_id, status, action_state, resolved_at, created_at, updated_at
		) VALUES
		 (1, 'join-old', 1, 'direct', -1001, 11, 0, '1', NULL, 'resolved', 'completed', ?, ?, ?),
		 (2, 'join-pending', 2, 'direct', -1001, 12, 0, '2', NULL, 'review_pending', 'none', NULL, ?, ?),
		 (3, 'join-pending-review', 3, 'direct', -1001, 13, 0, '3', NULL, 'resolved', 'completed', ?, ?, ?),
		 (4, 'join-old-review', 4, 'direct', -1001, 14, 0, '4', NULL, 'resolved', 'completed', ?, ?, ?),
		 (5, 'join-reference', 5, 'direct', -1001, 4, 0, '5', 'v-referenced', 'verifying', 'none', NULL, ?, ?),
		 (6, 'join-failed-action', 6, 'direct', -1001, 17, 0, '6', NULL, 'resolved', 'failed', ?, ?, ?)
	`, old30, created, old30, created, old30, old30, created, old30,
		old30, created, old30, created, old30, old30, created, old30); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO review_items(id, chat_id, user_id, join_case_id, public_id, status, card_state, created_at, updated_at)
		VALUES
		 (1, -1001, 13, 3, 'review-pending', 'pending', 'pending', ?, ?),
		 (2, -1001, 14, 4, 'review-old-attached', 'completed', 'completed', ?, ?),
		 (3, -1001, 15, NULL, 'review-old', 'completed', 'completed', ?, ?),
		 (4, -1001, 16, NULL, 'review-inside', 'completed', 'completed', ?, ?)
	`, created, old30, created, old30, created, old30, created, inside30); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO violations(chat_id, user_id, rule_id, score, content_hash, created_at, target_kind, target_id)
		VALUES
		 (-1001, 21, 'old', 1, 'old-hash', ?, 'user', 21),
		 (-1001, 22, 'recent', 1, 'recent-hash', ?, 'user', 22),
		 (-1001, 31, 'subject-recent', 1, 'subject-hash', ?, 'user', 31),
		 (-1001, 35, 'subject-retained', 1, 'retained-subject-hash', ?, 'user', 35)
	`, old30, inside30, now.Add(-time.Hour).Unix(), inside30); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO audit_events(chat_id, event_type, metadata_json, created_at)
		VALUES (-1001, 'old', '{}', ?), (-1001, 'inside', '{}', ?)
	`, old30, inside30); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO message_observations(
			chat_id, message_id, update_id, target_kind, target_id, content_hash,
			message_at, version_at, updated_at
		) VALUES
		 (-1001, 1, 1, 'user', 21, 'old-observation', ?, ?, ?),
		 (-1001, 2, 2, 'user', 22, 'inside-observation', ?, ?, ?)
	`, old30, old30, old30, inside30, inside30, inside30); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO moderation_actions(
			idempotency_key, chat_id, target_kind, target_id, action_kind, status,
			created_at, updated_at
		) VALUES
		 ('old-completed', -1001, 'user', 40, 'warn', 'completed', ?, ?),
		 ('old-failed', -1001, 'user', 32, 'ban', 'failed', ?, ?),
		 ('old-pending', -1001, 'user', 33, 'ban', 'pending', ?, ?),
		 ('old-processing', -1001, 'user', 34, 'ban', 'processing', ?, ?)
	`, created, old30, created, old30, created, old30, created, old30); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO moderation_subjects(chat_id, target_kind, target_id, updated_at)
		VALUES
		 (-1001, 'user', 30, ?),
		 (-1001, 'user', 31, ?),
		 (-1001, 'user', 32, ?),
		 (-1001, 'user', 35, ?)
	`, old30, old30, old30, old30); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO raid_signals(signal_key, chat_id, kind, created_at)
		VALUES ('old-raid', -1001, 'join', ?), ('inside-raid', -1001, 'join', ?)
	`, old30, inside30); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO trusted_users(chat_id, user_id, trusted_until, created_at)
		VALUES (-1001, 50, ?, ?), (-1001, 51, ?, ?)
	`, now.Unix(), created, now.Add(time.Second).Unix(), created); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO member_states(chat_id, user_id, joined_at, recent_until, normal_message_count)
		VALUES
		 (-1001, 60, ?, ?, 0),
		 (-1001, 61, ?, ?, 3),
		 (-1001, 62, ?, ?, 2)
	`, created, now.Unix(), created, now.Add(time.Hour).Unix(),
		now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO invite_links(
			public_id, create_operation_key, chat_id, display_name, telegram_name,
			creator_user_id, status, action_kind, action_state, created_at, updated_at
		) VALUES
		 ('aaaaaaaaaaaaaaaa', 'invite-active', -1001, 'Active', 'TAdmin active', 1, 'active', 'create', 'completed', ?, ?),
		 ('bbbbbbbbbbbbbbbb', 'invite-revoking', -1001, 'Revoking', 'TAdmin revoking', 1, 'revocation_pending', 'revoke', 'failed', ?, ?),
		 ('cccccccccccccccc', 'invite-revoked', -1001, 'Revoked', 'TAdmin revoked', 1, 'revoked', 'revoke', 'completed', ?, ?),
		 ('dddddddddddddddd', 'invite-failed-new', -1001, 'Failed new', 'TAdmin failed', 1, 'failed', 'create', 'completed', ?, ?),
		 ('eeeeeeeeeeeeeeee', 'invite-unknown', -1001, 'Unknown', 'TAdmin unknown', 1, 'indeterminate', 'create', 'completed', ?, ?)
	`, created, old30, created, old30, created, old30, created, inside30, created, old30); err != nil {
		t.Fatal(err)
	}

	stats, err := database.CleanupRetention(ctx, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := RetentionStats{
		VerificationSessions: 1, ReviewItems: 2, JoinCases: 2, Violations: 1,
		AuditEvents: 1, MessageObservations: 1, ModerationActions: 1, RaidSignals: 1,
		TrustedUsers: 1, MemberStates: 2, ModerationSubjects: 1, InviteLinks: 2,
	}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	second, err := database.CleanupRetention(ctx, now, 1)
	if err != nil || second.Total() != 0 {
		t.Fatalf("repeat cleanup = %+v, %v", second, err)
	}

	assertIDs(t, database, `SELECT id FROM verification_sessions ORDER BY id`, []string{"v-active", "v-inside", "v-referenced"})
	assertIDs(t, database, `SELECT source_key FROM join_cases ORDER BY source_key`, []string{"join-failed-action", "join-pending", "join-pending-review", "join-reference"})
	assertIDs(t, database, `SELECT public_id FROM review_items ORDER BY public_id`, []string{"review-inside", "review-pending"})
	assertIDs(t, database, `SELECT public_id FROM invite_links ORDER BY public_id`, []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb", "dddddddddddddddd"})
	assertIDs(t, database, `SELECT idempotency_key FROM moderation_actions ORDER BY idempotency_key`, []string{"old-failed", "old-pending", "old-processing"})
	assertIDs(t, database, `SELECT CAST(user_id AS TEXT) FROM trusted_users ORDER BY user_id`, []string{"51"})
	assertIDs(t, database, `SELECT CAST(user_id AS TEXT) FROM member_states ORDER BY user_id`, []string{"62"})
	assertIDs(t, database, `SELECT CAST(target_id AS TEXT) FROM moderation_subjects ORDER BY target_id`, []string{"31", "32", "35"})
	var foreignKeys int
	if err := database.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, %v", foreignKeys, err)
	}
}

func TestCleanupRetentionRejectsOversizedBatch(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "retention-limit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.CleanupRetention(context.Background(), time.Now(), 501); err == nil {
		t.Fatal("CleanupRetention() accepted a batch larger than 500")
	}
}

func assertIDs(t *testing.T, database *Store, query string, want []string) {
	t.Helper()
	rows, err := database.db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if len(got) != len(want) {
		t.Fatalf("query %q returned %v, want %v", query, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("query %q returned %v, want %v", query, got, want)
		}
	}
}
