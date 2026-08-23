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
			Start:               "TAdmin is a private, self-hosted Telegram group verification and moderation bot.\nVersion: %s",
			Unauthorized:        "Access denied: this command is restricted to the TAdmin owner.",
			Usage:               "Usage: /tadmin status|setup|invite, or a supported moderation command.",
			SetupSupergroupOnly: "This check can only be run inside a Telegram supergroup.",
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
			SetupManagement:     "Configure a forum-enabled management supergroup before setup.",
			SetupManagementPerm: "The bot must be an administrator that can manage topics in the management group.",
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
		Start:               "TAdmin 是一个私有、自托管的 Telegram 群验证与基础群管机器人。\n版本：%s",
		Unauthorized:        "拒绝访问：此命令仅限 TAdmin 全局所有者执行。",
		Usage:               "用法：/tadmin status|setup|invite，或受支持的群管命令。",
		SetupSupergroupOnly: "此检查只能在 Telegram 超级群中执行。",
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
		SetupManagement:     "接入前请配置已启用话题的管理超级群。",
		SetupManagementPerm: "机器人必须是管理群管理员，并具有管理话题权限。",
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
