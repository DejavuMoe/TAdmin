package i18n

func init() {
	add(map[string]pair{
		"cap_head_join":    {"🔐 <b>%s</b> · 入群验证", "🔐 <b>%s</b> · Join verification"},
		"cap_head_request": {"🔐 你正在申请加入 <b>%s</b>，请先完成验证", "🔐 You asked to join <b>%s</b>. Please verify first"},
		"cap_comma":        {"，", ", "},
		"cap_footer":       {"⏳ 剩余 %s · 机会 %d/%d", "⏳ %s left · %d/%d attempts"},
		"cap_wrong_note":   {"❌ 不对哦，已换一题，再仔细看看～", "❌ Not quite — here is a new one, take another look."},

		"prompt_overlay": {"👋 欢迎 %s 加入 <b>%s</b>！\n请点击下方按钮完成人机验证（只有你能看到题目），限时 %s。",
			"👋 Welcome %s to <b>%s</b>!\nTap the button below to verify you are human (only you can see the challenge). Time limit: %s."},
		"prompt_private": {"👋 欢迎 %s 加入 <b>%s</b>！\n请点击下方按钮，在私聊中完成人机验证，限时 %s。",
			"👋 Welcome %s to <b>%s</b>!\nTap the button below and verify in a private chat. Time limit: %s."},

		"btn_start":       {"🔐 开始验证", "🔐 Verify now"},
		"btn_go_private":  {"🔐 前往私聊验证", "🔐 Verify in private chat"},
		"btn_reset":       {"↺ 重选", "↺ Reset"},
		"btn_help":        {"🆘 需要帮助", "🆘 Need help"},
		"btn_webapp":      {"🖼 打开大图验证", "🖼 Open large view"},
		"btn_approve":     {"✅ 放行", "✅ Approve"},
		"btn_reject":      {"🚫 移除", "🚫 Remove"},
		"btn_ban":         {"⛔ 封禁", "⛔ Ban"},
		"btn_check_perms": {"🔍 检查权限", "🔍 Check permissions"},

		"cb_gone":       {"这个验证已经结束了", "This verification has ended"},
		"cb_not_yours":  {"这不是你的验证哦 🙂", "This verification is not for you 🙂"},
		"cb_in_review":  {"已提交管理员审核，请耐心等待", "Waiting for an administrator"},
		"cb_expired":    {"验证已超时", "Verification expired"},
		"cb_help_sent":  {"已通知管理员，请耐心等待处理。", "Administrators have been notified. Please wait."},
		"cb_stale":      {"题目已更新", "The challenge was updated"},
		"cb_passed":     {"✅ 验证通过，欢迎！", "✅ Verified — welcome!"},
		"cb_failed":     {"验证未通过，稍后可以再试。", "Verification failed. You can try again later."},
		"cb_wrong":      {"不对哦，还剩 %d 次机会", "Wrong answer — %d attempts left"},
		"cb_no_need":    {"你当前没有需要完成的验证 👍", "You have nothing to verify 👍"},
		"cb_admin_only": {"只有本群管理员可以操作", "Only group administrators can do this"},
		"cb_done":       {"已处理", "Done"},

		"user_review":       {"⏳ 已提交管理员审核，请耐心等待。", "⏳ Sent to the administrators for review. Please wait."},
		"user_pass_join":    {"✅ 验证通过！欢迎加入 <b>%s</b>，现在可以发言了。", "✅ Verified! Welcome to <b>%s</b> — you can chat now."},
		"user_pass_request": {"✅ 验证通过，入群申请已批准，欢迎加入 <b>%s</b>！", "✅ Verified — your request to join <b>%s</b> was approved!"},
		"user_fail":         {"❌ 未能通过 <b>%s</b> 的入群验证。稍后可以重新申请或加入。", "❌ You did not pass verification for <b>%s</b>. You can try again later."},
		"user_closed":       {"这个验证已经结束。", "This verification has ended."},

		"pm_invalid":   {"验证链接无效或已过期。请回到群里重新加入。", "This verification link is invalid or expired. Please rejoin the group."},
		"pm_not_yours": {"这个验证链接不属于你。", "This verification link belongs to someone else."},
		"pm_closed":    {"这个验证已经结束了。", "This verification has already ended."},

		"review_head":     {"🧑‍⚖️ <b>入群待审核</b> · %s", "🧑‍⚖️ <b>Admission review</b> · %s"},
		"review_user":     {"👤 %s（<code>%d</code>）", "👤 %s (<code>%d</code>)"},
		"review_source":   {"🚪 来源：%s", "🚪 Source: %s"},
		"review_reason":   {"📝 原因：%s", "📝 Reason: %s"},
		"review_attempts": {"🎯 已尝试：%d/%d", "🎯 Attempts: %d/%d"},
		"review_risk":     {"🤖 AI 资料风险：%d%%（%s）", "🤖 AI profile risk: %d%% (%s)"},
		"review_deadline": {"⏳ %s 内无人处理将自动移除", "⏳ Auto-removal in %s if nobody decides"},

		"source_join":    {"直接入群", "direct join"},
		"source_request": {"入群申请", "join request"},
		"source_guard":   {"入群申请（Guard）", "join request (Guard)"},

		"reason_help":          {"用户请求人工协助", "the user asked for help"},
		"reason_failed":        {"验证次数用尽", "attempts exhausted"},
		"reason_risk":          {"已答对，但 AI 判定资料高风险", "solved, but the AI flagged the profile as high risk"},
		"reason_unreachable":   {"无法私聊申请者", "cannot message the applicant"},
		"reason_undeliverable": {"验证题发送失败", "the challenge could not be delivered"},

		"actor_owner":    {"机器人所有者", "the bot owner"},
		"result_by":      {"👮 处理人：%s", "👮 By: %s"},
		"result_pass":    {"✅ %s 已通过验证", "✅ %s passed verification"},
		"result_approve": {"✅ %s 已被放行", "✅ %s was approved"},
		"result_fail":    {"❌ %s 未通过验证，已移除", "❌ %s failed verification and was removed"},
		"result_timeout": {"⌛ %s 验证超时，已移除", "⌛ %s timed out and was removed"},
		"result_reject":  {"🚫 %s 已被移除", "🚫 %s was removed"},
		"result_ban":     {"⛔ %s 已被封禁", "⛔ %s was banned"},
		"result_left":    {"👋 %s 已自行离开", "👋 %s left"},
		"result_cas":     {"🛡 %s 在 CAS 黑名单中，已封禁 30 天", "🛡 %s is CAS-listed and was banned for 30 days"},
		"result_cancel":  {"➖ %s 的验证已取消", "➖ Verification of %s was cancelled"},

		"welcome_default": {"🎉 欢迎 {mention} 加入 <b>{group}</b>！", "🎉 Welcome {mention} to <b>{group}</b>!"},

		"bot_kicked": {"🤖 已移除由非管理员 %s 拉入的机器人 %s。", "🤖 Removed bot %[2]s added by non-admin %[1]s."},
		"raid_owner": {"🛡 <b>%s</b> 触发突击防护：1 分钟内 %d 人入群。\n已暂停逐个公开提示并缩短验证时限，%d 分钟后自动解除。",
			"🛡 Raid protection in <b>%s</b>: %d joins within a minute.\nPer-user public prompts are paused and time limits shortened for %d minutes."},
		"raid_lift":   {"✅ 立即解除防护", "✅ Lift protection now"},
		"raid_notice": {"🛡 <b>突击防护中</b>\n最近入群的新成员请点击下方按钮完成验证，题目只有你自己能看到。", "🛡 <b>Raid protection active</b>\nNew members: tap below to verify. Only you can see your challenge."},

		"perm_problem": {"⚠️ 在 <b>%s</b> 执行 <code>%s</code> 失败：%s\n请检查机器人是否仍是管理员并拥有所需权限。",
			"⚠️ Action <code>%[2]s</code> failed in <b>%[1]s</b>: %[3]s\nCheck that the bot is still an administrator with the required rights."},
	})
}
