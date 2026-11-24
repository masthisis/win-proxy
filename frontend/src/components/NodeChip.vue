<script setup lang="ts">
/** One proxy node inside a group grid: name, protocol type, latency badge.
 * Right-click shares the node as a link/QR (#102). */
import { computed } from "vue";
import { fmtLatency } from "../lib/format";
import { t } from "../lib/i18n";

export interface NodeChipModel {
  name: string;
  type: string;
  alive?: boolean;
  delay: number;
}

/** Where a context menu was requested (viewport coordinates). */
export interface ChipMenu {
  node: NodeChipModel;
  x: number;
  y: number;
}

const props = withDefaults(
  defineProps<{
    node: NodeChipModel;
    selected?: boolean;
    interactive?: boolean;
    busy?: boolean;
    shareable?: boolean;
    /** Right-click offers "Route via…" too (#107). */
    routeable?: boolean;
  }>(),
  { selected: false, interactive: false, busy: false, shareable: false, routeable: false },
);

const emit = defineEmits<{
  (e: "select", node: NodeChipModel): void;
  (e: "share", node: NodeChipModel): void;
  (e: "menu", ev: ChipMenu): void;
}>();

const latCls = computed(() => {
  const d = props.node.delay;
  if (d > 0 && d < 200) return "good";
  if (d >= 200 && d < 500) return "mid";
  if (d >= 500) return "bad";
  return "none";
});

function onClick(): void {
  if (props.interactive && !props.busy) emit("select", props.node);
}

function onContextmenu(ev: MouseEvent): void {
  if (!props.shareable) return;
  ev.preventDefault();
  emit("menu", { node: props.node, x: ev.clientX, y: ev.clientY });
}

const shareHint = computed(() => t("dash.shareHint"));

/** Chain-derived entries (#107) are named "<landing> via <front>". */
const isChain = computed(() => props.node.name.includes(" via "));
</script>

<template>
  <component
    :is="interactive ? 'button' : 'div'"
    class="node-chip"
    :class="{ selected, interactive: interactive && !busy }"
    :aria-pressed="interactive ? selected : undefined"
    :disabled="interactive && busy ? true : undefined"
    @click="onClick"
    @contextmenu="onContextmenu"
    :title="shareable ? node.name + ' — ' + shareHint : node.name"
  >
    <span class="n-name" :title="node.name">{{ node.name }}</span>
    <span class="n-meta">
      <span v-if="isChain" class="n-type chain-tag">{{ t("dash.chainTag") }}</span>
      <span class="n-type">{{ node.type || "—" }}</span>
      <span v-if="busy" class="spinner" style="width: 10px; height: 10px" />
      <span class="latency" :class="latCls">{{ fmtLatency(node.delay) }}</span>
    </span>
  </component>
</template>

<style scoped>
.chain-tag {
  color: var(--accent, #7aa2f7);
}
</style>
