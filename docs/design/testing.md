# 测试与文档同步

## 测试

<TestChart />

测试覆盖从渲染器到整机的每一层：

- **Bot API 假服务器**（`internal/tgtest`）：记录每次调用，维护成员状态、消息、置顶与更新队列。验证引擎、控制台、群命令、频道联动都针对它做端到端测试。
- **整机测试**（`TestRunEndToEnd`）：真正调用 `app.Run`，通过假服务器推送“机器人入群”“新人入群”更新，检查禁言与临时消息题目，再优雅关闭。
- **状态机回归**：通过、答错冷却、超时、求助审核、保留原有限制、离群重进、入群申请、Guard 过期回退、突击防护、Mini App、故障重试、权限缺失……每个曾经发现的问题都有对应的回归测试。
- **安全回归**：伪造回调数据、待授权群绕过、跨用户打开验证、initData 篡改、SSRF、gzip 炸弹、Token 泄露。
- **文案完整性**：扫描源码中每个文案键，确认中英文都存在且格式化参数数量一致。

在 Windows 与 Linux 上都能直接运行；竞态检测需要 cgo：

```sh
go test ./...
CGO_ENABLED=1 go test -race ./...
```

## 文档如何与代码保持同步

这个文档站里标有 <span class="t-sync">本节内容由代码生成</span> 的部分，都来自 `cmd/docgen`：

| 文档内容 | 来源 |
|---|---|
| 环境变量表 | `config.EnvVars`——同一张表也驱动配置加载 |
| 配置文件模板 | 仓库中的 `config.example.toml`（与 `config.Sample` 一致） |
| 每群设置与默认值 | 反射读取 `store.DefaultSettings()`，缺少说明的字段会让生成失败 |
| 冷却阶梯 | `gate.Cooldown()` |
| 命令菜单 | `bot.CommandMenus()`——机器人注册菜单用的是同一个函数 |
| Bot API 方法 | 扫描 `internal/tg/methods.go` |
| 数据表 | 解析 `assets/migrations/*.sql` |
| 测试统计 | 解析所有 `*_test.go` |
| 验证码样例与答案 | 用固定种子调用真实渲染器，答案通过公开的 `Check` 暴力求解 |
| 项目进度 | `docs/progress.toml`，每项功能引用的测试名都会被核对 |
| 部署模板 | 页面直接引用仓库中的 compose、systemd、Nginx、Caddy 文件 |

生成的数据提交在仓库中。`internal/docgen` 的测试会重新生成一遍并与磁盘比较——代码变了而文档数据没更新，`go test ./...` 就会失败：

```sh
go run ./cmd/docgen          # 重新生成
go run ./cmd/docgen -check   # 只检查是否过期（适合 CI）
```

它还会核对 Mini App 前端的点击网格与 Go 渲染器的布局常量是否一致。

## 本地预览文档站

文档站是仓库根目录 pnpm 工作区中的 `docs` 包（VitePress）：

```sh
pnpm install
pnpm docs:dev       # http://127.0.0.1:5173
pnpm docs:build     # 输出到 docs/.vitepress/dist
```
