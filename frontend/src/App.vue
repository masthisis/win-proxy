<script setup lang="ts">
/** App shell: sidebar navigation, top header, current view, global overlays. */
import { computed, onMounted, type Component } from "vue";
import { store, bootstrap, setView, toggleSysProxy, engineRunning, type ViewId } from "./store";
import { t } from "./lib/i18n";
import { confirmState, resolveConfirm } from "./lib/confirm";
import { fmtRate } from "./lib/format";
import Toasts from "./components/Toasts.vue";
import Modal from "./components/Modal.vue";
import TrafficGraph from "./components/TrafficGraph.vue";
import StatusPill from "./components/StatusPill.vue";
import Dashboard from "./views/Dashboard.vue";
import Profiles from "./views/Profiles.vue";
import Test from "./views/Test.vue";
import Connections from "./views/Connections.vue";
import Logs from "./views/Logs.vue";
import Settings from "./views/Settings.vue";
import About from "./views/About.vue";
import {
  IconBolt,
  IconDashboard,
  IconProfiles,
  IconTest,
  IconConnections,
  IconLogs,
  IconSettings,
  IconAbout,
  IconDownload,
  IconUpload,
  IconGlobe,
} from "./components/Icons";

const views: Record<ViewId, Component> = {
  dashboard: Dashboard,
  profiles: Profiles,
  test: Test,
  connections: Connections,
  logs: Logs,
  settings: Settings,
  about: About,
};

const nav: { id: ViewId; icon: Component; key: string }[] = [
  { id: "dashboard", icon: IconDashboard, key: "nav.dashboard" },
  { id: "profiles", icon: IconProfiles, key: "nav.profiles" },
  { id: "test", icon: IconTest, key: "nav.test" },
  { id: "connections", icon: IconConnections, key: "nav.connections" },
  { id: "logs", icon: IconLogs, key: "nav.logs" },
  { id: "settings", icon: IconSettings, key: "nav.settings" },
  { id: "about", icon: IconAbout, key: "nav.about" },
];

const currentView = computed(() => views[store.currentView]);
const title = computed(() => t(`nav.${store.currentView}`));
const engineState = computed(() => store.engineStatus?.state ?? "unknown");
const coreVersion = computed(() => store.engineStatus?.coreVersion ?? "");
const sysProxyOn = computed(() => store.sysProxy?.enabled ?? false);
const sysProxyDisabled = computed(() => !sysProxyOn.value && !engineRunning());
const sysProxyTitle = computed(() =>
  sysProxyOn.value ? t("header.sysProxyOn") : t("header.sysProxyOff"),
);

onMounted(() => {
  void bootstrap();
});
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="logo">
        <IconBolt :size="22" />
        <span class="name">Neuro<em>Rift</em></span>
      </div>
      <nav aria-label="Main navigation">
        <button
          v-for="item in nav"
          :key="item.id"
          class="nav-item"
          :class="{ active: store.currentView === item.id }"
          :aria-current="store.currentView === item.id ? 'page' : undefined"
          @click="setView(item.id)"
        >
          <component :is="item.icon" :size="17" />
          <span class="label">{{ t(item.key) }}</span>
        </button>
      </nav>
      <div v-if="store.appInfo" class="sidebar-foot">v{{ store.appInfo.version }}</div>
    </aside>

    <div class="main">
      <header class="topbar">
        <h1 class="page-title">{{ title }}</h1>
        <div class="top-status">
          <div class="traffic-widget" :title="t('header.traffic')">
            <TrafficGraph :down="store.trafficDown" :up="store.trafficUp" :width="96" :height="26" />
            <span class="rates">
              <span class="rate down">
                <IconDownload :size="11" />
                <b>{{ fmtRate(store.curDown) }}</b>
              </span>
              <span class="rate up">
                <IconUpload :size="11" />
                <b>{{ fmtRate(store.curUp) }}</b>
              </span>
            </span>
          </div>
          <button
            class="pill-btn"
            :title="t('nav.about')"
            :aria-label="t('nav.about')"
            @click="setView('about')"
          >
            <StatusPill :state="engineState" />
          </button>
          <span v-if="coreVersion" class="dim small mono">v{{ coreVersion }}</span>
          <button
            class="btn-icon"
            :class="{ on: sysProxyOn }"
            :disabled="sysProxyDisabled"
            :aria-pressed="sysProxyOn"
            :aria-label="t('header.sysProxy')"
            :title="sysProxyTitle"
            @click="toggleSysProxy()"
          >
            <IconGlobe :size="16" />
          </button>
        </div>
      </header>

      <main class="content">
        <component :is="currentView" />
      </main>
    </div>

    <Toasts />

    <Modal :open="confirmState.open" :title="confirmState.title" @close="resolveConfirm(false)">
      <p>{{ confirmState.body }}</p>
      <template #actions>
        <button class="btn" @click="resolveConfirm(false)">{{ t("common.cancel") }}</button>
        <button
          class="btn"
          :class="confirmState.danger ? 'danger' : 'primary'"
          @click="resolveConfirm(true)"
        >
          {{ confirmState.confirmLabel || t("common.confirm") }}
        </button>
      </template>
    </Modal>
  </div>
</template>
