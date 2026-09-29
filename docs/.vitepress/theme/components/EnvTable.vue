<script setup lang="ts">
import { computed, ref } from 'vue'
import cfg from '../../data/generated/config.json'

const groups: Record<string, string> = {
  required: '必填', core: '基础', web: 'Mini App / HTTP', userbot: '预览刷新用户号', ai: 'AI 风险提示', advanced: '高级',
}
const q = ref('')
const copied = ref('')
const rows = computed(() => {
  const k = q.value.trim().toLowerCase()
  return cfg.env.filter((e) => !k || [e.name, e.toml ?? '', e.desc].some((s) => s.toLowerCase().includes(k)))
})
const grouped = computed(() =>
  Object.entries(groups)
    .map(([id, title]) => ({ id, title, rows: rows.value.filter((r) => r.group === id) }))
    .filter((g) => g.rows.length),
)
async function copy(name: string) {
  await navigator.clipboard.writeText(name)
  copied.value = name
  setTimeout(() => (copied.value = ''), 1400)
}
</script>

<template>
  <div class="env">
    <div class="bar">
      <input v-model="q" type="search" placeholder="搜索变量、配置键或说明…" aria-label="搜索环境变量" />
      <span class="t-sync">共 {{ cfg.env.length }} 项 · 由 <code>config.EnvVars</code> 生成</span>
    </div>
    <section v-for="g in grouped" :key="g.id" class="group">
      <h4>{{ g.title }}</h4>
      <div v-for="e in g.rows" :key="e.name" class="item">
        <div class="head">
          <button class="name" :title="`复制 ${e.name}`" @click="copy(e.name)">
            <code>{{ e.name }}</code><span class="copy">{{ copied === e.name ? '已复制' : '复制' }}</span>
          </button>
          <span v-if="e.required" class="badge req">必填</span>
          <span v-if="e.secret" class="badge secret">🔒 敏感</span>
          <span v-if="e.toml" class="badge toml">config.toml: <code>{{ e.toml }}</code></span>
        </div>
        <p>{{ e.desc }}</p>
        <p v-if="e.example" class="example">示例：<code>{{ e.example }}</code></p>
      </div>
    </section>
    <p v-if="!grouped.length" class="empty">没有匹配“{{ q }}”的配置项。</p>
  </div>
</template>

<style scoped>
.env {
  margin: 20px 0;
}
.bar {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
input {
  flex: 1;
  min-width: 220px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 10px;
  padding: 8px 12px;
  font-size: 14px;
  background: var(--vp-c-bg);
  transition: border-color 0.2s;
}
input:focus {
  border-color: var(--vp-c-brand-2);
  outline: none;
}
.group h4 {
  margin: 22px 0 8px;
  font-size: 13px;
  letter-spacing: 0.06em;
  color: var(--vp-c-text-3);
}
.item {
  padding: 12px 14px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  margin-bottom: 8px;
  background: var(--vp-c-bg);
  transition: border-color 0.2s;
}
.item:hover {
  border-color: var(--vp-c-brand-soft);
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}
.name {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.name code {
  font-size: 14px;
  font-weight: 700;
  color: var(--vp-c-brand-1);
}
.copy {
  font-size: 11px;
  color: var(--vp-c-text-3);
  opacity: 0;
  transition: opacity 0.2s;
}
.name:hover .copy,
.name:focus-visible .copy {
  opacity: 1;
}
.badge {
  font-size: 11.5px;
  border-radius: 6px;
  padding: 0 7px;
  line-height: 20px;
  background: var(--vp-c-default-soft);
  color: var(--vp-c-text-2);
}
.badge.req {
  background: rgba(208, 59, 59, 0.12);
  color: #b42e2e;
}
.dark .badge.req {
  color: #ff8a8a;
}
.badge code {
  font-size: 11px;
  background: none;
  padding: 0;
}
.item p {
  margin: 6px 0 0;
  font-size: 14px;
  line-height: 1.7;
}
.example {
  color: var(--vp-c-text-3);
  font-size: 13px !important;
}
.empty {
  color: var(--vp-c-text-3);
}
</style>
