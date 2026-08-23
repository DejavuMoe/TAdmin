package moderation

import (
	"fmt"
	"strings"
	"time"

	"tadmin/internal/store"
)

func moderationNotice(language, action, targetKind string, targetID int64, until *time.Time) string {
	targetLabel := fmt.Sprintf("user %d", targetID)
	zhTargetLabel := fmt.Sprintf("用户 %d", targetID)
	if targetKind == store.ModerationTargetSenderChat {
		targetLabel = fmt.Sprintf("sender chat %d", targetID)
		zhTargetLabel = fmt.Sprintf("发送者频道 %d", targetID)
	}
	if language == "en" {
		switch action {
		case store.ModerationDeleteWarn, store.ModerationWarn:
			return fmt.Sprintf("TAdmin: %s has been warned.", targetLabel)
		case store.ModerationDeleteMute, store.ModerationMute:
			return fmt.Sprintf("TAdmin: %s has been muted until %s.", targetLabel, formatOptionalTime(until))
		case store.ModerationKick:
			return fmt.Sprintf("TAdmin: %s has been removed and may rejoin.", targetLabel)
		case store.ModerationDeleteBan, store.ModerationBan:
			return fmt.Sprintf("TAdmin: %s has been banned.", targetLabel)
		}
	}
	switch action {
	case store.ModerationDeleteWarn, store.ModerationWarn:
		return fmt.Sprintf("TAdmin：已警告%s。", zhTargetLabel)
	case store.ModerationDeleteMute, store.ModerationMute:
		return fmt.Sprintf("TAdmin：已禁言%s，至 %s。", zhTargetLabel, formatOptionalTime(until))
	case store.ModerationKick:
		return fmt.Sprintf("TAdmin：已移除%s，允许重新加入。", zhTargetLabel)
	case store.ModerationDeleteBan, store.ModerationBan:
		return fmt.Sprintf("TAdmin：已封禁%s。", zhTargetLabel)
	}
	return ""
}

func moderationLog(language string, action store.ModerationAction) string {
	rules := "-"
	if len(action.RuleIDs) > 0 {
		rules = strings.Join(action.RuleIDs, ", ")
	}
	domains := "-"
	if len(action.Domains) > 0 {
		domains = strings.Join(action.Domains, ", ")
	}
	if language == "en" {
		return fmt.Sprintf("TAdmin moderation\nTarget: %s/%d\nMessage: %d\nAction: %s\nResult: %s\nReason: %s\nScore: %d\nRules: %s\nDomains: %s\nTime: %s",
			action.TargetKind, action.TargetID, action.MessageID, sourceAction(action),
			actionResult(action), actionReason(action), action.Score, rules, domains,
			action.UpdatedAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("TAdmin 管理记录\n目标：%s/%d\n消息：%d\n动作：%s\n结果：%s\n原因：%s\n分数：%d\n规则：%s\n域名：%s\n时间：%s",
		action.TargetKind, action.TargetID, action.MessageID, sourceAction(action),
		actionResult(action), actionReason(action), action.Score, rules, domains,
		action.UpdatedAt.UTC().Format(time.RFC3339))
}

func raidNotice(language string, action store.ModerationAction) string {
	parts := strings.Split(action.Reason, ":")
	mode, reason, active := "auto", "raid_state", false
	if len(parts) >= 3 {
		mode, reason, active = parts[0], parts[1], parts[2] == "true"
	}
	if language == "en" {
		return fmt.Sprintf("TAdmin Raid: mode=%s, active=%t, reason=%s, time=%s",
			mode, active, reason, action.UpdatedAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("TAdmin Raid：模式=%s，启用=%t，原因=%s，时间=%s",
		mode, active, reason, action.UpdatedAt.UTC().Format(time.RFC3339))
}

func sourceAction(action store.ModerationAction) string {
	if source, _, _, ok := derivedActionParts(action); ok {
		return source
	}
	return action.ActionKind
}

func actionResult(action store.ModerationAction) string {
	if _, _, result, ok := derivedActionParts(action); ok {
		return result
	}
	if action.Result != "" {
		return action.Result
	}
	return "recorded"
}

func actionReason(action store.ModerationAction) string {
	if _, reason, _, ok := derivedActionParts(action); ok {
		return reason
	}
	return action.Reason
}

func derivedActionParts(action store.ModerationAction) (string, string, string, bool) {
	if action.ActionKind != store.ModerationNotify && action.ActionKind != store.ModerationLog {
		return "", "", "", false
	}
	first := strings.IndexByte(action.Reason, ':')
	last := strings.LastIndexByte(action.Reason, ':')
	if first <= 0 || last <= first {
		return "", "", "", false
	}
	return action.Reason[:first], action.Reason[first+1 : last], action.Reason[last+1:], true
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}
