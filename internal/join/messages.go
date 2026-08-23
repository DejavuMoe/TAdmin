package join

import (
	"fmt"
	"strings"
	"time"

	"tadmin/internal/store"
)

func verificationPrompt(language string) string {
	if language == "en" {
		return "Complete the verification before the deadline."
	}
	return "请在截止时间前完成验证。"
}

func reviewLabels(language string) [4]string {
	if language == "en" {
		return [4]string{"Approve once", "Approve + trust", "Decline", "Ban"}
	}
	return [4]string{"批准一次", "批准并信任", "拒绝", "封禁"}
}

func formatReview(chat store.ManagedChat, claim store.ReviewClaim, session *store.VerificationSession) string {
	item := claim.Case
	review := claim.Review
	name := strings.TrimSpace(item.UserFirstName + " " + item.UserLastName)
	if name == "" {
		name = "-"
	}
	username := "-"
	if item.Username != "" {
		username = "@" + item.Username
	}
	invite := item.InviteSource
	if invite == "" {
		invite = "-"
	}
	invite = inviteLabel(invite, chat.Language)
	sessionStatus := "unavailable"
	sessionExpires := "-"
	attempts := 0
	if session != nil {
		sessionStatus = session.Status
		sessionExpires = formatTime(session.ExpiresAt)
		attempts = session.Attempts
	}
	if chat.Language == "en" {
		if review.Status == store.ReviewCompleted {
			return fmt.Sprintf("TAdmin review completed\nGroup: %s (%d)\nUser: %s / %d / %s\nPath: %s\nInvite source: %s\nDecision: %s\nActor: %s\nHandled: %s",
				chat.Title, chat.ChatID, name, item.UserID, username, pathLabel(item.Path, "en"), invite,
				decisionLabel(review.Decision, "en"), actorLabel(review.ActorUserID, "en"), formatOptionalTime(review.DecidedAt))
		}
		return fmt.Sprintf("TAdmin manual review\nGroup: %s (%d)\nUser: %s / %d / %s\nPath: %s\nInvite source: %s\nRequested: %s\nSession: %s; expires %s\nAttempts: %d\nReason: %s\nReview deadline: %s",
			chat.Title, chat.ChatID, name, item.UserID, username, pathLabel(item.Path, "en"), invite,
			formatTime(item.CreatedAt), statusLabel(sessionStatus, "en"), sessionExpires, attempts, reasonLabel(item.Reason, "en"), formatTime(review.DeadlineAt))
	}
	if review.Status == store.ReviewCompleted {
		return fmt.Sprintf("TAdmin 审核已完成\n来源群：%s（%d）\n用户：%s / %d / %s\n路径：%s\n邀请来源：%s\n决定：%s\n操作者：%s\n处理时间：%s",
			chat.Title, chat.ChatID, name, item.UserID, username, pathLabel(item.Path, "zh-CN"), invite,
			decisionLabel(review.Decision, "zh-CN"), actorLabel(review.ActorUserID, "zh-CN"), formatOptionalTime(review.DecidedAt))
	}
	return fmt.Sprintf("TAdmin 人工审核\n来源群：%s（%d）\n用户：%s / %d / %s\n路径：%s\n邀请来源：%s\n请求时间：%s\n会话：%s；过期 %s\n已用尝试：%d\n原因：%s\n审核截止：%s",
		chat.Title, chat.ChatID, name, item.UserID, username, pathLabel(item.Path, "zh-CN"), invite,
		formatTime(item.CreatedAt), statusLabel(sessionStatus, "zh-CN"), sessionExpires, attempts, reasonLabel(item.Reason, "zh-CN"), formatTime(review.DeadlineAt))
}

func formatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return "-"
	}
	return formatTime(*value)
}

func actorLabel(actor int64, language string) string {
	if actor == 0 {
		if language != "en" {
			return "自动超时"
		}
		return "automatic timeout"
	}
	return fmt.Sprintf("%d", actor)
}

func pathLabel(path, language string) string {
	if language == "en" {
		return map[string]string{store.JoinPathGuard: "Guard query", store.JoinPathRequest: "join request", store.JoinPathDirect: "direct join"}[path]
	}
	return map[string]string{store.JoinPathGuard: "Guard 查询", store.JoinPathRequest: "普通入群申请", store.JoinPathDirect: "直接入群"}[path]
}

func reasonLabel(reason, language string) string {
	if language == "en" {
		return map[string]string{ReasonFailed: "verification failed", ReasonExpired: "verification expired", ReasonUnreachable: "verification entry unavailable"}[reason]
	}
	return map[string]string{ReasonFailed: "验证失败", ReasonExpired: "验证已过期", ReasonUnreachable: "验证入口不可达"}[reason]
}

func statusLabel(status, language string) string {
	if language == "en" {
		return map[string]string{
			store.VerificationChallengeIssued: "challenge issued", store.VerificationPassed: "passed",
			store.VerificationAwaitingReview: "awaiting review", store.VerificationExpired: "expired",
			store.VerificationCancelled: "cancelled", "unavailable": "unavailable",
		}[status]
	}
	return map[string]string{
		store.VerificationChallengeIssued: "验证进行中", store.VerificationPassed: "已通过",
		store.VerificationAwaitingReview: "等待审核", store.VerificationExpired: "已过期",
		store.VerificationCancelled: "已取消", "unavailable": "不可用",
	}[status]
}

func decisionLabel(decision, language string) string {
	if language == "en" {
		return map[string]string{
			store.ReviewDecisionOnce: "approved once", store.ReviewDecisionTrust: "approved and trusted",
			store.ReviewDecisionDeny: "declined", store.ReviewDecisionBan: "banned",
			store.ReviewDecisionTime: "timed out", "left": "user left",
		}[decision]
	}
	return map[string]string{
		store.ReviewDecisionOnce: "批准一次", store.ReviewDecisionTrust: "批准并信任",
		store.ReviewDecisionDeny: "拒绝", store.ReviewDecisionBan: "封禁",
		store.ReviewDecisionTime: "审核超时", "left": "用户已离群",
	}[decision]
}

func inviteLabel(invite, language string) string {
	if strings.HasPrefix(invite, "name:") {
		if language == "en" {
			return "named invite: " + strings.TrimPrefix(invite, "name:")
		}
		return "命名邀请：" + strings.TrimPrefix(invite, "name:")
	}
	if strings.HasPrefix(invite, "creator:") {
		if language == "en" {
			return "creator ID " + strings.TrimPrefix(invite, "creator:")
		}
		return "创建者 ID " + strings.TrimPrefix(invite, "creator:")
	}
	if language == "en" {
		if value := map[string]string{"named": "named invite", "primary": "primary invite", "unnamed": "unnamed invite"}[invite]; value != "" {
			return value
		}
		return invite
	}
	if value := map[string]string{"named": "命名邀请", "primary": "主邀请链接", "unnamed": "未命名邀请"}[invite]; value != "" {
		return value
	}
	return invite
}

func callbackMessage(language, key string) string {
	if language == "en" {
		return map[string]string{
			"identity":     "Cannot verify the administrator identity.",
			"location":     "This review button is not valid here.",
			"unauthorized": "Only a current source-group administrator can review this request.",
			"processed":    "This review has already been handled.",
			"preparing":    "This review is still being prepared. Try again shortly.",
			"processing":   "Processing…",
		}[key]
	}
	return map[string]string{
		"identity":     "无法确认管理员身份。",
		"location":     "此审核按钮不能在这里使用。",
		"unauthorized": "仅来源群的当前管理员可以处理此审核。",
		"processed":    "此审核已经处理。",
		"preparing":    "审核仍在准备中，请稍后重试。",
		"processing":   "正在处理…",
	}[key]
}
