# TAdmin

自托管的 Telegram 多群管理机器人：Go 单进程、SQLite、可选 Mini App，支持 Linux AMD64 / ARM64。核心入群验证不依赖 AI、域名或用户号。

[![CI](https://github.com/DejavuMoe/TAdmin/actions/workflows/ci.yml/badge.svg)](https://github.com/DejavuMoe/TAdmin/actions/workflows/ci.yml) · [MIT](LICENSE)

当前处于部署试用准备阶段。自动化检查与真实 Telegram 客户端验收分开记录，构建通过不代表已在正式群验证。

## 已实现

- 多群独立策略、所有者授权接入、管理员权限检查和私聊控制台。
- 直接入群、入群申请、可选 Guard；图形、算术、按钮验证，超时、求助、人工审核与重启恢复。
- 可选 CAS 和 Jev 入群资料提示。**本轮试用采用 Jev 仅提示，保留原问题与阈值；Laya 暂停评估。**
- 欢迎语、服务消息清理、频道自动置顶管理及管理员禁言/移除/封禁命令。
- 可选用户号链接预览刷新、RSS/Atom/JSON Feed/sitemap 订阅。
- 一致性数据库备份、健康检查、中英文界面。

没有群消息 AI 内容审核，也没有独立通用群管 REST API。Mini App 接口需要 Telegram 签名和用户绑定。依赖 Bot API 10.3 的临时消息与 Guard 功能仍需按客户端实测。

## 首次试用

先阅读 [详细部署指南](docs/guide/deployment.md)，准备测试超级群和普通小号。在已有源码目录执行：

```sh
cp .env.example .env
cp tadmin.env.example tadmin.env
chmod 600 .env tadmin.env
# 编辑 tadmin.env，填写 Bot Token 和所有者数字 ID
docker compose build
docker compose run --rm tadmin check
docker compose up -d --no-build
docker compose exec tadmin tadmin healthcheck
```

默认从当前源码构建 `tadmin:local`，不假设版本镜像已发布。发布后可使用 GHCR 版本标签或 digest。端口仅绑定 `127.0.0.1`，数据保存在命名卷；不要执行 `docker compose down -v`。

## 文档与开发

- [功能与架构图](docs/design/current-map.md) · [配置参考](docs/guide/configuration.md)
- [备份与恢复](docs/operations/backup.md) · [实机验收](docs/operations/acceptance.md)
- [CI 与发版](docs/operations/releasing.md) · [安全报告](SECURITY.md) · [贡献方式](CONTRIBUTING.md)
- [本地发布准备验收与限制](docs/operations/preflight.md)
- [Jev 提示开关消融](docs/guide/jev-hint-ablation.md) · [独立短资料评测](docs/guide/jev-fresh96.md)

```sh
go test -race ./...
go run ./cmd/docgen -check
pnpm install --frozen-lockfile
pnpm docs:build
```

工具版本见 `mise.toml`；`pnpm docs:dev` 在本机预览文档。

维护者唯一写入远程是 `ssh://git@ssh.via.moe/dejavu/TAdmin.git`。Forgejo 同步到 [GitHub](https://github.com/DejavuMoe/TAdmin)，GitHub Actions 执行检查与标签发布。普通 master 推送不发布版本、不部署服务器。GitHub 的贡献由维护者回收到 Forgejo，避免镜像分叉。

代码使用 MIT 许可证；第三方依赖和内置技能保留各自许可。二进制与镜像携带 [第三方声明](THIRD_PARTY_NOTICES.txt)，依赖更新后用 `bash scripts/licenses.sh` 重新生成。
