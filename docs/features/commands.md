# 命令一览

## 机器人命令菜单

<CommandTable />

**时长格式**：`30s`、`10m`、`2h`、`7d`、`1w`，纯数字按分钟计。禁言和定时封禁的时长必须在 1 分钟到 366 天之间（超出这个范围 Telegram 会视为永久）。

**目标**：`/pass` 和 `/unban` 可以直接写用户 ID；其余命令需要回复目标成员的消息。命令对管理员本身无效。

设置 **🧹 消息清理 → 管理命令与回复** 后，命令消息会被删除，机器人的回复约 30 秒后自动消失。

## 命令行

```text
tadmin [run]           启动（默认）
tadmin check           检查配置与数据库，不连接 Telegram
tadmin backup out.db   在线一致性备份（服务运行时也可以执行）
tadmin healthcheck     探测本地 /healthz（容器健康检查）
tadmin sample-config   输出带注释的配置模板
tadmin version
```

所有子命令都接受 `-config config.toml`（或环境变量 `TADMIN_CONFIG`）；配置文件不存在时只使用环境变量。
