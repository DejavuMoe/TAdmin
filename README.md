# TAdmin

自托管 Telegram **入群验证码机器人**，面向约 3 个、每群 500–1000 人的无话题超级群，也支持频道关联讨论群。

新人加入后，机器人先限制发言，再通过私聊 Mini App 提供图形验证码；通过后解除本次验证限制、遵循当前群默认权限（保留入群时已有的个人限制），超时自动移除并允许重新加入。默认 180 秒、最多 3 次尝试，错误换题不延长截止时间。验证失败可由来源群管理员在原截止时间内审核，超时不会再等待 30 分钟。

**交付状态：离线验收候选版本。** 自动化测试可以验证代码与模拟接口的行为；真实 Telegram Token、群权限、客户端 Mini App 及平台实际投递仍须按照 [验收清单](docs/verification.md) 测试后才能宣布上线可用。

## 首版范围

- 单 Go 进程、SQLite、Long Polling；不需要 Redis、外部数据库或本地 Bot API Server。
- 服务端生成 3×3 PNG 图形验证码，按顺序点击两个目标；答案不下发到页面。
- 校验 Telegram `initData` 签名、有效期、用户与会话绑定；提交只能成功一次。
- 支持验证中重启、外部动作失败重试、持久化更新偏移和同一数据库的单实例锁。
- 审核卡与结果默认发到所有者私聊，无需额外管理群；可选配置已有群作为共享目的地，卡片仍绑定保存的聊天与消息 ID。
- 所有者通过机器人私聊按明确群 ID 接入、查看和调整验证时间；中英文提示。
- Docker Compose 和单二进制 + systemd 两种部署，复用宿主机 Nginx 的 HTTPS。
- 现有 Guard / 普通入群申请作为兼容入口保留；不要求开启审批制或设置 Guard。

直接入群验证针对普通用户账号；配置中的所有者、当前群管理员和 Telegram 标记的机器人账号不触发验证码。人工批准是有权限管理员作出的显式例外。普通用户退群再加入默认重新验证，不默认启用 30 天免验证。

首版运行时不分析成员消息，不启用广告评分、图片识别、刷屏处罚、Raid、外部频道身份封禁或邀请管理。历史相关包和数据库迁移保留，但不连接到运行入口。通过验证码不代表以后不会发垃圾消息；验证码也不保证阻止人工操作或专门识别图像的脚本。

## 构建与离线检查

需要 Go 1.27.x（模块最低 1.27.0；交付构建使用 1.27.1）、GNU Make、`sha256sum`。

```sh
go test ./...
go test -race ./...
go vet ./...
go mod verify
make release VERSION=v0.1.0-rc.1
(cd dist && sha256sum -c SHA256SUMS)
./dist/tadmin-linux-amd64 --version
```

配置见 [config.example.toml](config.example.toml)。Bot Token 只从环境变量 `TADMIN_BOT_TOKEN` 读取。`--check` 会验证配置并初始化/检查 SQLite，不连接 Telegram；会创建数据库及 `.lock` 文件，不能与使用同一数据库的运行实例同时执行。

```sh
TADMIN_BOT_TOKEN=123:offline-check ./dist/tadmin-linux-amd64 --config config.toml --check
```

此处合成 Token 仅用于离线检查，不能用于实际启动。不要把真实 Token 写进配置、提交或对话。

本机文件分工：`tadmin.env` 保存 `TADMIN_BOT_TOKEN`（模板见 [tadmin.env.example](tadmin.env.example)）；`config.toml` 保存 `owner_user_id`、`database_path` 和 `public_base_url`。监听地址、语言和审核目的地可省略，分别默认回环地址 `127.0.0.1:8080`、`zh-CN`、所有者私聊。二进制不会自动加载 env 文件，部署时由 systemd/Compose 提供环境变量。

`TADMIN_TEST_CHAT_ID` 只是可选的人工 API 核查参数，不是启动配置，也不会自动启用该群；正式接入使用 `/tadmin setup <群ID>`。不需要填写 `TADMIN_TEST_USER_ID`，当前程序和仓库脚本均不读取它。


## Telegram 管理命令

把机器人加入要管理的群并授予所需权限，再与机器人私聊点击 Start：

```text
/start
/help
/tadmin status
/tadmin groups
/tadmin setup -1001234567890
/tadmin timeout -1001234567890 180
```

群 ID 为示例，需换成自己的来源群 ID。`timeout` 支持 30–900 秒，只影响之后创建的入群工作。在来源群中可直接执行 `/tadmin setup`、`/tadmin timeout 180`，只有配置中的所有者有权执行。

审核卡默认只发给所有者。需要其他管理员共同审核时，可选配置已有群作为共享目的地，也可以使用来源群本身；能看到卡片不代表有权操作，仍需全局所有者或来源群当前管理员身份。匿名命令无法确认真人身份，因此拒绝执行。

## 部署与使用前提

- 来源群必须是无话题 `supergroup`。不需要创建额外群。
- 来源群机器人需要管理员身份及限制成员、处理申请权限；默认审核私聊要求所有者先点击 Start。如配置可选日志群，只检查可访问和发送/编辑自己消息的能力，不要求额外管理员权限。
- 频道关联讨论群须开启“加入后才能发言”。频道评论串不是论坛话题，不会被当作论坛拒绝。
- 不启用助推用户绕过群发言权限；接入会检查此条件。
- 使用专门的 HTTPS 域名根路径，例如 `https://verify.example.com`，不支持 `/tadmin` 子路径部署。
- 验证窗口通过私聊的 Web App 按钮打开；未启动过机器人或拒收私聊者使用来源群中的私聊深链。
- 机器人在线后才能处理加入事件；从成员进入到机器人限制生效之间存在平台投递/网络延迟，不能承诺“第一条消息绝不可能发出”。

完整步骤见 [部署文档](docs/deployment.md)，模板包括 [Compose](compose.yaml)、[systemd](deploy/systemd/tadmin.service)、[Nginx](deploy/nginx/tadmin.conf)。[Caddy 模板](deploy/caddy/Caddyfile) 作为可选反向代理示例保留。

维护、备份、恢复、Token 轮换见 [运维文档](docs/operations.md)；产品边界见 [需求文档](docs/requirements.md)；官方契约、测试证据及真实 API 验收见 [验证文档](docs/verification.md)。[历史进度](docs/progress.md) 中旧版群管能力与旧测试结论不代表当前首版范围。
