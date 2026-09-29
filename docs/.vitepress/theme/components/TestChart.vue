<script setup lang="ts">
import { ref } from 'vue'
import tests from '../../data/generated/tests.json'

const max = Math.max(...tests.packages.map((p) => p.tests.length))
const hover = ref<string | null>(null)
const view = ref<'chart' | 'table'>('chart')
const short = (p: string) => p.replace(/^internal\//, '')
</script>

<template>
  <figure class="tests t-card">
    <figcaption>
      <div>
        <strong>各包的测试函数数量</strong>
        <small>共 {{ tests.total }} 个，分布在 {{ tests.packages.length }} 个包 · 悬停查看测试名</small>
      </div>
      <div class="toggle" role="group" aria-label="视图">
        <button :class="{ on: view === 'chart' }" @click="view = 'chart'">图表</button>
        <button :class="{ on: view === 'table' }" @click="view = 'table'">表格</button>
      </div>
    </figcaption>
    <div v-if="view === 'chart'" class="rows">
      <div v-for="p in tests.packages" :key="p.package" class="row" tabindex="0"
        :aria-label="`${short(p.package)}：${p.tests.length} 个测试`"
        @mouseenter="hover = p.package" @mouseleave="hover = null" @focusin="hover = p.package" @focusout="hover = null">
        <span class="cat">{{ short(p.package) }}</span>
        <div class="track">
          <span class="bar" :class="{ dim: hover && hover !== p.package }" :style="{ width: `${(p.tests.length / max) * 100}%` }" />
          <span class="val" :style="{ left: `${(p.tests.length / max) * 100}%` }">{{ p.tests.length }}</span>
          <div v-if="hover === p.package" class="tip" role="tooltip">
            <b>{{ p.package }}</b>
            <span>{{ p.tests.slice(0, 8).join(' · ') }}<template v-if="p.tests.length > 8"> … 等 {{ p.tests.length }} 个</template></span>
          </div>
        </div>
      </div>
    </div>
    <table v-else>
      <thead><tr><th>包</th><th>测试数</th><th>测试函数</th></tr></thead>
      <tbody>
        <tr v-for="p in tests.packages" :key="p.package">
          <td><code>{{ p.package }}</code></td>
          <td>{{ p.tests.length }}</td>
          <td class="names">{{ p.tests.join(', ') }}</td>
        </tr>
      </tbody>
    </table>
    <p class="t-sync">由 docgen 解析 <code>*_test.go</code> 得到</p>
  </figure>
</template>

<style scoped>
.tests {
  margin: 20px 0;
}
figcaption {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 14px;
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
  gap: 4px;
}
.row {
  display: grid;
  grid-template-columns: 128px 1fr;
  gap: 10px;
  align-items: center;
  outline: none;
}
.cat {
  font-family: var(--vp-font-family-mono);
  font-size: 12.5px;
  color: var(--vp-c-text-2);
  text-align: right;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.track {
  position: relative;
  height: 20px;
  margin-right: 34px;
}
.bar {
  position: absolute;
  left: 0;
  top: 3px;
  height: 14px;
  border-radius: 0 4px 4px 0;
  background: var(--viz-series);
  transition: opacity 0.2s;
}
.bar.dim {
  opacity: 0.35;
}
.val {
  position: absolute;
  top: 0;
  padding-left: 6px;
  font-size: 12.5px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.tip {
  position: absolute;
  left: 0;
  bottom: 24px;
  z-index: 2;
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-width: 440px;
  padding: 8px 10px;
  border-radius: 8px;
  font-size: 12px;
  line-height: 1.5;
  background: var(--vp-c-bg-elv);
  border: 1px solid var(--vp-c-divider);
  box-shadow: var(--tadmin-shadow);
  pointer-events: none;
}
.tip span {
  color: var(--vp-c-text-2);
  font-family: var(--vp-font-family-mono);
}
.names {
  font-family: var(--vp-font-family-mono);
  font-size: 12px;
}
.t-sync {
  margin: 12px 0 0;
}
</style>
