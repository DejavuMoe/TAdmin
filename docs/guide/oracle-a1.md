# Oracle A1 首次试部署：Debian 12／13 ARM64

适用主机：Oracle ARM A1，4 OCPU / 24 GB，Debian 12（bookworm）或 13（trixie）ARM64。以下命令供你自行登录服务器后，在有 sudo 权限的普通用户 Bash 会话里逐块执行；任何一步报错都先停止排查。本轮仓库准备没有连接或操作服务器。

## 本轮固定范围

| 项目 | 本轮使用的值 |
|---|---|
| 已验收源码 | `d0564f8e27c0b33177bb33bf628bd8602447c34f` |
| 对应 CI | [完整 CI / acceptance 通过](https://github.com/DejavuMoe/TAdmin/actions/runs/39064318115) |
| 本地试用镜像 / 二进制内置版本 | `tadmin:v0.1.0-dev.d0564f8` / `v0.1.0-dev.d0564f8` |
| 入群验证 | 开启；先用默认浮层、图形题、180 秒、3 次机会 |
| Jev | 当前原问题、原阈值，每群设置为**仅提示** |
| CAS / 用户号 | 关闭 / 不配置 MTProto 凭据、不登录 |
| Mini App / Guard / Laya | 本轮不启用，无需域名或模型容器 |
| 正式首版 | 计划 `v0.1.0`，尚未创建 tag 或发布 |

固定提交包含已通过检查的业务代码；本手册及后续修改应单独验收，不能沿用旧提交的 CI 结论。正式发布时，Git tag、Release、二进制内置版本和 GHCR 标签统一取同一版本，见 [版本约定](/operations/releasing#统一版本约定)。本轮不要尝试拉取尚未发布的 `ghcr.io/dejavumoe/tadmin:v0.1.0`。

## 1. 准备账号与网络

- 使用专用测试 Bot Token；先私聊机器人点 Start。准备所有者 Telegram 数字 ID、测试超级群和两个普通成员账号（一个入群，另一个检查题目隔离和审核权限）。不要让其他程序同时使用同一 Token；启动会删除该机器人的现有 webhook。
- Jev 需要**从服务器容器可达**的 System One 服务及其密钥。官方 API 使用 `https://api.typesafe.ai` + `jev-latest`；现有代理使用其实际 HTTPS 地址 + `typesafe/jev-latest`。Base URL 不带 `/v1/systemone`，程序会追加接口路径。
- 之前 Windows 开发机的 `http://127.0.0.1:3425` 不能直接复制到 A1；容器内 loopback 也指向容器自身。代理若只在开发机监听，需要先另行准备可达方案；不能把本地代理密钥当成官方 API Key。暂时没有可用服务时，可先验收基础入群功能，但 Jev 一项必须记为未验收。
- 普通验证用长轮询。OCI 和主机入站规则只保留你需要的 SSH 管理入口；本轮不开放 8080、80、443。出站需要 DNS、时间同步，以及到 Debian/Docker 软件源、源码与构建依赖、Telegram、选定 Jev 服务的 HTTPS。

先确认系统、架构、时钟与磁盘：

```bash
cat /etc/os-release
dpkg --print-architecture
uname -m
nproc
free -h
df -h / /var
timedatectl status
```

预期架构为 `arm64` / `aarch64`，系统为上述 Debian 版本。确认系统时间已同步；构建期内存与磁盘需求高于应用运行期，先留出数 GB 可用空间。

## 2. 安装 Docker Engine 与 Compose

以下针对尚未安装容器运行时的新主机，依据 [Docker 官方 Debian 安装说明](https://docs.docker.com/engine/install/debian/)；Debian 12／13 和 ARM64 都在其支持范围内。已有 Docker Engine、Compose 和 Buildx 可直接跳到版本检查；已有 `docker.io`、Podman、containerd 等安装时先按官方说明核对冲突，不批量卸载已有服务。

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl nano
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
sudo tee /etc/apt/sources.list.d/docker.sources >/dev/null <<EOF
Types: deb
URIs: https://download.docker.com/linux/debian
Suites: $(. /etc/os-release && echo "$VERSION_CODENAME")
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
EOF
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"
```

退出并重新登录，让组权限生效，再继续。`docker` 组拥有主机 root 等级的管理能力，只加入可信维护账号，见 [Docker 安装后说明](https://docs.docker.com/engine/install/linux-postinstall/)。这些是服务器安装步骤；Windows 开发机继续使用 Docker Desktop / WSL 集成。

```bash
docker version
docker compose version
docker buildx version
docker run --rm hello-world
docker info --format '{{.OSType}}/{{.Architecture}}'
```

应能不加 sudo 运行，服务端为 Linux ARM64（架构字段也可能写作 `aarch64`）。

## 3. 获取已验收源码

使用固定提交的公开源码归档，不在服务器配置 Git 推送远程。维护者 Windows 仓库的唯一远程继续为 Forgejo。下面目录只用于首次试部署，已存在时不要覆盖，应回到原目录检查当前状态。

```bash
(
  set -eu
  trial_dir="$HOME/tadmin-a1-d0564f8"
  test ! -e "$trial_dir"
  curl -fL --retry 3 \
    https://github.com/DejavuMoe/TAdmin/archive/d0564f8e27c0b33177bb33bf628bd8602447c34f.tar.gz \
    -o "$HOME/tadmin-d0564f8.tar.gz"
  mkdir -m 700 "$trial_dir"
  tar -xzf "$HOME/tadmin-d0564f8.tar.gz" --strip-components=1 -C "$trial_dir"
)
cd "$HOME/tadmin-a1-d0564f8"
```

所有后续 `docker compose` 命令都在这个目录执行。下载发生在你操作主机时，本次准备没有在服务器执行它。

## 4. 写入配置

以下配置文件只创建一次，存在时命令会停止；更新时用编辑器修改。Compose 项目名固定为 `tadmin-a1-trial`，以便以后换源码目录仍能找到同一数据卷。不要同时启动第二份同名项目或同 Token 的实例。

```bash
(
  set -eu
  umask 077
  test ! -e .env
  test ! -e tadmin.env
  cat > .env <<'EOF'
COMPOSE_PROJECT_NAME=tadmin-a1-trial
TADMIN_IMAGE=tadmin:v0.1.0-dev.d0564f8
TADMIN_VERSION=v0.1.0-dev.d0564f8
TADMIN_HTTP_PORT=8080
EOF
  cat > tadmin.env <<'EOF'
TADMIN_BOT_TOKEN='REPLACE_WITH_BOT_TOKEN'
TADMIN_OWNER_ID=REPLACE_WITH_OWNER_NUMERIC_ID
TADMIN_TIMEZONE=Asia/Taipei
TADMIN_LANGUAGE=zh-CN
TADMIN_PUBLIC_URL=
TADMIN_API_ID=0
TADMIN_API_HASH=
TADMIN_TYPESAFE_PROVIDER=jev
TADMIN_TYPESAFE_BASE_URL=https://api.typesafe.ai
TADMIN_TYPESAFE_MODEL=jev-latest
TADMIN_TYPESAFE_API_KEY='REPLACE_WITH_JEV_KEY'
EOF
)
nano tadmin.env
chmod 600 .env tadmin.env
```

必须替换三项 `REPLACE_...`。使用代理时同时修改 `BASE_URL`、`MODEL`、`API_KEY`；不要将密钥填进 URL。密钥通过编辑器输入，不放进 shell 命令或聊天；包含特殊字符时遵循 [Compose env_file 的引号规则](https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/)。时区可按统计需要调整。

不挂载 `config.toml`，不使用 `compose.laya.yaml`。`TADMIN_API_ID=0` 与空 `TADMIN_API_HASH` 关闭用户号；空 `TADMIN_PUBLIC_URL` 关闭 Mini App。**CAS 和 AI 模式没有全局环境变量，它们是数据库中的每群设置，第 6 步仍必须核对。**

## 5. 构建、离线自检、启动

```bash
(
  set -eu
  if grep -q 'REPLACE_' tadmin.env; then
    echo '请先替换 tadmin.env 中的全部占位值。' >&2
    exit 1
  fi
  docker compose config --quiet
  docker compose build tadmin
  docker compose run --rm --no-deps tadmin version
  docker compose run --rm --no-deps tadmin check
)
```

版本应为 `TAdmin v0.1.0-dev.d0564f8 (... linux/arm64)`；自检应显示 `mini app off`、`userbot off`、`AI assessment on configured` 和数据库健康。`check` 会初始化/迁移数据库，但不连接 Telegram 或 Jev；`AI assessment on` 只说明配置了密钥，不证明认证成功，也不等于每群已启用提示。

检查通过后再启动：

```bash
docker compose up -d --no-build --wait --wait-timeout 90 tadmin
docker compose ps
docker compose logs --tail=100 tadmin
docker compose exec -T tadmin tadmin healthcheck
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
docker compose port tadmin 8080
docker inspect --format 'oom={{.State.OOMKilled}} restarts={{.RestartCount}}' "$(docker compose ps -q tadmin)"
docker stats --no-stream "$(docker compose ps -q tadmin)"
```

应看到健康状态、`connected to Telegram`，端口为 `127.0.0.1:8080`。私聊 `/start` 应打开控制台；若无法给所有者发启动消息，先主动发 `/start`。遇到 401 核对 Bot Token，409 核对是否有另一个长轮询实例。

运行中用 `healthcheck`，不要另开 `check` 与进程争用数据库锁。健康接口只覆盖 HTTP/SQLite；Telegram 交互、Jev 成功返回需要下一节的测试。现有 Compose 保留非 root、只读根目录、256 MiB 应用内存上限、256 PID 和日志轮转；4 OCPU / 24 GB 是主机容量，不是已测得的并发能力。暂不加入额外服务。

## 6. 设置测试群

1. 所有者将机器人加到**测试超级群**，设为管理员。普通验证需要封禁/限制用户与删除消息；测试入群申请时再确认邀请用户权限。置顶权限仅在验收频道联动时需要，本轮先不启用频道联动。非所有者添加的群，先由所有者批准接入。
2. 私聊 `/start` → **群组管理 → 测试群**，运行权限检查。只缺未启用功能的权限可记录；封禁/限制用户或删除消息缺失必须修复。
3. **入群验证**：开启，浮层、图形题、180 秒、3 次，次数用尽移出，冷却开启，入群申请开启。
4. **防护与安全**：确认 **CAS 关闭**，AI 从关闭点到 **仅提示** 后停止，不选“高风险转人工”。返回再进入，确认设置已保存。每个新群分别设置，不能从一个群推断其他群。
5. 不登录用户号；预览刷新不纳入本轮。欢迎语与频道自动取消置顶保持默认关闭。测试小号通过普通邀请链接加入，链接名称不要含 `#免验证`、`#noverify` 或 `#trusted`；不要由管理员直接拉入，否则走豁免路径。

## 7. 测试群验收：逐项记录通过 / 失败 / 未测

记录提交、镜像版本、测试日期、客户端版本、操作与实际结果。一次只保留一个待验证小号，避免快速连续入群触发默认突击防护。需要等待冷却时换另一普通小号，或等冷却结束；不要为测试修改生产策略。

| 项目 | 操作 | 必须看到的结果 |
|---|---|---|
| 正常入群 | 普通小号经普通链接入群，答对 | 先禁言，出题，答对后恢复；其他账号看不到其浮层题目，不能代答 |
| 提示不改变放行 | Jev 仅提示下换中英文正常短资料、空简介、反诈否定句各走一次 | 能正常完成验证码；不能因分数变高而自动封禁或转人工；不把某个分数当作本次通过条件 |
| 确认 Jev 真的工作 | 另一轮正常入群后点“需要帮助” | 人工审核卡有风险百分比/说明；提示由主动求助产生的审核卡展示，普通通过并不会单独发 AI 报告 |
| 人工审核权限 | 另一普通成员尝试点卡片放行，再由管理员放行 | 普通成员被拒绝，管理员成功；人工审核原因是求助，不是 AI 仅提示 |
| 错误与冷却 | 新一轮连续答错 3 次 | 被移出；首次失败通常 2 分钟冷却，24 小时内再次失败递增；旧题点击不应凭空增加失败次数 |
| 超时 | 临时把测试群时限设为 60 秒，小号不答题 | 到期移出；测试后恢复 180 秒 |
| 原有限制 | 对小号设置不能发图片，退群重进并答对；另测验证期间管理员新增限制 | 原有和新增限制不被机器人意外清除；正常通过不会扩大权限 |
| 入群申请 | 创建一个需要管理员批准的新邀请链接，小号通过它申请 | 私聊收到题目，通过后批准加入；不再重复群内验证；无法投递时转人工，不能算自动批准已通过 |
| 重启恢复 | 新一轮验证未结束时执行下方 restart，然后继续作答 | 原截止时间和流程保留，正常结束，无遗留禁言或重复处理 |
| 配置持久化 | 重启后再打开群的防护页 | Jev 仍仅提示、CAS 仍关闭；用户号仍未启用 |
| 客户端兼容 | 在能使用的 Android、iOS、桌面客户端分别走浮层显示/作答 | 题目正确、仅本人可见；没设备的一项写未测，不能用另一端代替 |

```bash
docker compose restart tadmin
docker compose exec -T tadmin tadmin healthcheck
docker compose logs --since=10m tadmin
```

重启后健康检查若赶在服务启动前执行，可在容器恢复健康后重试。AI 正常请求成功不写一条专门的成功日志；卡片没有风险项时，先查 `profile assessment failed` 和 API 认证/地址，不能仅凭面板按钮或健康接口宣布 Jev 接入成功。普通直入场景的简介获取还受 Telegram API 可见性影响，不应把取不到简介当成“空简介评测通过”。

### 单独验证 Jev 不可用时的降级

只在专用测试实例、没有其他待处理任务时进行。保持群设置为仅提示，使用临时 Compose 覆盖让 **Jev 请求**访问容器内未监听的端口，不改真实密钥、运行时问题或阈值：

```bash
(
  set -eu
  test ! -e /tmp/tadmin-jev-unavailable.yaml
  umask 077
  cat > /tmp/tadmin-jev-unavailable.yaml <<'EOF'
services:
  tadmin:
    environment:
      TADMIN_TYPESAFE_BASE_URL: http://127.0.0.1:1
EOF
  docker compose -f compose.yaml -f /tmp/tadmin-jev-unavailable.yaml up -d --no-build --force-recreate --wait --wait-timeout 90 tadmin
)
```

让普通小号重新入群并答对：日志应出现 `profile assessment failed`，验证仍能完成，不能自动封禁或转人工。随后立即恢复基础配置并复测一次求助卡片：

```bash
docker compose -f compose.yaml up -d --no-build --force-recreate --wait --wait-timeout 90 tadmin
docker compose exec -T tadmin tadmin healthcheck
```

恢复时明确使用基础 Compose 文件，临时覆盖不再生效。若当前没有可用 Jev 服务，降级成功也不能代替成功请求的验收。

## 8. 备份、观察与停止试用

使用应用的一致性快照，不直接复制运行中的 SQLite 文件。下列操作会在数据卷和主机各留一份快照：

```bash
(
  set -eu
  umask 077
  mkdir -p backups
  chmod 700 backups
  stamp=$(date +%Y%m%d-%H%M%S)
  docker compose exec -T tadmin tadmin backup "/data/backup-$stamp.db"
  docker compose cp "tadmin:/data/backup-$stamp.db" "backups/backup-$stamp.db"
  chmod 600 "backups/backup-$stamp.db"
  sha256sum "backups/backup-$stamp.db" > "backups/backup-$stamp.db.sha256"
  sha256sum -c "backups/backup-$stamp.db.sha256"
)
```

备份包含成员数据与配置状态；环境文件另行私密备份，不上传公开仓库。校验和通过只证明备份文件可读且与记录一致，不证明已经恢复成功。[恢复指南](/operations/backup#compose-恢复)的命令会操作当前 Compose 项目的数据卷：隔离演练时必须使用另一项目名和新卷，恢复副本只运行离线 `check`，不要在当前试用目录直接做覆盖演练。未做恢复演练时，验收记录应写“恢复未测”。

建议在测试群观察 24 小时，记录待处理数量、内存、重启次数、OOM 和网络错误。出现持续待处理、无法放行/移出、权限被覆盖、仅提示仍转人工等情况，停止扩大试用并保留脱敏日志。通过后再决定是否授权正式 tag 与发布；不要提前把文档里的待实测功能标成已验收。

退出试用前先处理完待验证成员，必要时由管理员恢复其权限，然后停止容器；保留数据卷和配置：

```bash
docker compose stop tadmin
docker compose ps -a
```

不要运行 `docker compose down -v`。将来 `v0.1.0` 的 Release、双架构 GHCR 清单及匿名拉取验收完成后，再单独安排备份与版本切换；本手册不执行发布或自动升级。
