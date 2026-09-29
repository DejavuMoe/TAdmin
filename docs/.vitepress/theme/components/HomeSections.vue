<script setup lang="ts">
import { ref } from 'vue'
import { withBase } from 'vitepress'
import meta from '../../data/generated/meta.json'
import progress from '../../data/generated/progress.json'
import DeliveryModes from './DeliveryModes.vue'

const stats = [
  { n: String(meta.counts.tests), label: '自动化测试', note: '含整机端到端' },
  { n: String(meta.counts.bot_api_methods), label: 'Bot API 方法', note: '自研客户端' },
  { n: '1', label: '静态二进制', note: '+ 一个 SQLite 文件' },
  { n: '0', label: '外部服务', note: '无需域名 / Redis / 数据库' },
]

const install = `cp tadmin.env.example tadmin.env   # 填入 Token 与你的 ID
docker compose up -d`
const copied = ref(false)
async function copy() {
  await navigator.clipboard.writeText(install)
  copied.value = true
  setTimeout(() => (copied.value = false), 1600)
}

const statusMeta: Record<string, { label: string; icon: string }> = {
  verified: { label: '已验证', icon: '✓' },
  live: { label: '待实机验收', icon: '◐' },
  shipped: { label: '已上线', icon: '★' },
  planned: { label: '规划中', icon: '○' },
}
const total = progress.features.length
const order = ['shipped', 'verified', 'live', 'planned'].filter((s) => (progress.counts as Record<string, number>)[s])
</script>

<template>
  <div class="home-sections">
    <section class="stats" aria-label="项目数据">
      <div v-for="s in stats" :key="s.label" class="stat">
        <strong>{{ s.n }}</strong>
        <span>{{ s.label }}</span>
        <small>{{ s.note }}</small>
      </div>
    </section>

    <section class="block">
      <p class="eyebrow">五种投递方式</p>
      <h2>同一套验证，按你的群选择打扰程度</h2>
      <p class="sub">从“群里只有一行字”到“验证完之前根本进不来”。随时在控制台里切换，立即生效。</p>
      <DeliveryModes />
    </section>

    <section class="block split">
      <div>
        <p class="eyebrow">三步开始</p>
        <h2>不用改配置文件，也不用域名</h2>
        <ol class="steps">
          <li><b>@BotFather</b> 创建机器人，拿到 Token；<b>@userinfobot</b> 查到你的数字 ID。</li>
          <li>运行右边两行命令（或直接启动单个二进制）。</li>
          <li>把机器人拉进群并设为管理员——权限检查卡片会自动私聊给你。</li>
        </ol>
        <a class="link" :href="withBase('/guide/quick-start')">查看完整上手指南 →</a>
      </div>
      <div class="terminal">
        <div class="bar"><i /><i /><i /><button @click="copy">{{ copied ? '已复制 ✓' : '复制' }}</button></div>
        <pre><code>{{ install }}</code></pre>
      </div>
    </section>

    <section class="block">
      <p class="eyebrow">项目进度 · {{ progress.updated }}</p>
      <h2>{{ progress.version }}</h2>
      <p class="sub">{{ progress.headline }}</p>
      <div class="bar-stack" role="img" :aria-label="order.map((s) => `${statusMeta[s].label} ${(progress.counts as any)[s]} 项`).join('，')">
        <span v-for="s in order" :key="s" :class="`seg ${s}`" :style="{ flexGrow: (progress.counts as any)[s] }" />
      </div>
      <div class="legend">
        <span v-for="s in order" :key="s" class="t-chip">
          <i :class="`sw ${s}`">{{ statusMeta[s].icon }}</i>{{ statusMeta[s].label }} {{ (progress.counts as any)[s] }} / {{ total }}
        </span>
      </div>
      <a class="link" :href="withBase('/progress/')">查看每项功能对应的测试 →</a>
    </section>
  </div>
</template>

<style scoped>
.home-sections {
  max-width: 1152px;
  margin: 0 auto;
  padding: 0 24px 64px;
}
@media (min-width: 640px) {
  .home-sections {
    padding: 0 48px 80px;
  }
}
.stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 1px;
  margin-top: 56px;
  border: 1px solid var(--vp-c-divider);
  border-radius: var(--tadmin-radius);
  overflow: hidden;
  background: var(--vp-c-divider);
}
@media (max-width: 720px) {
  .stats {
    grid-template-columns: repeat(2, 1fr);
  }
}
.stat {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 22px 24px;
  background: var(--vp-c-bg);
}
.stat strong {
  font-size: 34px;
  line-height: 1.1;
  font-weight: 700;
  letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
}
.stat span {
  font-weight: 600;
}
.stat small {
  color: var(--vp-c-text-3);
}
.block {
  margin-top: 80px;
}
.eyebrow {
  margin: 0;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: var(--vp-c-brand-1);
}
.block h2 {
  margin: 6px 0 0;
  font-size: 28px;
  line-height: 1.3;
  font-weight: 700;
  letter-spacing: -0.01em;
}
.sub {
  margin: 10px 0 0;
  max-width: 720px;
  color: var(--vp-c-text-2);
  line-height: 1.75;
}
.split {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 40px;
  align-items: center;
}
@media (max-width: 860px) {
  .split {
    grid-template-columns: 1fr;
  }
}
.steps {
  margin: 18px 0;
  padding: 0;
  list-style: none;
  counter-reset: s;
}
.steps li {
  counter-increment: s;
  position: relative;
  padding: 0 0 14px 40px;
  line-height: 1.7;
}
.steps li::before {
  content: counter(s);
  position: absolute;
  left: 0;
  top: 0;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 13px;
  font-weight: 700;
  color: #fff;
  background: linear-gradient(135deg, var(--tadmin-sky), var(--tadmin-accent));
}
.link {
  font-weight: 600;
  color: var(--vp-c-brand-1);
}
.terminal {
  border-radius: var(--tadmin-radius);
  overflow: hidden;
  background: #0d1117;
  box-shadow: var(--tadmin-shadow);
  border: 1px solid rgba(255, 255, 255, 0.06);
}
.terminal .bar {
  display: flex;
  gap: 6px;
  align-items: center;
  padding: 10px 12px;
  background: #161b22;
}
.terminal .bar i {
  width: 11px;
  height: 11px;
  border-radius: 50%;
  background: #30363d;
}
.terminal .bar button {
  margin-left: auto;
  font-size: 12px;
  color: #8b949e;
  border: 1px solid #30363d;
  border-radius: 6px;
  padding: 1px 10px;
}
.terminal .bar button:hover {
  color: #e6edf3;
}
.terminal pre {
  margin: 0;
  padding: 18px 20px 22px;
  color: #e6edf3;
  font-family: var(--vp-font-family-mono);
  font-size: 14px;
  line-height: 1.8;
  overflow-x: auto;
}
.bar-stack {
  display: flex;
  gap: 2px;
  height: 14px;
  margin-top: 22px;
  max-width: 720px;
}
.seg {
  border-radius: 4px;
  min-width: 6px;
}
.seg.verified,
.sw.verified {
  background: var(--status-good);
}
.seg.live,
.sw.live {
  background: var(--status-warning);
}
.seg.shipped,
.sw.shipped {
  background: var(--viz-series);
}
.seg.planned,
.sw.planned {
  background: var(--status-neutral);
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 12px 0 16px;
}
.sw {
  display: inline-grid;
  place-items: center;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  color: #fff;
  font-style: normal;
  font-size: 10px;
  font-weight: 700;
}
</style>
