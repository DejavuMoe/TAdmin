# 部署方式

TAdmin 只需要能访问 `api.telegram.org` 的出站网络。没有域名也能完整使用入群验证；只有想用 Mini App 大图验证或 Guard 零窗口入群时，才需要一个 HTTPS 域名。

下面引用的模板都直接取自仓库中的文件，文档与实际模板永远一致。

## Docker Compose（推荐）

```sh
cp tadmin.env.example tadmin.env
$EDITOR tadmin.env          # 填写 TADMIN_BOT_TOKEN、TADMIN_OWNER_ID
docker compose up -d
```

<<< @/../compose.yaml

- 数据保存在卷 `tadmin-data`（容器内 `/data`），包括 SQLite 的 WAL 文件；**不要让两个容器挂载同一个数据卷**。
- 镜像基于 `scratch`：非 root 用户、只读根文件系统、内置 CA 证书与时区数据库，自带健康检查 `tadmin healthcheck`。
- 8080 端口只绑定到宿主机 `127.0.0.1`，供宿主机上的反向代理使用。

::: details 环境变量模板 tadmin.env.example
<<< @/../tadmin.env.example{sh}
:::

## 单个二进制 + systemd

```sh
make release VERSION=v2.0.0                     # dist/tadmin-linux-{amd64,arm64}
sudo install -m 0755 dist/tadmin-linux-amd64 /usr/local/bin/tadmin
sudo useradd --system --home /var/lib/tadmin --shell /usr/sbin/nologin tadmin
sudo install -d -m 0750 -o root -g tadmin /etc/tadmin
sudo install -m 0640 -o root -g tadmin tadmin.env.example /etc/tadmin/tadmin.env
sudoedit /etc/tadmin/tadmin.env                 # 填写 Token 与所有者 ID
sudo install -m 0644 deploy/systemd/tadmin.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now tadmin
journalctl -u tadmin -f
```

<<< @/../deploy/systemd/tadmin.service{ini}

`tadmin check` 可以在启动前离线检查配置和数据库（会创建数据库文件，但不连接 Telegram）。

## 可选：Mini App 与 Guard

1. 准备一个专用子域名，例如 `verify.example.com`，并在 @BotFather 中通过 `/setdomain` 绑定。
2. 设置 `TADMIN_PUBLIC_URL=https://verify.example.com`（只能是源，不能带路径），重启。
3. 用反向代理把 `/v/*`、`/assets/*`、`/api/v/*` 转发到 `127.0.0.1:8080`。`/healthz` 只供本机探活，不必暴露。

::: code-group
<<< @/../deploy/nginx/tadmin.conf{nginx} [Nginx（含按客户端限流）]
<<< @/../deploy/caddy/Caddyfile{txt} [Caddy（自动证书）]
:::

配置完成后：私聊验证会多出“🖼 打开大图验证”按钮；入群申请若带有 Guard `query_id`，会在 10 秒内直接弹出 Mini App。

## 可选：预览刷新用户号

1. 用准备做刷新的账号登录 [my.telegram.org](https://my.telegram.org) → *API development tools*，得到 `api_id` 与 `api_hash`。
2. 设置 `TADMIN_API_ID`、`TADMIN_API_HASH` 并重启。
3. 私聊控制台 → 🔄 预览刷新 → 👤 用户号 → 📷 扫码登录，在该账号的手机上打开 **设置 → 设备 → 连接桌面设备** 扫码。

::: tip 建议使用小号
用户号会与 @WebpageBot 产生大量对话。会话只保存在你本地的数据库里。
:::

## 可选：AI 入群资料风险提示

设置 `TADMIN_TYPESAFE_API_KEY` 后，在群的 🛡 防护与安全 页面选择“仅提示”或“高风险转人工”。只会发送新成员的公开昵称、用户名、简介以及群名称，不发送用户 ID，也不发送任何群聊消息。详见 [防护与安全](/features/protection#ai-入群资料风险提示)。

## 升级与回滚

- **升级**：替换二进制，或 `docker compose build && docker compose up -d`。数据库迁移在启动时自动执行。
- **回滚前先备份**：`tadmin backup /path/tadmin-before-upgrade.db`。新版本增加的表和列通常对旧版本无害，但不保证旧版本能理解新数据。
