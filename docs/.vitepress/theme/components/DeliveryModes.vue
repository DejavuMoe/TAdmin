<script setup lang="ts">
import { computed, ref } from 'vue'

type Fact = { k: string; v: string; tone?: 'good' | 'warn' }
type Mode = {
  id: string
  name: string
  badge?: string
  summary: string
  facts: Fact[]
  chat: { from?: string; text: string; kind?: 'bot' | 'eph' | 'service' | 'pm' | 'app'; buttons?: string[] }[]
}

const modes: Mode[] = [
  {
    id: 'overlay', name: '浮层', badge: '默认',
    summary: '群里只留一行欢迎提示，题目以“仅本人可见”的临时消息弹出。新人点击“开始验证”时，题目覆盖在提示上，保证送达当前客户端。',
    facts: [
      { k: '群内打扰', v: '一行提示，结束后自动删除', tone: 'good' },
      { k: '需要域名', v: '不需要', tone: 'good' },
      { k: '兼容性', v: '依赖 Bot API 10.2 临时消息；失败自动回退私聊', tone: 'warn' },
    ],
    chat: [
      { kind: 'service', text: 'Alice 加入了群组' },
      { kind: 'bot', from: 'TAdmin', text: '👋 欢迎 Alice！点下方按钮完成验证（只有你能看到题目）', buttons: ['🔐 开始验证'] },
      { kind: 'eph', text: '🧩 依次点击：三角形 7 → 圆形 2 → 星形 5' },
    ],
  },
  {
    id: 'public', name: '群内',
    summary: '验证图直接发在群里，只有新人本人能作答，管理员可在同一条消息上放行或移除。兼容性最好。',
    facts: [
      { k: '群内打扰', v: '一张验证图，结束后删除' },
      { k: '需要域名', v: '不需要', tone: 'good' },
      { k: '兼容性', v: '所有客户端', tone: 'good' },
    ],
    chat: [
      { kind: 'service', text: 'Alice 加入了群组' },
      { kind: 'bot', from: 'TAdmin', text: '🔐 入群验证 · 🖼 3×3 验证图\nAlice，依次点击：三角形 7 → 圆形 2 → 星形 5', buttons: ['·', '·', '·', '↺ 重选', '🆘 需要帮助'] },
    ],
  },
  {
    id: 'private', name: '私聊',
    summary: '群里提示新人到私聊完成验证。适合希望群内尽量安静、又不依赖临时消息的群。',
    facts: [
      { k: '群内打扰', v: '一行提示' },
      { k: '需要域名', v: '不需要；配置后私聊多出 Mini App 大图', tone: 'good' },
      { k: '兼容性', v: '所有客户端；新人需要点一次“开始”', tone: 'warn' },
    ],
    chat: [
      { kind: 'bot', from: 'TAdmin', text: '👋 欢迎 Alice！请在私聊中完成人机验证', buttons: ['🔐 前往私聊验证'] },
      { kind: 'pm', from: '私聊 · TAdmin', text: '🖼 验证图 + 九宫格按钮', buttons: ['🖼 打开大图验证'] },
    ],
  },
  {
    id: 'request', name: '入群申请', badge: '零窗口',
    summary: '群开启“需要审批”后，申请者在私聊收到题目，答对即自动批准。验证完成前根本不在群里，没有任何可发言的窗口。',
    facts: [
      { k: '群内打扰', v: '无', tone: 'good' },
      { k: '需要域名', v: '不需要', tone: 'good' },
      { k: '限制', v: 'Telegram 只允许在 5 分钟内私聊申请者', tone: 'warn' },
    ],
    chat: [
      { kind: 'pm', from: '私聊 · TAdmin', text: '🔐 你正在申请加入「开源爱好者交流群」，请先完成验证' },
      { kind: 'pm', from: '私聊 · TAdmin', text: '✅ 验证通过，入群申请已批准，欢迎加入！' },
    ],
  },
  {
    id: 'guard', name: 'Guard', badge: 'Mini App',
    summary: '把 TAdmin 指派为群的 Guard 机器人（Bot API 10.1）：申请时 10 秒内直接弹出 Mini App 验证页，体验最顺滑。',
    facts: [
      { k: '群内打扰', v: '无', tone: 'good' },
      { k: '需要域名', v: '需要 HTTPS 域名（web.public_url）', tone: 'warn' },
      { k: '兼容性', v: 'Guard 由 Telegram 客户端设置；失败时回退为普通申请', tone: 'warn' },
    ],
    chat: [
      { kind: 'app', text: '🛡 开源爱好者交流群 · 入群验证\n⏳ 02:48 · ●●○\n[ 3×3 大图点选 ]' },
    ],
  },
]

const current = ref(modes[0].id)
const mode = computed(() => modes.find((m) => m.id === current.value)!)
</script>

<template>
  <div class="modes t-card">
    <div class="tabs" role="tablist" aria-label="投递方式">
      <button v-for="m in modes" :key="m.id" role="tab" :aria-selected="current === m.id" :class="{ on: current === m.id }"
        @click="current = m.id">
        {{ m.name }}<small v-if="m.badge">{{ m.badge }}</small>
      </button>
    </div>
    <Transition name="fade" mode="out-in">
      <div :key="mode.id" class="pane">
        <div class="chat" aria-hidden="true">
          <template v-for="(c, i) in mode.chat" :key="i">
            <div v-if="c.kind === 'service'" class="service">{{ c.text }}</div>
            <div v-else class="bubble" :class="c.kind">
              <span v-if="c.kind === 'eph'" class="tag">👁 仅新人可见</span>
              <span v-if="c.from" class="from">{{ c.from }}</span>
              <span class="text">{{ c.text }}</span>
              <div v-if="c.buttons" class="kb">
                <span v-for="(b, j) in c.buttons" :key="j">{{ b }}</span>
              </div>
            </div>
          </template>
        </div>
        <div class="info">
          <p class="summary">{{ mode.summary }}</p>
          <dl>
            <template v-for="f in mode.facts" :key="f.k">
              <dt>{{ f.k }}</dt>
              <dd :class="f.tone"><span class="dot" />{{ f.v }}</dd>
            </template>
          </dl>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.modes {
  padding: 0;
  overflow: hidden;
  margin: 20px 0;
}
.tabs {
  display: flex;
  gap: 2px;
  padding: 6px;
  border-bottom: 1px solid var(--vp-c-divider);
  overflow-x: auto;
}
.tabs button {
  flex: 1;
  min-width: max-content;
  padding: 8px 14px;
  border-radius: 10px;
  font-weight: 600;
  font-size: 14px;
  color: var(--vp-c-text-2);
  transition: all 0.2s;
}
.tabs button small {
  margin-left: 6px;
  font-size: 11px;
  font-weight: 600;
  padding: 1px 6px;
  border-radius: 6px;
  background: var(--vp-c-default-soft);
}
.tabs button.on {
  background: var(--vp-c-bg);
  color: var(--vp-c-brand-1);
  box-shadow: var(--tadmin-shadow);
}
.tabs button.on small {
  background: var(--vp-c-brand-soft);
}
.pane {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr);
  gap: 20px;
  padding: 20px;
}
@media (max-width: 720px) {
  .pane {
    grid-template-columns: 1fr;
  }
}
.chat {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 14px;
  border-radius: 14px;
  background: var(--tg-bg);
  background-image: radial-gradient(var(--tg-pattern) 1.2px, transparent 1.2px);
  background-size: 14px 14px;
  min-height: 210px;
  font-size: 13px;
  line-height: 1.5;
}
.service {
  align-self: center;
  background: rgba(0, 0, 0, 0.18);
  color: #fff;
  border-radius: 999px;
  padding: 1px 10px;
  font-size: 12px;
}
.bubble {
  align-self: flex-start;
  max-width: 92%;
  background: var(--tg-bubble);
  color: var(--tg-bubble-text);
  border-radius: 12px 12px 12px 4px;
  padding: 7px 10px;
  white-space: pre-line;
}
.bubble.eph {
  background: var(--tg-eph);
  border: 1px dashed rgba(109, 93, 252, 0.55);
}
.bubble.pm {
  border-left: 3px solid var(--tadmin-sky);
}
.bubble.app {
  align-self: stretch;
  max-width: none;
  text-align: center;
  padding: 18px;
  border-radius: 16px;
  background: linear-gradient(160deg, var(--tg-bubble), var(--tg-eph));
  font-weight: 600;
}
.tag {
  display: block;
  font-size: 11px;
  font-weight: 700;
  color: #6d5dfc;
}
.from {
  display: block;
  font-size: 12px;
  font-weight: 600;
  color: var(--vp-c-brand-1);
}
.kb {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 6px;
}
.kb span {
  flex: 1 1 auto;
  text-align: center;
  padding: 4px 8px;
  border-radius: 7px;
  background: var(--tg-button);
  color: var(--tg-button-text);
  font-weight: 600;
  font-size: 12px;
}
.summary {
  margin: 0 0 14px;
  line-height: 1.75;
}
dl {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 8px 16px;
  margin: 0;
  font-size: 14px;
}
dt {
  color: var(--vp-c-text-3);
}
dd {
  margin: 0;
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--status-neutral);
  transform: translateY(-1px);
}
dd.good .dot {
  background: var(--status-good);
}
dd.warn .dot {
  background: var(--status-warning);
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s, transform 0.2s;
}
.fade-enter-from {
  opacity: 0;
  transform: translateY(6px);
}
.fade-leave-to {
  opacity: 0;
}
</style>
