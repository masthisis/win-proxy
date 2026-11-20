<script setup lang="ts">
/** One proxy node inside a group grid: name, protocol type, latency badge. */
import { computed } from "vue";
import { fmtLatency } from "../lib/format";

export interface NodeChipModel {
  name: string;
  type: string;
  alive?: boolean;
  delay: number;
}

const props = withDefaults(
  defineProps<{
    node: NodeChipModel;
    selected?: boolean;
    interactive?: boolean;
    busy?: boolean;
  }>(),
  { selected: false, interactive: false, busy: false },
);

const emit = defineEmits<{ (e: "select", node: NodeChipModel): void }>();

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
</script>

<template>
  <component
    :is="interactive ? 'button' : 'div'"
    class="node-chip"
    :class="{ selected, interactive: interactive && !busy }"
    :aria-pressed="interactive ? selected : undefined"
    :disabled="interactive && busy ? true : undefined"
    @click="onClick"
  >
    <span class="n-name" :title="node.name">{{ node.name }}</span>
    <span class="n-meta">
      <span class="n-type">{{ node.type || "—" }}</span>
      <span v-if="busy" class="spinner" style="width: 10px; height: 10px" />
      <span class="latency" :class="latCls">{{ fmtLatency(node.delay) }}</span>
    </span>
  </component>
</template>
