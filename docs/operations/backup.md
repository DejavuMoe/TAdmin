# 备份与恢复

## 备份

任选其一，服务运行时都可以执行，结果都是一致性快照（基于 SQLite `VACUUM INTO`）：

::: code-group

```text [控制台]
⚙️ 系统 → 💾 立即备份
（或私聊发送 /backup）
快照会作为文件直接发到你的 Telegram。
```

```sh [服务器]
tadmin backup /backup/tadmin-$(date +%F).db
```

```sh [Docker]
docker compose exec tadmin tadmin backup /data/backup-$(date +%F).db
docker compose cp tadmin:/data/backup-$(date +%F).db .
```

:::

超过 49 MB 的快照无法通过 Telegram 发送，届时请使用服务器命令。

::: danger 备份包含用户号会话
像保管密码一样保管备份文件。拿到备份的人可以用其中的会话登录你的用户号。
:::

## 恢复

1. 停止服务。
2. 用备份文件替换数据目录中的 `tadmin.db`，并删除同目录的 `tadmin.db-wal`、`tadmin.db-shm`。
3. 启动服务。

恢复后，Telegram 上的真实状态可能比备份更新（例如备份之后又有人入群）。请在 **📋 待处理** 中检查进行中的验证；TAdmin 执行动作前总会先读取成员的当前状态，不会盲目重放旧动作。

## 迁移到新服务器

与恢复相同：停旧实例 → 复制数据目录（或最新备份）→ 在新服务器启动。**同一个 Bot Token 不能同时运行两个实例**，否则长轮询会互相冲突（日志出现 `409 Conflict`）。
