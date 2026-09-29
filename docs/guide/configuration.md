# 配置参考

TAdmin 的配置分两层：

- **部署配置**：环境变量或可选的 `config.toml`，决定“机器人是谁、数据放在哪、启用哪些增强”。环境变量优先于配置文件。
- **每群设置**：在私聊控制台里点按钮修改，保存在数据库中，改完立即生效。

<SyncNote source="config.EnvVars · config.Sample · store.DefaultSettings()" />

## 环境变量

<EnvTable />

## 配置文件

配置文件完全可选。`tadmin sample-config` 会输出下面这份带注释的模板（即仓库里的 `config.example.toml`）：

<<< @/../config.example.toml

- 未知字段会被拒绝，避免拼写错误悄悄失效。
- Bot Token **只能**来自环境变量，不能写进配置文件。
- 预览刷新的节奏（检查间隔、最小间隔、每日上限）只能在 `[preview]` 中调整。

## 每群设置与默认值

新群接入时使用以下默认值。所有项都能在控制台中修改，无需重启。

<SettingsTable />
