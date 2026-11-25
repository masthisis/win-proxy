<script setup lang="ts">
/**
 * Traffic view (compact layout, #123 Stitch design): Connections and Live
 * Logs merge under one tab with the design's segmented switcher and the
 * totals + Close-all strip. The desktop layout keeps the dedicated
 * Connections / Logs views — this wrapper is additive, not a replacement.
 */
import { computed, ref } from "vue";
import Connections from "./Connections.vue";
import Logs from "./Logs.vue";
import { store } from "../store";
import { t } from "../lib/i18n";
import { fmtBytes } from "../lib/format";
import * as Proxy from "../../bindings/github.com/neurorift/neurorift/proxyservice.js";
import { toast, errMsg } from "../lib/toast";

const tab = ref<"conn" | "logs">("conn");
const closing = ref(false);

const connCount = computed(() => store.connections?.items?.length ?? 0);

async function closeAll(): Promise<void> {
  if (closing.value) return;
  closing.value = true;
  try {
    await Proxy.CloseAllConnections();
    toast.success(t("connections.closedAll"));
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    closing.value = false;
  }
}
</script>

<template>
  <div class="view traffic-tabs">
    <!-- Segmented view switcher: Connections | Live Logs -->
    <div class="seg" role="tablist" :aria-label="t('traffic.views')">
      <button role="tab" :class="{ active: tab === 'conn' }" :aria-selected="tab === 'conn'" @click="tab = 'conn'">
        {{ t("traffic.connections") }} ({{ connCount }})
      </button>
      <button role="tab" :class="{ active: tab === 'logs' }" :aria-selected="tab === 'logs'" @click="tab = 'logs'">
        {{ t("traffic.liveLogs") }}
      </button>
    </div>

    <!-- Totals + close-all strip (connections panel only) -->
    <div v-if="tab === 'conn'" class="conn-totals">
      <span class="totals">
        <span class="down">↓ {{ fmtBytes(store.connections?.downloadTotal) }}</span>
        ·
        <span class="up">↑ {{ fmtBytes(store.connections?.uploadTotal) }}</span>
      </span>
      <button class="btn sm" :disabled="closing" @click="closeAll()">
        {{ t("traffic.closeAll") }}
      </button>
    </div>

    <Connections v-show="tab === 'conn'" />
    <Logs v-show="tab === 'logs'" />
  </div>
</template>
