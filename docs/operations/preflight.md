# 本地发布准备验收

核对日期：2026-10-18。本页记录本地检查和首次 Forgejo → GitHub 同步验收，不能替代尚未发生的版本发布或服务器试用。后续提交仍需核对各自的 CI 结果。

## 范围与保持不变的决定

检查覆盖入口与配置、Bot API 客户端、入群状态机、控制台权限、验证码与 Mini App、CAS/Jev、频道处理、SQLite、订阅抓取、用户号、依赖与发行产物。

保留 Jev 原问题与阈值，当前试用使用 hint；Laya 暂停；没有新增群消息 AI 审核。本轮回归使用合成数据、冻结的模型响应和本地假 API，没有调用线上模型或连接真实群。

## 已复现并修复的主要问题

修复前先用独立测试复现，修复后检查相关调用路径；既有源码审查候选不自动算作确认问题。

| 范围 | 修复内容 | 代表回归 |
|---|---|---|
| 控制台权限 | 权限失效期间的刷新不再写回旧缓存；管理员权限变化、旧欢迎语编辑会话和取消置顶权限重新校验 | `TestInvalidationDuringRefresh`、`TestAdminRightsChangeInvalidatesCache`、`TestWelcomeSessionRechecksChatAuthorization`、`TestUnpinSettingRequiresPinPermission` |
| 参数与备份 | 拒绝非法或溢出的禁言时长；只读 scratch 中的备份临时文件使用数据目录 | `TestInvalidModerationDurations`、`TestBackupWithoutSystemTemp` |
| 入群恢复 | CAS 命中与决定一起保存；过期新流程不发送失效禁言；未准备好的 Mini App 返回可重试错误；旧申请收尾不阻碍直接入群 | `TestCASCheckpointIncludesDecision`、`TestOverdueNewGateDoesNotSendExpiredMute`、`TestWebOpenRetryDoesNotReturnEmptyChallenge`、`TestResolvingRequestCannotSkipDirectJoin` |
| 管理动作 | 私聊审核决定在更新处理屏障前落库；保留管理员原有限制，重新查询并核对机器人禁言期限后才恢复/延长 | `TestPrivateDecisionCompletesBeforeUpdateBarrier`、`TestShortAdminRestrictionPreserved`、`TestVerificationPreservesNewAdminMute`、`TestEarlierGateDoesNotOwnAdminMute` |
| 资料与 API | 保留申请事件已有简介；Guard 回退来源持久化；认证前请求与其他用户不能消耗本人的 Mini App 配额 | `TestJoinRequestBioSurvivesProfileLookupFailure`、`TestGateSourceFallbackPersists`、`TestInvalidRequestsCannotExhaustUserQuota` |
| 用户号会话 | 取消的登录不能完成；旧键盘不能修改新会话；清除凭据等待已开始的写入；旧连接不能撤销新会话 | `TestCancelledLoginCannotComplete`、`TestOldKeypadCannotModifyReplacementSession`、`TestLogoutWaitsForAdmittedSessionWrite`、`TestOldClientCannotRevokeReplacementSession` |
| 订阅与抓取 | 基线失败撤销新增来源；索引不以自身 304 跳过子来源；拒绝不完整遍历与 HTML 替代源；失败请求和 JSON 项数量有预算 | `TestSourceBaselineMustSucceed`、`TestIndexRechecksChildrenAndRejectsPartialWalk`、`TestHTMLIsNotSuccessfulSourceCheck`、`TestFailedChildrenConsumeFetchBudget`、`TestJSONFeedItemBudget` |
| 队列与存储 | 删除来源阻止迟到写入并分离保留任务；来源 ID 不复用；FloodWait/去重延期不消耗失败次数；群迁移冲突整体回滚；DST 按日历日期统计 | `TestDeletedSourceCannotEnqueue`、`TestSourceIDMigrationPreservesData`、`TestFloodWaitDoesNotConsumeFailureBudget`、`TestChatMigrationConflictRollsBack`、`TestStatsAcrossDST` |

禁言期限匹配是保守识别，Telegram 没有提供“仅当权限版本未变时修改”的原子接口，查询与执行之间仍可能被另一管理员改变。旧版本未记录期限的未完成流程不会猜测归属后自动解禁，等待已有临时限制到期或人工处理。

## 文档与 AGENTS 的约束消融

这里做的是逐项删减、保留及一致性验收，不是多次代理运行的随机对照实验；没有声称 token 或效率提升的统计数值。

| 原有或候选约束 | 处理 | 保留的保障 |
|---|---|---|
| 所有项目工作都先执行 TypeSafe 集成研究 | 缩小到 AI 集成相关修改 | AI 接口修改仍读 skill 与实时文档 |
| Forgejo 与 GitHub 同时承担 CI | 删除旧 Forgejo workflow，集中到 GitHub | Forgejo 唯一写入远程、镜像提交核对 |
| README 同时重复架构、完整部署和发布步骤 | README 保留产品入口与最短试用步骤，专题文档分工 | docgen、站点构建与链接检查 |
| 审查候选数量代表全面安全 | 删除这种承诺，按复现、回归、外部验收记录 | 错例回归、竞态、漏洞与凭据扫描 |
| 一次引入服务拆分、自动依赖合并、自动部署 | 不加入 | 单进程 SQLite；版本标签发布；部署另行操作 |
| 删除历史评测以减小文档 | 不删除；Laya 标记暂停，主导航优先当前方案 | 冻结标签与原始响应可追溯 |
| 放宽授权、持久化、依赖或数据安全检查 | 不消融 | 必要边界继续进入 CI |

## 自动化验收记录

完整命令在 [CI 与发版](/operations/releasing)，实机清单在 [上线验收](/operations/acceptance)。下面只在对应命令完成后填写结果。

| 检查 | 当前结果 |
|---|---|
| Windows 定向 RED → GREEN 回归 | 已完成上表各项修复验证 |
| Linux 模块、vet、全量 race | `go mod verify`、`go vet ./...`、`go test -race ./...` 通过；后续 JSON 项预算修改另通过相关包 race 回归 |
| Go 与文档依赖漏洞 | govulncheck 无可达漏洞，pnpm audit 无已知漏洞；模块内不可达公告见下文 |
| docgen、VitePress、离线 Python 工具 | 生成数据检查、站点构建、14 项离线 Python 测试通过 |
| 工作流语法、完整历史及当前文件凭据扫描 | actionlint、Shell/Compose 语法检查通过；Gitleaks 完整历史及公开文件快照无检出 |
| AMD64/ARM64 镜像与隔离恢复 | 两架构均实际运行通过；ARM64 为本地仿真。断网、非 root、只读根目录、备份拒绝覆盖与临时卷恢复均通过 |
| 可选反向代理模板 | Nginx `-t`（临时自签证书）与 Caddy `validate` 在断网容器中通过；没有对外监听或申请真实证书 |
| Linux 双架构发布构建与许可证 | `make release VERSION=ci` 与四个文件的 SHA256SUMS 校验通过；39 份依赖/运行库/字体声明生成结果一致 |
| GitHub CI/CodeQL | `ad7b286ecc6039be6b2febd458b0b711b5b4b102` 的 [CI #2](https://github.com/DejavuMoe/TAdmin/actions/runs/39063615553) 全部通过，含 acceptance；Go、JS/TS、Python、Actions 四项 CodeQL 均为 0 条结果、无分析错误或警告 |
| GitHub 依赖与凭据告警 | 已启用 Dependabot alerts；验收时 Dependabot、CodeQL、secret scanning 均无开放告警 |
| 正式 Release、服务器与真实群 | 尚未运行，不用 CI 结果代替；本次没有创建标签或发布版本 |

提交前发现 Git 换行转换会改变冻结响应的字节哈希，现以 `.gitattributes` 保留评测数据原始字节，16 份数据文件与 Git 入库内容逐字节核对通过。首次 CI 的 Compose 检查因 runner 要求 `tadmin.env` 存在而失败，已改为仅在 CI 工作区从示例文件生成，再完成基础和可选 Compose 校验；真实部署仍要求自己的配置文件。

已知依赖背景：当前 `govulncheck` 将 [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) 标为模块内不可达问题；涉及的 `golang.org/x/crypto/openpgp` 没有被产品导入。保留扫描，不使用整个模块忽略规则。Go 字体版权和 MIT-0 声明由许可证脚本显式补入；脚本同时扫描 AMD64、ARM64 依赖。

## v0.1.0 版本与 Debian ARM64 文档候选

2026-10-18，在已验收提交 `d0564f8e27c0b33177bb33bf628bd8602447c34f` 上补齐本轮候选检查。改动包括 `v0.1.0-dev` 开发版本、正式产物同版本约定、Debian 12／13 ARM64 的 [A1 逐步部署手册](/guide/oracle-a1)及文档入口；没有修改 Jev 原问题、阈值或入群业务逻辑。手册仍固定上述已验收源码。

| 检查 | 本轮本地结果 |
|---|---|
| 格式、模块、静态检查 | gofmt 无差异，`go mod verify`、tidy 后模块文件哈希核对、`go vet ./...` 通过 |
| 完整竞态回归 | `go test -race -count=1 ./...` 通过，未复用测试结果缓存 |
| 漏洞与许可证 | govulncheck 无可达漏洞，pnpm audit 无已知漏洞；39 份第三方声明与生成结果一致 |
| 文档与评测工具 | `docgen -check`、VitePress 构建、14 项离线 Python 测试通过 |
| 部署与工作流 | actionlint、所有 shell 脚本、手册 12 段 Bash 语法通过；基础、A1、Jev 故障覆盖与恢复、可选 Laya Compose 仅用假凭据校验通过 |
| 固定源码入口 | 手册指定提交的公开源码归档下载返回 HTTP 200；没有在服务器执行下载或安装 |
| 本地版本打包 | `make release VERSION=v0.1.0` 的双架构二进制内置版本及 SHA256SUMS 已验证；只生成本地文件 |
| 双架构候选镜像 | `VERSION=v0.1.0` 的 AMD64、ARM64 镜像均通过 `scripts/verify-image.sh`：版本、非 root、只读根目录、离线数据库自检、备份拒绝覆盖及隔离恢复；ARM64 为本地仿真 |

WSL 构建镜像没有 `.git`，actionlint 必须显式传入 `.github/workflows/*.yml`，不能依赖其自动查找 Git 项目。Compose 检查使用临时目录和假密钥，不覆盖真实配置；镜像运行检查禁用网络，不连接 Telegram 或模型服务。

以上是提交前的本地记录。推送后应以该候选准确提交的 GitHub Actions、四项 CodeQL 分析和开放告警为最终远端验收依据；不沿用上表首次 CI 的结果。真实 A1、Telegram 客户端、Jev 连通性及正式 tag / Release / GHCR 发布仍属后续阶段。

## 首次试用范围与限制

先用专用测试群和普通小号验证默认入群流程，CAS、Jev 和用户号分别开启；开启 Jev 时选仅提示。hint 只约束 Jev，验证码超时、管理员命令与 CAS 命中仍有管理动作。

Telegram 动作与 SQLite 不能组成跨系统事务，步骤位支持恢复重试，不承诺 exactly-once。普通私聊慢操作队列不跨进程恢复，未完成的备份、设置或订阅操作应在重启后核对后重试。完整关闭流程后的迟到加入通知仍需在真实客户端/重连验收中检查，不把仅针对未完成流程的去重描述成无限期去重。

可选用户号目前以扫码和数字验证码为主；特殊邮件设置、词语验证码等 Telegram 登录分支没有完整控制台支持。@WebpageBot 未绑定原消息的自然语言回复只能按顺序归属，不能当作网站预览已生效的独立证明。Mini App 截止时遇到连续网络失败，应重新打开页面读取服务端状态。这些边界不由 Jev 分数解决。
