package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tadmin/assets/migrations"
)

func TestMigrationsAreIdempotentAndConfigureSQLite(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "tadmin.db")
	s, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	var migrationCount int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if migrationCount != 6 {
		t.Errorf("migration count = %d, want 6", migrationCount)
	}

	var challengeColumnCount int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_table_info('verification_sessions')
		WHERE name IN ('source_kind', 'source_ref', 'language', 'generation', 'challenge_secret', 'consumed_at')
	`).Scan(&challengeColumnCount); err != nil {
		t.Fatalf("query verification challenge columns: %v", err)
	}
	if challengeColumnCount != 6 {
		t.Errorf("verification challenge column count = %d, want 6", challengeColumnCount)
	}

	requiredTables := []string{
		"managed_chats",
		"verification_sessions",
		"trusted_users",
		"member_states",
		"list_entries",
		"violations",
		"review_items",
		"audit_events",
		"join_cases",
		"message_observations",
		"moderation_subjects",
		"moderation_actions",
		"raid_states",
		"raid_signals",
		"invite_links",
	}
	for _, table := range requiredTables {
		var exists bool
		if err := s.db.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", table,
		).Scan(&exists); err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("required table %s does not exist", table)
		}
	}

	var journalMode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busyTimeout int
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != busyTimeoutMilliseconds {
		t.Errorf("busy_timeout = %d, want %d", busyTimeout, busyTimeoutMilliseconds)
	}
}

func TestMigration0003UpgradesStage2Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stage2.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_initial.sql", "0002_verification_challenges.sql"} {
		contents, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(contents)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(name, applied_at) VALUES (?, unixepoch())`, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO managed_chats(chat_id, title, enabled, language, created_at, updated_at)
		VALUES (-1001, 'Existing', 1, 'en', unixepoch(), unixepoch())
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if err := upgraded.Migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	chat, err := upgraded.GetManagedChat(ctx, -1001)
	if err != nil {
		t.Fatal(err)
	}
	if chat.VerificationTimeoutSeconds != 180 || chat.ReentryTrustSeconds != 0 {
		t.Fatalf("stage 3 defaults = %+v", chat)
	}
	var count int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("migration count = %d", count)
	}
}

func TestMigration0004UpgradesStage3Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stage3.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_initial.sql", "0002_verification_challenges.sql", "0003_join_workflows.sql"} {
		contents, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(contents)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(name, applied_at) VALUES (?, unixepoch())`, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO managed_chats(chat_id, title, enabled, language, created_at, updated_at)
		VALUES (-1001, 'Existing', 1, 'en', unixepoch(), unixepoch());
		INSERT INTO violations(chat_id, user_id, rule_id, score, content_hash, created_at)
		VALUES (-1001, 42, 'legacy', 1, 'legacy-digest', unixepoch());
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if err := upgraded.Migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	chat, err := upgraded.GetManagedChat(ctx, -1001)
	if err != nil {
		t.Fatal(err)
	}
	if chat.ModerationEnabled {
		t.Fatalf("moderation unexpectedly enabled: %+v", chat)
	}
	var migrationCount, legacyCount, raidStateCount int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM violations WHERE rule_id = 'legacy'`).Scan(&legacyCount); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM raid_states WHERE chat_id = -1001 AND mode = 'auto'`).Scan(&raidStateCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 6 || legacyCount != 1 || raidStateCount != 1 {
		t.Fatalf("migration=%d legacy=%d raid=%d", migrationCount, legacyCount, raidStateCount)
	}
}

func TestMigration0005UpgradesStage4Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stage4.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001_initial.sql", "0002_verification_challenges.sql", "0003_join_workflows.sql", "0004_moderation_and_raid.sql"} {
		contents, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(contents)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(name, applied_at) VALUES (?, unixepoch())`, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO managed_chats(chat_id, title, enabled, language, created_at, updated_at)
		VALUES (-1001, 'Existing', 1, 'en', unixepoch(), unixepoch());
		INSERT INTO raid_states(chat_id, mode, active, updated_at)
		VALUES (-1001, 'auto', 0, unixepoch())
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if err := upgraded.Migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var migrationCount, inviteTable, chatCount int
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'invite_links'`).Scan(&inviteTable); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM managed_chats WHERE chat_id = -1001`).Scan(&chatCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 6 || inviteTable != 1 || chatCount != 1 {
		t.Fatalf("migration=%d invite_table=%d chat=%d", migrationCount, inviteTable, chatCount)
	}
}

func TestJoinActionClaimIsAtomic(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "actions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.SaveManagedChatSetup(ctx, ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 7,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	item, _, err := database.CreateJoinCase(ctx, JoinCase{
		SourceKey: "atomic-source", UpdateID: 1, Path: JoinPathRequest,
		ChatID: -1001, UserID: 42, UserChatID: 42, SourceRef: "1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ScheduleCaseAction(ctx, item.ID, "approve_request", now); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := database.ClaimCaseAction(ctx, item.ID, now.Add(-time.Minute), now)
			results <- err
		}()
	}
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrActionUnavailable) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims = %d", successes)
	}
}

func TestJoinAuditMetadataDoesNotContainVerificationSecrets(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Second)
	if err := database.SaveManagedChatSetup(ctx, ManagedChat{
		ChatID: -1001, Title: "Source", Enabled: true, Language: "en", ManagementTopicID: 7,
		VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	item, _, err := database.CreateJoinCase(ctx, JoinCase{
		SourceKey: "audit-source", UpdateID: 1, Path: JoinPathRequest,
		ChatID: -1001, UserID: 42, UserChatID: 42,
		SourceRef:    "123456:bot-token complete-init-data challenge-answer",
		InviteSource: "named",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnterReview(ctx, item.ID, "verification_unreachable", false, now); err != nil {
		t.Fatal(err)
	}
	review, _, err := database.EnsureReview(ctx, item.ID, "opaque-review", now.Add(30*time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ClaimReviewDecision(ctx, review.PublicID, ReviewDecisionOnce, 1, now); err != nil {
		t.Fatal(err)
	}
	claimed, err := database.ClaimCaseAction(ctx, item.ID, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteCaseAction(ctx, item.ID, claimed.ActionKind, JoinResolved, 0, true, now); err != nil {
		t.Fatal(err)
	}
	var metadata string
	if err := database.db.QueryRowContext(ctx, `SELECT COALESCE(group_concat(metadata_json, ''), '') FROM audit_events`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"123456:bot-token", "complete-init-data", "challenge-answer"} {
		if strings.Contains(metadata, marker) {
			t.Fatalf("audit metadata contains %q: %s", marker, metadata)
		}
	}
}

func TestCountManagedChatsCountsOnlyEnabledChats(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO managed_chats(chat_id, title, enabled, created_at, updated_at)
		VALUES (1, 'enabled', 1, unixepoch(), unixepoch()),
		       (2, 'disabled', 0, unixepoch(), unixepoch())
	`)
	if err != nil {
		t.Fatalf("insert managed chats: %v", err)
	}

	count, err := s.CountManagedChats(ctx)
	if err != nil {
		t.Fatalf("CountManagedChats() error = %v", err)
	}
	if count != 1 {
		t.Errorf("CountManagedChats() = %d, want 1", count)
	}
}
