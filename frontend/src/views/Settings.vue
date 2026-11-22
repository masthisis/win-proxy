<script setup lang="ts">
/** Settings: full settings document editor with collapsible sections. */
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { Events } from "@wailsio/runtime";
import { EV } from "../lib/events";
import * as SettingsSvc from "../../bindings/github.com/neurorift/neurorift/settingsservice.js";
import * as Sys from "../../bindings/github.com/neurorift/neurorift/sysservice.js";
import * as ReportSvc from "../../bindings/github.com/neurorift/neurorift/reportservice.js";
import * as AppSvc from "../../bindings/github.com/neurorift/neurorift/appservice.js";
import type { PendingInfo, PortableInfo } from "../../bindings/github.com/neurorift/neurorift/models.js";
import type { Settings as SettingsDoc } from "../../bindings/github.com/neurorift/neurorift/internal/settings/models.js";
import {
  store,
  checkAppUpdate,
  downloadAppUpdate,
  installAppUpdate,
  openUpdateNotes,
  refreshProxyMode,
  savePACScript,
  setProxyMode,
  PROXY_MODES,
  type ProxyMode,
} from "../store";
import { t, setLang, type Lang } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import { IconChevron, IconFolder, IconWarn, IconCheck, IconRefresh, IconDownload, IconExternal, IconTrash, IconPlus } from "../components/Icons";

const form = ref<SettingsDoc | null>(null);
const saving = ref(false);
const autostart = ref(false);

async function loadAutostart(): Promise<void> {
  try {
    autostart.value = await Sys.GetAutostart();
  } catch {
    autostart.value = false;
  }
}

async function onAutostart(ev: Event) {
  const target = ev.target as HTMLInputElement;
  const next = target.checked;
  try {
    await Sys.SetAutostart(next);
    autostart.value = next;
    toast.success(next ? t("settings.autostartOn") : t("settings.autostartOff"));
  } catch (e) {
    target.checked = !next;
    toast.error(errMsg(e));
  }
}
loadAutostart();

// ---- portable mode (#18) ----
const portable = ref<PortableInfo | null>(null);
const importing = ref(false);

async function loadPortable(): Promise<void> {
  try {
    portable.value = await AppSvc.PortableInfo();
  } catch {
    portable.value = null;
  }
}
loadPortable();

async function importLegacy(): Promise<void> {
  if (importing.value) return;
  importing.value = true;
  try {
    portable.value = await AppSvc.PortableImportLegacy();
    toast.success(t("settings.portable.imported"));
  } catch (e) {
    toast.error(`${t("settings.portable.importFailed")}: ${errMsg(e)}`);
  } finally {
    importing.value = false;
  }
}

const open = reactive<Record<string, boolean>>({
  general: true,
  proxy: false,
  ports: false,
  testing: false,
  failover: false,
  core: false,
  dns: false,
  tun: false,
  subscriptions: false,
  routing: false,
  updates: false,
  reporting: false,
});

// ---- TUN service mode (#14) ----
// Service state lives in the store (bootstrap + settings:changed refresh);
// Settings keeps the action buttons and the post-action result polling.
const tunSvc = computed(() => store.tunService);
const serviceRunning = computed(() => !!store.tunService?.running);

// --- Proxy mode (#15) ------------------------------------------------
const currentMode = computed<ProxyMode>(() => {
  const m = store.proxyMode?.mode ?? "";
  return m === "system" || m === "pac" ? m : "off";
});
const pacUrl = computed(() => store.proxyMode?.pacUrl ?? "");
const pacScript = ref("");
const pacScriptDirty = ref(false);

async function loadPacScript(): Promise<void> {
  try {
    pacScript.value = await Sys.GetPACScript();
    pacScriptDirty.value = false;
  } catch {
    /* non-fatal — the card still works with the default script */
  }
}

async function applyMode(m: ProxyMode): Promise<void> {
  await setProxyMode(m);
}

async function saveScript(): Promise<void> {
  await savePACScript(pacScript.value);
  pacScriptDirty.value = false;
}

async function resetScript(): Promise<void> {
  await savePACScript("");
  pacScript.value = await Sys.GetPACScript();
  pacScriptDirty.value = false;
}

function markPacDirty(): void {
  pacScriptDirty.value = true;
}

const tunSvcBusy = ref(false);
let tunSvcPoll: ReturnType<typeof setInterval> | null = null;

async function refreshTunService(): Promise<void> {
  try {
    store.tunService = await Sys.TunServiceStatus();
  } catch {
    store.tunService = null;
  }
}
void refreshTunService();

// Poll for a bounded window after install/uninstall: the action runs in a
// detached elevated child, so the result arrives asynchronously.
function pollTunService(seconds: number): void {
  if (tunSvcPoll) clearInterval(tunSvcPoll);
  let left = seconds;
  tunSvcPoll = setInterval(() => {
    left -= 1;
    void refreshTunService();
    if (left <= 0 && tunSvcPoll) {
      clearInterval(tunSvcPoll);
      tunSvcPoll = null;
    }
  }, 1000);
}

async function installTunService(): Promise<void> {
  if (tunSvcBusy.value) return;
  tunSvcBusy.value = true;
  try {
    await Sys.TunServiceInstall();
    toast.info(t("settings.tunService.installRequested"));
    pollTunService(20);
  } catch (e) {
    toast.error(`${t("settings.tunService.installFailed")}: ${errMsg(e)}`);
  } finally {
    tunSvcBusy.value = false;
    void refreshTunService();
  }
}

async function uninstallTunService(): Promise<void> {
  if (tunSvcBusy.value) return;
  tunSvcBusy.value = true;
  try {
    await Sys.TunServiceUninstall();
    toast.info(t("settings.tunService.uninstallRequested"));
    pollTunService(20);
  } catch (e) {
    toast.error(`${t("settings.tunService.uninstallFailed")}: ${errMsg(e)}`);
  } finally {
    tunSvcBusy.value = false;
    void refreshTunService();
  }
}

const SECTIONS = [
  { id: "general", label: "settings.secGeneral" },
  { id: "ports", label: "settings.secPorts" },
  { id: "testing", label: "settings.secTesting" },
  { id: "failover", label: "settings.secFailover" },
  { id: "core", label: "settings.secCore" },
  { id: "dns", label: "settings.secDns" },
  { id: "tun", label: "settings.secTun" },
  { id: "subscriptions", label: "settings.secSubscriptions" },
  { id: "routing", label: "settings.secRouting" },
  { id: "updates", label: "settings.secUpdates" },
  { id: "reporting", label: "settings.secReporting" },
] as const;

const upd = computed(() => store.appUpdate);
const updBusy = computed(
  () => upd.value?.state === "checking" || upd.value?.state === "downloading" || upd.value?.state === "restarting",
);

// ---- problem reporting (#36) ----
const pending = ref<PendingInfo[]>([]);
const reportBusy = ref(false);

async function loadPending(): Promise<void> {
  try {
    pending.value = (await ReportSvc.Pending()) ?? [];
  } catch {
    pending.value = [];
  }
}

function toggleReporting(openIt: boolean): void {
  open.reporting = openIt;
  if (openIt) loadPending();
}

async function sendTestReport(): Promise<void> {
  if (reportBusy.value) return;
  reportBusy.value = true;
  try {
    await ReportSvc.SendTestReport();
    toast.success(t("reporting.sentToast"));
  } catch (e) {
    toast.error(`${t("reporting.failed")}: ${errMsg(e)}`);
  } finally {
    reportBusy.value = false;
    loadPending();
  }
}

async function sendOne(id: string): Promise<void> {
  if (reportBusy.value) return;
  reportBusy.value = true;
  try {
    await ReportSvc.Send(id);
    toast.success(t("reporting.sentToast"));
  } catch (e) {
    toast.error(`${t("reporting.failed")}: ${errMsg(e)}`);
  } finally {
    reportBusy.value = false;
    loadPending();
  }
}

async function sendAllReports(): Promise<void> {
  if (reportBusy.value) return;
  reportBusy.value = true;
  try {
    const n = await ReportSvc.SendAll();
    toast.success(t("reporting.sentCountToast", { n }));
  } catch (e) {
    toast.error(`${t("reporting.failed")}: ${errMsg(e)}`);
  } finally {
    reportBusy.value = false;
    loadPending();
  }
}

async function deleteOne(id: string): Promise<void> {
  try {
    await ReportSvc.Delete(id);
    toast.info(t("reporting.deleteToast"));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    loadPending();
  }
}

async function openReportsFolder(): Promise<void> {
  try {
    await ReportSvc.OpenFolder();
  } catch (e) {
    toast.error(errMsg(e));
  }
}

const tokenHelpURL = "https://github.com/settings/personal-access-tokens/new";

// Live-refresh the pending list while the settings view is mounted (#36).
let offReports: (() => void) | undefined;
onMounted(() => {
  offReports = Events.On(EV.reportsChanged, () => loadPending()) as unknown as () => void;
});

// PAC mode (#15): load the script editor + the current mode snapshot.
onMounted(() => {
  void loadPacScript();
  void refreshProxyMode();
});
onUnmounted(() => {
  offReports?.();
  if (tunSvcPoll) clearInterval(tunSvcPoll);
});

function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}

watch(
  () => store.settings,
  (s) => {
    if (!s) return;
    if (!form.value || !isDirty()) form.value = clone(s);
  },
  { immediate: true, deep: false },
);

const dirty = computed(() => !!form.value && !!store.settings && JSON.stringify(form.value) !== JSON.stringify(store.settings));

function isDirty(): boolean {
  return !!form.value && !!store.settings && JSON.stringify(form.value) !== JSON.stringify(store.settings);
}

const dnsText = computed<string>({
  get: () => (form.value?.dns?.nameservers ?? []).join("\n"),
  set: (v: string) => {
    if (!form.value) return;
    form.value.dns.nameservers = v
      .split("\n")
      .map((s) => s.trim())
      .filter(Boolean);
  },
});

function onLanguage(e: Event): void {
  const lang = (e.target as HTMLSelectElement).value as Lang;
  if (!form.value) return;
  form.value.language = lang;
  setLang(lang); // instant feedback; persisted with Save
}

async function save(): Promise<void> {
  if (!form.value || saving.value) return;
  saving.value = true;
  try {
    const saved = await SettingsSvc.Save(clone(form.value));
    store.settings = saved;
    toast.success(t("settings.saved"));
  } catch (e) {
    toast.error(`${t("settings.saveFailed")}: ${errMsg(e)}`);
  } finally {
    saving.value = false;
  }
}

function reset(): void {
  if (store.settings) form.value = clone(store.settings);
}

async function openFolder(which: "config" | "core" | "logs"): Promise<void> {
  try {
    await Sys.OpenFolder(which);
  } catch (e) {
    toast.error(errMsg(e));
  }
}

/* ---------------------------------------------------------------- */
/* routing overrides (#50 / #10)                                      */
/* ---------------------------------------------------------------- */

const OV_TYPES = ["domain-suffix", "domain", "domain-keyword", "process-name"] as const;

/** Route targets offered as suggestions: built-ins + groups + their nodes. */
const targetOptions = computed<string[]>(() => {
  const opts = ["DIRECT", "REJECT"];
  for (const g of store.groups) {
    if (!g.hidden) opts.push(g.name);
    for (const n of g.all) opts.push(n.name);
  }
  return [...new Set(opts)];
});

function addOverride(): void {
  if (!form.value) return;
  form.value.routingOverrides = [
    ...(form.value.routingOverrides ?? []),
    { pattern: "", type: "domain-suffix", target: "DIRECT" },
  ];
}

function removeOverride(i: number): void {
  form.value?.routingOverrides?.splice(i, 1);
}

function moveOverride(i: number, dir: -1 | 1): void {
  const arr = form.value?.routingOverrides;
  if (!arr) return;
  const j = i + dir;
  if (j < 0 || j >= arr.length) return;
  [arr[i], arr[j]] = [arr[j], arr[i]];
}

/** Exports the override list as the YAML snippet the importer reads. */
function exportOverrides(): void {
  const list = form.value?.routingOverrides ?? [];
  if (!list.length) return;
  const lines = list.map(
    (o) => `  - pattern: ${JSON.stringify(o.pattern)}\n    type: ${o.type}\n    target: ${JSON.stringify(o.target)}`,
  );
  const text = `routingOverrides:\n${lines.join("\n")}\n`;
  const blob = new Blob([text], { type: "text/yaml" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = "neurorift-overrides.yaml";
  a.click();
  URL.revokeObjectURL(a.href);
}

const ovFile = ref<HTMLInputElement | null>(null);

/** Parses our strict snippet format (see exportOverrides). */
function parseOverridesYAML(text: string): { pattern: string; type: string; target: string }[] {
  const strip = (v: string): string => {
    v = v.trim();
    if (v.length >= 2 && ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'")))) {
      return v.slice(1, -1);
    }
    return v;
  };
  const out: { pattern: string; type: string; target: string }[] = [];
  let cur: { pattern: string; type: string; target: string } | null = null;
  const push = () => {
    if (cur && cur.pattern && cur.target) out.push(cur);
    cur = null;
  };
  for (const line of text.split(/\r?\n/)) {
    let m = line.match(/^\s*-\s*pattern:\s*(.+)$/);
    if (m) {
      push();
      cur = { pattern: strip(m[1]), type: "domain-suffix", target: "" };
      continue;
    }
    m = line.match(/^\s*type:\s*(\S+)\s*$/);
    if (m && cur) {
      cur.type = (OV_TYPES as readonly string[]).includes(m[1]) ? m[1] : "domain-suffix";
      continue;
    }
    m = line.match(/^\s*target:\s*(.+)$/);
    if (m && cur) {
      cur.target = strip(m[1]);
    }
  }
  push();
  return out;
}

async function importOverrides(ev: Event): Promise<void> {
  const input = ev.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = "";
  if (!file || !form.value) return;
  try {
    const text = await file.text();
    const parsed = parseOverridesYAML(text);
    if (!parsed.length) throw new Error("no entries");
    form.value.routingOverrides = [...(form.value.routingOverrides ?? []), ...parsed];
    toast.success(t("settings.routingImported", { n: parsed.length }));
  } catch {
    toast.error(t("settings.routingImportFailed"));
  }
}
</script>

<template>
  <div class="view">
    <div v-if="!form" class="panel">
      <div class="empty">
        <span class="spinner lg" />
        <div class="e-body">{{ t("common.loading") }}</div>
      </div>
    </div>

    <template v-else>
      <div class="panel">
        <div class="row" style="justify-content: space-between">
          <span v-if="dirty" class="badge warn">{{ t("common.save") }}?</span>
          <span v-else class="badge success"><IconCheck :size="11" /> {{ t("settings.saved") }}</span>
          <div class="row">
            <button class="btn" :disabled="!dirty || saving" @click="reset()">{{ t("common.reset") }}</button>
            <button class="btn primary" :disabled="!dirty || saving" @click="save()">
              <span v-if="saving" class="spinner" style="border-top-color: var(--accent)" />
              {{ t("common.save") }}
            </button>
          </div>
        </div>
      </div>

      <!-- General -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.general" @click="open.general = !open.general">
          <IconChevron :size="14" class="chev" :class="{ open: open.general }" />
          {{ t("settings.secGeneral") }}
        </button>
        <div v-if="open.general" class="form-grid" style="margin-top: 12px">
          <div class="field">
            <label for="set-lang">{{ t("settings.language") }}</label>
            <select id="set-lang" :value="form.language" @change="onLanguage">
              <option value="en">{{ t("settings.langEn") }}</option>
              <option value="fa">{{ t("settings.langFa") }}</option>
            </select>
          </div>
          <div class="field">
            <label for="set-mode">{{ t("settings.mode") }}</label>
            <select id="set-mode" v-model="form.mode">
              <option value="rule">{{ t("mode.rule") }}</option>
              <option value="global">{{ t("mode.global") }}</option>
              <option value="direct">{{ t("mode.direct") }}</option>
            </select>
          </div>
          <label class="checkbox">
            <input v-model="form.startMinimized" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.startMinimized") }}</span>
          </label>
          <label class="checkbox">
            <input v-model="form.autoConnect" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.autoConnect") }}</span>
          </label>
          <label class="checkbox">
            <input v-model="form.systemProxyOnStart" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.systemProxyOnStart") }}</span>
          </label>
          <label class="checkbox">
            <input v-model="form.allowLan" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.allowLan") }}</span>
          </label>
          <label class="checkbox">
            <input :checked="autostart" type="checkbox" @change="onAutostart" />
            <span class="box" />
            <span>{{ t("settings.autostart") }}</span>
          </label>
        </div>

        <!-- system proxy guard (#47) -->
        <div style="margin-top: 12px; padding-top: 10px; border-top: 1px solid var(--border)">
          <div class="form-grid">
            <label class="checkbox">
              <input v-model="form.proxyGuard.enable" type="checkbox" />
              <span class="box" />
              <span>{{ t("guard.enable") }}</span>
            </label>
            <label class="checkbox">
              <input v-model="form.proxyGuard.killSwitch" type="checkbox" />
              <span class="box" />
              <span>{{ t("guard.killSwitch") }}</span>
            </label>
            <div class="field" style="max-width: 180px">
              <label for="guard-interval">{{ t("guard.interval") }}</label>
              <input id="guard-interval" v-model.number="form.proxyGuard.intervalSec" type="number" min="3" max="300" />
            </div>
          </div>
          <p class="hint" style="margin: 8px 0 0">{{ t("guard.desc") }}</p>
          <div v-if="store.proxyGuard" class="row" style="margin-top: 8px; gap: 8px">
            <span v-if="store.proxyGuard.armed" class="badge success">
              {{ t("guard.activeChip", { n: store.proxyGuard.reappliedToday }) }}
            </span>
            <span v-else-if="form.proxyGuard.enable" class="badge">{{ t("guard.idleChip") }}</span>
          </div>
        </div>
      </section>

      <!-- Ports -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.ports" @click="open.ports = !open.ports">
          <IconChevron :size="14" class="chev" :class="{ open: open.ports }" />
          {{ t("settings.secPorts") }}
        </button>
        <div v-if="open.ports" class="form-grid" style="margin-top: 12px">
          <div class="field">
            <label for="port-mixed">{{ t("settings.portMixed") }}</label>
            <input id="port-mixed" v-model.number="form.ports.mixed" type="number" min="1" max="65535" />
          </div>
          <div class="field">
            <label for="port-ctrl">{{ t("settings.portController") }}</label>
            <input id="port-ctrl" v-model.number="form.ports.controller" type="number" min="1" max="65535" />
          </div>
          <div class="field">
            <label for="port-speed">{{ t("settings.portSpeedTest") }}</label>
            <input id="port-speed" v-model.number="form.ports.speedTest" type="number" min="1" max="65535" />
          </div>
        </div>
      </section>

      <!-- Testing -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.testing" @click="open.testing = !open.testing">
          <IconChevron :size="14" class="chev" :class="{ open: open.testing }" />
          {{ t("settings.secTesting") }}
        </button>
        <div v-if="open.testing" class="form-grid" style="margin-top: 12px">
          <div class="field span-all">
            <label for="t-url">{{ t("settings.testUrl") }}</label>
            <input id="t-url" v-model="form.test.url" type="text" class="mono" />
          </div>
          <div class="field">
            <label for="t-timeout">{{ t("settings.timeout") }} ({{ t("common.ms") }})</label>
            <input id="t-timeout" v-model.number="form.test.timeoutMs" type="number" min="100" max="10000" step="100" />
          </div>
          <div class="field">
            <label for="t-streams">{{ t("settings.streams") }} (1–16)</label>
            <input id="t-streams" v-model.number="form.test.speedStreams" type="number" min="1" max="16" />
          </div>
          <div class="field span-all">
            <label for="t-speedurl">{{ t("settings.speedUrl") }}</label>
            <input id="t-speedurl" v-model="form.test.speedUrl" type="text" class="mono" />
          </div>
          <div class="field span-all">
            <label for="t-upurl">{{ t("settings.uploadUrl") }}</label>
            <input id="t-upurl" v-model="form.test.speedUploadUrl" type="text" class="mono" />
          </div>
          <div class="field">
            <label for="t-maxsec">{{ t("settings.maxSeconds") }}</label>
            <input id="t-maxsec" v-model.number="form.test.speedMaxSeconds" type="number" min="1" max="120" />
          </div>
          <div class="field">
            <label for="t-maxmb">{{ t("settings.maxMb") }} (MB)</label>
            <input id="t-maxmb" v-model.number="form.test.speedMaxMb" type="number" min="1" max="4096" />
          </div>
          <label class="checkbox">
            <input v-model="form.test.uploadTest" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.uploadTest") }}</span>
          </label>
        </div>
      </section>

      <!-- Failover -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.failover" @click="open.failover = !open.failover">
          <IconChevron :size="14" class="chev" :class="{ open: open.failover }" />
          {{ t("settings.secFailover") }}
        </button>
        <div v-if="open.failover" class="form-grid" style="margin-top: 12px">
          <label class="checkbox">
            <input v-model="form.failover.enable" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.failoverEnable") }}</span>
          </label>
          <div class="field">
            <label for="fo-interval">{{ t("settings.failoverInterval") }} ({{ t("common.seconds") }})</label>
            <input id="fo-interval" v-model.number="form.failover.intervalSec" type="number" min="15" max="600" />
          </div>
          <div class="field">
            <label for="fo-timeout">{{ t("settings.failoverTimeout") }} ({{ t("common.ms") }})</label>
            <input id="fo-timeout" v-model.number="form.failover.timeoutMs" type="number" min="200" max="10000" step="100" />
          </div>
          <div class="field">
            <label for="fo-strikes">{{ t("settings.failoverStrikes") }}</label>
            <input id="fo-strikes" v-model.number="form.failover.strikes" type="number" min="1" max="10" />
          </div>
          <div class="field">
            <label for="fo-cooldown">{{ t("settings.failoverCooldown") }} ({{ t("common.seconds") }})</label>
            <input id="fo-cooldown" v-model.number="form.failover.cooldownSec" type="number" min="0" max="3600" />
          </div>
        </div>
      </section>

      <!-- Core -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.core" @click="open.core = !open.core">
          <IconChevron :size="14" class="chev" :class="{ open: open.core }" />
          {{ t("settings.secCore") }}
        </button>
        <div v-if="open.core" class="form-grid" style="margin-top: 12px">
          <div class="field">
            <label for="core-channel">{{ t("settings.coreChannel") }}</label>
            <select id="core-channel" v-model="form.core.channel">
              <option value="stable">{{ t("settings.channelStable") }}</option>
              <option value="alpha">{{ t("settings.channelAlpha") }}</option>
            </select>
          </div>
          <div class="field">
            <label for="core-interval">{{ t("settings.coreUpdateInterval") }} ({{ t("common.hours") }})</label>
            <input id="core-interval" v-model.number="form.core.updateIntervalH" type="number" min="0" max="720" />
          </div>
          <label class="checkbox">
            <input v-model="form.core.autoUpdate" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.coreAutoUpdate") }}</span>
          </label>
          <div class="field span-all">
            <label for="core-ghproxy">{{ t("settings.ghProxy") }}</label>
            <input id="core-ghproxy" v-model="form.core.ghProxyPrefix" type="text" class="mono" placeholder="https://gh-proxy.com/" />
            <span class="hint">{{ t("settings.ghProxyHint") }}</span>
          </div>
        </div>
      </section>

      <!-- DNS -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.dns" @click="open.dns = !open.dns">
          <IconChevron :size="14" class="chev" :class="{ open: open.dns }" />
          {{ t("settings.secDns") }}
        </button>
        <div v-if="open.dns" style="margin-top: 12px" class="form-grid">
          <label class="checkbox">
            <input v-model="form.dns.override" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.dnsOverride") }}</span>
          </label>
          <div class="field span-all">
            <label for="dns-ns">{{ t("settings.dnsNameservers") }}</label>
            <textarea id="dns-ns" v-model="dnsText" rows="4" :placeholder="'https://dns.google/dns-query\n8.8.8.8'" />
            <span class="hint">{{ t("settings.dnsHint") }}</span>
          </div>
        </div>
      </section>

      <!-- TUN -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.tun" @click="open.tun = !open.tun">
          <IconChevron :size="14" class="chev" :class="{ open: open.tun }" />
          {{ t("settings.secTun") }}
        </button>
        <div v-if="open.tun" style="margin-top: 12px" class="form-grid">
          <label class="checkbox">
            <input
              v-model="form.tun.enable"
              type="checkbox"
              :disabled="store.elevatedKnown && !store.elevated && !serviceRunning"
            />
            <span class="box" />
            <span>{{ t("settings.tunEnable") }}</span>
          </label>
          <div class="field">
            <label for="tun-stack">{{ t("settings.tunStack") }}</label>
            <select id="tun-stack" v-model="form.tun.stack" :disabled="store.elevatedKnown && !store.elevated && !serviceRunning">
              <option value="system">system</option>
              <option value="gvisor">gvisor</option>
              <option value="mixed">mixed</option>
            </select>
          </div>
          <div v-if="store.elevatedKnown && !store.elevated && !serviceRunning" class="warn-box span-all">
            <IconWarn :size="15" />
            <span>{{ t("settings.tunNeedsAdminOrService") }}</span>
          </div>
          <div v-else-if="store.elevatedKnown && !store.elevated && serviceRunning" class="hint span-all" style="display: flex; align-items: center; gap: 6px">
            <IconCheck :size="14" />
            <span>{{ t("settings.tunViaService") }}</span>
          </div>

          <!-- TUN service mode (#14): one UAC prompt at install, never after -->
          <div v-if="tunSvc?.supported" class="span-all" style="margin-top: 10px; padding-top: 10px; border-top: 1px solid var(--border)">
            <div v-if="tunSvc.installed" class="row" style="gap: 8px; align-items: center; flex-wrap: wrap">
              <span class="dim small">{{ t("settings.tunService.label") }}</span>
              <span v-if="tunSvc.running" class="badge success">{{ t("settings.tunService.running") }}</span>
              <span v-else class="badge">{{ t("settings.tunService.installedStopped") }}</span>
              <span v-if="tunSvc.version" class="mono dim small">v{{ tunSvc.version }}</span>
              <button class="btn sm" :disabled="tunSvcBusy" @click="uninstallTunService">
                {{ t("settings.tunService.uninstall") }}
              </button>
            </div>
            <div v-else class="row" style="gap: 8px; align-items: center">
              <span class="dim small">{{ t("settings.tunService.label") }}</span>
              <span class="badge warn">{{ t("settings.tunService.notInstalled") }}</span>
              <button class="btn sm" :disabled="tunSvcBusy" @click="installTunService">
                {{ t("settings.tunService.install") }}
              </button>
            </div>
            <p class="hint" style="margin: 8px 0 0">{{ t("settings.tunService.desc") }}</p>
          </div>
        </div>
      </section>

      <!-- Proxy mode (#15): off / system / PAC -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.proxy" @click="open.proxy = !open.proxy">
          <IconChevron :size="14" class="chev" :class="{ open: open.proxy }" />
          {{ t("settings.secProxy") }}
        </button>
        <div v-if="open.proxy" style="margin-top: 12px" class="form-grid">
          <div class="field span-all">
            <label>{{ t("settings.proxyModeLabel") }}</label>
            <div class="row" style="gap: 8px; flex-wrap: wrap">
              <button
                v-for="m in PROXY_MODES"
                :key="m"
                class="btn sm"
                :class="{ primary: currentMode === m }"
                :disabled="store.proxyModeBusy"
                @click="applyMode(m)"
              >
                {{ t(`proxyMode.${m}`) }}
              </button>
              <span v-if="pacUrl" class="badge success mono">{{ t("settings.pacActive", { url: pacUrl }) }}</span>
              <span v-else-if="currentMode === 'pac'" class="badge warn">{{ t("settings.pacIdle") }}</span>
            </div>
            <p class="hint" style="margin: 8px 0 0">{{ t("settings.proxyModeHint") }}</p>
          </div>
          <div class="field">
            <label for="pac-port">{{ t("settings.pacPort") }}</label>
            <input id="pac-port" v-model.number="form.pac.port" type="number" min="1024" max="65535" />
          </div>
          <p class="hint span-all" style="margin: 0">{{ t("settings.pacPortAppliedNote") }}</p>
          <div class="field span-all">
            <label for="pac-script">{{ t("settings.pacScript") }}<span v-if="pacScriptDirty" class="dim"> *</span></label>
            <textarea
              id="pac-script"
              v-model="pacScript"
              class="mono"
              rows="10"
              spellcheck="false"
              style="width: 100%; resize: vertical"
              @input="markPacDirty"
            />
            <p class="hint">{{ t("settings.pacScriptHint") }}</p>
            <div class="row" style="gap: 8px; margin-top: 8px">
              <button class="btn sm" :disabled="store.proxyModeBusy || !pacScriptDirty" @click="saveScript">
                {{ t("settings.pacSave") }}
              </button>
              <button class="btn sm ghost" :disabled="store.proxyModeBusy" @click="resetScript">
                {{ t("settings.pacReset") }}
              </button>
            </div>
          </div>
        </div>
      </section>

      <!-- Subscriptions -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.subscriptions" @click="open.subscriptions = !open.subscriptions">
          <IconChevron :size="14" class="chev" :class="{ open: open.subscriptions }" />
          {{ t("settings.secSubscriptions") }}
        </button>
        <div v-if="open.subscriptions" class="form-grid" style="margin-top: 12px">
          <div class="field">
            <label for="sub-interval">{{ t("settings.subDefaultInterval") }} ({{ t("common.hours") }})</label>
            <input id="sub-interval" v-model.number="form.subscription.defaultIntervalH" type="number" min="0" max="720" />
          </div>
          <label class="checkbox">
            <input v-model="form.subscription.updateViaProxy" type="checkbox" />
            <span class="box" />
            <span>{{ t("settings.subViaProxy") }}</span>
          </label>
        </div>
      </section>

      <!-- Routing overrides (#50 / #10) -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.routing" @click="open.routing = !open.routing">
          <IconChevron :size="14" class="chev" :class="{ open: open.routing }" />
          {{ t("settings.secRouting") }}
          <span v-if="form.routingOverrides?.length" class="badge accent">{{ form.routingOverrides.length }}</span>
        </button>
        <div v-if="open.routing" style="margin-top: 12px">
          <p class="hint" style="margin: 0 0 10px">{{ t("settings.routingDesc") }}</p>

          <div v-if="!form.routingOverrides?.length" class="dim" style="padding: 6px 0 10px">
            {{ t("settings.routingEmpty") }}
          </div>

          <div v-for="(o, i) in form.routingOverrides" :key="i" class="row" style="gap: 6px; margin-bottom: 6px">
            <select v-model="o.type" class="input" style="width: 140px" :aria-label="t('settings.routingType')">
              <option v-for="ty in OV_TYPES" :key="ty" :value="ty">{{ t(`settings.routingType.${ty}`) }}</option>
            </select>
            <input
              v-model="o.pattern"
              class="input mono"
              :placeholder="i === 0 ? 'openai.com / telegram.exe' : ''"
              style="flex: 1; min-width: 120px"
              :aria-label="t('settings.routingPattern')"
            />
            <input
              v-model="o.target"
              class="input mono"
              list="ov-targets"
              :placeholder="t('settings.routingTarget')"
              style="flex: 1; min-width: 120px"
              :aria-label="t('settings.routingTarget')"
            />
            <button
              class="btn-icon"
              :disabled="i === 0"
              :aria-label="t('routing.moveUp')"
              :title="t('routing.moveUp')"
              @click="moveOverride(i, -1)"
            >
              <IconChevron :size="13" style="transform: rotate(-90deg)" />
            </button>
            <button
              class="btn-icon"
              :disabled="i === (form.routingOverrides?.length ?? 0) - 1"
              :aria-label="t('routing.moveDown')"
              :title="t('routing.moveDown')"
              @click="moveOverride(i, 1)"
            >
              <IconChevron :size="13" style="transform: rotate(90deg)" />
            </button>
            <button class="btn-icon" :aria-label="t('common.delete')" :title="t('common.delete')" @click="removeOverride(i)">
              <IconTrash :size="13" />
            </button>
          </div>
          <datalist id="ov-targets">
            <option v-for="tg in targetOptions" :key="tg" :value="tg" />
          </datalist>

          <div class="row" style="gap: 8px; margin-top: 10px">
            <button class="btn sm" @click="addOverride()">
              <IconPlus :size="13" />
              {{ t("settings.routingAdd") }}
            </button>
            <button class="btn sm" :disabled="!form.routingOverrides?.length" @click="exportOverrides()">
              {{ t("settings.routingExport") }}
            </button>
            <button class="btn sm" @click="ovFile?.click()">{{ t("settings.routingImport") }}</button>
            <input ref="ovFile" type="file" accept=".yaml,.yml,.txt" style="display: none" @change="importOverrides" />
          </div>
          <p class="hint" style="margin: 10px 0 0">{{ t("settings.routingHint") }}</p>
        </div>
      </section>

      <!-- Application updates (#13) -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.updates" @click="open.updates = !open.updates">
          <IconChevron :size="14" class="chev" :class="{ open: open.updates }" />
          {{ t("settings.secUpdates") }}
        </button>
        <div v-if="open.updates" style="margin-top: 12px">
          <div class="form-grid">
            <label class="checkbox">
              <input v-model="form.updates.autoCheck" type="checkbox" />
              <span class="box" />
              <span>{{ t("update.autoCheck") }}</span>
            </label>
            <label class="checkbox">
              <input v-model="form.updates.includePrerelease" type="checkbox" />
              <span class="box" />
              <span>{{ t("update.includePrerelease") }}</span>
            </label>
          </div>

          <div class="kv" style="margin-top: 10px">
            <span class="k">{{ t("about.version") }}</span>
            <span class="v mono">{{ upd?.current ?? store.appInfo?.version ?? "—" }}</span>
            <span class="k">{{ t("about.latest") }}</span>
            <span class="v mono">
              <template v-if="upd?.latest">
                <span v-if="upd.state === 'available'" class="badge warn">{{ upd.latest }}</span>
                <span v-else>{{ upd.latest }}</span>
              </template>
              <template v-else>—</template>
            </span>
            <span class="k">{{ t("update.status") }}</span>
            <span class="v">
              <span v-if="upd?.state === 'none'" class="badge success"><IconCheck :size="11" /> {{ t("update.upToDate") }}</span>
              <span v-else-if="upd?.state === 'error'" class="badge warn"><IconWarn :size="11" /> {{ upd.detail || t("update.checkFailed") }}</span>
              <span v-else-if="upd?.state === 'restarting'" class="badge warn">{{ t("update.restarting") }}</span>
              <span v-else class="dim">{{ t(`update.state.${upd?.state ?? "idle"}`) }}</span>
            </span>
          </div>

          <div v-if="upd?.state === 'downloading'" style="margin-top: 10px">
            <div class="row" style="justify-content: space-between; margin-bottom: 5px">
              <span class="dim small">{{ upd.detail }}</span>
              <span class="dim small mono">{{ Math.round(upd.percent) }}%</span>
            </div>
            <div class="progress">
              <div class="bar" :style="{ width: `${Math.min(100, Math.max(2, upd.percent))}%` }" />
            </div>
          </div>

          <!-- Issue #41: installing stage with indeterminate bar -->
          <div v-else-if="upd?.state === 'restarting'" style="margin-top: 10px">
            <div class="row" style="margin-bottom: 5px; gap: 8px">
              <span class="spinner" style="border-top-color: var(--accent)" />
              <span class="dim small">{{ upd.detail || t("update.restarting") }}</span>
            </div>
            <div class="progress indeterminate">
              <div class="bar" style="width: 40%" />
            </div>
          </div>

          <div v-if="!upd?.installed && upd?.state === 'available'" class="error-box" style="margin-top: 10px">
            <IconWarn :size="15" />
            <span>{{ t("update.portableHint") }}</span>
          </div>

          <div class="row" style="margin-top: 12px; flex-wrap: wrap">
            <button class="btn sm" :disabled="updBusy" @click="checkAppUpdate()">
              <span v-if="upd?.state === 'checking'" class="spinner" style="width: 11px; height: 11px" />
              <IconRefresh v-else :size="12" />
              {{ t("update.checkNow") }}
            </button>
            <button v-if="upd?.state === 'available'" class="btn sm primary" :disabled="updBusy" @click="downloadAppUpdate()">
              <IconDownload :size="12" />
              {{ t("update.download") }}
            </button>
            <button v-if="upd?.state === 'ready'" class="btn sm primary" :disabled="updBusy" @click="installAppUpdate()">
              <IconDownload :size="12" />
              {{ t("update.installRestart") }}
            </button>
            <button v-if="upd?.latest" class="btn sm" @click="openUpdateNotes()">
              <IconExternal :size="12" />
              {{ t("update.releaseNotes") }}
            </button>
          </div>
        </div>
      </section>

      <!-- Problem reporting (#36) -->
      <section class="panel">
        <button class="section-head" :aria-expanded="open.reporting" @click="toggleReporting(!open.reporting)">
          <IconChevron :size="14" class="chev" :class="{ open: open.reporting }" />
          {{ t("settings.secReporting") }}
        </button>
        <div v-if="open.reporting" style="margin-top: 12px">
          <p class="dim small" style="margin-bottom: 10px">{{ t("reporting.desc") }}</p>

          <div class="form-grid">
            <label class="checkbox">
              <input v-model="form.reporting.enable" type="checkbox" />
              <span class="box" />
              <span>{{ t("reporting.enable") }}</span>
            </label>
          </div>

          <div class="form-grid" style="margin-top: 10px">
            <label class="field">
              <span class="label">{{ t("reporting.token") }}</span>
              <input
                v-model="form.reporting.githubToken"
                type="password"
                :placeholder="t('reporting.tokenPlaceholder')"
                autocomplete="off"
                spellcheck="false"
                class="mono"
              />
            </label>
          </div>
          <div class="row" style="margin-top: 6px; align-items: center; flex-wrap: wrap">
            <span class="dim small">{{ t("reporting.tokenHelp") }}</span>
            <button class="btn sm" @click="Sys.OpenURL(tokenHelpURL)">
              <IconExternal :size="12" />
              {{ t("reporting.createToken") }}
            </button>
          </div>

          <div v-if="pending.length === 0" class="dim small" style="margin-top: 12px">
            {{ t("reporting.pendingEmpty") }}
          </div>
          <div v-else style="margin-top: 12px">
            <div class="row" style="justify-content: space-between">
              <strong class="small">{{ t("reporting.pending") }} ({{ pending.length }})</strong>
              <button class="btn sm" :disabled="reportBusy" @click="sendAllReports()">
                <IconRefresh :size="12" />
                {{ t("reporting.sendAll") }}
              </button>
            </div>
            <div v-for="p in pending" :key="p.id" class="row" style="justify-content: space-between; border-top: 1px solid var(--border); padding-top: 8px; margin-top: 8px">
              <div style="min-width: 0">
                <div class="small"><span class="badge warn">{{ p.type }}</span> {{ p.detail }}</div>
                <div class="dim" style="font-size: 11px">v{{ p.version }} · {{ p.time }} · sig {{ p.signature }}</div>
              </div>
              <div class="row" style="flex-shrink: 0">
                <button class="btn sm primary" :disabled="reportBusy" @click="sendOne(p.id)">{{ t("reporting.send") }}</button>
                <button class="btn sm" @click="deleteOne(p.id)">{{ t("reporting.delete") }}</button>
              </div>
            </div>
          </div>

          <div class="row" style="margin-top: 14px; flex-wrap: wrap">
            <button class="btn sm" :disabled="reportBusy" @click="sendTestReport()">
              <span v-if="reportBusy" class="spinner" style="width: 11px; height: 11px" />
              <IconRefresh v-else :size="12" />
              {{ reportBusy ? t("reporting.testing") : t("reporting.test") }}
            </button>
            <button class="btn sm" @click="openReportsFolder()">
              <IconFolder :size="12" />
              {{ t("reporting.openFolder") }}
            </button>
          </div>
        </div>
      </section>

      <!-- portable mode (#18) -->
      <div class="panel" data-testid="portable-panel">
        <div class="row" style="align-items: flex-start">
          <div style="flex: 1; min-width: 0">
            <div class="label">{{ t("settings.portable.title") }}</div>
            <div v-if="portable?.portable" class="small" style="margin-top: 4px">
              <span class="badge ok">{{ t("settings.portable.title") }}</span>
              {{ t("settings.portable.active", { dir: portable.dataDir }) }}
            </div>
            <div v-else class="small dim" style="margin-top: 4px">{{ t("settings.portable.standard") }}</div>
            <div
              v-if="portable?.portable && portable?.legacyPresent && !portable?.imported"
              class="small"
              style="margin-top: 10px"
            >
              <IconWarn :size="12" style="vertical-align: -2px" />
              {{ t("settings.portable.importBanner", { dir: portable?.legacyDir }) }}
            </div>
            <div v-if="portable?.imported" class="small" style="margin-top: 10px">
              <IconCheck :size="12" style="vertical-align: -2px" />
              {{ t("settings.portable.imported") }}
            </div>
          </div>
          <button
            v-if="portable?.portable && portable?.legacyPresent && !portable?.imported"
            class="btn sm primary"
            :disabled="importing"
            @click="importLegacy()"
          >
            <span v-if="importing" class="spinner" style="width: 11px; height: 11px" />
            {{ importing ? t("settings.portable.importing") : t("settings.portable.import") }}
          </button>
        </div>
      </div>

      <!-- folders -->
      <div class="panel">
        <div class="row">
          <button class="btn" @click="openFolder('config')">
            <IconFolder :size="14" />
            {{ t("settings.openConfig") }}
          </button>
          <button class="btn" @click="openFolder('core')">
            <IconFolder :size="14" />
            {{ t("settings.openCore") }}
          </button>
          <button class="btn" @click="openFolder('logs')">
            <IconFolder :size="14" />
            {{ t("settings.openLogs") }}
          </button>
        </div>
      </div>
    </template>
  </div>
</template>
