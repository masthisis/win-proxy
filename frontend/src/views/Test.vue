<script setup lang="ts">
/** Test Center: node table with latency/speed actions + failover watchdog. */
import { computed, onMounted, ref } from "vue";
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
}

const testAllBusy = ref(false);
const pingBusy = ref<string | null>(null);
const speedOneBusy = ref<string | null>(null);
const fallbackNodes = ref<Row[]>([]);

const primaryGroup = computed(() => store.groups.find((g) => !g.hidden));

const rows = computed<Row[]>(() => {
  if (primaryGroup.value) {
    return (primaryGroup.value.all ?? []).map((n) => ({ name: n.name, type: n.type, delay: n.delay }));
  }
  return fallbackNodes.value;
});

const speedOf = (name: string): SpeedResultPayload | undefined => store.speedResults[name];
const speedDown = (name: string): string => (speedOf(name) ? fmtMbps(speedOf(name)!.downloadMbps) : "—");
const speedUp = (name: string): string => (speedOf(name) ? fmtMbps(speedOf(name)!.uploadMbps) : "—");

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
  const id = store.activeProfileId || store.engineStatus?.activeProfile;
  if (!id) return;
  try {
    const names = await ProfileSvc.GetNodes(id);
    fallbackNodes.value = (names ?? []).map((n) => ({ name: n, type: "", delay: store.latency[n] ?? 0 }));
  } catch {
    /* ignore — engine may be down */
  }
}

onMounted(() => {
  void refreshFailover();
  void refreshSpeedHistory();
  if (!store.groups.length) void loadFallbackNodes();
});

async function testAll(): Promise<void> {
  if (testAllBusy.value) return;
  testAllBusy.value = true;
  toast.info(t("toast.testingAll"));
  try {
    const batch = await Proxy.TestAllNodes();
    toast.success(t("toast.tested", { n: batch?.results?.length ?? 0 }));
    for (const r of batch?.results ?? []) applyLatency(r.node, r.delay);
    if (!primaryGroup.value) void loadFallbackNodes();
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

async function failoverTestNow(): Promise<void> {
  try {
    await Test.FailoverTestNow();
  } catch (e) {
    toast.error(errMsg(e));
  }
}

const evBadge = (type: string): string =>
  type === "switched" ? "danger" : type === "probe-failed" ? "warn" : "success";
</script>

<template>
  <div class="view">
    <!-- toolbar -->
    <div class="panel">
      <div class="row" style="justify-content: space-between">
        <div class="row">
          <button class="btn" :disabled="testAllBusy || !rows.length" @click="testAll()">
            <span v-if="testAllBusy" class="spinner" />
            <IconTest v-else :size="14" />
            {{ t("test.testAllLatency") }}
          </button>
          <button class="btn primary" :disabled="store.speedBusy || !rows.length" @click="speedTop5()">
            <span v-if="store.speedBusy" class="spinner" style="border-top-color: var(--accent)" />
            <IconGauge v-else :size="14" />
            {{ t("test.speedTop5") }}
          </button>
        </div>
        <span v-if="primaryGroup" class="dim small">{{ primaryGroup.name }}</span>
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
            <th>{{ t("test.colName") }}</th>
            <th>{{ t("test.colType") }}</th>
            <th>{{ t("test.colLatency") }}</th>
            <th>{{ t("test.colDown") }}</th>
            <th>{{ t("test.colUp") }}</th>
            <th v-if="engineRunning()" style="width: 1%">{{ t("test.colActions") }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.name">
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
        <div class="e-body">{{ t("test.emptyBody") }}</div>
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
