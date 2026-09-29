<script setup lang="ts">
import { computed, ref } from 'vue'
import commands from '../../data/generated/commands.json'

const usage: Record<string, string> = {
  start: '/start', panel: '/panel', refresh: '/refresh https://…', backup: '/backup', help: '/help',
  settings: '/settings', status: '/status', pass: '回复成员 → /pass（或 /pass 用户ID）', kick: '回复成员 → /kick',
  ban: '回复成员 → /ban [7d]', mute: '回复成员 → /mute [2h]', unmute: '回复成员 → /unmute',
  unban: '/unban 用户ID', unpin: '/unpin',
}
const lang = ref<'zh-CN' | 'en'>('zh-CN')
const menus = computed(() => (commands as Record<string, { private: any[]; admins: any[] }>)[lang.value])
</script>

<template>
  <div class="cmds">
    <div class="top">
      <span class="t-sync">由 <code>bot.CommandMenus()</code> 生成，与机器人注册的菜单一致</span>
      <div class="toggle" role="group" aria-label="菜单语言">
        <button :class="{ on: lang === 'zh-CN' }" @click="lang = 'zh-CN'">中文菜单</button>
        <button :class="{ on: lang === 'en' }" @click="lang = 'en'">English</button>
      </div>
    </div>
    <div class="cols">
      <div class="t-card">
        <h4>💬 私聊（所有人可见，功能按身份开放）</h4>
        <div v-for="c in menus.private" :key="c.command" class="cmd">
          <code>/{{ c.command }}</code>
          <span>{{ c.description }}</span>
          <small>{{ usage[c.command] }}</small>
        </div>
      </div>
      <div class="t-card">
        <h4>👮 群内（仅管理员可见）</h4>
        <div v-for="c in menus.admins" :key="c.command" class="cmd">
          <code>/{{ c.command }}</code>
          <span>{{ c.description }} <em v-if="c.is_ephemeral" title="命令本身对其他成员不可见">临时</em></span>
          <small>{{ usage[c.command] }}</small>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cmds {
  margin: 20px 0;
}
.top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.toggle {
  display: inline-flex;
  border: 1px solid var(--vp-c-divider);
  border-radius: 8px;
  overflow: hidden;
}
.toggle button {
  padding: 2px 10px;
  font-size: 12.5px;
  color: var(--vp-c-text-2);
}
.toggle button.on {
  background: var(--vp-c-brand-soft);
  color: var(--vp-c-brand-1);
  font-weight: 600;
}
.cols {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
@media (max-width: 760px) {
  .cols {
    grid-template-columns: 1fr;
  }
}
h4 {
  margin: 0 0 10px;
  font-size: 14.5px;
}
.cmd {
  display: grid;
  grid-template-columns: 92px 1fr;
  gap: 2px 10px;
  padding: 8px 0;
  border-top: 1px solid var(--vp-c-divider);
  font-size: 14px;
}
.cmd code {
  grid-row: span 2;
  font-weight: 700;
  color: var(--vp-c-brand-1);
  align-self: start;
}
.cmd small {
  color: var(--vp-c-text-3);
  font-family: var(--vp-font-family-mono);
  font-size: 12px;
}
em {
  font-style: normal;
  font-size: 11px;
  padding: 0 6px;
  border-radius: 6px;
  background: rgba(109, 93, 252, 0.14);
  color: #6d5dfc;
}
.dark em {
  color: #b3a8ff;
}
</style>
