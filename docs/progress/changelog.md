# 更新日志

## v0.1.0（准备中，未发布）

<Badge type="warning" text="CI 已通过，待实群验收" /> <Badge type="tip" text="2026-10-18" />

当前处于公开开源和首次试用准备阶段，尚未发布稳定版。实现、自动化测试、真实 Telegram 行为分别验收，不用“审查通过”代替运行证据。

### 本轮准备

- 首个稳定版计划为 `v0.1.0`，后续 tag、Release、二进制内置版本与 GHCR 精确版本标签始终取同一 Git tag。新增 [Debian ARM64 / Oracle A1 试部署手册](/guide/oracle-a1)，固定已验收提交；本轮不创建标签、不发布、不操作服务器。
- 采用 MIT，发行包和镜像携带依赖、Go 运行库与 Go 字体的许可证声明。
- 唯一维护远程继续为 Forgejo，自动同步 GitHub。GitHub Actions 执行质量、竞态、CodeQL、依赖、凭据、文档和双架构镜像检查；稳定版本标签才触发发布，不自动部署。
- 升级 Go 1.27.2、x/net 0.60.0，以及文档构建链的 Vite/source-map-js 安全版本。
- 修复管理员权限缓存、旧面板与登录会话、验证重启恢复、CAS 决定落库、禁言恢复边界、订阅基线及队列一致性；具体回归见 [本地发布准备验收](/operations/preflight)。
- 新迁移保留既有订阅源，避免 ID 复用；保存入群简介与机器人禁言期限。升级前备份，回滚须配套快照。
- 重写 A1、Compose、systemd、HTTPS、备份恢复和发布说明，区分必须配置和可选功能。
- 保留 Jev 原问题和阈值，试用采用仅提示；Laya 暂停。冻结评测及 Jev 提示消融记录保留。
- 首次 Forgejo → GitHub 同步和完整 CI 已验收，四项 CodeQL 无告警；修复冻结数据的 Git 换行转换和干净 runner 的 Compose 配置检查。准确提交和运行链接见 [验收记录](/operations/preflight)。

### 已实现的产品范围

多群独立设置、入群验证与人工审核、群管理命令、频道联动、服务消息清理、欢迎语、统计、在线快照，以及可选用户号链接预览刷新。一个 Go 进程与 SQLite，不需要额外队列或数据库服务。

临时消息使用 Bot API 10.3 参数，Guard 使用入群申请查询接口。源码接口已对照官方文档，客户端显示与送达仍需 [实群验收](/operations/acceptance)。功能关系见 [当前架构地图](/design/current-map)。

### 发布前仍需外部验证

- 后续每次提交的 Actions、CodeQL 告警，以及 PR 专用的 dependency review；首次 master CI 通过不代替后续提交或 PR 验收。
- 实际 Telegram Token、小号、权限、浮层、Guard、用户号登录与 @WebpageBot 回复。
- 稳定标签授权后的 Release、GHCR 公开拉取、双架构清单和构建证明。
- Oracle A1 真机资源占用与持续运行；本地 ARM64 仿真通过不能替代真机负载验证。
