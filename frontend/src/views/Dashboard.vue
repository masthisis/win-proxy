<script setup lang="ts">
/** Dashboard: mode switcher, group panels with node chips, engine empty state. */
import { computed, onMounted, ref } from "vue";
import * as Proxy from "../../bindings/github.com/neurorift/neurorift/proxyservice.js";
import * as Profile from "../../bindings/github.com/neurorift/neurorift/profileservice.js";
import * as Settings from "../../bindings/github.com/neurorift/neurorift/settingsservice.js";
import type { GroupUI, NodeUI } from "../../bindings/github.com/neurorift/neurorift/models.js";
import type { ShareInfo } from "../../bindings/github.com/neurorift/neurorift/internal/profile/models.js";
import type { ProxyChain } from "../../bindings/github.com/neurorift/neurorift/internal/settings/models.js";
import type { ChipMenu } from "../components/NodeChip.vue";
import RouteViaModal from "../components/RouteViaModal.vue";
import { store, refreshGroups, startEngine, stopEngine, setView, engineRunning } from "../store";
import { t } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import { fmtRate, fmtDuration } from "../lib/format";
import NodeChip from "../components/NodeChip.vue";
import ShareModal from "../components/ShareModal.vue";
import StatusPill from "../components/StatusPill.vue";
import { IconRefresh, IconBolt, IconGauge, IconProfiles, IconWarn, IconSort, IconPower } from "../components/Icons";

const MODES = ["rule", "global", "direct"] as const;

onMounted(() => {
  window.addEventListener("click", closeMenu);
  window.addEventListener("resize", closeMenu);
});

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

/** Context menu state: right-click a chip → Share / Route via… (#107). */
const menu = ref<ChipMenu | null>(null);

function openMenu(ev: ChipMenu): void {
  menu.value = ev;
}

function closeMenu(): void {
  menu.value = null;
}

function menuShare(): void {
  const n = menu.value?.node;
  closeMenu();
  if (n) void shareNode(n);
}

function menuRoute(): void {
  const n = menu.value?.node;
  closeMenu();
  if (n) openRouteVia(n.name);
}

/** Chain state (#107): the app-managed chain list, refreshed on mount and
 * after every add/remove so the modal shows the current pairing. */
const chains = ref<ProxyChain[]>([]);
const routeViaOpen = ref(false);
const routeViaNode = ref("");

async function refreshChains(): Promise<void> {
  try {
    const doc = await Settings.Get();
    chains.value = doc.proxyChains ?? [];
  } catch {
    chains.value = [];
  }
}

onMounted(refreshChains);

function openRouteVia(node: string): void {
  routeViaNode.value = node;
  routeViaOpen.value = true;
}

async function onRouteViaChanged(): Promise<void> {
  await refreshChains();
  await refreshGroups();
}

/** Candidates for a front: every node visible in any group. */
const routeCandidates = computed(() => {
  const names: string[] = [];
  for (const g of store.groups) {
    for (const n of g.all ?? []) names.push(n.name);
  }
  return [...new Set(names)];
});

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

/** Hero card (#123): the node the primary selector currently points at. */
const heroNode = computed(() => {
  for (const g of visibleGroups.value) {
    if (g.type !== "Selector" || !g.now) continue;
    const node = (g.all ?? []).find((n) => n.name === g.now);
    return { name: g.now, delay: node?.delay ?? store.latency[g.now] ?? 0, type: node?.type ?? "" };
  }
  return null;
});

/** Latency chip tier per the design system (success / warning / error). */
const heroTier = computed<"good" | "mid" | "bad" | null>(() => {
  const d = heroNode.value?.delay ?? 0;
  if (d <= 0) return null;
  if (d <= 200) return "good";
  if (d <= 400) return "mid";
  return "bad";
});

const heroProto = computed(() => {
  const t = heroNode.value?.type ?? "";
  return t && t !== "Selector" ? t.toUpperCase() : "";
});
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
      <!-- Hero connect card (#123 Stitch design): power, active node,
           live rates, routing mode, test-all. -->
      <section class="hero-card" aria-label="Connection status">
        <div class="hero-main">
          <div class="row" style="gap: 16px; min-width: 0">
            <button
              class="power-btn"
              :class="{ off: engineOff }"
              :disabled="store.engineBusy || engineBusyState"
              :aria-label="engineOff ? t('dash.powerOn') : t('dash.powerOff')"
              :title="engineOff ? t('dash.powerOn') : t('dash.powerOff')"
              @click="engineOff ? startEngine() : stopEngine()"
            >
              <span v-if="store.engineBusy || engineBusyState" class="spinner" style="border-top-color: currentcolor" />
              <IconPower v-else :size="26" />
            </button>
            <div style="min-width: 0">
              <div class="row" style="gap: 8px">
                <h1 style="overflow: hidden; text-overflow: ellipsis; white-space: nowrap">
                  {{ heroNode?.name ?? store.engineStatus?.activeName ?? t("dash.noActiveNode") }}
                </h1>
                <span v-if="heroTier" class="latency-chip" :class="heroTier">
                  <span
                    class="dot"
                    :style="{ width: '6px', height: '6px', borderRadius: '999px', background: 'currentcolor' }"
                  />
                  {{ heroNode?.delay }} ms
                </span>
              </div>
              <p class="hero-sub" style="margin-top: 2px">
                <template v-if="heroProto">{{ heroProto }} · </template>{{ t("dash.routingVia") }}
                <b style="color: var(--on-surface)">{{ t(`mode.${store.mode}`) }}</b>
                <template v-if="store.engineStatus?.uptimeSec"> · {{ t("dash.uptime") }} {{ fmtDuration(store.engineStatus.uptimeSec) }}</template>
              </p>
            </div>
          </div>
          <div class="row" style="gap: 16px; flex-wrap: wrap">
            <div class="hero-rates" :title="t('header.traffic')">
              <div class="down">↓ {{ fmtRate(store.curDown) }}</div>
              <div class="up">↑ {{ fmtRate(store.curUp) }}</div>
            </div>
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
            <button class="btn" :disabled="testAllBusy || !engineRunning() || !visibleGroups.length" @click="testAll()">
              <span v-if="testAllBusy" class="spinner" />
              <IconGauge v-else :size="16" style="color: var(--accent)" />
              {{ t("dash.testAll") }}
            </button>
          </div>
        </div>
      </section>

      <!-- toolbar: search + sort + refresh -->
      <div class="row" style="justify-content: space-between; gap: 8px; flex-wrap: wrap">
        <div class="row" style="gap: 8px">
          <input
            v-model="nodeSearch"
            type="text"
            class="node-search"
            :placeholder="t('test.searchNodes')"
            style="max-width: 200px"
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
        </div>
        <div class="row" style="gap: 8px">
          <StatusPill :state="engineState" />
          <button class="btn-icon" :aria-label="t('common.refresh')" :title="t('common.refresh')" :disabled="store.groupsBusy" @click="refreshGroups()">
            <IconRefresh :size="15" :class="{ spin: store.groupsBusy }" />
          </button>
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
            routeable
            @select="selectNode(g, n)"
            @share="shareNode(n)"
            @menu="openMenu"
          />
        </div>
      </section>
    </template>

    <ShareModal :open="shareOpen" :info="shareInfo" @close="closeShare" />

    <!-- node context menu (right-click a chip) -->
    <div
      v-if="menu"
      class="chip-menu"
      :style="{ left: `${menu.x}px`, top: `${menu.y}px` }"
      role="menu"
      @click.stop
    >
      <button class="chip-menu-item" role="menuitem" @click="menuShare">
        {{ t("dash.menuShare") }}
      </button>
      <button
        v-if="!menu.node.name.includes(' via ')"
        class="chip-menu-item"
        role="menuitem"
        @click="menuRoute"
      >
        {{ t("dash.menuRouteVia") }}
      </button>
    </div>

    <RouteViaModal
      :open="routeViaOpen"
      :node="routeViaNode"
      :chains="chains"
      :candidates="routeCandidates"
      @close="routeViaOpen = false"
      @changed="onRouteViaChanged"
    />
  </div>
</template>
