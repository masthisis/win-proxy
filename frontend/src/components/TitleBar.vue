<script setup lang="ts">
/**
 * Custom titlebar for the borderless window (issue #45).
 *
 * The bar itself is the drag region (`--wails-draggable: drag` — Wails v3
 * intercepts mousedown and runs the native window move; double-click
 * toggles maximize). Interactive children (buttons) explicitly reset the
 * property so they stay clickable.
 */
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { Window, Events } from "@wailsio/runtime";
import { store, cycleProxyMode } from "../store";
import { t } from "../lib/i18n";
import { fmtRate } from "../lib/format";
import TrafficGraph from "./TrafficGraph.vue";
import StatusPill from "./StatusPill.vue";
import UpdateProgress from "./UpdateProgress.vue";
import {
  IconBolt,
  IconDownload,
  IconUpload,
  IconGlobe,
  IconWinMin,
  IconWinMax,
  IconWinRestore,
  IconWinClose,
} from "./Icons";

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

// Issue #121: this used to be a plain const evaluated once at mount, when
// the status had not loaded yet — the pill stayed "unknown" forever. A
// computed tracks core:status events for the component's lifetime.
const engineState = computed(() => store.engineStatus?.state ?? "unknown");
// OS proxy mode (#15): the button cycles off -> system -> pac -> off.
// legacy "" (pre-#15 install) displays as off until first cycle.
const proxyMode = computed(() => store.proxyMode?.mode ?? "");
const proxyOn = computed(() => proxyMode.value === "system" || proxyMode.value === "pac");
const proxyTitle = computed(() =>
  proxyMode.value === "pac" ? t("header.proxyPac")
    : proxyMode.value === "system" ? t("header.proxySystem")
    : t("header.proxyOff"),
);

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
      <IconBolt :size="15" />
      <span class="tb-app">Neuro<em>Rift</em></span>
      <span class="tb-sep" aria-hidden="true" />
      <span class="tb-page">{{ t(`nav.${store.currentView}`) }}</span>
    </div>

    <div class="tb-right">
      <UpdateProgress />
      <div class="traffic-widget" :title="t('header.traffic')">
        <TrafficGraph :down="store.trafficDown" :up="store.trafficUp" :width="88" :height="24" />
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
        class="btn-icon"
        :class="{ on: proxyOn }"
        :disabled="store.proxyModeBusy"
        :aria-pressed="proxyOn"
        :aria-label="proxyTitle"
        :title="proxyTitle"
        @click="cycleProxyMode()"
      >
        <IconGlobe :size="15" />
      </button>
      <StatusPill :state="engineState" />

      <div class="tb-controls">
        <button class="tb-btn" :aria-label="t('window.minimise')" :title="t('window.minimise')" @click="minimise()">
          <IconWinMin :size="14" />
        </button>
        <button
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
