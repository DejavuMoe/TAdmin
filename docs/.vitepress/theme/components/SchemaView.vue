<script setup lang="ts">
import { ref } from 'vue'
import schema from '../../data/generated/schema.json'

const purpose: Record<string, string> = {
  meta: '键值：更新 offset、用户号会话、首次问候标记',
  chats: '受管群：状态、关联频道、设置 JSON、防护期、权限问题',
  gates: '入群流程状态机（每个新成员一行）',
  events: '审计与统计事件（保留 90 天）',
  deletions: '定时删除的消息（欢迎语、提示、临时消息兜底）',
  channel_pins: '关联频道自动转发的消息 ID',
  preview_sources: '预览刷新订阅源（sitemap / Feed）',
  preview_urls: '被追踪的链接与刷新队列',
}
const open = ref<string>('gates')
</script>

<template>
  <div class="schema">
    <div v-for="t in schema" :key="t.name" class="tbl" :class="{ open: open === t.name }">
      <button class="head" :aria-expanded="open === t.name" @click="open = open === t.name ? '' : t.name">
        <code>{{ t.name }}</code>
        <span class="purpose">{{ purpose[t.name] }}</span>
        <span class="count">{{ t.columns.length }} 列</span>
        <span class="chev">›</span>
      </button>
      <div v-if="open === t.name" class="cols">
        <span v-for="c in t.columns" :key="c.name" class="col"><code>{{ c.name }}</code><small>{{ c.type }}</small></span>
      </div>
    </div>
    <p class="t-sync">解析自 <code>assets/migrations/*.sql</code></p>
  </div>
</template>

<style scoped>
.schema {
  margin: 20px 0;
}
.tbl {
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  margin-bottom: 8px;
  overflow: hidden;
  background: var(--vp-c-bg);
}
.head {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 10px 14px;
  text-align: left;
}
.head code {
  font-weight: 700;
  color: var(--vp-c-brand-1);
  min-width: 128px;
}
.purpose {
  flex: 1;
  font-size: 14px;
  color: var(--vp-c-text-2);
}
.count {
  font-size: 12px;
  color: var(--vp-c-text-3);
}
.chev {
  transition: transform 0.2s;
  color: var(--vp-c-text-3);
}
.open .chev {
  transform: rotate(90deg);
}
.cols {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 4px 14px 14px;
}
.col {
  display: inline-flex;
  gap: 6px;
  align-items: baseline;
  border: 1px solid var(--vp-c-divider);
  border-radius: 8px;
  padding: 2px 8px;
  font-size: 13px;
}
.col small {
  color: var(--vp-c-text-3);
  font-size: 11px;
}
</style>
