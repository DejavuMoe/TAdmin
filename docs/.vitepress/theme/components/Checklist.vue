<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

const props = defineProps<{
  id: string
  items: { title: string; detail?: string }[]
}>()

const done = ref<boolean[]>(props.items.map(() => false))
const key = `tadmin-checklist-${props.id}`
onMounted(() => {
  try {
    const saved = JSON.parse(localStorage.getItem(key) || '[]')
    if (Array.isArray(saved)) done.value = props.items.map((_, i) => !!saved[i])
  } catch {
    /* ignore corrupt storage */
  }
})
watch(done, (v) => localStorage.setItem(key, JSON.stringify(v)), { deep: true })
const count = computed(() => done.value.filter(Boolean).length)
const reset = () => (done.value = props.items.map(() => false))
</script>

<template>
  <div class="checklist t-card">
    <div class="head">
      <div class="meter" role="progressbar" :aria-valuenow="count" aria-valuemin="0" :aria-valuemax="items.length">
        <span :style="{ width: `${(count / items.length) * 100}%` }" />
      </div>
      <span class="n">{{ count }} / {{ items.length }}</span>
      <button v-if="count" class="reset" @click="reset">重置</button>
    </div>
    <label v-for="(it, i) in items" :key="i" class="item" :class="{ done: done[i] }">
      <input v-model="done[i]" type="checkbox" />
      <span class="box" aria-hidden="true">✓</span>
      <span class="text">
        <b>{{ it.title }}</b>
        <small v-if="it.detail">{{ it.detail }}</small>
      </span>
    </label>
    <p class="note">勾选状态只保存在你的浏览器里。</p>
  </div>
</template>

<style scoped>
.checklist {
  margin: 20px 0;
}
.head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 10px;
}
.meter {
  flex: 1;
  height: 8px;
  border-radius: 999px;
  background: var(--vp-c-default-soft);
  overflow: hidden;
}
.meter span {
  display: block;
  height: 100%;
  border-radius: 999px;
  background: var(--status-good);
  transition: width 0.3s;
}
.n {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
  font-size: 14px;
}
.reset {
  font-size: 12.5px;
  color: var(--vp-c-text-3);
}
.item {
  display: flex;
  gap: 12px;
  padding: 10px 4px;
  border-top: 1px solid var(--vp-c-divider);
  cursor: pointer;
}
.item input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}
.box {
  flex: none;
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  margin-top: 2px;
  border-radius: 6px;
  border: 1.5px solid var(--vp-c-text-3);
  color: transparent;
  font-size: 13px;
  font-weight: 700;
  transition: all 0.15s;
}
.item input:focus-visible + .box {
  outline: 2px solid var(--vp-c-brand-2);
  outline-offset: 2px;
}
.item.done .box {
  background: var(--status-good);
  border-color: var(--status-good);
  color: #fff;
}
.text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.6;
}
.text b {
  font-weight: 600;
  font-size: 14.5px;
}
.item.done .text b {
  color: var(--vp-c-text-3);
  text-decoration: line-through;
}
.text small {
  color: var(--vp-c-text-2);
  font-size: 13.5px;
}
.note {
  margin: 8px 0 0;
  font-size: 12.5px;
  color: var(--vp-c-text-3);
}
</style>
