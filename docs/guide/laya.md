# 自部署 Laya：入群资料仅提示

当前决定（2026-10-18）：保留 Jev，暂停 Laya 评估与部署推进。以下配置保留备查；[Jev 原问题与短 v1 对照](./jev-runtime-comparison) 支持继续保留现有运行时问题，保持仅提示。

TAdmin 保留 Jev 默认行为，可切换到兼容 `/v1/systemone` 的 Laya HTTP 服务。当前阶段只评估新成员公开资料，不审核群聊消息。Laya 分数尚未在你的群数据上校准，因此代码强制仅提示：高分不会自动放行、封禁，也不会触发高风险转人工。

## 选择后端

| 环境变量 | Jev（默认） | Laya |
|---|---|---|
| `TADMIN_TYPESAFE_PROVIDER` | `jev` | `laya` |
| `TADMIN_TYPESAFE_BASE_URL` | 不填时为 `https://api.typesafe.ai` | 必填，例如 `http://laya:8000` |
| `TADMIN_TYPESAFE_MODEL` | 不填时为 `jev-latest` | 不填时为 `convaiinnovations/laya-multilingual`；也接受 `multilingual` |
| `TADMIN_TYPESAFE_API_KEY` | TypeSafe 云端密钥 | 自部署服务的本地 Bearer 密钥 |

基础地址不包含 `/v1/systemone`，可以带反向代理路径前缀，不能带用户密码、查询参数或片段。没有 Key 时 AI 关闭。Laya 不会在故障时自动改用 Jev，也不会把资料转发给云端。

每群在 **🛡 防护与安全 → AI** 选择“仅提示”。新群仍默认关闭。切换到 Laya 后，控制台只有“关闭 / 仅提示”；已有 `review` 设置按 `hint` 执行和显示，读取页面不会修改数据库里的原偏好。切回 Jev 后原偏好恢复。切换前已进入人工审核或已经标记高风险的旧验证不被自动撤销。

## Oracle ARM A1：可选 Compose 服务

以下命令是手动部署步骤，不会由测试自动执行。资源起点按 4 OCPU / 24 GB 设计：Laya 限制 3 CPU、8 GB 内存、3 个推理线程和 2 个接纳请求；这些不是 A1 性能保证。首次下载与构建需预留磁盘和时间。

基础 `compose.yaml` 不含 Laya，旧部署方式不变。可选文件增加独立服务与模型缓存，通过专用 Docker 网络访问，不发布 Laya 主机端口；模型下载需要出站网络。TAdmin 仍保留原 Mini App 回环端口。

1. 按[部署方式](./deployment)准备 `tadmin.env`。已有文件直接保留。
2. 首次创建本地配置：`cp .env.laya.example .env.laya`，填写随机生成的 `LAYA_API_KEY`。不要复制云端 Key。
3. 保持示例的 `LAYA_PLATFORM=linux/arm64`。在 AMD64 开发机本地验证时改成 `linux/amd64`；模拟 ARM 构建不能证明 A1 性能。

::: code-group
<<< @/../.env.laya.example{dotenv} [显式资源与版本参数]
<<< @/../compose.laya.yaml{yaml} [可选 Compose 文件]
:::

先只启动推理服务并验证（不会启动 TAdmin）：

```sh
docker compose --env-file .env --env-file .env.laya -f compose.yaml -f compose.laya.yaml config --quiet
docker compose --env-file .env --env-file .env.laya -f compose.yaml -f compose.laya.yaml up -d --build --wait laya
docker compose --env-file .env --env-file .env.laya -f compose.yaml -f compose.laya.yaml exec -T laya python /opt/tadmin/laya-smoke.py
```

Smoke 使用合成资料，检查认证后的实际 CPU 设备、模型 revision、路由和一次真实推理。它的 120 秒超时允许首次预热；这不是 TAdmin 的业务超时。`/health` 成功只说明进程可响应，不能替代这个检查。

准备接入机器人时才执行：

```sh
docker compose --env-file .env --env-file .env.laya -f compose.yaml -f compose.laya.yaml up -d --build tadmin
```

可选文件为两个服务注入相同本地密钥，并显式选择 Laya。基础文件的 `tadmin.env` 必须存在，但只启动 `laya` 不会连接 Telegram。不要把 `docker compose config` 的完整展开输出发给他人，其中包含密钥；校验用 `config --quiet`。

## 固定版本与输入完整性

示例固定 Laya 0.4.10 的源码 commit 与独立 `convaiinnovations/laya-multilingual` 仓库的模型 commit。服务通过 `LAYA_EXTRA_MODELS` 把 `multilingual` 映射到这个独立仓库；不要把 bundled 仓库的 revision 与它混用。底层基础镜像和传递依赖仍由上游 Dockerfile 解析，这不是逐字节可复现的依赖锁。

客户端发送原有五个 `noul` 问题；Laya 专用请求预算为 `max_len=2048`、`head_max_len=1024`，Jev 请求不增加这些字段。`LAYA_MAX_TOKEN_BUDGET` 只是服务允许的上限，不能替代客户端预算。

保留 `LAYA_JEV_STRICT=0`。客户端要求 `routing.model=multilingual`、非空来源仓库与 `usage.truncated`，拒绝截断、丢失状态 token、缺少答案或越界分数。模型来源读取 `routing.repo`，不把顶层通用名称 `laya-rl-agent` 当作模型版本。部署 revision 用认证的 `/health` 与 smoke 单独确认。

当前字段长度限制不等于 token 限制。对不完整输入的处理是跳过本次 AI 提示，继续正常验证；不会把不完整结果当成低风险证明。五个信号的最大值仍只是风险提示值，不是已经校准的违规概率。

**已知限制：上述检查仅能拒绝服务报告的截断。** 固定 Laya 版本另把每个选项描述硬截断为 48 token，`usage.truncated=false` 不能证明标准完整，增大 head 预算也不能解除这个上限。原长标准下正常资料高分已独立复现；去掉选项截断仍不能解决。短 noul 有改善，但二选一 choice 存在漏报和选项顺序敏感性，详见 [完整诊断与复现记录](./laya-diagnosis)。当前问题和阈值保持原样，仅用于提示实验。

HTTP 请求最多等待 4 秒；入群资料查询与推理共用外层 5 秒预算。服务启动时预加载，并手动 smoke 预热。401、503、超时和无效响应均不重试、不自动切云端，入群继续走原有验证码／审核流程。Laya 无法在预算内完成时，先测 CPU、输入长度和排队时间，不要直接增加阻塞时间。

## 本地测试与真实请求验收

常规回归使用本地假 HTTP 和 Telegram 服务，不需要真实 Token：

```sh
go test ./internal/config ./internal/intel ./internal/gate ./internal/bot
```

`TestLayaLive` 默认跳过。需要验证 **TAdmin 的完整五个问题** 时，在开发机额外启动一个只绑定回环的测试实例；不要同时运行同一模型服务的多个副本争抢 CPU：

```sh
docker compose --env-file .env --env-file .env.laya -f compose.yaml -f compose.laya.yaml run --rm --service-ports --publish 127.0.0.1:18000:8000 laya
```

在另一终端设置下列测试变量。Key 填本地测试服务的密钥，通过环境注入；不要把真实值写进仓库或共享终端记录。

```sh
export TADMIN_LAYA_TEST_URL=http://127.0.0.1:18000
export TADMIN_LAYA_TEST_KEY='<本地测试密钥>'
go test ./internal/intel -run '^TestLayaLive$' -count=1 -v
```

该测试仅使用合成中英文资料，检查五项结果、截断和路由，并报告耗时；不声称模型分类准确。Windows 开发遵循项目的 `linux-task.ps1 -Mode build -Project '<Windows 仓库路径>' -Command '<上述 Linux 测试命令>'` 流程。A1 原生性能、真实群资料准确率与概率校准仍需另行验收。

### 首次本地验证记录（2026-10-17）

固定源码和模型版本在 Docker Desktop 的 Linux AMD64 CPU 容器完成构建与 smoke。TAdmin 的三条合成中英文资料请求通过完整协议校验，单次耗时约 1.5–1.6 秒，未报告截断；这不是 A1 基准测试。

正常中文简介、广告简介、正常英文简介的最大风险值分别为 `0.9999`、`1`、`1`。这些结果表明当前原样迁移的问法与模型组合存在明显误报，接口通过不等于判断可用。保持仅提示，后续需要带标签样本评估、问法对照与校准，不能据此开启处罚。

后续已完成 [80 条短简介的中英文标注评测](./laya-evaluation)，按五项分别统计原问法与短 noul 的误报、漏报；该评测不替换运行时问题。

[冻结 140 条资料的 Jev 对照基线](./jev-laya-baseline) 已使用同一短 noul v1 完成真实调用。当前 Laya 在正常资料误报、角色声明漏报上明显落后，建议保留 Jev，停止对该 checkpoint 继续微调提示词；保留 Laya 接入与证据，运行时问题和仅提示边界不变。

TAdmin 已通过 Linux ARM64 交叉编译。Laya ARM64 镜像构建在本地 AMD64 Docker 上因执行 `/bin/sh` 返回 `exec format error` 而中止，尚未验证 ARM64 镜像或 A1 原生推理；在 ARM64 主机原生构建后执行上述 smoke 与完整请求测试。

## 切回 Jev

在 `tadmin.env` 恢复 `TADMIN_TYPESAFE_PROVIDER=jev`、Jev Key，并清除 Laya 的地址与模型覆盖（或明确填写 Jev 地址和模型）。省略可选 Compose 文件重建 TAdmin：

```sh
docker compose -f compose.yaml up -d --build --force-recreate tadmin
docker compose --env-file .env --env-file .env.laya -f compose.yaml -f compose.laya.yaml stop laya
```

不要使用 `down -v`，以免删除数据库和模型缓存。切回后检查每群 AI 模式，Jev 的 `review` 会恢复高风险转人工行为。

上游依据：[TypeSafe HTTP API](https://docs.typesafe.ai/api)、[Laya HTTP API](https://nandhakishorm.github.io/laya/http-api/)、[ARM64 部署](https://nandhakishorm.github.io/laya/docker-platforms/)、[模型卡与已知限制](https://huggingface.co/convaiinnovations/laya-multilingual)。
