# 🛡 TAdmin

**为个人自托管打造的 Telegram 群管家。** 一个静态二进制 + 一个 SQLite 文件，填入 Bot Token 和你的 ID 就能跑。
所有设置都在机器人私聊里点按钮完成——不用记命令，不用后台网站，不用数据库服务。

> 它只守门，不审查：新成员入群要过人机验证；已经在群里的朋友怎么聊天，TAdmin 一概不看、不评分、不处罚。

---

## ✨ 亮点

### 🔐 入群验证，干净又难绕过
- **浮层验证（默认）**：群里只出现一行“欢迎 @新人”，验证题以 **仅新人本人可见** 的临时消息弹出（Bot API 10.2 Ephemeral Messages）。其他群友看不到题目刷屏，验证结束提示自动消失。
  临时消息不保证送达，所以新人点“开始验证”时，题目会用 `callback_query_id` 直接覆盖在提示消息上，**保证送达当前客户端**；接口异常时自动回退到私聊。
- **三种题型**：
  - 🧩 **图形点选**（默认）：3×3 抗锯齿图卡，按顺序点出“三角形 7 → 圆形 2 → 星形 5”。每个目标的**数字和形状都至少在另一格重复出现**，只会识别数字或只会识别形状的脚本都找不准；盲猜概率 1/504。
  - 🔢 **图片算术**：扭曲渲染的算式，6 选 1。
  - 👆 **一键确认**：最宽松，只拦截完全不交互的脚本。
- **多种入口**：浮层 / 群内公开 / 私聊 / Mini App 大图页面 / **入群申请**（审批制群零“未禁言窗口”）/ **Guard 查询**（Bot API 10.1，10 秒内弹出 Mini App）。
- **不永久误伤**：失败或超时只是移出，**冷却时间递增**（2 → 8 → 32 → 128 分钟…封顶 24 小时），永久封禁只由管理员手动执行。
- **尊重管理员**：入群前已有的个人限制在通过后原样恢复；管理员拉进来的人、名称含 `#免验证` 的邀请链接、入群申请已批准的人自动免验证；验证途中被升为管理员自动取消。
- **人工兜底**：看不清图片可以点“🆘 需要帮助”，转给群管理员一键放行 / 移除 / 封禁；也可以设置“答错用尽转人工”。
- **突击防护**：1 分钟内入群超过阈值自动进入防护模式——停止逐个公开提示、缩短时限、只发一条共享的“开始验证”按钮，并私聊通知你一键解除。
- **可选增强**：CAS 公共黑名单联防；**AI 入群资料风险提示**（TypeSafe Jev，5 个并行判断），只会让流程更严格，绝不自动放行或封禁。
- **拦截机器人**：非管理员拉进来的机器人账号自动移除。

### 📢 频道联动
- 关联频道新帖在讨论群里的自动置顶：**保持 / 立即取消 / 只保留最新一条**。
- **一键清理历史频道置顶，保留管理员自己的置顶**：先处理记录在案的频道转发，再循环检查当前最上层置顶，只要来自频道就取消，遇到管理员置顶即停。
- 拦截“频道马甲”：以其他频道身份发言会被删除并封禁该频道身份（关联频道与匿名管理员除外）。这是身份规则，不是内容审查。

### 🔄 链接预览刷新
Telegram 只允许通过官方 @WebpageBot 强制刷新链接预览，而机器人之间不能互发消息，所以 TAdmin 可以挂一个 **用户号**（建议小号）专门做这件事：
- 私聊把任意链接发给机器人 → 🔄 一键刷新，结果当场回显。
- 添加 **sitemap / RSS / Atom / JSON Feed**，或者直接给网站首页让它自动发现（robots.txt、`<link rel=alternate>`、常见路径）。
- 周期性条件请求检查，发现新文章或 `lastmod` 变化就进入 **慢速队列**：默认 45 秒 ± 抖动一条、每日上限、24 小时去重、自动遵守 FLOOD_WAIT，批次完成后只给你发一条摘要。
- 用户号登录全程在私聊里完成：**📷 扫码登录**，或手机号 + **内联数字键盘**输入验证码（验证码不以消息形式发送，避免被 Telegram 判定“验证码外泄”），两步验证密码读取后立即删除。

### 🧰 日常好用
- 👋 **欢迎语**：保留 Telegram 原生格式、支持占位符和链接按钮，可设为 **仅新人可见**，定时自动删除。
- 🧹 **服务消息清理**：入群、退群、置顶、改名/头像/通话提示，管理员命令与回复。
- 👮 **管理员回复命令**：`/pass` `/kick` `/ban 7d` `/mute 2h` `/unmute` `/unban` `/unpin`，`/settings` 把本群设置面板发到你私聊。
- 📊 **统计**：14 天迷你柱状图、通过率、中位答题用时、各群对比。
- 🎮 **试玩验证题**：在控制台里亲手体验每种题型。
- 💾 **一键备份**：控制台点一下，数据库快照直接发到你的私聊。
- 👥 **多管理员**：群管理员也能管理自己所在的群（但看不到别的群和系统设置）；别人把机器人拉进群时需要你授权。
- 🌐 中文 / English，按群设置。

---

## 🚀 30 秒上手

1. 找 [@BotFather](https://t.me/BotFather) 创建机器人，拿到 Token；找 [@userinfobot](https://t.me/userinfobot) 查到你的数字 ID。
2. 启动（Docker）：

   ```sh
   cp tadmin.env.example tadmin.env   # 填入 TADMIN_BOT_TOKEN 与 TADMIN_OWNER_ID
   docker compose up -d
   ```

   或者用单个二进制：

   ```sh
   make build
   TADMIN_BOT_TOKEN=123456:ABC... TADMIN_OWNER_ID=123456789 ./bin/tadmin
   ```

3. 在 Telegram 里给机器人发 `/start`，然后把它拉进群并设为管理员（开启 **封禁用户**、**删除消息**、**置顶消息**、**邀请用户**）。
   机器人会立刻私聊你一张权限检查卡片，之后就开始守门了。

就这些。不需要域名、不需要反向代理、不需要改任何配置文件。

---

## ⚙️ 配置

全部配置都可以用环境变量完成；也可以写 `config.toml`（`tadmin sample-config` 生成带注释的模板，见 [config.example.toml](config.example.toml)）。环境变量优先。

| 环境变量 | 说明 |
|---|---|
| `TADMIN_BOT_TOKEN` / `TADMIN_BOT_TOKEN_FILE` | **必填**，Bot Token（或存放 Token 的文件，适合 Docker secrets / systemd credentials） |
| `TADMIN_OWNER_ID` | **必填**，你的 Telegram 数字 ID |
| `TADMIN_DATA_DIR` | 数据目录，默认 `data`（Docker 镜像内为 `/data`） |
| `TADMIN_TIMEZONE` | 统计与日期使用的时区，如 `Asia/Shanghai` |
| `TADMIN_LANGUAGE` | 默认语言 `zh-CN` / `en` |
| `TADMIN_PUBLIC_URL` | 可选。HTTPS 源（如 `https://verify.example.com`），启用 Mini App 大图验证与 Guard |
| `TADMIN_LISTEN` | HTTP 监听地址，默认 `127.0.0.1:8080`（`/healthz` 与 Mini App） |
| `TADMIN_API_ID` / `TADMIN_API_HASH` | 可选。预览刷新用户号的 API 凭据（[my.telegram.org](https://my.telegram.org)） |
| `TADMIN_TYPESAFE_API_KEY` | 可选。AI 入群资料风险提示 |

预览刷新的节奏（检查间隔、最小间隔、每日上限）在 `config.toml` 的 `[preview]` 中调整。

每个群的行为（验证方式、题型、时限、欢迎语、频道置顶、清理、防护……）都在私聊控制台里设置，存在数据库里，改完立即生效。

---

## 🧭 命令

```text
tadmin [run]        启动（默认）
tadmin check        检查配置与数据库，不连接 Telegram
tadmin backup out.db  在线一致性备份（服务运行时也可以执行）
tadmin healthcheck  探测本地 /healthz（容器健康检查）
tadmin sample-config  输出带注释的配置模板
tadmin version
```

机器人私聊：`/start` `/panel` `/refresh 链接` `/backup` `/help`
群内管理员：`/settings` `/status` `/pass` `/kick` `/ban [时长]` `/mute [时长]` `/unmute` `/unban` `/unpin`

---

## 🏗 设计

- **可恢复**：每个入群流程是持久化状态机（`new → challenge → review → resolving → closed`），任何时刻重启都从断点继续；外部动作幂等、指数退避重试。禁言本身带到期时间，即使服务长时间宕机也不会有人被永久禁言。
- **不阻塞**：长轮询只负责分发，同一用户的更新保持顺序、不同用户并行；限制/放行/移除走关键通道，不被消息限速拖慢；私聊控制台的慢操作走独立队列，永远不会拖慢入群验证。
- **安全**：Mini App 服务端校验 `initData` HMAC、用户与流程绑定、Guard query 绑定；答案从不下发；Token 不写日志、不出现在错误信息里；预览抓取器在连接时校验 IP，防 SSRF 与 DNS 重绑定。
- **零依赖运维**：纯 Go、`CGO_ENABLED=0`，SQLite（WAL）+ 单实例文件锁，迁移与前端资源全部嵌入二进制，Docker 镜像基于 `scratch`。

详见文档站：[架构总览](docs/design/index.md)、[入群状态机](docs/design/state-machine.md)、[部署方式](docs/guide/deployment.md)、[日常运维](docs/operations/index.md)。

---

## ❓ 常见问题

**为什么群里还能看到“XX 加入了群组”？** 在控制台 → 群 → 🧹 消息清理 中打开“入群提示”。机器人需要“删除消息”权限。

**频道讨论群的评论者会被验证吗？** 请在讨论群设置中开启“加入后才能发言”（权限检查会提醒你）。否则评论者不需要入群就能发言，任何入群验证都拦不住。

**直接入群能保证新人一条消息都发不出来吗？** 不能完全保证：从成员进群到禁言生效之间有网络和平台投递的延迟（通常不到一秒），这段时间里发出的消息会被自动删除。若要求绝对零窗口，请把群设为“需要审批才能加入”，TAdmin 会在批准前完成验证。

**验证码能挡住所有机器人吗？** 不能。它能显著抬高自动化批量入群的成本，但挡不住真人广告号或专门训练的识别脚本。TAdmin 的定位是低打扰、不误伤的门槛。

**用户号安全吗？** 会话只保存在你自己的 SQLite 里，只用来给 @WebpageBot 发链接。建议使用小号，并在不需要时在控制台一键退出登录。

---

## 🧪 开发

```sh
go test ./...                       # Windows / Linux / macOS 都可以跑
CGO_ENABLED=1 go test -race ./...   # 竞态检测（需要 cgo）
make release VERSION=v2.0.0         # linux/amd64 + linux/arm64
```

测试使用内置的 Telegram Bot API 假服务器（`internal/tgtest`），覆盖入群验证全流程（通过、答错冷却、超时、求助审核、保留原有限制、入群申请、突击防护、Mini App、故障重试与权限缺失）、控制台导航与权限、群命令、频道置顶清理和预览刷新队列。

真实 Telegram 的行为（尤其是 Bot API 10.x 的临时消息在各客户端上的表现、@WebpageBot 的回复措辞、扫码登录）仍需在测试群中验证，见 [上线验收清单](docs/operations/acceptance.md)，当前进度见 [项目进度](docs/progress/index.md)。

---

## 📚 文档站

完整文档是仓库内的 VitePress 站点（`docs/`，pnpm 工作区）：指南、功能、运维、设计与项目进度，含可交互的验证码试玩、状态机动画与投递方式对比。

```sh
pnpm install
pnpm docs:dev        # 本地预览 http://127.0.0.1:5173
pnpm docs:build      # 静态站点输出到 docs/.vitepress/dist
go run ./cmd/docgen  # 代码变更后同步文档数据（go test 会检查是否过期）
```

文档中的环境变量、每群默认设置、命令菜单、Bot API 方法、数据表、测试统计与验证码样例都由 `cmd/docgen` 从源码生成；项目进度来自 [docs/progress.toml](docs/progress.toml)，其中引用的每个测试都会被核对。
