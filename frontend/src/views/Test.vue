<script setup lang="ts">
/** Test Center: profile selector, node table with latency/speed actions + failover watchdog. */
import { computed, onMounted, ref, watch } from "vue";
import * as Proxy from "../../bindings/github.com/neurorift/neurorift/proxyservice.js";
import * as Test from "../../bindings/github.com/neurorift/neurorift/testservice.js";
import * as ProfileSvc from "../../bindings/github.com/neurorift/neurorift/profileservice.js";
import { store, refreshFailover, refreshSpeedHistory, applyLatency, engineRunning } from "../store";
import { t } from "../lib/i18n";
import type { SpeedResultPayload } from "../lib/events";
import { toast, errMsg } from "../lib/toast";
import { fmtLatency, fmtMbps, fmtMsDuration, fmtDateTime, relTimeKey } from "../lib/format";
import { IconGauge, IconTest, IconWarn, IconCheck, IconClose, IconRefresh } from "../components/Icons";

interface Row {
  name: string;
  type: string;
  delay: number;
  profileName?: string;
}

const testAllBusy = ref(false);
const pingBusy = ref<string | null>(null);
const speedOneBusy = ref<string | null>(null);
const fallbackNodes = ref<Row[]>([]);

/** Issue #43: sorting, search and filters for the node table. */
type SortKey = "default" | "name" | "delay" | "down" | "up";
const sortBy = ref<SortKey>("default");
const sortDir = ref<1 | -1>(1);
const search = ref("");
const latFilter = ref<"all" | "alive" | "failed">("all");
const autoSort = ref((localStorage.getItem("nr.test.autoSort") ?? "1") === "1");

function setAutoSort(v: boolean): void {
  autoSort.value = v;
  localStorage.setItem("nr.test.autoSort", v ? "1" : "0");
}

function toggleSort(key: Exclude<SortKey, "default">): void {
  if (sortBy.value === key) {
    sortDir.value = sortDir.value === 1 ? -1 : 1;
  } else {
    sortBy.value = key;
    sortDir.value = 1;
  }
}

function sortIndicator(key: Exclude<SortKey, "default">): string {
  if (sortBy.value !== key) return "";
  return sortDir.value === 1 ? "▲" : "▼";
}

/** Issue #42: which subscription the table shows. "" = active profile. */
const selectedProfileId = ref("");
const profileNodesBusy = ref(false);

const primaryGroup = computed(() => store.groups.find((g) => !g.hidden));

const selectedProfile = computed(() => store.profiles.find((p) => p.id === selectedProfileId.value));
const showingActive = computed(() => selectedProfileId.value === "");
const showingAll = computed(() => selectedProfileId.value === "__all__");

const activeProfileName = computed(
  () => store.profiles.find((p) => p.id === (store.activeProfileId || store.engineStatus?.activeProfile))?.name,
);

const rows = computed<Row[]>(() => {
  if (showingAll.value) {
    return fallbackNodes.value;
  }
  if (showingActive.value) {
    if (primaryGroup.value) {
      return (primaryGroup.value.all ?? []).map((n) => ({ name: n.name, type: n.type, delay: n.delay }));
    }
    return fallbackNodes.value.filter((r) => r.profileName === activeProfileName.value);
  }
  return fallbackNodes.value.filter((r) => r.profileName === selectedProfile.value?.name);
});

const speedOf = (name: string): SpeedResultPayload | undefined => store.speedResults[name];
const speedDown = (name: string): string => (speedOf(name) ? fmtMbps(speedOf(name)!.downloadMbps) : "—");
const speedUp = (name: string): string => (speedOf(name) ? fmtMbps(speedOf(name)!.uploadMbps) : "—");

/** Issue #43: search + alive/failed filter + sortable columns. */
const filteredRows = computed<Row[]>(() => {
  const q = search.value.trim().toLowerCase();
  return rows.value.filter((r) => {
    if (q && !r.name.toLowerCase().includes(q)) return false;
    if (latFilter.value === "alive" && !(r.delay > 0)) return false;
    if (latFilter.value === "failed" && r.delay > 0) return false;
    return true;
  });
});

const sortedRows = computed<Row[]>(() => {
  const list = [...filteredRows.value];
  const dir = sortDir.value;
  switch (sortBy.value) {
    case "name":
      list.sort((a, b) => dir * a.name.localeCompare(b.name));
      break;
    case "delay":
      // Alive nodes first (ascending), failures at the end.
      list.sort((a, b) => {
        const fa = a.delay > 0 ? 0 : 1;
        const fb = b.delay > 0 ? 0 : 1;
        if (fa !== fb) return fa - fb;
        if (a.delay !== b.delay) return dir * (a.delay - b.delay);
        return a.name.localeCompare(b.name);
      });
      break;
    case "down": {
      const d = (r: Row) => speedOf(r.name)?.downloadMbps ?? -1;
      list.sort((a, b) => dir * (d(a) - d(b)) || a.name.localeCompare(b.name));
      break;
    }
    case "up": {
      const u = (r: Row) => speedOf(r.name)?.uploadMbps ?? -1;
      list.sort((a, b) => dir * (u(a) - u(b)) || a.name.localeCompare(b.name));
      break;
    }
    default:
      break; // provider order
  }
  return list;
});

function latCls(d: number): string {
  if (d > 0 && d < 200) return "good";
  if (d >= 200 && d < 500) return "mid";
  if (d >= 500) return "bad";
  return "none";
}

function rel(iso: string | undefined): string {
  const r = relTimeKey(iso, store.now);
  return r ? t(r.key, r.params) : "—";
}

/** Elapsed progress % of the running speed test vs configured caps. */
const speedPercent = computed(() => {
  const p = store.speedProgress;
  if (!p) return 0;
  const capMs = Math.max(1, (store.settings?.test?.speedMaxSeconds ?? 10)) * 1000;
  const phases = store.settings?.test?.uploadTest ? 2 : 1;
  const pct = (p.elapsedMs / (capMs * phases)) * 100;
  return Math.min(100, Math.max(2, pct));
});

async function loadFallbackNodes(): Promise<void> {
  // Issue #42: load the node list of EVERY profile (merged view) so the
  // table works for non-active subscriptions and the "all subscriptions"
  // selection even when the engine is down.
  profileNodesBusy.value = true;
  const merged: Row[] = [];
  try {
    for (const p of store.profiles) {
      try {
        const names = await ProfileSvc.GetNodes(p.id);
        for (const n of names ?? []) {
          merged.push({ name: n, type: "", delay: store.latency[n] ?? 0, profileName: p.name });
        }
      } catch {
        /* skip unreadable profiles */
      }
    }
    fallbackNodes.value = merged;
  } finally {
    profileNodesBusy.value = false;
  }
}

const profileIdForTesting = computed<string>(() => {
  if (showingAll.value) return "__all__";
  if (showingActive.value) return store.activeProfileId || store.engineStatus?.activeProfile || "";
  return selectedProfileId.value;
});

onMounted(() => {
  void refreshFailover();
  void refreshSpeedHistory();
  if (!store.groups.length || store.profiles.length > 1) void loadFallbackNodes();
});

// Reload per-profile node lists when the profile set changes.
watch(
  () => store.profiles.map((p) => p.id + ":" + p.nodeCount).join("|"),
  () => void loadFallbackNodes(),
);

async function testAll(): Promise<void> {
  if (testAllBusy.value) return;
  testAllBusy.value = true;
  const target = profileIdForTesting.value;
  try {
    if (target === "__all__") {
      const batches = await Test.TestAllProfilesLatency();
      let n = 0;
      for (const b of batches ?? []) {
        for (const r of b?.results ?? []) {
          applyLatency(r.node, r.delay);
          n++;
        }
      }
      toast.success(t("toast.tested", { n }));
    } else if (target) {
      const batch = await Test.TestProfileLatency(target);
      for (const r of batch?.results ?? []) applyLatency(r.node, r.delay);
      toast.success(t("toast.tested", { n: batch?.results?.length ?? 0 }));
    } else {
      const batch = await Proxy.TestAllNodes();
      toast.success(t("toast.tested", { n: batch?.results?.length ?? 0 }));
      for (const r of batch?.results ?? []) applyLatency(r.node, r.delay);
    }
    // Issue #43: v2rayN-style auto-sort by latency after a batch test.
    if (autoSort.value) {
      sortBy.value = "delay";
      sortDir.value = 1;
    }
    if (!primaryGroup.value || !showingActive.value) void loadFallbackNodes();
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    testAllBusy.value = false;
  }
}

async function ping(row: Row): Promise<void> {
  if (pingBusy.value) return;
  pingBusy.value = row.name;
  try {
    const sample = await Proxy.TestNode(row.name);
    if (sample) {
      applyLatency(sample.node, sample.delay);
      row.delay = sample.delay;
    }
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    pingBusy.value = null;
  }
}

async function speedOne(row: Row): Promise<void> {
  if (store.speedBusy) return;
  store.speedBusy = true;
  speedOneBusy.value = row.name;
  try {
    await Test.SpeedTest(row.name);
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    store.speedBusy = false;
    speedOneBusy.value = null;
  }
}

async function speedTop5(): Promise<void> {
  if (store.speedBusy || !rows.value.length) return;
  const withDelay = rows.value.filter((r) => r.delay > 0).sort((a, b) => a.delay - b.delay);
  const picked = (withDelay.length ? withDelay : rows.value).slice(0, 5);
  store.speedBusy = true;
  try {
    await Test.SpeedTestQueue(picked.map((r) => r.name));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    store.speedBusy = false;
    store.speedProgress = null;
  }
}

async function speedProfile(): Promise<void> {
  const target = profileIdForTesting.value;
  if (store.speedBusy || !target || target === "__all__") return;
  store.speedBusy = true;
  try {
    await Test.SpeedTestProfile(target);
  } catch (e) {
    toast.error(errMsg(e));
    store.speedBusy = false;
  }
}

async function failoverTestNow(): Promise<void> {
  try {
    await Test.FailoverTestNow();
  } catch (e) {
    toast.error(errMsg(e));
  }
}

const evBadge = (type: string): string =>
  type === "switched" ? "danger" : type === "probe-failed" ? "warn" : "success";

const rowKey = (row: Row): string => `${row.profileName ?? ""}::${row.name}`;

const tableCaption = computed(() => {
  if (showingAll.value) return t("test.captionAll", { n: store.profiles.length });
  if (selectedProfile.value) return `${selectedProfile.value.name} · ${selectedProfile.value.nodeCount ?? ""}`;
  if (primaryGroup.value) return primaryGroup.value.name;
  return activeProfileName.value ?? "";
});
</script>

<template>
  <div class="view">
    <!-- toolbar -->
    <div class="panel">
      <div class="row" style="justify-content: space-between">
        <div class="row" style="flex-wrap: wrap">
          <!-- Issue #42: test ANY imported subscription, not just the active one -->
          <select
            v-model="selectedProfileId"
            class="profile-select"
            :aria-label="t('test.profile')"
            :title="t('test.profile')"
          >
            <option value="">{{ t("test.profileActive") }}</option>
            <option v-for="p in store.profiles" :key="p.id" :value="p.id">{{ p.name }}</option>
            <option v-if="store.profiles.length > 1" value="__all__">{{ t("test.profileAll") }}</option>
          </select>
          <button class="btn" :disabled="testAllBusy || !rows.length || !engineRunning()" @click="testAll()">
            <span v-if="testAllBusy" class="spinner" />
            <IconTest v-else :size="14" />
            {{ showingAll ? t("test.testAllSubsLatency") : t("test.testAllLatency") }}
          </button>
          <button class="btn primary" :disabled="store.speedBusy || !rows.length || !engineRunning()" @click="speedTop5()">
            <span v-if="store.speedBusy" class="spinner" style="border-top-color: var(--accent)" />
            <IconGauge v-else :size="14" />
            {{ t("test.speedTop5") }}
          </button>
          <button
            v-if="!showingAll && selectedProfileId"
            class="btn"
            :disabled="store.speedBusy || !engineRunning()"
            @click="speedProfile()"
          >
            <IconGauge :size="14" />
            {{ t("test.speedProfile") }}
          </button>
        </div>
        <span class="dim small">{{ tableCaption }}</span>
      </div>

      <!-- Issue #43: search, filters, auto-sort -->
      <div class="row" style="margin-top: 10px; flex-wrap: wrap">
        <input
          v-model="search"
          type="text"
          class="node-search"
          :placeholder="t('test.searchNodes')"
          style="max-width: 220px"
        />
        <div class="seg" role="group" :aria-label="t('test.filter')">
          <button :class="{ active: latFilter === 'all' }" @click="latFilter = 'all'">{{ t("test.filterAll") }}</button>
          <button :class="{ active: latFilter === 'alive' }" @click="latFilter = 'alive'">{{ t("test.filterAlive") }}</button>
          <button :class="{ active: latFilter === 'failed' }" @click="latFilter = 'failed'">{{ t("test.filterFailed") }}</button>
        </div>
        <label class="checkbox" style="gap: 6px">
          <input :checked="autoSort" type="checkbox" @change="setAutoSort(($event.target as HTMLInputElement).checked)" />
          <span class="dim small">{{ t("test.autoSort") }}</span>
        </label>
        <span class="dim small" style="margin-inline-start: auto">
          {{ t("test.showing", { n: sortedRows.length, m: rows.length }) }}
        </span>
      </div>

      <!-- live speed progress -->
      <div v-if="store.speedBusy" style="margin-top: 10px">
        <div class="row" style="justify-content: space-between; margin-bottom: 5px">
          <span class="row" style="gap: 8px">
            <span class="spinner" style="border-top-color: var(--accent)" />
            <b>{{ t("test.speedRunning") }}</b>
            <span v-if="store.speedProgress" class="mono">
              {{ store.speedProgress.node }} ·
              {{ store.speedProgress.phase === "upload" ? t("test.phaseUpload") : t("test.phaseDownload") }}
              · {{ fmtMbps(store.speedProgress.mbps) }} {{ t("test.mbps") }}
              · {{ fmtMsDuration(store.speedProgress.elapsedMs) }}
            </span>
          </span>
          <span class="dim small mono">{{ Math.round(speedPercent) }}%</span>
        </div>
        <div class="progress indeterminate" aria-hidden="true">
          <div class="bar" :style="{ width: `${speedPercent}%` }" />
        </div>
      </div>
    </div>

    <!-- node table -->
    <div v-if="rows.length" class="table-wrap">
      <table class="tbl">
        <thead>
          <tr>
            <th v-if="showingAll">{{ t("test.colProfile") }}</th>
            <th class="sortable" @click="toggleSort('name')">{{ t("test.colName") }} {{ sortIndicator("name") }}</th>
            <th>{{ t("test.colType") }}</th>
            <th class="sortable" @click="toggleSort('delay')">{{ t("test.colLatency") }} {{ sortIndicator("delay") }}</th>
            <th class="sortable" @click="toggleSort('down')">{{ t("test.colDown") }} {{ sortIndicator("down") }}</th>
            <th class="sortable" @click="toggleSort('up')">{{ t("test.colUp") }} {{ sortIndicator("up") }}</th>
            <th v-if="engineRunning()" style="width: 1%">{{ t("test.colActions") }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in sortedRows" :key="rowKey(row)">
            <td v-if="showingAll" class="dim" style="max-width: 140px; overflow: hidden; text-overflow: ellipsis" :title="row.profileName">
              {{ row.profileName }}
            </td>
            <td class="mono" style="max-width: 260px; overflow: hidden; text-overflow: ellipsis" :title="row.name">
              {{ row.name }}
            </td>
            <td class="dim">{{ row.type || "—" }}</td>
            <td>
              <span class="latency" :class="latCls(row.delay)">{{ fmtLatency(row.delay) }}</span>
            </td>
            <td class="num" :title="speedOf(row.name) ? fmtDateTime(speedOf(row.name)!.at) : ''">
              <span v-if="speedOf(row.name)?.error" class="dim" :title="speedOf(row.name)?.error">✕</span>
              <template v-else>{{ speedDown(row.name) }}</template>
            </td>
            <td class="num">{{ speedUp(row.name) }}</td>
            <td v-if="engineRunning()">
              <div class="row" style="gap: 6px; flex-wrap: nowrap">
                <button class="btn sm" :disabled="pingBusy !== null || store.speedBusy" @click="ping(row)">
                  <span v-if="pingBusy === row.name" class="spinner" style="width: 10px; height: 10px" />
                  {{ t("test.ping") }}
                </button>
                <button
                  class="btn sm"
                  :disabled="store.speedBusy || speedOneBusy !== null"
                  @click="speedOne(row)"
                >
                  <span v-if="speedOneBusy === row.name" class="spinner" style="width: 10px; height: 10px" />
                  {{ t("test.speed") }}
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- empty state -->
    <div v-else class="panel">
      <div class="empty">
        <IconTest :size="30" />
        <div class="e-title">{{ t("test.emptyTitle") }}</div>
        <div class="e-body">{{ rows.length ? t("test.noMatches") : t("test.emptyBody") }}</div>
      </div>
    </div>

    <!-- failover watchdog -->
    <section class="panel">
      <header class="panel-head">
        <h2>{{ t("test.failoverTitle") }}</h2>
        <span class="badge" :class="store.failover?.enabled ? 'success' : ''">
          {{ store.failover?.enabled ? t("test.failoverEnabled") : t("test.failoverDisabled") }}
        </span>
        <span v-if="store.failover?.enabled" class="badge" :class="store.failover?.running ? 'accent' : ''">
          {{ store.failover?.running ? t("test.failoverRunning") : t("test.failoverIdle") }}
        </span>
        <div class="panel-head-actions">
          <button class="btn sm" :disabled="!store.failover?.enabled" @click="failoverTestNow()">
            <IconRefresh :size="12" />
            {{ t("test.failoverTestNow") }}
          </button>
        </div>
      </header>

      <div v-if="store.failover" class="kv">
        <span class="k">{{ t("test.failoverActiveNode") }}</span>
        <span class="v mono">{{ store.failover.activeNode || "—" }}</span>
        <span class="k">{{ t("test.failoverGroup") }}</span>
        <span class="v mono">{{ store.failover.group || "—" }}</span>
        <span class="k">{{ t("test.failoverStrikes") }}</span>
        <span class="v">{{ store.failover.strikes }}</span>
        <span class="k">{{ t("test.failoverLastCheck") }}</span>
        <span class="v" :title="fmtDateTime(store.failover.lastCheck)">{{ rel(store.failover.lastCheck) }}</span>
        <span class="k">{{ t("test.failoverLastSwitch") }}</span>
        <span class="v" :title="fmtDateTime(store.failover.lastSwitch)">{{ rel(store.failover.lastSwitch) }}</span>
      </div>

      <h2 style="margin: 14px 0 8px">{{ t("test.failoverHistory") }}</h2>
      <div v-if="!store.failoverEvents.length" class="dim small">{{ t("test.failoverHistoryEmpty") }}</div>
      <div v-else class="table-wrap">
        <table class="tbl">
          <tbody>
            <tr v-for="(e, i) in store.failoverEvents" :key="i">
              <td style="width: 1%">
                <span class="badge" :class="evBadge(e.type)">
                  <IconCheck v-if="e.type === 'probe-ok'" :size="10" />
                  <IconWarn v-else-if="e.type === 'probe-failed'" :size="10" />
                  <IconClose v-else :size="10" />
                  {{ t(`test.ev.${e.type}`) }}
                </span>
              </td>
              <td class="mono">
                <template v-if="e.type === 'switched'">
                  {{ e.from || "?" }} → {{ e.to || "?" }}
                </template>
                <template v-else>{{ e.from || "—" }}</template>
              </td>
              <td class="dim">{{ e.reason || (e.strikes ? `×${e.strikes}` : "") }}</td>
              <td class="dim num" :title="fmtDateTime(e.at)">{{ rel(e.at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
