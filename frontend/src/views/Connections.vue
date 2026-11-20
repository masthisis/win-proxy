<script setup lang="ts">
/** Connections: live table from net:connections events (1 Hz). */
import { computed } from "vue";
import * as Proxy from "../../bindings/github.com/neurorift/neurorift/proxyservice.js";
import type { ConnItem } from "../../bindings/github.com/neurorift/neurorift/internal/engine/models.js";
import { store } from "../store";
import { t } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import { askConfirm } from "../lib/confirm";
import { fmtBytes, fmtDuration } from "../lib/format";
import { IconClose, IconConnections, IconDownload, IconUpload } from "../components/Icons";

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
            <td class="mono" style="max-width: 240px; overflow: hidden; text-overflow: ellipsis" :title="c.host">
              {{ c.host || "—" }}
            </td>
            <td class="dim">{{ c.network }}</td>
            <td class="dim" style="max-width: 200px; overflow: hidden; text-overflow: ellipsis" :title="c.rule">
              {{ c.rule || "—" }}
            </td>
            <td class="dim" style="max-width: 240px; overflow: hidden; text-overflow: ellipsis" :title="c.chain">
              {{ c.chain || "—" }}
            </td>
            <td class="dim" style="max-width: 160px; overflow: hidden; text-overflow: ellipsis" :title="c.process">
              {{ c.process || "—" }}
            </td>
            <td class="num">{{ fmtBytes(c.down) }}</td>
            <td class="num">{{ fmtBytes(c.up) }}</td>
            <td class="num dim">{{ duration(c.start) }}</td>
            <td>
              <button class="btn-icon" :aria-label="t('conn.close')" :title="t('conn.close')" @click="closeConn(c.id)">
                <IconClose :size="13" />
              </button>
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
  </div>
</template>
