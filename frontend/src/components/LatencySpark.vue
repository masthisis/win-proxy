<script setup lang="ts">
/** Tiny latency sparkline (issue #12): recent delay samples as a polyline. */
import { computed } from "vue";

const props = withDefaults(
  defineProps<{
    samples: number[];
    width?: number;
    height?: number;
  }>(),
  { width: 64, height: 18 },
);

const points = computed<string>(() => {
  const s = props.samples;
  const n = s.length;
  if (n < 2) return "";
  const max = Math.max(...s);
  const min = Math.min(...s);
  const span = Math.max(1, max - min);
  const step = props.width / (n - 1);
  return s
    .map((v, i) => `${(i * step).toFixed(1)},${(props.height - 2 - ((v - min) / span) * (props.height - 4)).toFixed(1)}`)
    .join(" ");
});

const trend = computed<"better" | "worse" | "flat" | "none">(() => {
  const s = props.samples;
  if (s.length < 4) return "none";
  const half = Math.floor(s.length / 2);
  const older = s.slice(0, half).reduce((a, b) => a + b, 0) / half;
  const newer = s.slice(half).reduce((a, b) => a + b, 0) / (s.length - half);
  if (newer < older * 0.85) return "better";
  if (newer > older * 1.15) return "worse";
  return "flat";
});
</script>

<template>
  <svg
    class="lat-spark"
    :width="width"
    :height="height"
    :viewBox="`0 0 ${width} ${height}`"
    role="img"
    aria-hidden="true"
    :title="samples.join(' → ')"
  >
    <polyline
      v-if="points"
      :points="points"
      fill="none"
      stroke-width="1.3"
      :class="trend"
    />
  </svg>
</template>

<style scoped>
.lat-spark polyline.better {
  stroke: var(--success);
}
.lat-spark polyline.flat {
  stroke: var(--text-dim);
}
.lat-spark polyline.worse {
  stroke: var(--warn);
}
.lat-spark polyline.none {
  stroke: var(--text-dim);
}
</style>
