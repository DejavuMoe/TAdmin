# 架构总览

TAdmin 是一个 Go 进程：长轮询 Telegram，把一切状态放进一个 SQLite 文件。没有消息队列、没有缓存服务、没有 Web 框架。

## 设计原则

1. **零配置可用**：没有域名也能完整验证（浮层、群内、私聊）。
2. **Telegram 原生交互**：所有设置在私聊控制台里点按钮完成。
3. **只管门口**：处理入群、频道联动与服务消息，不分析已入群成员的发言。
4. **不永久误伤**：失败 = 带递增冷却的移除；永久封禁只由管理员显式执行。
5. **可恢复**：每个入群流程都是持久化状态机，任意时刻重启都从断点继续；外部动作幂等、退避重试。
6. **不阻塞**：轮询只做分发；限制 / 解除 / 移除走关键通道，不被普通消息的限速拖住；慢操作放进独立队列。

## 一条更新的旅程

```text
getUpdates ──► 批次（≤100 条）
                │
                ├─ my_chat_member（机器人进出群）  先于其他更新处理，保证群已登记
                │
                ├─ 按“用户”分成 16 个分片并行，同一用户的更新保持顺序
                │     chat_member / 入群服务消息 ──► gate：落库 → 立即禁言 → 唤醒调度器
                │     chat_join_request          ──► gate：落库（Guard 10 秒内弹出 Mini App）
                │     群内按钮                    ──► gate：作答 / 求助 / 管理员审核
                │     群消息                      ──► 清理服务消息、频道联动、管理命令
                │
                ├─ 私聊消息与控制台按钮 ──► 按用户串行的独立队列（不占用批次）
                │
                └─ 整批处理完才提交 offset：崩溃后入群事件不会丢

gate 调度器 ──► 按 next_at 取到期流程 ──► 有界 worker（按流程加锁）
                检查（CAS / AI）→ 投递题目 → 超时 → 收尾步骤（动作 · 清理 · 欢迎 · 记录）
```

## 包结构

| 包 | 职责 |
|---|---|
| `cmd/tadmin` | 命令行：run / check / backup / healthcheck / sample-config / version |
| `cmd/docgen` | 从代码生成文档数据（本站的配置表、命令、数据结构、测试统计、验证码样例） |
| `internal/config` | TOML + 环境变量配置，`EnvVars` 表同时驱动加载与文档 |
| `internal/tg` | 自研 Bot API 客户端：类型、错误分类、按聊天限速、429 退避、Token 脱敏 |
| `internal/store` | SQLite（modernc，纯 Go）、嵌入式迁移、仓储、单实例文件锁、在线备份 |
| `internal/gate` | 入群验证引擎：状态机、调度器、投递、冷却、突击防护、CAS、风险提示 |
| `internal/captcha` | 验证题：抗锯齿渲染、256 色量化，由 seed + 代数确定性生成 |
| `internal/bot` | 更新路由、私聊控制台、群命令、输入会话、授权 |
| `internal/channel` | 频道联动：自动取消置顶、一键清理、频道马甲 |
| `internal/preview` · `feeds` | 预览刷新调度与慢速队列；sitemap / Feed 发现、解析与安全抓取 |
| `internal/userbot` | MTProto 用户号（gotd/td）：扫码 / 验证码登录、@WebpageBot 刷新 |
| `internal/intel` | 可选 TypeSafe Jev 入群资料风险评估 |
| `internal/webapp` | `/healthz`、Mini App 页面与 API、initData 校验 |
| `internal/roles` | 所有者 / 管理员判定（短期缓存） |
| `internal/i18n` | 中英文文案目录，测试保证每个引用都有两种语言 |
| `internal/tgtest` | 测试用 Bot API 假服务器 |

## 用到的 Bot API

<BotApiList />

## 依赖

刻意保持很少：SQLite 驱动（纯 Go）、TOML 解析、`golang.org/x/*`、MTProto 库 gotd/td 与二维码编码。Mini App 前端是原生 HTML / CSS / JS，无构建链，嵌入二进制。
