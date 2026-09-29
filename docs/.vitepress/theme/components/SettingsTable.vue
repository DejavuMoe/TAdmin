<script setup lang="ts">
import sections from '../../data/generated/settings.json'

const fmt = (v: unknown) => (v === '' ? '""' : typeof v === 'string' ? v : JSON.stringify(v))
</script>

<template>
  <div class="settings">
    <div v-for="s in sections" :key="s.key" class="sec">
      <h4>{{ s.title }} <code v-if="s.key">{{ s.key }}</code></h4>
      <table>
        <thead><tr><th>字段</th><th>默认值</th><th>说明</th></tr></thead>
        <tbody>
          <tr v-for="f in s.fields" :key="f.key">
            <td><code>{{ f.key }}</code></td>
            <td class="val"><code>{{ fmt(f.default) }}</code></td>
            <td>{{ f.desc }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="t-sync">字段与默认值通过反射读取 <code>store.DefaultSettings()</code>；缺少说明的字段会让 docgen 失败</p>
  </div>
</template>

<style scoped>
.sec h4 {
  margin: 24px 0 6px;
  font-size: 15px;
}
.sec h4 code {
  font-size: 12px;
  color: var(--vp-c-text-3);
}
table {
  margin: 0;
  font-size: 14px;
}
td:first-child,
.val {
  white-space: nowrap;
  width: 1%;
}
</style>
