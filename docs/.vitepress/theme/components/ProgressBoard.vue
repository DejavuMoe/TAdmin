<script setup lang="ts">
import { computed, ref } from 'vue'
import progress from '../../data/generated/progress.json'

type Feature = (typeof progress.features)[number]

const statuses: Record<string, { label: string; icon: string; desc: string }> = {
  shipped: { label: '已上线', icon: '★', desc: '已在真实 Telegram 验收通过' },
  verified: { label: '已验证', icon: '✓', desc: '行为完全由自动化测试覆盖' },
  live: { label: '待实机验收', icon: '◐', desc: '离线测试通过，还需真实 Telegram 测试群验收' },
  planned: { label: '规划中', icon: '○', desc: '尚未开始' },
}
const areas: Record<string, string> = {
  verification: '入群验证', channel: '频道联动', preview: '预览刷新', panel: '控制台与管理', platform: '平台与运维',
}
const milestoneIcon: Record<string, string> = { done: '✓', next: '◐', planned: '○' }

const counts = progress.counts as Record<string, number>
const order = ['shipped', 'verified', 'live', 'planned'].filter((s) => counts[s])
const total = progress.features.length
const testCount = new Set(progress.features.flatMap((f) => f.tests.map((t) => t.name))).size

const area = ref('all')
const status = ref('all')
const open = ref<string | null>(null)
const shown = computed(() =>
  progress.features.filter((f) => (area.value === 'all' || f.area === area.value) && (status.value === 'all' || f.status === status.value)),
)
const grouped = computed(() =>
  Object.entries(areas)
    .map(([id, title]) => ({ id, title, items: shown.value.filter((f: Feature) => f.area === id) }))
    .filter((g) => g.items.length),
)
</script>

<template>
  <div class="board">
    <div class="tiles">
      <div v-for="s in order" :key="s" class="tile" :class="s">
        <span class="ico" aria-hidden="true">{{ statuses[s].icon }}</span>
        <strong>{{ counts[s] }}</strong>
        <span class="lbl">{{ statuses[s].label }}</span>
        <small>{{ statuses[s].desc }}</small>
      </div>
      <div class="tile tests">
        <span class="ico" aria-hidden="true">⚙</span>
        <strong>{{ testCount }}</strong>
        <span class="lbl">关联测试</span>
        <small>每项“已验证”都指向真实存在的测试</small>
      </div>
    </div>

    <div class="stack" role="img" :aria-label="order.map((s) => `${statuses[s].label} ${counts[s]} 项`).join('，')">
      <span v-for="s in order" :key="s" :class="`seg ${s}`" :style="{ flexGrow: counts[s] }">
        <em>{{ Math.round((counts[s] / total) * 100) }}%</em>
      </span>
    </div>

    <h3 id="里程碑">里程碑</h3>
    <ol class="timeline">
      <li v-for="m in progress.milestones" :key="m.title" :class="m.status">
        <span class="dot" aria-hidden="true">{{ milestoneIcon[m.status] }}</span>
        <div>
          <strong>{{ m.title }}</strong>
          <span v-if="m.date" class="date">{{ m.date }}</span>
          <span v-else-if="m.status === 'next'" class="date next">进行中</span>
          <p>{{ m.desc }}</p>
        </div>
      </li>
    </ol>

    <h3 id="功能清单">功能清单</h3>
    <div class="filters">
      <div class="row" role="group" aria-label="按领域筛选">
        <button class="t-chip" :class="{ on: area === 'all' }" @click="area = 'all'">全部领域</button>
        <button v-for="(t, id) in areas" :key="id" class="t-chip" :class="{ on: area === id }" @click="area = id">{{ t }}</button>
      </div>
      <div class="row" role="group" aria-label="按状态筛选">
        <button class="t-chip" :class="{ on: status === 'all' }" @click="status = 'all'">全部状态</button>
        <button v-for="s in order" :key="s" class="t-chip" :class="{ on: status === s }" @click="status = s">
          <i :class="`sw ${s}`">{{ statuses[s].icon }}</i>{{ statuses[s].label }}
        </button>
      </div>
    </div>

    <section v-for="g in grouped" :key="g.id" class="area">
      <h4>{{ g.title }} <small>{{ g.items.length }}</small></h4>
      <article v-for="f in g.items" :key="f.title" class="feature" :class="{ open: open === f.title }">
        <button class="fhead" :aria-expanded="open === f.title" @click="open = open === f.title ? null : f.title">
          <span class="pill" :class="f.status"><i aria-hidden="true">{{ statuses[f.status].icon }}</i>{{ statuses[f.status].label }}</span>
          <span class="ftitle">{{ f.title }}</span>
          <span class="ntests">{{ f.tests.length ? `${f.tests.length} 个测试` : '无测试' }}</span>
        </button>
        <div v-if="open === f.title" class="fbody">
          <p v-if="f.live" class="live-note"><b>实机验收要点：</b>{{ f.live }}</p>
          <div v-if="f.tests.length" class="tests">
            <span v-for="t in f.tests" :key="t.name" class="t-chip" :title="t.package"><code>{{ t.name }}</code><small>{{ t.package.replace('internal/', '') }}</small></span>
          </div>
        </div>
      </article>
    </section>
    <p v-if="!grouped.length" class="empty">没有符合筛选条件的功能。</p>
    <p class="t-sync">数据源 <code>docs/progress.toml</code> · 测试引用由 docgen 校验 · 更新于 {{ progress.updated }}</p>
  </div>
</template>

<style scoped>
.board {
  margin: 20px 0;
}
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 10px;
}
.tile {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 16px;
  border-radius: var(--tadmin-radius);
  border: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg-soft);
  overflow: hidden;
}
.tile::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 4px;
  background: var(--c, var(--vp-c-divider));
}
.tile.verified {
  --c: var(--status-good);
}
.tile.live {
  --c: var(--status-warning);
}
.tile.shipped {
  --c: var(--viz-series);
}
.tile.planned {
  --c: var(--status-neutral);
}
.tile.tests {
  --c: var(--tadmin-accent);
}
.tile .ico {
  position: absolute;
  right: 14px;
  top: 12px;
  font-size: 18px;
  color: var(--c);
}
.tile strong {
  font-size: 30px;
  line-height: 1.15;
  font-variant-numeric: tabular-nums;
}
.tile .lbl {
  font-weight: 600;
}
.tile small {
  font-size: 12px;
  color: var(--vp-c-text-3);
  line-height: 1.5;
}
.stack {
  display: flex;
  gap: 2px;
  height: 26px;
  margin: 16px 0 8px;
}
.seg {
  display: grid;
  place-items: center;
  border-radius: 4px;
  min-width: 36px;
}
.seg em {
  font-style: normal;
  font-size: 12px;
  font-weight: 700;
  color: #fff;
}
.seg.live em {
  color: #3b2a00;
}
.seg.verified,
.sw.verified,
.pill.verified i {
  background: var(--status-good);
}
.seg.live,
.sw.live,
.pill.live i {
  background: var(--status-warning);
}
.seg.shipped,
.sw.shipped,
.pill.shipped i {
  background: var(--viz-series);
}
.seg.planned,
.sw.planned,
.pill.planned i {
  background: var(--status-neutral);
}
.timeline {
  list-style: none;
  padding: 0;
  margin: 12px 0 0;
}
.timeline li {
  position: relative;
  display: flex;
  gap: 14px;
  padding-bottom: 18px;
  margin: 0;
}
.timeline li:not(:last-child)::after {
  content: '';
  position: absolute;
  left: 13px;
  top: 30px;
  bottom: 0;
  width: 2px;
  background: var(--vp-c-divider);
}
.timeline .dot {
  flex: none;
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  font-size: 13px;
  font-weight: 700;
  color: #fff;
  background: var(--status-neutral);
}
.timeline .done .dot {
  background: var(--status-good);
}
.timeline .next .dot {
  position: relative;
  background: var(--status-warning);
  color: #3b2a00;
}
/* Compositor-only pulse (transform + opacity): no repaint per frame. */
.timeline .next .dot::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: var(--status-warning);
  opacity: 0;
  animation: pulse 2.4s ease-out 3;
  z-index: -1;
}
.timeline strong {
  font-size: 15px;
}
.timeline .date {
  margin-left: 8px;
  font-size: 12.5px;
  color: var(--vp-c-text-3);
}
.timeline .date.next {
  color: #a86b00;
  font-weight: 600;
}
.dark .timeline .date.next {
  color: var(--status-warning);
}
.timeline p {
  margin: 2px 0 0;
  font-size: 14px;
  line-height: 1.7;
  color: var(--vp-c-text-2);
}
.filters {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 12px 0 8px;
}
.filters .row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.sw,
.pill i {
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
.sw.live,
.pill.live i {
  color: #3b2a00;
}
.area h4 {
  margin: 22px 0 8px;
  font-size: 14px;
}
.area h4 small {
  color: var(--vp-c-text-3);
  font-weight: 400;
}
.feature {
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  margin-bottom: 6px;
  background: var(--vp-c-bg);
  transition: border-color 0.2s;
}
.feature.open,
.feature:hover {
  border-color: var(--vp-c-brand-soft);
}
.fhead {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 10px 14px;
  text-align: left;
}
.pill {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 104px;
  font-size: 12.5px;
  color: var(--vp-c-text-2);
}
.ftitle {
  flex: 1;
  font-weight: 600;
  font-size: 14.5px;
}
.ntests {
  font-size: 12px;
  color: var(--vp-c-text-3);
  white-space: nowrap;
}
.fbody {
  padding: 0 14px 14px;
}
.live-note {
  margin: 0 0 10px;
  font-size: 14px;
  line-height: 1.7;
  padding: 8px 12px;
  border-radius: 8px;
  background: rgba(250, 178, 25, 0.1);
}
.tests {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.tests code {
  font-size: 12px;
  background: none;
  padding: 0;
}
.tests small {
  color: var(--vp-c-text-3);
  font-size: 11px;
}
.empty {
  color: var(--vp-c-text-3);
}
@keyframes pulse {
  0% {
    transform: scale(1);
    opacity: 0.55;
  }
  80%,
  100% {
    transform: scale(1.9);
    opacity: 0;
  }
}
@media (prefers-reduced-motion: reduce) {
  .timeline .next .dot::after {
    animation: none;
  }
}
@media (max-width: 640px) {
  .fhead {
    flex-wrap: wrap;
  }
  .ftitle {
    flex-basis: 100%;
    order: 3;
  }
}
</style>
