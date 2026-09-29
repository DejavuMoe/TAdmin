<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'

type NodeId = 'new' | 'challenge' | 'review' | 'resolving' | 'closed'
const nodes: Record<NodeId, { x: number; y: number; title: string; sub: string; detail: string[] }> = {
  new: {
    x: 70, y: 110, title: 'new', sub: '准备',
    detail: [
      '检测到直接入群（chat_member）或入群申请后，先把流程写进 SQLite，再做任何外部动作——崩溃也不会丢。',
      '直接入群：读取成员当前权限并先保存（区分管理员原有限制与 TAdmin 自己的禁言），然后立即禁言，禁言自带到期时间。',
      '可选检查：CAS 黑名单、AI 资料风险。检查只在调度器里跑，不拖慢更新循环。',
      '投递题目：浮层 / 群内 / 私聊 / 申请私聊 / Guard Mini App；永久性投递失败直接转人工审核。',
    ],
  },
  challenge: {
    x: 290, y: 110, title: 'challenge', sub: '答题中',
    detail: [
      '题目由 seed + 代数确定性生成，数据库只存 seed，答案从不下发。',
      '答错：代数 +1 换新题，截止时间不变；次数用尽后移出，或按设置转人工审核。',
      '多个视图（临时消息、群内消息、私聊、Mini App）共享同一状态；旧代数的点击只会刷新视图，不计入次数。',
      '截止时间到达由调度器按 next_at 触发超时。',
    ],
  },
  review: {
    x: 415, y: 240, title: 'review', sub: '人工审核',
    detail: [
      '触发：新人点“需要帮助”、答错用尽且设置为转人工、AI 判定高风险（答对后仍需确认）、题目无法投递。',
      '直接入群的审核卡就地编辑群内提示消息，本群管理员一键放行 / 移除 / 封禁；入群申请的审核卡私聊所有者。',
      '禁言期限会延长到审核窗口结束；到期无人处理默认移出，但“题目无法投递”到期会放行——不因机器人的故障惩罚用户。',
    ],
  },
  resolving: {
    x: 540, y: 110, title: 'resolving', sub: '收尾',
    detail: [
      '带结果（pass / approve / fail / timeout / reject / ban / left / cas / cancel）与已完成步骤位图。',
      '步骤依次执行且各自幂等：① Telegram 动作（解除/恢复原限制/移出/批准/拒绝）② 清理或定稿消息 ③ 欢迎语 ④ 统计事件。',
      '临时失败指数退避重试；永久失败告警所有者。带 until_date 的调用不在客户端内部重试 429，避免过期时间跌破 30 秒被当作永久封禁。',
    ],
  },
  closed: {
    x: 740, y: 110, title: 'closed', sub: '结束',
    detail: [
      '部分唯一索引保证同一 (群, 用户) 同时只有一个未结束流程；结束后可以重新开始。',
      '已结束流程保留 30 天，用于统计与递增冷却（24 小时内的失败次数）。',
    ],
  },
}

type EdgeId = 'e1' | 'e2' | 'e3' | 'e4' | 'e5' | 'e6' | 'e7'
const edges: Record<EdgeId, { d: string; label: string; lx: number; ly: number; anchor?: string }> = {
  e1: { d: 'M128,110 L230,110', label: '禁言 · 投递', lx: 179, ly: 99 },
  e2: { d: 'M348,110 L480,110', label: '答对 / 超时 / 用尽', lx: 414, ly: 99 },
  e3: { d: 'M598,110 L680,110', label: '收尾', lx: 639, ly: 99 },
  e4: { d: 'M290,135 Q290,240 355,240', label: '求助 / 转人工', lx: 280, ly: 214, anchor: 'end' },
  e5: { d: 'M473,240 Q540,240 540,137', label: '放行 / 移除 / 到期', lx: 552, ly: 214, anchor: 'start' },
  e6: { d: 'M70,85 C70,8 540,8 540,83', label: 'CAS 命中 / 已离开 / 升为管理员', lx: 430, ly: 24 },
  e7: { d: 'M268,85 C248,40 332,40 312,85', label: '答错换题', lx: 290, ly: 46 },
}

const scenarios = [
  { id: 'pass', name: '正常通过', path: ['new', 'e1', 'challenge', 'e2', 'resolving', 'e3', 'closed'] },
  { id: 'fail', name: '答错用尽', path: ['new', 'e1', 'challenge', 'e7', 'challenge', 'e7', 'challenge', 'e2', 'resolving', 'e3', 'closed'] },
  { id: 'help', name: '求助审核', path: ['new', 'e1', 'challenge', 'e4', 'review', 'e5', 'resolving', 'e3', 'closed'] },
  { id: 'cas', name: 'CAS 命中', path: ['new', 'e6', 'resolving', 'e3', 'closed'] },
]

const selected = ref<NodeId>('new')
const active = ref<string[]>([])
const playing = ref('')
let timer: ReturnType<typeof setInterval> | undefined

function play(id: string) {
  clearInterval(timer)
  const s = scenarios.find((x) => x.id === id)!
  playing.value = id
  active.value = []
  let i = 0
  const tick = () => {
    const item = s.path[i]
    active.value = [...active.value, item]
    if (item in nodes) selected.value = item as NodeId
    i++
    if (i >= s.path.length) {
      clearInterval(timer)
      playing.value = ''
    }
  }
  tick()
  timer = setInterval(tick, 700)
}
onBeforeUnmount(() => clearInterval(timer))

const current = computed(() => nodes[selected.value])
const isActive = (id: string) => active.value.includes(id)
</script>

<template>
  <div class="sm t-card">
    <div class="controls" role="group" aria-label="播放场景">
      <span class="label">播放场景</span>
      <button v-for="s in scenarios" :key="s.id" class="t-chip" :class="{ on: playing === s.id }" @click="play(s.id)">
        ▶ {{ s.name }}
      </button>
    </div>
    <svg viewBox="0 0 810 280" role="img" aria-label="入群流程状态机：new、challenge、review、resolving、closed">
      <defs>
        <marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M0,0 L10,5 L0,10 z" class="arrowhead" />
        </marker>
        <marker id="arrow-on" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M0,0 L10,5 L0,10 z" class="arrowhead on" />
        </marker>
      </defs>
      <g v-for="(e, id) in edges" :key="id" class="edge" :class="{ on: isActive(id) }">
        <path :d="e.d" :marker-end="isActive(id) ? 'url(#arrow-on)' : 'url(#arrow)'" />
        <text :x="e.lx" :y="e.ly" :text-anchor="e.anchor ?? 'middle'">{{ e.label }}</text>
      </g>
      <g v-for="(n, id) in nodes" :key="id" class="node" :class="{ on: isActive(id), sel: selected === id }"
        tabindex="0" role="button" :aria-label="`${n.title}：${n.sub}`" @click="selected = id" @keydown.enter="selected = id">
        <rect :x="n.x - 58" :y="n.y - 25" width="116" height="50" rx="14" />
        <text :x="n.x" :y="n.y - 3" text-anchor="middle" class="t">{{ n.title }}</text>
        <text :x="n.x" :y="n.y + 15" text-anchor="middle" class="s">{{ n.sub }}</text>
      </g>
    </svg>
    <div class="detail" aria-live="polite">
      <h4><code>{{ current.title }}</code> · {{ current.sub }}</h4>
      <ul>
        <li v-for="(d, i) in current.detail" :key="i">{{ d }}</li>
      </ul>
    </div>
  </div>
</template>

<style scoped>
.sm {
  margin: 20px 0;
}
.controls {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.label {
  font-size: 13px;
  color: var(--vp-c-text-3);
  margin-right: 4px;
}
svg {
  width: 100%;
  height: auto;
  overflow: visible;
}
.edge path {
  fill: none;
  stroke: var(--vp-c-text-3);
  stroke-width: 2;
  opacity: 0.55;
  transition: stroke 0.3s, opacity 0.3s;
}
.edge text {
  font-size: 13px;
  fill: var(--vp-c-text-3);
}
.edge.on path {
  stroke: var(--viz-series);
  opacity: 1;
  stroke-width: 2.5;
  stroke-dasharray: 6 5;
  animation: flow 0.8s linear infinite;
}
.edge.on text {
  fill: var(--vp-c-brand-1);
  font-weight: 600;
}
.arrowhead {
  fill: var(--vp-c-text-3);
}
.arrowhead.on {
  fill: var(--viz-series);
}
.node {
  cursor: pointer;
  outline: none;
}
.node rect {
  fill: var(--vp-c-bg);
  stroke: var(--vp-c-divider);
  stroke-width: 1.5;
  transition: all 0.25s;
}
.node .t {
  font-family: var(--vp-font-family-mono);
  font-size: 15px;
  font-weight: 700;
  fill: var(--vp-c-text-1);
}
.node .s {
  font-size: 12.5px;
  fill: var(--vp-c-text-2);
}
.node:hover rect,
.node:focus-visible rect {
  stroke: var(--vp-c-brand-2);
}
.node.on rect {
  fill: var(--vp-c-brand-soft);
  stroke: var(--viz-series);
}
.node.sel rect {
  stroke: var(--vp-c-brand-1);
  stroke-width: 2.5;
}
.detail {
  border-top: 1px solid var(--vp-c-divider);
  margin-top: 8px;
  padding-top: 12px;
}
.detail h4 {
  margin: 0 0 6px;
  font-size: 15px;
}
.detail ul {
  margin: 0;
  padding-left: 1.2em;
}
.detail li {
  margin: 4px 0;
  line-height: 1.7;
  font-size: 14px;
}
@keyframes flow {
  to {
    stroke-dashoffset: -22;
  }
}
@media (prefers-reduced-motion: reduce) {
  .edge.on path {
    animation: none;
  }
}
</style>
