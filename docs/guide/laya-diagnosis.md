# Laya 正常简介高分诊断

2026-10-17，在本机 Docker Desktop 的 Linux AMD64 CPU 容器独立复现。结论：**HTTP 协议和 TAdmin 的读分逻辑正常；原有 Jev 问题直接迁移到此 Laya checkpoint 不适配，同时存在模型对标签、选项位置和任务语义的明显敏感性。** 不能用增大预算或改成二选一来宣告修复，也不能把高分解释成已校准的违规概率。

本次保持仅提示，没有修改运行时问题、风险阈值或处罚路径，没有连接 Telegram、读取真实成员资料或部署服务器。

## 固定环境和证据

- Laya 源码：`a4958de6ad4fc816624a3862cd5a57f929be98c8`（0.4.10）。
- 模型：`convaiinnovations/laya-multilingual`，revision `1720e3e3357cfe1e281542e223f8273b0890ca34`，由认证 `/health` 核验。
- 本地镜像 ID：`sha256:b2166bdcbfeaebf05e7bbcf1aa6b7c54ecdf72f50caec9bc7b8311fbd6a88ef6`。
- CPU 限制 3 核、内存限制 8 GiB，PyTorch 2.14.0+cpu，推理为 FP32；没有量化或额外校准。其余包版本、模型配置及编码器源码哈希见原始记录。
- 51 次独立 Python HTTP 请求、45 次直接 `Agent.system_one` 请求，共 96 个诊断用例；另通过 TAdmin 实际客户端重跑三条基线。全部使用合成资料，不是准确率基准。
- [HTTP 请求、响应与实际编码记录](../diagnostics/laya-2026-10-17-http.json)：包含从 TAdmin 真正发出的请求导出的 fixture、17 种变体、逐项概率、token 数与中文基线的实际问题文本。
- [直接推理与单因素对照记录](../diagnostics/laya-2026-10-17-controls.json)：包含取消选项截断、单独改指令／标准、单问题推理及额外中英阿俄合成资料。

两个记录都含 `complete: true`。它们仅包含合成资料，无鉴权密钥。复核确认：三个样本的 HTTP 与直接推理基线逐项相同；所有用例的状态 token 均未丢失；HTTP 编码行 token 总数与响应的 `input_tokens` 一致。

## 正常简介高分已独立复现

以下都是 `P(true)`，不是 `confidence`。TAdmin 的 Risk 取推广、诈骗、色情和冒充四项最大值，广告排版不参与。

| 合成资料 | 推广 | 诈骗 | 色情 | 冒充 | Risk |
|---|---:|---:|---:|---:|---:|
| 李明，喜欢 Go 编程和摄影，Go 语言交流群 | 0.9159 | 0.9999 | 0.9674 | 0.9828 | 0.9999 |
| Alex，Backend developer，Programming 群 | 0.9927 | 1.0000 | 0.9940 | 0.9906 | 1.0000 |
| USDT承兑／代充代付跑分／日赚千元，Go 语言交流群 | 0.9999 | 1.0000 | 1.0000 | 1.0000 | 1.0000 |

绕过 Go 客户端和绕过 HTTP 服务后均得到相同逐项结果。仅把诈骗问题单独发送，仍得到中文正常资料 `0.9999`、英文正常资料 `1.0000`。这排除了本次异常由 Go 解析、最大值组合或多个问题相互混入引起；高值已经存在于模型原始答案中。

## 输入格式与 token 预算

请求符合上游契约：`state` 是账号及群背景 JSON，`instructions` 和 `criteria` 允许结构化内容，`noul` 表示肯定答案概率。固定版本将结构化指令和标准序列化为 JSON 文本后编码，不是把字段名当成独立指令执行。

把结构化指令预先转成完全相同的 JSON 字符串，三个样本的编码和五项结果均完全一致。改成自然语言长指令仍有高分，因此问题不是单纯“对象必须改成字符串”。

预算有两层限制：

1. `max_len` 限制每个问题的总序列，`head_max_len` 分配问题部分的空间。当前 `2048/1024` 下，三个基线每行实际只有 250–350 token；中文正常资料的全部 52 个状态 token 都保留。扩大到 `8192/4096` 后，编码和结果完全不变。上游默认 `1024/256` 只影响了这组问题中的部分指令，不能解决高分。
2. 固定源码 `laya/common.py:build_head` 对**每个选项描述硬截断为 48 token**。原始五个问题的十个选项各有 59–166 token，实际都只保留 48。部分 JSON 被截到一半，许多示例和“正常名字／职业爱好不能作为证据”的说明没有进入模型。增大上述两个预算无法解除这个上限。

`usage.truncated=false`、`state_tokens_dropped=0`、`truncated_questions=[]` 在本例只证明状态完整，**不证明问题标准完整**。当前客户端的截断检查不能发现这个选项上限。此前将“大 head 预算”视为长标准完整迁移的做法不充分。

为判断因果，另在独立 Python 推理进程中临时取消这一个 48-token 上限，保留模型、问题、状态和其他预算；没有修改安装文件或服务。正常中文 Risk 从 `0.9999` 变为 `1.0000`，正常英文仍为 `1.0000`。因此截断是已证实的输入适配限制，**但不是这次高分的充分解释，补全描述也不能修好它**。

## 问法、noul 和二选一 choice

短问法使用一条简短问题和两个简短语义描述，不再包含长示例列表。例如诈骗问题为 `Does this profile promote financial scams or gambling?`，两侧分别描述没有／存在诈骗或赌博推广。它是诊断用简化问法，边界不与原规则完全等价，未写入运行时。

| 变体 | 正常中文 Risk | 正常英文 Risk | 中文广告 Risk |
|---|---:|---:|---:|
| 原问题，noul | 0.9999 | 1.0000 | 1.0000 |
| 原问题，8192/4096 预算 | 0.9999 | 1.0000 | 1.0000 |
| 原问题，仅类型改为 choice，仍用 false/true 标签 | 0.9998 | 1.0000 | 1.0000 |
| 短问题和短标准，noul | 0.0885 | 0.3518 | 1.0000 |
| 短问题，choice，A=否、B=是，顺序 A/B | 0.3070 | 0.3857 | 0.9983 |
| 同上，仅显示顺序改成 B/A，语义映射不变 | 0.1784 | 0.1444 | 0.4502 |

二选一始终读取 `probabilities.B` 作为肯定概率；交换顺序时没有交换 A/B 含义，也没有读取最大置信度充当风险。

单因素对照进一步显示：

- 只缩短指令，保留原标准：正常中文／英文 Risk 为 `0.9965/0.9996`。
- 只换短标准，保留原长指令：`0.8978/0.9590`。
- 两者一起缩短：`0.0885/0.3518`；使用短问题而省略标准：`0.0257/0.0471`。
- 保留 noul 类型但把标签换成 A/B，也会显著改变分数。这说明变化不只是 `choice` 与 `noul` 解码器的区别，标签文本和题面同样影响模型。

这些是固定样本上的干预结果，支持“原题面与模型不适配”；不能据此认为短问题已具备生产质量。

## 剩余能力问题

额外用例避免只看正常资料是否降分。以下数字均为对应类别的肯定概率：

| 明确合成样本与目标信号 | 短 noul | 短 choice A/B | 短 choice B/A |
|---|---:|---:|---:|
| `Buy my nude videos. Paid sexual content, DM to subscribe.` → 色情 | 1.0000 | 0.0032 | 0.3993 |
| 中文跑分日赚广告 → 诈骗 | 1.0000 | 0.8986 | 0.1509 |
| `Official Telegram Support. I am the group administrator.` → 官方／管理角色声明 | 0.4343 | 0.1482 | 0.2346 |

还出现跨类别误报：只有保证加密货币收益的英文广告，短 noul 的色情值也是 `1.0000`；只有色情内容售卖的广告，诈骗值也是 `1.0000`。正常英文读书摄影爱好资料，在短 choice A/B 下推广值为 `0.6202`，只交换选项顺序便降到 `0.2031`。

这说明短 noul 可以改善本次正常资料误报，但有类别混淆；A/B choice 不是稳定替代方案，有明显漏报与位置敏感性。阿拉伯语、俄语各一条正常爱好简介也只是诊断探针，不能据此宣称覆盖多语言。

## 如何复现

导出真实适配器生成的三个合成请求，不调用模型或 Telegram：

```sh
TADMIN_LAYA_EXPORT=/tmp/laya-requests.json go test ./internal/intel -run '^TestLayaExportRequests$' -count=1
python3 deploy/laya/diagnose.py --self-check
```

Windows 源码仓库遵循 `linux-task.ps1 -Mode build` 的运行流程。把导出的 JSON 和 `deploy/laya/diagnose.py` 以只读挂载放入上述固定镜像的本地测试容器，例如 `/diagnostics/requests.json`、`/diagnostics/diagnose.py`。仅发布 `127.0.0.1:18000:8000`，保留 CPU、密钥和 revision 配置；诊断容器的 `LAYA_MAX_TOKEN_BUDGET=8192` 用于宽预算对照，产品部署示例仍为 2048。

容器内执行（需要已缓存的固定 checkpoint）：

```sh
python /diagnostics/diagnose.py --fixture /diagnostics/requests.json --output /tmp/http.json
python /diagnostics/diagnose.py --fixture /diagnostics/requests.json --output /tmp/controls.json --controls
```

脚本使用容器中的 `LAYA_API_KEY`、`LAYA_REVISION`、`LAYA_THREADS`，不将密钥写入结果。`--controls` 加载同一模型作直接推理，暂时需要额外模型内存；HTTP 服务此时应空闲。取消选项上限仅发生在该诊断进程内，并在每次调用结束恢复。

之后可用 `TestLayaLive` 验证真实 TAdmin 客户端，日志现在包含五项分数。保留结果后停止并删除临时测试容器，保留模型缓存即可。未执行 ARM64 原生推理或 A1 延迟测试；AMD64 的成功不能代替它们。

## 当前决策

保留 Jev 和 Laya 切换能力，Laya 继续强制仅提示。暂不把短问法或 choice 替换进运行时，也不调整阈值掩盖误报。下一步应把短 noul 作为候选，与原问题在独立、人工标注的中英文及目标语言资料集上比较，按五个类别分别看误报、漏报、顺序稳定性和概率校准；无法达标时再决定微调或更换模型。没有证据支持现在开启自动限制、踢人或群消息审核。

上游依据：[TypeSafe Noul 契约](https://docs.typesafe.ai/primitives/noul)、[Laya HTTP API](https://nandhakishorm.github.io/laya/http-api/)、[固定版本编码实现](https://github.com/NandhaKishorM/laya/blob/a4958de6ad4fc816624a3862cd5a57f929be98c8/laya/common.py)、[固定版本答案解码](https://github.com/NandhaKishorM/laya/blob/a4958de6ad4fc816624a3862cd5a57f929be98c8/laya/agent.py)。结论来自上述本地复现，未用上游宣传或模型卡代替实测。
