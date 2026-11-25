<script setup lang="ts">
/**
 * Custom titlebar for the borderless window (issue #45), redesigned to the
 * Stitch "NeuroRift Adaptive Cockpit" system (#123).
 *
 * Compact layout (default): logo chip + page name on the left; theme toggle
 * and window controls on the right — live rates move into the Dashboard
 * hero card per the design.
 *
 * Desktop layout: adds the live traffic pill, the system-proxy pill with a
 * switch (ON restores the last non-off mode; PAC stays selectable in
 * Settings) and the engine status pill.
 *
 * The bar itself is the drag region (`--wails-draggable: drag` — Wails v3
 * intercepts mousedown and runs the native window move; double-click
 * toggles maximize). Interactive children explicitly reset the property.
 */
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { Window } from "@wailsio/runtime";
import {
  store,
  setTheme,
  toggleProxySwitch,
  proxySwitchOn,
  resolvedTheme,
  type ThemeMode,
} from "../store";
import { t } from "../lib/i18n";
import { fmtRate } from "../lib/format";
import TrafficGraph from "./TrafficGraph.vue";
import StatusPill from "./StatusPill.vue";
import UpdateProgress from "./UpdateProgress.vue";
import {
  IconBolt,
  IconContrast,
  IconWinMin,
  IconWinMax,
  IconWinRestore,
  IconWinClose,
} from "./Icons";

const props = defineProps<{ compact?: boolean }>();

const maximised = ref(false);
let unresize: (() => void) | undefined;

async function syncMaximised(): Promise<void> {
  try {
    maximised.value = await Window.IsMaximised();
  } catch {
    /* fall back to the tracked value */
  }
}

async function minimise(): Promise<void> {
  try {
    await Window.Minimise();
  } catch {
    /* ignore */
  }
}

async function toggleMaximise(): Promise<void> {
  try {
    await Window.ToggleMaximise();
    maximised.value = !(maximised.value ?? false);
    window.setTimeout(() => void syncMaximised(), 120);
  } catch {
    /* ignore */
  }
}

/** Close = hide to tray (same as the native close button, issue #32/#45). */
async function hideToTray(): Promise<void> {
  try {
    await Window.Close();
  } catch {
    /* the WindowClosing hook hides the window instead of quitting */
  }
}

// Issue #121: computed — a plain const went stale before core:status arrived.
const engineState = computed(() => store.engineStatus?.state ?? "unknown");
// OS proxy mode (#15): the desktop pill switch toggles on/off; the Settings
// card keeps the full off/system/pac choice, and the compact layout keeps
// the cycle button there.
const proxyOn = computed(() => proxySwitchOn());
const proxyTitle = computed(() =>
  proxyOn.value ? t("header.sysProxyOn") : t("header.sysProxyOff"),
);

/** Compact theme toggle: flip between the two concrete themes. */
async function cycleTheme(): Promise<void> {
  const cur = resolvedTheme((store.settings?.ui?.theme ?? "dark") as ThemeMode);
  await setTheme(cur === "dark" ? "light" : "dark");
}

onMounted(() => {
  void syncMaximised();
  const onResize = (): void => void syncMaximised();
  window.addEventListener("resize", onResize);
  unresize = () => window.removeEventListener("resize", onResize);
});

onBeforeUnmount(() => unresize?.());
</script>

<template>
  <header class="titlebar">
    <div class="tb-left">
      <span class="tb-logo-chip"><IconBolt :size="16" /></span>
      <span class="tb-app">NeuroRift</span>
      <span class="tb-sep" aria-hidden="true">/</span>
      <span class="tb-page">{{ t(`nav.${store.currentView === "traffic" ? "traffic" : store.currentView}`) }}</span>
    </div>

    <div class="tb-right">
      <UpdateProgress />

      <!-- Live traffic pill (desktop layout; compact shows rates in the hero card) -->
      <div v-if="!props.compact" class="traffic-widget" :title="t('header.traffic')">
        <TrafficGraph :down="store.trafficDown" :up="store.trafficUp" :width="88" :height="24" />
        <span class="rates">
          <span class="rate down">
            <b>↓ {{ fmtRate(store.curDown) }}</b>
          </span>
          <span class="rate up">
            <b>↑ {{ fmtRate(store.curUp) }}</b>
          </span>
        </span>
      </div>

      <!-- System proxy pill with switch (desktop layout, #15/#123) -->
      <div v-if="!props.compact" class="proxy-pill">
        <IconGlobe :size="15" />
        <span class="proxy-pill-label">{{ t("header.sysProxyLabel") }}</span>
        <button
          type="button"
          role="switch"
          class="switch"
          :aria-checked="proxyOn"
          :aria-label="proxyTitle"
          :title="proxyTitle"
          :disabled="store.proxyModeBusy"
          @click="toggleProxySwitch()"
        >
          <span class="knob" />
        </button>
      </div>

      <StatusPill v-if="!props.compact" :state="engineState" />

      <!-- Compact theme toggle (design: contrast glyph in the compact bar) -->
      <button
        v-if="props.compact"
        class="tb-theme-btn"
        :aria-label="t('settings.themeToggle')"
        :title="t('settings.themeToggle')"
        @click="cycleTheme()"
      >
        <IconContrast :size="16" />
      </button>

      <div class="tb-controls">
        <button class="tb-btn" :aria-label="t('window.minimise')" :title="t('window.minimise')" @click="minimise()">
          <IconWinMin :size="14" />
        </button>
        <button
          v-if="!props.compact"
          class="tb-btn"
          :aria-label="t('window.maximise')"
          :title="t('window.maximise')"
          @click="toggleMaximise()"
        >
          <IconWinRestore v-if="maximised" :size="13" />
          <IconWinMax v-else :size="13" />
        </button>
        <button class="tb-btn tb-close" :aria-label="t('window.close')" :title="t('window.close')" @click="hideToTray()">
          <IconWinClose :size="14" />
        </button>
      </div>
    </div>
  </header>
</template>

<style scoped>
.proxy-pill {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 3px 12px;
  border: 1px solid var(--outline-variant);
  border-radius: var(--radius-pill);
  background: var(--panel);
  color: var(--on-surface);
  font-size: 12px;
  font-weight: 500;
}

.proxy-pill-label {
  white-space: nowrap;
}

.tb-theme-btn {
  width: 32px;
  height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-sm);
  background: var(--panel);
  border: 1px solid var(--outline-variant);
  color: var(--on-surface-variant);
  transition: color 0.1s ease-out, border-color 0.1s ease-out;
}

.tb-theme-btn:hover {
  color: var(--accent);
  border-color: var(--accent);
}
</style>
