<script setup lang="ts">
/** About: app info, core/engine state, update check, external links. */
import { computed, onMounted, ref } from "vue";
import * as Core from "../../bindings/github.com/neurorift/neurorift/coreservice.js";
import * as Sys from "../../bindings/github.com/neurorift/neurorift/sysservice.js";
import type { CoreUpdateInfo } from "../../bindings/github.com/neurorift/neurorift/models.js";
import { store, refreshAppInfo } from "../store";
import { t } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import { fmtDuration, fmtBytes } from "../lib/format";
import StatusPill from "../components/StatusPill.vue";
import {
  IconBolt,
  IconExternal,
  IconRefresh,
  IconDownload,
  IconWarn,
  IconCheck,
} from "../components/Icons";

const LINKS = [
  { url: "https://github.com/MetaCubeX/mihomo", label: "about.mihomo" },
  { url: "https://github.com/wailsapp/wails", label: "about.wails" },
];

const checking = ref(false);
const updating = ref(false);
const updateInfo = ref<CoreUpdateInfo | null>(null);

const engineState = computed(() => store.engineStatus?.state ?? "unknown");
const coreUpdating = computed(() => engineState.value === "updating");

async function openURL(url: string): Promise<void> {
  try {
    await Sys.OpenURL(url);
  } catch (e) {
    toast.error(errMsg(e));
  }
}

async function checkUpdates(): Promise<void> {
  if (checking.value) return;
  checking.value = true;
  try {
    updateInfo.value = await Core.CheckUpdate();
    if (updateInfo.value?.hasUpdate) {
      toast.info(t("about.available", { v: updateInfo.value.latest }));
    } else {
      toast.success(t("about.upToDate"));
    }
  } catch (e) {
    toast.error(`${t("about.checkFailed")}: ${errMsg(e)}`);
  } finally {
    checking.value = false;
  }
}

async function updateNow(): Promise<void> {
  if (updating.value || coreUpdating.value) return;
  updating.value = true;
  try {
    await Core.UpdateNow();
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    updating.value = false;
  }
}

onMounted(() => {
  if (!store.appInfo) void refreshAppInfo();
});
</script>

<template>
  <div class="view">
    <!-- app identity -->
    <section class="panel">
      <div class="row" style="gap: 14px">
        <IconBolt :size="34" />
        <div>
          <h2 style="font-size: 17px">NeuroRift</h2>
          <span class="dim small">A fast, modern proxy cockpit powered by mihomo</span>
        </div>
      </div>
      <div class="kv" style="margin-top: 14px">
        <span class="k">{{ t("about.version") }}</span>
        <span class="v mono">{{ store.appInfo?.version ?? "—" }}</span>
        <span class="k">{{ t("about.goVersion") }}</span>
        <span class="v mono">{{ store.appInfo?.goVersion ?? "—" }}</span>
        <span class="k">{{ t("about.platform") }}</span>
        <span class="v mono">{{ store.appInfo?.platform ?? "—" }}</span>
        <span class="k">{{ t("about.configDir") }}</span>
        <span class="v mono" :title="store.appInfo?.configDir">{{ store.appInfo?.configDir ?? "—" }}</span>
      </div>
    </section>

    <!-- engine -->
    <section class="panel">
      <header class="panel-head">
        <h2>{{ t("about.engine") }}</h2>
        <StatusPill :state="engineState" />
      </header>
      <div class="kv">
        <span class="k">{{ t("about.core") }}</span>
        <span class="v mono">{{ store.engineStatus?.coreVersion || "—" }}</span>
        <span class="k">{{ t("about.channel") }}</span>
        <span class="v mono">{{ store.appInfo?.coreMeta?.channel || "—" }}</span>
        <span class="k">{{ t("about.activeProfile") }}</span>
        <span class="v">{{ store.engineStatus?.activeName || "—" }}</span>
        <span class="k">{{ t("about.uptime") }}</span>
        <span class="v">{{ store.engineStatus?.uptimeSec ? fmtDuration(store.engineStatus.uptimeSec) : "—" }}</span>
        <span class="k">RAM</span>
        <span class="v">{{ store.curMem ? fmtBytes(store.curMem) : "—" }}</span>
      </div>
      <div v-if="store.engineStatus?.state === 'failed' && store.engineStatus.detail" class="error-box" style="margin-top: 12px">
        <IconWarn :size="15" />
        <span>{{ store.engineStatus.detail }}</span>
      </div>
    </section>

    <!-- core updates -->
    <section class="panel">
      <header class="panel-head">
        <h2>{{ t("about.core") }}</h2>
        <div class="panel-head-actions">
          <button class="btn sm" :disabled="checking" @click="checkUpdates()">
            <span v-if="checking" class="spinner" style="width: 11px; height: 11px" />
            <IconRefresh v-else :size="12" />
            {{ t("about.checkUpdates") }}
          </button>
          <button
            v-if="updateInfo?.hasUpdate"
            class="btn sm primary"
            :disabled="coreUpdating || updating"
            @click="updateNow()"
          >
            <span v-if="coreUpdating || updating" class="spinner" style="width: 11px; height: 11px; border-top-color: var(--accent)" />
            <IconDownload v-else :size="12" />
            {{ t("about.updateNow") }}
          </button>
        </div>
      </header>

      <div v-if="coreUpdating && store.coreDownload" style="margin-bottom: 10px">
        <div class="row" style="justify-content: space-between; margin-bottom: 5px">
          <span class="dim small">{{ t("about.updatingHint") }}</span>
          <span class="dim small mono">{{ Math.round(store.coreDownload.percent) }}%</span>
        </div>
        <div class="progress">
          <div class="bar" :style="{ width: `${Math.min(100, Math.max(2, store.coreDownload.percent))}%` }" />
        </div>
      </div>

      <div v-if="updateInfo" class="kv">
        <span class="k">{{ t("about.current") }}</span>
        <span class="v mono">{{ updateInfo.current || "—" }}</span>
        <span class="k">{{ t("about.latest") }}</span>
        <span class="v mono">{{ updateInfo.latest || "—" }}</span>
        <span class="k">{{ t("about.channel") }}</span>
        <span class="v mono">{{ updateInfo.channel || "—" }}</span>
        <span class="k">&nbsp;</span>
        <span class="v">
          <span v-if="updateInfo.hasUpdate" class="badge warn">{{ t("about.available", { v: updateInfo.latest }) }}</span>
          <span v-else class="badge success"><IconCheck :size="11" /> {{ t("about.upToDate") }}</span>
        </span>
      </div>
      <div v-else class="dim small">{{ t("about.checkUpdates") }}</div>
    </section>

    <!-- links -->
    <section class="panel">
      <header class="panel-head">
        <h2>{{ t("about.links") }}</h2>
      </header>
      <div class="row">
        <button v-for="l in LINKS" :key="l.url" class="btn" @click="openURL(l.url)">
          <IconExternal :size="13" />
          {{ t(l.label) }}
        </button>
      </div>
    </section>
  </div>
</template>
