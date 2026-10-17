# 部署与首次试用

先在测试超级群完成 [实机验收](/operations/acceptance)，再接入正式群。本指南是手动操作步骤；仓库准备工作不会自动连接服务器、启动机器人或创建版本标签。

## 部署选择与资源

| 环境 | 建议 |
|---|---|
| Oracle ARM A1，4 OCPU / 24 GB | Docker Compose 自动选择 ARM64；先关闭用户号和 CAS，Jev 按需开启仅提示 |
| x86 Linux | 同一份 Compose，自动选择 AMD64 |
| 不使用 Docker | 验证过的 Linux 二进制 + systemd |
| Mini App / Guard | 在上述部署上增加 HTTPS 反向代理 |

核心只有一个进程和一个 SQLite 数据目录。Compose 限制应用 256 MiB 内存、256 个 PID，根文件系统只读，UID/GID 为 65532。A1 的配置适合作为试用起点，实际容量与延迟需要观察；仓库没有测得 A1 的性能上限。Laya 当前暂停，首次试用无需模型容器。

## 1. 准备主机与 Telegram

1. 使用受支持的 Debian/Ubuntu，按 [Docker 官方说明](https://docs.docker.com/engine/install/)安装 Docker Engine 和 Compose v2。开发机已有 Docker Desktop 时使用其 WSL 集成，不另装 daemon。
2. 确认时间同步和磁盘余量，允许到 Telegram 的出站 HTTPS；用户号还需要 MTProto 出站连接。
3. 从 @BotFather 创建专用机器人并获取 Token，确认自己的 Telegram 数字 ID，先私聊机器人点击 Start。
4. 准备测试超级群和非管理员小号。机器人需要封禁/限制用户权限；清理需要删除消息，置顶管理需要置顶消息，入群申请需要邀请用户权限。新增 API 权限按功能提示授予。
5. 使用长轮询，不设置 webhook。启动会删除这个机器人的已有 webhook，不要复用其他服务的 Token，也不要运行两个实例。
6. 云防火墙和主机防火墙不开放 8080；普通验证无需入站公网端口。Mini App 仅由反向代理开放 80/443，SSH 按自己的管理策略限制。

```sh
uname -m                       # A1 应为 aarch64
docker version
docker compose version
timedatectl status
df -h
```

## 2. 获取源码与配置

公众可从 GitHub 克隆；维护者现有工作区仍只向 Forgejo 推送。

```sh
git clone https://github.com/DejavuMoe/TAdmin.git
cd TAdmin
git rev-parse HEAD             # 记录本次试用的准确提交
cp .env.example .env
cp tadmin.env.example tadmin.env
chmod 600 .env tadmin.env
```

分别编辑：

| 文件 | 用途 | 首次填写 |
|---|---|---|
| `.env` | Compose 插值 | 保留 `TADMIN_IMAGE=tadmin:local`、`TADMIN_VERSION=dev`、`TADMIN_HTTP_PORT=8080`；VERSION 可改成记录的提交标识 |
| `tadmin.env` | 机器人配置 | `TADMIN_BOT_TOKEN`、`TADMIN_OWNER_ID`、`TADMIN_TIMEZONE` |

真实值不要贴到 issue、日志附件或 Git。Docker 管理员能读取容器环境变量，主机访问权应按持有机器人凭据处理。特殊字符按 [Compose env_file 规则](https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/)填写。

默认不用 `config.toml`。高级配置可用 `docker compose run --rm tadmin sample-config` 导出，再用本地 Compose 覆盖文件只读挂载到 `/etc/tadmin/config.toml`；环境变量优先。不要改变容器内 `/data` 路径，除非同步修改数据挂载。

## 3. 构建、自检与启动

首次没有发行版镜像时从当前源码构建：

```sh
docker compose config --quiet  # 不输出含密钥的完整配置
docker compose build
docker compose run --rm tadmin version
docker compose run --rm tadmin check
docker compose up -d --no-build
docker compose ps
docker compose logs --tail=100 tadmin
docker compose exec tadmin tadmin healthcheck
curl --fail http://127.0.0.1:8080/healthz
```

`check` 验证配置、创建/迁移数据库并检查 SQLite，不联系 Telegram。运行中的应用持有单实例锁，此时使用 `healthcheck`，不要再次执行 `check`。健康接口表示 HTTP 和数据库可用，不保证 Telegram 或 Jev 可达。

空命名卷从镜像初始化 `/data` 的 65532:65532、0700 权限，已有卷不会自动修复权限。若改用宿主机 bind mount，先创建目录并设置相同所有者和权限，不要长期改用 root 运行。scratch 镜像没有 shell、curl 或包管理器，直接调用 `tadmin` 子命令。

## 4. 启用测试群

1. 私聊 `/start`，将机器人加到测试超级群。非所有者添加的群需要所有者批准接入。
2. 打开权限检查卡修复缺失权限；频道讨论群开启“加入后才能发言”。
3. 保留验证时限和尝试次数，用普通小号测试通过、答错、超时、求助、放行和重启恢复。所有者与管理员可能豁免验证，不适合用来测试新人路径。
4. CAS 默认关闭；开启后命中会影响入群结果，单独验收。
5. Jev 只选择“仅提示”，不选择“通过后复核”。模拟不可用时仍应继续普通验证。
6. 观察待处理列表、权限告警、日志和 `docker stats --no-stream`，再扩大试用。

临时消息和 Guard 兼容性需覆盖 Android、iOS、桌面客户端。

## 可选：Jev 仅提示

在 `tadmin.env` 配置官方服务：

```dotenv
TADMIN_TYPESAFE_PROVIDER=jev
TADMIN_TYPESAFE_BASE_URL=https://api.typesafe.ai
TADMIN_TYPESAFE_MODEL=jev-latest
TADMIN_TYPESAFE_API_KEY=YOUR_TYPESAFE_KEY
```

修改环境后重新创建容器：

```sh
docker compose up -d --no-build --force-recreate
```

再逐群开启“仅提示”。提供密钥不会自动为所有群开启 AI。已有代理需填写实际可访问的地址、模型名和认证信息；本地代理模型可以是 `typesafe/jev-latest`。容器内 `127.0.0.1` 指容器自身，服务器也不能直接访问开发机的 loopback 代理。

Jev 接收昵称、用户名、简介、群标题等资料字段，不发送群消息正文。概率不等于准确率。当前原问题、阈值保持不变；[Laya](./laya)保留为暂停的实验记录。

## 可选：Mini App 与 Guard

设置 `TADMIN_PUBLIC_URL=https://verify.example.com`，专用域名 DNS 指向主机，按 @BotFather 的 Mini App 提示配置域名；Guard 还需在 Telegram 群中指派机器人并按客户端支持情况验收。

仓库提供 `deploy/nginx/tadmin.conf` 和 `deploy/caddy/Caddyfile`，选其中一种。替换示例域名及证书路径，后端仍使用 `127.0.0.1:8080`：

- Caddy：先确认 DNS、80/443 可达，验证配置后加载，由 Caddy 管理证书。
- Nginx：先准备证书，将示例放进现有 `http {}` 配置体系；`sudo nginx -t` 成功后再 `sudo systemctl reload nginx`。

只代理 `/v/*`、`/assets/*`、`/api/v/*`，不公开健康接口或数据目录。Nginx 模板包含客户端限流；其他代理也应设置客户端限流、小请求体限制。Token 路径与 initData 不应写进访问日志。

## 可选：用户号与预览刷新

填写 `TADMIN_API_ID`、`TADMIN_API_HASH` 并重建容器，然后由所有者在私聊控制台登录。建议专用小号；核心入群验证不需要用户号。会话保存在 SQLite，备份包含账户访问凭据。

先完成 [预览刷新](/features/preview) 的登录、退出、单链接和订阅测试。不要多实例控制相同会话，保留默认 SSRF 防护与刷新间隔。

## 使用已发布镜像

仅当对应标签 CI、GitHub Release 和 GHCR 均已存在并验收后，才改用版本镜像，例如 `.env` 的 `TADMIN_IMAGE=ghcr.io/dejavumoe/tadmin:v2.0.0`。此处是格式示例，不代表该版本已发布；长期运行可固定核对过的 digest。

```sh
docker compose pull
docker compose run --rm --no-deps tadmin version
docker compose up -d --no-build
```

OCI 索引提供 AMD64/ARM64，A1 不要强制设置 `linux/amd64`。首次发布后维护者需确认 GHCR 包可公开拉取。

## systemd 与二进制

从可信发行版下载匹配架构的二进制和 `SHA256SUMS`。有 GitHub CLI 时还可执行 `gh attestation verify <binary> --repo DejavuMoe/TAdmin` 验证构建来源。无发行版时从固定提交 `make build VERSION=<commit>`。

```sh
# ARM64 示例，在下载目录先完成校验
sha256sum --ignore-missing -c SHA256SUMS
sudo useradd --system --home /var/lib/tadmin --shell /usr/sbin/nologin tadmin
sudo install -m 0755 tadmin-linux-arm64 /usr/local/bin/tadmin
sudo install -d -m 0755 /usr/share/doc/tadmin
sudo install -m 0644 README.md LICENSE THIRD_PARTY_NOTICES.txt /usr/share/doc/tadmin/
sudo install -d -m 0700 -o tadmin -g tadmin /var/lib/tadmin
sudo install -d -m 0750 -o root -g tadmin /etc/tadmin
sudo install -m 0600 tadmin.env /etc/tadmin/tadmin.env
sudo install -m 0644 deploy/systemd/tadmin.service /etc/systemd/system/tadmin.service
sudo systemctl daemon-reload
sudo systemctl enable --now tadmin
sudo systemctl status tadmin
sudo journalctl -u tadmin -n 100 --no-pager
curl --fail http://127.0.0.1:8080/healthz
```

用户已存在则跳过 useradd。模板允许 config.toml 不存在，必填值从 EnvironmentFile 读取；保留数据目录 `/var/lib/tadmin`。不要同时启用 Compose 与 systemd。更新环境变量需要 restart，修改 unit 才需要 daemon-reload。

## 升级、回滚与排错

升级前记录源码 SHA、版本、镜像 digest、配置副本，执行 [一致性备份](/operations/backup)。拉取或构建选定版本后，停止旧实例、执行一次 check，再启动并检查健康与测试群。

数据库迁移后不能只换旧二进制。回滚时停止服务，保留当前数据，再配套恢复旧镜像/二进制和升级前快照；运行中的 WAL 不可单独复制为备份。

本轮新增迁移保留订阅源并防止 ID 复用，另保存申请简介和禁言期限。旧版本未记录期限的未完成验证会保守地等待既有临时禁言到期，必要时由管理员处理；不会猜测后覆盖管理员的限制。

| 现象 | 检查 |
|---|---|
| 401/404 | BotFather Token、文件保存、容器是否重建 |
| 409 Conflict | 同 Token 的其他实例、外部服务重新设置 webhook |
| 数据库被占用 | 两个实例，或运行时执行了 check |
| Permission denied | 数据目录 65532:65532 / 0700；配置挂载是否可读 |
| 健康失败 | 日志是否仍在连接 Telegram、8080 占用、数据库可写性 |
| 没有所有者私聊 | 私聊 /start、数字 ID 正确 |
| 新人不验证 | 超级群、接入授权、开关、管理员和可信链接豁免 |
| Jev 失败 | 地址所在网络、模型名、密钥、超时；普通验证应继续 |
| 内存接近限制 | 记录功能和规模后再有依据地调整 mem_limit |

部署命令成功、mock 测试通过与真实群验收是不同证据。下一步逐项记录 [上线验收](/operations/acceptance)。
