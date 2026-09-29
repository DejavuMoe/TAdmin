<script setup lang="ts">
import { computed, ref } from 'vue'
import verify from '../../data/generated/verify.json'

const steps = verify.cooldown
const lo = Math.log(60), hi = Math.log(86400)
const width = (s: number) => `${Math.max(6, ((Math.log(s) - lo) / (hi - lo)) * 100)}%`
const ticks = [
  { s: 60, label: '1 分' }, { s: 600, label: '10 分' }, { s: 3600, label: '1 时' },
  { s: 36000, label: '10 时' }, { s: 86400, label: '24 时' },
].map((t) => ({ ...t, left: `${((Math.log(t.s) - lo) / (hi - lo)) * 100}%` }))

const human = (s: number) => {
  const h = Math.floor(s / 3600), m = Math.round((s % 3600) / 60)
  return h ? (m ? `${h} 小时 ${m} 分钟` : `${h} 小时`) : `${m} 分钟`
}
const hover = ref<number | null>(null)
const view = ref<'chart' | 'table'>('chart')
const capped = computed(() => steps.findIndex((s) => s.seconds >= 86400) + 1)
</script>

<template>
  <figure class="cooldown t-card">
    <figcaption>
      <div>
        <strong>24 小时内第 n 次验证失败后，需要等待多久才能重新加入</strong>
        <small>每次 ×4，下限 {{ verify.min_ban_seconds / 60 }} 分钟，第 {{ capped }} 次起封顶 24 小时 · 横轴为对数刻度</small>
      </div>
      <div class="toggle" role="group" aria-label="视图">
        <button :class="{ on: view === 'chart' }" @click="view = 'chart'">图表</button>
        <button :class="{ on: view === 'table' }" @click="view = 'table'">表格</button>
      </div>
    </figcaption>

    <div v-if="view === 'chart'" class="chart">
      <div class="rows">
        <div v-for="(s, i) in steps" :key="s.failure" class="row" @mouseenter="hover = i" @mouseleave="hover = null"
          @focusin="hover = i" @focusout="hover = null" tabindex="0" :aria-label="`第 ${s.failure} 次失败：${human(s.seconds)}`">
          <span class="cat">第 {{ s.failure }} 次</span>
          <div class="track">
            <span v-for="t in ticks" :key="t.s" class="grid" :style="{ left: t.left }" />
            <span class="bar" :class="{ dim: hover !== null && hover !== i }" :style="{ width: width(s.seconds) }" />
            <span class="val" :style="{ left: width(s.seconds) }">{{ s.label }}</span>
            <div v-if="hover === i" class="tip" :style="{ left: width(s.seconds) }" role="tooltip">
              <b>第 {{ s.failure }} 次失败</b><br />封禁 {{ human(s.seconds) }}，到期自动解除
            </div>
          </div>
        </div>
      </div>
      <div class="axis">
        <span class="cat" />
        <div class="track">
          <span v-for="t in ticks" :key="t.s" class="tick" :style="{ left: t.left }">{{ t.label }}</span>
        </div>
      </div>
    </div>

    <table v-else>
      <thead><tr><th>失败次数（24 小时内）</th><th>封禁时长</th><th>秒</th></tr></thead>
      <tbody>
        <tr v-for="s in steps" :key="s.failure"><td>第 {{ s.failure }} 次</td><td>{{ human(s.seconds) }}</td><td>{{ s.seconds }}</td></tr>
      </tbody>
    </table>
    <p class="t-sync">数据来自 <code>gate.Cooldown()</code></p>
  </figure>
</template>

<style scoped>
.cooldown {
  margin: 20px 0;
}
figcaption {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
  margin-bottom: 16px;
}
figcaption strong {
  display: block;
  font-size: 15px;
}
figcaption small {
  color: var(--vp-c-text-3);
  font-size: 12.5px;
}
.toggle {
  display: inline-flex;
  flex: none;
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
.rows {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.row,
.axis {
  display: grid;
  grid-template-columns: 64px 1fr;
  align-items: center;
  gap: 10px;
  outline: none;
}
.cat {
  font-size: 13px;
  color: var(--vp-c-text-2);
  text-align: right;
}
.track {
  position: relative;
  height: 22px;
  margin-right: 56px;
}
.grid {
  position: absolute;
  top: -3px;
  bottom: -3px;
  width: 1px;
  background: var(--viz-grid);
}
.bar {
  position: absolute;
  left: 0;
  top: 3px;
  height: 16px;
  border-radius: 0 4px 4px 0;
  background: var(--viz-series);
  transition: opacity 0.2s, width 0.4s;
}
.bar.dim {
  opacity: 0.35;
}
.val {
  position: absolute;
  top: 1px;
  padding-left: 8px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--vp-c-text-1);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.tip {
  position: absolute;
  bottom: 28px;
  transform: translateX(-50%);
  z-index: 2;
  padding: 6px 10px;
  border-radius: 8px;
  font-size: 12.5px;
  line-height: 1.5;
  white-space: nowrap;
  color: var(--vp-c-text-1);
  background: var(--vp-c-bg-elv);
  border: 1px solid var(--vp-c-divider);
  box-shadow: var(--tadmin-shadow);
  pointer-events: none;
}
.axis .track {
  height: 18px;
}
.tick {
  position: absolute;
  transform: translateX(-50%);
  font-size: 11.5px;
  color: var(--vp-c-text-3);
  white-space: nowrap;
}
table {
  margin: 0;
}
.t-sync {
  margin: 14px 0 0;
}
</style>
