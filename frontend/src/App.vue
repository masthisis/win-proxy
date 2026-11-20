<script setup lang="ts">
/** App shell: custom titlebar, sidebar navigation, current view, overlays. */
import { computed, onMounted, type Component } from "vue";
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

onMounted(() => {
  void bootstrap();
});
</script>

<template>
  <div class="shell">
    <TitleBar />
    <div class="body">
      <aside class="sidebar">
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
        <div v-if="store.appInfo" class="sidebar-foot">
          v{{ store.appInfo.version }}
          <span v-if="store.engineStatus?.coreVersion" class="dim">· core {{ store.engineStatus.coreVersion }}</span>
        </div>
      </aside>

      <div class="main">
        <main class="content">
          <component :is="currentView" />
        </main>
      </div>
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
