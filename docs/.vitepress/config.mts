import { defineConfig, type DefaultTheme } from 'vitepress'
import meta from './data/generated/meta.json'

const sidebar: DefaultTheme.Sidebar = {
  '/guide/': [
    {
      text: '开始',
      items: [
        { text: 'TAdmin 是什么', link: '/guide/' },
        { text: '五分钟上手', link: '/guide/quick-start' },
      ],
    },
    {
      text: '部署与配置',
      items: [
        { text: '部署方式', link: '/guide/deployment' },
        { text: 'Laya 实验记录（暂停）', link: '/guide/laya' },
        { text: '配置参考', link: '/guide/configuration' },
        { text: '权限与角色', link: '/guide/permissions' },
      ],
    },
  ],
  '/features/': [
    {
      text: '入群验证',
      items: [
        { text: '验证流程', link: '/features/verification' },
        { text: '题型与试玩', link: '/features/challenges' },
        { text: '防护与安全', link: '/features/protection' },
      ],
    },
    {
      text: '更多功能',
      items: [
        { text: '频道联动', link: '/features/channel' },
        { text: '链接预览刷新', link: '/features/preview' },
        { text: '私聊控制台', link: '/features/panel' },
        { text: '欢迎语与消息清理', link: '/features/welcome' },
        { text: '命令一览', link: '/features/commands' },
      ],
    },
  ],
  '/operations/': [
    {
      text: '运维',
      items: [
        { text: '日常运维', link: '/operations/' },
        { text: '备份与恢复', link: '/operations/backup' },
        { text: '故障排查', link: '/operations/troubleshooting' },
        { text: '上线验收清单', link: '/operations/acceptance' },
        { text: 'CI 与发版', link: '/operations/releasing' },
        { text: '本地发布准备验收', link: '/operations/preflight' },
      ],
    },
  ],
  '/design/': [
    {
      text: '设计',
      items: [
        { text: '架构总览', link: '/design/' },
        { text: '当前功能架构图', link: '/design/current-map' },
        { text: '入群状态机', link: '/design/state-machine' },
        { text: '安全模型', link: '/design/security' },
        { text: '数据与存储', link: '/design/data' },
        { text: '测试与文档同步', link: '/design/testing' },
      ],
    },
  ],
  '/progress/': [
    {
      text: '进度',
      items: [
        { text: '项目进度', link: '/progress/' },
        { text: '更新日志', link: '/progress/changelog' },
      ],
    },
  ],
}

export default defineConfig({
  lang: 'zh-CN',
  title: 'TAdmin',
  titleTemplate: ':title · TAdmin',
  description: '为个人自托管打造的 Telegram 群管家：只守门，不审查。',
  cleanUrls: true,
  srcExclude: ['**/README.md'],
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' }],
    ['meta', { name: 'theme-color', content: '#2a78d6' }],
    ['meta', { property: 'og:title', content: 'TAdmin · Telegram 群管家' }],
    ['meta', { property: 'og:description', content: '一个二进制 + 一个 SQLite。仅本人可见的浮层验证、频道置顶清理、链接预览刷新。' }],
  ],
  markdown: {
    lineNumbers: false,
    theme: { light: 'github-light', dark: 'github-dark' },
    container: {
      tipLabel: '提示',
      warningLabel: '注意',
      dangerLabel: '警告',
      infoLabel: '说明',
      detailsLabel: '详情',
    },
  },
  themeConfig: {
    logo: { src: '/logo.svg', alt: 'TAdmin' },
    siteTitle: 'TAdmin',
    nav: [
      { text: '指南', link: '/guide/', activeMatch: '/guide/' },
      { text: '功能', link: '/features/verification', activeMatch: '/features/' },
      { text: '运维', link: '/operations/', activeMatch: '/operations/' },
      { text: '设计', link: '/design/', activeMatch: '/design/' },
      {
        text: meta.version,
        items: [
          { text: '项目进度', link: '/progress/' },
          { text: '更新日志', link: '/progress/changelog' },
        ],
      },
    ],
    sidebar,
    outline: { level: [2, 3], label: '本页目录' },
    search: {
      provider: 'local',
      options: {
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            noResultsText: '没有找到相关结果',
            resetButtonTitle: '清除',
            footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' },
          },
        },
      },
    },
    docFooter: { prev: '上一页', next: '下一页' },
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到浅色',
    darkModeSwitchTitle: '切换到深色',
    sidebarMenuLabel: '目录',
    returnToTopLabel: '回到顶部',
    langMenuLabel: '语言',
    notFound: {
      title: '页面不存在',
      quote: '这里什么也没有——就像通过验证之前的群聊权限。',
      linkText: '回到首页',
    },
    footer: {
      message: `文档中的配置、命令、数据结构、测试统计与验证码样例由 <code>cmd/docgen</code> 从代码生成 · 数据同步于 ${meta.updated}`,
      copyright: 'TAdmin · 为个人自托管打造',
    },
  },
})
