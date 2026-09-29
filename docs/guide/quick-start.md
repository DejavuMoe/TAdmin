# 五分钟上手

整个过程不需要域名，也不需要编辑任何配置文件。

<Steps>

1. **创建机器人，拿到两个值**

   - 找 [@BotFather](https://t.me/BotFather) 发送 `/newbot`，得到 **Bot Token**（形如 `123456:ABC-DEF…`）。
   - 找 [@userinfobot](https://t.me/userinfobot)，得到你自己的 **数字 ID**——你将是这个机器人的“所有者”。

2. **启动 TAdmin**

   ::: code-group

   ```sh [Docker Compose]
   cp tadmin.env.example tadmin.env
   # 编辑 tadmin.env：填写 TADMIN_BOT_TOKEN 与 TADMIN_OWNER_ID
   docker compose up -d
   docker compose logs -f
   ```

   ```sh [单个二进制]
   make build
   TADMIN_BOT_TOKEN=123456:ABC... \
   TADMIN_OWNER_ID=123456789 \
   TADMIN_TIMEZONE=Asia/Shanghai \
   ./bin/tadmin
   ```

   ```sh [离线自检]
   # 只检查配置与数据库，不连接 Telegram
   TADMIN_BOT_TOKEN=123456:ABC... TADMIN_OWNER_ID=123456789 ./bin/tadmin check
   ```

   :::

   启动后机器人会私聊你一条“TAdmin 已上线”。如果日志提示 *cannot message the owner yet*，先在 Telegram 里给机器人发一次 `/start`。

3. **把机器人拉进群并设为管理员**

   需要的管理员权限：**封禁用户**（必需）、**删除消息**、**置顶消息**、**邀请用户**。控制台首页的“➕ 添加到群组”按钮会预先勾好这些权限。

   机器人进群后会立刻私聊你一张权限检查卡片：

   > ✅ 已接入群组 **开源爱好者交流群**
   > ✅ 超级群组　✅ 机器人是管理员　✅ 封禁/限制用户（必需）　✅ 删除消息　✅ 置顶消息　✅ 邀请用户
   > 默认已开启：浮层验证（题目仅新人可见）· 图形点选 · 180 秒 · 3 次机会 · 递增冷却 · 突击防护。

4. **打开控制台看看**

   私聊发送 `/start`。在 **👥 群组管理 → 你的群 → 🎮 试玩验证题** 里亲手体验新成员会看到的题目，然后按需调整投递方式、题型和时限。

</Steps>

::: warning 频道讨论群
如果这是频道的讨论群，请在群设置中开启 **“加入后才能发言”**。否则评论者无需入群就能发言，任何入群验证都拦不住他们。权限检查会提醒你。
:::

## 可选增强

| 想要 | 需要 | 文档 |
|---|---|---|
| Mini App 大图验证、Guard 零窗口入群 | 一个 HTTPS 域名与反向代理 | [部署 → Mini App](./deployment#可选-mini-app-与-guard) |
| 链接预览刷新 | 一个 Telegram 用户号（建议小号）与 api_id / api_hash | [预览刷新](/features/preview) |
| AI 入群资料风险提示 | TypeSafe API Key | [防护与安全](/features/protection#ai-入群资料风险提示) |
