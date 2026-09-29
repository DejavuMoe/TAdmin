import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import { h } from 'vue'
import './style.css'

import HeroChat from './components/HeroChat.vue'
import HomeSections from './components/HomeSections.vue'
import CaptchaPlayground from './components/CaptchaPlayground.vue'
import DeliveryModes from './components/DeliveryModes.vue'
import StateMachine from './components/StateMachine.vue'
import CooldownChart from './components/CooldownChart.vue'
import EnvTable from './components/EnvTable.vue'
import SettingsTable from './components/SettingsTable.vue'
import CommandTable from './components/CommandTable.vue'
import SchemaView from './components/SchemaView.vue'
import BotApiList from './components/BotApiList.vue'
import ProgressBoard from './components/ProgressBoard.vue'
import TestChart from './components/TestChart.vue'
import Checklist from './components/Checklist.vue'
import PanelMap from './components/PanelMap.vue'
import SyncNote from './components/SyncNote.vue'
import Steps from './components/Steps.vue'

export default {
  extends: DefaultTheme,
  Layout() {
    return h(DefaultTheme.Layout, null, {
      'home-hero-image': () => h(HeroChat),
      'home-features-after': () => h(HomeSections),
    })
  },
  enhanceApp({ app }) {
    const components = {
      CaptchaPlayground, DeliveryModes, StateMachine, CooldownChart, EnvTable, SettingsTable, CommandTable,
      SchemaView, BotApiList, ProgressBoard, TestChart, Checklist, PanelMap, SyncNote, Steps,
    }
    for (const [name, c] of Object.entries(components)) app.component(name, c)
  },
} satisfies Theme
