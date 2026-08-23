package store

import (
	"context"
	"fmt"
	"time"
)

const (
	verificationRetention = 24 * time.Hour
	recordRetention       = 30 * 24 * time.Hour
	maximumRetentionBatch = 500
)

type RetentionStats struct {
	VerificationSessions int64
	ReviewItems          int64
	JoinCases            int64
	Violations           int64
	AuditEvents          int64
	MessageObservations  int64
	ModerationActions    int64
	RaidSignals          int64
	TrustedUsers         int64
	MemberStates         int64
	ModerationSubjects   int64
	InviteLinks          int64
}

func (s RetentionStats) Total() int64 {
	return s.VerificationSessions + s.ReviewItems + s.JoinCases + s.Violations +
		s.AuditEvents + s.MessageObservations + s.ModerationActions + s.RaidSignals +
		s.TrustedUsers + s.MemberStates + s.ModerationSubjects + s.InviteLinks
}

type retentionStep struct {
	name      string
	statement string
	arguments []any
	add       func(*RetentionStats, int64)
}

func (s *Store) CleanupRetention(ctx context.Context, now time.Time, batchSize int) (RetentionStats, error) {
	if now.IsZero() || batchSize <= 0 || batchSize > maximumRetentionBatch {
		return RetentionStats{}, fmt.Errorf("invalid retention cleanup parameters")
	}
	now = now.UTC().Truncate(time.Second)
	verificationCutoff := now.Add(-verificationRetention).Unix()
	recordCutoff := now.Add(-recordRetention).Unix()
	steps := []retentionStep{
		{
			name: "completed reviews",
			statement: `DELETE FROM review_items WHERE rowid IN (
				SELECT rowid FROM review_items WHERE status = ? AND card_state = ? AND updated_at <= ? ORDER BY rowid LIMIT ?
			)`,
			arguments: []any{ReviewCompleted, ActionCompleted, recordCutoff},
			add:       func(stats *RetentionStats, count int64) { stats.ReviewItems += count },
		},
		{
			name: "terminal join cases",
			statement: `DELETE FROM join_cases WHERE rowid IN (
				SELECT current.rowid FROM join_cases AS current
				WHERE current.status IN (?, ?) AND current.action_state IN (?, ?) AND current.updated_at <= ?
				  AND NOT EXISTS (
					SELECT 1 FROM review_items AS review
					WHERE review.join_case_id = current.id
					  AND (review.status <> ? OR review.updated_at > ?)
				  )
				ORDER BY current.rowid LIMIT ?
			)`,
			arguments: []any{JoinResolved, JoinCancelled, ActionNone, ActionCompleted,
				recordCutoff, ReviewCompleted, recordCutoff},
			add: func(stats *RetentionStats, count int64) { stats.JoinCases += count },
		},
		{
			name: "terminal verification sessions",
			statement: `DELETE FROM verification_sessions WHERE rowid IN (
				SELECT current.rowid FROM verification_sessions AS current
				WHERE current.status IN (?, ?, ?, ?) AND current.updated_at <= ?
				  AND NOT EXISTS (
					SELECT 1 FROM join_cases AS joins
					WHERE joins.verification_session_id = current.id
					  AND joins.status IN (?, ?, ?)
				  )
				  AND NOT EXISTS (
					SELECT 1 FROM review_items AS review
					WHERE review.verification_session_id = current.id AND review.status <> ?
				  )
				ORDER BY current.rowid LIMIT ?
			)`,
			arguments: []any{VerificationPassed, VerificationAwaitingReview, VerificationExpired,
				VerificationCancelled, verificationCutoff, JoinInitializing, JoinVerifying,
				JoinReviewPending, ReviewCompleted},
			add: func(stats *RetentionStats, count int64) { stats.VerificationSessions += count },
		},
		{
			name:      "violations",
			statement: `DELETE FROM violations WHERE rowid IN (SELECT rowid FROM violations WHERE created_at <= ? ORDER BY rowid LIMIT ?)`,
			arguments: []any{recordCutoff},
			add:       func(stats *RetentionStats, count int64) { stats.Violations += count },
		},
		{
			name:      "message observations",
			statement: `DELETE FROM message_observations WHERE rowid IN (SELECT rowid FROM message_observations WHERE updated_at <= ? ORDER BY rowid LIMIT ?)`,
			arguments: []any{recordCutoff},
			add:       func(stats *RetentionStats, count int64) { stats.MessageObservations += count },
		},
		{
			name:      "completed moderation actions",
			statement: `DELETE FROM moderation_actions WHERE rowid IN (SELECT rowid FROM moderation_actions WHERE status = ? AND updated_at <= ? ORDER BY rowid LIMIT ?)`,
			arguments: []any{ModerationCompleted, recordCutoff},
			add:       func(stats *RetentionStats, count int64) { stats.ModerationActions += count },
		},
		{
			name:      "Raid signals",
			statement: `DELETE FROM raid_signals WHERE rowid IN (SELECT rowid FROM raid_signals WHERE created_at <= ? ORDER BY rowid LIMIT ?)`,
			arguments: []any{recordCutoff},
			add:       func(stats *RetentionStats, count int64) { stats.RaidSignals += count },
		},
		{
			name:      "expired trust",
			statement: `DELETE FROM trusted_users WHERE rowid IN (SELECT rowid FROM trusted_users WHERE trusted_until <= ? ORDER BY rowid LIMIT ?)`,
			arguments: []any{now.Unix()},
			add:       func(stats *RetentionStats, count int64) { stats.TrustedUsers += count },
		},
		{
			name:      "established member states",
			statement: `DELETE FROM member_states WHERE rowid IN (SELECT rowid FROM member_states WHERE recent_until <= ? OR normal_message_count >= 3 ORDER BY rowid LIMIT ?)`,
			arguments: []any{now.Unix()},
			add:       func(stats *RetentionStats, count int64) { stats.MemberStates += count },
		},
		{
			name: "inactive moderation subjects",
			statement: `DELETE FROM moderation_subjects WHERE rowid IN (
				SELECT subject.rowid FROM moderation_subjects AS subject
				WHERE subject.updated_at <= ?
				  AND NOT EXISTS (
					SELECT 1 FROM violations AS violation
					WHERE violation.chat_id = subject.chat_id
					  AND violation.target_kind = subject.target_kind
					  AND violation.target_id = subject.target_id
				  )
				  AND NOT EXISTS (
					SELECT 1 FROM moderation_actions AS action
					WHERE action.chat_id = subject.chat_id
					  AND action.target_kind = subject.target_kind
					  AND action.target_id = subject.target_id
					  AND action.status <> ?
				  )
				ORDER BY subject.rowid LIMIT ?
			)`,
			arguments: []any{recordCutoff, ModerationCompleted},
			add:       func(stats *RetentionStats, count int64) { stats.ModerationSubjects += count },
		},
		{
			name: "terminal invite links",
			statement: `DELETE FROM invite_links WHERE rowid IN (
				SELECT rowid FROM invite_links WHERE status IN (?, ?, ?) AND action_state = ? AND updated_at <= ? ORDER BY rowid LIMIT ?
			)`,
			arguments: []any{InviteRevoked, InviteFailed, InviteIndeterminate, InviteActionCompleted, recordCutoff},
			add:       func(stats *RetentionStats, count int64) { stats.InviteLinks += count },
		},
		{
			name:      "audit events",
			statement: `DELETE FROM audit_events WHERE rowid IN (SELECT rowid FROM audit_events WHERE created_at <= ? ORDER BY rowid LIMIT ?)`,
			arguments: []any{recordCutoff},
			add:       func(stats *RetentionStats, count int64) { stats.AuditEvents += count },
		},
	}

	var stats RetentionStats
	for _, step := range steps {
		for {
			count, err := s.runRetentionBatch(ctx, step.statement, append(step.arguments, batchSize)...)
			if err != nil {
				return stats, fmt.Errorf("clean %s: %w", step.name, err)
			}
			step.add(&stats, count)
			if count < int64(batchSize) {
				break
			}
		}
	}
	return stats, nil
}

func (s *Store) runRetentionBatch(ctx context.Context, statement string, arguments ...any) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
