<script setup lang="ts">
import methods from '../../data/generated/botapi.json'

const groups: [string, RegExp][] = [
  ['消息', /^(send|edit|delete|pin|unpin|answerCallback)/],
  ['成员与入群', /(ChatMember|JoinRequest|SenderChat)/],
  ['群信息', /^(getChat|leaveChat)/],
  ['机器人', /^(getMe|getUpdates|setMyCommands|getWebhookInfo|deleteWebhook)/],
]
const bucket = (m: string) => groups.find(([, re]) => re.test(m))?.[0] ?? '其他'
const byGroup = groups
  .map(([g]) => ({ g, items: methods.filter((m) => bucket(m.method) === g) }))
  .concat([{ g: '其他', items: methods.filter((m) => bucket(m.method) === '其他') }])
  .filter((x) => x.items.length)
const isNew = (m: string) => /Ephemeral|JoinRequestQuery|JoinRequestWebApp/.test(m)
</script>

<template>
  <div class="api">
    <div v-for="g in byGroup" :key="g.g" class="grp">
      <h4>{{ g.g }} <small>{{ g.items.length }}</small></h4>
      <div class="chips">
        <a v-for="m in g.items" :key="m.method" class="t-chip" :class="{ fresh: isNew(m.method) }"
          :href="`https://core.telegram.org/bots/api#${m.method.toLowerCase()}`" target="_blank" rel="noopener"
          :title="`tg.Client.${m.func}`">{{ m.method }}</a>
      </div>
    </div>
    <p class="t-sync">共 {{ methods.length }} 个方法 · 扫描自 <code>internal/tg/methods.go</code> · 紫色为 Bot API 10.x 新方法</p>
  </div>
</template>

<style scoped>
.api {
  margin: 20px 0;
}
h4 {
  margin: 16px 0 8px;
  font-size: 14px;
}
h4 small {
  color: var(--vp-c-text-3);
  font-weight: 400;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.chips a {
  font-family: var(--vp-font-family-mono);
  text-decoration: none;
}
.chips a:hover {
  border-color: var(--vp-c-brand-2);
  color: var(--vp-c-brand-1);
}
.chips a.fresh {
  border-color: rgba(109, 93, 252, 0.5);
  color: #6d5dfc;
}
.dark .chips a.fresh {
  color: #b3a8ff;
}
</style>
