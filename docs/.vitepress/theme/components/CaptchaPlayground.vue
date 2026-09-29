<script setup lang="ts">
import { computed, ref } from 'vue'
import { withBase } from 'vitepress'
import data from '../../data/generated/captcha.json'

const props = withDefaults(defineProps<{ kind?: 'image' | 'math' }>(), { kind: 'image' })
const samples = computed(() => data.samples.filter((s) => s.kind === props.kind))
const index = ref(0)
const sample = computed(() => samples.value[index.value % samples.value.length])
const lang = ref<'zh' | 'en'>('zh')
const picks = ref<number[]>([])
const result = ref<'' | 'ok' | 'bad'>('')
const started = ref(Date.now())
const elapsed = ref(0)
const geo = data.geometry

const cellStyle = (i: number) => {
  const r = Math.floor(i / geo.n), c = i % geo.n
  const pct = (px: number) => `${(px / geo.size) * 100}%`
  return {
    left: pct(geo.margin + c * (geo.cell + geo.gap)),
    top: pct(geo.margin + r * (geo.cell + geo.gap)),
    width: pct(geo.cell),
    height: pct(geo.cell),
  }
}

function check(answer: number[]) {
  const ok = answer.length === sample.value.answer.length && answer.every((v, i) => v === sample.value.answer[i])
  result.value = ok ? 'ok' : 'bad'
  elapsed.value = (Date.now() - started.value) / 1000
}

function tap(cell: number) {
  if (result.value) return
  const i = picks.value.indexOf(cell)
  if (i >= 0) picks.value.splice(i, 1)
  else picks.value.push(cell)
  if (picks.value.length === sample.value.picks) check(picks.value)
}

function choose(option: number) {
  if (result.value) return
  picks.value = [option]
  check([option])
}

function next() {
  index.value++
  reset()
}
function reset() {
  picks.value = []
  result.value = ''
  started.value = Date.now()
}
const rowCol = (i: number) => `第 ${Math.floor(i / 3) + 1} 行第 ${(i % 3) + 1} 列`
</script>

<template>
  <div class="playground t-card">
    <div class="top">
      <span class="t-chip">🎮 真实题目 · 由 Go 渲染器生成</span>
      <div class="toggle" role="group" aria-label="题目语言">
        <button :class="{ on: lang === 'zh' }" @click="lang = 'zh'">中文</button>
        <button :class="{ on: lang === 'en' }" @click="lang = 'en'">English</button>
      </div>
    </div>
    <p class="prompt">{{ lang === 'zh' ? sample.prompt_zh : sample.prompt_en }}</p>

    <div v-if="sample.kind === 'image'" class="board" :class="result">
      <img :src="withBase(sample.image)" alt="3×3 图形点选验证题" width="540" height="540" />
      <button v-for="i in 9" :key="i" class="cell" :style="cellStyle(i - 1)" :aria-label="rowCol(i - 1)"
        :aria-pressed="picks.includes(i - 1)" @click="tap(i - 1)">
        <b v-if="picks.includes(i - 1)">{{ picks.indexOf(i - 1) + 1 }}</b>
      </button>
    </div>
    <template v-else>
      <img class="math" :src="withBase(sample.image)" alt="算术验证题" width="540" height="240" />
      <div class="options">
        <button v-for="(o, i) in sample.options" :key="i" :class="{
          picked: picks[0] === i,
          right: result && i === sample.answer[0],
        }" @click="choose(i)">{{ o }}</button>
      </div>
    </template>

    <div class="footer" aria-live="polite">
      <template v-if="result === 'ok'">
        <span class="res ok">✅ 回答正确，用时 {{ elapsed.toFixed(1) }} 秒</span>
        <button class="btn" @click="next">再来一题</button>
      </template>
      <template v-else-if="result === 'bad'">
        <span class="res bad">❌ 不对哦——真实验证中会换一张新图，已用 1 次机会</span>
        <button class="btn" @click="reset">重试这题</button>
        <button class="btn ghost" @click="next">换一题</button>
      </template>
      <template v-else>
        <span class="muted">{{ sample.kind === 'image' ? `已选 ${picks.length} / ${sample.picks}，再点一次可取消` : '选出正确答案' }}</span>
        <button v-if="picks.length" class="btn ghost" @click="reset">↺ 重选</button>
      </template>
    </div>
  </div>
</template>

<style scoped>
.playground {
  max-width: 460px;
  margin: 20px 0;
}
.top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
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
.prompt {
  margin: 14px 0 12px;
  font-weight: 700;
  font-size: 15.5px;
}
.board {
  position: relative;
  border-radius: 14px;
  overflow: hidden;
  transition: box-shadow 0.3s;
}
.board.ok {
  box-shadow: 0 0 0 3px var(--status-good);
}
.board.bad {
  box-shadow: 0 0 0 3px var(--status-critical);
  animation: shake 0.4s;
}
.board img,
.math {
  display: block;
  width: 100%;
  height: auto;
  border-radius: 14px;
}
.cell {
  position: absolute;
  display: grid;
  place-items: center;
  border-radius: 16px;
  transition: background 0.15s;
}
.cell:hover {
  background: rgba(42, 120, 214, 0.1);
}
.cell:focus-visible {
  outline: 3px solid var(--vp-c-brand-2);
  outline-offset: -3px;
}
.cell b {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: #2a78d6;
  color: #fff;
  font-size: 18px;
  box-shadow: 0 0 0 3px #fff, 0 6px 16px rgba(0, 0, 0, 0.2);
  animation: pop 0.3s cubic-bezier(0.34, 1.56, 0.64, 1);
}
.options {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
  margin-top: 12px;
}
.options button {
  padding: 10px;
  border-radius: 10px;
  border: 1px solid var(--vp-c-divider);
  background: var(--vp-c-bg);
  font-weight: 700;
  font-size: 17px;
  font-variant-numeric: tabular-nums;
  transition: all 0.15s;
}
.options button:hover {
  border-color: var(--vp-c-brand-2);
}
.options button.picked {
  border-color: var(--status-critical);
}
.options button.right {
  border-color: var(--status-good);
  background: rgba(12, 163, 12, 0.1);
}
.footer {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 14px;
  min-height: 34px;
  font-size: 14px;
}
.res {
  font-weight: 600;
  margin-right: auto;
}
.muted {
  color: var(--vp-c-text-3);
  margin-right: auto;
}
.btn {
  padding: 4px 14px;
  border-radius: 8px;
  background: var(--vp-c-brand-2);
  color: #fff;
  font-weight: 600;
  font-size: 13.5px;
}
.btn.ghost {
  background: transparent;
  color: var(--vp-c-text-2);
  border: 1px solid var(--vp-c-divider);
}
@keyframes pop {
  from {
    transform: scale(0.2);
  }
}
@keyframes shake {
  25% {
    transform: translateX(-6px);
  }
  75% {
    transform: translateX(6px);
  }
}
@media (prefers-reduced-motion: reduce) {
  .board.bad,
  .cell b {
    animation: none;
  }
}
</style>
