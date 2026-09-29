---
layout: home
title: TAdmin
titleTemplate: Telegram 群管家
hero:
  name: TAdmin
  text: 只守门，不审查
  tagline: 为个人自托管打造的 Telegram 群管家。一个二进制加一个 SQLite 文件，填入 Token 就能跑；所有设置都在私聊里点按钮完成。
  actions:
    - theme: brand
      text: 五分钟上手
      link: /guide/quick-start
    - theme: alt
      text: 试玩验证题
      link: /features/challenges
    - theme: alt
      text: 项目进度
      link: /progress/
features:
  - icon: 🫥
    title: 仅本人可见的浮层验证
    details: 群里只留一行欢迎提示，题目以临时消息弹给新人本人。其他群友看不到刷屏，验证结束提示自动消失。
    link: /features/verification
    linkText: 了解验证流程
  - icon: 🧩
    title: 难绕过、不误伤
    details: 每个目标的数字与形状都在别的格子重复出现，只认数字或只认形状的脚本都找不准。失败只是移出，冷却逐次递增，永不自动永久封禁。
    link: /features/challenges
    linkText: 亲手试一题
  - icon: 📢
    title: 频道置顶一键清理
    details: 关联频道的新帖自动取消置顶或只保留最新一条；一键清理历史频道置顶，管理员自己的置顶原样保留。
    link: /features/channel
    linkText: 查看频道联动
  - icon: 🔄
    title: 链接预览自动刷新
    details: 订阅 sitemap、RSS、Atom 或 JSON Feed，页面更新后通过 @WebpageBot 慢速刷新 Telegram 预览；用户号扫码即可登录。
    link: /features/preview
    linkText: 了解预览刷新
  - icon: 🛡
    title: 可恢复的状态机
    details: 每个新成员的验证都持久化在 SQLite，任意时刻重启都从断点继续；禁言自带到期时间，宕机再久也不会有人被永远禁言。
    link: /design/state-machine
    linkText: 看状态机动画
  - icon: ⚙️
    title: 文档即代码
    details: 配置项、命令、数据结构、测试统计和验证码样例都由 docgen 从源码生成，文档过期时测试直接失败。
    link: /design/testing
    linkText: 了解同步机制
---
