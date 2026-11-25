<script setup lang="ts">
/** Connections: live table from net:connections events (1 Hz). */
import { computed, ref } from "vue";
import * as Proxy from "../../bindings/github.com/neurorift/neurorift/proxyservice.js";
import type { ConnItem } from "../../bindings/github.com/neurorift/neurorift/internal/engine/models.js";
import { store, addRoutingOverride } from "../store";
import { t } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import { askConfirm } from "../lib/confirm";
import { fmtBytes, fmtDuration } from "../lib/format";
import Modal from "../components/Modal.vue";
import { IconClose, IconConnections, IconDownload, IconLink, IconUpload } from "../components/Icons";

const items = computed<ConnItem[]>(() => {
  const list = store.connections?.items ?? [];
  return [...list].sort((a, b) => (a.start < b.start ? 1 : a.start > b.start ? -1 : 0));
});

const totalDown = computed(() => store.connections?.downloadTotal ?? 0);
const totalUp = computed(() => store.connections?.uploadTotal ?? 0);
const hasItems = computed(() => items.value.length > 0);

function duration(start: string): string {
  const t0 = Date.parse(start);
  if (Number.isNaN(t0)) return "—";
  return fmtDuration(Math.max(0, (store.now - t0) / 1000));
}

async function closeConn(id: string): Promise<void> {
  try {
    await Proxy.CloseConnection(id);
  } catch (e) {
    toast.error(errMsg(e));
  }
}

async function closeAll(): Promise<void> {
  if (!hasItems.value) return;
  const ok = await askConfirm({
    title: t("conn.confirmCloseAllTitle"),
    body: t("conn.confirmCloseAllBody", { n: items.value.length }),
    confirmLabel: t("conn.closeAll"),
    danger: true,
  });
  if (!ok) return;
  try {
    await Proxy.CloseAllConnections();
  } catch (e) {
    toast.error(errMsg(e));
  }
}

/* ---- routing override quick-add (#50 / #10) ---- */

/** A host is routable when it is a domain (IP literals have no DOMAIN rule). */
function isDomainHost(host: string): boolean {
  return !!host && host.includes(".") && !/^\d{1,3}(\.\d{1,3}){3}/.test(host);
}

const routeHost = ref<string | null>(null);
const routeTarget = ref("DIRECT");
const routeBusy = ref(false);

/** Route targets offered in the quick-add modal: built-ins + visible groups. */
const routeTargets = computed<string[]>(() => {
  const opts = ["DIRECT", "REJECT"];
  for (const g of store.groups) {
    if (!g.hidden) opts.push(g.name);
  }
  return opts;
});

function askRoute(host: string): void {
  routeHost.value = host;
  routeTarget.value = "DIRECT";
}

async function applyRoute(): Promise<void> {
  const host = routeHost.value;
  if (!host || routeBusy.value) return;
  routeBusy.value = true;
  try {
    await addRoutingOverride(host, "domain-suffix", routeTarget.value);
    toast.success(t("conn.routeAdded", { host }));
    routeHost.value = null;
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    routeBusy.value = false;
  }
}
</script>

<template>
  <div class="view">
    <div class="panel">
      <div class="row" style="justify-content: space-between">
        <div class="row">
          <span class="badge" :class="{ accent: hasItems }">{{ t("conn.count", { n: items.length }) }}</span>
          <span class="rate down small">
            <IconDownload :size="12" />
            {{ t("conn.totalDown") }}: <b>{{ fmtBytes(totalDown) }}</b>
          </span>
          <span class="rate up small">
            <IconUpload :size="12" />
            {{ t("conn.totalUp") }}: <b>{{ fmtBytes(totalUp) }}</b>
          </span>
        </div>
        <button class="btn danger sm" :disabled="!hasItems" @click="closeAll()">
          <IconClose :size="13" />
          {{ t("conn.closeAll") }}
        </button>
      </div>
    </div>

    <div v-if="hasItems" class="table-wrap" style="max-height: calc(100vh - 210px)">
      <table class="tbl">
        <thead>
          <tr>
            <th>{{ t("conn.host") }}</th>
            <th>{{ t("conn.network") }}</th>
            <th>{{ t("conn.rule") }}</th>
            <th>{{ t("conn.chain") }}</th>
            <th>{{ t("conn.process") }}</th>
            <th>{{ t("conn.down") }}</th>
            <th>{{ t("conn.up") }}</th>
            <th>{{ t("conn.duration") }}</th>
            <th style="width: 1%"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in items" :key="c.id">
            <td class="mono" data-label="" style="max-width: 240px; overflow: hidden; text-overflow: ellipsis" :title="c.host">
              {{ c.host || "—" }}
            </td>
            <td class="dim" :data-label="t('conn.network')">{{ c.network }}</td>
            <td class="dim" :data-label="t('conn.rule')" style="max-width: 200px; overflow: hidden; text-overflow: ellipsis" :title="c.rule">
              {{ c.rule || "—" }}
            </td>
            <td class="dim" :data-label="t('conn.chain')" style="max-width: 240px; overflow: hidden; text-overflow: ellipsis" :title="c.chain">
              {{ c.chain || "—" }}
            </td>
            <td class="dim" :data-label="t('conn.process')" style="max-width: 160px; overflow: hidden; text-overflow: ellipsis" :title="c.process">
              {{ c.process || "—" }}
            </td>
            <td class="num" :data-label="t('conn.down')">{{ fmtBytes(c.down) }}</td>
            <td class="num" :data-label="t('conn.up')">{{ fmtBytes(c.up) }}</td>
            <td class="num dim" :data-label="t('conn.duration')">{{ duration(c.start) }}</td>
            <td class="td-actions">
              <span class="row" style="gap: 2px">
                <button
                  v-if="isDomainHost(c.host)"
                  class="btn-icon"
                  :aria-label="t('conn.routeVia')"
                  :title="t('conn.routeVia')"
                  @click="askRoute(c.host)"
                >
                  <IconLink :size="13" />
                </button>
                <button class="btn-icon" :aria-label="t('conn.close')" :title="t('conn.close')" @click="closeConn(c.id)">
                  <IconClose :size="13" />
                </button>
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else class="panel">
      <div class="empty">
        <IconConnections :size="30" />
        <div class="e-title">{{ t("conn.emptyTitle") }}</div>
        <div class="e-body">{{ t("conn.emptyBody") }}</div>
      </div>
    </div>

    <!-- Routing override quick-add (#50) -->
    <Modal :open="routeHost !== null" :title="t('conn.routeViaTitle')" @close="routeHost = null">
      <p style="margin: 0 0 10px">{{ t("conn.routeViaBody", { host: routeHost ?? "" }) }}</p>
      <div class="form-grid">
        <label v-for="tg in routeTargets" :key="tg" class="radio">
          <input v-model="routeTarget" type="radio" :value="tg" name="route-target" />
          <span class="mono">{{ tg }}</span>
        </label>
      </div>
      <template #actions>
        <button class="btn" @click="routeHost = null">{{ t("common.cancel") }}</button>
        <button class="btn primary" :disabled="routeBusy" @click="applyRoute()">{{ t("common.confirm") }}</button>
      </template>
    </Modal>
  </div>
</template>
