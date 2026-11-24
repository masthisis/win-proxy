<script setup lang="ts">
/** Profiles: add (URL/clipboard/file), manage, update, rename, delete. */
import { computed, ref } from "vue";
import * as AppSvc from "../../bindings/github.com/neurorift/neurorift/appservice.js";
import * as Profile from "../../bindings/github.com/neurorift/neurorift/profileservice.js";
import type { Profile as ProfileSummary } from "../../bindings/github.com/neurorift/neurorift/internal/profile/models.js";
import type { LintIssue } from "../../bindings/github.com/neurorift/neurorift/internal/profile/models.js";
import { store, refreshProfiles } from "../store";
import { t } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import { askConfirm } from "../lib/confirm";
import { fmtBytes, fmtDateTime, relTimeKey, sourceHost, isZeroTime } from "../lib/format";
import Modal from "../components/Modal.vue";
import {
  IconLink,
  IconClipboard,
  IconFile,
  IconQR,
  IconPlus,
  IconRefresh,
  IconCheck,
  IconEdit,
  IconTrash,
  IconWarn,
} from "../components/Icons";

const INTERVALS = [0, 1, 6, 12, 24, 72];

const url = ref("");
const name = ref("");
const userAgent = ref("");
const headersText = ref("");
const addingUrl = ref(false);
const clipboardBusy = ref(false);
const fileBusy = ref(false);
const qrBusy = ref(false);
const updatingId = ref<string | null>(null);
const activatingId = ref<string | null>(null);
const deletingId = ref<string | null>(null);
const intervalBusyId = ref<string | null>(null);
const uaBusyId = ref<string | null>(null);

/** inline rename */
const editingId = ref<string | null>(null);
const editName = ref("");

function isActive(p: ProfileSummary): boolean {
  return p.id === store.activeProfileId || (!!store.engineStatus?.activeProfile && p.id === store.engineStatus.activeProfile);
}

function rel(iso: string | undefined): string {
  const r = relTimeKey(iso, store.now);
  return r ? t(r.key, r.params) : "—";
}

function intervalLabel(h: number): string {
  return h === 0 ? t("common.manual") : `${h}h`;
}

const busyPhase = (id: string): string | null => {
  const p = store.subProgress[id];
  if (!p) return null;
  if (p.phase === "done" || p.phase === "error") return null;
  return p.phase;
};

const subPercent = (p: ProfileSummary): number => {
  const si = p.subInfo;
  if (!si || !si.total) return 0;
  return Math.min(100, ((si.upload + si.download) / si.total) * 100);
};

async function addFromUrl(): Promise<void> {
  const u = url.value.trim();
  if (!u || addingUrl.value) return;
  addingUrl.value = true;
  try {
    const p = await Profile.AddFromURL(u, name.value.trim(), userAgent.value.trim(), parseHeaders(headersText.value));
    toast.success(t("toast.profileAdded", { name: p?.name ?? u }));
    url.value = "";
    name.value = "";
    userAgent.value = "";
    headersText.value = "";
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    addingUrl.value = false;
  }
}

async function fromClipboard(): Promise<void> {
  if (clipboardBusy.value) return;
  clipboardBusy.value = true;
  try {
    const p = await Profile.ImportFromClipboard();
    toast.success(t("toast.profileAdded", { name: p?.name ?? "" }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    clipboardBusy.value = false;
  }
}

async function fromFile(): Promise<void> {
  if (fileBusy.value) return;
  fileBusy.value = true;
  try {
    const path = await AppSvc.PickClashFile();
    if (!path) return;
    const p = await Profile.AddFromFile(path);
    toast.success(t("toast.profileAdded", { name: p?.name ?? path }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    fileBusy.value = false;
  }
}

/** Scan QR from the whole screen (Windows) and import its payload (#102). */
async function scanScreenQR(): Promise<void> {
  if (qrBusy.value) return;
  qrBusy.value = true;
  try {
    const p = await Profile.ScanScreenQR();
    toast.success(t("toast.profileAdded", { name: p?.name ?? "QR" }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    qrBusy.value = false;
  }
}

async function activate(p: ProfileSummary): Promise<void> {
  if (activatingId.value) return;
  activatingId.value = p.id;
  try {
    await Profile.SetActive(p.id);
    store.activeProfileId = p.id;
    toast.success(t("toast.activated", { name: p.name }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    activatingId.value = null;
  }
}

const updatingAll = ref(false);

async function updateAll(): Promise<void> {
  if (updatingAll.value) return;
  updatingAll.value = true;
  try {
    const sum = await Profile.UpdateAll();
    if (sum?.failed) {
      toast.error(t("profiles.updateAllSome", { ok: sum.updated ?? 0, fail: sum.failed }));
    } else {
      toast.success(t("profiles.updateAllDone", { n: sum?.updated ?? 0 }));
    }
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    updatingAll.value = false;
  }
}

async function updateNow(p: ProfileSummary): Promise<void> {
  if (updatingId.value) return;
  updatingId.value = p.id;
  try {
    const updated = await Profile.Update(p.id);
    toast.success(t("toast.profileUpdated", { name: updated?.name ?? p.name }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    updatingId.value = null;
  }
}

const unionBusyId = ref<string | null>(null);

async function toggleUnion(p: ProfileSummary): Promise<void> {
  if (unionBusyId.value) return;
  unionBusyId.value = p.id;
  const next = !p.union;
  try {
    await Profile.SetUnion(p.id, next);
    p.union = next;
    toast.success(next ? t("profiles.unionOn", { name: p.name }) : t("profiles.unionOff", { name: p.name }));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    unionBusyId.value = null;
  }
}

async function setIntervalH(p: ProfileSummary, h: number): Promise<void> {
  if (intervalBusyId.value || h === p.intervalH) return;
  intervalBusyId.value = p.id;
  try {
    await Profile.SetInterval(p.id, h);
    p.intervalH = h;
    toast.success(t("profiles.intervalSet"));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    intervalBusyId.value = null;
  }
}

async function setUserAgent(p: ProfileSummary, ua: string): Promise<void> {
  const v = ua.trim();
  if (uaBusyId.value || v === (p.userAgent ?? "")) return;
  uaBusyId.value = p.id;
  try {
    await Profile.SetUserAgent(p.id, v);
    p.userAgent = v || undefined;
    toast.success(v ? t("profiles.userAgentSet") : t("profiles.userAgentReset"));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    uaBusyId.value = null;
  }
}

// ---- custom fetch headers (#75, custom-header half) ----
// Textarea format: one "Name: value" per line. Empty text clears.
type HeaderMap = { [x: string]: string | undefined };

function parseHeaders(text: string): HeaderMap {
  const out: HeaderMap = {};
  for (const line of text.split("\n")) {
    const i = line.indexOf(":");
    if (i <= 0) continue;
    const k = line.slice(0, i).trim();
    const v = line.slice(i + 1).trim();
    if (k && v) out[k] = v;
  }
  return out;
}

function headersToText(h?: HeaderMap): string {
  if (!h) return "";
  return Object.entries(h)
    .filter(([k, v]) => k && v)
    .map(([k, v]) => `${k}: ${v}`)
    .join("\n");
}

const hdrBusyId = ref<string | null>(null);

async function setUserHeaders(p: ProfileSummary, text: string): Promise<void> {
  const next = parseHeaders(text);
  const current = headersToText(p.headers);
  if (hdrBusyId.value || text.trim() === current) return;
  hdrBusyId.value = p.id;
  try {
    await Profile.SetHeaders(p.id, next);
    p.headers = next;
    toast.success(Object.keys(next).length ? t("profiles.headersSet") : t("profiles.headersReset"));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    hdrBusyId.value = null;
  }
}

function startRename(p: ProfileSummary): void {
  editingId.value = p.id;
  editName.value = p.name;
}

async function saveRename(p: ProfileSummary): Promise<void> {
  const n = editName.value.trim();
  editingId.value = null;
  if (!n || n === p.name) return;
  try {
    await Profile.Rename(p.id, n);
    p.name = n;
    toast.success(t("toast.renamed"));
  } catch (e) {
    toast.error(errMsg(e));
  }
}

async function removeProfile(p: ProfileSummary): Promise<void> {
  const ok = await askConfirm({
    title: t("profiles.confirmDeleteTitle"),
    body: t("profiles.confirmDeleteBody", { name: p.name }),
    confirmLabel: t("common.delete"),
    danger: true,
  });
  if (!ok) return;
  deletingId.value = p.id;
  try {
    await Profile.Remove(p.id);
    toast.success(t("toast.profileRemoved"));
    await refreshProfiles();
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    deletingId.value = null;
  }
}

const kindBadge = (p: ProfileSummary): string => (p.kind === "clash" ? "violet" : p.kind === "local" ? "warn" : "accent");

/** URL-sourced subscription (issue #75): User-Agent override only applies to these. */
function isUrlSub(p: ProfileSummary): boolean {
  return /^https?:\/\//i.test(p.source);
}

/* ---------------------------------------------------------------- */
/* raw YAML editor (#49 / #9)                                         */
/* ---------------------------------------------------------------- */

const yamlOpen = ref(false);
const yamlProfile = ref<ProfileSummary | null>(null);
const yamlText = ref("");
const yamlOriginal = ref("");
const yamlIssues = ref<LintIssue[]>([]);
const yamlChecking = ref(false);
const yamlSaving = ref(false);
let yamlDebounce: ReturnType<typeof setTimeout> | undefined;

const yamlEditable = computed(
  () => yamlProfile.value?.kind === "clash" || yamlProfile.value?.kind === "local",
);
const yamlValid = computed(() => (yamlIssues.value?.length ?? 0) === 0);
const yamlDirty = computed(() => yamlText.value !== yamlOriginal.value);
const yamlTitle = computed(() =>
  yamlEditable.value ? t("yaml.editTitle", { name: yamlProfile.value?.name ?? "" }) : t("yaml.viewTitle", { name: yamlProfile.value?.name ?? "" }),
);

async function editYaml(p: ProfileSummary): Promise<void> {
  try {
    const raw = await Profile.GetRaw(p.id);
    yamlProfile.value = p;
    yamlText.value = raw;
    yamlOriginal.value = raw;
    yamlIssues.value = [];
    yamlOpen.value = true;
  } catch (e) {
    toast.error(errMsg(e));
  }
}

function closeYaml(): void {
  clearTimeout(yamlDebounce);
  yamlOpen.value = false;
  yamlProfile.value = null;
}

/** Debounced live validation (500 ms, issue #49). */
function onYamlInput(): void {
  if (!yamlEditable.value) return;
  clearTimeout(yamlDebounce);
  yamlDebounce = setTimeout(async () => {
    yamlChecking.value = true;
    try {
      const issues = await Profile.ValidateRaw(yamlText.value);
      yamlIssues.value = issues ?? [];
    } catch {
      yamlIssues.value = [];
    } finally {
      yamlChecking.value = false;
    }
  }, 500);
}

async function saveYaml(): Promise<void> {
  if (!yamlProfile.value || !yamlEditable.value || yamlSaving.value || !yamlValid.value) return;
  yamlSaving.value = true;
  try {
    const res = await Profile.UpdateRaw(yamlProfile.value.id, yamlText.value);
    if (res && !res.ok) {
      yamlIssues.value = res.issues ?? [];
      toast.error(t("yaml.notSaved"));
      return;
    }
    toast.success(t("yaml.savedToast", { name: yamlProfile.value.name }));
    await refreshProfiles();
    closeYaml();
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    yamlSaving.value = false;
  }
}

// Ctrl+U = update all subscriptions (issue #46).
if (typeof window !== "undefined") {
  window.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "u") {
      e.preventDefault();
      void updateAll();
    }
  });
}
</script>

<template>
  <div class="view">
    <!-- add profile -->
    <section class="panel">
      <header class="panel-head">
        <h2>{{ t("profiles.addTitle") }}</h2>
        <div class="panel-head-actions">
          <button
            v-if="store.profiles.length"
            class="btn sm"
            :disabled="updatingAll || Object.keys(store.subProgress).length > 0"
            title="Ctrl+U"
            @click="updateAll()"
          >
            <span v-if="updatingAll" class="spinner" style="width: 11px; height: 11px" />
            <IconRefresh v-else :size="12" />
            {{ t("profiles.updateAll") }}
          </button>
        </div>
      </header>
      <div class="row" style="align-items: flex-end">
        <div class="field" style="flex: 2; min-width: 220px">
          <label for="sub-url">{{ t("profiles.urlLabel") }}</label>
          <input id="sub-url" v-model="url" type="text" :placeholder="t('profiles.urlPlaceholder')" @keyup.enter="addFromUrl" />
        </div>
        <div class="field" style="flex: 1; min-width: 140px">
          <label for="sub-name">&nbsp;</label>
          <input id="sub-name" v-model="name" type="text" :placeholder="t('profiles.namePlaceholder')" @keyup.enter="addFromUrl" />
        </div>
        <button class="btn primary" :disabled="!url.trim() || addingUrl" @click="addFromUrl">
          <span v-if="addingUrl" class="spinner" />
          <IconPlus v-else :size="14" />
          {{ t("profiles.fromUrl") }}
        </button>
      </div>
      <div class="row" style="margin-top: 10px">
        <div class="field" style="flex: 1; min-width: 220px; max-width: 420px">
          <label for="sub-ua">{{ t("profiles.userAgent") }}</label>
          <input
            id="sub-ua"
            v-model="userAgent"
            type="text"
            :placeholder="t('profiles.userAgentPlaceholder')"
            :title="t('profiles.userAgentHint')"
            @keyup.enter="addFromUrl"
          />
        </div>
        <div class="field" style="flex: 1; min-width: 220px; max-width: 420px">
          <label for="sub-headers">{{ t("profiles.headers") }}</label>
          <textarea
            id="sub-headers"
            v-model="headersText"
            rows="2"
            class="mono"
            :placeholder="t('profiles.headersPlaceholder')"
            :title="t('profiles.headersHint')"
          />
        </div>
      </div>
      <div class="row" style="margin-top: 10px">
        <button class="btn" :disabled="clipboardBusy" @click="fromClipboard()">
          <span v-if="clipboardBusy" class="spinner" />
          <IconClipboard v-else :size="14" />
          {{ t("profiles.fromClipboard") }}
        </button>
        <button class="btn" :disabled="fileBusy" @click="fromFile()">
          <span v-if="fileBusy" class="spinner" />
          <IconFile v-else :size="14" />
          {{ t("profiles.fromFile") }}
        </button>
        <button class="btn" :disabled="qrBusy" @click="scanScreenQR()">
          <span v-if="qrBusy" class="spinner" />
          <IconQR v-else :size="14" />
          {{ t("profiles.scanQR") }}
        </button>
        <span class="dim small">{{ t("profiles.addHint") }}</span>
      </div>
    </section>

    <!-- empty state -->
    <section v-if="store.profilesLoaded && !store.profiles.length" class="panel">
      <div class="empty">
        <IconLink :size="30" />
        <div class="e-title">{{ t("profiles.emptyTitle") }}</div>
        <div class="e-body">{{ t("profiles.emptyBody") }}</div>
      </div>
    </section>

    <!-- profile cards -->
    <section v-for="p in store.profiles" :key="p.id" class="panel">
      <header class="panel-head">
        <template v-if="editingId === p.id">
          <input
            v-model="editName"
            type="text"
            class="mono"
            style="max-width: 260px"
            :aria-label="t('common.rename')"
            @keyup.enter="saveRename(p)"
            @keyup.escape="editingId = null"
          />
          <button class="btn sm primary" @click="saveRename(p)">
            <IconCheck :size="13" />
            {{ t("profiles.renameSave") }}
          </button>
          <button class="btn sm ghost" @click="editingId = null">{{ t("common.cancel") }}</button>
        </template>
        <template v-else>
          <h2 :title="p.name">{{ p.name }}</h2>
          <span class="badge" :class="kindBadge(p)">{{ p.kind }}</span>
          <span v-if="isActive(p)" class="badge success">
            <IconCheck :size="11" />
            {{ t("common.active") }}
          </span>
          <span v-if="p.union && !isActive(p)" class="badge accent" :title="t('profiles.unionHint')">
            <IconLink :size="11" />
            {{ t("profiles.unionBadge") }}
          </span>
          <span v-if="busyPhase(p.id)" class="badge warn">
            <span class="spinner" style="width: 10px; height: 10px; border-top-color: var(--warn)" />
            {{ t(`profiles.phase.${busyPhase(p.id)}`) }}
          </span>
          <span v-else-if="p.lastError" class="badge danger" :title="p.lastError">
            <IconWarn :size="11" />
            {{ t("profiles.syncFailed") }}
          </span>
          <div class="panel-head-actions">
            <button
              v-if="!isActive(p)"
              class="btn sm"
              :disabled="activatingId === p.id"
              @click="activate(p)"
            >
              <span v-if="activatingId === p.id" class="spinner" style="width: 11px; height: 11px" />
              {{ t("common.activate") }}
            </button>
            <button
              class="btn sm"
              :title="t('profiles.updateNow')"
              :disabled="updatingId === p.id || busyPhase(p.id) !== null"
              @click="updateNow(p)"
            >
              <span v-if="updatingId === p.id" class="spinner" style="width: 11px; height: 11px" />
              <IconRefresh v-else :size="12" />
              {{ t("common.update") }}
            </button>
            <button
              class="btn-icon"
              :class="{ on: p.union }"
              :aria-label="t('profiles.unionToggle')"
              :title="t('profiles.unionToggle')"
              :disabled="unionBusyId === p.id || isActive(p)"
              @click="toggleUnion(p)"
            >
              <span v-if="unionBusyId === p.id" class="spinner" style="width: 11px; height: 11px" />
              <IconLink v-else :size="14" />
            </button>
            <button
              class="btn-icon"
              :aria-label="p.kind === 'nodes' ? t('yaml.view') : t('yaml.edit')"
              :title="p.kind === 'nodes' ? t('yaml.view') : t('yaml.edit')"
              @click="editYaml(p)"
            >
              <IconFile :size="14" />
            </button>
            <button class="btn-icon" :aria-label="t('common.rename')" :title="t('common.rename')" @click="startRename(p)">
              <IconEdit :size="14" />
            </button>
            <button
              class="btn-icon"
              :aria-label="t('common.delete')"
              :title="t('common.delete')"
              :disabled="deletingId === p.id"
              @click="removeProfile(p)"
            >
              <span v-if="deletingId === p.id" class="spinner" style="width: 11px; height: 11px" />
              <IconTrash v-else :size="14" />
            </button>
          </div>
        </template>
      </header>

      <div class="kv">
        <span class="k">{{ t("profiles.source") }}</span>
        <span class="v mono" :title="p.source">{{ sourceHost(p.source) }}</span>
        <span class="k">{{ t("common.nodes") }}</span>
        <span class="v">{{ p.nodeCount }}</span>
        <span class="k">{{ t("profiles.updated") }}</span>
        <span class="v" :title="fmtDateTime(p.updatedAt)">{{ rel(p.updatedAt) }}</span>
        <span class="k">{{ t("profiles.nextUpdate") }}</span>
        <span class="v">
          <template v-if="p.intervalH > 0 && !isZeroTime(p.nextUpdateAt)">{{ rel(p.nextUpdateAt) }}</template>
          <template v-else>{{ t("common.manual") }}</template>
        </span>
        <span class="k">{{ t("profiles.interval") }}</span>
        <span class="v">
          <select
            :value="p.intervalH"
            :disabled="intervalBusyId === p.id"
            :aria-label="t('profiles.interval')"
            @change="setIntervalH(p, Number(($event.target as HTMLSelectElement).value))"
          >
            <option v-for="h in INTERVALS" :key="h" :value="h">
              {{ intervalLabel(h) }}
            </option>
          </select>
        </span>
        <template v-if="isUrlSub(p)">
          <span class="k">{{ t("profiles.userAgent") }}</span>
          <span class="v">
            <input
              :value="p.userAgent ?? ''"
              :disabled="uaBusyId === p.id"
              type="text"
              class="mono"
              style="width: 100%; max-width: 280px"
              :placeholder="t('profiles.userAgentPlaceholder')"
              :title="t('profiles.userAgentHint')"
              :aria-label="t('profiles.userAgent')"
              @change="setUserAgent(p, ($event.target as HTMLInputElement).value)"
            />
          </span>
          <span class="k">{{ t("profiles.headers") }}</span>
          <span class="v">
            <textarea
              :value="headersToText(p.headers)"
              :disabled="hdrBusyId === p.id"
              rows="2"
              class="mono"
              style="width: 100%; max-width: 280px; resize: vertical"
              :placeholder="t('profiles.headersPlaceholder')"
              :title="t('profiles.headersHint')"
              :aria-label="t('profiles.headers')"
              @change="setUserHeaders(p, ($event.target as HTMLTextAreaElement).value)"
            />
          </span>
        </template>
      </div>

      <!-- subscription usage -->
      <div v-if="p.subInfo && p.subInfo.total > 0" style="margin-top: 12px">
        <div class="row" style="justify-content: space-between; margin-bottom: 5px">
          <span class="dim small">
            {{ t("profiles.subUsed") }}:
            {{ fmtBytes(p.subInfo.upload + p.subInfo.download) }} / {{ fmtBytes(p.subInfo.total) }}
          </span>
          <span class="dim small">
            ↑ {{ fmtBytes(p.subInfo.upload) }} · ↓ {{ fmtBytes(p.subInfo.download) }}
          </span>
        </div>
        <div class="progress" :class="{ danger: subPercent(p) > 90, warn: subPercent(p) > 75 && subPercent(p) <= 90 }">
          <div class="bar" :style="{ width: `${subPercent(p)}%` }" />
        </div>
        <div v-if="!isZeroTime(p.subInfo.expireAt)" class="dim small" style="margin-top: 5px">
          {{ t("profiles.subExpire") }}: {{ fmtDateTime(p.subInfo.expireAt) }}
        </div>
      </div>

      <!-- last sub progress line (done/error) -->
      <div
        v-if="store.subProgress[p.id] && (store.subProgress[p.id].phase === 'error')"
        class="error-box"
        style="margin-top: 10px"
      >
        <IconWarn :size="14" />
        <span>{{ store.subProgress[p.id].detail || t("profiles.phase.error") }}</span>
      </div>
    </section>

    <!-- raw YAML editor / viewer (#49) -->
    <Modal :open="yamlOpen" :title="yamlTitle" @close="closeYaml">
      <p v-if="!yamlEditable" class="dim small" style="margin: 0 0 8px">{{ t("yaml.readOnly") }}</p>
      <textarea
        v-model="yamlText"
        class="mono"
        spellcheck="false"
        :readonly="!yamlEditable"
        style="width: 100%; min-height: 320px; resize: vertical; font-size: 12px; line-height: 1.5; white-space: pre; overflow: auto"
        @input="onYamlInput"
      />
      <div v-if="yamlIssues?.length" class="error-box" style="margin-top: 10px">
        <IconWarn :size="14" />
        <ul style="margin: 0; padding-left: 16px">
          <li v-for="(is, i) in yamlIssues" :key="i" class="mono">{{ t("yaml.line") }} {{ is.line }}: {{ is.message }}</li>
        </ul>
      </div>
      <div v-else-if="yamlEditable" class="row" style="margin-top: 10px; gap: 6px">
        <span v-if="yamlChecking" class="dim small">{{ t("yaml.checking") }}</span>
        <span v-else-if="yamlText" class="badge success">{{ t("yaml.valid") }}</span>
      </div>
      <template #actions>
        <button class="btn" @click="closeYaml()">{{ t("common.close") }}</button>
        <button
          v-if="yamlEditable"
          class="btn primary"
          :disabled="!yamlValid || !yamlDirty || yamlSaving || yamlChecking"
          @click="saveYaml()"
        >
          <span v-if="yamlSaving" class="spinner" style="width: 11px; height: 11px" />
          {{ t("common.save") }}
        </button>
      </template>
    </Modal>
  </div>
</template>
