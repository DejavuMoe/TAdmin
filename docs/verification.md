# 官方契约与离线验收

核查与验收日期：2026-09-17（Asia/Singapore）。目标是可进入真实 Telegram 验收的候选交付物，不能把本文件理解为已经连接生产 Telegram 的证明。

## 官方依据

- [Bot API 10.3](https://core.telegram.org/bots/api)：本次按当前文档核对调用，不使用旧版 ephemeral 参数。直接入群只依赖稳定的成员事件、私聊按钮、限制/解除限制和移除接口。
- [成员更新](https://core.telegram.org/bots/api#update)、[成员状态](https://core.telegram.org/bots/api#chatmemberrestricted)：显式订阅 `chat_member`；`restricted` 必须同时看 `is_member`。
- [限制成员](https://core.telegram.org/bots/api#restrictchatmember)、[权限](https://core.telegram.org/bots/api#chatpermissions)：仅接入超级群；完整解除本次限制时使用全部 true，已有个人限制单独恢复，显式保留 false。
- [移除/解封](https://core.telegram.org/bots/api#unbanchatmember)：超时采用单次非永久移除，避免 ban/unban 中间状态。
- [Mini App 鉴权](https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app)、[Guard Mini App](https://core.telegram.org/bots/webapps#launching-mini-apps-on-chat-join-requests)：HMAC 校验包含除 hash 外的字段；Guard 绑定 `chat_join_request_query_id`，不混用 Web App `query_id`。已签名的 chat ID 存在时也必须匹配。鉴权最大年龄 15 分钟，实际可提交期限仍以会话截止时间为准。
- [讨论群](https://core.telegram.org/api/discussion#requiring-users-to-join-the-group)：讨论区可能允许非成员评论，接入必须检查“加入后才能发言”。
- [Long Polling](https://core.telegram.org/bots/api#getupdates)、[限流](https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this)：持久化 offset、启动时排空旧更新；共享 HTTP 429 退避，群消息及编辑间隔至少 3.1 秒。
- [已处理申请错误](https://core.telegram.org/method/messages.hideChatJoinRequest)：明确的 `HIDE_REQUESTER_MISSING` 结束本地工作，不伪造批准，也不以“用户不是成员”推断申请已经拒绝。
- [Docker host networking](https://docs.docker.com/engine/network/drivers/host/)：Compose 使用 Linux host 网络，HTTP 服务仍只绑定回环地址。
- [Go 官方漏洞检查](https://go.dev/doc/security/vuln/)：使用 govulncheck 检查实际程序入口可达的已知漏洞。

这是一份实现契约摘要；完整字段和能力条件以链接中的官方规范为准。Guard 的首次响应时限、真实平台错误文本及客户端行为仍须实测，不由模拟接口替代。

## 本地验证覆盖

| 风险或行为 | 验证方式与结论 |
|---|---|
| 成员加入、禁言、验证、恢复 | 模拟 Bot API + 实际 SQLite + 本地 TLS 验证 HTTP 接口贯通；普通消息没有进入评分或处罚链 |
| 重启和重复更新 | 重新打开数据库并重放服务端历史，验证 offset 和动作幂等；启动先消费离群事件再恢复过期动作 |
| 超时和 API 失败 | 截止时间不延长；直接移除，无额外 30 分钟；失败保留任务、退避后重试 |
| 超时与已通过状态 | 截止前已成功的验证码，重启后仍恢复权限，不因恢复时已过期而踢人 |
| 既有个人限制 | 保存入群事件中的权限与截止时间，成功后恢复原限制；不足 60 秒的剩余限制等待到期后恢复（为平台 30 秒边界保留网络余量），避免被解释成永久 |
| 管理员保护 | 新晋管理员、所有者、错误管理群、零消息 ID、复制卡片、非来源群管理员及重复回调均有检查 |
| 申请与直接入群衔接 | 只有成功申请能形成一次性入群证明；消费证明与安排后续动作同一事务完成 |
| API 契约 | 检查返回 false、参数字段、无话题消息、权限布尔值、Mini App/Guard ID 区分、错误脱敏 |
| 验证 HTTP 边界 | 用户绑定、签名/时间、重复键、过期、跨用户、重放、一次性提交、大小/方法/内容类型、图片代次、限流和并发 PNG 渲染上限 |
| 随机畸形输入 | initData 和坐标的 Go fuzz；发现并修复“第一点错误时未验证第二点”的提前返回，回归样本随源码保存 |
| 并发 | 全量 race 检查、重复运行、事务领取与并发答案/审核检查 |
| SQLite | 有序迁移、重复迁移、quick_check、外键、清理保留活跃工作、数据库单实例锁及 Bot ID 绑定 |
| 部署 | 两种 Linux 架构交叉构建与 SHA-256；Docker 镜像构建，断网只读容器中两次 SQLite 离线检查；Compose 配置、Nginx 与 systemd 语法检查 |
| 浏览器 | 实际 Chromium + 真实本地 Go 验证器；仅 Telegram JS 桥使用合成 initData。桌面、移动、键盘、换题、三次失败、缺失鉴权、时钟偏差均通过 |

生产程序不存在测试会话创建路由、测试成功后门或环境变量指定任意 Bot API 服务器的入口。模拟 API 构造器为内部函数，浏览器 fixture 仅存在于 `_test.go`。

## 可复现检查

```sh
go test ./...
go test -race -count=3 ./...
go vet ./...
go mod verify
go test ./internal/verify -run '^$' -fuzz '^FuzzInitDataParser$' -fuzztime=3s -parallel=2
go test ./internal/verify -run '^$' -fuzz '^FuzzChallengeCoordinates$' -fuzztime=3s -parallel=2
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./cmd/tadmin
make release VERSION=v0.1.0-rc.1
(cd dist && sha256sum -c SHA256SUMS)
git diff --check
```

本次漏洞检查结果为 `No vulnerabilities found.`，数据库更新时间为 2026-09-15 18:39:25 UTC；这表示扫描当时未发现程序可达的已知 Go 漏洞，不代表形式化安全证明。项目运行依赖没有升级；`golang.org/x/sys` 从现有间接依赖改为直接使用，以实现 Linux 文件锁。

构建使用 Go 1.27.1、`CGO_ENABLED=0`；amd64 实际运行离线检查，arm64 为交叉编译及构建信息验证，未在真实 ARM 服务器执行。源码与文档保留在工作区，未推送、打正式发布标签或部署服务。

Docker 镜像构建在本机需要 host 构建网络；最初受网络超时影响，重试成功后进行了实际容器检查，没有通过关闭 Go 模块校验规避问题。Nginx 模板使用 1.28.0 检查；systemd 检查以本地已构建二进制替换检查副本的 ExecStart 路径，没有安装系统服务。

## 浏览器复现

Browser 插件及其 browser skill 在本次会话中不可用，按 frontend-testing-debugging 流程使用现有 Playwright 1.62.1 和 Chromium 152.0.7977.82。未为生产程序增加前端依赖或构建链。

先启动仅本机可访问的测试 fixture（路径放在仓库之外，使用新的目录）：

```sh
TADMIN_UI_FIXTURE=/tmp/tadmin-ui/fixture.json go test ./internal/webapp -run '^TestBrowserFixture$' -count=1 -timeout=6m
```

在另一个终端、fixture 文件生成后执行：

```sh
TADMIN_UI_FIXTURE=/tmp/tadmin-ui/fixture.json node scripts/check-ui.cjs
```

脚本使用开发环境已有的 Playwright。若不在 Node 标准搜索路径中，设置 `PLAYWRIGHT_MODULE` 为它的绝对模块目录；若使用系统 Chromium，设置 `PLAYWRIGHT_CHROMIUM_EXECUTABLE=/usr/bin/chromium`。没有安装 Playwright 的部署服务器不需要运行此项。

检查视口为 1000×850、390×844、320×740；包含 Telegram 主题变量模拟、页面标题/有效内容、无横向溢出、无相关 console/page 错误、原生键盘选择、实际后端判题、错误后换题、三次失败、缺失 initData，以及客户端时钟快一小时。截图写入 fixture 同目录；结束时创建 `.done` 文件让 Go fixture 安全退出。测试超时上限 5 分钟，合成会话不进入生产数据。

## 真实 Telegram 验收：必须仍执行

以下完整清单尚未全部执行；已完成的真实预检和当前阻碍见 [live-testing.md](live-testing.md)。凭据只放到自己的受保护环境文件。

1. 用一次性 Bot 和测试群核对 `getMe`、真实来源群类型与机器人权限、所有者私聊可达性，确认讨论群入口设置。
2. 用普通成员直接入群，观察实际禁言、私聊投递、首次 Start 深链、验证码成功后恢复；检查带既有个人限制和管理员身份变化的情况。
3. 验证超时、三次失败后的人工审核窗口、所有者与来源群管理员审核、非管理员/其他群管理员拒绝；检查超时不会永久封禁。
4. 在 Android/iOS 和至少一个桌面客户端打开真实 Mini App，核对 Telegram 生成的 initData、域名/证书、触摸与主题、失败换题和关闭后的结果。
5. 在验证进行中、验证码已通过但恢复 API 尚未完成、移除失败和审核卡待同步时重启，核对真实外部副作用及重入行为。
6. 若实际使用普通申请或 Guard，再分别检查联系窗口、query 能力/首次响应期限及审批结果；它们不是直接入群上线的必要入口。
7. 验证断网、限流、权限撤销/恢复、管理群不可写时的真实反馈。仅用测试账号操作处罚；不要在生产群试验。

## 明确边界

- 直接入群后的限制依赖事件投递与 API 生效，存在短暂窗口；离线期间无法实时保护群。
- 机器人不扫描入群后的内容，也不处理图片广告或外部频道身份垃圾消息。直接入群验证码不覆盖所有者、当前管理员和 Telegram bot 账号；人工批准属于已授权例外。
- 图形验证码提高自动化成本，不证明“真人”或未来行为。会解题的脚本和人工操作者仍可通过；本次未测得真实拦截率。
- Telegram 外部 API 与 SQLite 之间没有共同事务。数据库决策只成功一次，外部限制/恢复可能在响应丢失后幂等重试；审核卡发送在崩溃窗口可能出现重复外观，但只有保存了准确群与消息 ID 的卡片可操作。
- 15 分钟是 initData 最大年龄，实际会话默认 180 秒；客户端倒计时使用服务器时间，服务器时钟仍须正常同步。
- 真实群 ID 迁移、其他管理员或其他机器人的并行权限修改不属于自动协调保证。管理权限、入口设置或 Token 变化后必须检查实际 Telegram 状态。

## 2026-09-17 接入简化修正

额外审核群不再是接入条件：`management_chat_id` 省略/为零时使用所有者私聊，也允许显式指定来源群本身。配置群目的地只检查发送/编辑能力，不要求额外管理员身份。已加入默认私聊、同一来源群、独立共享群和私聊不可达的接入回归测试。旧审核卡继续使用保存的聊天/消息快照，不随配置变化重新解释授权位置。

设计参考：[Rose CAPTCHA](https://missrose.org/docs/anti-spam/captchas/) 将验证入口放在来源群/申请人私聊，[Rose 日志频道](https://missrose.org/docs/moderation/log-channels/) 是单独配置；[Shieldy 用户帮助](https://github.com/1inch/shieldy/blob/master/src/helpers/localizations.ts) 以将机器人加入目标群并设管理员为基本接入步骤。
