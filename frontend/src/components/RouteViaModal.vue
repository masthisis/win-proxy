<script setup lang="ts">
/** RouteViaModal (#107): pick a front node so the landing node dials
 * through it (mihomo dialer-proxy). Shows the existing chain with a
 * remove action; candidates exclude chain-derived entries and the node
 * itself. */
import { computed, ref, watch } from "vue";
import { AddProxyChain, RemoveProxyChain } from "../../bindings/github.com/neurorift/neurorift/settingsservice.js";
import type { ProxyChain } from "../../bindings/github.com/neurorift/neurorift/internal/settings/models.js";
import { t } from "../lib/i18n";
import { toast, errMsg } from "../lib/toast";
import Modal from "./Modal.vue";

const props = defineProps<{
  open: boolean;
  node: string;
  chains: ProxyChain[];
  candidates: string[];
}>();

const emit = defineEmits<{ (e: "close"): void; (e: "changed"): void }>();

const search = ref("");
const busy = ref(false);

watch(
  () => props.open,
  (o) => {
    if (o) search.value = "";
  },
);

const current = computed(() => props.chains.find((c) => c.node === props.node) ?? null);

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase();
  return props.candidates
    .filter((n) => n !== props.node && !n.includes(" via ") && !n.startsWith("via: "))
    .filter((n) => (q ? n.toLowerCase().includes(q) : true))
    .slice(0, 200);
});

async function create(via: string): Promise<void> {
  if (busy.value) return;
  busy.value = true;
  try {
    await AddProxyChain(props.node, via);
    toast.success(t("dash.chainCreated"));
    emit("changed");
    emit("close");
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    busy.value = false;
  }
}

async function removeChain(): Promise<void> {
  if (busy.value || !current.value) return;
  busy.value = true;
  try {
    await RemoveProxyChain(current.value.id);
    toast.success(t("dash.chainRemoved"));
    emit("changed");
  } catch (e) {
    toast.error(errMsg(e));
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <Modal :open="open" :title="t('dash.routeViaTitle', { node })" @close="emit('close')">
    <div class="route-via">
      <p class="dim small" style="margin: 0 0 10px">{{ t("dash.routeViaDesc") }}</p>

      <div v-if="current" class="chain-current">
        <span class="dim small">{{ t("dash.routeViaCurrent") }}:</span>
        <span class="mono small">{{ current.via }}</span>
        <button class="btn danger sm" :disabled="busy" @click="removeChain()">
          {{ t("dash.routeViaRemove") }}
        </button>
      </div>
      <p v-else class="dim small">{{ t("dash.routeViaNone") }}</p>

      <input
        v-model="search"
        type="text"
        class="node-search"
        style="width: 100%"
        :placeholder="t('test.searchNodes')"
      />
      <div class="candidate-list">
        <button
          v-for="n in filtered"
          :key="n"
          class="candidate"
          :disabled="busy"
          @click="create(n)"
        >
          {{ n }}
        </button>
        <p v-if="!filtered.length" class="dim small" style="padding: 10px">
          {{ t("dash.routeViaNone") }}
        </p>
      </div>
    </div>
    <template #actions>
      <button class="btn ghost" @click="emit('close')">{{ t("common.close") }}</button>
    </template>
  </Modal>
</template>

<style scoped>
.route-via {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 320px;
  max-width: 420px;
}
.chain-current {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.chain-current .btn {
  margin-left: auto;
}
.candidate-list {
  margin-top: 10px;
  overflow-y: auto;
  max-height: 260px;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.candidate {
  display: block;
  width: 100%;
  text-align: left;
  padding: 8px 12px;
  background: none;
  border: none;
  border-bottom: 1px solid var(--border);
  color: var(--text);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
}
.candidate:hover {
  background: rgba(128, 128, 128, 0.08);
}
.candidate:last-child {
  border-bottom: none;
}
</style>
