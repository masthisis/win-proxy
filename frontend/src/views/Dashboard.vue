<script setup lang="ts">
/** Dashboard: mode switcher, group panels with node chips, engine empty state. */
import { computed, ref } from "vue";
import * as Proxy from "../../bindings/github.com/neurorift/neurorift/proxyservice.js";
import * as Profile from "../../bindings/github.com/neurorift/neurorift/profileservice.js";
import type { GroupUI, NodeUI } from "../../bindings/github.com/neurorift/neurorift/models.js";
import type { ShareInfo } from "../../bindings/github.com/neurorift/neurorift/internal/profile/models.js";
import { store, refreshGroups, startEngine, setView, engineRunning } from "../store";
import { t } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import NodeChip from "../components/NodeChip.vue";
import ShareModal from "../components/ShareModal.vue";
import StatusPill from "../components/StatusPill.vue";
import { IconRefresh, IconBolt, IconGauge, IconProfiles, IconWarn, IconSort } from "../components/Icons";

const MODES = ["rule", "global", "direct"] as const;

const modeBusy = ref(false);
const testAllBusy = ref(false);
const groupTestBusy = ref<string | null>(null);
const selecting = ref<string | null>(null);

/** Share modal state (#102): right-click a node chip to share it. */
const shareInfo = ref<ShareInfo | null>(null);
const shareOpen = ref(false);
const shareBusy = ref(false);

async function shareNode(n: { name: string }): Promise<void> {
  if (shareBusy.value) return;
  shareBusy.value = true;
  try {
    shareInfo.value = await Profile.ShareNode(n.name);
    shareOpen.value = true;
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    shareBusy.value = false;
  }
}

function closeShare(): void {
  shareOpen.value = false;
  shareInfo.value = null;
}

/** Issue #43: node search + sort-by-delay toggle (persisted per session). */
const nodeSearch = ref("");
const sortByDelay = ref((localStorage.getItem("nr.dash.sortByDelay") ?? "0") === "1");

function setSortByDelay(v: boolean): void {
  sortByDelay.value = v;
  localStorage.setItem("nr.dash.sortByDelay", v ? "1" : "0");
}

function groupNodes(g: GroupUI): NodeUI[] {
  const q = nodeSearch.value.trim().toLowerCase();
  let list = g.all ?? [];
  if (q) list = list.filter((n) => n.name.toLowerCase().includes(q));
  if (sortByDelay.value) {
    list = [...list].sort((a, b) => {
      const fa = a.delay > 0 ? 0 : 1;
      const fb = b.delay > 0 ? 0 : 1;
      if (fa !== fb) return fa - fb;
      if (a.delay !== b.delay) return a.delay - b.delay;
      return a.name.localeCompare(b.name);
    });
  }
  return list;
}

const engineState = computed(() => store.engineStatus?.state ?? "unknown");
// "unknown" (status not yet fetched / call failed) is treated as off.
const engineOff = computed(
  () => engineState.value === "stopped" || engineState.value === "failed" || engineState.value === "unknown",
);
const engineBusyState = computed(() => engineState.value === "starting" || engineState.value === "updating");
const visibleGroups = computed(() => store.groups.filter((g) => !g.hidden));
const downloading = computed(() => store.coreDownload !== null || engineState.value === "updating");
const showEmptyEngine = computed(
  () => engineOff.value && !store.groups.length && !store.groupsBusy && !store.groupsError,
);

async function setMode(m: string): Promise<void> {
  if (store.mode === m || modeBusy.value) return;
  modeBusy.value = true;
  try {
    await Proxy.SetMode(m);
    store.mode = m;
    toast.success(t("toast.modeSet", { mode: t(`mode.${m}`) }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    modeBusy.value = false;
  }
}

async function testAll(): Promise<void> {
  if (testAllBusy.value) return;
  testAllBusy.value = true;
  toast.info(t("toast.testingAll"));
  try {
    const batch = await Proxy.TestAllNodes();
    toast.success(t("toast.tested", { n: batch?.results?.length ?? 0 }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    testAllBusy.value = false;
  }
}

async function testGroup(g: GroupUI): Promise<void> {
  if (groupTestBusy.value) return;
  groupTestBusy.value = g.name;
  try {
    const batch = await Proxy.TestGroup(g.name);
    toast.success(t("toast.tested", { n: batch?.results?.length ?? 0 }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    groupTestBusy.value = null;
  }
}

async function selectNode(g: GroupUI, node: NodeUI): Promise<void> {
  if (g.type !== "Selector" || selecting.value) return;
  selecting.value = node.name;
  try {
    await Proxy.SelectProxy(g.name, node.name);
    g.now = node.name;
    toast.success(t("toast.nodeSelected", { node: node.name }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    selecting.value = null;
  }
}
</script>

<template>
  <div class="view">
    <!-- engine off: centered start card -->
    <div v-if="showEmptyEngine" class="panel">
      <div class="empty" style="padding: 34px 20px">
        <IconBolt :size="40" />
        <div class="e-title">{{ t("dash.engineOffTitle") }}</div>
        <div class="e-body">{{ t("dash.engineOffBody") }}</div>
        <div v-if="store.engineStatus?.state === 'failed' && store.engineStatus.detail" class="error-box" style="max-width: 480px">
          <IconWarn :size="15" />
          <span>{{ t("dash.failureHint") }}: {{ store.engineStatus.detail }}</span>
        </div>
        <div v-if="downloading && store.coreDownload" style="width: min(420px, 100%)">
          <div class="row" style="justify-content: space-between; margin-bottom: 6px">
            <span class="dim small">{{ t("dash.downloadHint") }}</span>
            <span class="dim small mono">{{ Math.round(store.coreDownload.percent) }}%</span>
          </div>
          <div class="progress">
            <div class="bar" :style="{ width: `${Math.min(100, Math.max(2, store.coreDownload.percent))}%` }" />
          </div>
          <div class="dim small" style="margin-top: 5px">{{ store.coreDownload.detail }}</div>
        </div>
        <button class="btn primary lg" :disabled="store.engineBusy || engineBusyState" @click="startEngine()">
          <span v-if="store.engineBusy || engineBusyState" class="spinner" style="border-top-color: var(--accent)" />
          <IconBolt v-else :size="17" />
          {{ t("dash.startEngine") }}
        </button>
        <div class="dim small" style="max-width: 420px">{{ t("dash.firstRunHint") }}</div>
      </div>
    </div>

    <template v-else>
      <!-- header controls -->
      <div class="panel">
        <div class="row" style="justify-content: space-between">
          <div class="row">
            <div class="seg" role="group" :aria-label="t('settings.mode')">
              <button
                v-for="m in MODES"
                :key="m"
                :class="{ active: store.mode === m }"
                :disabled="modeBusy || !engineRunning()"
                @click="setMode(m)"
              >
                {{ t(`mode.${m}`) }}
              </button>
            </div>
            <StatusPill :state="engineState" />
          </div>
          <div class="row">
            <input
              v-model="nodeSearch"
              type="text"
              class="node-search"
              :placeholder="t('test.searchNodes')"
              style="max-width: 180px"
            />
            <button
              class="btn-icon"
              :class="{ on: sortByDelay }"
              :aria-pressed="sortByDelay"
              :aria-label="t('dash.sortByDelay')"
              :title="t('dash.sortByDelay')"
              @click="setSortByDelay(!sortByDelay)"
            >
              <IconSort :size="15" />
            </button>
            <button class="btn" :disabled="testAllBusy || !engineRunning() || !visibleGroups.length" @click="testAll()">
              <span v-if="testAllBusy" class="spinner" />
              <IconGauge v-else :size="14" />
              {{ t("dash.testAll") }}
            </button>
            <button class="btn-icon" :aria-label="t('common.refresh')" :title="t('common.refresh')" :disabled="store.groupsBusy" @click="refreshGroups()">
              <IconRefresh :size="15" :class="{ spin: store.groupsBusy }" />
            </button>
          </div>
        </div>
      </div>

      <!-- starting / downloading state -->
      <div v-if="engineBusyState && !visibleGroups.length" class="panel">
        <div class="empty">
          <span class="spinner lg" />
          <div class="e-title">
            {{ downloading ? t("dash.downloadHint") : t("state.starting") }}
          </div>
          <div v-if="store.coreDownload" style="width: min(420px, 100%)">
            <div class="row" style="justify-content: space-between; margin-bottom: 6px">
              <span class="dim small">{{ store.coreDownload.detail }}</span>
              <span class="dim small mono">{{ Math.round(store.coreDownload.percent) }}%</span>
            </div>
            <div class="progress">
              <div class="bar" :style="{ width: `${Math.min(100, Math.max(2, store.coreDownload.percent))}%` }" />
            </div>
          </div>
        </div>
      </div>

      <!-- loading -->
      <div v-if="store.groupsBusy && !store.groups.length" class="panel">
        <div class="empty">
          <span class="spinner lg" />
          <div class="e-body">{{ t("dash.loadingGroups") }}</div>
        </div>
      </div>

      <!-- error -->
      <div v-else-if="store.groupsError && !store.groups.length" class="panel">
        <div class="empty">
          <IconWarn :size="30" />
          <div class="e-title">{{ t("dash.groupsError") }}</div>
          <div class="e-body">{{ store.groupsError }}</div>
          <button class="btn" @click="refreshGroups()">
            <IconRefresh :size="14" />
            {{ t("common.retry") }}
          </button>
        </div>
      </div>

      <!-- no groups -->
      <div v-else-if="!visibleGroups.length && engineRunning()" class="panel">
        <div class="empty">
          <IconProfiles :size="30" />
          <div class="e-title">{{ t("dash.noGroupsTitle") }}</div>
          <div class="e-body">{{ t("dash.noGroupsBody") }}</div>
          <button class="btn primary" @click="setView('profiles')">
            {{ t("dash.goProfiles") }}
          </button>
        </div>
      </div>

      <!-- groups -->
      <section v-for="g in visibleGroups" :key="g.name" class="panel">
        <header class="panel-head">
          <h2 :title="g.name">{{ g.name }}</h2>
          <span class="badge" :class="g.type === 'Selector' ? 'accent' : 'violet'">{{ g.type }}</span>
          <span class="dim small">{{ g.type === "Selector" ? t("dash.clickHint") : t("dash.autoHint") }}</span>
          <div class="panel-head-actions">
            <button
              class="btn-icon"
              :aria-label="t('dash.testGroup')"
              :title="t('dash.testGroup')"
              :disabled="groupTestBusy !== null || testAllBusy"
              @click="testGroup(g)"
            >
              <span v-if="groupTestBusy === g.name" class="spinner" style="width: 12px; height: 12px" />
              <IconGauge v-else :size="15" />
            </button>
          </div>
        </header>
        <div class="chips">
          <NodeChip
            v-for="n in groupNodes(g)"
            :key="n.name"
            :node="n"
            :selected="n.name === g.now"
            :interactive="g.type === 'Selector'"
            :busy="selecting === n.name"
            shareable
            @select="selectNode(g, n)"
            @share="shareNode(n)"
          />
        </div>
      </section>
    </template>

    <ShareModal :open="shareOpen" :info="shareInfo" @close="closeShare" />
  </div>
</template>
