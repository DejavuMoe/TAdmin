# 真实 API 验收记录

日期：2026-09-17（Asia/Singapore）。本记录仅列实际完成的检查，不替代完整入群验收。真实 Token、群 ID、用户 ID 和完整 API 响应不写入本文件。

| 检查 | 实际结果 |
|---|---|
| Bot 身份与本机 API 连接 | getMe 成功 |
| Webhook | 未设置，支持当前 Long Polling 启动方式 |
| 来源群 | 可访问的无话题 supergroup，关联频道讨论群 |
| 来源群机器人权限 | administrator，具备 can_restrict_members、can_invite_users、can_delete_messages |
| 配置的所有者身份 | 在来源群中为 creator |
| 讨论群入口 | join_to_send_messages=true；join_by_request=true，保留原设置 |
| 所有者私聊信息 | getChat 成功；这不代表已允许机器人主动发消息 |
| 所有者私聊发送/编辑 | 首次返回 403；所有者 Start 后，含 Web App 按钮的发送及编辑均成功 |
| 本机服务 | 使用真实 Token 启动成功，监听 127.0.0.1:8080 |
| 本机健康检查 | HTTP 200，status=ok |
| 重启 | SIGTERM 正常退出；SQLite 离线检查通过；重启后健康检查通过 |
| 群接入状态 | enabled_groups=0，join_cases=0；尚未启用群验证 |

## 继续验收所需条件

1. 所有者私聊已打通，无需重复此项。
2. 临时 Cloudflare HTTPS 页面验收已完成并关闭。正式使用前配置稳定 HTTPS 入口；当前 `public_base_url` 已恢复为空，群验证未启用。
3. 当前没有额外普通测试账号。所有者/管理员不会被当作普通新成员验证；完整的入群、验证码、恢复和移除仍未验收。

本机服务是本次测试启动的普通进程，没有安装系统服务。受保护日志位于 `data/live-test/service.log`，测试数据库位于 `data/live-test/tadmin.db`；两者均在 Git 忽略目录内。修改 HTTPS 配置后需要重启进程才能生效。

完整验收项目见 [verification.md](verification.md)。

## 2026-09-17 真实 Telegram Mini App 页面验收通过

- 用户从机器人私聊中的 Web App 按钮打开临时 HTTPS 页面，提供了真实 Telegram 客户端的加载及成功截图。
- 服务端核对：`status=challenge_passed`、`attempts=1`、`consumed_at` 已写入、`challenge_secret` 已置空，绑定用户与配置中的所有者一致。
- 成功时间：2026-09-17 07:57:21（Asia/Singapore）。完成后再次请求题目图片返回 HTTP 409。
- 此会话为 `owner_ui_check`，有效期 15 分钟，仅给本人检查实际 Telegram initData 鉴权、页面加载和提交；未生成 join case。普通入群验证默认仍为 180 秒。
- 最终 `enabled_groups=0`、`join_cases=0`。页面的通用“恢复发言权限”提示不作为群权限 API 已执行的证据；本次没有进行成员权限操作。
- 临时 cloudflared 进程已结束，临时 Nginx 代理容器已移除；仅恢复 public_base_url 原值，其余配置及 Token 保留。本机机器人以原配置重启，健康检查通过，处于未启用群验证的状态。

剩余验收是普通成员的真实入群/申请、对应限制与放行、失败/超时处理及人工审核；不能用本次所有者页面测试替代。
