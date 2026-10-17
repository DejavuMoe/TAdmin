# 备份与恢复

## 一致性备份

运行时使用 `VACUUM INTO`，不要直接复制可能仍有 WAL 写入的 `tadmin.db`。私聊 `/backup` 或系统面板会将快照发送给所有者；超过 49 MiB 时改用主机命令。

```sh
# 在 Compose 项目目录执行，文件名精确到秒，防止覆盖旧备份
mkdir -p backups
chmod 700 backups
stamp=$(date +%Y%m%d-%H%M%S)
docker compose exec tadmin tadmin backup "/data/backup-$stamp.db"
docker compose cp "tadmin:/data/backup-$stamp.db" "backups/backup-$stamp.db"
chmod 600 "backups/backup-$stamp.db"
sha256sum "backups/backup-$stamp.db" > "backups/backup-$stamp.db.sha256"
```

容器内副本会占用数据卷，确认宿主副本校验和与恢复测试后再定期清理。使用 systemd 时，从已加载相同环境配置、对数据目录有权限的进程运行 `tadmin backup <新文件路径>`；Token 仍从环境读取，不要放在命令参数里。

备份包含群设置、成员相关数据、审计事件和用户号会话。会话可用于账户访问；保存在受限目录，异地副本加密，不上传公开 issue。部署配置与密钥另行备份，数据库快照不包含环境文件。

## Compose 恢复

先在隔离环境验证快照，再操作正式实例。恢复会丢失快照之后的数据；Telegram 当前状态也可能比快照更新。历史未完成流程可能继续执行，不能承诺每种动作都先重新查询全部成员状态。

1. 记录当前镜像版本；回滚时使用与快照兼容的旧版本。
2. 将确认过的备份复制为项目内 `backups/restore.db`，检查其校验和。
3. 预先拉取仅用于文件维护的工具镜像，然后停止服务。下面命令只操作该 Compose 容器的数据卷，不删除卷。

```sh
docker pull busybox:1.37
docker compose stop tadmin
container=$(docker compose ps -aq tadmin)
test -n "$container"
test -f backups/restore.db
docker run --rm --network none --user 0 --volumes-from "$container" \
  -v "$PWD/backups:/backup:ro" busybox:1.37 sh -eu -c '
    test -s /backup/restore.db
    mkdir /data/pre-restore
    for f in tadmin.db tadmin.db-wal tadmin.db-shm; do
      if test -f "/data/$f"; then mv "/data/$f" /data/pre-restore/; fi
    done
    cp /backup/restore.db /data/tadmin.db
    chown 65532:65532 /data/tadmin.db
    chmod 600 /data/tadmin.db
  '
docker compose run --rm --no-deps tadmin check
docker compose up -d --no-build
docker compose exec tadmin tadmin healthcheck
```

`pre-restore` 已存在时脚本停止，避免覆盖上次恢复前的数据。先检查并归档它再重试。若 `check` 失败，不启动机器人，保留失败现场并恢复刚保存的原数据库文件。成功后查看待处理列表，并用测试小号验收。

systemd 部署使用同一顺序：stop → 保存数据库及 WAL/SHM → 恢复快照 → 设置 tadmin 用户所有权及 0600 → check → start。不要在服务运行时替换数据库，也不要只更换旧二进制而忽略数据库迁移。

## 迁移主机

停止旧实例，取得最终一致性快照，迁移配置及快照，验证架构、权限和健康，再启用新实例。同一个 Bot Token 只能有一个长轮询实例；旧实例确认停止后才启动新实例。恢复完成也需要重新检查 Telegram 上的实际状态。
