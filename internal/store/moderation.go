package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	ModerationTargetUser       = "user"
	ModerationTargetSenderChat = "sender_chat"
	ModerationTargetChat       = "chat"

	ModerationPending    = "pending"
	ModerationProcessing = "processing"
	ModerationCompleted  = "completed"
	ModerationFailed     = "failed"

	ModerationDeleteWarn = "delete_warn"
	ModerationDeleteMute = "delete_mute"
	ModerationDeleteBan  = "delete_ban"
	ModerationWarn       = "warn"
	ModerationMute       = "mute"
	ModerationKick       = "kick"
	ModerationBan        = "ban"
	ModerationNotify     = "notify"
	ModerationLog        = "log"
	ModerationRaidNotice = "raid_notice"

	RaidModeAuto      = "auto"
	RaidModeForcedOn  = "forced_on"
	RaidModeForcedOff = "forced_off"
)

const (
	RuleRecentExternalLink = "recent_external_link"
	RuleTelegramPromotion  = "telegram_promotion"
	RuleObfuscatedLink     = "obfuscated_link"
	RuleMentionFlood       = "mention_flood"
	RuleMessageFlood       = "message_flood"
	RuleMediaFlood         = "media_flood"
	RuleSelfRepeat         = "self_repeat"
	RuleCoordinatedRepeat  = "coordinated_repeat"
	RuleRecentForwardMedia = "recent_forward_or_media"
	RuleBlocklist          = "blocklist"
	RaidSignalJoin         = "join"
	RaidSignalVerifyFailed = "verification_failed"
	RaidSignalCoordinated  = "coordinated_repeat"
	RaidSignalPunished     = "punished_recent"
)

const (
	moderationWindow      = 24 * time.Hour
	messageFloodWindow    = 10 * time.Second
	mediaFloodWindow      = 30 * time.Second
	selfRepeatWindow      = time.Minute
	coordinatedWindow     = 5 * time.Minute
	raidQuietWindow       = 15 * time.Minute
	raidJoinWindow        = time.Minute
	raidSignalWindow      = 5 * time.Minute
	raidJoinThreshold     = 12
	raidFailureThreshold  = 6
	raidPunishedThreshold = 4
)

var (
	ErrModerationState  = errors.New("invalid moderation state")
	ErrModerationAction = errors.New("moderation action is not available")
)

type ModerationMessage struct {
	UpdateID        int64
	ChatID          int64
	MessageID       int
	TargetKind      string
	TargetID        int64
	ContentHash     string
	Domains         []string
	HasExternalLink bool
	HasTelegramLink bool
	HasObfuscation  bool
	HasMedia        bool
	IsForward       bool
	MentionCount    int
	MessageAt       time.Time
	VersionAt       time.Time
	Now             time.Time
}

type ModerationRuleScore struct {
	RuleID    string
	Score     int
	BaseScore int
}

type ModerationAssessment struct {
	Ignored         bool
	Duplicate       bool
	Recent          bool
	Blocked         bool
	RaidActive      bool
	MessageScore    int
	RollingScore    int
	Threshold       int
	Rules           []ModerationRuleScore
	ScheduledAction string
}

type ModerationAction struct {
	ID             int64
	IdempotencyKey string
	ChatID         int64
	MessageID      int
	TargetKind     string
	TargetID       int64
	ActorUserID    int64
	ActionKind     string
	Reason         string
	Score          int
	RuleIDs        []string
	Domains        []string
	UntilAt        *time.Time
	Result         string
	Status         string
	Attempts       int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ManualModerationRequest struct {
	IdempotencyKey   string
	ChatID           int64
	CommandMessageID int
	TargetUserID     int64
	ActorUserID      int64
	ActionKind       string
	UntilAt          *time.Time
	Reason           string
}

type RaidState struct {
	ChatID        int64
	Mode          string
	Active        bool
	EnteredAt     *time.Time
	LastSignalAt  *time.Time
	LastReason    string
	TransitionSeq int64
	UpdatedAt     time.Time
}

type ModerationStatus struct {
	RaidState
	JoinsLastMinute        int
	FailuresLastFiveMin    int
	CoordinatedLastFiveMin int
	PunishedLastFiveMin    int
	PendingActions         int
}

func (s *Store) AssessModerationMessage(ctx context.Context, input ModerationMessage) (ModerationAssessment, error) {
	if err := validateModerationMessage(input); err != nil {
		return ModerationAssessment{}, err
	}
	now := input.Now.UTC().Truncate(time.Second)
	messageAt := input.MessageAt.UTC().Truncate(time.Second)
	versionAt := input.VersionAt.UTC().Truncate(time.Second)
	domains := cleanStringSet(input.Domains, 16, 253)
	domainsJSON := marshalStrings(domains)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModerationAssessment{}, fmt.Errorf("begin moderation assessment: %w", err)
	}
	defer tx.Rollback()

	var enabled, moderationEnabled int
	err = tx.QueryRowContext(ctx, `
		SELECT enabled, moderation_enabled
		FROM managed_chats WHERE chat_id = ?
	`, input.ChatID).Scan(&enabled, &moderationEnabled)
	if errors.Is(err, sql.ErrNoRows) || enabled == 0 || moderationEnabled == 0 {
		return ModerationAssessment{Ignored: true}, tx.Commit()
	}
	if err != nil {
		return ModerationAssessment{}, fmt.Errorf("read moderation chat: %w", err)
	}

	if input.TargetKind == ModerationTargetUser {
		blocked, allowed, err := listStatusTx(ctx, tx, input.ChatID, input.TargetID)
		if err != nil {
			return ModerationAssessment{}, err
		}
		if allowed && !blocked {
			return ModerationAssessment{Ignored: true}, tx.Commit()
		}
	}

	var previousUpdateID int64
	var previousVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT update_id, version_at FROM message_observations
		WHERE chat_id = ? AND message_id = ?
	`, input.ChatID, input.MessageID).Scan(&previousUpdateID, &previousVersion)
	if err == nil && (input.UpdateID <= previousUpdateID || versionAt.Unix() < previousVersion) {
		return ModerationAssessment{Duplicate: true}, tx.Commit()
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ModerationAssessment{}, fmt.Errorf("read message observation: %w", err)
	}

	recent, err := recentMemberTx(ctx, tx, input.ChatID, input.TargetKind, input.TargetID, now)
	if err != nil {
		return ModerationAssessment{}, err
	}
	if err := ensureRaidStateTx(ctx, tx, input.ChatID, now); err != nil {
		return ModerationAssessment{}, err
	}
	var raidActiveInt int
	if err := tx.QueryRowContext(ctx, `SELECT active FROM raid_states WHERE chat_id = ?`, input.ChatID).Scan(&raidActiveInt); err != nil {
		return ModerationAssessment{}, fmt.Errorf("read Raid state: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO message_observations (
			chat_id, message_id, update_id, target_kind, target_id, content_hash, domains_json,
			has_link, has_telegram_link, has_obfuscation, has_media, is_forward,
			is_recent, mention_count, message_at, version_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			update_id = excluded.update_id,
			target_kind = excluded.target_kind,
			target_id = excluded.target_id,
			content_hash = excluded.content_hash,
			domains_json = excluded.domains_json,
			has_link = excluded.has_link,
			has_telegram_link = excluded.has_telegram_link,
			has_obfuscation = excluded.has_obfuscation,
			has_media = excluded.has_media,
			is_forward = excluded.is_forward,
			is_recent = excluded.is_recent,
			mention_count = excluded.mention_count,
			message_at = excluded.message_at,
			version_at = excluded.version_at,
			updated_at = excluded.updated_at
	`, input.ChatID, input.MessageID, input.UpdateID, input.TargetKind, input.TargetID,
		input.ContentHash, domainsJSON, boolInt(input.HasExternalLink || input.HasTelegramLink),
		boolInt(input.HasTelegramLink), boolInt(input.HasObfuscation), boolInt(input.HasMedia),
		boolInt(input.IsForward), boolInt(recent), input.MentionCount, messageAt.Unix(), versionAt.Unix(), now.Unix())
	if err != nil {
		return ModerationAssessment{}, fmt.Errorf("save message observation: %w", err)
	}

	rules, coordinated, err := moderationRulesTx(ctx, tx, input, recent, raidActiveInt == 1, now)
	if err != nil {
		return ModerationAssessment{}, err
	}
	blocked := false
	if input.TargetKind == ModerationTargetUser {
		blocked, _, err = listStatusTx(ctx, tx, input.ChatID, input.TargetID)
		if err != nil {
			return ModerationAssessment{}, err
		}
		if blocked {
			rules = append(rules, ModerationRuleScore{RuleID: RuleBlocklist, Score: 0, BaseScore: 0})
		}
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].RuleID < rules[j].RuleID })

	if _, err := tx.ExecContext(ctx, `DELETE FROM violations WHERE chat_id = ? AND message_id = ?`, input.ChatID, input.MessageID); err != nil {
		return ModerationAssessment{}, fmt.Errorf("replace message violations: %w", err)
	}
	messageScore := 0
	for _, rule := range rules {
		messageScore += rule.Score
		userID := int64(0)
		if input.TargetKind == ModerationTargetUser {
			userID = input.TargetID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO violations (
				chat_id, user_id, rule_id, score, content_hash, created_at,
				message_id, target_kind, target_id, domains_json, base_score
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, input.ChatID, userID, rule.RuleID, rule.Score, input.ContentHash, now.Unix(),
			input.MessageID, input.TargetKind, input.TargetID, domainsJSON, rule.BaseScore); err != nil {
			return ModerationAssessment{}, fmt.Errorf("save moderation rule: %w", err)
		}
	}

	var rollingScore, rollingBaseScore int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(score), 0), COALESCE(SUM(base_score), 0) FROM violations
		WHERE chat_id = ? AND target_kind = ? AND target_id = ? AND created_at >= ?
	`, input.ChatID, input.TargetKind, input.TargetID, now.Add(-moderationWindow).Unix()).Scan(&rollingScore, &rollingBaseScore); err != nil {
		return ModerationAssessment{}, fmt.Errorf("read rolling moderation score: %w", err)
	}

	if coordinated {
		bucket := now.Unix() / int64(coordinatedWindow/time.Second)
		signalKey := fmt.Sprintf("coordinated:%d:%s:%d", input.ChatID, input.ContentHash, bucket)
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO raid_signals
				(signal_key, chat_id, kind, target_id, content_hash, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, signalKey, input.ChatID, RaidSignalCoordinated, input.TargetID, input.ContentHash, now.Unix()); err != nil {
			return ModerationAssessment{}, fmt.Errorf("record coordinated Raid signal: %w", err)
		}
	}

	ruleIDs := make([]string, 0, len(rules))
	for _, rule := range rules {
		ruleIDs = append(ruleIDs, rule.RuleID)
	}
	threshold := moderationThreshold(rollingScore)
	if threshold == 12 && rollingBaseScore < 12 {
		threshold = 8
	}
	scheduled := ""
	if blocked {
		scheduled = ModerationDeleteBan
		key := fmt.Sprintf("block:%d:%d:%d", input.ChatID, input.TargetID, input.MessageID)
		if err := insertModerationActionTx(ctx, tx, ModerationAction{
			IdempotencyKey: key, ChatID: input.ChatID, MessageID: input.MessageID,
			TargetKind: input.TargetKind, TargetID: input.TargetID, ActionKind: scheduled,
			Reason: RuleBlocklist, Score: rollingScore, RuleIDs: ruleIDs, Domains: domains,
		}, now); err != nil {
			return ModerationAssessment{}, err
		}
	} else if threshold >= 3 {
		newThreshold, err := claimModerationThresholdTx(ctx, tx, input.ChatID, input.TargetKind, input.TargetID, threshold, now)
		if err != nil {
			return ModerationAssessment{}, err
		}
		if newThreshold {
			var untilAt *time.Time
			scheduled, untilAt = automaticAction(input.TargetKind, threshold, now)
			key := fmt.Sprintf("auto:%d:%s:%d:%d:%d", input.ChatID, input.TargetKind, input.TargetID, threshold, input.MessageID)
			if err := insertModerationActionTx(ctx, tx, ModerationAction{
				IdempotencyKey: key, ChatID: input.ChatID, MessageID: input.MessageID,
				TargetKind: input.TargetKind, TargetID: input.TargetID, ActionKind: scheduled,
				Reason: fmt.Sprintf("threshold_%d", threshold), Score: rollingScore,
				RuleIDs: ruleIDs, Domains: domains, UntilAt: untilAt,
			}, now); err != nil {
				return ModerationAssessment{}, err
			}
		}
	}

	if recent && input.TargetKind == ModerationTargetUser && scheduled == "" && !hasHighRiskRule(rules) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE member_states SET normal_message_count = normal_message_count + 1
			WHERE chat_id = ? AND user_id = ? AND recent_until > ? AND normal_message_count < 3
		`, input.ChatID, input.TargetID, now.Unix()); err != nil {
			return ModerationAssessment{}, fmt.Errorf("advance recent member window: %w", err)
		}
	}

	transitioned, err := evaluateRaidTx(ctx, tx, input.ChatID, now)
	if err != nil {
		return ModerationAssessment{}, err
	}
	_ = transitioned
	if err := tx.QueryRowContext(ctx, `SELECT active FROM raid_states WHERE chat_id = ?`, input.ChatID).Scan(&raidActiveInt); err != nil {
		return ModerationAssessment{}, fmt.Errorf("refresh Raid state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ModerationAssessment{}, fmt.Errorf("commit moderation assessment: %w", err)
	}
	return ModerationAssessment{
		Recent: recent, Blocked: blocked, RaidActive: raidActiveInt == 1,
		MessageScore: messageScore, RollingScore: rollingScore, Threshold: threshold,
		Rules: rules, ScheduledAction: scheduled,
	}, nil
}

func moderationRulesTx(ctx context.Context, tx *sql.Tx, input ModerationMessage, recent, raidActive bool, now time.Time) ([]ModerationRuleScore, bool, error) {
	rules := make([]ModerationRuleScore, 0, 9)
	add := func(id string, score int) {
		rules = append(rules, ModerationRuleScore{RuleID: id, Score: score, BaseScore: score})
	}
	if recent && input.HasExternalLink {
		add(RuleRecentExternalLink, 1)
	}
	if recent && input.HasTelegramLink {
		add(RuleTelegramPromotion, 3)
	}
	if input.HasObfuscation {
		add(RuleObfuscatedLink, 2)
	}
	if input.MentionCount >= 10 {
		add(RuleMentionFlood, 3)
	} else if input.MentionCount >= 5 {
		add(RuleMentionFlood, 2)
	}
	if recent && (input.IsForward || (input.HasMedia && input.HasTelegramLink)) {
		add(RuleRecentForwardMedia, 1)
	}

	var messageCount, mediaCount, selfCount, coordinatedCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM message_observations
		WHERE chat_id = ? AND target_kind = ? AND target_id = ? AND message_at >= ?
	`, input.ChatID, input.TargetKind, input.TargetID, now.Add(-messageFloodWindow).Unix()).Scan(&messageCount); err != nil {
		return nil, false, fmt.Errorf("count message rate: %w", err)
	}
	if messageCount >= 6 {
		add(RuleMessageFlood, 3)
	}
	if recent {
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM message_observations
			WHERE chat_id = ? AND target_kind = ? AND target_id = ? AND has_media = 1 AND message_at >= ?
		`, input.ChatID, input.TargetKind, input.TargetID, now.Add(-mediaFloodWindow).Unix()).Scan(&mediaCount); err != nil {
			return nil, false, fmt.Errorf("count media rate: %w", err)
		}
		if mediaCount >= 3 {
			add(RuleMediaFlood, 2)
		}
	}
	if input.ContentHash != "" {
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM message_observations
			WHERE chat_id = ? AND target_kind = ? AND target_id = ?
			  AND content_hash = ? AND message_at >= ?
		`, input.ChatID, input.TargetKind, input.TargetID, input.ContentHash,
			now.Add(-selfRepeatWindow).Unix()).Scan(&selfCount); err != nil {
			return nil, false, fmt.Errorf("count self repeats: %w", err)
		}
		if selfCount >= 2 {
			add(RuleSelfRepeat, 3)
		}
		if recent && input.TargetKind == ModerationTargetUser {
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(DISTINCT target_id) FROM message_observations
				WHERE chat_id = ? AND target_kind = 'user' AND is_recent = 1
				  AND content_hash = ? AND message_at >= ?
			`, input.ChatID, input.ContentHash, now.Add(-coordinatedWindow).Unix()).Scan(&coordinatedCount); err != nil {
				return nil, false, fmt.Errorf("count coordinated repeats: %w", err)
			}
			if coordinatedCount >= 3 {
				add(RuleCoordinatedRepeat, 5)
			}
		}
	}
	if !recent && input.HasTelegramLink && (input.MentionCount >= 5 || selfCount >= 2 || coordinatedCount >= 3) {
		add(RuleTelegramPromotion, 2)
	}
	if raidActive && recent && len(rules) > 0 {
		for index := range rules {
			rules[index].Score++
		}
	}
	return rules, coordinatedCount >= 3, nil
}

func validateModerationMessage(input ModerationMessage) error {
	if input.UpdateID < 0 || input.ChatID == 0 || input.MessageID <= 0 || input.TargetID == 0 ||
		(input.TargetKind != ModerationTargetUser && input.TargetKind != ModerationTargetSenderChat) ||
		len(input.ContentHash) < 32 || input.MentionCount < 0 || input.MessageAt.IsZero() ||
		input.VersionAt.IsZero() || input.Now.IsZero() {
		return ErrModerationState
	}
	return nil
}

func recentMemberTx(ctx context.Context, tx *sql.Tx, chatID int64, targetKind string, targetID int64, now time.Time) (bool, error) {
	if targetKind != ModerationTargetUser {
		return false, nil
	}
	var recent bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM member_states
			WHERE chat_id = ? AND user_id = ? AND recent_until > ? AND normal_message_count < 3
		)
	`, chatID, targetID, now.Unix()).Scan(&recent); err != nil {
		return false, fmt.Errorf("read recent member state: %w", err)
	}
	return recent, nil
}

func listStatusTx(ctx context.Context, tx *sql.Tx, chatID, userID int64) (blocked, allowed bool, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT kind FROM list_entries WHERE chat_id = ? AND user_id = ?`, chatID, userID)
	if err != nil {
		return false, false, fmt.Errorf("read moderation list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return false, false, fmt.Errorf("scan moderation list: %w", err)
		}
		blocked = blocked || kind == "block"
		allowed = allowed || kind == "allow"
	}
	return blocked, allowed, rows.Err()
}

func moderationThreshold(score int) int {
	switch {
	case score >= 12:
		return 12
	case score >= 8:
		return 8
	case score >= 5:
		return 5
	case score >= 3:
		return 3
	default:
		return 0
	}
}

func automaticAction(targetKind string, threshold int, now time.Time) (string, *time.Time) {
	if targetKind == ModerationTargetSenderChat {
		return ModerationDeleteWarn, nil
	}
	switch threshold {
	case 3:
		return ModerationDeleteWarn, nil
	case 5:
		until := now.Add(10 * time.Minute)
		return ModerationDeleteMute, &until
	case 8:
		until := now.Add(24 * time.Hour)
		return ModerationDeleteMute, &until
	case 12:
		return ModerationDeleteBan, nil
	default:
		return "", nil
	}
}

func claimModerationThresholdTx(ctx context.Context, tx *sql.Tx, chatID int64, targetKind string, targetID int64, threshold int, now time.Time) (bool, error) {
	var previous int
	var lastAction sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT last_threshold, last_action_at FROM moderation_subjects
		WHERE chat_id = ? AND target_kind = ? AND target_id = ?
	`, chatID, targetKind, targetID).Scan(&previous, &lastAction)
	if errors.Is(err, sql.ErrNoRows) {
		previous = 0
	} else if err != nil {
		return false, fmt.Errorf("read moderation threshold: %w", err)
	}
	if lastAction.Valid && lastAction.Int64 <= now.Add(-moderationWindow).Unix() {
		previous = 0
	}
	if threshold <= previous {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO moderation_subjects
			(chat_id, target_kind, target_id, last_threshold, last_action_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, target_kind, target_id) DO UPDATE SET
			last_threshold = excluded.last_threshold,
			last_action_at = excluded.last_action_at,
			updated_at = excluded.updated_at
	`, chatID, targetKind, targetID, threshold, now.Unix(), now.Unix())
	if err != nil {
		return false, fmt.Errorf("claim moderation threshold: %w", err)
	}
	return true, nil
}

func hasHighRiskRule(rules []ModerationRuleScore) bool {
	for _, rule := range rules {
		if rule.Score >= 2 {
			return true
		}
	}
	return false
}

func insertModerationActionTx(ctx context.Context, tx *sql.Tx, action ModerationAction, now time.Time) error {
	if action.IdempotencyKey == "" || action.ChatID == 0 || action.TargetID == 0 || !validModerationTarget(action.TargetKind) || !validModerationAction(action.ActionKind) {
		return ErrModerationState
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO moderation_actions (
			idempotency_key, chat_id, message_id, target_kind, target_id, actor_user_id,
			action_kind, reason, score, rule_ids_json, domains_json, until_at,
			status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, NULLIF(?, 0), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(idempotency_key) DO NOTHING
	`, action.IdempotencyKey, action.ChatID, action.MessageID, action.TargetKind, action.TargetID,
		action.ActorUserID, action.ActionKind, cleanReason(action.Reason), action.Score,
		marshalStrings(cleanStringSet(action.RuleIDs, 16, 64)), marshalStrings(cleanStringSet(action.Domains, 16, 253)),
		nullableUnix(action.UntilAt), ModerationPending, now.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("schedule moderation action: %w", err)
	}
	return nil
}

func validModerationTarget(kind string) bool {
	return kind == ModerationTargetUser || kind == ModerationTargetSenderChat || kind == ModerationTargetChat
}

func validModerationAction(kind string) bool {
	switch kind {
	case ModerationDeleteWarn, ModerationDeleteMute, ModerationDeleteBan,
		ModerationWarn, ModerationMute, ModerationKick, ModerationBan,
		ModerationNotify, ModerationLog, ModerationRaidNotice:
		return true
	default:
		return false
	}
}

func cleanReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 64 {
		reason = reason[:64]
	}
	for _, character := range reason {
		if !(character == '_' || character == '-' || character == ':' || character == '.' ||
			(character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9')) {
			return "moderation"
		}
	}
	return reason
}

func cleanStringSet(values []string, maximum, length int) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > length {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == maximum {
			break
		}
	}
	sort.Strings(result)
	return result
}

func marshalStrings(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) ClaimModerationActions(ctx context.Context, retryBefore, now time.Time, limit int) ([]ModerationAction, error) {
	if limit <= 0 || limit > 200 {
		return nil, ErrModerationState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin moderation action claim: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM moderation_actions
		WHERE status = ?
		   OR (status = ? AND updated_at <= ?)
		   OR (status = ? AND updated_at <= ?
		       AND action_kind NOT IN (?, ?, ?, ?))
		ORDER BY id LIMIT ?
	`, ModerationPending, ModerationFailed, retryBefore.Unix(),
		ModerationProcessing, retryBefore.Unix(), ModerationWarn, ModerationNotify,
		ModerationLog, ModerationRaidNotice, limit)
	if err != nil {
		return nil, fmt.Errorf("list moderation actions: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan moderation action identifier: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close moderation action list: %w", err)
	}

	claimed := make([]ModerationAction, 0, len(ids))
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `
			UPDATE moderation_actions
			SET status = ?, attempts = attempts + 1, updated_at = ?
			WHERE id = ? AND (
				status = ?
				OR (status = ? AND updated_at <= ?)
				OR (status = ? AND updated_at <= ?
				    AND action_kind NOT IN (?, ?, ?, ?))
			)
		`, ModerationProcessing, now.Unix(), id, ModerationPending,
			ModerationFailed, retryBefore.Unix(), ModerationProcessing, retryBefore.Unix(),
			ModerationWarn, ModerationNotify, ModerationLog, ModerationRaidNotice)
		if err != nil {
			return nil, fmt.Errorf("claim moderation action: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			continue
		}
		action, err := scanModerationAction(tx.QueryRowContext(ctx, moderationActionSelect+` WHERE id = ?`, id))
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, action)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit moderation action claim: %w", err)
	}
	return claimed, nil
}

// SettleStaleUnreplayableActions closes actions whose Telegram send may have
// succeeded before the process stopped. Retrying these actions could duplicate
// a group notification or management log, so their outcome is recorded as
// indeterminate instead.
func (s *Store) SettleStaleUnreplayableActions(ctx context.Context, retryBefore, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin stale moderation action settlement: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, moderationActionSelect+`
		WHERE status = ? AND updated_at <= ? AND action_kind IN (?, ?, ?, ?)
		ORDER BY id
	`, ModerationProcessing, retryBefore.Unix(), ModerationWarn, ModerationNotify,
		ModerationLog, ModerationRaidNotice)
	if err != nil {
		return fmt.Errorf("list stale unreplayable moderation actions: %w", err)
	}
	var actions []ModerationAction
	for rows.Next() {
		action, err := scanModerationAction(rows)
		if err != nil {
			rows.Close()
			return err
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate stale moderation action list: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close stale moderation action list: %w", err)
	}
	for _, action := range actions {
		result, err := tx.ExecContext(ctx, `
			UPDATE moderation_actions
			SET status = ?, result = 'indeterminate_after_restart', completed_at = ?, updated_at = ?
			WHERE id = ? AND status = ? AND updated_at <= ?
		`, ModerationCompleted, now.Unix(), now.Unix(), action.ID,
			ModerationProcessing, retryBefore.Unix())
		if err != nil {
			return fmt.Errorf("settle stale moderation action: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			continue
		}
		metadata := moderationAuditMetadata(action, "indeterminate_after_restart")
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events
				(chat_id, actor_user_id, target_user_id, event_type, metadata_json, created_at)
			VALUES (?, NULLIF(?, 0), NULLIF(?, 0), 'moderation_action_indeterminate', json(?), ?)
		`, action.ChatID, action.ActorUserID, userTarget(action), metadata, now.Unix()); err != nil {
			return fmt.Errorf("audit indeterminate moderation action: %w", err)
		}
		if action.ActionKind == ModerationWarn {
			logAction := action
			logAction.ID = 0
			logAction.IdempotencyKey += ":log"
			logAction.ActionKind = ModerationLog
			logAction.Reason = action.ActionKind + ":" + action.Reason + ":indeterminate_after_restart"
			if err := insertModerationActionTx(ctx, tx, logAction, now); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit stale moderation action settlement: %w", err)
	}
	return nil
}

func (s *Store) CompleteModerationAction(ctx context.Context, id int64, result string, now time.Time) error {
	if result != "completed" && result != "skipped_admin" && result != "skipped_owner" &&
		result != "skipped_sender_chat" && result != "indeterminate_after_restart" {
		return ErrModerationState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin moderation action completion: %w", err)
	}
	defer tx.Rollback()
	action, err := scanModerationAction(tx.QueryRowContext(ctx, moderationActionSelect+` WHERE id = ?`, id))
	if err != nil {
		return err
	}
	if action.Status == ModerationCompleted {
		return nil
	}
	if action.Status != ModerationProcessing {
		return ErrModerationAction
	}
	update, err := tx.ExecContext(ctx, `
		UPDATE moderation_actions SET status = ?, result = ?, completed_at = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, ModerationCompleted, result, now.Unix(), now.Unix(), id, ModerationProcessing)
	if err != nil {
		return fmt.Errorf("complete moderation action: %w", err)
	}
	rows, _ := update.RowsAffected()
	if rows != 1 {
		return ErrModerationAction
	}
	metadata := moderationAuditMetadata(action, result)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events
			(chat_id, actor_user_id, target_user_id, event_type, metadata_json, created_at)
		VALUES (?, NULLIF(?, 0), NULLIF(?, 0), 'moderation_action_completed', json(?), ?)
	`, action.ChatID, action.ActorUserID, userTarget(action), metadata, now.Unix()); err != nil {
		return fmt.Errorf("audit moderation action: %w", err)
	}
	if result == "completed" && action.TargetKind == ModerationTargetUser && isEnforcementAction(action.ActionKind) {
		var recent bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM message_observations
				WHERE chat_id = ? AND message_id = ? AND target_kind = 'user'
				  AND target_id = ? AND is_recent = 1
			)
		`, action.ChatID, action.MessageID, action.TargetID).Scan(&recent); err != nil {
			return fmt.Errorf("read completed punishment recency: %w", err)
		}
		if recent {
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO raid_signals
					(signal_key, chat_id, kind, target_id, created_at)
				VALUES (?, ?, ?, ?, ?)
			`, fmt.Sprintf("punished-action:%d", action.ID), action.ChatID,
				RaidSignalPunished, action.TargetID, now.Unix()); err != nil {
				return fmt.Errorf("record completed punishment Raid signal: %w", err)
			}
		}
	}
	if result == "completed" && needsGroupNotification(action.ActionKind) {
		notifyAction := action
		notifyAction.ID = 0
		notifyAction.IdempotencyKey += ":notify"
		notifyAction.ActionKind = ModerationNotify
		notifyAction.Reason = action.ActionKind + ":" + action.Reason + ":" + result
		if err := insertModerationActionTx(ctx, tx, notifyAction, now); err != nil {
			return err
		}
	}
	if action.ActionKind != ModerationNotify && action.ActionKind != ModerationLog && action.ActionKind != ModerationRaidNotice {
		logAction := action
		logAction.ID = 0
		logAction.IdempotencyKey += ":log"
		logAction.ActionKind = ModerationLog
		logAction.Reason = action.ActionKind + ":" + action.Reason + ":" + result
		if err := insertModerationActionTx(ctx, tx, logAction, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit moderation action completion: %w", err)
	}
	return nil
}

func isEnforcementAction(kind string) bool {
	switch kind {
	case ModerationDeleteWarn, ModerationDeleteMute, ModerationDeleteBan,
		ModerationMute, ModerationKick, ModerationBan:
		return true
	default:
		return false
	}
}

func needsGroupNotification(kind string) bool {
	return isEnforcementAction(kind)
}

func (s *Store) FailModerationAction(ctx context.Context, id int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin moderation action failure: %w", err)
	}
	defer tx.Rollback()
	action, err := scanModerationAction(tx.QueryRowContext(ctx, moderationActionSelect+` WHERE id = ?`, id))
	if err != nil {
		return err
	}
	if action.Status != ModerationProcessing {
		if action.Status == ModerationFailed || action.Status == ModerationCompleted {
			return nil
		}
		return ErrModerationAction
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE moderation_actions SET status = ?, result = 'telegram_error', updated_at = ?
		WHERE id = ? AND status = ?
	`, ModerationFailed, now.Unix(), id, ModerationProcessing); err != nil {
		return fmt.Errorf("fail moderation action: %w", err)
	}
	metadata := moderationAuditMetadata(action, "telegram_error")
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events
			(chat_id, actor_user_id, target_user_id, event_type, metadata_json, created_at)
		VALUES (?, NULLIF(?, 0), NULLIF(?, 0), 'moderation_action_failed', json(?), ?)
	`, action.ChatID, action.ActorUserID, userTarget(action), metadata, now.Unix()); err != nil {
		return fmt.Errorf("audit failed moderation action: %w", err)
	}
	return tx.Commit()
}

func (s *Store) GetModerationActionByKey(ctx context.Context, key string) (ModerationAction, error) {
	action, err := scanModerationAction(s.db.QueryRowContext(ctx, moderationActionSelect+` WHERE idempotency_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return ModerationAction{}, ErrModerationAction
	}
	return action, err
}

func (s *Store) ScheduleManualModeration(ctx context.Context, request ManualModerationRequest, now time.Time) (bool, error) {
	if request.IdempotencyKey == "" || request.ChatID == 0 || request.TargetUserID <= 0 || request.ActorUserID <= 0 ||
		(request.ActionKind != ModerationWarn && request.ActionKind != ModerationMute &&
			request.ActionKind != ModerationKick && request.ActionKind != ModerationBan) {
		return false, ErrModerationState
	}
	if request.ActionKind == ModerationMute && request.UntilAt == nil {
		return false, ErrModerationState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin manual moderation: %w", err)
	}
	defer tx.Rollback()
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM managed_chats WHERE chat_id = ?`, request.ChatID).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrManagedChatNotFound
		}
		return false, fmt.Errorf("read manual moderation chat: %w", err)
	}
	if enabled == 0 {
		return false, ErrManagedChatNotFound
	}
	action := ModerationAction{
		IdempotencyKey: request.IdempotencyKey, ChatID: request.ChatID,
		MessageID: request.CommandMessageID, TargetKind: ModerationTargetUser,
		TargetID: request.TargetUserID, ActorUserID: request.ActorUserID,
		ActionKind: request.ActionKind, Reason: request.Reason, UntilAt: request.UntilAt,
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO moderation_actions (
			idempotency_key, chat_id, message_id, target_kind, target_id, actor_user_id,
			action_kind, reason, score, rule_ids_json, domains_json, until_at,
			status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, '[]', '[]', ?, ?, ?, ?)
		ON CONFLICT(idempotency_key) DO NOTHING
	`, action.IdempotencyKey, action.ChatID, action.MessageID, action.TargetKind, action.TargetID,
		action.ActorUserID, action.ActionKind, cleanReason(action.Reason), nullableUnix(action.UntilAt),
		ModerationPending, now.Unix(), now.Unix())
	if err != nil {
		return false, fmt.Errorf("schedule manual moderation: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 1 {
		metadata, _ := json.Marshal(map[string]any{"action": action.ActionKind, "target_user_id": action.TargetID})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events
				(chat_id, actor_user_id, target_user_id, event_type, metadata_json, created_at)
			VALUES (?, ?, ?, 'manual_moderation_scheduled', json(?), ?)
		`, action.ChatID, action.ActorUserID, action.TargetID, string(metadata), now.Unix()); err != nil {
			return false, fmt.Errorf("audit manual moderation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit manual moderation: %w", err)
	}
	return changed == 1, nil
}

func (s *Store) UpdateListEntry(ctx context.Context, operationKey string, chatID, userID int64, kind string, add bool, actorUserID int64, now time.Time) (bool, error) {
	if operationKey == "" || chatID == 0 || userID <= 0 || actorUserID <= 0 || (kind != "allow" && kind != "block") {
		return false, ErrModerationState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin moderation list update: %w", err)
	}
	defer tx.Rollback()
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM managed_chats WHERE chat_id = ?`, chatID).Scan(&enabled); err != nil || enabled == 0 {
		if errors.Is(err, sql.ErrNoRows) || enabled == 0 {
			return false, ErrManagedChatNotFound
		}
		return false, fmt.Errorf("read moderation list chat: %w", err)
	}
	changed := false
	if add {
		var current string
		err := tx.QueryRowContext(ctx, `
			SELECT kind FROM list_entries WHERE chat_id = ? AND user_id = ?
			ORDER BY CASE kind WHEN 'block' THEN 0 ELSE 1 END LIMIT 1
		`, chatID, userID).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("read existing moderation list entry: %w", err)
		}
		if current != kind {
			if _, err := tx.ExecContext(ctx, `DELETE FROM list_entries WHERE chat_id = ? AND user_id = ?`, chatID, userID); err != nil {
				return false, fmt.Errorf("clear conflicting moderation list entry: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO list_entries (chat_id, user_id, kind, created_at) VALUES (?, ?, ?, ?)
			`, chatID, userID, kind, now.Unix()); err != nil {
				return false, fmt.Errorf("add moderation list entry: %w", err)
			}
			changed = true
		}
	} else {
		result, err := tx.ExecContext(ctx, `DELETE FROM list_entries WHERE chat_id = ? AND user_id = ? AND kind = ?`, chatID, userID, kind)
		if err != nil {
			return false, fmt.Errorf("remove moderation list entry: %w", err)
		}
		rows, _ := result.RowsAffected()
		changed = rows == 1
	}
	if changed {
		operation := kind + "_removed"
		if add {
			operation = kind + "_added"
		}
		metadata, _ := json.Marshal(map[string]any{"kind": kind, "operation": operation})
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events
				(chat_id, actor_user_id, target_user_id, event_type, metadata_json, created_at)
			VALUES (?, ?, ?, 'moderation_list_changed', json(?), ?)
		`, chatID, actorUserID, userID, string(metadata), now.Unix()); err != nil {
			return false, fmt.Errorf("audit moderation list update: %w", err)
		}
		if err := insertModerationActionTx(ctx, tx, ModerationAction{
			IdempotencyKey: "list:" + operationKey,
			ChatID:         chatID, TargetKind: ModerationTargetUser, TargetID: userID,
			ActorUserID: actorUserID, ActionKind: ModerationLog, Reason: operation,
		}, now); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit moderation list update: %w", err)
	}
	return changed, nil
}

func (s *Store) IsBlocked(ctx context.Context, chatID, userID int64) (bool, error) {
	var blocked bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM list_entries WHERE chat_id = ? AND user_id = ? AND kind = 'block')
	`, chatID, userID).Scan(&blocked); err != nil {
		return false, fmt.Errorf("read blocklist: %w", err)
	}
	return blocked, nil
}

func moderationAuditMetadata(action ModerationAction, result string) string {
	metadata, _ := json.Marshal(map[string]any{
		"action": action.ActionKind, "result": result, "message_id": action.MessageID,
		"target_kind": action.TargetKind, "target_id": action.TargetID,
		"reason": action.Reason, "score": action.Score, "rules": action.RuleIDs,
		"domains": action.Domains, "until_at": unixPointer(action.UntilAt),
	})
	return string(metadata)
}

func userTarget(action ModerationAction) int64 {
	if action.TargetKind == ModerationTargetUser {
		return action.TargetID
	}
	return 0
}

func unixPointer(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

const moderationActionSelect = `
	SELECT id, idempotency_key, chat_id, message_id, target_kind, target_id,
	       COALESCE(actor_user_id, 0), action_kind, reason, score,
	       rule_ids_json, domains_json, until_at, result, status, attempts,
	       created_at, updated_at
	FROM moderation_actions`

func scanModerationAction(row rowScanner) (ModerationAction, error) {
	var action ModerationAction
	var rulesJSON, domainsJSON string
	var untilAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&action.ID, &action.IdempotencyKey, &action.ChatID, &action.MessageID,
		&action.TargetKind, &action.TargetID, &action.ActorUserID, &action.ActionKind,
		&action.Reason, &action.Score, &rulesJSON, &domainsJSON, &untilAt,
		&action.Result, &action.Status, &action.Attempts, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ModerationAction{}, ErrModerationAction
		}
		return ModerationAction{}, fmt.Errorf("read moderation action: %w", err)
	}
	_ = json.Unmarshal([]byte(rulesJSON), &action.RuleIDs)
	_ = json.Unmarshal([]byte(domainsJSON), &action.Domains)
	action.UntilAt = timePointer(untilAt)
	action.CreatedAt = time.Unix(createdAt, 0).UTC()
	action.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return action, nil
}

func (s *Store) SetRaidMode(ctx context.Context, chatID int64, mode string, actorUserID int64, now time.Time) (RaidState, bool, error) {
	if chatID == 0 || actorUserID <= 0 || !validRaidMode(mode) {
		return RaidState{}, false, ErrModerationState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RaidState{}, false, fmt.Errorf("begin Raid mode update: %w", err)
	}
	defer tx.Rollback()
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM managed_chats WHERE chat_id = ?`, chatID).Scan(&enabled); err != nil || enabled == 0 {
		if errors.Is(err, sql.ErrNoRows) || enabled == 0 {
			return RaidState{}, false, ErrManagedChatNotFound
		}
		return RaidState{}, false, fmt.Errorf("read Raid chat: %w", err)
	}
	if err := ensureRaidStateTx(ctx, tx, chatID, now); err != nil {
		return RaidState{}, false, err
	}
	state, err := scanRaidState(tx.QueryRowContext(ctx, raidStateSelect+` WHERE chat_id = ?`, chatID))
	if err != nil {
		return RaidState{}, false, err
	}
	if state.Mode == mode {
		if err := tx.Commit(); err != nil {
			return RaidState{}, false, err
		}
		return state, false, nil
	}
	active := state.Active
	enteredAt := state.EnteredAt
	reason := "manual_auto"
	switch mode {
	case RaidModeForcedOn:
		active = true
		reason = "manual_forced_on"
		if enteredAt == nil {
			value := now
			enteredAt = &value
		}
	case RaidModeForcedOff:
		active = false
		enteredAt = nil
		reason = "manual_forced_off"
	case RaidModeAuto:
		trigger, triggerReason, _, err := raidTriggerTx(ctx, tx, chatID, now)
		if err != nil {
			return RaidState{}, false, err
		}
		active = trigger
		if trigger {
			reason = triggerReason
			if enteredAt == nil {
				value := now
				enteredAt = &value
			}
		} else {
			enteredAt = nil
		}
	}
	state.Mode = mode
	state.Active = active
	state.EnteredAt = enteredAt
	state.LastReason = reason
	state.TransitionSeq++
	state.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		UPDATE raid_states SET mode = ?, active = ?, entered_at = ?, last_reason = ?,
			transition_seq = ?, updated_at = ? WHERE chat_id = ?
	`, state.Mode, boolInt(state.Active), nullableUnix(state.EnteredAt), state.LastReason,
		state.TransitionSeq, now.Unix(), chatID); err != nil {
		return RaidState{}, false, fmt.Errorf("update Raid mode: %w", err)
	}
	if err := scheduleRaidNoticeTx(ctx, tx, state, actorUserID, now); err != nil {
		return RaidState{}, false, err
	}
	metadata, _ := json.Marshal(map[string]any{"mode": mode, "active": active, "reason": reason})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (chat_id, actor_user_id, event_type, metadata_json, created_at)
		VALUES (?, ?, 'raid_mode_changed', json(?), ?)
	`, chatID, actorUserID, string(metadata), now.Unix()); err != nil {
		return RaidState{}, false, fmt.Errorf("audit Raid mode: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return RaidState{}, false, fmt.Errorf("commit Raid mode: %w", err)
	}
	return state, true, nil
}

func (s *Store) RefreshRaidStates(ctx context.Context, now time.Time) error {
	now = now.UTC().Truncate(time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Raid refresh: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO raid_signals (signal_key, chat_id, kind, target_id, created_at)
		SELECT 'join:' || id, chat_id, 'join', user_id, created_at
		FROM join_cases WHERE created_at >= ?
	`, now.Add(-time.Hour).Unix()); err != nil {
		return fmt.Errorf("synchronize join Raid signals: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO raid_signals (signal_key, chat_id, kind, target_id, created_at)
		SELECT 'verify-failed:' || id, chat_id, 'verification_failed', user_id, updated_at
		FROM join_cases WHERE reason = 'verification_failed' AND updated_at >= ?
	`, now.Add(-time.Hour).Unix()); err != nil {
		return fmt.Errorf("synchronize verification Raid signals: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT chat_id FROM managed_chats WHERE enabled = 1 AND moderation_enabled = 1`)
	if err != nil {
		return fmt.Errorf("list Raid chats: %w", err)
	}
	var chatIDs []int64
	for rows.Next() {
		var chatID int64
		if err := rows.Scan(&chatID); err != nil {
			rows.Close()
			return fmt.Errorf("scan Raid chat: %w", err)
		}
		chatIDs = append(chatIDs, chatID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close Raid chat list: %w", err)
	}
	for _, chatID := range chatIDs {
		if err := ensureRaidStateTx(ctx, tx, chatID, now); err != nil {
			return err
		}
		if _, err := evaluateRaidTx(ctx, tx, chatID, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Raid refresh: %w", err)
	}
	return nil
}

func (s *Store) GetModerationStatus(ctx context.Context, chatID int64, now time.Time) (ModerationStatus, error) {
	if chatID == 0 {
		return ModerationStatus{}, ErrModerationState
	}
	chat, err := s.GetManagedChat(ctx, chatID)
	if err != nil {
		return ModerationStatus{}, err
	}
	if !chat.Enabled || !chat.ModerationEnabled {
		return ModerationStatus{}, ErrManagedChatNotFound
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO raid_states (chat_id, mode, active, updated_at) VALUES (?, 'auto', 0, ?)
	`, chatID, now.Unix()); err != nil {
		return ModerationStatus{}, fmt.Errorf("ensure Raid status: %w", err)
	}
	state, err := scanRaidState(s.db.QueryRowContext(ctx, raidStateSelect+` WHERE chat_id = ?`, chatID))
	if err != nil {
		return ModerationStatus{}, err
	}
	status := ModerationStatus{RaidState: state}
	queries := []struct {
		destination *int
		query       string
		args        []any
	}{
		{&status.JoinsLastMinute, `SELECT COUNT(*) FROM raid_signals WHERE chat_id = ? AND kind = 'join' AND created_at >= ?`, []any{chatID, now.Add(-raidJoinWindow).Unix()}},
		{&status.FailuresLastFiveMin, `SELECT COUNT(*) FROM raid_signals WHERE chat_id = ? AND kind = 'verification_failed' AND created_at >= ?`, []any{chatID, now.Add(-raidSignalWindow).Unix()}},
		{&status.CoordinatedLastFiveMin, `SELECT COUNT(*) FROM raid_signals WHERE chat_id = ? AND kind = 'coordinated_repeat' AND created_at >= ?`, []any{chatID, now.Add(-raidSignalWindow).Unix()}},
		{&status.PunishedLastFiveMin, `SELECT COUNT(DISTINCT target_id) FROM raid_signals WHERE chat_id = ? AND kind = 'punished_recent' AND created_at >= ?`, []any{chatID, now.Add(-raidSignalWindow).Unix()}},
		{&status.PendingActions, `SELECT COUNT(*) FROM moderation_actions WHERE chat_id = ? AND status IN ('pending', 'processing', 'failed')`, []any{chatID}},
	}
	for _, query := range queries {
		if err := s.db.QueryRowContext(ctx, query.query, query.args...).Scan(query.destination); err != nil {
			return ModerationStatus{}, fmt.Errorf("read moderation status: %w", err)
		}
	}
	return status, nil
}

func ensureRaidStateTx(ctx context.Context, tx *sql.Tx, chatID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO raid_states (chat_id, mode, active, updated_at)
		VALUES (?, 'auto', 0, ?)
	`, chatID, now.Unix()); err != nil {
		return fmt.Errorf("ensure Raid state: %w", err)
	}
	return nil
}

func evaluateRaidTx(ctx context.Context, tx *sql.Tx, chatID int64, now time.Time) (bool, error) {
	state, err := scanRaidState(tx.QueryRowContext(ctx, raidStateSelect+` WHERE chat_id = ?`, chatID))
	if err != nil {
		return false, err
	}
	trigger, reason, lastSignal, err := raidTriggerTx(ctx, tx, chatID, now)
	if err != nil {
		return false, err
	}
	desired := state.Active
	transitionReason := ""
	switch state.Mode {
	case RaidModeForcedOn:
		desired = true
	case RaidModeForcedOff:
		desired = false
	case RaidModeAuto:
		if !state.Active && trigger {
			desired = true
			transitionReason = reason
		} else if state.Active {
			quietSince := state.LastSignalAt
			if lastSignal != nil && (quietSince == nil || lastSignal.After(*quietSince)) {
				quietSince = lastSignal
			}
			if quietSince == nil {
				quietSince = state.EnteredAt
			}
			if quietSince != nil && !quietSince.After(now.Add(-raidQuietWindow)) {
				desired = false
				transitionReason = "quiet_exit"
			}
		}
	}
	if lastSignal != nil && (state.LastSignalAt == nil || lastSignal.After(*state.LastSignalAt)) {
		state.LastSignalAt = lastSignal
	}
	if desired == state.Active {
		if state.LastSignalAt != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE raid_states SET last_signal_at = ?, updated_at = ? WHERE chat_id = ?`,
				state.LastSignalAt.Unix(), now.Unix(), chatID); err != nil {
				return false, fmt.Errorf("refresh Raid signal time: %w", err)
			}
		}
		return false, nil
	}
	state.Active = desired
	state.LastReason = transitionReason
	state.TransitionSeq++
	state.UpdatedAt = now
	if desired {
		entered := now
		state.EnteredAt = &entered
	} else {
		state.EnteredAt = nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE raid_states SET active = ?, entered_at = ?, last_signal_at = ?,
			last_reason = ?, transition_seq = ?, updated_at = ? WHERE chat_id = ?
	`, boolInt(state.Active), nullableUnix(state.EnteredAt), nullableUnix(state.LastSignalAt),
		state.LastReason, state.TransitionSeq, now.Unix(), chatID); err != nil {
		return false, fmt.Errorf("transition Raid state: %w", err)
	}
	if err := scheduleRaidNoticeTx(ctx, tx, state, 0, now); err != nil {
		return false, err
	}
	metadata, _ := json.Marshal(map[string]any{"active": state.Active, "mode": state.Mode, "reason": state.LastReason})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (chat_id, event_type, metadata_json, created_at)
		VALUES (?, 'raid_state_changed', json(?), ?)
	`, chatID, string(metadata), now.Unix()); err != nil {
		return false, fmt.Errorf("audit Raid transition: %w", err)
	}
	return true, nil
}

func raidTriggerTx(ctx context.Context, tx *sql.Tx, chatID int64, now time.Time) (bool, string, *time.Time, error) {
	var joins, failures, coordinated, punished int
	var lastSignal sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN kind = 'join' AND created_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'verification_failed' AND created_at >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'coordinated_repeat' AND created_at >= ? THEN 1 ELSE 0 END), 0),
			COUNT(DISTINCT CASE WHEN kind = 'punished_recent' AND created_at >= ? THEN target_id END),
			MAX(created_at)
		FROM raid_signals WHERE chat_id = ?
	`, now.Add(-raidJoinWindow).Unix(), now.Add(-raidSignalWindow).Unix(),
		now.Add(-raidSignalWindow).Unix(), now.Add(-raidSignalWindow).Unix(), chatID).
		Scan(&joins, &failures, &coordinated, &punished, &lastSignal); err != nil {
		return false, "", nil, fmt.Errorf("evaluate Raid signals: %w", err)
	}
	var last *time.Time
	if lastSignal.Valid {
		value := time.Unix(lastSignal.Int64, 0).UTC()
		last = &value
	}
	switch {
	case joins >= raidJoinThreshold:
		return true, "join_volume", last, nil
	case coordinated >= 1:
		return true, "coordinated_repeat", last, nil
	case failures >= raidFailureThreshold:
		return true, "verification_failures", last, nil
	case punished >= raidPunishedThreshold:
		return true, "punished_recent", last, nil
	default:
		return false, "", last, nil
	}
}

func scheduleRaidNoticeTx(ctx context.Context, tx *sql.Tx, state RaidState, actorUserID int64, now time.Time) error {
	reason := state.LastReason
	if reason == "" {
		reason = "raid_state"
	}
	return insertModerationActionTx(ctx, tx, ModerationAction{
		IdempotencyKey: fmt.Sprintf("raid:%d:%d", state.ChatID, state.TransitionSeq),
		ChatID:         state.ChatID, TargetKind: ModerationTargetChat, TargetID: state.ChatID,
		ActorUserID: actorUserID, ActionKind: ModerationRaidNotice,
		Reason: fmt.Sprintf("%s:%s:%t", state.Mode, reason, state.Active),
	}, now)
}

func validRaidMode(mode string) bool {
	return mode == RaidModeAuto || mode == RaidModeForcedOn || mode == RaidModeForcedOff
}

const raidStateSelect = `
	SELECT chat_id, mode, active, entered_at, last_signal_at, last_reason,
	       transition_seq, updated_at FROM raid_states`

func scanRaidState(row rowScanner) (RaidState, error) {
	var state RaidState
	var active int
	var enteredAt, lastSignal sql.NullInt64
	var updatedAt int64
	if err := row.Scan(&state.ChatID, &state.Mode, &active, &enteredAt, &lastSignal,
		&state.LastReason, &state.TransitionSeq, &updatedAt); err != nil {
		return RaidState{}, fmt.Errorf("read Raid state: %w", err)
	}
	state.Active = active == 1
	state.EnteredAt = timePointer(enteredAt)
	state.LastSignalAt = timePointer(lastSignal)
	state.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return state, nil
}
