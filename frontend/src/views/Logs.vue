<script setup lang="ts">
/** Logs: Core (live stream from core:logs) and App (App.LogTail) tabs. */
import { computed, nextTick, onMounted, ref, watch } from "vue";
import * as AppSvc from "../../bindings/github.com/neurorift/neurorift/appservice.js";
import { store } from "../store";
import { t } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import { IconLogs, IconRefresh } from "../components/Icons";

const TABS = ["core", "app"] as const;
type Tab = (typeof TABS)[number];

const tab = ref<Tab>("core");
const filter = ref("");
const autoScroll = ref(true);
const appLogs = ref<string[]>([]);
const appLoading = ref(false);
const coreBox = ref<HTMLElement | null>(null);

const TS_RE = /^(\[?\d{4}-\d{2}-\d{2}[T ][\d:.]+(?:\s*(?:UTC|Z|[+-][\d:]+))?\]?)(.*)$/;

interface LogLine {
  ts: string;
  rest: string;
}

function parseLine(line: string): LogLine {
  const m = line.match(TS_RE);
  if (m) return { ts: m[1], rest: m[2] };
  return { ts: "", rest: line };
}

const q = computed(() => filter.value.trim().toLowerCase());

const coreLines = computed<LogLine[]>(() => {
  const src = store.coreLogs;
  if (!q.value) return src.map(parseLine);
  return src.filter((l) => l.toLowerCase().includes(q.value)).map(parseLine);
});

const appLines = computed<LogLine[]>(() => {
  const src = appLogs.value;
  if (!q.value) return src.map(parseLine);
  return src.filter((l) => l.toLowerCase().includes(q.value)).map(parseLine);
});

const lineCls = (l: LogLine): string => {
  const s = l.rest.toLowerCase();
  if (s.includes(" error ") || s.includes("error]") || s.includes("level=error")) return "error";
  if (s.includes(" warn") || s.includes("warning")) return "warn";
  return "";
};

async function scrollBottom(): Promise<void> {
  await nextTick();
  const el = coreBox.value;
  if (el && autoScroll.value && tab.value === "core") {
    el.scrollTop = el.scrollHeight;
  }
}

watch(() => store.coreLogs.length, scrollBottom);
watch(coreLines, scrollBottom);

async function loadAppLogs(): Promise<void> {
  appLoading.value = true;
  try {
    appLogs.value = (await AppSvc.LogTail(200)) ?? [];
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    appLoading.value = false;
  }
}

onMounted(() => {
  void loadAppLogs();
});

function onCoreScroll(): void {
  const el = coreBox.value;
  if (!el) return;
  autoScroll.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
}
</script>

<template>
  <div class="view">
    <div class="panel">
      <div class="row" style="justify-content: space-between">
        <div class="tabs" role="tablist">
          <button
            v-for="tb in TABS"
            :key="tb"
            class="tab"
            role="tab"
            :class="{ active: tab === tb }"
            :aria-selected="tab === tb"
            @click="tab = tb"
          >
            {{ tb === "core" ? t("logs.tabCore") : t("logs.tabApp") }}
          </button>
        </div>
        <div class="row">
          <label class="checkbox">
            <input v-model="autoScroll" type="checkbox" />
            <span class="box" />
            <span class="dim small">{{ t("logs.autoScroll") }}</span>
          </label>
          <input
            v-model="filter"
            type="text"
            style="width: 180px"
            :placeholder="t('logs.filterPlaceholder')"
            :aria-label="t('logs.filterPlaceholder')"
          />
          <button
            v-if="tab === 'app'"
            class="btn-icon"
            :aria-label="t('logs.refresh')"
            :title="t('logs.refresh')"
            :disabled="appLoading"
            @click="loadAppLogs()"
          >
            <span v-if="appLoading" class="spinner" style="width: 12px; height: 12px" />
            <IconRefresh v-else :size="14" />
          </button>
        </div>
      </div>
    </div>

    <div v-if="tab === 'core'" ref="coreBox" class="log-box" style="min-height: 340px; max-height: calc(100vh - 230px)" @scroll.passive="onCoreScroll">
      <template v-if="coreLines.length">
        <div v-for="(l, i) in coreLines" :key="i" class="log-line" :class="lineCls(l)">
          <span v-if="l.ts" class="ts">{{ l.ts }}</span>{{ l.rest }}
        </div>
      </template>
      <div v-else class="dim">{{ store.engineStatus?.state === "running" ? t("logs.empty") : t("logs.coreHint") }}</div>
    </div>

    <div v-else class="log-box" style="min-height: 340px; max-height: calc(100vh - 230px)">
      <template v-if="appLines.length">
        <div v-for="(l, i) in appLines" :key="i" class="log-line" :class="lineCls(l)">
          <span v-if="l.ts" class="ts">{{ l.ts }}</span>{{ l.rest }}
        </div>
      </template>
      <div v-else class="dim">{{ t("logs.empty") }}</div>
    </div>

    <div class="dim small row">
      <IconLogs :size="13" />
      {{ tab === "core" ? t("logs.coreHint") : t("logs.tabApp") }}
    </div>
  </div>
</template>
