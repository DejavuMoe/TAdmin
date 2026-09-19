package i18n

type Messages struct {
	Start               string
	Unauthorized        string
	Usage               string
	SetupSupergroupOnly string
	SetupCheckFailed    string
	SetupTitle          string
	BotAdministrator    string
	CanInviteUsers      string
	CanRestrictMembers  string
	CanDeleteMessages   string
	JoinRequestQueries  string
	SetupNotEnabled     string
	SetupEnabled        string
	SetupPublicURL      string
	SetupManagement     string
	SetupManagementPerm string
	StartVerifyInvalid  string
	StartVerifyWrong    string
	StatusTitle         string
	Version             string
	Uptime              string
	Database            string
	ManagedChats        string
	Yes                 string
	No                  string
	Healthy             string
	Unhealthy           string
}

func For(language string) Messages {
	if language == "en" {
		return Messages{
			Start:               "TAdmin is a private, self-hosted Telegram group admission verification bot.\nVersion: %s",
			Unauthorized:        "Access denied: this command is restricted to the TAdmin owner.",
			Usage:               "Private: /tadmin status; /tadmin groups; /tadmin setup <chat_id>; /tadmin timeout <chat_id> <30-900 seconds>. In a source group: /tadmin setup or /tadmin timeout <seconds>.",
			SetupSupergroupOnly: "The source must be a supergroup without forum topics.",
			SetupCheckFailed:    "The permission check failed. See the service log for details.",
			SetupTitle:          "TAdmin permission check",
			BotAdministrator:    "Bot administrator",
			CanInviteUsers:      "Invite and join-request permission",
			CanRestrictMembers:  "Restrict members permission",
			CanDeleteMessages:   "Delete messages permission",
			JoinRequestQueries:  "Join Request Query capability",
			SetupNotEnabled:     "The group was not enabled.",
			SetupEnabled:        "TAdmin verification is enabled for this group.",
			SetupPublicURL:      "Configure an absolute HTTPS public_base_url before setup.",
			SetupManagement:     "The optional review destination must be an accessible group without forum topics. Leave management_chat_id unset to use the owner private chat.",
			SetupManagementPerm: "Cannot send/edit in the review destination. In the default mode, start the bot in your private chat and make sure it is not blocked.",
			StartVerifyInvalid:  "This verification link is invalid or has expired.",
			StartVerifyWrong:    "This verification link belongs to another user.",
			StatusTitle:         "TAdmin status",
			Version:             "Version",
			Uptime:              "Uptime",
			Database:            "Database",
			ManagedChats:        "Managed groups",
			Yes:                 "yes",
			No:                  "no",
			Healthy:             "healthy",
			Unhealthy:           "unhealthy",
		}
	}

	return Messages{
		Start:               "TAdmin 是一个私有、自托管的 Telegram 入群验证机器人。\n版本：%s",
		Unauthorized:        "拒绝访问：此命令仅限 TAdmin 全局所有者执行。",
		Usage:               "私聊：/tadmin status；/tadmin groups；/tadmin setup <群ID>；/tadmin timeout <群ID> <30-900秒>。来源群：/tadmin setup 或 /tadmin timeout <秒数>。",
		SetupSupergroupOnly: "来源群必须是无话题超级群。",
		SetupCheckFailed:    "权限检查失败，请查看服务日志了解详情。",
		SetupTitle:          "TAdmin 权限检查",
		BotAdministrator:    "机器人管理员",
		CanInviteUsers:      "邀请与入群申请权限",
		CanRestrictMembers:  "限制成员权限",
		CanDeleteMessages:   "删除消息权限",
		JoinRequestQueries:  "Join Request Query 能力",
		SetupNotEnabled:     "未启用此群组。",
		SetupEnabled:        "此群组已启用 TAdmin 验证。",
		SetupPublicURL:      "接入前请配置绝对 HTTPS public_base_url。",
		SetupManagement:     "可选的审核目的地必须是机器人可访问的无话题群；不配置 management_chat_id 即使用所有者私聊。",
		SetupManagementPerm: "无法向审核目的地发送或编辑消息。默认模式下，请先私聊机器人点击 Start，并确保没有屏蔽机器人。",
		StartVerifyInvalid:  "验证链接无效或已过期。",
		StartVerifyWrong:    "此验证链接属于其他用户。",
		StatusTitle:         "TAdmin 状态",
		Version:             "版本",
		Uptime:              "运行时间",
		Database:            "数据库",
		ManagedChats:        "已管理群组",
		Yes:                 "是",
		No:                  "否",
		Healthy:             "正常",
		Unhealthy:           "异常",
	}
}
