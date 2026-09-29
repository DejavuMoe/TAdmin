# 数据与存储

TAdmin 的全部状态都在一个 SQLite 文件里：WAL 模式、外键开启、5 秒忙等待；迁移脚本嵌入二进制，启动时按顺序自动执行。同一个数据库同时只能被一个进程打开（Unix `flock` / Windows `LockFileEx`）。

## 表结构

<SchemaView />

## 关键约束

- `gates` 上的部分唯一索引 `(chat_id, user_id) WHERE state <> 'closed'`：同一个人在同一个群同时只有一个未结束的验证，重复的入群通知天然去重。
- `gates.next_at` 索引驱动调度器，只扫描未结束的流程。
- 群设置以 JSON 保存在 `chats.settings`，读取时总是从默认值开始合并——新增的设置项自动获得默认值，无需迁移。
- 预览刷新的每日上限按 `events` 中的刷新记录计算（只追加），不会因为链接重新排队而被重置。

## 保留策略

| 数据 | 保留 |
|---|---|
| 已结束的验证 | 30 天 |
| 统计 / 审计事件 | 90 天 |
| 频道置顶记录、未排队的预览链接 | 180 天 |
| 过期的定时删除 | 7 天 |

每小时自动清理一次，永远不会删除进行中的流程。

## 统计从哪来

控制台的迷你柱状图来自 `events` 表：`join`、`pass`（附带答题用时）、`fail`、`timeout`、`reject`、`ban`、`left`、`cas`、`bot_kicked`、`unpin`、`help` 等事件按时区划分到天。

<SyncNote source="assets/migrations/*.sql" />
