<script setup lang="ts">
import { ref } from 'vue'

type Node = { icon: string; name: string; desc: string; owner?: boolean; children?: Node[] }
const tree: Node[] = [
  {
    icon: '👥', name: '群组管理', desc: '所有受管群及其状态、进行中的验证数',
    children: [
      { icon: '🔐', name: '入群验证', desc: '开关、投递方式、题型、时限、次数、失败处理、冷却、入群申请、Mini App、试玩' },
      { icon: '👋', name: '欢迎语', desc: '开关、可见范围、自动删除、编辑文案与按钮、预览' },
      { icon: '📢', name: '频道联动', desc: '自动置顶策略、一键清理频道置顶、取消全部置顶、拦截频道马甲' },
      { icon: '🧹', name: '消息清理', desc: '入群 / 退群 / 置顶 / 其他服务消息、管理命令' },
      { icon: '🛡', name: '防护与安全', desc: '突击防护阈值与时长、CAS、拦截机器人、AI 资料风险、群内语言' },
      { icon: '📊', name: '统计', desc: '14 天入群、通过、拦截迷你柱状图与中位用时' },
      { icon: '📋', name: '待处理', desc: '进行中的验证，一键放行 / 移除 / 封禁' },
      { icon: '🔍', name: '权限检查', desc: '逐项检查机器人权限与讨论群设置' },
      { icon: '⏸', name: '暂停 / 恢复', desc: '暂停只阻止新验证，已有流程照常收尾' },
      { icon: '🗑', name: '移除群组', desc: '退出该群并删除设置与统计', owner: true },
    ],
  },
  { icon: '📊', name: '统计', desc: '全部群汇总与各群对比', owner: true },
  {
    icon: '🔄', name: '预览刷新', desc: '队列、今日刷新数、限流状态', owner: true,
    children: [
      { icon: '📚', name: '订阅源', desc: '立即检查、全部排队、暂停、删除' },
      { icon: '➕', name: '添加订阅源', desc: '发送网址，自动发现 sitemap / Feed，选择是否刷新现有链接' },
      { icon: '📜', name: '最近记录', desc: '最近 15 次刷新结果' },
      { icon: '👤', name: '用户号', desc: '扫码登录 / 手机号 + 数字键盘 / 退出登录' },
    ],
  },
  { icon: '⚙️', name: '系统', desc: '版本、运行时长、数据库大小、一键备份、更新命令菜单', owner: true },
]
const open = ref<string | null>('群组管理')
</script>

<template>
  <div class="map">
    <div class="root">🛡 控制台首页 <small>今日入群 · 通过 · 拦截 · 进行中 · 需要处理的提醒</small></div>
    <div v-for="n in tree" :key="n.name" class="branch">
      <button class="node" :aria-expanded="n.children ? open === n.name : undefined" @click="open = open === n.name ? null : n.name">
        <span class="ic">{{ n.icon }}</span>
        <span class="nm">{{ n.name }}</span>
        <span v-if="n.owner" class="own">仅所有者</span>
        <span class="ds">{{ n.desc }}</span>
        <span v-if="n.children" class="chev" :class="{ open: open === n.name }">›</span>
      </button>
      <div v-if="n.children && open === n.name" class="kids">
        <div v-for="c in n.children" :key="c.name" class="kid">
          <span class="ic">{{ c.icon }}</span>
          <span class="nm">{{ c.name }}</span>
          <span v-if="c.owner" class="own">仅所有者</span>
          <span class="ds">{{ c.desc }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.map {
  margin: 20px 0;
  font-size: 14px;
}
.root {
  font-weight: 700;
  padding: 10px 14px;
  border-radius: 12px;
  color: #fff;
  background: linear-gradient(120deg, #1f8fe0, #6d5dfc);
}
.root small {
  margin-left: 8px;
  font-weight: 400;
  opacity: 0.85;
}
.branch {
  margin-left: 18px;
  border-left: 2px solid var(--vp-c-divider);
  padding: 6px 0 0 14px;
}
.node,
.kid {
  display: flex;
  align-items: baseline;
  gap: 8px;
  width: 100%;
  text-align: left;
  padding: 8px 12px;
  border-radius: 10px;
  border: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg);
}
.node:hover {
  border-color: var(--vp-c-brand-2);
}
.nm {
  font-weight: 600;
  white-space: nowrap;
}
.ds {
  color: var(--vp-c-text-2);
  font-size: 13px;
  flex: 1;
}
.own {
  font-size: 11px;
  padding: 0 6px;
  border-radius: 6px;
  background: var(--vp-c-default-soft);
  color: var(--vp-c-text-3);
  white-space: nowrap;
}
.chev {
  color: var(--vp-c-text-3);
  transition: transform 0.2s;
}
.chev.open {
  transform: rotate(90deg);
}
.kids {
  display: grid;
  gap: 6px;
  margin: 6px 0 4px 16px;
}
.kid {
  background: var(--vp-c-bg-soft);
}
</style>
