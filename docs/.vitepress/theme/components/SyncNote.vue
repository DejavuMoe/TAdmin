<script setup lang="ts">
defineProps<{ source: string }>()
</script>

<template>
  <p class="sync-note">
    <span class="t-sync">本节内容由代码生成</span>
    <span>来源：<code>{{ source }}</code>，执行 <code>go run ./cmd/docgen</code> 同步；数据过期时 <code>go test ./...</code> 会失败。</span>
  </p>
</template>

<style scoped>
.sync-note {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  align-items: center;
  font-size: 13px;
  color: var(--vp-c-text-3);
  border: 1px dashed var(--vp-c-divider);
  border-radius: 10px;
  padding: 8px 12px;
}
</style>
