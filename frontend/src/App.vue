<script setup lang="ts">
/**
 * App shell (#123 Stitch design): dual layout modes sharing the same views.
 *
 * Compact (default) — widget-like window: titlebar with theme toggle,
 * scrolling content, bottom 5-tab bar (Home / Profiles / Tests / Traffic /
 * Settings). Connections and Logs merge under the Traffic tab.
 *
 * Desktop — cockpit: titlebar with live traffic + proxy pills, 224px nav
 * rail with every view (incl. About), fluid content.
 *
 * Theme (light/dark/system) applies via settings.ui; every view, control
 * and feature stays reachable in both layouts.
 */
import { computed, onMounted, watch, type Component } from "vue";
import { store, bootstrap, setView, type ViewId } from "./store";
import { t } from "./lib/i18n";
import { confirmState, resolveConfirm } from "./lib/confirm";
import Toasts from "./components/Toasts.vue";
import Modal from "./components/Modal.vue";
import TitleBar from "./components/TitleBar.vue";
import Dashboard from "./views/Dashboard.vue";
import Profiles from "./views/Profiles.vue";
import Test from "./views/Test.vue";
import Connections from "./views/Connections.vue";
import Logs from "./views/Logs.vue";
import TrafficView from "./views/TrafficView.vue";
import Settings from "./views/Settings.vue";
import About from "./views/About.vue";
import {
  IconDashboard,
  IconProfiles,
  IconTest,
  IconConnections,
  IconLogs,
  IconSettings,
  IconAbout,
} from "./components/Icons";

const views: Record<ViewId, Component> = {
  dashboard: Dashboard,
  profiles: Profiles,
  test: Test,
  connections: Connections,
  logs: Logs,
  traffic: TrafficView,
  settings: Settings,
  about: About,
};

const desktopNav: { id: ViewId; icon: Component; key: string }[] = [
  { id: "dashboard", icon: IconDashboard, key: "nav.dashboard" },
  { id: "profiles", icon: IconProfiles, key: "nav.profiles" },
  { id: "test", icon: IconTest, key: "nav.test" },
  { id: "connections", icon: IconConnections, key: "nav.connections" },
  { id: "logs", icon: IconLogs, key: "nav.logs" },
  { id: "settings", icon: IconSettings, key: "nav.settings" },
  { id: "about", icon: IconAbout, key: "nav.about" },
];

const compactNav: { id: ViewId; icon: Component; key: string }[] = [
  { id: "dashboard", icon: IconDashboard, key: "nav.compactHome" },
  { id: "profiles", icon: IconProfiles, key: "nav.profiles" },
  { id: "test", icon: IconTest, key: "nav.compactTests" },
  { id: "traffic", icon: IconConnections, key: "nav.compactTraffic" },
  { id: "settings", icon: IconSettings, key: "nav.settings" },
];

const layout = computed(() => (store.settings?.ui?.layout ?? "compact") as "compact" | "desktop");

/** Views unreachable from the compact tab bar fall back to their host tab. */
watch(layout, (mode) => {
  if (mode === "compact" && (store.currentView === "connections" || store.currentView === "logs")) {
    store.currentView = "traffic";
  }
  if (mode === "compact" && store.currentView === "about") {
    store.currentView = "settings";
  }
});

const currentView = computed(() => views[store.currentView]);
const compact = computed(() => layout.value === "compact");

onMounted(() => {
  void bootstrap();
});
</script>

<template>
  <div class="shell" :data-layout="layout">
    <TitleBar :compact="compact" />

    <!-- Desktop cockpit: rail + content -->
    <div v-if="!compact" class="body">
      <aside class="sidebar">
        <nav aria-label="Main navigation">
          <button
            v-for="item in desktopNav"
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
        <div class="sidebar-foot">
          <div v-if="store.engineStatus?.activeName" class="rail-profile">
            <span class="rp-label">{{ t("dash.activeProfile") }}</span>
            <span class="rp-name" :title="store.engineStatus.activeName">{{ store.engineStatus.activeName }}</span>
          </div>
          <span v-if="store.appInfo">
            v{{ store.appInfo.version }}
            <span v-if="store.engineStatus?.coreVersion">· core {{ store.engineStatus.coreVersion }}</span>
          </span>
        </div>
      </aside>

      <div class="main">
        <main class="content">
          <component :is="currentView" />
        </main>
      </div>
    </div>

    <!-- Compact widget: content + bottom tab bar -->
    <template v-else>
      <div class="body compact-body">
        <div class="main">
          <main class="content">
            <component :is="currentView" />
          </main>
        </div>
      </div>
      <nav class="tabbar" aria-label="Compact navigation">
        <button
          v-for="item in compactNav"
          :key="item.id"
          class="tabbar-item"
          :class="{ active: store.currentView === item.id }"
          :aria-current="store.currentView === item.id ? 'page' : undefined"
          @click="setView(item.id)"
        >
          <component :is="item.icon" :size="20" />
          <span>{{ t(item.key) }}</span>
        </button>
      </nav>
    </template>

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
