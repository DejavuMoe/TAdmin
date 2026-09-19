# 运维与恢复

TAdmin 只有一个运行实例、一个本地 SQLite 数据目录和一份 TOML 配置。数据库使用 WAL mode；备份、容器迁移和回滚都必须把这一点纳入操作。

## 日常检查

```sh
# 二选一：
sudo systemctl status --no-pager tadmin
sudo journalctl -u tadmin -n 100 --no-pager
# 或：
docker compose ps
docker compose logs --tail=100 tadmin

curl --fail http://127.0.0.1:8080/healthz
```

在机器人私聊使用 `/tadmin status`、`/tadmin groups`。更改验证时限：`/tadmin timeout <群ID> <30-900秒>`，已有会话的截止时间保持不变。

管理卡显示来源群与用户 ID、验证路径、结果；完整 Token、`initData`、验证码答案和用户消息正文不进入管理卡或日志。不要把完整日志、数据库或配置直接公开发布。

## 备份与恢复

最简单的一致备份是停服务后复制整个数据目录，同时保存配置、Token 环境文件和正在运行的二进制/镜像版本；备份文件应限制读取权限。

systemd 示例：

```sh
sudo systemctl stop tadmin
sudo install -d -m 0700 /var/backups/tadmin
sudo tar -C /var/lib -cpf /var/backups/tadmin/data.tar tadmin
sudo chmod 0600 /var/backups/tadmin/data.tar
sudo systemctl start tadmin
```

备份名应在本地按日期调整，避免覆盖唯一可用备份。在线备份必须使用 SQLite backup API 或 `sqlite3 /var/lib/tadmin/tadmin.db '.backup /受保护目录/tadmin.db'`；不能在服务运行时只复制 `.db` 而遗漏未 checkpoint 的 WAL。

Compose：`docker compose stop tadmin` 后，使用 Docker 命名卷备份工具或宿主机管理员方式备份 `tadmin-data` 卷的全部内容，再 `docker compose start tadmin`。不要操作其他项目的同名逻辑卷，先用 `docker compose config --volumes` 和 `docker volume inspect` 确认完整卷名。scratch 镜像没有 shell；不要假设能进入容器运行备份命令。

恢复时先停服务，恢复同一份完整快照，确认目录所有者（systemd 为 `tadmin`，容器为 UID/GID 65532）。停服务后可运行对应部署的 `--check` 验证配置、SQLite quick_check 和外键，再启动并观察管理状态。

锁文件可以随备份保留：真正的实例锁是内核文件锁，不是“文件存在”。不要在实例运行时删除锁文件。请使用本地文件系统，不依赖 NFS 上的锁/WAL 语义。

## 升级与回滚

迁移是 forward-only；已执行的 0001–0005 保持原样，0006 定义验证首版状态。首次部署没有生产迁移负担。已有开发数据库仍会正常执行迁移，但旧话题卡没有管理群快照时不能使用新按钮授权。

升级前记录版本、校验和，停止服务并备份。替换二进制或重建镜像后执行 `--check`，再启动。回滚使用配套的旧二进制/镜像与升级前数据库备份，不能让旧程序任意读取新结构。

如果只是重启且没有更换版本，保留数据库即可：验证截止时间不延长，已经通过的验证码仍会恢复权限，过期工作会继续移除/拒绝；API 暂时失败会保留任务并退避重试。

## Bot Token rotation

在 BotFather 本地轮换 Token 后，更新自己的环境文件，再重启对应服务。旧 Mini App 的 `initData` 将不再通过新 Token 的签名检查，用户需要从机器人重新打开入口；已有未完成会话仍使用原截止时间。可以在临时停止新增入群后进行轮换，避免验证窗口内中断。

新版本不运行旧邀请管理器，也不会尝试用新 Token 解密历史邀请数据。若开发期曾创建 Telegram 邀请链接，是否保留或撤销应在 Telegram 管理界面人工确认。

## 故障处理

| 现象 | 检查与处理 |
|---|---|
| 启动提示已有实例使用数据库 | 停止另一个 systemd/Compose 实例；不要删除正在使用的锁文件 |
| 启动提示 webhook 冲突 | 在原部署移除 webhook 后再启用 Long Polling；程序不会清空待处理更新 |
| 接入失败 | 核对真实群类型、机器人权限及所有者私聊（或可选共享目的地）可发送/编辑消息 |
| 频道评论绕过入群 | 在 Telegram 开启“加入后才能发言”，重新接入检查 |
| 私聊发不出去 | 新人点击群内专属私聊链接，按 Start 打开验证；仍失败会进入原期限内人工审核 |
| 验证通过但仍不能发言 | 检查当前群默认权限、入群前已有的个人限制和 Telegram API 失败日志；“验证码通过”与“权限恢复完成”是两个状态 |
| 机器人被降权/移出 | 恢复权限并重新 setup；不要手工修改 SQLite 来伪造完成 |
| Telegram 429 | 程序遵守 `retry_after` 并限制群消息发送速度；不要靠循环重启规避限流 |
| 403/权限错误持续存在 | 恢复相应权限；未完成动作不会被标成成功，`status`/日志用于定位 |
| Mini App 空白或鉴权失败 | 检查域名根路径、证书、Nginx 路由、Telegram 官方脚本可达性以及是否在 Telegram Web App 内打开 |
| 图片题加载失败 | 重新从入口打开；加载完成前不能提交点击，错误不会自动放行 |

来源群迁移事件会暂停旧群接入，记录审计；不会自动对新群 ID 重放处罚。管理员需要在 Telegram 确认新群并重新接入。当前版本不提供生产跨群 ID 自动迁移。

机器人离线、平台延迟、权限被撤销、有人通过其他管理员手工放行时，不能保证即时禁言/移除。应用不改变 Telegram 的最终权限控制，也不承诺验证码能够阻止所有自动化账号。

验证码通过不会解除入群前已存在的个人限制。已有定时限制距到期不足 60 秒时，恢复动作等待其到期后重试，避免 Telegram 把过短的限制期限解释为永久限制。不要同时使用多个入群验证机器人；验证期间管理员主动修改同一用户权限时，以 Telegram 中的实际权限为准，必要时人工处理。

数据库在首次连接 Telegram 时绑定 Bot ID。正常轮换同一机器人的 Token 不受影响；换成另一个机器人的 Token 会拒绝启动，应使用独立数据目录，不能重放旧机器人的待处理动作。
