package invite

import (
	"fmt"
	"strings"
	"time"

	"tadmin/internal/store"
)

func preflightMessage(language, publicID, name string) string {
	if language == "en" {
		return fmt.Sprintf("TAdmin invite check\nGroup invite %s (%s) is about to be created. The secret link will follow in this private chat.", publicID, name)
	}
	return fmt.Sprintf("TAdmin 邀请链接检查\n即将创建群邀请 %s（%s）。完整链接随后只会发送到此私聊。", publicID, name)
}

func secretMessage(language, publicID, name string) string {
	if language == "en" {
		return fmt.Sprintf("TAdmin invite %s (%s)\nKeep this join-request link private:", publicID, name)
	}
	return fmt.Sprintf("TAdmin 邀请 %s（%s）\n请妥善保管以下入群申请链接：", publicID, name)
}

func CreateResultMessage(language string, result OperationResult) string {
	if language == "en" {
		switch result.Code {
		case ResultDelivered:
			return fmt.Sprintf("Invite %s (%s) was created and sent to the owner in private chat.", result.PublicID, result.Name)
		case ResultExisting:
			return fmt.Sprintf("Invite request %s was already handled; no link was created or sent again.", result.PublicID)
		case ResultPreflightFailed:
			return "Invite creation stopped because the bot cannot privately message the owner. Start the bot in private chat and try a new command."
		case ResultCreateRejected:
			return fmt.Sprintf("Telegram rejected invite request %s; no link was created. Check the group and try a new command.", result.PublicID)
		case ResultPermissionDenied:
			return fmt.Sprintf("Telegram rejected invite request %s because the bot lacks invite-link administrator rights.", result.PublicID)
		case ResultDeliveryFailedRevoked:
			return fmt.Sprintf("Invite %s could not be delivered privately and was revoked.", result.PublicID)
		case ResultRevocationPending:
			return fmt.Sprintf("Invite %s could not be delivered; revocation is pending and will be retried safely.", result.PublicID)
		case ResultCreateIndeterminate:
			return fmt.Sprintf("Invite request %s has an indeterminate Telegram result and will not be replayed automatically. Check Telegram before trying again.", result.PublicID)
		}
		return "The invite operation failed without exposing the invite link."
	}
	switch result.Code {
	case ResultDelivered:
		return fmt.Sprintf("邀请 %s（%s）已创建，完整链接已私聊发送给全局所有者。", result.PublicID, result.Name)
	case ResultExisting:
		return fmt.Sprintf("邀请请求 %s 已处理，不会再次创建或发送链接。", result.PublicID)
	case ResultPreflightFailed:
		return "机器人无法私聊全局所有者，已停止创建。请先在私聊中启动机器人，再发送一条新的创建命令。"
	case ResultCreateRejected:
		return fmt.Sprintf("Telegram 已拒绝邀请请求 %s，未创建链接。请核对群组后重新发送命令。", result.PublicID)
	case ResultPermissionDenied:
		return fmt.Sprintf("Telegram 已拒绝邀请请求 %s：机器人缺少管理邀请链接的管理员权限。", result.PublicID)
	case ResultDeliveryFailedRevoked:
		return fmt.Sprintf("邀请 %s 无法通过私聊交付，现已撤销。", result.PublicID)
	case ResultRevocationPending:
		return fmt.Sprintf("邀请 %s 无法交付，撤销正在等待安全重试。", result.PublicID)
	case ResultCreateIndeterminate:
		return fmt.Sprintf("邀请请求 %s 的 Telegram 结果不确定，不会自动重放。再次操作前请先在 Telegram 中核对。", result.PublicID)
	}
	return "邀请操作失败，未泄露邀请链接。"
}

func RevokeResultMessage(language string, result OperationResult) string {
	if language == "en" {
		switch result.Code {
		case ResultRevoked:
			return fmt.Sprintf("Invite %s was revoked.", result.PublicID)
		case ResultAlreadyRevoked:
			return fmt.Sprintf("Invite %s is already revoked; no duplicate action was sent.", result.PublicID)
		case ResultRevocationPending:
			return fmt.Sprintf("Revocation of invite %s is queued for safe retry.", result.PublicID)
		case ResultDecryptFailed:
			return fmt.Sprintf("Invite %s cannot be decrypted with the configured Bot Token. Correct a Token configuration mistake, or revoke it manually in Telegram after a real rotation.", result.PublicID)
		case ResultPermissionDenied:
			return fmt.Sprintf("Telegram refused to revoke invite %s because the bot lacks invite-link administrator rights; the revocation remains pending.", result.PublicID)
		case ResultUnavailable:
			return fmt.Sprintf("Invite %s is not available in this group or cannot be revoked from its current state.", result.PublicID)
		}
		return "The invite revocation failed without exposing the invite link."
	}
	switch result.Code {
	case ResultRevoked:
		return fmt.Sprintf("邀请 %s 已撤销。", result.PublicID)
	case ResultAlreadyRevoked:
		return fmt.Sprintf("邀请 %s 已处于撤销状态，没有重复调用 Telegram。", result.PublicID)
	case ResultRevocationPending:
		return fmt.Sprintf("邀请 %s 的撤销已进入安全重试队列。", result.PublicID)
	case ResultDecryptFailed:
		return fmt.Sprintf("当前配置的 Bot Token 无法解密邀请 %s。若是配置错误请改回仍有效的匹配 Token；若已真实轮换，请在 Telegram 中人工撤销。", result.PublicID)
	case ResultPermissionDenied:
		return fmt.Sprintf("Telegram 拒绝撤销邀请 %s：机器人缺少管理邀请链接的管理员权限；撤销仍处于待处理状态。", result.PublicID)
	case ResultUnavailable:
		return fmt.Sprintf("邀请 %s 不属于当前群、并不存在，或当前状态不可撤销。", result.PublicID)
	}
	return "邀请撤销失败，未泄露邀请链接。"
}

func ListMessage(language string, summaries []store.InviteSummary) string {
	if len(summaries) == 0 {
		if language == "en" {
			return "No managed invite links exist for this group."
		}
		return "此群还没有由 TAdmin 管理的邀请链接。"
	}
	var builder strings.Builder
	if language == "en" {
		builder.WriteString("TAdmin invite links")
	} else {
		builder.WriteString("TAdmin 邀请链接")
	}
	for _, summary := range summaries {
		builder.WriteString("\n")
		builder.WriteString(summary.PublicID)
		builder.WriteString(" · ")
		builder.WriteString(summary.DisplayName)
		builder.WriteString(" · ")
		builder.WriteString(summary.Status)
		builder.WriteString(" · ")
		builder.WriteString(summary.CreatedAt.UTC().Format(time.RFC3339))
	}
	return builder.String()
}

func Usage(language string) string {
	if language == "en" {
		return "Usage: /tadmin invite create <name>; /tadmin invite list; /tadmin invite revoke <public_id>."
	}
	return "用法：/tadmin invite create <名称>；/tadmin invite list；/tadmin invite revoke <public_id>。"
}
