package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const moderationTestChatID int64 = -1004001

func newModerationStore(t *testing.T) (*Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "moderation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.db.ExecContext(context.Background(), `PRAGMA wal_checkpoint(TRUNCATE)`)
		_ = database.Close()
	})
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if err := database.SaveManagedChatSetup(ctx, ManagedChat{
		ChatID: moderationTestChatID, Title: "Source", Enabled: true, Language: "en",
		ManagementTopicID: 77, VerificationTimeoutSeconds: 180, ReentryTrustSeconds: 2592000,
	}, now); err != nil {
		t.Fatal(err)
	}
	return database, now
}

func moderationInput(updateID int64, messageID int, userID int64, now time.Time) ModerationMessage {
	return ModerationMessage{
		UpdateID: updateID, ChatID: moderationTestChatID, MessageID: messageID,
		TargetKind: ModerationTargetUser, TargetID: userID,
		ContentHash: fmt.Sprintf("%064x", updateID*1000+int64(messageID)),
		MessageAt:   now, VersionAt: now, Now: now,
	}
}

func hasRule(rules []ModerationRuleScore, id string, score int) bool {
	for _, rule := range rules {
		if rule.RuleID == id && rule.Score == score {
			return true
		}
	}
	return false
}

func TestRecentMemberWindowAndEstablishedExternalLink(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	if err := database.UpsertMemberState(ctx, moderationTestChatID, 101, now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}
	input := moderationInput(1, 1, 101, now)
	input.HasExternalLink = true
	result, err := database.AssessModerationMessage(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Recent || result.MessageScore != 1 || result.Threshold != 0 || !hasRule(result.Rules, RuleRecentExternalLink, 1) {
		t.Fatalf("recent result = %+v", result)
	}
	var normalCount int
	if err := database.db.QueryRowContext(ctx, `SELECT normal_message_count FROM member_states WHERE chat_id = ? AND user_id = 101`, moderationTestChatID).Scan(&normalCount); err != nil {
		t.Fatal(err)
	}
	if normalCount != 1 {
		t.Fatalf("normal message count = %d", normalCount)
	}

	established := moderationInput(2, 2, 202, now.Add(time.Second))
	established.HasExternalLink = true
	result, err = database.AssessModerationMessage(ctx, established)
	if err != nil {
		t.Fatal(err)
	}
	if result.Recent || result.MessageScore != 0 || result.ScheduledAction != "" {
		t.Fatalf("established result = %+v", result)
	}

	if err := database.UpsertMemberState(ctx, moderationTestChatID, 303, now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		message := moderationInput(int64(10+index), 10+index, 303, now.Add(time.Duration(index)*20*time.Second))
		if _, err := database.AssessModerationMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	afterThree := moderationInput(20, 20, 303, now.Add(time.Minute))
	afterThree.HasTelegramLink = true
	result, err = database.AssessModerationMessage(ctx, afterThree)
	if err != nil {
		t.Fatal(err)
	}
	if result.Recent || result.MessageScore != 0 {
		t.Fatalf("post-window result = %+v", result)
	}
	var recentUntil int64
	if err := database.db.QueryRowContext(ctx, `SELECT recent_until FROM member_states WHERE chat_id = ? AND user_id = 303`, moderationTestChatID).Scan(&recentUntil); err != nil {
		t.Fatal(err)
	}
	if recentUntil != now.Add(-time.Minute).Add(24*time.Hour).Unix() {
		t.Fatalf("recent_until changed to %d", recentUntil)
	}
}

func TestRateMentionRepeatAndCoordinatedRules(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)

	var last ModerationAssessment
	for index := 0; index < 6; index++ {
		input := moderationInput(int64(index+1), index+1, 401, now.Add(time.Duration(index)*time.Second))
		result, err := database.AssessModerationMessage(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		last = result
	}
	if !hasRule(last.Rules, RuleMessageFlood, 3) {
		t.Fatalf("message flood result = %+v", last)
	}

	mention := moderationInput(20, 20, 402, now.Add(time.Minute))
	mention.MentionCount = 10
	result, err := database.AssessModerationMessage(ctx, mention)
	if err != nil || !hasRule(result.Rules, RuleMentionFlood, 3) {
		t.Fatalf("mention result = %+v, err = %v", result, err)
	}

	repeatA := moderationInput(30, 30, 403, now.Add(2*time.Minute))
	repeatA.ContentHash = strings.Repeat("a", 64)
	repeatB := moderationInput(31, 31, 403, now.Add(2*time.Minute+time.Second))
	repeatB.ContentHash = repeatA.ContentHash
	if _, err := database.AssessModerationMessage(ctx, repeatA); err != nil {
		t.Fatal(err)
	}
	result, err = database.AssessModerationMessage(ctx, repeatB)
	if err != nil || !hasRule(result.Rules, RuleSelfRepeat, 3) {
		t.Fatalf("self repeat result = %+v, err = %v", result, err)
	}

	for index, userID := range []int64{501, 502, 503} {
		if err := database.UpsertMemberState(ctx, moderationTestChatID, userID, now, now); err != nil {
			t.Fatal(err)
		}
		input := moderationInput(int64(40+index), 40+index, userID, now.Add(3*time.Minute+time.Duration(index)*time.Second))
		input.ContentHash = strings.Repeat("b", 64)
		result, err = database.AssessModerationMessage(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !hasRule(result.Rules, RuleCoordinatedRepeat, 5) {
		t.Fatalf("coordinated repeat result = %+v", result)
	}
	status, err := database.GetModerationStatus(ctx, moderationTestChatID, now.Add(3*time.Minute+2*time.Second))
	if err != nil || !status.Active || status.LastReason != "coordinated_repeat" || status.CoordinatedLastFiveMin != 1 {
		t.Fatalf("coordinated Raid status = %+v, err=%v", status, err)
	}

	if err := database.UpsertMemberState(ctx, moderationTestChatID, 601, now, now); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		input := moderationInput(int64(50+index), 50+index, 601, now.Add(4*time.Minute+time.Duration(index)*time.Second))
		input.HasMedia = true
		result, err = database.AssessModerationMessage(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !hasRule(result.Rules, RuleMediaFlood, 2) && !hasRule(result.Rules, RuleMediaFlood, 3) {
		t.Fatalf("media flood result = %+v", result)
	}
}

func TestRollingThresholdsScheduleOnlyHighestNewAction(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	if err := database.UpsertMemberState(ctx, moderationTestChatID, 701, now, now); err != nil {
		t.Fatal(err)
	}
	features := []func(*ModerationMessage){
		func(value *ModerationMessage) { value.HasTelegramLink = true },
		func(value *ModerationMessage) { value.HasObfuscation = true },
		func(value *ModerationMessage) { value.HasObfuscation = true },
		func(value *ModerationMessage) { value.HasExternalLink = true },
		func(value *ModerationMessage) { value.HasObfuscation = true },
		func(value *ModerationMessage) { value.HasObfuscation = true },
	}
	wantThresholds := []int{3, 5, 5, 8, 8, 12}
	for index, apply := range features {
		at := now.Add(time.Duration(index) * 20 * time.Second)
		input := moderationInput(int64(index+1), index+1, 701, at)
		apply(&input)
		result, err := database.AssessModerationMessage(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Threshold != wantThresholds[index] {
			t.Fatalf("message %d threshold = %d, want %d; result=%+v", index, result.Threshold, wantThresholds[index], result)
		}
	}
	actions, err := database.ClaimModerationActions(ctx, now.Add(-time.Minute), now.Add(3*time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	var enforcement []ModerationAction
	for _, action := range actions {
		if action.ActionKind != ModerationRaidNotice && action.ActionKind != ModerationLog {
			enforcement = append(enforcement, action)
		}
	}
	if len(enforcement) != 4 {
		t.Fatalf("enforcement actions = %+v", enforcement)
	}
	if enforcement[0].ActionKind != ModerationDeleteWarn || enforcement[1].ActionKind != ModerationDeleteMute ||
		enforcement[2].ActionKind != ModerationDeleteMute || enforcement[3].ActionKind != ModerationDeleteBan {
		t.Fatalf("action ladder = %+v", enforcement)
	}
	if enforcement[1].UntilAt == nil || !enforcement[1].UntilAt.Equal(now.Add(20*time.Second+10*time.Minute)) {
		t.Fatalf("10 minute mute = %+v", enforcement[1].UntilAt)
	}
	if enforcement[2].UntilAt == nil || !enforcement[2].UntilAt.Equal(now.Add(60*time.Second+24*time.Hour)) {
		t.Fatalf("24 hour mute = %+v", enforcement[2].UntilAt)
	}
}

func TestRollingScoreExcludesViolationsOlderThanTwentyFourHours(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	old := now.Add(-25 * time.Hour)
	if err := database.UpsertMemberState(ctx, moderationTestChatID, 702, old, old); err != nil {
		t.Fatal(err)
	}
	first := moderationInput(1, 1, 702, old)
	first.HasTelegramLink = true
	result, err := database.AssessModerationMessage(ctx, first)
	if err != nil || result.RollingScore != 3 {
		t.Fatalf("old score = %+v, err=%v", result, err)
	}
	current := moderationInput(2, 2, 702, now)
	current.HasObfuscation = true
	result, err = database.AssessModerationMessage(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if result.RollingScore != 2 || result.Threshold != 0 || result.ScheduledAction != "" {
		t.Fatalf("rolling result = %+v", result)
	}
}

func TestDuplicateConcurrentAndEditedMessageReplaceCurrentScore(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	if err := database.UpsertMemberState(ctx, moderationTestChatID, 801, now, now); err != nil {
		t.Fatal(err)
	}
	input := moderationInput(1, 88, 801, now)
	input.HasTelegramLink = true
	results := make(chan ModerationAssessment, 2)
	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			result, err := database.AssessModerationMessage(ctx, input)
			results <- result
			errorsChannel <- err
		}()
	}
	duplicates := 0
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
		if (<-results).Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("duplicate outcomes = %d", duplicates)
	}
	var violations, actions int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM violations WHERE chat_id = ? AND message_id = 88`, moderationTestChatID).Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM moderation_actions WHERE chat_id = ? AND message_id = 88`, moderationTestChatID).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if violations != 1 || actions != 1 {
		t.Fatalf("violations=%d actions=%d", violations, actions)
	}

	edited := moderationInput(2, 88, 801, now.Add(time.Second))
	edited.ContentHash = strings.Repeat("c", 64)
	result, err := database.AssessModerationMessage(ctx, edited)
	if err != nil {
		t.Fatal(err)
	}
	if result.Duplicate || result.RollingScore != 0 || len(result.Rules) != 0 {
		t.Fatalf("edited result = %+v", result)
	}
	replayed, err := database.AssessModerationMessage(ctx, edited)
	if err != nil || !replayed.Duplicate {
		t.Fatalf("edited replay = %+v, err=%v", replayed, err)
	}
}

func TestBlocklistWinsConflictAndAllowlistSkipsModeration(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	changed, err := database.UpdateListEntry(ctx, "one", moderationTestChatID, 901, "block", true, 1, now)
	if err != nil || !changed {
		t.Fatalf("add block = %t, %v", changed, err)
	}
	input := moderationInput(1, 1, 901, now.Add(time.Second))
	result, err := database.AssessModerationMessage(ctx, input)
	if err != nil || !result.Blocked || result.ScheduledAction != ModerationDeleteBan {
		t.Fatalf("blocked result = %+v, err=%v", result, err)
	}
	changed, err = database.UpdateListEntry(ctx, "two", moderationTestChatID, 901, "allow", true, 1, now.Add(2*time.Second))
	if err != nil || !changed {
		t.Fatalf("replace with allow = %t, %v", changed, err)
	}
	allowed := moderationInput(2, 2, 901, now.Add(3*time.Second))
	result, err = database.AssessModerationMessage(ctx, allowed)
	if err != nil || !result.Ignored {
		t.Fatalf("allowed result = %+v, err=%v", result, err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO list_entries(chat_id, user_id, kind, created_at) VALUES (?, 901, 'block', ?)`, moderationTestChatID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	conflict := moderationInput(3, 3, 901, now.Add(4*time.Second))
	result, err = database.AssessModerationMessage(ctx, conflict)
	if err != nil || !result.Blocked {
		t.Fatalf("conflict result = %+v, err=%v", result, err)
	}
}

func TestSenderChatNeverSchedulesAutomaticMuteOrBan(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	for index := 0; index < 4; index++ {
		input := moderationInput(int64(index+1), index+1, 1, now.Add(time.Duration(index)*20*time.Second))
		input.TargetKind = ModerationTargetSenderChat
		input.TargetID = -20099
		input.MentionCount = 10
		result, err := database.AssessModerationMessage(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if result.ScheduledAction != ModerationDeleteWarn {
			t.Fatalf("sender action %d = %+v", index, result)
		}
	}
	var unsafe int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM moderation_actions
		WHERE target_kind = 'sender_chat' AND action_kind IN ('delete_mute', 'delete_ban', 'mute', 'ban')
	`).Scan(&unsafe); err != nil {
		t.Fatal(err)
	}
	if unsafe != 0 {
		t.Fatalf("unsafe sender_chat actions = %d", unsafe)
	}
}

func TestModerationActionClaimRetryCompletionAndRecovery(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	created, err := database.ScheduleManualModeration(ctx, ManualModerationRequest{
		IdempotencyKey: "manual-one", ChatID: moderationTestChatID, CommandMessageID: 1,
		TargetUserID: 1001, ActorUserID: 1, ActionKind: ModerationWarn, Reason: "manual_warn",
	}, now)
	if err != nil || !created {
		t.Fatalf("schedule = %t, %v", created, err)
	}
	claims := make(chan []ModerationAction, 2)
	errCh := make(chan error, 2)
	for range 2 {
		go func() {
			actions, err := database.ClaimModerationActions(ctx, now.Add(-time.Minute), now, 10)
			claims <- actions
			errCh <- err
		}()
	}
	total := 0
	var claimed ModerationAction
	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		actions := <-claims
		total += len(actions)
		if len(actions) == 1 {
			claimed = actions[0]
		}
	}
	if total != 1 {
		t.Fatalf("claimed action count = %d", total)
	}
	if err := database.FailModerationAction(ctx, claimed.ID, now); err != nil {
		t.Fatal(err)
	}
	actions, err := database.ClaimModerationActions(ctx, now.Add(-time.Second), now.Add(time.Second), 10)
	if err != nil || len(actions) != 0 {
		t.Fatalf("early retry actions=%+v err=%v", actions, err)
	}
	actions, err = database.ClaimModerationActions(ctx, now, now.Add(20*time.Second), 10)
	if err != nil || len(actions) != 1 {
		t.Fatalf("retry actions=%+v err=%v", actions, err)
	}
	if err := database.CompleteModerationAction(ctx, actions[0].ID, "completed", now.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteModerationAction(ctx, actions[0].ID, "completed", now.Add(21*time.Second)); err != nil {
		t.Fatalf("repeat completion: %v", err)
	}
	logs, err := database.ClaimModerationActions(ctx, now, now.Add(21*time.Second), 10)
	if err != nil || len(logs) != 1 || logs[0].ActionKind != ModerationLog {
		t.Fatalf("recovery log actions=%+v err=%v", logs, err)
	}
}

func TestStaleUnreplayableModerationActionsAreNotReclaimed(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	until := now.Add(10 * time.Minute)
	created, err := database.ScheduleManualModeration(ctx, ManualModerationRequest{
		IdempotencyKey: "manual-stale-notifications", ChatID: moderationTestChatID, CommandMessageID: 2,
		TargetUserID: 1002, ActorUserID: 1, ActionKind: ModerationMute,
		UntilAt: &until, Reason: "manual_mute",
	}, now)
	if err != nil || !created {
		t.Fatalf("schedule = %t, %v", created, err)
	}
	actions, err := database.ClaimModerationActions(ctx, now.Add(-time.Minute), now, 10)
	if err != nil || len(actions) != 1 || actions[0].ActionKind != ModerationMute {
		t.Fatalf("enforcement claim = %+v, %v", actions, err)
	}
	if err := database.CompleteModerationAction(ctx, actions[0].ID, "completed", now); err != nil {
		t.Fatal(err)
	}

	children, err := database.ClaimModerationActions(ctx, now.Add(-time.Minute), now.Add(time.Second), 10)
	if err != nil || len(children) != 2 {
		t.Fatalf("notification claims = %+v, %v", children, err)
	}
	kinds := map[string]bool{}
	for _, child := range children {
		kinds[child.ActionKind] = true
	}
	if !kinds[ModerationNotify] || !kinds[ModerationLog] {
		t.Fatalf("notification kinds = %+v", kinds)
	}

	settledAt := now.Add(20 * time.Second)
	if err := database.SettleStaleUnreplayableActions(ctx, now.Add(5*time.Second), settledAt); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{":notify", ":log"} {
		action, err := database.GetModerationActionByKey(ctx, "manual-stale-notifications"+suffix)
		if err != nil || action.Status != ModerationCompleted || action.Result != "indeterminate_after_restart" {
			t.Fatalf("settled %s = %+v, %v", suffix, action, err)
		}
	}
	reclaimed, err := database.ClaimModerationActions(ctx, now.Add(25*time.Second), now.Add(40*time.Second), 10)
	if err != nil || len(reclaimed) != 0 {
		t.Fatalf("reclaimed stale notification actions = %+v, %v", reclaimed, err)
	}
	var audited int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM audit_events WHERE event_type = 'moderation_action_indeterminate'
	`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 2 {
		t.Fatalf("indeterminate audit count = %d", audited)
	}

	created, err = database.ScheduleManualModeration(ctx, ManualModerationRequest{
		IdempotencyKey: "manual-stale-warn", ChatID: moderationTestChatID, CommandMessageID: 3,
		TargetUserID: 1003, ActorUserID: 1, ActionKind: ModerationWarn, Reason: "manual_warn",
	}, settledAt)
	if err != nil || !created {
		t.Fatalf("schedule stale warn = %t, %v", created, err)
	}
	warns, err := database.ClaimModerationActions(ctx, now.Add(19*time.Second), now.Add(21*time.Second), 10)
	if err != nil || len(warns) != 1 || warns[0].ActionKind != ModerationWarn {
		t.Fatalf("warn claim = %+v, %v", warns, err)
	}
	if err := database.SettleStaleUnreplayableActions(ctx, now.Add(25*time.Second), now.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	warn, err := database.GetModerationActionByKey(ctx, "manual-stale-warn")
	if err != nil || warn.Status != ModerationCompleted || warn.Result != "indeterminate_after_restart" {
		t.Fatalf("settled warn = %+v, %v", warn, err)
	}
	warnLogs, err := database.ClaimModerationActions(ctx, now.Add(39*time.Second), now.Add(41*time.Second), 10)
	if err != nil || len(warnLogs) != 1 || warnLogs[0].ActionKind != ModerationLog ||
		warnLogs[0].Reason != "warn:manual_warn:indeterminate_after_restart" {
		t.Fatalf("warn recovery log = %+v, %v", warnLogs, err)
	}
}

func TestRaidJoinTriggerQuietExitAndNoticeIdempotence(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	for index := 0; index < 12; index++ {
		_, created, err := database.CreateJoinCase(ctx, JoinCase{
			SourceKey: fmt.Sprintf("raid-join-%d", index), UpdateID: int64(index + 1),
			Path: JoinPathRequest, ChatID: moderationTestChatID, UserID: int64(2000 + index),
			UserChatID: int64(2000 + index), SourceRef: fmt.Sprintf("%d", index+1),
		}, now.Add(time.Duration(index)*time.Second))
		if err != nil || !created {
			t.Fatalf("create join %d = %t, %v", index, created, err)
		}
	}
	if err := database.RefreshRaidStates(ctx, now.Add(12*time.Second)); err != nil {
		t.Fatal(err)
	}
	status, err := database.GetModerationStatus(ctx, moderationTestChatID, now.Add(12*time.Second))
	if err != nil || !status.Active || status.LastReason != "join_volume" || status.JoinsLastMinute != 12 {
		t.Fatalf("Raid status = %+v, err=%v", status, err)
	}
	if err := database.RefreshRaidStates(ctx, now.Add(13*time.Second)); err != nil {
		t.Fatal(err)
	}
	var notices int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM moderation_actions WHERE action_kind = 'raid_notice'`).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 1 {
		t.Fatalf("entry notices = %d", notices)
	}
	if err := database.RefreshRaidStates(ctx, now.Add(16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err = database.GetModerationStatus(ctx, moderationTestChatID, now.Add(16*time.Minute))
	if err != nil || status.Active || status.LastReason != "quiet_exit" {
		t.Fatalf("quiet exit status = %+v, err=%v", status, err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM moderation_actions WHERE action_kind = 'raid_notice'`).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 2 {
		t.Fatalf("transition notices = %d", notices)
	}
}

func TestRaidSignalsManualModesAndVerificationStateIsolation(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	state, changed, err := database.SetRaidMode(ctx, moderationTestChatID, RaidModeForcedOff, 1, now)
	if err != nil || !changed || state.Active {
		t.Fatalf("forced off = %+v, %t, %v", state, changed, err)
	}
	for index := 0; index < raidFailureThreshold; index++ {
		if _, err := database.db.ExecContext(ctx, `
			INSERT INTO raid_signals(signal_key, chat_id, kind, target_id, created_at)
			VALUES (?, ?, 'verification_failed', ?, ?)
		`, fmt.Sprintf("failure-%d", index), moderationTestChatID, 3000+index, now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.RefreshRaidStates(ctx, now); err != nil {
		t.Fatal(err)
	}
	status, err := database.GetModerationStatus(ctx, moderationTestChatID, now)
	if err != nil || status.Active || status.FailuresLastFiveMin != raidFailureThreshold {
		t.Fatalf("forced-off signal status = %+v, err=%v", status, err)
	}
	state, changed, err = database.SetRaidMode(ctx, moderationTestChatID, RaidModeAuto, 1, now.Add(time.Second))
	if err != nil || !changed || !state.Active || state.LastReason != "verification_failures" {
		t.Fatalf("auto trigger = %+v, %t, %v", state, changed, err)
	}
	state, changed, err = database.SetRaidMode(ctx, moderationTestChatID, RaidModeForcedOn, 1, now.Add(2*time.Second))
	if err != nil || !changed || !state.Active {
		t.Fatalf("forced on = %+v, %t, %v", state, changed, err)
	}
	if err := database.RefreshRaidStates(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	status, err = database.GetModerationStatus(ctx, moderationTestChatID, now.Add(time.Hour))
	if err != nil || !status.Active || status.Mode != RaidModeForcedOn {
		t.Fatalf("forced-on persistence = %+v, err=%v", status, err)
	}

	session := VerificationSession{
		ID: "raid-verification-session", ChatID: moderationTestChatID, UserID: 4040,
		SourceKind: "test", SourceRef: "source", Language: "en",
		Status: VerificationChallengeIssued, Generation: 1, ChallengeSecret: []byte("0123456789abcdef"),
		ExpiresAt: now.Add(180 * time.Second), CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateVerificationSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	before, err := database.GetVerificationSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.SetRaidMode(ctx, moderationTestChatID, RaidModeForcedOff, 1, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := database.GetVerificationSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Attempts != after.Attempts || !before.ExpiresAt.Equal(after.ExpiresAt) || before.Status != after.Status {
		t.Fatalf("Raid changed verification: before=%+v after=%+v", before, after)
	}
}

func TestRaidWeightAloneCannotCausePermanentBan(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	if _, _, err := database.SetRaidMode(ctx, moderationTestChatID, RaidModeForcedOn, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertMemberState(ctx, moderationTestChatID, 6060, now, now); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 6; index++ {
		at := now.Add(time.Duration(index+1) * 20 * time.Second)
		input := moderationInput(int64(index+1), index+1, 6060, at)
		if index < 5 {
			input.HasObfuscation = true
		} else {
			input.HasExternalLink = true
		}
		if _, err := database.AssessModerationMessage(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	var bans int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM moderation_actions
		WHERE target_id = 6060 AND action_kind = 'delete_ban'
	`).Scan(&bans); err != nil {
		t.Fatal(err)
	}
	if bans != 0 {
		t.Fatalf("Raid-only weight scheduled %d permanent bans", bans)
	}
	last := moderationInput(7, 7, 6060, now.Add(140*time.Second))
	last.HasExternalLink = true
	result, err := database.AssessModerationMessage(ctx, last)
	if err != nil {
		t.Fatal(err)
	}
	if result.Threshold != 12 || result.ScheduledAction != ModerationDeleteBan {
		t.Fatalf("base score 12 did not reach normal ban threshold: %+v", result)
	}
}

func TestRaidVerificationFailureAndPunishedRecentSignals(t *testing.T) {
	t.Run("verification failures", func(t *testing.T) {
		ctx := context.Background()
		database, now := newModerationStore(t)
		for index := 0; index < raidFailureThreshold; index++ {
			item, _, err := database.CreateJoinCase(ctx, JoinCase{
				SourceKey: fmt.Sprintf("failed-case-%d", index), UpdateID: int64(index + 1),
				Path: JoinPathRequest, ChatID: moderationTestChatID, UserID: int64(6000 + index),
				UserChatID: int64(6000 + index), SourceRef: fmt.Sprintf("failure-%d", index),
			}, now.Add(time.Duration(index)*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if err := database.EnterReview(ctx, item.ID, "verification_failed", false, now.Add(time.Duration(index)*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		if err := database.RefreshRaidStates(ctx, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		status, err := database.GetModerationStatus(ctx, moderationTestChatID, now.Add(time.Minute))
		if err != nil || !status.Active || status.LastReason != "verification_failures" || status.FailuresLastFiveMin != raidFailureThreshold {
			t.Fatalf("failure Raid status = %+v, err=%v", status, err)
		}
	})

	t.Run("punished recent members", func(t *testing.T) {
		ctx := context.Background()
		database, now := newModerationStore(t)
		var result ModerationAssessment
		for index := 0; index < raidPunishedThreshold; index++ {
			userID := int64(7000 + index)
			if err := database.UpsertMemberState(ctx, moderationTestChatID, userID, now, now); err != nil {
				t.Fatal(err)
			}
			input := moderationInput(int64(index+1), index+1, userID, now.Add(time.Duration(index)*time.Second))
			input.HasTelegramLink = true
			var err error
			result, err = database.AssessModerationMessage(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			actions, err := database.ClaimModerationActions(ctx, now.Add(-time.Minute), now.Add(time.Duration(index)*time.Second), 20)
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range actions {
				if err := database.CompleteModerationAction(ctx, action.ID, "completed", now.Add(time.Duration(index)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if result.RaidActive {
			t.Fatalf("Raid activated before the punishment completed: %+v", result)
		}
		if err := database.RefreshRaidStates(ctx, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}
		status, err := database.GetModerationStatus(ctx, moderationTestChatID, now.Add(3*time.Second))
		if err != nil || !status.Active || status.LastReason != "punished_recent" || status.PunishedLastFiveMin != raidPunishedThreshold {
			t.Fatalf("punishment Raid status = %+v, err=%v", status, err)
		}
	})
}

func TestModerationStorageContainsOnlyDigestAndDomains(t *testing.T) {
	ctx := context.Background()
	database, now := newModerationStore(t)
	if err := database.UpsertMemberState(ctx, moderationTestChatID, 5050, now, now); err != nil {
		t.Fatal(err)
	}
	input := moderationInput(1, 1, 5050, now)
	input.ContentHash = strings.Repeat("d", 64)
	input.Domains = []string{"example.test"}
	input.HasExternalLink = true
	if _, err := database.AssessModerationMessage(ctx, input); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := database.db.QueryRowContext(ctx, `
		SELECT COALESCE(group_concat(value, ''), '') FROM (
			SELECT content_hash || domains_json AS value FROM message_observations
			UNION ALL SELECT COALESCE(content_hash, '') || domains_json FROM violations
			UNION ALL SELECT reason || rule_ids_json || domains_json FROM moderation_actions
		)
	`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"buy now secret phrase", "https://example.test/path?invite=secret", "invite=secret"} {
		if strings.Contains(stored, secret) {
			t.Fatalf("stored moderation data leaked %q: %s", secret, stored)
		}
	}
	if !strings.Contains(stored, strings.Repeat("d", 64)) || !strings.Contains(stored, "example.test") {
		t.Fatalf("expected digest/domain missing: %s", stored)
	}
}

func TestModerationErrorsAreStable(t *testing.T) {
	database, now := newModerationStore(t)
	_, err := database.AssessModerationMessage(context.Background(), ModerationMessage{Now: now})
	if !errors.Is(err, ErrModerationState) {
		t.Fatalf("error = %v", err)
	}
}
