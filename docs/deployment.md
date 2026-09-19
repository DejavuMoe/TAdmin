# 部署 TAdmin

本项目支持 Linux amd64/arm64。Docker Compose 与单二进制 systemd 运行同一应用；选择一种，不要让同一个 Bot Token 的两个实例同时 Long Polling。数据库锁能阻止同一数据库的并行实例，无法阻止使用不同数据库却共用 Token 的两个进程。

准备自己的 Bot Token、所有者用户 ID、来源群 ID，以及已经配置证书的域名。示例 ID/域名必须本地替换；不要发送 Token 给开发者。

## Telegram 设置

1. 来源群：无话题超级群；授予机器人管理员、限制成员、邀请/处理入群申请权限。
2. 无需额外管理群，审核和日志默认发到所有者私聊。如需共享审核，可选把 `management_chat_id` 配成已有无话题群，也可以是来源群本身；机器人能发送和编辑自己的消息即可。该可选目的地是全局配置，接入多个群时会共享它。
3. 频道关联讨论群：在 Telegram 中启用“加入后才能发言”。关闭助推用户绕过发言权限的设置。
4. 所有者先与机器人私聊点击 Start。新成员不必提前 Start：私聊不可达时，机器人会发送绑定该用户的深链。
5. 直接入群使用私聊消息内的 `InlineKeyboardButton.web_app`，URL 由 `public_base_url` 生成；不需要先配置 Main Mini App、菜单 Web App 或 Guard。具体客户端实际能否打开和回传 `initData` 必须在真实验收时确认。
6. 本程序使用 Long Polling。已有 webhook 会阻止启动；先在原部署中移除 webhook，程序不会擅自删除或丢弃更新。

## Docker Compose

要求 rootful Docker Engine、Docker Compose 2.30+ 和原生 Linux 宿主机；rootless 的主机网络语义不在此模板验收范围内。模板使用 `network_mode: host`，使应用继续只监听宿主机 `127.0.0.1:8080`，由宿主机 Nginx 代理；无需公开容器端口。该模板不是面向未启用 host networking 的 Docker Desktop 的通用模板。

在仓库根目录执行：

```sh
test -e config.docker.toml || cp config.docker.example.toml config.docker.toml
test -e tadmin.env || install -m 600 tadmin.env.example tadmin.env
```

本地编辑 `config.docker.toml`，替换自己的 ID 和域名；本地编辑 `tadmin.env`，填入单行 `TADMIN_BOT_TOKEN=你的真实Token`。这两个文件已加入 Git 忽略。

完整配置示例：

```toml
owner_user_id = 123456789
database_path = "/var/lib/tadmin/tadmin.db"
listen_address = "127.0.0.1:8080"
public_base_url = "https://verify.example.com"
default_language = "zh-CN"
```

```sh
docker compose config --quiet
docker compose build
docker compose run --rm tadmin --config /etc/tadmin/config.toml --check
docker compose up -d
docker compose logs --tail=50 tadmin
curl --fail http://127.0.0.1:8080/healthz
```

`--check` 不连接 Telegram，会初始化命名卷中的 SQLite。无真实 Token 时可以在本地 `tadmin.env` 暂填 `TADMIN_BOT_TOKEN=123:offline-check`，只运行 `--check`，不要把它用于正常启动。

运行镜像为 scratch + 静态 Go 二进制 + CA 证书，UID/GID 65532，根文件系统只读，无 Linux capabilities；数据库目录通过命名卷持久化。命名卷保存 `.db`、WAL/SHM 和锁文件。不要执行 `docker compose down -v`，除非确实要删除全部数据。镜像无 shell，不依赖容器内 curl。

首次构建需下载固定版本 Go 基础镜像和模块。受限制网络可在 Compose 的 `build` 下使用 `network: host`，或配置自己信任的构建网络；不得关闭模块校验来掩盖下载错误。运行模板已限制为 1 CPU / 256 MiB，这只是保守资源上限，不是负载测试结论，可按真实验收结果调整。

## 单二进制 + systemd

在构建机器运行：

```sh
make release VERSION=v0.1.0-rc.1
(cd dist && sha256sum -c SHA256SUMS)
```

把对应架构的二进制和模板复制到服务器。以下以 amd64 为例；已有 `tadmin` 用户时跳过创建用户：

```sh
sudo useradd --system --home-dir /var/lib/tadmin --shell /usr/sbin/nologin tadmin
sudo install -d -m 0750 -o root -g tadmin /etc/tadmin
sudo install -d -m 0700 -o tadmin -g tadmin /var/lib/tadmin
sudo install -m 0755 dist/tadmin-linux-amd64 /usr/local/bin/tadmin
sudo install -m 0640 -o root -g tadmin config.docker.example.toml /etc/tadmin/config.toml
sudo install -m 0600 /dev/null /etc/tadmin/tadmin.env
sudo install -m 0644 deploy/systemd/tadmin.service /etc/systemd/system/tadmin.service
```

本地编辑 `/etc/tadmin/config.toml` 的 ID、HTTPS 域名；编辑 `/etc/tadmin/tadmin.env` 为单行 `TADMIN_BOT_TOKEN=你的真实Token`。服务由 systemd 读取环境文件，不需要应用用户能读取该文件。

部署前可用合成 Token 检查文件权限和 SQLite：

```sh
sudo -u tadmin env TADMIN_BOT_TOKEN=123:offline-check /usr/local/bin/tadmin --config /etc/tadmin/config.toml --check
sudo systemctl daemon-reload
sudo systemctl enable --now tadmin
sudo systemctl status --no-pager tadmin
curl --fail http://127.0.0.1:8080/healthz
```

## Nginx HTTPS

使用 `deploy/nginx/tadmin.conf` 的完整 server 模板（Nginx 1.25.1+，离线语法验收使用 1.28.0），替换 `verify.example.com` 和证书路径。若已有该域名的 server，合并对应 location，不要添加重复 server。

```sh
sudo nginx -t
sudo systemctl reload nginx
```

模板只代理 `/verify/`、`/api/verify/`，不公开 `/healthz`。验证路由关闭访问日志及包含请求路径的错误日志，避免会话链接进入日志；故障在应用结构化日志中排查。不要添加记录请求体的代理调试日志。反向代理必须保留原路径，不要添加子目录前缀。

来源服务始终绑定回环地址，避免绕过 HTTPS 直接访问。现有 Caddy 示例在 `deploy/caddy/Caddyfile`，使用同样的路由边界。

## 接入三个群

在机器人私聊中分别执行，ID 换成自己的来源群 ID：

```text
/tadmin setup -1001111111111
/tadmin setup -1002222222222
/tadmin setup -1003333333333
/tadmin groups
/tadmin status
```

接入检查来源群类型、权限、讨论群入口，以及所有者私聊（或可选共享目的地）的发送与编辑能力，然后保存配置。重复接入不会创建话题。默认私聊审核由所有者处理；配置共享目的地后，来源群当前管理员也能通过卡片审核。

`/healthz` 只证明本地 HTTP 与数据库健康，不证明 Telegram 可达、机器人权限充足或 Mini App 在真实客户端可用。上线前完成 `docs/verification.md` 的真实 API 清单；日常维护见 [docs/operations.md](operations.md)。
